// mechanism: json-api (email/password JSON login against api/login, Bearer
// token on api/torrent searches, silent re-login + single retry on 401,
// token-less first search logs in on demand)
//
// Ported from: Definitions/SpeedApp.cs (+ Abstract/SpeedAppTracker.cs).
//
// 澄清记录:
//   - upstream posts the login body as a JSON string (rawbody +
//     Content-Type: application/json, SpeedAppTracker.cs:75-77); the Go
//     port sends RawBody with the same content type (NOT form-encoded).
//   - upstream RenewalTokenAsync does not check the HTTP status
//     (SpeedAppTracker.cs:76-83): a 401 page that is not JSON makes
//     JObject.Parse throw, a JSON body without "token" throws the
//     "message" value. The Go port mirrors both (JSON decode failure →
//     KindAuth with the decode message; missing token → KindAuth with the
//     upstream message).
//   - the 401-retry in PerformQuery (SpeedAppTracker.cs:126-130) is only
//     tried once; a still-failing retry is parsed as a torrent array and
//     ends in OnParseError upstream — replicated verbatim (no second
//     status check after the retry).
//   - upstream Download throws $"Unknown error in download:
//     {response.ContentBytes}" (SpeedAppTracker.cs:221); interpolating a
//     byte[] renders the literal "System.Byte[]" in .NET — replicated.
//   - upstream release.Genres is built by Union(genres.Split(',')) after
//     genres was re-joined with ", " (SpeedAppTracker.cs:159-199), so
//     every entry after the first keeps its leading space; and when the
//     genre list is empty, Split(',') yields [""] so Genres == [""]
//     (upstream quirk, replicated).
package manual

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"vestt/common/httpclient"
	"vestt/common/indexer"
	"vestt/common/indexer/configdata"
	"vestt/common/indexer/shared"
	"vestt/common/models"
	"vestt/common/services"
	"vestt/common/utils/dateutil"
)

// SpeedApp ports Jackett.Common/Indexers/Definitions/SpeedApp.cs.
type SpeedApp struct {
	speedAppTracker
}

// speedAppAccountInactivity mirrors ConfigurationDataSpeedAppTracker's
// AccountActivity item (Models/IndexerConfig/Bespoke/
// ConfigurationDataSpeedAppTracker.cs).
const speedAppAccountInactivity = "The accounts shall not be deleted or automatically deactivated. Inactive accounts, regardless of class, will have the invitations deleted after 180 days."

// NewSpeedApp builds the indexer (services.Factory shape).
func NewSpeedApp(deps services.Deps) (indexer.Indexer, error) {
	s := &SpeedApp{}
	s.buildSpeedAppTracker(deps, speedAppIdentity{
		id:          "speedapp",
		name:        "SpeedApp",
		description: "SpeedApp is a ROMANIAN Private Torrent Tracker for MOVIES / TV / GENERAL",
		siteLink:    "https://speedapp.io/",
		legacyLinks: []string{
			"https://www.icetorrent.org/",
			"https://icetorrent.org/",
			"https://scenefz.me/",
			"https://www.scenefz.me/",
			"https://www.u-torrents.ro/",
			"https://myxz.eu/",
			"https://www.myxz.eu/",
			"https://www.myxz.org/",
		},
		lang: "ro-RO",
	}, speedAppCaps(), 172800, nil) // upstream default MinimumSeedTime (48h)
	return s, nil
}

