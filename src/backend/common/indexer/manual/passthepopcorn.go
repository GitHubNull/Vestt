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
)

// PassThePopcorn ports Jackett.Common/Indexers/Definitions/PassThePopcorn.cs.
//
// mechanism: json-api (ApiUser/ApiKey auth headers)
//
// 澄清记录:
//   - upstream PerformQuery hardcodes "https://passthepopcorn.me/torrents.php"
//     instead of using SiteLink; searchBase keeps that upstream default and
//     doubles as the httptest seam.
//   - upstream wraps the whole parse block in try/catch and only logs via
//     OnParseError, returning the releases accumulated so far; the Go port
//     logs and returns the partial set as well.
//   - upstream has no explicit HTTP status check; the PTP API answers auth
//     failures with a non-200 status and a JSON {"error": ...} body. Per
//     SPEC-09 §4 these map to IndexerError{Kind: Auth} (401/403) or
//     {Kind: Config} (any other non-200), preserving the upstream-flavoured
//     message.
//
// Ported from: Definitions/PassThePopcorn.cs,
// Models/IndexerConfig/ConfigurationDataAPILoginWithUserAndPasskeyAndFilter.cs.
type PassThePopcorn struct {
	indexer.Base
	cfg *passThePopcornConfig

	// searchBase mirrors the upstream hardcoded
	// "https://passthepopcorn.me/" (see 澄清记录 above).
	searchBase string
}

type passThePopcornConfig struct {
	configdata.ConfigData
	KeyHint              *configdata.InfoItem
	User                 *configdata.StringItem
	Key                  *configdata.StringItem
	AddAttributesToTitle *configdata.BoolItem
	FilterExample        *configdata.InfoItem
	FilterString         *configdata.StringItem
}

const passThePopcornFilterInstructions = `Enter filter options below to restrict search results.
                                                                        Separate options with a space if using more than one option.<br>Filter options available:
                                                                        <br><code>GoldenPopcorn</code><br><code>Scene</code><br><code>Checked</code><br><code>Free</code>`

func newPassThePopcornConfig() *passThePopcornConfig {
	return &passThePopcornConfig{
		ConfigData: *configdata.NewConfigData(),
		KeyHint: configdata.NewInfoItem("API Authentication",
			"<ul><li>Visit the security tab on your user settings page to access your ApiUser and ApiKey <li>If you haven't yet generated a key, you may have to first generate one using the checkbox below your keys</ul>"),
		User:                 configdata.NewStringItem("ApiUser"),
		Key:                  configdata.NewStringItem("ApiKey"),
		AddAttributesToTitle: configdata.NewBoolItem("Include release attributes in the title"),
		FilterExample:        configdata.NewInfoItem("", passThePopcornFilterInstructions),
		FilterString:         configdata.NewStringItem("Filters (optional)"),
	}
}

// NewPassThePopcorn builds the indexer (services.Factory shape).
func NewPassThePopcorn(deps services.Deps) (indexer.Indexer, error) {
	p := &PassThePopcorn{cfg: newPassThePopcornConfig(), searchBase: "https://passthepopcorn.me/"}
	p.Base = indexer.Base{
		IDValue:          "passthepopcorn",
		NameValue:        "PassThePopcorn",
		DescriptionValue: "PassThePopcorn (PTP) is a Private site for MOVIES",
		SiteLinkValue:    "https://passthepopcorn.me/",
		DefaultSiteLink:  "https://passthepopcorn.me/",
		LangValue:        "en-US",
		TypeValue:        "private",
		Caps:             passThePopcornCaps(),
		Config:           p.cfg,
		Client:           deps.Client,
		Saver:            deps.Saver,
		Cache:            deps.Cache,
	}
	// upstream: webclient.requestDelay = 4
	if deps.Client != nil {
		deps.Client.SetRequestDelay(p.IDValue, 4*time.Second)
	}
	return p, nil
}

func passThePopcornCaps() *models.Capabilities {
	caps := models.NewCapabilities()
	def, max := 50, 50
	caps.LimitsDefault = &def
	caps.LimitsMax = &max
	caps.MovieSearchParams = []string{models.MovieParamQ, models.MovieParamImdbID}
	c := caps.Categories
	c.AddCategoryMapping("1", models.CatMovies, "Feature Film")
	c.AddCategoryMapping("2", models.CatMovies, "Short Film")
	c.AddCategoryMapping("3", models.CatMovies, "Miniseries")
	c.AddCategoryMapping("4", models.CatMovies, "Stand-up Comedy")
	c.AddCategoryMapping("5", models.CatMovies, "Live Performance")
	c.AddCategoryMapping("6", models.CatMovies, "Movie Collection")
	return caps
}

