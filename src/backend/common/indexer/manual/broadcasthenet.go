// Package manual holds the hand-written indexers (one file per site).
package manual

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"vestt/common/httpclient"
	"vestt/common/indexer"
	"vestt/common/indexer/configdata"
	"vestt/common/models"
	"vestt/common/services"
	"vestt/common/utils/dateutil"
	"vestt/common/utils/parseutil"
)

// BroadcasTheNet ports Jackett.Common/Indexers/Definitions/BroadcasTheNet.cs.
//
// mechanism: json-api (JSON-RPC 2.0)
//
// Ported from: Definitions/BroadcasTheNet.cs,
// Models/IndexerConfig/ConfigurationDataAPIKey.cs.
//
// 澄清记录:
//   - Upstream 0.24.2900 extends IndexerBase; per SPEC-08 tasking this port
//     additionally implements the BaseCachingWebIndexer cache semantics
//     (9-minute TTL, results returned as clones) in-struct via
//     lastRequest/lastResult, short-circuiting repeated identical queries.
//   - apiBase is an upstream const ("https://api.broadcasthe.net"); here it
//     is an overridable unexported field (default identical) so httptest
//     fixtures can point the indexer at a fake API endpoint.
type BroadcasTheNet struct {
	indexer.Base
	cfg     *broadcasthenetConfig
	apiBase string // upstream const ApiBase; test seam

	// lastRequest/lastResult implement the BaseCachingWebIndexer semantics:
	// an identical query short-circuits the repeated API call.
	cacheMu     sync.Mutex
	lastRequest string
	lastResult  []*models.ReleaseInfo
}

type broadcasthenetConfig struct {
	configdata.ConfigData
	Key *configdata.StringItem
}

func newBroadcasthenetConfig() *broadcasthenetConfig {
	return &broadcasthenetConfig{
		ConfigData: *configdata.NewConfigData(),
		Key:        configdata.NewStringItem("APIKey"),
	}
}

// NewBroadcastheNet builds the indexer (services.Factory shape).
func NewBroadcastheNet(deps services.Deps) (indexer.Indexer, error) {
	b := &BroadcasTheNet{cfg: newBroadcasthenetConfig(), apiBase: "https://api.broadcasthe.net"}
	b.Base = indexer.Base{
		IDValue:          "broadcasthenet",
		NameValue:        "BroadcasTheNet",
		DescriptionValue: "BroadcasTheNet (BTN) is a Private invite-only Torrent Tracker focused on TV shows",
		SiteLinkValue:    "https://broadcasthe.net/",
		DefaultSiteLink:  "https://broadcasthe.net/",
		LangValue:        "en-US",
		TypeValue:        "private",
		Caps:             broadcasthenetCaps(),
		Config:           b.cfg,
		Client:           deps.Client,
		Saver:            deps.Saver,
		Cache:            deps.Cache,
	}
	// upstream: webclient.requestDelay = 5 (seconds)
	if deps.Client != nil {
		deps.Client.SetRequestDelay(b.IDValue, 5*time.Second)
	}
	return b, nil
}

func broadcasthenetCaps() *models.Capabilities {
	caps := models.NewCapabilities()
	def, max := 100, 1000
	caps.LimitsDefault = &def
	caps.LimitsMax = &max
	caps.TVSearchParams = []string{models.TVParamQ, models.TVParamSeason, models.TVParamEp, models.TVParamImdbID, models.TVParamTvdbID}
	caps.TVSearchImdbAvailable = true
	c := caps.Categories
	c.AddCategoryMapping("SD", models.CatTVSD, "SD")
	c.AddCategoryMapping("720p", models.CatTVHD, "720p")
	c.AddCategoryMapping("1080p", models.CatTVHD, "1080p")
	c.AddCategoryMapping("1080i", models.CatTVHD, "1080i")
	c.AddCategoryMapping("2160p", models.CatTVUHD, "2160p")
	c.AddCategoryMapping("Portable Device", models.CatTVSD, "Portable Device")
	return caps
}

