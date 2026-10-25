package manual

import (
	"bytes"
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

	"github.com/PuerkitoBio/goquery"

	"vestt/common/httpclient"
	"vestt/common/indexer"
	"vestt/common/indexer/configdata"
	"vestt/common/indexer/shared"
	"vestt/common/models"
	"vestt/common/services"
	"vestt/common/utils/parseutil"
)

// Uniotaku ports Jackett.Common/Indexers/Definitions/Uniotaku.cs.
//
// mechanism: html-login (account-login.php form login, DataTables-style
// torrents_.php JSON response whose cells carry HTML fragments, freeleech /
// silver volume factors, Download override scraping download.php?id=)
//
// Ported from: Definitions/Uniotaku.cs,
// Models/IndexerConfig/Bespoke/ConfigurationDataUniotaku.cs.
//
// 澄清记录:
//   - upstream `MapTorznabCapsToTrackers(query).FirstIfSingleOrDefault("0")`
//     uses a Jackett extension method (single mapped category or "0");
//     ported inline in perform.
//   - upstream PublishDate starts at DateTime.Now and is decremented one
//     minute per parsed row (Uniotaku.cs:138,158) — no real date exists in
//     the response; replicated with time.Now().
//   - upstream ConfigureIfOK checks the raw response cookie string for
//     "uid=" and "pass=" (Uniotaku.cs:95); the port inspects the union of the
//     final response Set-Cookie headers and the per-indexer cookie jar
//     (which captured the whole login redirect chain) the same way.
//   - upstream JObject.Parse errors are routed through OnParseError (log +
//     "Parse error"); the port returns an IndexerError of KindParse.
type Uniotaku struct {
	indexer.Base
	cfg *uniotakuConfig
}

// uniotakuConfig ports upstream ConfigurationDataUniotaku
// (ConfigurationDataBasicLogin + Freeleech + SortBy); declared as fields
// instead of base+dynamic items — same registration and ordering.
type uniotakuConfig struct {
	configdata.ConfigData
	Username     *configdata.StringItem
	Password     *configdata.StringItem
	Instructions *configdata.InfoItem
	Freeleech    *configdata.BoolItem
	SortBy       *configdata.SelectItem
}

func newUniotakuConfig() *uniotakuConfig {
	sortBy := configdata.NewSelectItem("Sort By", []configdata.Option{
		{Value: "0", Label: "created"},
		{Value: "3", Label: "seeders"},
		{Value: "9", Label: "size"},
		{Value: "1", Label: "title"},
	})
	sortBy.Value = "0"
	freeleech := configdata.NewBoolItem("Search freeleech only")
	freeleech.Value = false
	return &uniotakuConfig{
		ConfigData:   *configdata.NewConfigData(),
		Username:     configdata.NewStringItem("Username"),
		Password:     configdata.NewStringItem("Password"),
		Instructions: configdata.NewInfoItem("", ""),
		Freeleech:    freeleech,
		SortBy:       sortBy,
	}
}

// NewUniotaku builds the indexer (services.Factory shape).
func NewUniotaku(deps services.Deps) (indexer.Indexer, error) {
	u := &Uniotaku{cfg: newUniotakuConfig()}
	u.Base = indexer.Base{
		IDValue:          "uniotaku",
		NameValue:        "UniOtaku",
		DescriptionValue: "UniOtaku is a BRAZILIAN Semi-Private Torrent Tracker for ANIME",
		SiteLinkValue:    "https://tracker.uniotaku.com/",
		DefaultSiteLink:  "https://tracker.uniotaku.com/",
		LangValue:        "pt-BR",
		TypeValue:        "semi-private",
		Caps:             uniotakuCaps(),
		Config:           u.cfg,
		Client:           deps.Client,
		Saver:            deps.Saver,
		Cache:            deps.Cache,
	}
	return u, nil
}