// ApplyConfiguration ports upstream: load values, test-query the API, mark
// configured and save (upstream wraps failures in ExceptionWithConfigData —
// the message is preserved verbatim).
func (p *PassThePopcorn) ApplyConfiguration(ctx context.Context, items []indexer.ConfigItem) (indexer.ConfigStatus, error) {
	if err := p.ApplyItems(items); err != nil {
		return indexer.ConfigCompleted, err
	}
	if err := p.LoadValuesFromJSON(nil, false); err != nil {
		return indexer.ConfigCompleted, err
	}
	p.SetConfigured(false)
	results, err := p.perform(ctx, models.NewTorznabQuery())
	if err != nil {
		return indexer.ConfigCompleted, err
	}
	if len(results) == 0 {
		return indexer.ConfigCompleted, errors.New("Testing returned no results!") //nolint:staticcheck // upstream PassThePopcorn.cs error text, kept 1:1
	}
	p.SetConfigured(true)
	p.SaveConfig(ctx)
	return indexer.ConfigCompleted, nil
}

func (p *PassThePopcorn) ResultsForQuery(ctx context.Context, q *models.TorznabQuery, isMeta bool) (*indexer.IndexerResult, error) {
	return p.ResultsForQueryWith(ctx, q, isMeta, p.perform)
}

func (p *PassThePopcorn) Test(ctx context.Context) error {
	return p.Base.Test(ctx, p.perform)
}

// Download ports upstream PassThePopcorn.Download: the .torrent fetch is
// authorised with the ApiUser/ApiKey headers.
func (p *PassThePopcorn) Download(ctx context.Context, link *url.URL) ([]byte, error) {
	return p.DownloadWith(ctx, link, http.MethodGet, "", map[string]string{
		"ApiUser": p.cfg.User.Value,
		"ApiKey":  p.cfg.Key.Value,
	})
}

func (p *PassThePopcorn) perform(ctx context.Context, query *models.TorznabQuery) ([]*models.ReleaseInfo, error) {
	filterString := strings.ToLower(p.cfg.FilterString.Value)
	configGoldenPopcornOnly := strings.Contains(filterString, "goldenpopcorn")
	configSceneOnly := strings.Contains(filterString, "scene")
	configCheckedOnly := strings.Contains(filterString, "checked")
	configFreeOnly := strings.Contains(filterString, "free")

	qc := shared.QueryParams{}
	qc.Add("action", "advanced")
	qc.Add("json", "noredirect")
	qc.Add("grouping", "0")
	qc.Add("order_by", "time")
	qc.Add("order_way", "desc")

	if configFreeOnly {
		qc.Set("freetorrent", "1")
	}

	if configGoldenPopcornOnly {
		qc.Set("scene", "2")
	}

	searchQuery := query.ImdbID
	if strings.TrimSpace(searchQuery) == "" {
		searchQuery = query.GetQueryString()
	}
	if strings.TrimSpace(searchQuery) != "" {
		qc.Set("searchstr", searchQuery)
	}

	// upstream: MapTorznabCapsToTrackers(query).Select(int.Parse).Distinct()
	var queryCats []int
	seenCat := map[string]bool{}
	for _, cat := range p.Caps.Categories.MapTorznabCapsToTrackers(query, false) {
		if seenCat[cat] {
			continue
		}
		seenCat[cat] = true
		n, err := strconv.Atoi(cat)
		if err != nil {
			return nil, err
		}
		queryCats = append(queryCats, n)
	}

	if strings.TrimSpace(searchQuery) == "" && len(queryCats) > 0 {
		for _, cat := range queryCats {
			qc.Add(fmt.Sprintf("filter_cat[%d]", cat), "1")
		}
	}

	if query.Limit > 0 && query.Offset > 0 {
		page := query.Offset/query.Limit + 1
		qc.Set("page", strconv.Itoa(page))
	}

	movieListSearchURL := p.searchBase + "torrents.php?" + qc.GetQueryString()

	if p.Client == nil {
		return nil, errors.New("http client not configured")
	}
	res, err := p.Client.Do(ctx, p.ID(), &httpclient.Request{
		URL:     movieListSearchURL,
		Headers: map[string]string{"ApiUser": p.cfg.User.Value, "ApiKey": p.cfg.Key.Value},
		Cookies: p.CookieHeader(),
	})
	if err != nil {
		return nil, err
	}
	if res.StatusCode != http.StatusOK {
		return nil, p.authError(res)
	}

	var jsonResponse passThePopcornResponse
	if err := json.Unmarshal(res.Body, &jsonResponse); err != nil {
		// upstream OnParseError: log only, return what we have (nothing yet)
		p.log().Error(fmt.Sprintf("Error while parsing result: %v\n%s", err, string(res.Body)))
		return nil, nil
	}

	if jsonResponse.TotalResults == "0" ||
		strings.TrimSpace(jsonResponse.TotalResults) == "" ||
		jsonResponse.Movies == nil {
		return nil, nil
	}

	var releases []*models.ReleaseInfo

	for _, result := range jsonResponse.Movies {
		for _, torrent := range result.Torrents {
			if configGoldenPopcornOnly && !torrent.GoldenPopcorn {
				// Skip release if user only wants GoldenPopcorn
				continue
			}
			if configSceneOnly && !torrent.Scene {
				continue
			}
			if configCheckedOnly && !torrent.Checked {
				continue
			}

			freeleechType := strings.ToUpper(torrent.FreeleechType)
			var downloadVolumeFactor float64
			switch freeleechType {
			case "FREELEECH", "NEUTRAL LEECH":
				downloadVolumeFactor = 0
			case "HALF LEECH":
				downloadVolumeFactor = 0.5
			default:
				downloadVolumeFactor = 1
			}

			if configFreeOnly && downloadVolumeFactor != 0.0 {
				continue
			}

			release, err := p.mapTorrent(result, torrent, downloadVolumeFactor)
			if err != nil {
				// upstream try/catch: OnParseError logs and the partial
				// release list is returned.
				p.log().Error(fmt.Sprintf("Error while parsing result: %v\n%s", err, string(res.Body)))
				return releases, nil
			}

			releases = append(releases, release)
		}
	}

	return releases, nil
}

