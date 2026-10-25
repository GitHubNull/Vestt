// Package manual holds the hand-written indexers (one file per site).
package manual

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"vestt/common/httpclient"
	"vestt/common/indexer"
	"vestt/common/indexer/configdata"
	"vestt/common/indexer/shared"
	"vestt/common/models"
	"vestt/common/services"
	"vestt/common/utils/parseutil"
)

// NebulanceAPI ports Jackett.Common/Indexers/Definitions/NebulanceAPI.cs.
//
// mechanism: json-api
//
// 澄清记录:
//   - upstream declares the two help DisplayInfo items via AddDynamic with an
//     EMPTY name (both would serialize with id ""); the Go port keeps the
//     AddDynamic registration because configdata.Collect dedupes declared
//     fields by id and would silently drop the second empty-id item.
//   - upstream maps a plain Exception for the "Invalid params" body; per
//     SPEC-09 §4 the Go port returns IndexerError{Kind: Config}
//     ("Invalid API Key configured") and IndexerError{Kind: Auth} for API
//     error payloads / non-OK statuses, preserving upstream messages.
//   - upstream NebulanceResponse uses System.Text.Json camelCase-insensitive
//     names via STJson; the Go DTO tags match the API wire names.
//
// Ported from: Definitions/NebulanceAPI.cs,
// Models/IndexerConfig/ConfigurationDataAPIKey.cs.
type NebulanceAPI struct {
	indexer.Base
	cfg *nebulanceAPIConfig
}

type nebulanceAPIConfig struct {
	configdata.ConfigData
	Key *configdata.StringItem
}

const (
	nebulanceAPIKeyLength = 32
	nebulanceAPIKeyInfo   = "Generate a new key by accessing your account profile settings at <a href=\"https://nebulance.io/\" target=_blank>Nebulance</a>, scroll down to the <b>API Keys</b> section, tick the <i>New Key</i>, <i>list</i> and <i>download</i> checkboxes and save."
	nebulanceAPILoginReq  = "You must meet the login requirements: VPN with 2FA or ISP in home country."
)

// NewNebulanceAPI builds the indexer (services.Factory shape).
func NewNebulanceAPI(deps services.Deps) (indexer.Indexer, error) {
	n := &NebulanceAPI{cfg: &nebulanceAPIConfig{ConfigData: *configdata.NewConfigData(), Key: configdata.NewStringItem("APIKey")}}
	n.Base = indexer.Base{
		IDValue:          "nebulanceapi",
		NameValue:        "NebulanceAPI",
		DescriptionValue: "Nebulance is a Private site. At Nebulance we will change the way you think about TV. Using API.",
		SiteLinkValue:    "https://nebulance.io/",
		DefaultSiteLink:  "https://nebulance.io/",
		LangValue:        "en-US",
		TypeValue:        "private",
		Caps:             nebulanceAPICaps(),
		Config:           n.cfg,
		Client:           deps.Client,
		Saver:            deps.Saver,
		Cache:            deps.Cache,
	}
	// upstream constructor: AddDynamic("keyInfo", …) / ("loginRequirements", …)
	n.cfg.AddDynamic("keyInfo", configdata.NewInfoItem("", nebulanceAPIKeyInfo))
	n.cfg.AddDynamic("loginRequirements", configdata.NewInfoItem("", nebulanceAPILoginReq))
	return n, nil
}

func nebulanceAPICaps() *models.Capabilities {
	caps := models.NewCapabilities()
	def, max := 100, 1000
	caps.LimitsDefault = &def
	caps.LimitsMax = &max
	caps.TVSearchParams = []string{models.TVParamQ, models.TVParamSeason, models.TVParamEp, models.TVParamTvmazeID}
	caps.SupportsRawSearch = true

	c := caps.Categories
	c.AddCategoryMapping("tv", models.CatTV, "tv")
	c.AddCategoryMapping("sd", models.CatTVSD, "sd")
	c.AddCategoryMapping("hd", models.CatTVHD, "hd")
	c.AddCategoryMapping("uhd", models.CatTVUHD, "uhd")
	c.AddCategoryMapping("4k", models.CatTVUHD, "4k")
	c.AddCategoryMapping("480p", models.CatTVSD, "480p")
	c.AddCategoryMapping("720p", models.CatTVHD, "720p")
	c.AddCategoryMapping("1080p", models.CatTVHD, "1080p")
	c.AddCategoryMapping("1080i", models.CatTVHD, "1080i")
	c.AddCategoryMapping("2160p", models.CatTVUHD, "2160p")
	return caps
}

