// mechanism: json-api (username / password / PID bearer token)
//
// Ported from: Abstract/AvistazTracker.cs
//
// Avistaz is the shared base for the AvistaZ family (AvistaZ, CinemaZ,
// PrivateHD, ExoticaZ, AnimeZ). Concrete indexers embed it, set identity/
// Caps via indexer.Base and Bind a NewAvistazConfig. Hook fields mirror the
// upstream virtual methods; nil falls back to the upstream defaults.
//
// 澄清记录:
//   - upstream sets webclient.requestDelay = 6 (seconds) in the
//     constructor; the Go http stack applies delays per indexer at wiring
//     time, so the port exposes AvistazRequestDelay + WireDelay for the
//     factory to call (same value, same effect).
//   - upstream RenewalTokenAsync checks result.Status == 429 explicitly;
//     the Go http stack surfaces 429 as *httpclient.TooManyRequests before
//     a Result exists, so the port re-wraps it with the upstream message
//     "Rate limited".
//   - upstream sends the auth body form-encoded while also setting an
//     (ineffective upstream) Content-Type: application/json header; the Go
//     client overrides Content-Type to form-urlencoded whenever a form body
//     is present, which matches the upstream wire format.
package shared

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"strings"
	"time"

	"vestt/common/httpclient"
	"vestt/common/indexer"
	"vestt/common/indexer/configdata"
	"vestt/common/models"
	"vestt/common/utils/dateutil"
	"vestt/common/utils/parseutil"
)

// AvistazRequestDelay mirrors upstream webclient.requestDelay = 6 (seconds).
const AvistazRequestDelay = 6 * time.Second

// Avistaz instruction message (upstream ConfigurationDataAvistaZTracker).
const avistazInstructions = "You have to check 'Enable RSS Feed' in 'My Account', without this configuration the torrent download does not work.<br/>You can find the PID in 'My profile'."

// AvistazAccountActivity mirrors the upstream Account Inactivity info item.
const AvistazAccountActivity = "To avoid account deletion you must login at least 1 time every 60 days, and you must download at least 1 torrent every 90 days. Simply keeping torrents seeding long term will not protect your account. Do not rely on inactivity emails, we often do not send them."

// AvistazConfig ports upstream ConfigurationDataAvistaZTracker
// (BasicLoginWithPID + Freeleech + AccountActivity).
type AvistazConfig struct {
	configdata.ConfigData
	Username        *configdata.StringItem
	Password        *configdata.StringItem
	Pid             *configdata.StringItem
	Instructions    *configdata.InfoItem
	Freeleech       *configdata.BoolItem
	AccountActivity *configdata.InfoItem
}

// NewAvistazConfig mirrors the upstream constructor
// (instructionMessageOptional defaults to the upstream PID instructions).
func NewAvistazConfig(instructionMessage string) *AvistazConfig {
	if instructionMessage == "" {
		instructionMessage = avistazInstructions
	}
	return &AvistazConfig{
		ConfigData:      *configdata.NewConfigData(),
		Username:        configdata.NewStringItem("Username"),
		Password:        configdata.NewStringItem("Password"),
		Pid:             configdata.NewStringItem("Pid"),
		Instructions:    configdata.NewInfoItem("", instructionMessage),
		Freeleech:       configdata.NewBoolItem("Search freeleech only"),
		AccountActivity: configdata.NewInfoItem("Account Inactivity", AvistazAccountActivity),
	}
}

// Avistaz ports upstream AvistazTracker. Embed it, set identity/Caps via
// indexer.Base, Bind a config and call WireDelay when a client is wired.
type Avistaz struct {
	indexer.Base

	// Config is the typed configuration; also wired into Base via Bind.
	Config *AvistazConfig

	// AuthorizationToken is the bearer token from the last auth call
	// (upstream protected string AuthorizationToken).
	AuthorizationToken string

	// Hooks mirror upstream virtual methods; nil = default behaviour.
	GetSearchTerm            func(q *models.TorznabQuery) string
	GetEpisodeSearchTerm     func(q *models.TorznabQuery) string
	GetSearchQueryParameters func(q *models.TorznabQuery) QueryParams
	ParseCategories          func(q *models.TorznabQuery, row *AvistazRelease) ([]int, error)
	ParseTitle               func(row *AvistazRelease) string
}

// Bind wires the typed config into Base.
func (a *Avistaz) Bind(c *AvistazConfig) {
	a.Config = c
	a.Base.Config = c
}

