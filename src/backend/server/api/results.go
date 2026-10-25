// ResultsController ports upstream ResultsController.cs: the torznab,
// potato and manual-search actions with the RequiresApiKey /
// RequiresConfiguredIndexer / RequiresValidQuery filter chain semantics.
//
// Ported from: Jackett.Server/Controllers/ResultsController.cs.
package api

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"

	"vestt/common/indexer"
	"vestt/common/models"
	"vestt/common/services"
	"vestt/common/torznab"
	"vestt/common/utils/parseutil"
	"vestt/common/utils/stringutil"
)

// ResultsHandlers serves /api/v2.0/indexers/{indexerId}/results*.
type ResultsHandlers struct {
	Config  *services.ConfigService
	Manager *services.IndexerManagerService
	Logger  *slog.Logger
}

func (h *ResultsHandlers) logger() *slog.Logger {
	if h.Logger != nil {
		return h.Logger
	}
	return slog.Default()
}

// Routes registers the result endpoints (upstream controller routes).
func (h *ResultsHandlers) Routes(r chi.Router) {
	r.Route("/api/v2.0/indexers/{indexerId}/results", func(r chi.Router) {
		r.Get("/", h.ManualSearch)
		// upstream Route("[action]/{ignored?}") accepts both torznab and
		// torznab/ (and any single trailing segment) — chi needs each
		// spelling registered explicitly.
		r.Get("/torznab", h.Torznab)
		r.Get("/torznab/", h.Torznab)
		r.Get("/torznab/{ignored}", h.Torznab)
		r.Get("/potato", h.Potato)
		r.Get("/potato/", h.Potato)
		r.Get("/potato/{ignored}", h.Potato)
	})
}

// apiKey mirrors RequiresApiKey: query apikey|passkey must match.
func (h *ResultsHandlers) apiKey(r *http.Request) string {
	key := r.URL.Query().Get("apikey")
	if key == "" {
		key = r.URL.Query().Get("passkey")
	}
	return key
}

// requireApiKey enforces RequiresApiKey. isTorznab selects the error
// rendering (XML@200 vs bare status).
func (h *ResultsHandlers) requireApiKey(w http.ResponseWriter, r *http.Request, isTorznab bool) bool {
	if h.apiKey(r) != h.Config.Config().APIKey {
		errorAction(w, isTorznab, http.StatusUnauthorized, 100, "Invalid API Key")
		return false
	}
	return true
}

// requireConfiguredIndexer enforces RequiresConfiguredIndexer. Unknown
// indexers error like upstream GetWebIndexer ("Unknown indexer: X" → 500);
// unconfigured indexers get the 201 XML/code semantics.
func (h *ResultsHandlers) requireConfiguredIndexer(w http.ResponseWriter, r *http.Request, isTorznab bool) (indexer.Indexer, bool) {
	id := chi.URLParam(r, "indexerId")
	if strings.TrimSpace(id) == "" {
		errorAction(w, isTorznab, http.StatusNotFound, 201, "Indexer is not specified (empty value)")
		return nil, false
	}
	idx, err := h.Manager.GetIndexer(id)
	if err != nil {
		// upstream GetIndexer throws "Unknown indexer: <name>" → 500
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return nil, false
	}
	if !idx.IsConfigured() {
		errorAction(w, isTorznab, http.StatusNotFound, 201, "Indexer is not configured")
		return nil, false
	}
	return idx, true
}

// requireValidQuery enforces RequiresValidQuery (skipped for manual search,
// where ApiSearch queries bypass the capability check upstream).
func (h *ResultsHandlers) requireValidQuery(w http.ResponseWriter, isTorznab bool, idx indexer.Indexer, q *models.TorznabQuery) bool {
	if !idx.CanHandleQuery(q) {
		errorAction(w, isTorznab, http.StatusBadRequest, 201,
			fmt.Sprintf("%s does not support the requested query. Please check the capabilities (t=caps) and make sure the search mode and parameters are supported.", idx.ID()))
		return false
	}
	return true
}