// ApplyConfiguration ports upstream: validate the key length, test-query the
// API, mark configured and save (upstream wraps failures in
// ExceptionWithConfigData — the message is preserved verbatim).
func (n *NebulanceAPI) ApplyConfiguration(ctx context.Context, items []indexer.ConfigItem) (indexer.ConfigStatus, error) {
	if err := n.ApplyItems(items); err != nil {
		return indexer.ConfigCompleted, err
	}
	if err := n.LoadValuesFromJSON(nil, false); err != nil {
		return indexer.ConfigCompleted, err
	}

	n.SetConfigured(false)
	if len(n.cfg.Key.Value) != nebulanceAPIKeyLength {
		return indexer.ConfigCompleted, fmt.Errorf("Invalid API Key configured: expected length: %d, got %d", nebulanceAPIKeyLength, len(n.cfg.Key.Value)) //nolint:staticcheck // upstream NebulanceAPI.cs error text, kept 1:1
	}

	results, err := n.perform(ctx, models.NewTorznabQuery())
	if err != nil {
		return indexer.ConfigCompleted, err
	}
	if len(results) == 0 {
		return indexer.ConfigCompleted, errors.New("Testing returned no results!") //nolint:staticcheck // upstream NebulanceAPI.cs error text, kept 1:1
	}

	n.SetConfigured(true)
	n.SaveConfig(ctx)
	return indexer.ConfigCompleted, nil
}

func (n *NebulanceAPI) ResultsForQuery(ctx context.Context, q *models.TorznabQuery, isMeta bool) (*indexer.IndexerResult, error) {
	return n.ResultsForQueryWith(ctx, q, isMeta, n.perform)
}

func (n *NebulanceAPI) Test(ctx context.Context) error {
	return n.Base.Test(ctx, n.perform)
}

// nblValidCategories mirrors upstream _validCategories (order matters:
// HashSet.Intersect enumerates the first operand's insertion order).
var nblValidCategories = []string{
	"sd",
	"hd",
	"uhd",
	"4k",
	"480p",
	"720p",
	"1080i",
	"1080p",
	"2160p",
}

// nblValidTags mirrors upstream _validTags (same Intersect order note).
var nblValidTags = []string{
	"action",
	"adventure",
	"children",
	"biography",
	"comedy",
	"crime",
	"documentary",
	"drama",
	"family",
	"fantasy",
	"game-show",
	"history",
	"horror",
	"medical",
	"music",
	"musical",
	"mystery",
	"news",
	"reality-tv",
	"romance",
	"sci-fi",
	"sitcom",
	"sport",
	"talk-show",
	"thriller",
	"travel",
	"war",
	"western",
}

func (n *NebulanceAPI) perform(ctx context.Context, query *models.TorznabQuery) ([]*models.ReleaseInfo, error) {
	limit := query.Limit
	if limit == 0 {
		limit = 100 // upstream: _torznabCaps.LimitsDefault.GetValueOrDefault(100)
	}

	// buildSearchRequests ports upstream NebulanceAPIRequestGenerator.
	queryParams := shared.QueryParams{}
	queryParams.Add("action", "search")
	queryParams.Add("api_key", n.cfg.Key.Value)
	queryParams.Add("age", ">0")
	queryParams.Add("per_page", strconv.Itoa(limit))

	if query.Limit > 0 && query.Offset > 0 {
		// upstream quirk preserved: no +1 (page = offset/limit)
		page := query.Offset / query.Limit
		queryParams.Add("page", strconv.Itoa(page))
	}

	if query.IsTvmazeQuery() && query.TvmazeID != nil {
		queryParams.Set("tvmaze", strconv.Itoa(*query.TvmazeID))
	}

	searchQuery := strings.TrimSpace(query.SanitizedSearchTerm())

	if strings.TrimSpace(searchQuery) != "" {
		queryParams.Set("release", searchQuery)
	}

	if query.Season != nil &&
		strings.TrimSpace(query.Episode) != "" &&
		nblIsDailyDate(*query.Season, query.Episode) {
		if strings.TrimSpace(searchQuery) != "" {
			queryParams.Set("name", searchQuery)
		}
		queryParams.Set("release", nblDailyDate(*query.Season, query.Episode))
	} else {
		if query.Season != nil {
			queryParams.Set("season", strconv.Itoa(*query.Season))
		}
		if ep, err := strconv.Atoi(strings.TrimSpace(query.Episode)); err == nil {
			queryParams.Set("episode", strconv.Itoa(ep))
		}
	}

	get := func(key string) string {
		for _, kv := range queryParams {
			if kv.Key == key {
				return kv.Value
			}
		}
		return ""
	}

	if (strings.TrimSpace(get("season")) != "" || strings.TrimSpace(get("episode")) != "") &&
		strings.TrimSpace(get("name")) == "" &&
		strings.TrimSpace(get("release")) == "" &&
		strings.TrimSpace(get("tvmaze")) == "" {
		n.log().Warn("NBL API does not support season calls without name, series, id, imdb, tvmaze, or time keys.")

		return nil, nil // upstream: empty IndexerPageableRequestChain
	}

	if l := len(get("name")); l > 0 && l < 3 || len(get("release")) > 0 && len(get("release")) < 3 {
		n.log().Warn("NBL API does not support release calls that are 2 characters or fewer.")

		return nil, nil // upstream: empty IndexerPageableRequestChain
	}

	searchURL := n.SiteLink() + "api.php?" + queryParams.GetQueryString()

	if n.Client == nil {
		return nil, errors.New("http client not configured")
	}
	res, err := n.Client.Do(ctx, n.ID(), &httpclient.Request{
		URL:            searchURL,
		Headers:        map[string]string{"Accept": "application/json"},
		Cookies:        n.CookieHeader(),
		EmulateBrowser: httpclient.Bool(false), // upstream WebRequest EmulateBrowser = false
	})
	if err != nil {
		return nil, err
	}

	return n.parseResponse(res)
}