func speedAppCaps() *models.Capabilities {
	caps := models.NewCapabilities()
	caps.TVSearchParams = []string{models.TVParamQ, models.TVParamSeason, models.TVParamEp, models.TVParamImdbID}
	caps.MovieSearchParams = []string{models.MovieParamQ, models.MovieParamImdbID}
	caps.MusicSearchParams = []string{models.MusicParamQ}
	caps.BookSearchParams = []string{models.BookParamQ}

	// from https://speedapp.io/internal
	c := caps.Categories
	c.AddCategoryMapping("38", models.CatMovies, "Movie Packs")
	c.AddCategoryMapping("10", models.CatMoviesSD, "Movies: SD")
	c.AddCategoryMapping("35", models.CatMoviesSD, "Movies: SD Ro")
	c.AddCategoryMapping("8", models.CatMoviesHD, "Movies: HD")
	c.AddCategoryMapping("29", models.CatMoviesHD, "Movies: HD Ro")
	c.AddCategoryMapping("7", models.CatMoviesDVD, "Movies: DVD")
	c.AddCategoryMapping("2", models.CatMoviesDVD, "Movies: DVD Ro")
	c.AddCategoryMapping("17", models.CatMoviesBluRay, "Movies: BluRay")
	c.AddCategoryMapping("24", models.CatMoviesBluRay, "Movies: BluRay Ro")
	c.AddCategoryMapping("59", models.CatMovies, "Movies: Ro")
	c.AddCategoryMapping("57", models.CatMoviesUHD, "Movies: 4K (2160p) Ro")
	c.AddCategoryMapping("61", models.CatMoviesUHD, "Movies: 4K (2160p)")
	c.AddCategoryMapping("41", models.CatTV, "TV Packs")
	c.AddCategoryMapping("66", models.CatTV, "TV Packs Ro")
	c.AddCategoryMapping("45", models.CatTVSD, "TV Episodes")
	c.AddCategoryMapping("46", models.CatTVSD, "TV Episodes Ro")
	c.AddCategoryMapping("43", models.CatTVHD, "TV Episodes HD")
	c.AddCategoryMapping("44", models.CatTVHD, "TV Episodes HD Ro")
	c.AddCategoryMapping("60", models.CatTV, "TV Ro")
	c.AddCategoryMapping("11", models.CatPCGames, "Games: PC-ISO")
	c.AddCategoryMapping("52", models.CatConsole, "Games: Console")
	c.AddCategoryMapping("1", models.CatPC0day, "Applications")
	c.AddCategoryMapping("14", models.CatPC, "Applications: Linux")
	c.AddCategoryMapping("37", models.CatPCMac, "Applications: Mac")
	c.AddCategoryMapping("19", models.CatPCMobileOther, "Applications: Mobile")
	c.AddCategoryMapping("62", models.CatTV, "TV Cartoons")
	c.AddCategoryMapping("3", models.CatTVAnime, "TV Anime / Hentai")
	c.AddCategoryMapping("6", models.CatBooksEBook, "E-books")
	c.AddCategoryMapping("5", models.CatAudio, "Music")
	c.AddCategoryMapping("64", models.CatAudioVideo, "Music Video")
	c.AddCategoryMapping("68", models.CatAudioLossless, "Music Lossless")
	c.AddCategoryMapping("18", models.CatOther, "Images")
	c.AddCategoryMapping("22", models.CatTVSport, "TV Sports")
	c.AddCategoryMapping("58", models.CatTVSport, "TV Sports Ro")
	c.AddCategoryMapping("9", models.CatTVDocumentary, "TV Documentary")
	c.AddCategoryMapping("63", models.CatTVDocumentary, "TV Documentary Ro")
	c.AddCategoryMapping("65", models.CatOther, "Tutorial")
	c.AddCategoryMapping("67", models.CatOtherMisc, "Miscellaneous")
	// from https://speedapp.io/adult
	c.AddCategoryMapping("15", models.CatXXX, "XXX Movies")
	c.AddCategoryMapping("47", models.CatXXX, "XXX DVD")
	c.AddCategoryMapping("48", models.CatXXX, "XXX HD")
	c.AddCategoryMapping("49", models.CatXXXImageSet, "XXX Images")
	c.AddCategoryMapping("50", models.CatXXX, "XXX Packs")
	c.AddCategoryMapping("51", models.CatXXX, "XXX SD")
	return caps
}

// --- SpeedAppTracker base (Abstract/SpeedAppTracker.cs) ---------------------