// --- Torznab --------------------------------------------------------------

// Torznab serves GET .../results/torznab/{ignored?}.
func (h *ResultsHandlers) Torznab(w http.ResponseWriter, r *http.Request) {
	noStore(w)
	if !h.requireApiKey(w, r, true) {
		return
	}
	idx, ok := h.requireConfiguredIndexer(w, r, true)
	if !ok {
		return
	}

	q := models.ParseTorznabQuery(r.URL.Query())
	q.ApiKey = h.apiKey(r)
	if !h.requireValidQuery(w, true, idx, q) {
		return
	}

	switch q.QueryType {
	case "caps":
		w.Header().Set("Content-Type", "application/rss+xml")
		torznab.WriteCaps(w, idx.Capabilities())
		return
	case "indexers":
		h.torznabIndexers(w, r, idx)
		return
	}

	if q.ImdbID != "" {
		q.ImdbID = parseutil.GetFullImdbID(q.ImdbID)
		if q.ImdbID == "" {
			h.logger().Warn(fmt.Sprintf("A search request from %s was made with an invalid imdbid.", r.RemoteAddr))
			h.errorXML(w, 201, "Incorrect parameter: invalid imdbid format", http.StatusOK)
			return
		}
		caps := idx.Capabilities()
		if q.IsMovieSearch() && !containsString(caps.MovieSearchParams, models.MovieParamImdbID) {
			h.logger().Warn(fmt.Sprintf("A search request with imdbid from %s was made but the indexer %s doesn't support it.", r.RemoteAddr, idx.Name()))
			h.errorXML(w, 203, "Function Not Available: imdbid is not supported for movie search by this indexer", http.StatusOK)
			return
		}
		if q.IsTVSearch() && !caps.TVSearchImdbAvailable {
			h.logger().Warn(fmt.Sprintf("A search request with imdbid from %s was made but the indexer %s doesn't support it.", r.RemoteAddr, idx.Name()))
			h.errorXML(w, 203, "Function Not Available: imdbid is not supported for TV search by this indexer", http.StatusOK)
			return
		}
	}
	if q.TmdbID != nil {
		caps := idx.Capabilities()
		if q.IsMovieSearch() && !containsString(caps.MovieSearchParams, models.MovieParamTmdbID) {
			h.errorXML(w, 203, "Function Not Available: tmdbid is not supported for movie search by this indexer", http.StatusOK)
			return
		}
		if q.IsTVSearch() && !containsString(caps.TVSearchParams, models.TVParamTmdbID) {
			h.errorXML(w, 203, "Function Not Available: tmdbid is not supported for TV search by this indexer", http.StatusOK)
			return
		}
	}
	if q.TvdbID != nil {
		if q.IsTVSearch() && len(idx.Capabilities().TVSearchParams) == 0 {
			h.errorXML(w, 203, "Function Not Available: tvdbid is not supported for movie search by this indexer", http.StatusOK)
			return
		}
	}

	stopwatch := time.Now()
	result, err := idx.ResultsForQuery(r.Context(), q, false)
	if err != nil {
		var ie *indexer.IndexerError
		if errors.As(err, &ie) && ie.Kind == indexer.KindRateLimit {
			h.logger().Error(err.Error())
			retry := ie.RetryAfter
			if retry == 0 {
				retry = time.Minute
			}
			if seconds := int(retry.Seconds()); seconds > 0 {
				w.Header().Set("Retry-After", strconv.Itoa(seconds))
			}
			h.errorXML(w, 900, ie.Error(), http.StatusTooManyRequests)
			return
		}
		var tm *indexer.TooManyRequestsError
		if errors.As(err, &tm) {
			h.logger().Error(err.Error())
			retry := tm.RetryAfter
			if retry == 0 {
				retry = time.Minute
			}
			if seconds := int(retry.Seconds()); seconds > 0 {
				w.Header().Set("Retry-After", strconv.Itoa(seconds))
			}
			h.errorXML(w, 900, tm.Error(), http.StatusTooManyRequests)
			return
		}
		h.logger().Error(err.Error())
		h.errorXML(w, 900, err.Error(), http.StatusBadRequest)
		return
	}

	serverURL := GetServerUrl(h.Config, r)
	proxied := make([]*models.ReleaseInfo, len(result.Releases))
	for i, rel := range result.Releases {
		clone := rel.Clone()
		clone.Link = ConvertToProxyLink(h.Config, clone.Link, serverURL, clone.Origin.ID, "dl", clone.Title)
		clone.Poster = ConvertToProxyLink(h.Config, clone.Poster, serverURL, clone.Origin.ID, "img", "poster")
		proxied[i] = clone
	}

	elapsed := time.Since(stopwatch).Milliseconds()
	cacheStr := ""
	if result.IsFromCache {
		cacheStr = " (from cache)"
	}
	if strings.TrimSpace(q.SanitizedSearchTerm()) == "" {
		h.logger().Info(fmt.Sprintf("Torznab search in %s => Found %d releases%s [%dms]", idx.Name(), len(result.Releases), cacheStr, elapsed))
	} else {
		h.logger().Info(fmt.Sprintf("Torznab search in %s for %s => Found %d releases%s [%dms]", idx.Name(), q.GetQueryString(), len(result.Releases), cacheStr, elapsed))
	}

	ch := torznab.NewChannelInfo()
	ch.Title = idx.Name()
	ch.Description = idx.Description()
	if link, err := urlParse(idx.SiteLink()); err == nil {
		ch.Link = link
	}
	w.Header().Set("Content-Type", "application/rss+xml")
	torznab.WriteRSS(w, ch, proxied, mustParseURL(serverURL))
}