// nblIsDailyDate mirrors DateTime.TryParseExact($"{season} {episode}",
// "yyyy MM/dd", InvariantCulture) — a daily-show airdate query.
func nblIsDailyDate(season int, episode string) bool {
	_, err := time.Parse("2006 01/02", fmt.Sprintf("%d %s", season, episode))
	return err == nil
}

// nblDailyDate formats the parsed daily date as yyyy.MM.dd.
func nblDailyDate(season int, episode string) string {
	t, err := time.Parse("2006 01/02", fmt.Sprintf("%d %s", season, episode))
	if err != nil {
		return ""
	}
	return t.Format("2006.01.02")
}

// parseResponse ports upstream NebulanceAPIParser.ParseResponse.
func (n *NebulanceAPI) parseResponse(res *httpclient.Result) ([]*models.ReleaseInfo, error) {
	if res.StatusCode != http.StatusOK {
		return nil, &indexer.IndexerError{
			IndexerID: n.ID(),
			Kind:      indexer.KindAuth,
			Message:   fmt.Sprintf("Unexpected response status '%d' code from indexer request. Check the logs for more information.", res.StatusCode),
		}
	}

	content := string(res.Body)
	if strings.Contains(content, "Invalid params") {
		return nil, &indexer.IndexerError{IndexerID: n.ID(), Kind: indexer.KindConfig, Message: "Invalid API Key configured"}
	}
	if strings.Contains(content, "API is down") {
		return nil, errors.New("NBL API is down at the moment")
	}

	var jsonResponse nebulanceResponse
	if err := json.Unmarshal(res.Body, &jsonResponse); err != nil {
		return nil, fmt.Errorf("Unexpected response from indexer request: %s", err.Error()) //nolint:staticcheck // upstream NebulanceAPI.cs error text, kept 1:1
	}

	if jsonResponse.Error != nil {
		return nil, &indexer.IndexerError{
			IndexerID: n.ID(),
			Kind:      indexer.KindAuth,
			Message:   fmt.Sprintf("Indexer API call returned an error [%s]", jsonResponse.Error.Message),
		}
	}

	if jsonResponse.TotalResults == 0 || len(jsonResponse.Items) == 0 {
		return nil, nil
	}

	var releases []*models.ReleaseInfo

	for _, row := range jsonResponse.Items {
		release, err := n.mapTorrent(row)
		if err != nil {
			return nil, err
		}
		releases = append(releases, release)
	}

	return releases, nil
}