// speedAppIdentity carries the per-site identity for the shared base.
type speedAppIdentity struct {
	id          string
	name        string
	description string
	siteLink    string
	altLinks    []string // upstream AlternativeSiteLinks
	legacyLinks []string // upstream LegacySiteLinks
	lang        string
}

// speedAppConfig ports upstream ConfigurationDataSpeedAppTracker
// (ConfigurationDataBasicLoginWithEmail + FreeleechOnly + AccountActivity).
type speedAppConfig struct {
	configdata.ConfigData
	Email           *configdata.StringItem
	Password        *configdata.StringItem
	Instructions    *configdata.InfoItem
	FreeleechOnly   *configdata.BoolItem
	AccountActivity *configdata.InfoItem
}

func newSpeedAppConfig() *speedAppConfig {
	return &speedAppConfig{
		ConfigData:      *configdata.NewConfigData(),
		Email:           configdata.NewStringItem("Email"),
		Password:        configdata.NewStringItem("Password"),
		Instructions:    configdata.NewInfoItem("", ""),
		FreeleechOnly:   configdata.NewBoolItem("Show freeleech only"),
		AccountActivity: configdata.NewInfoItem("Account Inactivity", speedAppAccountInactivity),
	}
}

// speedAppTracker ports upstream SpeedAppTracker (shared by SpeedApp and
// RetroFlix).
type speedAppTracker struct {
	indexer.Base
	cfg             *speedAppConfig
	token           string // upstream private string _token
	minimumSeedTime int64  // upstream virtual MinimumSeedTime (seconds)
}

// speedAppRequestDelay mirrors webclient.requestDelay = 2.1 (SpeedApp.cs:50,
// RetroFlix.cs:40 — "requestDelay for API Limit (1 request per 2 seconds)").
const speedAppRequestDelay = 2100 * time.Millisecond

func (s *speedAppTracker) buildSpeedAppTracker(deps services.Deps, idn speedAppIdentity, caps *models.Capabilities, minimumSeedTime int64, cfg *speedAppConfig) {
	if cfg == nil {
		cfg = newSpeedAppConfig()
	}
	s.cfg = cfg
	s.minimumSeedTime = minimumSeedTime
	s.Base = indexer.Base{
		IDValue:          idn.id,
		NameValue:        idn.name,
		DescriptionValue: idn.description,
		SiteLinkValue:    idn.siteLink,
		DefaultSiteLink:  idn.siteLink,
		AltSiteLinks:     idn.altLinks,
		LegacySiteLinks:  idn.legacyLinks,
		LangValue:        idn.lang,
		TypeValue:        "private",
		Caps:             caps,
		Config:           cfg,
		Client:           deps.Client,
		Saver:            deps.Saver,
		Cache:            deps.Cache,
	}
	if deps.Client != nil {
		deps.Client.SetRequestDelay(idn.id, speedAppRequestDelay)
	}
}

func (s *speedAppTracker) loginURL() string  { return s.SiteLink() + "api/login" }
func (s *speedAppTracker) searchURL() string { return s.SiteLink() + "api/torrent" }

// apiHeaders mirrors the upstream _apiHeaders dictionary.
func (s *speedAppTracker) apiHeaders() map[string]string {
	return map[string]string{
		"Accept":       "application/json",
		"Content-Type": "application/json",
	}
}

// searchHeaders mirrors upstream GetSearchHeaders.
func (s *speedAppTracker) searchHeaders() map[string]string {
	return map[string]string{"Authorization": "Bearer " + s.token}
}