// torznabIndexers serves t=indexers (meta indexers only).
func (h *ResultsHandlers) torznabIndexers(w http.ResponseWriter, r *http.Request, idx indexer.Indexer) {
	metaIdx, ok := idx.(interface {
		Members() []indexer.Indexer
	})
	if !ok {
		h.logger().Warn(fmt.Sprintf("A search request with t=indexers from %s was made but the indexer %s isn't a meta indexer.", r.RemoteAddr, idx.Name()))
		h.errorXML(w, 203, "Function Not Available: this isn't a meta indexer", http.StatusOK)
		return
	}
	indexers := metaIdx.Members()
	configured := r.URL.Query().Get("configured")
	if strings.EqualFold(configured, "true") {
		var filtered []indexer.Indexer
		for _, i := range indexers {
			if i.IsConfigured() {
				filtered = append(filtered, i)
			}
		}
		indexers = filtered
	} else if strings.EqualFold(configured, "false") {
		var filtered []indexer.Indexer
		for _, i := range indexers {
			if !i.IsConfigured() {
				filtered = append(filtered, i)
			}
		}
		indexers = filtered
	}

	entries := make([]torznab.IndexerEntry, 0, len(indexers))
	for _, i := range indexers {
		entries = append(entries, torznab.IndexerEntry{
			ID:          i.ID(),
			Configured:  i.IsConfigured(),
			Title:       i.Name(),
			Description: i.Description(),
			Link:        i.SiteLink(),
			Language:    i.Language(),
			Type:        i.Type(),
			Caps:        i.Capabilities(),
		})
	}
	w.Header().Set("Content-Type", "application/xml")
	torznab.WriteIndexers(w, entries)
}

// errorXML writes an XML error document (upstream GetErrorXML).
func (h *ResultsHandlers) errorXML(w http.ResponseWriter, code int, description string, status int) {
	w.Header().Set("Content-Type", "application/xml")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(torznab.ErrorXMLString(code, description)))
}

// --- manual search --------------------------------------------------------

// metaIndexers is the meta-indexer surface used by the manual search
// (upstream BaseMetaIndexer.ValidIndexers).
type metaIndexers interface {
	ValidIndexers() []indexer.Indexer
}