// WireDelay applies the upstream requestDelay (6s) to this indexer on the
// wired client; call once from the factory after Client is set.
func (a *Avistaz) WireDelay() {
	if a.Client != nil {
		a.Client.SetRequestDelay(a.ID(), AvistazRequestDelay)
	}
}

func (a *Avistaz) authURL() string { return a.SiteLinkValue + "api/v1/jackett/auth" }
func (a *Avistaz) searchAPIURL() string {
	return a.SiteLinkValue + "api/v1/jackett/torrents"
}

// authHeaders mirrors the upstream AuthHeaders dictionary.
func (a *Avistaz) authHeaders() map[string]string {
	return map[string]string{
		"Accept":       "application/json",
		"Content-Type": "application/json",
	}
}

// searchHeaders mirrors the upstream GetSearchHeaders.
func (a *Avistaz) searchHeaders() map[string]string {
	return map[string]string{
		"Accept":        "application/json",
		"Authorization": "Bearer " + a.AuthorizationToken,
	}
}

// --- configuration ---------------------------------------------------------

// ApplyConfiguration mirrors upstream AvistazTracker.ApplyConfiguration:
// renew the token, run a test query, mark configured on success.
func (a *Avistaz) ApplyConfiguration(ctx context.Context, items []indexer.ConfigItem) (indexer.ConfigStatus, error) {
	if err := applyPostedItems(&a.Base, items); err != nil {
		return indexer.ConfigCompleted, err
	}

	if err := a.RenewalToken(ctx); err != nil {
		return indexer.ConfigCompleted, err
	}

	releases, err := a.PerformQuery(ctx, models.NewTorznabQuery())
	if err != nil {
		return indexer.ConfigCompleted, err
	}
	if len(releases) == 0 {
		return indexer.ConfigCompleted, errors.New("Could not find releases.") //nolint:staticcheck // upstream AvistazTracker.cs error text, kept 1:1
	}
	a.SetConfigured(true)
	a.SaveConfig(ctx)
	return indexer.ConfigCompleted, nil
}

// RenewalToken mirrors upstream RenewalTokenAsync.
func (a *Avistaz) RenewalToken(ctx context.Context) error {
	body := url.Values{}
	body.Set("username", strings.TrimSpace(a.Config.Username.Value))
	body.Set("password", strings.TrimSpace(a.Config.Password.Value))
	body.Set("pid", strings.TrimSpace(a.Config.Pid.Value))

	res, err := a.Client.Do(ctx, a.ID(), &httpclient.Request{
		Method:  http.MethodPost,
		URL:     a.authURL(),
		Body:    body,
		Headers: a.authHeaders(),
		Cookies: a.CookieHeader(),
	})
	if err != nil {
		var tooMany *httpclient.TooManyRequests
		if errors.As(err, &tooMany) {
			return &httpclient.TooManyRequests{Message: "Rate limited", RetryAfter: tooMany.RetryAfter}
		}
		return err
	}

	var authResponse AvistazAuthResponse
	if err := json.Unmarshal(res.Body, &authResponse); err != nil {
		return errors.New("Invalid response from AvistaZ, the response is not valid JSON") //nolint:staticcheck // upstream AvistazTracker.cs error text, kept 1:1
	}

	a.AuthorizationToken = authResponse.Token
	if a.AuthorizationToken == "" {
		if authResponse.Message != "" {
			return errors.New(authResponse.Message)
		}
		return errors.New("Unauthorized request to indexer") //nolint:staticcheck // upstream AvistazTracker.cs error text, kept 1:1
	}
	return nil
}

// --- query handling --------------------------------------------------------

// ResultsForQuery implements indexer.Indexer.
func (a *Avistaz) ResultsForQuery(ctx context.Context, q *models.TorznabQuery, isMeta bool) (*indexer.IndexerResult, error) {
	return a.ResultsForQueryWith(ctx, q, isMeta, a.PerformQuery)
}

// Test implements indexer.Indexer (upstream TestIndexer).
func (a *Avistaz) Test(ctx context.Context) error {
	return a.Base.Test(ctx, a.PerformQuery)
}

// getSearchTerm mirrors the upstream virtual GetSearchTerm.
func (a *Avistaz) getSearchTerm(q *models.TorznabQuery) string {
	if a.GetSearchTerm != nil {
		return a.GetSearchTerm(q)
	}
	return strings.TrimSpace(q.SearchTerm + " " + a.getEpisodeSearchTerm(q))
}

// getEpisodeSearchTerm mirrors the upstream virtual GetEpisodeSearchTerm.
func (a *Avistaz) getEpisodeSearchTerm(q *models.TorznabQuery) string {
	if a.GetEpisodeSearchTerm != nil {
		return a.GetEpisodeSearchTerm(q)
	}
	return strings.TrimSpace(q.GetEpisodeSearchString())
}