func uniotakuCaps() *models.Capabilities {
	caps := models.NewCapabilities()
	caps.TVSearchParams = []string{models.TVParamQ, models.TVParamSeason, models.TVParamEp}
	caps.MovieSearchParams = []string{models.MovieParamQ}
	caps.MusicSearchParams = []string{models.MusicParamQ}
	caps.BookSearchParams = []string{models.BookParamQ}
	c := caps.Categories
	c.AddCategoryMapping("28", models.CatTVAnime, "Anime")
	c.AddCategoryMapping("47", models.CatMoviesOther, "Filme")
	c.AddCategoryMapping("48", models.CatTVAnime, "OVA")
	c.AddCategoryMapping("49", models.CatBooksComics, "Mangá")
	c.AddCategoryMapping("50", models.CatTVOther, "Dorama")
	c.AddCategoryMapping("51", models.CatAudio, "OST")
	c.AddCategoryMapping("52", models.CatTVAnime, "Anime Completo")
	c.AddCategoryMapping("53", models.CatBooksComics, "Mangá Completo")
	c.AddCategoryMapping("54", models.CatTVOther, "Dorama Completo")
	c.AddCategoryMapping("55", models.CatXXX, "Hentai")
	c.AddCategoryMapping("56", models.CatXXXOther, "H Doujinshi")
	c.AddCategoryMapping("57", models.CatTVOther, "Tokusatsu")
	c.AddCategoryMapping("58", models.CatTVOther, "Live Action")
	return caps
}

// log is a nil-safe logger (Base.Logger may be nil).
func (u *Uniotaku) log() *slog.Logger {
	if u.Logger != nil {
		return u.Logger
	}
	return slog.Default()
}

// ApplyConfiguration ports upstream Uniotaku.ApplyConfiguration: POST the
// credentials to account-login.php, then verify the response set uid/pass
// cookies. RequiresTesting (the manager runs the test afterwards).
func (u *Uniotaku) ApplyConfiguration(ctx context.Context, items []indexer.ConfigItem) (indexer.ConfigStatus, error) {
	if err := u.ApplyItems(items); err != nil {
		return indexer.ConfigCompleted, err
	}
	if err := u.LoadValuesFromJSON(nil, false); err != nil {
		return indexer.ConfigCompleted, err
	}
	if u.Client == nil {
		return indexer.ConfigCompleted, errors.New("http client not configured")
	}

	pairs := url.Values{}
	pairs.Set("username", u.cfg.Username.Value)
	pairs.Set("password", u.cfg.Password.Value)
	pairs.Set("manter", "1")

	res, err := u.Client.Do(ctx, u.ID(), &httpclient.Request{
		Method:  http.MethodPost,
		URL:     u.SiteLink() + "account-login.php",
		Body:    pairs,
		Referer: u.SiteLink(),
		Cookies: u.CookieHeader(),
	})
	if err != nil {
		return indexer.ConfigCompleted, err
	}

	// upstream checks response.Cookies (aggregated across the login redirect
	// chain) for "uid=" and "pass="; the Go jar captured the whole chain, so
	// the check is the union of the final response cookies and the jar.
	cookies := uniotakuCookieHeaderValue(res)
	if u.Client != nil {
		if site, err := url.Parse(u.SiteLink()); err == nil {
			cookies = uniotakuMergeCookies(cookies, u.Client.CookieHeader(u.ID(), site))
		}
	}
	if strings.Contains(cookies, "uid=") && strings.Contains(cookies, "pass=") {
		u.SetCookieHeader(cookies)
		u.SetConfigured(true)
		u.SaveConfig(ctx)
		return indexer.ConfigRequiresTesting, nil
	}

	// upstream: dom.QuerySelector(".login-content span.text-red")?.TextContent.Trim()
	errMessage := ""
	if doc, docErr := goquery.NewDocumentFromReader(bytes.NewReader(res.Body)); docErr == nil {
		errMessage = strings.TrimSpace(doc.Find(".login-content span.text-red").First().Text())
	}
	if errMessage == "" {
		errMessage = "Unknown error message, please report."
	}
	return indexer.ConfigCompleted, &indexer.IndexerError{IndexerID: u.ID(), Kind: indexer.KindConfig, Message: errMessage}
}