// manualTask is one fan-out task's outcome.
type manualTask struct {
	idx    indexer.Indexer
	result *indexer.IndexerResult
	err    error
}

// ManualSearch serves GET .../results (upstream Results action).
func (h *ResultsHandlers) ManualSearch(w http.ResponseWriter, r *http.Request) {
	noStore(w)
	if !h.requireApiKey(w, r, false) {
		return
	}
	current, ok := h.requireConfiguredIndexer(w, r, false)
	if !ok {
		return
	}

	stopwatch := time.Now()
	q := apiSearchToTorznabQuery(r.URL.Query())

	var trackers []indexer.Indexer
	if meta, isMeta := current.(metaIndexers); isMeta {
		trackers = meta.ValidIndexers()
	} else {
		trackers = []indexer.Indexer{current}
	}

	trackerFilter := splitCSV(r.URL.Query().Get("Tracker[]"))
	if trackerFilter != nil {
		filtered := trackers[:0]
		for _, t := range trackers {
			if containsString(trackerFilter, t.ID()) {
				filtered = append(filtered, t)
			}
		}
		trackers = filtered
	}
	filtered := trackers[:0]
	for _, t := range trackers {
		if t.CanHandleQuery(q) {
			filtered = append(filtered, t)
		}
	}
	trackers = filtered

	q.InteractiveSearch = true
	isMetaIndexer := trackerFilter == nil || len(trackerFilter) > 1

	tasks := make([]manualTask, len(trackers))
	var wg sync.WaitGroup
	for i, t := range trackers {
		wg.Add(1)
		go func(i int, t indexer.Indexer) {
			defer wg.Done()
			res, err := t.ResultsForQuery(r.Context(), q, isMetaIndexer)
			tasks[i] = manualTask{idx: t, result: res, err: err}
		}(i, t)
	}
	wg.Wait()

	indexerResults := make([]ManualSearchResultIndexer, len(tasks))
	for i, t := range tasks {
		entry := ManualSearchResultIndexer{
			ID:   t.idx.ID(),
			Name: t.idx.Name(),
		}
		if t.err == nil {
			entry.Status = manualStatusOK
			entry.Results = len(t.result.Releases)
			entry.ElapsedTime = t.result.ElapsedTime
			entry.Error = nil
		} else {
			var ie *indexer.IndexerError
			if errors.As(t.err, &ie) {
				entry.Status = manualStatusError
				entry.Error = stringPtr(ie.Error())
			} else {
				entry.Status = manualStatusUnknown
			}
		}
		indexerResults[i] = entry
	}

	results := []models.TrackerCacheResult{}
	for _, t := range tasks {
		if t.err != nil || t.result == nil || len(t.result.Releases) == 0 {
			continue
		}
		origin := t.result.Indexer
		if origin == nil {
			origin = t.idx
		}
		for _, rel := range t.result.Releases {
			tcr := models.NewTrackerCacheResult(rel)
			tcr.Tracker = origin.Name()
			tcr.TrackerId = origin.ID()
			tcr.TrackerType = origin.Type()
			results = append(results, *tcr)
		}
	}
	sort.SliceStable(results, func(i, j int) bool {
		return results[i].PublishDate.After(results[j].PublishDate)
	})

	h.configureCacheResults(r, results)

	fromCache := false
	for _, t := range tasks {
		if t.err == nil && t.result != nil && t.result.IsFromCache {
			fromCache = true
		}
	}
	names := make([]string, 0, len(indexerResults))
	for _, e := range indexerResults {
		names = append(names, e.Name)
	}
	sort.Slice(names, func(i, j int) bool { return strings.ToLower(names[i]) < strings.ToLower(names[j]) })
	cacheStr := ""
	if fromCache {
		cacheStr = " (from cache)"
	}
	elapsed := time.Since(stopwatch).Milliseconds()
	if strings.TrimSpace(q.SanitizedSearchTerm()) == "" {
		h.logger().Info(fmt.Sprintf("Manual search in %s => Found %d releases%s [%dms]", strings.Join(names, ", "), len(results), cacheStr, elapsed))
	} else {
		h.logger().Info(fmt.Sprintf("Manual search in %s for %s => Found %d releases%s [%dms]", strings.Join(names, ", "), q.GetQueryString(), len(results), cacheStr, elapsed))
	}

	writeJSON(w, http.StatusOK, ManualSearchResult{
		Results:  results,
		Indexers: indexerResults,
	})
}