// ApplyConfiguration ports upstream: test-query the API, mark configured and
// save (upstream wraps failures in ExceptionWithConfigData — the message is
// preserved verbatim).
func (b *BroadcasTheNet) ApplyConfiguration(ctx context.Context, items []indexer.ConfigItem) (indexer.ConfigStatus, error) {
	if err := b.ApplyItems(items); err != nil {
		return indexer.ConfigCompleted, err
	}
	if err := b.LoadValuesFromJSON(nil, false); err != nil {
		return indexer.ConfigCompleted, err
	}
	b.SetConfigured(false)
	results, err := b.perform(ctx, models.NewTorznabQuery())
	if err != nil {
		return indexer.ConfigCompleted, err
	}
	if len(results) == 0 {
		return indexer.ConfigCompleted, errors.New("Testing returned no results!") //nolint:staticcheck // upstream BroadcasTheNet.cs:100 error text, kept 1:1
	}
	b.SetConfigured(true)
	b.SaveConfig(ctx)
	return indexer.ConfigCompleted, nil
}

func (b *BroadcasTheNet) ResultsForQuery(ctx context.Context, q *models.TorznabQuery, isMeta bool) (*indexer.IndexerResult, error) {
	return b.ResultsForQueryWith(ctx, q, isMeta, b.perform)
}

func (b *BroadcasTheNet) Test(ctx context.Context) error {
	return b.Base.Test(ctx, b.perform)
}

func (b *BroadcasTheNet) perform(ctx context.Context, query *models.TorznabQuery) ([]*models.ReleaseInfo, error) {
	parametersList := b.buildSearchQueries(query)
	if len(parametersList) == 0 {
		// e.g. absolute episode number with an ID query: no requests at all
		return []*models.ReleaseInfo{}, nil
	}

	btnResults := query.Limit
	if btnResults == 0 {
		btnResults = 100 // upstream: _torznabCaps.LimitsDefault.GetValueOrDefault(100)
	}
	btnOffset := query.Offset

	// cache key: the full request set (params + pagination), excluding the
	// random JSON-RPC id (upstream Guid.NewGuid().Substring(0, 8)).
	var keyParts []string
	for _, p := range parametersList {
		raw, err := json.Marshal(p)
		if err != nil {
			return nil, err
		}
		keyParts = append(keyParts, string(raw))
	}
	cacheKey := strings.Join(keyParts, "|") + "|" + strconv.Itoa(btnResults) + "|" + strconv.Itoa(btnOffset)

	b.cacheMu.Lock()
	if b.lastRequest == cacheKey && b.lastResult != nil {
		cached := cloneReleases(b.lastResult)
		b.cacheMu.Unlock()
		return cached, nil
	}
	b.cacheMu.Unlock()

	var releases []*models.ReleaseInfo
	for _, parameters := range parametersList {
		body, err := b.jsonRPCRequest("getTorrents", b.cfg.Key.Value, parameters, btnResults, btnOffset)
		if err != nil {
			return nil, err
		}
		if b.Client == nil {
			return nil, errors.New("http client not configured")
		}
		res, err := b.Client.Do(ctx, b.ID(), &httpclient.Request{
			Method: http.MethodPost,
			URL:    b.apiBase,
			Headers: map[string]string{
				"Accept":       "application/json-rpc, application/json",
				"Content-Type": "application/json-rpc",
			},
			RawBody:        body,
			EmulateBrowser: httpclient.Bool(false), // upstream: EmulateBrowser = false
		})
		if err != nil {
			return nil, err
		}
		parsed, err := b.parseResponse(res.Body)
		if err != nil {
			return nil, err
		}
		releases = append(releases, parsed...)
	}

	b.cacheMu.Lock()
	b.lastRequest = cacheKey
	b.lastResult = releases
	b.cacheMu.Unlock()
	return releases, nil
}