// uniotakuCookieHeaderValue renders a response's Set-Cookie headers in
// Cookie-header form.
func uniotakuCookieHeaderValue(res *httpclient.Result) string {
	var parts []string
	for _, line := range res.Headers.Values("Set-Cookie") {
		if i := strings.IndexByte(line, ';'); i >= 0 {
			line = line[:i]
		}
		if line != "" {
			parts = append(parts, line)
		}
	}
	return strings.Join(parts, "; ")
}

// uniotakuMergeCookies unions two Cookie-header strings (first wins).
func uniotakuMergeCookies(a, b string) string {
	seen := map[string]bool{}
	var parts []string
	for _, s := range append(strings.Split(a, "; "), strings.Split(b, "; ")...) {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		key := s
		if i := strings.IndexByte(s, '='); i >= 0 {
			key = s[:i]
		}
		if seen[key] {
			continue
		}
		seen[key] = true
		parts = append(parts, s)
	}
	return strings.Join(parts, "; ")
}

func (u *Uniotaku) ResultsForQuery(ctx context.Context, q *models.TorznabQuery, isMeta bool) (*indexer.IndexerResult, error) {
	return u.ResultsForQueryWith(ctx, q, isMeta, u.perform)
}

func (u *Uniotaku) Test(ctx context.Context) error {
	return u.Base.Test(ctx, u.perform)
}

var uniotakuSearchStringRe = regexp.MustCompile(`[ -._]+`)

// perform ports upstream Uniotaku.PerformQuery (Uniotaku.cs:107-188).
func (u *Uniotaku) perform(ctx context.Context, query *models.TorznabQuery) ([]*models.ReleaseInfo, error) {
	if u.Client == nil {
		return nil, errors.New("http client not configured")
	}

	searchString := query.GetQueryString()
	if strings.TrimSpace(searchString) != "" {
		searchString = "%" + strings.TrimSpace(uniotakuSearchStringRe.ReplaceAllString(searchString, "%")) + "%"
	}

	// upstream: MapTorznabCapsToTrackers(query).FirstIfSingleOrDefault("0")
	categoria := "0"
	if cats := u.Caps.Categories.MapTorznabCapsToTrackers(query, false); len(cats) == 1 {
		categoria = cats[0]
	}

	status := "0"
	if u.cfg.Freeleech.Value {
		status = "1"
	}

	qc := shared.QueryParams{}
	qc.Add("categoria", categoria)
	qc.Add("grupo", "0")
	qc.Add("status", status)
	qc.Add("ordenar", u.cfg.SortBy.Value)
	qc.Add("start", "0")
	qc.Add("length", "100")
	qc.Add("search[value]", searchString)
	qc.Add("search[regex]", "false")

	searchURL := u.SiteLink() + "torrents_.php?" + qc.GetQueryString()
	res, err := u.Client.Do(ctx, u.ID(), &httpclient.Request{
		URL:     searchURL,
		Cookies: u.CookieHeader(),
	})
	if err != nil {
		return nil, err
	}

	return u.parseResponse(res.Body)
}

