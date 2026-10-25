// Package manual holds the hand-written indexers (one file per site).
package manual

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"vestt/common/httpclient"
	"vestt/common/indexer"
	"vestt/common/indexer/configdata"
	"vestt/common/indexer/shared"
	"vestt/common/models"
	"vestt/common/services"
)

// MTeamTp ports Jackett.Common/Indexers/Definitions/MTeamTp.cs.
//
// mechanism: json-api (API-key authenticated POST to api.<domain>)
//
// 澄清记录:
//   - upstream `Replaces => ["mteamtp2fa"]` has no Go Base equivalent
//     (no replaces concept in the indexer interface) — dropped.
//   - the API base is derived from the site link
//     ("https://api." + SiteLink.Substring(IndexOf('.') + 1)); here it is an
//     overridable unexported field (default "" = derive) so the httptest
//     fixtures can point the indexer at a fake site.
//   - the search body is serialized with STJson (WriteIndented,
//     camelCase, nulls omitted, camelCase string enums) — the builder below
//     reproduces the exact wire format including the 2-space indentation.
//
// Ported from: Definitions/MTeamTp.cs,
// Models/IndexerConfig/Bespoke/ConfigurationDataMTeamTp.cs.
type MTeamTp struct {
	indexer.Base
	cfg *mteamtpConfig
	// apiBase overrides the derived API base URL; "" derives from SiteLink.
	apiBase string
}

// mteamtpConfig ports upstream ConfigurationDataMTeamTp.
type mteamtpConfig struct {
	configdata.ConfigData
	ApiKey        *configdata.StringItem
	ApiKeyInfo    *configdata.InfoItem
	FreeleechOnly *configdata.BoolItem
}

func newMTeamtpConfig() *mteamtpConfig {
	return &mteamtpConfig{
		ConfigData:    *configdata.NewConfigData(),
		ApiKey:        configdata.NewStringItem("API Key"),
		ApiKeyInfo:    configdata.NewInfoItem("ApiKey Info", "The API key can be obtained by accessing your M-Team - TP User CP > Laboratory."),
		FreeleechOnly: configdata.NewBoolItem("Search freeleech only"),
	}
}

// NewMTeamTp builds the indexer (services.Factory shape).
func NewMTeamTp(deps services.Deps) (indexer.Indexer, error) {
	m := &MTeamTp{cfg: newMTeamtpConfig()}
	m.Base = indexer.Base{
		IDValue:          "mteamtp",
		NameValue:        "M-Team - TP",
		DescriptionValue: "M-Team - TP (MTTP) is a CHINESE Private Torrent Tracker for MOVIES / TV / GENERAL",
		SiteLinkValue:    "https://kp.m-team.cc/",
		DefaultSiteLink:  "https://kp.m-team.cc/",
		AltSiteLinks:     []string{"https://kp.m-team.cc/", "https://tp.m-team.cc/", "https://pt.m-team.cc/"},
		LangValue:        "zh-CN",
		TypeValue:        "private",
		Caps:             mteamtpCaps(),
		Config:           m.cfg,
		Client:           deps.Client,
		Saver:            deps.Saver,
		Cache:            deps.Cache,
	}
	// upstream: webclient.requestDelay = 5 (seconds).
	if deps.Client != nil {
		deps.Client.SetRequestDelay(m.IDValue, 5*time.Second)
	}
	return m, nil
}

var mteamtpAdultCategories = map[int]bool{
	410: true, 424: true, 437: true, 431: true, 429: true, 430: true,
	426: true, 432: true, 436: true, 440: true, 425: true, 433: true,
	411: true, 412: true, 413: true,
}

