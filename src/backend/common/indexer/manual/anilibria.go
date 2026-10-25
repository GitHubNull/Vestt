// Package manual holds the hand-written indexers (one file per site).
package manual

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"vestt/common/httpclient"
	"vestt/common/indexer"
	"vestt/common/indexer/configdata"
	"vestt/common/models"
	"vestt/common/services"
)

// Anilibria ports Jackett.Common/Indexers/Definitions/Anilibria.cs.
//
// mechanism: json-api
//
// Ported from: Definitions/Anilibria.cs.
type Anilibria struct {
	indexer.Base
	cfg *anilibriaConfig
}

type anilibriaConfig struct {
	configdata.ConfigData
	AddRussianToTitle *configdata.BoolItem
	AddSeasonToTitle  *configdata.BoolItem
	EnglishTitleOnly  *configdata.BoolItem
}

func newAnilibriaConfig() *anilibriaConfig {
	return &anilibriaConfig{
		ConfigData:        *configdata.NewConfigData(),
		AddRussianToTitle: configdata.NewBoolItem("Add RUS to end of all titles to improve language detection by Sonarr. Will cause English-only results to be misidentified."),
		AddSeasonToTitle:  configdata.NewBoolItem("Improve Sonarr compatibility by trying to better parse Season information in release titles."),
		EnglishTitleOnly:  configdata.NewBoolItem("Improve Sonarr compatibility by returning the English Title instead of the Russian title."),
	}
}

const anilibriaDefaultRSSLimit = 15

// NewAnilibria builds the indexer (services.Factory shape).
func NewAnilibria(deps services.Deps) (indexer.Indexer, error) {
	a := &Anilibria{cfg: newAnilibriaConfig()}
	a.Base = indexer.Base{
		IDValue:          "anilibria",
		NameValue:        "Anilibria",
		DescriptionValue: "Anilibria is a russian-language anime distribution platform",
		SiteLinkValue:    "https://aniliberty.top/",
		DefaultSiteLink:  "https://aniliberty.top/",
		LegacySiteLinks:  []string{"https://www.anilibria.tv/", "https://anilibria.top/"},
		LangValue:        "ru-RU",
		TypeValue:        "public",
		Caps:             anilibriaCaps(),
		Config:           a.cfg,
		Client:           deps.Client,
		Saver:            deps.Saver,
		Cache:            deps.Cache,
	}
	// upstream: webclient.requestDelay = 2.1 (avoid DDoS-Guard).
	if deps.Client != nil {
		deps.Client.SetRequestDelay(a.IDValue, 2100*time.Millisecond)
	}
	return a, nil
}

func anilibriaCaps() *models.Capabilities {
	caps := models.NewCapabilities()
	caps.TVSearchParams = []string{models.TVParamQ, models.TVParamSeason, models.TVParamEp}
	c := caps.Categories
	c.AddCategoryMapping("TV", models.CatTVAnime, "Аниме TV")
	c.AddCategoryMapping("MOVIE", models.CatTVAnime, "Аниме Фильмы")
	c.AddCategoryMapping("OVA", models.CatTVAnime, "Аниме OVA")
	c.AddCategoryMapping("ONA", models.CatTVAnime, "Аниме ONA")
	c.AddCategoryMapping("SPECIAL", models.CatTVAnime, "Аниме Спешл")
	c.AddCategoryMapping("WEB", models.CatTVAnime, "Аниме WEB")
	c.AddCategoryMapping("OAD", models.CatTVAnime, "Аниме OAD")
	c.AddCategoryMapping("DORAMA", models.CatTV, "Дорамы")
	return caps
}

func (a *Anilibria) apiBase() string { return a.SiteLink() + "api/v1/" }

// ApplyConfiguration ports upstream: load values, test-query the API, mark
// configured and save; an empty result set is an error ("API unavailable or
// unknown error").
func (a *Anilibria) ApplyConfiguration(ctx context.Context, items []indexer.ConfigItem) (indexer.ConfigStatus, error) {
	if err := a.ApplyItems(items); err != nil {
		return indexer.ConfigCompleted, err
	}
	if err := a.LoadValuesFromJSON(nil, false); err != nil {
		return indexer.ConfigCompleted, err
	}
	a.SetConfigured(false)
	results, err := a.perform(ctx, models.NewTorznabQuery())
	if err != nil {
		return indexer.ConfigCompleted, err
	}
	if len(results) == 0 {
		return indexer.ConfigCompleted, errors.New("API unavailable or unknown error")
	}
	a.SetConfigured(true)
	a.SaveConfig(ctx)
	return indexer.ConfigCompleted, nil
}