// SearchURL builds the torrents API URL (SearchUrl + "?" + query string).
func (a *Avistaz) SearchURL(q *models.TorznabQuery) string {
	return a.searchAPIURL() + "?" + a.getSearchQueryParameters(q).GetQueryString()
}

// getSearchQueryParameters resolves the hook or the upstream default.
func (a *Avistaz) getSearchQueryParameters(q *models.TorznabQuery) QueryParams {
	if a.GetSearchQueryParameters != nil {
		return a.GetSearchQueryParameters(q)
	}
	return a.DefaultSearchQueryParameters(q)
}

// DefaultSearchQueryParameters ports upstream GetSearchQueryParameters.
func (a *Avistaz) DefaultSearchQueryParameters(query *models.TorznabQuery) QueryParams {
	categoryMapping := distinct(a.Caps.Categories.MapTorznabCapsToTrackers(query, false))

	var qc QueryParams
	// NameValueCollection doesn't support cat[]=19&cat[]=6, hence the pair list
	qc.Add("in", "1")
	qc.Add("type", firstIfSingleOrDefault(categoryMapping, "0"))
	qc.Add("limit", "50")

	if query.Limit > 0 && query.Offset > 0 {
		page := query.Offset/query.Limit + 1
		qc.Add("page", fmt.Sprintf("%d", page))
	}

	// resolution filter to improve the search
	if !containsInt(query.Categories, models.CatMovies.ID) &&
		!containsInt(query.Categories, models.CatTV.ID) &&
		!containsInt(query.Categories, models.CatAudio.ID) {
		if containsInt(query.Categories, models.CatMoviesUHD.ID) || containsInt(query.Categories, models.CatTVUHD.ID) {
			qc.Add("video_quality[]", "6") // 2160p
		}
		if containsInt(query.Categories, models.CatMoviesHD.ID) || containsInt(query.Categories, models.CatTVHD.ID) {
			qc.Add("video_quality[]", "2") // 720p
			qc.Add("video_quality[]", "7") // 1080i
			qc.Add("video_quality[]", "3") // 1080p
		}
		if containsInt(query.Categories, models.CatMoviesSD.ID) || containsInt(query.Categories, models.CatTVSD.ID) {
			qc.Add("video_quality[]", "1") // SD
		}
	}

	// note, search by tmdb and tvdb are supported too
	if query.IsImdbQuery() {
		qc.Add("imdb", query.ImdbID)
		qc.Add("search", a.getEpisodeSearchTerm(query))
	} else if query.IsTmdbQuery() {
		qc.Add("tmdb", fmt.Sprintf("%d", *query.TmdbID))
		qc.Add("search", a.getEpisodeSearchTerm(query))
	} else if query.IsTvdbQuery() {
		qc.Add("tvdb", fmt.Sprintf("%d", *query.TvdbID))
		qc.Add("search", a.getEpisodeSearchTerm(query))
	} else {
		qc.Add("search", strings.TrimSpace(a.getSearchTerm(query)))
	}

	if strings.TrimSpace(query.Genre) != "" {
		qc.Add("tags", query.Genre)
	}

	if a.Config != nil && a.Config.Freeleech != nil && a.Config.Freeleech.Value {
		qc.Add("discount[]", "1")
	}

	return qc
}

func containsInt(list []int, v int) bool {
	for _, n := range list {
		if n == v {
			return true
		}
	}
	return false
}