// buildSearchQueries ports
// BroadcastheNetRequestGenerator.GetSearchRequests. A season-only search
// produces two query variants (Season pack + Episodes); the absolute-number
// guard produces none.
func (b *BroadcasTheNet) buildSearchQueries(query *models.TorznabQuery) []btnSearchParams {
	searchTerm := query.SearchTerm

	var parameters btnSearchParams
	if query.IsTvdbQuery() && query.TvdbID != nil {
		parameters.Tvdb = strconv.Itoa(*query.TvdbID)
	} else if query.IsImdbQuery() {
		parameters.Imdb = query.ImdbID
	}

	if strings.TrimSpace(searchTerm) != "" {
		parameters.Search = strings.ReplaceAll(searchTerm, " ", "%")
	}

	season := 0
	if query.Season != nil {
		season = *query.Season
	}

	if season > 0 && strings.TrimSpace(query.Episode) == "" {
		// Search Season
		seasonParams := parameters
		seasonParams.Category = "Season"
		seasonParams.Name = fmt.Sprintf("Season %d%%", season)

		// Search Episode
		episodeParams := parameters
		episodeParams.Category = "Episode"
		episodeParams.Name = fmt.Sprintf("S%02dE%%", season)

		return []btnSearchParams{seasonParams, episodeParams}
	} else if showDate, ok := parseBTNAirDate(season, query.Episode); ok {
		// Daily Episode
		parameters.Name = showDate.Format("2006.01.02")
		parameters.Category = "Episode"
		return []btnSearchParams{parameters}
	} else if season > 0 {
		if episode, err := strconv.Atoi(strings.TrimSpace(query.Episode)); err == nil && episode > 0 {
			// Standard (S/E) Episode
			if episode < 100 {
				parameters.Name = fmt.Sprintf("S%02dE%%%02d%%", season, episode)
			} else {
				parameters.Name = fmt.Sprintf("S%02dE%02d%%", season, episode)
			}
			parameters.Category = "Episode"
			return []btnSearchParams{parameters}
		}
	}

	if strings.TrimSpace(searchTerm) != "" {
		if _, err := strconv.Atoi(strings.TrimSpace(searchTerm)); err == nil && (query.TvdbID != nil && *query.TvdbID > 0 || strings.TrimSpace(query.ImdbID) != "") {
			// Disable ID-based searches for episodes with absolute episode number
			return nil
		}
	}

	// Neither a season only search nor daily nor standard, fall back to query
	return []btnSearchParams{parameters}
}