func (a *Anilibria) ResultsForQuery(ctx context.Context, q *models.TorznabQuery, isMeta bool) (*indexer.IndexerResult, error) {
	return a.ResultsForQueryWith(ctx, q, isMeta, a.perform)
}

func (a *Anilibria) Test(ctx context.Context) error {
	return a.Base.Test(ctx, a.perform)
}

func (a *Anilibria) perform(ctx context.Context, query *models.TorznabQuery) ([]*models.ReleaseInfo, error) {
	if strings.TrimSpace(query.GetQueryString()) != "" {
		return a.searchReleases(ctx, query)
	}
	return a.returnLastReleases(ctx, query)
}

func (a *Anilibria) returnLastReleases(ctx context.Context, query *models.TorznabQuery) ([]*models.ReleaseInfo, error) {
	limit := anilibriaDefaultRSSLimit
	if query.IsTest || !a.IsConfigured() {
		limit = 1
	}
	res, err := a.get(ctx, a.apiBase()+"anime/torrents/rss?limit="+strconv.Itoa(limit))
	if err != nil {
		return nil, err
	}
	torrentIDs, err := rssTorrentIDs(res.Body)
	if err != nil {
		return nil, err
	}
	var releases []*models.ReleaseInfo
	for _, id := range torrentIDs {
		torrentsURL := a.apiBase() + "anime/torrents/" + id
		torrentsRes, err := a.get(ctx, torrentsURL)
		if err != nil {
			a.log().Error("Anilibria: Failed to load url [" + torrentsURL + "]: " + err.Error())
			continue
		}
		releases = append(releases, a.mapToReleaseInfo(torrentsRes.Body)...)
	}
	return releases, nil
}

func (a *Anilibria) searchReleases(ctx context.Context, query *models.TorznabQuery) ([]*models.ReleaseInfo, error) {
	searchQuery := escapeDataString(query.GetQueryString())
	searchRes, err := a.get(ctx, a.apiBase()+"app/search/releases?query="+searchQuery)
	if err != nil {
		return nil, err
	}
	var searchResults []struct {
		ID *int64 `json:"id"`
	}
	if err := json.Unmarshal(searchRes.Body, &searchResults); err != nil {
		return nil, err
	}
	seen := map[int64]bool{}
	var ids []int64
	for _, r := range searchResults {
		if r.ID != nil && !seen[*r.ID] {
			seen[*r.ID] = true
			ids = append(ids, *r.ID)
		}
	}
	var releases []*models.ReleaseInfo
	for _, id := range ids {
		torrentsURL := a.apiBase() + "anime/torrents/release/" + strconv.FormatInt(id, 10)
		torrentsRes, err := a.get(ctx, torrentsURL)
		if err != nil {
			a.log().Error("Anilibria: Failed to load url [" + torrentsURL + "]: " + err.Error())
			continue
		}
		releases = append(releases, a.mapToReleaseInfo(torrentsRes.Body)...)
	}
	return releases, nil
}