// ApplyConfiguration ports upstream SpeedAppTracker.ApplyConfiguration
// (SpeedAppTracker.cs:50-61): load values, renew the token, run a test
// query, mark configured on success.
func (s *speedAppTracker) ApplyConfiguration(ctx context.Context, items []indexer.ConfigItem) (indexer.ConfigStatus, error) {
	if err := s.ApplyItems(items); err != nil {
		return indexer.ConfigCompleted, s.speedAppConfigError(err)
	}
	if err := s.LoadValuesFromJSON(nil, false); err != nil {
		return indexer.ConfigCompleted, s.speedAppConfigError(err)
	}
	if err := s.renewalToken(ctx); err != nil {
		return indexer.ConfigCompleted, err
	}
	releases, err := s.PerformQuery(ctx, models.NewTorznabQuery())
	if err != nil {
		return indexer.ConfigCompleted, s.speedAppConfigError(err)
	}
	if len(releases) == 0 {
		return indexer.ConfigCompleted, s.speedAppConfigError(errors.New("Could not find releases.")) //nolint:staticcheck // upstream SpeedAppTracker.cs error text, kept 1:1
	}
	s.SetConfigured(true)
	s.SaveConfig(ctx)
	return indexer.ConfigCompleted, nil
}

// renewalToken ports upstream RenewalTokenAsync (SpeedAppTracker.cs:63-84).
func (s *speedAppTracker) renewalToken(ctx context.Context) error {
	if s.cfg.Email == nil || s.cfg.Password == nil {
		return &indexer.IndexerError{IndexerID: s.ID(), Kind: indexer.KindConfig, Message: "Please, check the indexer configuration."}
	}

	body, err := json.Marshal(map[string]string{
		"username": strings.TrimSpace(s.cfg.Email.Value),
		"password": strings.TrimSpace(s.cfg.Password.Value),
	})
	if err != nil {
		return err
	}
	res, err := s.Client.Do(ctx, s.ID(), &httpclient.Request{
		Method:  http.MethodPost,
		URL:     s.loginURL(),
		RawBody: string(body),
		Headers: s.apiHeaders(),
		Cookies: s.CookieHeader(),
	})
	if err != nil {
		return err
	}

	var loginResponse struct {
		Token   string `json:"token"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(res.Body, &loginResponse); err != nil {
		// upstream: JObject.Parse throws → generic exception; mapped to
		// KindAuth (auth-endpoint failure, SPEC-09 §4).
		return &indexer.IndexerError{IndexerID: s.ID(), Kind: indexer.KindAuth, Message: err.Error()}
	}
	s.token = loginResponse.Token
	if s.token == "" {
		return &indexer.IndexerError{IndexerID: s.ID(), Kind: indexer.KindAuth, Message: loginResponse.Message}
	}
	return nil
}

// ResultsForQuery implements indexer.Indexer.
func (s *speedAppTracker) ResultsForQuery(ctx context.Context, q *models.TorznabQuery, isMeta bool) (*indexer.IndexerResult, error) {
	return s.ResultsForQueryWith(ctx, q, isMeta, s.PerformQuery)
}

// Test implements indexer.Indexer (upstream TestIndexer).
func (s *speedAppTracker) Test(ctx context.Context) error {
	return s.Base.Test(ctx, s.PerformQuery)
}

// PerformQuery ports upstream SpeedAppTracker.PerformQuery
// (SpeedAppTracker.cs:86-209).
func (s *speedAppTracker) PerformQuery(ctx context.Context, query *models.TorznabQuery) ([]*models.ReleaseInfo, error) {
	if s.Client == nil {
		return nil, errors.New("http client not configured")
	}

	var qc shared.QueryParams // NameValueCollection doesn't support cat[]=19&cat[]=6
	qc.Add("itemsPerPage", "100")
	qc.Add("includingDead", "1")
	qc.Add("sort", "torrent.createdAt")
	qc.Add("direction", "desc")

	if query.Limit > 0 && query.Offset > 0 {
		page := query.Offset/query.Limit + 1
		qc.Add("page", fmt.Sprintf("%d", page))
	}

	for _, cat := range s.Caps.Categories.MapTorznabCapsToTrackers(query, false) {
		qc.Add("categories[]", cat)
	}

	if query.IsImdbQuery() {
		qc.Add("imdbId", query.ImdbID)
	} else {
		qc.Add("search", query.GetQueryString())
	}

	if strings.TrimSpace(s.token) == "" { // first time login
		if err := s.renewalToken(ctx); err != nil {
			return nil, err
		}
	}

	searchURL := s.searchURL() + "?" + qc.GetQueryString()
	res, err := s.Client.Do(ctx, s.ID(), &httpclient.Request{
		URL:     searchURL,
		Headers: s.searchHeaders(),
		Cookies: s.CookieHeader(),
	})
	if err != nil {
		return nil, err
	}

	if res.StatusCode == http.StatusUnauthorized {
		if err := s.renewalToken(ctx); err != nil { // re-login
			return nil, err
		}
		res, err = s.Client.Do(ctx, s.ID(), &httpclient.Request{
			URL:     searchURL,
			Headers: s.searchHeaders(),
			Cookies: s.CookieHeader(),
		})
		if err != nil {
			return nil, err
		}
		// upstream quirk: no status re-check after the retry — a still-401
		// body is parsed as a torrent array and ends in OnParseError.
	} else if res.StatusCode != http.StatusOK {
		return nil, &indexer.IndexerError{
			IndexerID: s.ID(),
			Kind:      indexer.KindConfig,
			Message:   fmt.Sprintf("Unknown error in search: %s", string(res.Body)),
		}
	}

	var rows []json.RawMessage
	if err := json.Unmarshal(res.Body, &rows); err != nil {
		return nil, s.speedAppParseFailure(string(res.Body), err)
	}

	var releases []*models.ReleaseInfo
	for _, raw := range rows {
		release, err := s.mapTorrent(query, raw)
		if err != nil {
			return nil, s.speedAppParseFailure(string(res.Body), err)
		}
		if release != nil {
			releases = append(releases, release)
		}
	}
	return releases, nil
}

// speedAppRow is one torrent entry of the api/torrent JSON array.
type speedAppRow struct {
	ID                int64   `json:"id"`
	URL               string  `json:"url"`
	Name              string  `json:"name"`
	CreatedAt         string  `json:"created_at"`
	ShortDescription  string  `json:"short_description"`
	Poster            string  `json:"poster"`
	Size              int64   `json:"size"`
	TimesCompleted    int64   `json:"times_completed"`
	Seeders           int64   `json:"seeders"`
	Leechers          int64   `json:"leechers"`
	DownloadVolFactor float64 `json:"download_volume_factor"`
	UploadVolFactor   float64 `json:"upload_volume_factor"`
	Category          struct {
		ID string `json:"id"`
	} `json:"category"`
}

var speedAppRequestRegex = regexp.MustCompile(`(?i)\[REQUEST(ED)?\]`)

// mapTorrent ports the upstream per-row mapping
// (SpeedAppTracker.cs:140-201); nil release = skipped/filtered row.
func (s *speedAppTracker) mapTorrent(query *models.TorznabQuery, raw json.RawMessage) (*models.ReleaseInfo, error) {
	var row speedAppRow
	if err := json.Unmarshal(raw, &row); err != nil {
		return nil, err
	}

	dlVolumeFactor := row.DownloadVolFactor

	// skip non-freeleech results when freeleech only is set
	if s.cfg.FreeleechOnly.Value && dlVolumeFactor != 0 {
		return nil, nil
	}

	link, err := url.Parse(fmt.Sprintf("%sapi/torrent/%d/download", s.SiteLink(), row.ID))
	if err != nil {
		return nil, err
	}
	details, err := url.Parse(row.URL)
	if err != nil {
		return nil, err
	}
	publishDate, err := dateutil.FromUnknown(row.CreatedAt, "")
	if err != nil {
		return nil, err
	}

	description := ""
	var genresList []string
	for _, piece := range strings.FieldsFunc(row.ShortDescription, func(r rune) bool {
		// upstream delimiters: { ',', ' ', '/', ')', '(', '.', ';', '[', ']', '"', '|', ':' }
		return r == ' ' || strings.ContainsRune(",/).;[]\"|:", r)
	}) {
		if piece != "" {
			genresList = append(genresList, piece)
		}
	}
	genres := strings.Join(genresList, ", ")
	if genres != "" {
		description = genres
	}

	var poster *url.URL
	if u, err := url.Parse(row.Poster); err == nil && u.IsAbs() {
		poster = u
	}

	title := speedAppCleanTitle(row.Name)

	if !query.IsImdbQuery() && !query.MatchQueryStringAND(title, 0, "") {
		return nil, nil
	}

	minimumRatio := 1.0
	peers := row.Leechers + row.Seeders
	release := &models.ReleaseInfo{
		Title:                title,
		Link:                 link,
		Details:              details,
		Guid:                 details,
		Category:             s.Caps.Categories.MapTrackerCatToNewznab(row.Category.ID),
		PublishDate:          publishDate,
		Description:          description,
		Poster:               poster,
		Size:                 &row.Size,
		Grabs:                &row.TimesCompleted,
		Seeders:              &row.Seeders,
		Peers:                &peers,
		DownloadVolumeFactor: &dlVolumeFactor,
		UploadVolumeFactor:   &row.UploadVolFactor,
		MinimumRatio:         &minimumRatio,
		MinimumSeedTime:      &s.minimumSeedTime,
	}

	// upstream: release.Genres ??= new List<string>();
	// release.Genres = release.Genres.Union(genres.Split(',')).ToList();
	// Genres is always null here, so this is genres.Split(',') verbatim —
	// including the [""] entry for an empty genre string (quirk, see file
	// header).
	release.Genres = strings.Split(genres, ",")

	return release, nil
}

// Download ports upstream SpeedAppTracker.Download (SpeedAppTracker.cs:211-225).
func (s *speedAppTracker) Download(ctx context.Context, link *url.URL) ([]byte, error) {
	res, err := s.Client.Do(ctx, s.ID(), &httpclient.Request{
		URL:     link.String(),
		Headers: s.searchHeaders(),
		Cookies: s.CookieHeader(),
	})
	if err != nil {
		return nil, err
	}

	if res.StatusCode == http.StatusUnauthorized {
		if err := s.renewalToken(ctx); err != nil {
			return nil, err
		}
		res, err = s.Client.Do(ctx, s.ID(), &httpclient.Request{
			URL:     link.String(),
			Headers: s.searchHeaders(),
			Cookies: s.CookieHeader(),
		})
		if err != nil {
			return nil, err
		}
	} else if res.StatusCode != http.StatusOK {
		// upstream quirk: $"{response.ContentBytes}" renders "System.Byte[]"
		return nil, fmt.Errorf("Unknown error in download: %s", "System.Byte[]") //nolint:staticcheck // upstream SpeedAppTracker.cs error text, kept 1:1
	}

	return res.Body, nil
}

// speedAppCleanTitle ports upstream CleanTitle (SpeedAppTracker.cs:232-237).
func speedAppCleanTitle(title string) string {
	title = speedAppRequestRegex.ReplaceAllString(title, "")
	return strings.Trim(title, " .")
}

// speedAppConfigError maps configuration/self-test failures to KindConfig.
func (s *speedAppTracker) speedAppConfigError(err error) error {
	var ie *indexer.IndexerError
	if errors.As(err, &ie) {
		return err
	}
	return &indexer.IndexerError{IndexerID: s.ID(), Kind: indexer.KindConfig, Message: err.Error()}
}

// speedAppParseFailure mirrors upstream OnParseError.
func (s *speedAppTracker) speedAppParseFailure(dump string, err error) error {
	s.log().Error(fmt.Sprintf("Error while parsing result: %v\n%s", err, dump))
	return &indexer.IndexerError{IndexerID: s.ID(), Kind: indexer.KindParse, Message: "Parse error"}
}

// log is a nil-safe logger (Base.Logger may be nil).
func (s *speedAppTracker) log() *slog.Logger {
	if s.Logger != nil {
		return s.Logger
	}
	return slog.Default()
}

// batch3F64 is the small *float64 helper for this batch's files.
func batch3F64(v float64) *float64 { return &v }