// configureCacheResults ports upstream ConfigureCacheResults (manual search
// variant): proxy Link/Poster, and BlackholeLink from the ORIGINAL link
// (or the magnet) when a blackhole directory is configured.
func (h *ResultsHandlers) configureCacheResults(r *http.Request, results []models.TrackerCacheResult) {
	serverURL := GetServerUrl(h.Config, r)
	blackhole := h.Config.Config().BlackholeDir != ""
	for i := range results {
		res := &results[i]
		link := res.Link
		file := stringutil.MakeValidFileName(res.Title, '_', false)
		res.Link = ConvertToProxyLink(h.Config, link, serverURL, res.TrackerId, "dl", file)
		res.Poster = ConvertToProxyLink(h.Config, res.Poster, serverURL, res.TrackerId, "img", "poster")
		if blackhole {
			if link != nil {
				res.BlackholeLink = ConvertToProxyLink(h.Config, link, serverURL, res.TrackerId, "bh", file)
			} else if res.MagnetUri != nil {
				res.BlackholeLink = ConvertToProxyLink(h.Config, res.MagnetUri, serverURL, res.TrackerId, "bh", file)
			}
		}
	}
}

// apiSearchToTorznabQuery ports upstream ApiSearch.ToTorznabQuery plus the
// controller's Tracker[]/Category[]/query parsing.
func apiSearchToTorznabQuery(v map[string][]string) *models.TorznabQuery {
	stringQuery := models.NewTorznabQuery()
	stringQuery.QueryType = "search"

	queryStr := strings.TrimSpace(firstValue(v, "query"))
	if queryStr != "" {
		if m := seasonEpisodeRe.FindStringSubmatchIndex(queryStr); m != nil {
			stringQuery.Season = intPtr(mustAtoi(queryStr[m[2]:m[3]]))
			stringQuery.Episode = strings.TrimLeft(queryStr[m[4]:m[5]], "0")
			queryStr = strings.TrimSpace(queryStr[:m[0]] + queryStr[m[1]:])
		} else {
			if m := episodeRe.FindStringSubmatchIndex(queryStr); m != nil {
				stringQuery.Episode = strings.TrimLeft(queryStr[m[2]:m[3]], "0")
				queryStr = strings.TrimSpace(queryStr[:m[0]] + queryStr[m[1]:])
			}
			if m := seasonRe.FindStringSubmatchIndex(queryStr); m != nil {
				stringQuery.Season = intPtr(mustAtoi(queryStr[m[2]:m[3]]))
				queryStr = strings.TrimSpace(queryStr[:m[0]] + queryStr[m[1]:])
			}
		}
		queryStr = strings.TrimSpace(queryStr)
	}
	stringQuery.SearchTerm = queryStr

	if cats := splitCSV(firstValue(v, "Category[]")); cats != nil {
		ids := make([]int, 0, len(cats))
		for _, c := range cats {
			ids = append(ids, mustAtoi(c))
		}
		stringQuery.Categories = ids
	}
	if s := firstValue(v, "imdbid"); s != "" {
		stringQuery.ImdbID = s
	}
	if s := firstValue(v, "tmdbid"); s != "" {
		stringQuery.TmdbID = intPtr(mustAtoi(s))
	}
	if s := firstValue(v, "tvdbid"); s != "" {
		stringQuery.TvdbID = intPtr(mustAtoi(s))
	}
	if s := firstValue(v, "tvmazeid"); s != "" {
		stringQuery.TvmazeID = intPtr(mustAtoi(s))
	}

	// a search term shaped like tt + 6-8 digits becomes an IMDb query
	term := stringQuery.SanitizedSearchTerm()
	if strings.HasPrefix(term, "tt") && len(term) <= 10 {
		if imdbID := parseutil.GetFullImdbID(term); imdbID != "" {
			return &models.TorznabQuery{
				QueryType:  "search",
				ImdbID:     imdbID,
				Categories: stringQuery.Categories,
				Cache:      true,
				Season:     stringQuery.Season,
				Episode:    stringQuery.Episode,
			}
		}
	}
	return stringQuery
}