func (a *Anilibria) mapToReleaseInfo(body []byte) []*models.ReleaseInfo {
	var torrents []anilibriaTorrent
	trimmed := strings.TrimSpace(string(body))
	if strings.HasPrefix(trimmed, "[") {
		if err := json.Unmarshal(body, &torrents); err != nil {
			return nil
		}
	} else {
		var single anilibriaTorrent
		if err := json.Unmarshal(body, &single); err != nil {
			return nil
		}
		torrents = []anilibriaTorrent{single}
	}
	releases := make([]*models.ReleaseInfo, 0, len(torrents))
	for _, t := range torrents {
		category := t.Release.Type.Value
		if strings.TrimSpace(category) == "" {
			category = "TV"
		}
		var title string
		if a.cfg.EnglishTitleOnly.Value {
			title = t.Release.Name.English + " " + a.formatLabel(t.Label)
		} else {
			title = t.Release.Name.Main + " / " + a.formatLabel(t.Label)
		}
		guid, _ := url.Parse(a.SiteLink() + "anime/releases/release/" + t.Release.Alias + "/" + t.Hash)
		link, _ := url.Parse(a.apiBase() + "anime/torrents/" + t.Hash + "/file")
		details, _ := url.Parse(a.SiteLink() + "anime/releases/release/" + t.Release.Alias)
		var poster *url.URL
		if t.Release.Poster.Original != "" {
			poster, _ = url.Parse(a.SiteLink() + strings.TrimLeft(t.Release.Poster.Original, "/"))
		}
		size := t.Size
		peers := t.Seeders + t.Leechers
		grabs := t.Grabs
		dvf := 0.0
		uvf := 1.0
		releases = append(releases, &models.ReleaseInfo{
			Guid:                 guid,
			Link:                 link,
			Details:              details,
			Title:                title,
			Category:             a.Caps.Categories.MapTrackerCatToNewznab(category),
			Year:                 t.Release.Year,
			InfoHash:             t.Hash,
			Size:                 &size,
			Seeders:              &t.Seeders,
			Peers:                &peers,
			Grabs:                &grabs,
			PublishDate:          t.UpdateAt,
			DownloadVolumeFactor: &dvf,
			UploadVolumeFactor:   &uvf,
			Poster:               poster,
		})
	}
	return releases
}

func (a *Anilibria) formatLabel(label string) string {
	season, episodes := parseSeasonEpisodes(label)
	seasonPart := ""
	if a.cfg.AddSeasonToTitle.Value {
		seasonPart = season
	}
	rus := ""
	if a.cfg.AddRussianToTitle.Value {
		rus = " RUS"
	}
	return seasonPart + episodes + " " + label + " " + rus
}

// log is a nil-safe logger for the manual package (Base.Logger may be nil).
func (a *Anilibria) log() *slog.Logger {
	if a.Logger != nil {
		return a.Logger
	}
	return slog.Default()
}

func (a *Anilibria) get(ctx context.Context, target string) (*httpclient.Result, error) {
	if a.Client == nil {
		return nil, errors.New("http client not configured")
	}
	res, err := a.Client.Do(ctx, a.ID(), &httpclient.Request{
		URL:     target,
		Cookies: a.CookieHeader(),
	})
	if err != nil {
		return nil, err
	}
	if res.StatusCode != http.StatusOK {
		return nil, &httpclient.ErrSiteDown{URL: target, Status: res.StatusCode}
	}
	return res, nil
}

// rssTorrentIDs mirrors XDocument.Descendants("torrentId") (any depth).
func rssTorrentIDs(body []byte) ([]string, error) {
	decoder := xml.NewDecoder(strings.NewReader(string(body)))
	var ids []string
	for {
		tok, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		if se, ok := tok.(xml.StartElement); ok && se.Name.Local == "torrentId" {
			var value string
			if err := decoder.DecodeElement(&value, &se); err != nil {
				return nil, err
			}
			ids = append(ids, value)
		}
	}
	return ids, nil
}

func escapeDataString(s string) string {
	return strings.ReplaceAll(url.QueryEscape(s), "+", "%20")
}

// --- upstream DTOs ----------------------------------------------------------

type anilibriaTorrent struct {
	Release   anilibriaTorrentRelease `json:"release"`
	Label     string                  `json:"label"`
	Hash      string                  `json:"hash"`
	Size      int64                   `json:"size"`
	Seeders   int64                   `json:"seeders"`
	Leechers  int64                   `json:"leechers"`
	Grabs     int64                   `json:"completed_times"`
	CreatedAt time.Time               `json:"created_at"`
	UpdateAt  time.Time               `json:"updated_at"`
}

type anilibriaTorrentRelease struct {
	ID     int64                         `json:"id"`
	Type   anilibriaTorrentReleaseType   `json:"type"`
	Name   anilibriaTorrentReleaseName   `json:"name"`
	Alias  string                        `json:"alias"`
	Year   *int64                        `json:"year"`
	Poster anilibriaTorrentReleasePoster `json:"poster"`
}