// mapTorrent builds one ReleaseInfo from a movie/torrent pair; a non-nil
// error mirrors an upstream exception inside the parse try-block.
func (p *PassThePopcorn) mapTorrent(result *passThePopcornMovie, torrent *passThePopcornTorrent, downloadVolumeFactor float64) (*models.ReleaseInfo, error) {
	year, err := strconv.ParseInt(strings.TrimSpace(result.Year), 10, 64)
	if err != nil {
		return nil, err
	}
	size, err := strconv.ParseInt(strings.TrimSpace(torrent.Size), 10, 64)
	if err != nil {
		return nil, err
	}
	grabs, err := strconv.ParseInt(strings.TrimSpace(torrent.Snatched), 10, 64)
	if err != nil {
		return nil, err
	}
	seeders, err := strconv.ParseInt(strings.TrimSpace(torrent.Seeders), 10, 64)
	if err != nil {
		return nil, err
	}
	leechers, err := strconv.ParseInt(strings.TrimSpace(torrent.Leechers), 10, 64)
	if err != nil {
		return nil, err
	}

	// upstream: DateTime.Parse(torrent.UploadTime + " +0000", InvariantCulture, AdjustToUniversal)
	publishDate, err := time.Parse("2006-01-02 15:04:05 -0700", torrent.UploadTime+" +0000")
	if err != nil {
		return nil, err
	}

	var imdb int64
	if strings.TrimSpace(result.ImdbId) != "" {
		imdb, err = strconv.ParseInt(strings.TrimSpace(result.ImdbId), 10, 64)
		if err != nil {
			return nil, err
		}
	}

	infoURL := p.getInfoURL(result.GroupId, torrent.Id)
	downloadURL := p.getDownloadURL(torrent.Id)

	var uploadVolumeFactor float64
	if strings.ToUpper(torrent.FreeleechType) == "NEUTRAL LEECH" {
		uploadVolumeFactor = 0
	} else {
		uploadVolumeFactor = 1
	}

	minimumRatio := 1.0
	minimumSeedTime := int64(345600)

	description := ""
	if strings.TrimSpace(result.Year) != "" {
		description += "<br>\nYear: " + result.Year
	}
	if strings.TrimSpace(torrent.Quality) != "" {
		description += "<br>\nQuality: " + torrent.Quality
	}

	var titleTags []string
	if strings.TrimSpace(torrent.Resolution) != "" {
		titleTags = append(titleTags, torrent.Resolution)
		description += "<br>\nResolution: " + torrent.Resolution
	}
	if strings.TrimSpace(torrent.Source) != "" {
		titleTags = append(titleTags, torrent.Source)
		description += "<br>\nSource: " + torrent.Source
	}
	if strings.TrimSpace(torrent.Codec) != "" {
		titleTags = append(titleTags, torrent.Codec)
		description += "<br>\nCodec: " + torrent.Codec
	}
	if strings.TrimSpace(torrent.Container) != "" {
		titleTags = append(titleTags, torrent.Container)
		description += "<br>\nContainer: " + torrent.Container
	}
	if torrent.Scene {
		titleTags = append(titleTags, "Scene")
		description += "<br>\nScene"
	}
	if torrent.Checked {
		titleTags = append(titleTags, "Checked")
		description += "<br>\nChecked"
	}
	if torrent.GoldenPopcorn {
		titleTags = append(titleTags, "Golden Popcorn")
		description += "<br>\nGolden Popcorn"
	}
	if strings.TrimSpace(torrent.RemasterTitle) != "" {
		titleTags = append(titleTags, torrent.RemasterTitle)
	}

	genres := make([]string, 0, len(result.Tags))
	genres = append(genres, result.Tags...)

	peers := leechers + seeders

	release := &models.ReleaseInfo{
		Guid:                 infoURL,
		Title:                torrent.ReleaseName,
		Year:                 &year,
		Details:              infoURL,
		Link:                 downloadURL,
		Category:             p.Caps.Categories.MapTrackerCatToNewznab(result.CategoryId),
		Size:                 &size,
		Grabs:                &grabs,
		Seeders:              &seeders,
		Peers:                &peers,
		PublishDate:          publishDate,
		Imdb:                 &imdb,
		DownloadVolumeFactor: &downloadVolumeFactor,
		UploadVolumeFactor:   &uploadVolumeFactor,
		MinimumRatio:         &minimumRatio,
		MinimumSeedTime:      &minimumSeedTime,
		Genres:               genres,
		Description:          description,
		Poster:               getPTPPosterURL(result.Cover),
	}

	if p.cfg.AddAttributesToTitle.Value && len(titleTags) > 0 {
		release.Title += " [" + strings.Join(titleTags, " / ") + "]"
	}

	return release, nil
}