// --- potato ---------------------------------------------------------------

// Potato serves GET .../results/potato/{ignored?}.
func (h *ResultsHandlers) Potato(w http.ResponseWriter, r *http.Request) {
	noStore(w)
	if !h.requireApiKey(w, r, false) {
		return
	}
	idx, ok := h.requireConfiguredIndexer(w, r, false)
	if !ok {
		return
	}

	q := models.NewTorznabQuery()
	q.QueryType = "TorrentPotato"
	q.Categories = []int{models.CatMovies.ID}
	q.SearchTerm = r.URL.Query().Get("search")
	q.ImdbID = r.URL.Query().Get("imdbid")
	if !h.requireValidQuery(w, false, idx, q) {
		return
	}

	result, err := idx.ResultsForQuery(r.Context(), q, false)
	if err != nil {
		// upstream potato action has no error mapping: failures surface as
		// a 500 (unhandled exception)
		h.logger().Error(err.Error())
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	cacheStr := ""
	if result.IsFromCache {
		cacheStr = " (from cache)"
	}
	if strings.TrimSpace(q.SanitizedSearchTerm()) == "" {
		h.logger().Info(fmt.Sprintf("Potato search in %s => Found %d releases%s", idx.Name(), len(result.Releases), cacheStr))
	} else {
		h.logger().Info(fmt.Sprintf("Potato search in %s for %s => Found %d releases%s", idx.Name(), q.GetQueryString(), len(result.Releases), cacheStr))
	}

	serverURL := GetServerUrl(h.Config, r)
	items := []TorrentPotatoResponseItem{}
	for _, rel := range result.Releases {
		if rel.Link == nil && rel.MagnetUri == nil {
			continue
		}
		// upstream proxies with CurrentIndexer.Id (not the release origin)
		rel.Link = ConvertToProxyLink(h.Config, rel.Link, serverURL, idx.ID(), "dl", rel.Title)
		item := TorrentPotatoResponseItem{
			ReleaseName: rel.Title + "[" + idx.Name() + "]",
			Type:        "movie",
		}
		if rel.Guid != nil {
			item.TorrentID = rel.Guid.String()
		}
		if rel.Details != nil {
			item.DetailsURL = rel.Details.String()
		}
		if rel.Link != nil {
			item.DownloadURL = rel.Link.String()
		} else if rel.MagnetUri != nil {
			item.DownloadURL = rel.MagnetUri.String()
		}
		if rel.Imdb != nil {
			item.ImdbID = parseutil.GetFullImdbID("tt" + strconv.FormatInt(*rel.Imdb, 10))
		}
		if rel.DownloadVolumeFactor != nil && *rel.DownloadVolumeFactor == 0 {
			item.Freeleech = true
		}
		if rel.Size != nil {
			item.Size = *rel.Size / (1024 * 1024)
		}
		peers := int64(-1)
		if rel.Peers != nil {
			peers = *rel.Peers
		}
		seeders := int64(-1)
		if rel.Seeders != nil {
			seeders = *rel.Seeders
		}
		item.Leechers = peers - seeders
		item.Seeders = seeders
		if !rel.PublishDate.IsZero() {
			item.PublishDate = rel.PublishDate.UTC().Format("2006-01-02T15:04:05")
		}
		items = append(items, item)
	}

	writeJSON(w, http.StatusOK, TorrentPotatoResponse{
		Results:      items,
		TotalResults: len(items),
	})
}