func mteamtpCaps() *models.Capabilities {
	caps := models.NewCapabilities()
	caps.TVSearchParams = []string{models.TVParamQ, models.TVParamSeason, models.TVParamEp, models.TVParamImdbID}
	caps.MovieSearchParams = []string{models.MovieParamQ, models.MovieParamImdbID}
	caps.MusicSearchParams = []string{models.MusicParamQ}
	caps.BookSearchParams = []string{models.BookParamQ}
	c := caps.Categories
	c.AddCategoryMapping("401", models.CatMoviesSD, "Movie(電影)/SD")
	c.AddCategoryMapping("419", models.CatMoviesHD, "Movie(電影)/HD")
	c.AddCategoryMapping("420", models.CatMoviesDVD, "Movie(電影)/DVDiSo")
	c.AddCategoryMapping("421", models.CatMoviesBluRay, "Movie(電影)/Blu-Ray")
	c.AddCategoryMapping("439", models.CatMoviesHD, "Movie(電影)/Remux")
	c.AddCategoryMapping("403", models.CatTVSD, "TV Series(影劇/綜藝)/SD")
	c.AddCategoryMapping("402", models.CatTVHD, "TV Series(影劇/綜藝)/HD")
	c.AddCategoryMapping("438", models.CatTVHD, "TV Series(影劇/綜藝)/BD")
	c.AddCategoryMapping("435", models.CatTVSD, "TV Series(影劇/綜藝)/DVDiSo")
	c.AddCategoryMapping("404", models.CatTVDocumentary, "紀錄教育")
	c.AddCategoryMapping("434", models.CatAudio, "Music(無損)")
	c.AddCategoryMapping("406", models.CatAudioVideo, "MV(演唱)")
	c.AddCategoryMapping("423", models.CatPCGames, "PCGame(PC遊戲)")
	c.AddCategoryMapping("448", models.CatConsoleOther, "TvGame(TV遊戲)")
	c.AddCategoryMapping("405", models.CatTVAnime, "Anime(動畫)")
	c.AddCategoryMapping("453", models.CatTVAnime, "Anime(動畫)/BluRay")
	c.AddCategoryMapping("407", models.CatTVSport, "Sports(運動)")
	c.AddCategoryMapping("427", models.CatBooksEBook, "Study/Edu ebook(教育書面)")
	c.AddCategoryMapping("422", models.CatPC0day, "Software(軟體)")
	c.AddCategoryMapping("442", models.CatAudioAudiobook, "Study/Edu audio(教育音檔)")
	c.AddCategoryMapping("451", models.CatMoviesOther, "教育影片")
	c.AddCategoryMapping("409", models.CatOther, "Misc(其他)")

	// adult
	c.AddCategoryMapping("410", models.CatXXX, "AV(有碼)/HD Censored")
	c.AddCategoryMapping("424", models.CatXXXSD, "AV(有碼)/SD Censored")
	c.AddCategoryMapping("437", models.CatXXXDVD, "AV(有碼)/DVDiSo Censored")
	c.AddCategoryMapping("431", models.CatXXX, "AV(有碼)/Blu-Ray Censored")
	c.AddCategoryMapping("429", models.CatXXX, "AV(無碼)/HD Uncensored")
	c.AddCategoryMapping("430", models.CatXXXSD, "AV(無碼)/SD Uncensored")
	c.AddCategoryMapping("426", models.CatXXXDVD, "AV(無碼)/DVDiSo Uncensored")
	c.AddCategoryMapping("432", models.CatXXX, "AV(無碼)/Blu-Ray Uncensored")
	c.AddCategoryMapping("436", models.CatXXX, "AV(網站)/0Day")
	c.AddCategoryMapping("440", models.CatXXX, "AV(Gay)/HD")
	c.AddCategoryMapping("425", models.CatXXX, "IV(寫真影集)/Video Collection")
	c.AddCategoryMapping("433", models.CatXXXImageSet, "IV(寫真圖集)/Picture Collection")
	c.AddCategoryMapping("411", models.CatXXX, "H-Game(遊戲)")
	c.AddCategoryMapping("412", models.CatXXX, "H-Anime(動畫)")
	c.AddCategoryMapping("413", models.CatXXX, "H-Comic(漫畫)")
	return caps
}

func (m *MTeamTp) apiBaseURL() string {
	if m.apiBase != "" {
		return m.apiBase
	}
	site := m.SiteLink()
	i := strings.Index(site, ".")
	// upstream: "https://api." + SiteLink.Substring(SiteLink.IndexOf('.') + 1)
	return "https://api." + site[i+1:]
}

// ApplyConfiguration ports upstream: require the API key, run an empty
// query and require at least one release.
func (m *MTeamTp) ApplyConfiguration(ctx context.Context, items []indexer.ConfigItem) (indexer.ConfigStatus, error) {
	if err := m.ApplyItems(items); err != nil {
		return indexer.ConfigCompleted, err
	}
	if err := m.LoadValuesFromJSON(nil, false); err != nil {
		return indexer.ConfigCompleted, err
	}
	if strings.TrimSpace(m.cfg.ApiKey.Value) == "" {
		return indexer.ConfigCompleted, errors.New("Missing API Key.") //nolint:staticcheck // upstream MTeamTp.cs error text, kept 1:1
	}
	m.SetConfigured(false)
	results, err := m.perform(ctx, models.NewTorznabQuery())
	if err != nil {
		return indexer.ConfigCompleted, err
	}
	if len(results) == 0 {
		return indexer.ConfigCompleted, errors.New("Could not find releases.") //nolint:staticcheck // upstream MTeamTp.cs error text, kept 1:1
	}
	m.SetConfigured(true)
	m.SaveConfig(ctx)
	return indexer.ConfigCompleted, nil
}