func (n *NebulanceAPI) mapTorrent(row *nebulanceTorrent) (*models.ReleaseInfo, error) {
	details, err := url.Parse(n.SiteLink() + "torrents.php?id=" + strconv.Itoa(row.TorrentId))
	if err != nil {
		return nil, err
	}

	title := row.ReleaseTitle
	if strings.TrimSpace(title) == "" {
		title = row.GroupName
	}

	tagSet := map[string]bool{}
	for _, t := range row.Tags {
		lower := strings.ToLower(t)
		if !tagSet[lower] {
			tagSet[lower] = true
		}
	}
	var releaseCategories []string
	for _, cat := range nblValidCategories {
		if tagSet[cat] {
			releaseCategories = append(releaseCategories, cat)
		}
	}
	var genres []string
	for _, tag := range nblValidTags {
		if tagSet[tag] {
			genres = append(genres, tag)
		}
	}

	var descriptions []string
	if strings.TrimSpace(row.GroupName) != "" {
		descriptions = append(descriptions, "Group Name: "+row.GroupName)
	}
	if len(genres) > 0 {
		descriptions = append(descriptions, "Genre: "+strings.Join(genres, ", "))
	}

	// upstream: DateTime.Parse(rls_utc, InvariantCulture, AssumeUniversal)
	publishDate, err := nblParsePublishDate(row.PublishDateUtc)
	if err != nil {
		return nil, err
	}

	link, err := url.Parse(row.DownloadLink)
	if err != nil {
		return nil, err
	}

	size := row.Size
	files := int64(len(row.FileList))
	grabs := int64(row.Snatch)
	seeders := int64(row.Seed)
	peers := int64(row.Seed + row.Leech)
	minimumRatio := 0.0 // ratioless
	minimumSeedTime := int64(86400)
	if strings.ToUpper(row.Category) == "SEASON" {
		minimumSeedTime = 432000 // 120 hours for seasons and 24 hours for episodes
	}
	downloadVolumeFactor := 0.0 // ratioless tracker
	uploadVolumeFactor := 1.0

	trackerCategory := "TV"
	if len(releaseCategories) > 0 {
		trackerCategory = releaseCategories[0]
	}

	release := &models.ReleaseInfo{
		Guid:                 details,
		Details:              details,
		Link:                 link,
		Title:                strings.TrimSpace(title),
		Category:             n.Caps.Categories.MapTrackerCatToNewznab(trackerCategory),
		Size:                 &size,
		Files:                &files,
		PublishDate:          publishDate,
		Grabs:                &grabs,
		Seeders:              &seeders,
		Peers:                &peers,
		MinimumRatio:         &minimumRatio,
		MinimumSeedTime:      &minimumSeedTime,
		DownloadVolumeFactor: &downloadVolumeFactor,
		UploadVolumeFactor:   &uploadVolumeFactor,
		Genres:               genres,
		Description:          strings.Join(descriptions, "<br />\n"),
	}

	if strings.TrimSpace(row.ImdbId) != "" {
		if imdb, ok := parseutil.GetImdbID(row.ImdbId); ok {
			release.Imdb = &imdb
		}
	}

	if row.TvMazeId != nil {
		tvMaze := int64(*row.TvMazeId)
		release.TVMazeId = &tvMaze
	}

	return release, nil
}

// nblParsePublishDate mirrors DateTime.Parse(rls_utc, InvariantCulture,
// AssumeUniversal): the UTC timestamp formats the NBL API emits.
func nblParsePublishDate(s string) (time.Time, error) {
	layouts := []string{
		"2006-01-02 15:04:05",
		"2006-01-02T15:04:05",
		time.RFC3339,
	}
	for _, layout := range layouts {
		if t, err := time.ParseInLocation(layout, s, time.UTC); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("Unable to parse rls_utc: %q", s) //nolint:staticcheck // port-authored message (upstream DateTime.Parse throws), kept stable
}

// log is a nil-safe logger for the manual package (Base.Logger may be nil).
func (n *NebulanceAPI) log() *slog.Logger {
	if n.Logger != nil {
		return n.Logger
	}
	return slog.Default()
}

// --- upstream DTOs ----------------------------------------------------------

type nebulanceResponse struct {
	TotalResults int                 `json:"total_results"`
	Items        []*nebulanceTorrent `json:"items"`
	Error        *nebulanceError     `json:"error"`
}

type nebulanceError struct {
	Message string `json:"message"`
}

type nebulanceTorrent struct {
	ReleaseTitle   string   `json:"rls_name"`
	Category       string   `json:"cat"`
	Size           int64    `json:"size"`
	Seed           int      `json:"seed"`
	Leech          int      `json:"leech"`
	Snatch         int      `json:"snatch"`
	DownloadLink   string   `json:"download"`
	FileList       []string `json:"file_list"`
	GroupName      string   `json:"group_name"`
	TorrentId      int      `json:"group_id"`
	ImdbId         string   `json:"imdb_id"`
	TvMazeId       *int     `json:"tvmaze_id"`
	PublishDateUtc string   `json:"rls_utc"`
	Tags           []string `json:"tags"`
}