// parseResponse ports the upstream JSON parsing block (Uniotaku.cs:134-181).
func (u *Uniotaku) parseResponse(body []byte) ([]*models.ReleaseInfo, error) {
	var jsonContent struct {
		Data []json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(body, &jsonContent); err != nil {
		return nil, u.parseFailure(string(body), err)
	}

	var releases []*models.ReleaseInfo
	publishDate := time.Now()
	for _, raw := range jsonContent.Data {
		var cells []json.RawMessage
		if err := json.Unmarshal(raw, &cells); err != nil {
			return nil, u.parseFailure(string(body), err)
		}
		if len(cells) < 8 {
			return nil, u.parseFailure(string(body), fmt.Errorf("uniotaku: row has %d cells, want 8", len(cells)))
		}

		release, err := u.parseRow(cells, &publishDate)
		if err != nil {
			return nil, u.parseFailure(string(body), err)
		}
		releases = append(releases, release)
	}
	return releases, nil
}

func (u *Uniotaku) parseRow(cells []json.RawMessage, publishDate *time.Time) (*models.ReleaseInfo, error) {
	cellString := func(i int) string {
		var s string
		_ = json.Unmarshal(cells[i], &s)
		return s
	}
	cellInt := func(i int) int64 {
		var n int64
		_ = json.Unmarshal(cells[i], &n)
		return n
	}

	detailsDom, err := goquery.NewDocumentFromReader(strings.NewReader(cellString(0)))
	if err != nil {
		return nil, err
	}
	categoryDom, err := goquery.NewDocumentFromReader(strings.NewReader(cellString(1)))
	if err != nil {
		return nil, err
	}
	groupDom, err := goquery.NewDocumentFromReader(strings.NewReader(cellString(7)))
	if err != nil {
		return nil, err
	}

	qTitleLink := detailsDom.Find(`a[href^="torrents-details.php?id="]`).First()
	title := strings.TrimSpace(qTitleLink.Text())
	details, err := url.Parse(u.SiteLink() + qTitleLink.AttrOr("href", ""))
	if err != nil {
		return nil, err
	}

	category := "Anime"
	if alt, ok := categoryDom.Find("img[alt]").First().Attr("alt"); ok {
		if c := strings.TrimSpace(alt); c != "" {
			category = c
		}
	}

	releaseGroup := strings.TrimSpace(groupDom.Find(`a[href*="teams-view.php?id="]`).First().Text())
	if releaseGroup != "" {
		title += " [" + releaseGroup + "]"
	}

	seeders := cellInt(3)
	leechers := cellInt(4)
	grabs := cellInt(5)
	size := parseutil.GetBytes(cellString(6))
	peers := seeders + leechers

	*publishDate = publishDate.Add(-time.Minute)

	dvf := 1.0
	if detailsDom.Find(`img[src*="images/free.gif"]`).Length() > 0 {
		dvf = 0
	} else if detailsDom.Find(`img[src*="images/silverdownload.gif"]`).Length() > 0 {
		dvf = 0.5
	}
	minimumRatio := 0.7

	return &models.ReleaseInfo{
		Guid:                 details,
		Details:              details,
		Link:                 details,
		Title:                title,
		Category:             u.Caps.Categories.MapTrackerCatDescToNewznab(category),
		Size:                 &size,
		Grabs:                &grabs,
		Seeders:              &seeders,
		Peers:                &peers,
		PublishDate:          *publishDate,
		DownloadVolumeFactor: &dvf,
		UploadVolumeFactor:   uniotakuF64(1),
		MinimumRatio:         &minimumRatio,
	}, nil
}

// parseFailure mirrors upstream OnParseError: log the response dump and
// surface Exception("Parse error", ex).
func (u *Uniotaku) parseFailure(dump string, err error) error {
	u.log().Error(fmt.Sprintf("Error while parsing result: %v\n%s", err, dump))
	return &indexer.IndexerError{IndexerID: u.ID(), Kind: indexer.KindParse, Message: "Parse error"}
}

// Download ports upstream Uniotaku.Download: fetch the details page, scrape
// the download.php?id= link and download that instead.
func (u *Uniotaku) Download(ctx context.Context, link *url.URL) ([]byte, error) {
	if u.Client == nil {
		return nil, errors.New("http client not configured")
	}
	res, err := u.Client.Do(ctx, u.ID(), &httpclient.Request{
		URL:     link.String(),
		Cookies: u.CookieHeader(),
	})
	if err != nil {
		return nil, err
	}

	dom, err := goquery.NewDocumentFromReader(bytes.NewReader(res.Body))
	if err != nil {
		return nil, err
	}
	downloadLink, ok := dom.Find(`a[href^="download.php?id="]`).First().Attr("href")
	if !ok || strings.TrimSpace(downloadLink) == "" {
		return nil, fmt.Errorf("Failed to fetch download link from %s", link) //nolint:staticcheck // upstream Uniotaku.cs error text, kept 1:1
	}

	downloadURL, err := url.Parse(u.SiteLink() + downloadLink)
	if err != nil {
		return nil, err
	}
	return u.Base.Download(ctx, downloadURL)
}

func uniotakuF64(v float64) *float64 { return &v }