func (m *MTeamTp) ResultsForQuery(ctx context.Context, q *models.TorznabQuery, isMeta bool) (*indexer.IndexerResult, error) {
	return m.ResultsForQueryWith(ctx, q, isMeta, m.perform)
}

func (m *MTeamTp) Test(ctx context.Context) error {
	return m.Base.Test(ctx, m.perform)
}

// Download ports upstream MTeamTp.Download: exchange the download link for
// a tokenized URL (genDlToken response) and download that.
func (m *MTeamTp) Download(ctx context.Context, link *url.URL) ([]byte, error) {
	if m.Client == nil {
		return nil, errors.New("http client not configured")
	}
	res, err := m.Client.Do(ctx, m.ID(), &httpclient.Request{
		Method: http.MethodPost,
		URL:    link.String(),
		Headers: map[string]string{
			"Accept":    "application/json",
			"x-api-key": m.cfg.ApiKey.Value,
		},
		Cookies: m.CookieHeader(),
	})
	if err != nil {
		return nil, err
	}

	var jsonResponse mteamtpDownloadTokenResponse
	if err := json.Unmarshal(res.Body, &jsonResponse); err != nil {
		return nil, errors.New("Invalid response received from M-Team, not a valid JSON") //nolint:staticcheck // upstream MTeamTp.cs error text, kept 1:1
	}
	if strings.TrimSpace(jsonResponse.Data) == "" {
		return nil, fmt.Errorf("Unable to find download link for: %s", link) //nolint:staticcheck // upstream MTeamTp.cs error text, kept 1:1
	}
	downloadURL, err := url.Parse(jsonResponse.Data)
	if err != nil {
		return nil, err
	}
	return m.Base.Download(ctx, downloadURL)
}

// perform ports upstream MTeamTp.PerformQuery: split the mapped categories
// into normal/adult, fetch each group and order by descending date.
func (m *MTeamTp) perform(ctx context.Context, query *models.TorznabQuery) ([]*models.ReleaseInfo, error) {
	var categoryMapping []int
	seen := map[int]bool{}
	for _, cat := range m.Caps.Categories.MapTorznabCapsToTrackers(query, false) {
		n, err := strconv.Atoi(cat)
		if err != nil {
			return nil, err
		}
		if !seen[n] {
			seen[n] = true
			categoryMapping = append(categoryMapping, n)
		}
	}

	var adultCategories []int
	var normalCategories []int
	for _, c := range categoryMapping {
		if mteamtpAdultCategories[c] {
			adultCategories = append(adultCategories, c)
		} else {
			normalCategories = append(normalCategories, c)
		}
	}

	var releases []*models.ReleaseInfo
	if len(categoryMapping) == 0 || len(normalCategories) > 0 {
		rels, err := m.fetchTrackerReleases(ctx, "normal", query, normalCategories)
		if err != nil {
			return nil, err
		}
		releases = append(releases, rels...)
	}
	if len(adultCategories) > 0 {
		rels, err := m.fetchTrackerReleases(ctx, "adult", query, adultCategories)
		if err != nil {
			return nil, err
		}
		releases = append(releases, rels...)
	}

	sort.SliceStable(releases, func(i, j int) bool {
		return releases[i].PublishDate.After(releases[j].PublishDate)
	})
	return releases, nil
}