// PerformQuery mirrors upstream AvistazTracker.PerformQuery.
func (a *Avistaz) PerformQuery(ctx context.Context, query *models.TorznabQuery) ([]*models.ReleaseInfo, error) {
	episodeSearchURL := a.SearchURL(query)

	res, err := a.Client.Do(ctx, a.ID(), &httpclient.Request{
		URL:     episodeSearchURL,
		Headers: a.searchHeaders(),
		Cookies: a.CookieHeader(),
	})
	if err != nil {
		var tooMany *httpclient.TooManyRequests
		if errors.As(err, &tooMany) && tooMany.Message != "Rate limited" {
			return nil, &httpclient.TooManyRequests{Message: "Rate limited", RetryAfter: tooMany.RetryAfter}
		}
		return nil, err
	}

	if res.StatusCode == http.StatusUnauthorized || res.StatusCode == http.StatusPreconditionFailed {
		if err := a.RenewalToken(ctx); err != nil {
			return nil, err
		}
		res, err = a.Client.Do(ctx, a.ID(), &httpclient.Request{
			URL:     episodeSearchURL,
			Headers: a.searchHeaders(),
			Cookies: a.CookieHeader(),
		})
		if err != nil {
			var tooMany *httpclient.TooManyRequests
			if errors.As(err, &tooMany) && tooMany.Message != "Rate limited" {
				return nil, &httpclient.TooManyRequests{Message: "Rate limited", RetryAfter: tooMany.RetryAfter}
			}
			return nil, err
		}
	}

	if res.StatusCode == http.StatusNotFound {
		return []*models.ReleaseInfo{}, nil // search without results, eg CinemaZ: tt0075998
	}

	if res.StatusCode >= 400 {
		return nil, fmt.Errorf("Invalid status code %d (%s) received from indexer", res.StatusCode, StatusName(res.StatusCode)) //nolint:staticcheck // upstream AvistazTracker.cs error text, kept 1:1
	}

	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Unknown status code: %d (%s)", res.StatusCode, StatusName(res.StatusCode)) //nolint:staticcheck // upstream AvistazTracker.cs error text, kept 1:1
	}

	var jsonResponse AvistazResponse
	if err := json.Unmarshal(res.Body, &jsonResponse); err != nil {
		return nil, parseFailure(&a.Base, string(res.Body), err)
	}

	var releases []*models.ReleaseInfo
	for _, row := range jsonResponse.Data {
		details, err := url.Parse(row.URL)
		if err != nil {
			return nil, parseFailure(&a.Base, string(res.Body), err)
		}
		link, err := url.Parse(row.Download)
		if err != nil {
			return nil, parseFailure(&a.Base, string(res.Body), err)
		}

		description := ""
		if len(row.Audio) > 0 {
			var languages []string
			for _, tag := range row.Audio {
				languages = append(languages, tag.Language)
			}
			description += "Audio: " + strings.Join(languages, ", ")
		}
		if len(row.Subtitle) > 0 {
			var languages []string
			for _, tag := range row.Subtitle {
				languages = append(languages, tag.Language)
			}
			description += "<br/>Subtitles: " + strings.Join(languages, ", ")
		}

		categories, err := a.parseCategories(query, &row)
		if err != nil {
			return nil, parseFailure(&a.Base, string(res.Body), err)
		}

		publishDate, err := dateutil.FromUnknown(row.CreatedAtIso, "")
		if err != nil {
			return nil, parseFailure(&a.Base, string(res.Body), err)
		}

		minimumRatio := 1.0
		minimumSeedTime := AvistazMinimumSeedTime(row.FileSize)

		var languages []string
		for _, x := range row.Audio {
			languages = append(languages, x.Language)
		}
		var subs []string
		for _, x := range row.Subtitle {
			subs = append(subs, x.Language)
		}

		release := &models.ReleaseInfo{
			Title:                a.parseTitle(&row),
			Link:                 link,
			InfoHash:             row.InfoHash,
			Details:              details,
			Guid:                 details,
			Category:             categories,
			PublishDate:          publishDate,
			Description:          description,
			Size:                 row.FileSize,
			Files:                row.FileCount,
			Grabs:                row.Completed,
			Seeders:              row.Seed,
			Peers:                ptrSum(row.Leech, row.Seed),
			DownloadVolumeFactor: row.DownloadMultiply,
			UploadVolumeFactor:   row.UploadMultiply,
			MinimumRatio:         &minimumRatio,
			MinimumSeedTime:      &minimumSeedTime,
			Languages:            languages,
			Subs:                 subs,
		}

		if row.MovieTvinfo != nil {
			if imdb, ok := parseutil.GetImdbID(row.MovieTvinfo.Imdb); ok {
				release.Imdb = &imdb
			}
			if id, ok := parseLongLoose(row.MovieTvinfo.Tmdb); ok {
				release.TMDb = &id
			}
			if id, ok := parseLongLoose(row.MovieTvinfo.Tvdb); ok {
				release.TVDBId = &id
			}
		}

		releases = append(releases, release)
	}

	return releases, nil
}

// ptrSum mirrors nullable addition (a + b) with nil → nil.
func ptrSum(a, b *int64) *int64 {
	if a == nil && b == nil {
		return nil
	}
	var x, y int64
	if a != nil {
		x = *a
	}
	if b != nil {
		y = *b
	}
	sum := x + y
	return &sum
}