// jsonRPCRequest ports the upstream dynamic JObject construction
// ({"jsonrpc":"2.0","method":...,"params":[apikey, parameters, results,
// offset],"id": <8 hex chars from Guid>}).
func (b *BroadcasTheNet) jsonRPCRequest(method, apiKey string, parameters btnSearchParams, results, offset int) (string, error) {
	idBytes := make([]byte, 4)
	if _, err := rand.Read(idBytes); err != nil {
		return "", err
	}
	id := hex.EncodeToString(idBytes) // upstream: Guid.NewGuid().ToString().Substring(0, 8)
	req := btnJSONRPCRequest{
		JSONRPC: "2.0",
		Method:  method,
		Params:  []any{apiKey, parameters, results, offset},
		ID:      id,
	}
	raw, err := json.Marshal(req)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

// parseResponse ports BroadcastheNetParser.ParseResponse.
func (b *BroadcasTheNet) parseResponse(body []byte) ([]*models.ReleaseInfo, error) {
	var jsonResponse btnResponse
	if err := json.Unmarshal(body, &jsonResponse); err != nil {
		return nil, err
	}
	// Newtonsoft leaves the JToken property null for a JSON null; a JSON
	// null "error" member is therefore not an error.
	if jsonResponse.Error != nil && string(jsonResponse.Error) != "null" {
		return nil, fmt.Errorf("Indexer API call returned an error [%s]", btnErrorString(jsonResponse.Error)) //nolint:staticcheck // upstream BroadcasTheNet.cs error text, kept 1:1
	}
	if jsonResponse.Result == nil || jsonResponse.Result.Torrents == nil {
		return []*models.ReleaseInfo{}, nil
	}

	// deterministic order: upstream iterates a Dictionary<int, ...> whose
	// JSON keys arrive sorted; sort the ids ascending.
	ids := make([]int, 0, len(jsonResponse.Result.Torrents))
	for id := range jsonResponse.Result.Torrents {
		ids = append(ids, id)
	}
	sortInts(ids)

	var releases []*models.ReleaseInfo
	for _, id := range ids {
		btnResult := jsonResponse.Result.Torrents[id]
		if btnResult == nil {
			continue
		}
		releases = append(releases, b.mapTorrent(btnResult))
	}
	return releases, nil
}

func (b *BroadcasTheNet) mapTorrent(t *btnTorrent) *models.ReleaseInfo {
	var descriptions []string
	if strings.TrimSpace(t.Series) != "" {
		descriptions = append(descriptions, "Series: "+t.Series)
	}
	if strings.TrimSpace(t.GroupName) != "" {
		descriptions = append(descriptions, "Group Name: "+t.GroupName)
	}
	if strings.TrimSpace(t.Source) != "" {
		descriptions = append(descriptions, "Source: "+t.Source)
	}
	if strings.TrimSpace(t.Container) != "" {
		descriptions = append(descriptions, "Container: "+t.Container)
	}
	if strings.TrimSpace(t.Codec) != "" {
		descriptions = append(descriptions, "Codec: "+t.Codec)
	}
	if strings.TrimSpace(t.Resolution) != "" {
		descriptions = append(descriptions, "Resolution: "+t.Resolution)
	}
	if strings.TrimSpace(t.Origin) != "" {
		descriptions = append(descriptions, "Origin: "+t.Origin)
	}
	if strings.TrimSpace(t.YoutubeTrailer) != "" {
		descriptions = append(descriptions, "Youtube Trailer: <a href=\""+t.YoutubeTrailer+"\">"+t.YoutubeTrailer+"</a>")
	}

	imdb, _ := parseutil.GetImdbID(t.ImdbID)
	link, _ := url.Parse(t.DownloadURL)
	details, _ := url.Parse(fmt.Sprintf("%storrents.php?id=%d&torrentid=%d", b.SiteLink(), t.GroupID, t.TorrentID))
	publishDate := dateutil.UnixTimestampToDateTime(t.Time)

	size := t.Size
	grabs := int64(0)
	if t.Snatched != nil {
		grabs = int64(*t.Snatched)
	}
	seeders := int64(0)
	if t.Seeders != nil {
		seeders = int64(*t.Seeders)
	}
	leechers := int64(0)
	if t.Leechers != nil {
		leechers = int64(*t.Leechers)
	}
	peers := seeders + leechers
	dvf := 0.0 // ratioless
	uvf := 1.0
	minRatio := 1.0
	minSeedTime := int64(86400) // 24 hours for episodes
	if strings.ToUpper(t.Category) == "SEASON" {
		minSeedTime = 432000 // 120 hours for seasons
	}

	release := &models.ReleaseInfo{
		Guid:                 link,
		Details:              details,
		Link:                 link,
		Title:                btnGetTitle(t),
		Description:          strings.Join(descriptions, "<br />\n"),
		Category:             b.Caps.Categories.MapTrackerCatToNewznab(t.Resolution),
		InfoHash:             t.InfoHash,
		Size:                 &size,
		Grabs:                &grabs,
		Seeders:              &seeders,
		Peers:                &peers,
		PublishDate:          publishDate,
		Imdb:                 &imdb,
		DownloadVolumeFactor: &dvf,
		UploadVolumeFactor:   &uvf,
		MinimumRatio:         &minRatio,
		MinimumSeedTime:      &minSeedTime,
	}
	if t.TvdbID != nil {
		tvdb := int64(*t.TvdbID)
		release.TVDBId = &tvdb
	}
	if t.TvrageID != nil {
		rage := int64(*t.TvrageID)
		release.RageID = &rage
	}

	if strings.TrimSpace(t.SeriesBanner) != "" {
		posterURL := t.SeriesBanner
		if strings.HasPrefix(posterURL, "//") {
			posterURL = "https:" + posterURL
		}
		if poster, err := url.Parse(posterURL); err == nil {
			release.Poster = poster
		}
	}

	if len(release.Category) == 0 { // default to TV
		release.Category = append(release.Category, models.CatTV.ID)
	}
	return release
}

var (
	btnH265Re = regexp.MustCompile(`\b(H\.?265)\b`)
	btnH264Re = regexp.MustCompile(`\b(H\.?264)\b`)
)

// btnGetTitle ports BroadcastheNetParser.GetTitle.
func btnGetTitle(torrent *btnTorrent) string {
	releaseName := strings.ReplaceAll(torrent.ReleaseName, "\\", "")

	if strings.ToUpper(torrent.Container) == "M2TS" || strings.ToUpper(torrent.Container) == "ISO" {
		releaseName = btnH265Re.ReplaceAllString(releaseName, "HEVC")
		releaseName = btnH264Re.ReplaceAllString(releaseName, "AVC")
	}

	return releaseName
}

// --- helpers ----------------------------------------------------------------

// btnSearchParams mirrors upstream BroadcastheNetSearchQuery
// (DefaultValueHandling.Ignore → omitempty).
type btnSearchParams struct {
	Category string `json:"category,omitempty"`
	Name     string `json:"name,omitempty"`
	Search   string `json:"search,omitempty"`
	Imdb     string `json:"imdb,omitempty"`
	Tvdb     string `json:"tvdb,omitempty"`
	Tvrage   string `json:"tvrage,omitempty"`
}

// btnJSONRPCRequest is the upstream JSON-RPC envelope (member order kept).
type btnJSONRPCRequest struct {
	JSONRPC string `json:"jsonrpc"`
	Method  string `json:"method"`
	Params  []any  `json:"params"`
	ID      string `json:"id"`
}

// btnResponse mirrors upstream BroadcastheNetResponse.
type btnResponse struct {
	ID     string          `json:"id"`
	Result *btnResult      `json:"result"`
	Error  json.RawMessage `json:"error"`
}

type btnResult struct {
	Torrents map[int]*btnTorrent `json:"torrents"`
}

// btnTorrent mirrors upstream BroadcastheNetTorrent.
type btnTorrent struct {
	TorrentID      int    `json:"TorrentID"`
	DownloadURL    string `json:"DownloadURL"`
	GroupName      string `json:"GroupName"`
	GroupID        int    `json:"GroupID"`
	SeriesID       int    `json:"SeriesID"`
	Series         string `json:"Series"`
	SeriesBanner   string `json:"SeriesBanner"`
	SeriesPoster   string `json:"SeriesPoster"`
	YoutubeTrailer string `json:"YoutubeTrailer"`
	Category       string `json:"Category"`
	Snatched       *int   `json:"Snatched"`
	Seeders        *int   `json:"Seeders"`
	Leechers       *int   `json:"Leechers"`
	Source         string `json:"Source"`
	Container      string `json:"Container"`
	Codec          string `json:"Codec"`
	Resolution     string `json:"Resolution"`
	Origin         string `json:"Origin"`
	ReleaseName    string `json:"ReleaseName"`
	Size           int64  `json:"Size"`
	Time           int64  `json:"Time"`
	TvdbID         *int   `json:"TvdbID"`
	TvrageID       *int   `json:"TvrageID"`
	ImdbID         string `json:"ImdbID"`
	InfoHash       string `json:"InfoHash"`
}

// parseBTNAirDate mirrors DateTime.TryParseExact($"{season} {episode}",
// "yyyy MM/dd", InvariantCulture) — season is the 4-digit year of a daily
// show, episode is "MM/dd".
func parseBTNAirDate(season int, episode string) (time.Time, bool) {
	t, err := time.Parse("2006 01/02", fmt.Sprintf("%d %s", season, episode))
	if err != nil {
		return time.Time{}, false
	}
	return t, true
}

// btnErrorString renders the upstream JToken the way C# string interpolation
// does: a plain string token renders as the string, anything else as JSON.
func btnErrorString(raw json.RawMessage) string {
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	var v any
	if err := json.Unmarshal(raw, &v); err == nil {
		if compact, err := json.Marshal(v); err == nil {
			return string(compact)
		}
	}
	return string(raw)
}

func cloneReleases(in []*models.ReleaseInfo) []*models.ReleaseInfo {
	out := make([]*models.ReleaseInfo, 0, len(in))
	for _, r := range in {
		out = append(out, r.Clone())
	}
	return out
}

func sortInts(ints []int) {
	for i := 1; i < len(ints); i++ {
		for j := i; j > 0 && ints[j-1] > ints[j]; j-- {
			ints[j-1], ints[j] = ints[j], ints[j-1]
		}
	}
}