// authError maps a non-200 PTP API response to an IndexerError
// (SPEC-09 §4: 响应错误码 → Auth/Config).
func (p *PassThePopcorn) authError(res *httpclient.Result) error {
	message := fmt.Sprintf("Remote server returned %d", res.StatusCode)
	var body struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(res.Body, &body); err == nil && strings.TrimSpace(body.Error) != "" {
		message = body.Error
	}
	kind := indexer.KindConfig
	if res.StatusCode == http.StatusUnauthorized || res.StatusCode == http.StatusForbidden {
		kind = indexer.KindAuth
	}
	return &indexer.IndexerError{IndexerID: p.ID(), Kind: kind, Message: message}
}

func (p *PassThePopcorn) getDownloadURL(torrentID int) *url.URL {
	u, _ := url.Parse(p.SiteLink() + "torrents.php?action=download&id=" + strconv.Itoa(torrentID))
	return u
}

func (p *PassThePopcorn) getInfoURL(groupID string, torrentID int) *url.URL {
	u, _ := url.Parse(p.SiteLink() + "torrents.php?id=" + groupID + "&torrentid=" + strconv.Itoa(torrentID))
	return u
}

// getPTPPosterURL mirrors upstream GetPosterUrl: absolute http/https only.
func getPTPPosterURL(cover string) *url.URL {
	if strings.TrimSpace(cover) == "" {
		return nil
	}
	u, err := url.Parse(cover)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return nil
	}
	return u
}

// log is a nil-safe logger for the manual package (Base.Logger may be nil).
func (p *PassThePopcorn) log() *slog.Logger {
	if p.Logger != nil {
		return p.Logger
	}
	return slog.Default()
}

// --- upstream DTOs ----------------------------------------------------------

type passThePopcornResponse struct {
	TotalResults string                 `json:"TotalResults"`
	Movies       []*passThePopcornMovie `json:"Movies"`
}

type passThePopcornMovie struct {
	GroupId    string                   `json:"GroupId"`
	CategoryId string                   `json:"CategoryId"`
	Title      string                   `json:"Title"`
	Year       string                   `json:"Year"`
	ImdbId     string                   `json:"ImdbId"`
	Cover      string                   `json:"Cover"`
	Tags       []string                 `json:"Tags"`
	Torrents   []*passThePopcornTorrent `json:"Torrents"`
}

type passThePopcornTorrent struct {
	Id            int    `json:"Id"`
	Quality       string `json:"Quality"`
	Source        string `json:"Source"`
	Container     string `json:"Container"`
	Codec         string `json:"Codec"`
	Resolution    string `json:"Resolution"`
	Scene         bool   `json:"Scene"`
	Size          string `json:"Size"`
	UploadTime    string `json:"UploadTime"`
	RemasterTitle string `json:"RemasterTitle"`
	Snatched      string `json:"Snatched"`
	Seeders       string `json:"Seeders"`
	Leechers      string `json:"Leechers"`
	ReleaseName   string `json:"ReleaseName"`
	Checked       bool   `json:"Checked"`
	GoldenPopcorn bool   `json:"GoldenPopcorn"`
	FreeleechType string `json:"FreeleechType"`
}