// parseLongLoose mirrors upstream IsNotNullOrWhiteSpace && long.TryParse.
func parseLongLoose(s string) (int64, bool) {
	if strings.TrimSpace(s) == "" {
		return 0, false
	}
	var n int64
	if _, err := fmt.Sscanf(strings.TrimSpace(s), "%d", &n); err != nil {
		return 0, false
	}
	return n, true
}

// AvistazMinimumSeedTime ports the upstream size-dependent MinimumSeedTime
// formula (default 72h; >50 GiB: (long)(100*ln(gb) - 219.2023) * 3600, else
// 259200 + (long)(gb * 7200)).
func AvistazMinimumSeedTime(fileSize *int64) int64 {
	if fileSize == nil || *fileSize <= 0 {
		return 259200
	}
	sizeGigabytes := float64(*fileSize) / (1024 * 1024 * 1024)
	if sizeGigabytes > 50.0 {
		return int64(100*math.Log(sizeGigabytes)-219.2023) * 3600
	}
	return 259200 + int64(sizeGigabytes*7200)
}

// parseCategories resolves the hook or the upstream default.
func (a *Avistaz) parseCategories(q *models.TorznabQuery, row *AvistazRelease) ([]int, error) {
	if a.ParseCategories != nil {
		return a.ParseCategories(q, row)
	}
	return DefaultAvistazParseCategories(row)
}

// DefaultAvistazParseCategories ports upstream ParseCategories.
func DefaultAvistazParseCategories(row *AvistazRelease) ([]int, error) {
	hdResolutions := map[string]bool{"1080p": true, "1080i": true, "720p": true}

	switch strings.ToUpper(row.Type) {
	case "MOVIE":
		switch {
		case hdResolutions[row.VideoQuality]:
			return []int{models.CatMoviesHD.ID}, nil
		case row.VideoQuality == "2160p":
			return []int{models.CatMoviesUHD.ID}, nil
		default:
			return []int{models.CatMoviesSD.ID}, nil
		}
	case "TV-SHOW":
		switch {
		case hdResolutions[row.VideoQuality]:
			return []int{models.CatTVHD.ID}, nil
		case row.VideoQuality == "2160p":
			return []int{models.CatTVUHD.ID}, nil
		default:
			return []int{models.CatTVSD.ID}, nil
		}
	case "MUSIC":
		return []int{models.CatAudio.ID}, nil
	default:
		return nil, errors.New("Error parsing category!") //nolint:staticcheck // upstream AvistazTracker.cs error text, kept 1:1
	}
}

// parseTitle resolves the hook or the upstream default.
func (a *Avistaz) parseTitle(row *AvistazRelease) string {
	if a.ParseTitle != nil {
		return a.ParseTitle(row)
	}
	return row.FileName
}

// --- response models (upstream AvistazRelease et al.) -----------------------

// AvistazRelease is one row of the torrents API response
// (upstream AvistazRelease; JSON names verbatim).
type AvistazRelease struct {
	URL              string            `json:"url"`
	Download         string            `json:"download"`
	Category         map[string]string `json:"category"`
	MovieTvinfo      *AvistazIdInfo    `json:"movie_tv"`
	CreatedAtIso     string            `json:"created_at_iso"`
	FileName         string            `json:"file_name"`
	ReleaseTitle     string            `json:"release_title"`
	InfoHash         string            `json:"info_hash"`
	Leech            *int64            `json:"leech"`
	Completed        *int64            `json:"completed"`
	Seed             *int64            `json:"seed"`
	FileSize         *int64            `json:"file_size"`
	FileCount        *int64            `json:"file_count"`
	DownloadMultiply *float64          `json:"download_multiply"`
	UploadMultiply   *float64          `json:"upload_multiply"`
	VideoQuality     string            `json:"video_quality"`
	Type             string            `json:"type"`
	Format           string            `json:"format"`
	Audio            []AvistazLanguage `json:"audio"`
	Subtitle         []AvistazLanguage `json:"subtitle"`
}

// AvistazLanguage is one audio/subtitle language entry.
type AvistazLanguage struct {
	ID       int    `json:"id"`
	Language string `json:"language"`
}

// AvistazIdInfo is the movie_tv id block (upstream AvistazIdInfo).
type AvistazIdInfo struct {
	Tmdb string `json:"tmdb"`
	Tvdb string `json:"tvdb"`
	Imdb string `json:"imdb"`
}

// AvistazResponse is the torrents API envelope.
type AvistazResponse struct {
	Data []AvistazRelease `json:"data"`
}

// AvistazAuthResponse is the auth API envelope.
type AvistazAuthResponse struct {
	Token   string `json:"token"`
	Message string `json:"message"`
}