// fetchTrackerReleases ports upstream FetchTrackerReleasesAsync.
func (m *MTeamTp) fetchTrackerReleases(ctx context.Context, requestType string, query *models.TorznabQuery, categories []int) ([]*models.ReleaseInfo, error) {
	if m.Client == nil {
		return nil, errors.New("http client not configured")
	}

	searchQuery := mteamtpSearchQuery{
		Mode:       requestType,
		PageNumber: 1,
		PageSize:   100,
	}
	for _, c := range categories {
		searchQuery.Categories = append(searchQuery.Categories, strconv.Itoa(c))
	}

	if strings.TrimSpace(query.ImdbID) != "" {
		searchQuery.Imdb = strings.TrimSpace(query.ImdbID)
	}

	searchTerm := query.GetQueryString()
	if strings.TrimSpace(searchTerm) != "" {
		searchQuery.Keyword = searchTerm
	}

	if m.cfg.FreeleechOnly.Value {
		searchQuery.Discount = "FREE"
	}

	body, err := searchQuery.wireFormat()
	if err != nil {
		return nil, err
	}

	apiBase := m.apiBaseURL()
	res, err := m.Client.Do(ctx, m.ID(), &httpclient.Request{
		Method: http.MethodPost,
		URL:    apiBase + "api/torrent/search",
		Headers: map[string]string{
			"Accept":       "application/json",
			"Content-Type": "application/json",
			"x-api-key":    m.cfg.ApiKey.Value,
		},
		RawBody: body,
	})
	if err != nil {
		return nil, err
	}

	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Unknown status code: %d (%s)", res.StatusCode, shared.StatusName(res.StatusCode)) //nolint:staticcheck // upstream MTeamTp.cs error text, kept 1:1
	}

	var jsonResponse mteamtpAPIResponse
	if err := json.Unmarshal(res.Body, &jsonResponse); err != nil {
		return nil, errors.New("Invalid response received from M-Team, not a valid JSON") //nolint:staticcheck // upstream MTeamTp.cs error text, kept 1:1
	}

	if jsonResponse.Data == nil || jsonResponse.Data.Torrents == nil {
		if strings.TrimSpace(jsonResponse.Message) != "" &&
			!strings.EqualFold(jsonResponse.Message, "SUCCESS") {
			return nil, fmt.Errorf("Invalid response received from M-Team. Response from API: %s", jsonResponse.Message) //nolint:staticcheck // upstream MTeamTp.cs error text, kept 1:1
		}
		return nil, nil
	}

	var releases []*models.ReleaseInfo
	for _, torrent := range jsonResponse.Data.Torrents {
		torrentID, err := strconv.Atoi(torrent.ID)
		if err != nil {
			return nil, err
		}
		infoURL, err := url.Parse(fmt.Sprintf("%sdetail/%d", m.SiteLink(), torrentID))
		if err != nil {
			return nil, err
		}
		downloadURL, err := url.Parse(fmt.Sprintf("%sapi/torrent/genDlToken?id=%d", apiBase, torrentID))
		if err != nil {
			return nil, err
		}

		numFiles, err := strconv.ParseInt(torrent.NumFiles, 10, 64)
		if err != nil {
			return nil, err
		}
		size, err := strconv.ParseInt(torrent.Size, 10, 64)
		if err != nil {
			return nil, err
		}
		grabs, err := strconv.ParseInt(torrent.Status.TimesCompleted, 10, 64)
		if err != nil {
			return nil, err
		}
		seeders, err := strconv.ParseInt(torrent.Status.Seeders, 10, 64)
		if err != nil {
			return nil, err
		}
		leechers, err := strconv.ParseInt(torrent.Status.Leechers, 10, 64)
		if err != nil {
			return nil, err
		}

		discount := strings.ToUpper(torrent.Status.Discount)
		dvf := 1.0
		switch discount {
		case "FREE", "_2X_FREE":
			dvf = 0
		case "PERCENT_50", "_2X_PERCENT_50":
			dvf = 0.5
		case "PERCENT_70":
			dvf = 0.3
		}
		uvf := 1.0
		switch discount {
		case "_2X_FREE", "_2X_PERCENT_50":
			uvf = 2
		}

		peers := seeders + leechers
		minimumRatio := 1.0
		minimumSeedTime := int64(172800) // 2 days

		release := &models.ReleaseInfo{
			Guid:                 infoURL,
			Title:                mteamtpCleanTitle(torrent.Name),
			Details:              infoURL,
			Link:                 downloadURL,
			Category:             m.Caps.Categories.MapTrackerCatToNewznab(torrent.Category),
			Description:          torrent.Description,
			Files:                &numFiles,
			Size:                 &size,
			Grabs:                &grabs,
			Seeders:              &seeders,
			Peers:                &peers,
			DownloadVolumeFactor: &dvf,
			UploadVolumeFactor:   &uvf,
			MinimumRatio:         &minimumRatio,
			MinimumSeedTime:      &minimumSeedTime,
		}

		if torrent.Status.CreatedDate != "" {
			// upstream: DateTime.TryParseExact(createdDate + " +08:00",
			// "yyyy-MM-dd HH:mm:ss zzz", AssumeUniversal)
			if publishDate, err := time.Parse("2006-01-02 15:04:05 -07:00", torrent.Status.CreatedDate+" +08:00"); err == nil {
				release.PublishDate = publishDate
			}
		}

		releases = append(releases, release)
	}
	return releases, nil
}