type anilibriaTorrentReleaseType struct {
	Value string `json:"value"`
}

type anilibriaTorrentReleaseName struct {
	Main    string `json:"main"`
	English string `json:"english"`
}

type anilibriaTorrentReleasePoster struct {
	Original string `json:"src"`
}

// --- season/episode title parsing -------------------------------------------

var (
	seasonYearParenRe = regexp.MustCompile(`\(\d{4}\)`)
	seasonYearEndRe   = regexp.MustCompile(`\b\d{4}\b$`)
	seasonPartNumRe   = regexp.MustCompile(`(?i)\bPart\s+\d+\b`)
	seasonNumberRe    = regexp.MustCompile(`(?i)\b(?:Season|S|Series)\s*(?P<season_number>\d+)|\b(?P<season_number2>\d+)(?:st|nd|rd|th)?\s*Season\b|\b(?P<roman_number>M{0,4}(CM|CD|D?C{0,3})(XC|XL|L?X{0,3})(IX|IV|V?I{0,3}))\b|\b(?P<season_number3>\d+)\b`)
	episodesRe        = regexp.MustCompile(`(\d+)(?:[-–—](\d+))?`)
)

// parseSeasonEpisodes ports Anilibria.ParseSeasonEpisodes (static).
func parseSeasonEpisodes(title string) (string, string) {
	firstBracket := strings.Index(title, "[")
	lastBracket := strings.LastIndex(title, "]")
	seasonPart := title
	if firstBracket >= 0 {
		seasonPart = title[:firstBracket]
	}
	episodesPart := ""
	if lastBracket > firstBracket && firstBracket >= 0 {
		episodesPart = title[strings.LastIndex(title, "[")+1 : lastBracket]
	}
	seasonPart = seasonYearParenRe.ReplaceAllString(seasonPart, "")
	seasonPart = seasonYearEndRe.ReplaceAllString(seasonPart, "")
	hasPartNumber := seasonPartNumRe.MatchString(seasonPart)
	season := "S01"
	if m := seasonNumberRe.FindStringSubmatch(seasonPart); m != nil && !hasPartNumber {
		if n := firstNamedInt(m, "season_number", "season_number2", "season_number3"); n > 0 {
			season = "S" + pad2(n)
		} else if roman := firstNamed(m, "roman_number"); roman != "" {
			season = "S" + pad2(romanToArabic(roman))
		}
	}
	episodes := ""
	if m := episodesRe.FindStringSubmatch(episodesPart); m != nil {
		start, _ := strconv.Atoi(m[1])
		if m[2] != "" {
			end, _ := strconv.Atoi(m[2])
			episodes = "E" + pad2(start) + "-E" + pad2(end)
		} else {
			episodes = "E" + pad2(start)
		}
	}
	return season, episodes
}

func pad2(n int) string {
	if n < 10 {
		return "0" + strconv.Itoa(n)
	}
	return strconv.Itoa(n)
}

func firstNamed(m []string, names ...string) string {
	// submatch index lookup via the regexp's group names
	for _, name := range names {
		for i, n := range seasonNumberRe.SubexpNames() {
			if n == name && i < len(m) && m[i] != "" {
				return m[i]
			}
		}
	}
	return ""
}

func firstNamedInt(m []string, names ...string) int {
	v := firstNamed(m, names...)
	if v == "" {
		return 0
	}
	n, _ := strconv.Atoi(v)
	return n
}

// romanToArabic ports Anilibria.RomanToArabic (static).
func romanToArabic(roman string) int {
	roman = strings.ToUpper(roman)
	values := []int{1000, 900, 500, 400, 100, 90, 50, 40, 10, 9, 5, 4, 1}
	numerals := []string{"M", "CM", "D", "CD", "C", "XC", "L", "XL", "X", "IX", "V", "IV", "I"}
	result := 0
	i := 0
	for len(roman) > 0 {
		if strings.HasPrefix(roman, numerals[i]) {
			result += values[i]
			roman = roman[len(numerals[i]):]
		} else {
			i++
		}
	}
	return result
}