// mteamtpMultiSpaceRe ports upstream CleanTitle's @"\s+" regex.
var mteamtpMultiSpaceRe = regexp.MustCompile(`\s+`)

// mteamtpCleanTitle ports upstream CleanTitle.
func mteamtpCleanTitle(title string) string {
	return strings.Trim(mteamtpMultiSpaceRe.ReplaceAllString(title, " "), " ")
}

// --- upstream DTOs ----------------------------------------------------------

type mteamtpSearchQuery struct {
	Mode       string   // "normal" | "adult" (upstream enum, camelCase string)
	Categories []string `json:"categories"`
	Discount   string   `json:"discount,omitempty"`
	Imdb       string   `json:"imdb,omitempty"`
	Keyword    string   `json:"keyword,omitempty"`
	PageNumber int      `json:"pageNumber"`
	PageSize   int      `json:"pageSize"`
}

// wireFormat renders the query exactly like upstream STJson.ToJson
// (WriteIndented, 2-space indent, nulls omitted).
func (q mteamtpSearchQuery) wireFormat() (string, error) {
	quote := func(s string) (string, error) {
		b, err := json.Marshal(s)
		return string(b), err
	}
	var sb strings.Builder
	sb.WriteString("{\n")
	sb.WriteString(`  "mode": `)
	mode, err := quote(q.Mode)
	if err != nil {
		return "", err
	}
	sb.WriteString(mode)
	sb.WriteString(",\n")
	sb.WriteString(`  "categories": `)
	if len(q.Categories) == 0 {
		sb.WriteString("[]")
	} else {
		sb.WriteString("[\n")
		for i, c := range q.Categories {
			if i > 0 {
				sb.WriteString(",\n")
			}
			qc, err := quote(c)
			if err != nil {
				return "", err
			}
			sb.WriteString("    ")
			sb.WriteString(qc)
		}
		sb.WriteString("\n  ]")
	}
	if q.Discount != "" {
		qd, err := quote(q.Discount)
		if err != nil {
			return "", err
		}
		sb.WriteString(",\n  \"discount\": ")
		sb.WriteString(qd)
	}
	if q.Imdb != "" {
		qi, err := quote(q.Imdb)
		if err != nil {
			return "", err
		}
		sb.WriteString(",\n  \"imdb\": ")
		sb.WriteString(qi)
	}
	if q.Keyword != "" {
		qk, err := quote(q.Keyword)
		if err != nil {
			return "", err
		}
		sb.WriteString(",\n  \"keyword\": ")
		sb.WriteString(qk)
	}
	sb.WriteString(",\n  \"pageNumber\": ")
	sb.WriteString(strconv.Itoa(q.PageNumber))
	sb.WriteString(",\n  \"pageSize\": ")
	sb.WriteString(strconv.Itoa(q.PageSize))
	sb.WriteString("\n}")
	return sb.String(), nil
}

type mteamtpAPIResponse struct {
	Data    *mteamtpAPIData `json:"data"`
	Message string          `json:"message"`
}

type mteamtpAPIData struct {
	Torrents []mteamtpAPITorrent `json:"data"`
}

type mteamtpAPITorrent struct {
	ID          string                `json:"id"`
	Name        string                `json:"name"`
	Description string                `json:"smallDescr"`
	Category    string                `json:"category"`
	NumFiles    string                `json:"numfiles"`
	Imdb        string                `json:"imdb"`
	Size        string                `json:"size"`
	Status      mteamtpAPIReleaseStat `json:"status"`
}

type mteamtpAPIReleaseStat struct {
	CreatedDate    string `json:"createdDate"`
	Discount       string `json:"discount"`
	TimesCompleted string `json:"timesCompleted"`
	Seeders        string `json:"seeders"`
	Leechers       string `json:"leechers"`
}

type mteamtpDownloadTokenResponse struct {
	Data string `json:"data"`
}
