// Package manual holds the hand-written indexers (one file per site).
package manual

import (
	"context"
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

// ImmortalSeed ports Jackett.Common/Indexers/Definitions/ImmortalSeed.cs.
//
// mechanism: html-login (private tracker; username/password login against
// takelogin.php, browse.php sortable table parsing, freeleech/silver/x2
// title-image flags, tooltip descriptions, selectable sort order)
//
// Ported from: Definitions/ImmortalSeed.cs,
// Models/IndexerConfig/ConfigurationDataBasicLogin.cs.
//
// 澄清记录:
//   - upstream `#main table td:contains("ERROR")` uses the AngleSharp-only
//     :contains pseudo selector; the port scans the td cells below #main
//     table for the first one whose text contains "ERROR" (equivalent for
//     the flat error markup).
//   - upstream `new Uri(qLink.GetAttribute("href"))` requires absolute
//     hrefs; the port resolves relative hrefs against the site link instead
//     of crashing (same precedent as BitHDTV).
//   - upstream `#sortabletable tr:has(a[href*="details.php?id="])` uses
//     :has; the port filters tr elements by descendant anchor manually.
//   - upstream PublishDate parsing uses the 12-hour-clock layout
//     "yyyy-MM-dd hh:mm tt"; the port uses the equivalent Go layout.
//   - upstream QuerySelector(...).TextContent on the error td is null-safe
//     via `?.` and falls back to "Login failed."; the port mirrors that.
type ImmortalSeed struct {
	indexer.Base
	cfg *immortalSeedConfig
}

type immortalSeedConfig struct {
	configdata.ConfigData
	Username          *configdata.StringItem
	Password          *configdata.StringItem
	Freeleech         *configdata.BoolItem
	SortBy            *configdata.SelectItem
	Order             *configdata.SelectItem
	AccountInactivity *configdata.InfoItem
}

func newImmortalSeedConfig() *immortalSeedConfig {
	freeleech := configdata.NewBoolItem("Filter freeleech only")
	freeleech.Value = false
	sortBy := configdata.NewSelectItem("Sort by", []configdata.Option{
		{Value: "added", Label: "created"},
		{Value: "seeders", Label: "seeders"},
		{Value: "size", Label: "size"},
		{Value: "name", Label: "title"},
	})
	sortBy.Value = "added"
	order := configdata.NewSelectItem("Order", []configdata.Option{
		{Value: "desc", Label: "descending"},
		{Value: "asc", Label: "ascending"},
	})
	order.Value = "desc"
	return &immortalSeedConfig{
		ConfigData:        *configdata.NewConfigData(),
		Username:          configdata.NewStringItem("Username"),
		Password:          configdata.NewStringItem("Password"),
		Freeleech:         freeleech,
		SortBy:            sortBy,
		Order:             order,
		AccountInactivity: configdata.NewInfoItem("Account Inactivity", "To keep records updated reguarly, all inactive accounts will be deleted after 60 days of inactivity."),
	}
}

// imsDateMatchRegex mirrors upstream _dateMatchRegex.
var imsDateMatchRegex = regexp.MustCompile(`\d{4}-\d{2}-\d{2} \d{2}:\d{2} [AaPp][Mm]`)

// NewImmortalSeed builds the indexer (services.Factory shape).
func NewImmortalSeed(deps services.Deps) (indexer.Indexer, error) {
	i := &ImmortalSeed{cfg: newImmortalSeedConfig()}
	i.Base = indexer.Base{
		IDValue:          "immortalseed",
		NameValue:        "ImmortalSeed",
		DescriptionValue: "ImmortalSeed (iS) is a Private Torrent Tracker for MOVIES / TV / GENERAL",
		SiteLinkValue:    "https://immortalseed.me/",
		DefaultSiteLink:  "https://immortalseed.me/",
		LegacySiteLinks:  []string{"http://immortalseed.me/"},
		LangValue:        "en-US",
		TypeValue:        "private",
		Caps:             immortalSeedCaps(),
		Config:           i.cfg,
		Client:           deps.Client,
		Saver:            deps.Saver,
		Cache:            deps.Cache,
	}
	return i, nil
}

func immortalSeedCaps() *models.Capabilities {
	caps := models.NewCapabilities()
	caps.TVSearchParams = []string{models.TVParamQ, models.TVParamSeason, models.TVParamEp}
	caps.MovieSearchParams = []string{models.MovieParamQ}
	caps.MusicSearchParams = []string{models.MusicParamQ}
	caps.BookSearchParams = []string{models.BookParamQ}
	c := caps.Categories
	c.AddCategoryMapping("3", models.CatOther, "Nuked")
	c.AddCategoryMapping("32", models.CatTVAnime, "Anime")
	c.AddCategoryMapping("23", models.CatPC, "Apps")
	c.AddCategoryMapping("35", models.CatAudioAudiobook, "Audiobooks")
	c.AddCategoryMapping("31", models.CatTV, "Childrens/Cartoons")
	c.AddCategoryMapping("54", models.CatTVDocumentary, "Documentary - HD")
	c.AddCategoryMapping("53", models.CatTVDocumentary, "Documentary - SD")
	c.AddCategoryMapping("22", models.CatBooksEBook, "Ebooks")
	c.AddCategoryMapping("41", models.CatBooksComics, "Comics")
	c.AddCategoryMapping("46", models.CatBooksMags, "Magazines")
	c.AddCategoryMapping("25", models.CatPCGames, "Games")
	c.AddCategoryMapping("61", models.CatConsoleNDS, "Games Nintendo")
	c.AddCategoryMapping("26", models.CatPCGames, "Games-PC ISO")
	c.AddCategoryMapping("28", models.CatConsolePS4, "Games-PSx")
	c.AddCategoryMapping("29", models.CatConsoleXBox, "Games Xbox")
	c.AddCategoryMapping("49", models.CatPCMobileOther, "Mobile")
	c.AddCategoryMapping("51", models.CatPCMobileAndroid, "Android")
	c.AddCategoryMapping("50", models.CatPCMobileiOS, "IOS")
	c.AddCategoryMapping("52", models.CatPC0day, "Windows")
	c.AddCategoryMapping("59", models.CatMoviesUHD, "Movies-4k")
	c.AddCategoryMapping("60", models.CatMoviesForeign, "Non-English 4k Movies")
	c.AddCategoryMapping("16", models.CatMoviesHD, "Movies HD")
	c.AddCategoryMapping("18", models.CatMoviesForeign, "Movies HD Non-English")
	c.AddCategoryMapping("17", models.CatMoviesSD, "TS/CAM/PPV")
	c.AddCategoryMapping("34", models.CatMoviesForeign, "Movies Low Def Non-English")
	c.AddCategoryMapping("62", models.CatMovies, "Movies-Packs")
	c.AddCategoryMapping("14", models.CatMoviesSD, "Movies-SD")
	c.AddCategoryMapping("33", models.CatMoviesForeign, "Movies SD Non-English")
	c.AddCategoryMapping("30", models.CatAudioOther, "Music")
	c.AddCategoryMapping("37", models.CatAudioLossless, "FLAC")
	c.AddCategoryMapping("36", models.CatAudioMP3, "MP3")
	c.AddCategoryMapping("39", models.CatAudioOther, "Music Other")
	c.AddCategoryMapping("38", models.CatAudioVideo, "Music Video")
	c.AddCategoryMapping("45", models.CatOther, "Other")
	c.AddCategoryMapping("7", models.CatTVSport, "Sports Tv")
	c.AddCategoryMapping("44", models.CatTVSport, "Sports Fitness-Instructional")
	c.AddCategoryMapping("58", models.CatTVSport, "Olympics")
	c.AddCategoryMapping("67", models.CatTVSport, "World Cup")
	c.AddCategoryMapping("47", models.CatTVSD, "TV - 480p")
	c.AddCategoryMapping("64", models.CatTVUHD, "TV - 4K")
	c.AddCategoryMapping("8", models.CatTVHD, "TV - High Definition")
	c.AddCategoryMapping("48", models.CatTVSD, "TV - Standard Definition - x264")
	c.AddCategoryMapping("9", models.CatTVSD, "TV - Standard Definition - XviD")
	c.AddCategoryMapping("63", models.CatTVUHD, "TV Season Packs - 4K")
	c.AddCategoryMapping("4", models.CatTVHD, "TV Season Packs - HD")
	c.AddCategoryMapping("6", models.CatTVSD, "TV Season Packs - SD")
	return caps
}

func (i *ImmortalSeed) searchURL() string { return i.SiteLink() + "browse.php" }
func (i *ImmortalSeed) loginURL() string  { return i.SiteLink() + "takelogin.php" }

// ApplyConfiguration ports upstream: post username/password to
// takelogin.php; success (logout.php present) saves the cookies and returns
// RequiresTesting, otherwise the login error (or "Login failed.") is
// returned.
func (i *ImmortalSeed) ApplyConfiguration(ctx context.Context, items []indexer.ConfigItem) (indexer.ConfigStatus, error) {
	if err := i.ApplyItems(items); err != nil {
		return indexer.ConfigCompleted, err
	}
	if err := i.LoadValuesFromJSON(nil, false); err != nil {
		return indexer.ConfigCompleted, err
	}
	return i.applyLogin(ctx)
}

func (i *ImmortalSeed) applyLogin(ctx context.Context) (indexer.ConfigStatus, error) {
	if i.Client == nil {
		return indexer.ConfigCompleted, errors.New("http client not configured")
	}
	pairs := url.Values{}
	pairs.Set("username", i.cfg.Username.Value)
	pairs.Set("password", i.cfg.Password.Value)

	res, err := i.Client.Do(ctx, i.ID(), &httpclient.Request{
		Method:  http.MethodPost,
		URL:     i.loginURL(),
		Body:    pairs,
		Referer: i.loginURL(),
		Cookies: i.CookieHeader(),
	})
	if err != nil {
		return indexer.ConfigCompleted, err
	}
	content := string(res.Body)

	if strings.Contains(content, "logout.php") {
		// 澄清记录: the login POST may Set-Cookie before redirecting; the Go
		// client only feeds the final response into the jar, so the capture
		// is the union of both (same approach as shared/gazelle.go).
		cookies := imsCookieHeader(res)
		if site, err := url.Parse(i.SiteLink()); err == nil {
			cookies = imsMergeCookies(cookies, i.Client.CookieHeader(i.ID(), site))
		}
		i.SetCookieHeader(cookies)
		i.SetConfigured(true)
		i.SaveConfig(ctx)
		return indexer.ConfigRequiresTesting, nil
	}

	return indexer.ConfigCompleted, errors.New(imsLoginError(content))
}

// imsLoginError ports upstream `#main table td:contains("ERROR")` extraction
// (null-safe, "Login failed." fallback).
func imsLoginError(content string) string {
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(content))
	if err != nil {
		return "Login failed."
	}
	message := ""
	doc.Find("#main table td").EachWithBreak(func(_ int, s *goquery.Selection) bool {
		if strings.Contains(s.Text(), "ERROR") {
			message = strings.TrimSpace(s.Text())
			return false
		}
		return true
	})
	if message == "" {
		return "Login failed."
	}
	return message
}

func (i *ImmortalSeed) ResultsForQuery(ctx context.Context, q *models.TorznabQuery, isMeta bool) (*indexer.IndexerResult, error) {
	return i.ResultsForQueryWith(ctx, q, isMeta, i.perform)
}

func (i *ImmortalSeed) Test(ctx context.Context) error {
	return i.Base.Test(ctx, i.perform)
}

var imsSearchStringRe = regexp.MustCompile(`[ -._]+`)

func (i *ImmortalSeed) perform(ctx context.Context, query *models.TorznabQuery) ([]*models.ReleaseInfo, error) {
	if i.Client == nil {
		return nil, errors.New("http client not configured")
	}

	searchParams := shared.QueryParams{}
	searchParams.Add("category", "0")
	searchParams.Add("include_dead_torrents", "yes")
	searchParams.Add("sort", i.cfg.SortBy.Value)
	searchParams.Add("order", i.cfg.Order.Value)

	searchString := strings.TrimSpace(imsSearchStringRe.ReplaceAllString(query.GetQueryString(), " "))
	if searchString != "" {
		searchParams.Add("do", "search")
		searchParams.Add("keywords", searchString)
		searchParams.Add("search_type", "t_name")
	}

	categoryMapping := i.Caps.Categories.MapTorznabCapsToTrackers(query, false)
	if len(categoryMapping) > 0 {
		searchParams.Add("selectedcats2", strings.Join(categoryMapping, ","))
	}

	searchURL := i.searchURL() + "?" + searchParams.GetQueryString()

	res, err := i.Client.Do(ctx, i.ID(), &httpclient.Request{
		URL:     searchURL,
		Cookies: i.CookieHeader(),
	})
	if err != nil {
		return nil, err
	}
	body := string(res.Body)

	// Occasionally the cookies become invalid, login again if that happens
	if !strings.Contains(body, "logout.php") {
		if _, err := i.applyLogin(ctx); err != nil {
			return nil, err
		}
		res, err = i.Client.Do(ctx, i.ID(), &httpclient.Request{
			URL:     searchURL,
			Cookies: i.CookieHeader(),
		})
		if err != nil {
			return nil, err
		}
		body = string(res.Body)
	}

	return i.parseBrowse(body)
}

// parseBrowse ports the upstream PerformQuery parsing block (whole block is
// inside one try/catch → OnParseError, mirrored here).
func (i *ImmortalSeed) parseBrowse(content string) ([]*models.ReleaseInfo, error) {
	var releases []*models.ReleaseInfo
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(content))
	if err != nil {
		return nil, i.parseError(content, err)
	}

	// upstream: #sortabletable tr:has(a[href*="details.php?id="])
	rows := doc.Find("#sortabletable tr").FilterFunction(func(_ int, s *goquery.Selection) bool {
		return s.Find(`a[href*="details.php?id="]`).Length() > 0
	})
	for ri := 0; ri < rows.Length(); ri++ {
		rel, err := i.parseRow(rows.Eq(ri))
		if err != nil {
			return nil, i.parseError(content, err)
		}
		if rel != nil {
			releases = append(releases, rel)
		}
	}
	return releases, nil
}

func (i *ImmortalSeed) parseRow(row *goquery.Selection) (*models.ReleaseInfo, error) {
	release := &models.ReleaseInfo{}

	if row.Find(`img[title^="Free Torrent"], img[title^="Sitewide Free Torrent"]`).Length() > 0 {
		release.DownloadVolumeFactor = imsF64(0)
	} else if row.Find(`img[title^="Silver Torrent"]`).Length() > 0 {
		release.DownloadVolumeFactor = imsF64(0.5)
	} else {
		release.DownloadVolumeFactor = imsF64(1)
	}
	if i.cfg.Freeleech.Value && *release.DownloadVolumeFactor != 0 {
		return nil, nil // filtered out
	}
	if row.Find(`img[title^="x2 Torrent"]`).Length() > 0 {
		release.UploadVolumeFactor = imsF64(2)
	} else {
		release.UploadVolumeFactor = imsF64(1)
	}

	qDetails := row.Find(`div > a[href*="details.php?id="]`).First()
	if qDetails.Length() == 0 {
		return nil, errors.New("row has no details link")
	}
	// use Title from tooltip or fallback to Details link if there's no tooltip
	qTitle := row.Find(".tooltip-content > div").First()
	if qTitle.Length() == 0 {
		qTitle = qDetails
	}
	release.Title = qTitle.Text()

	qDescription := row.Find(".tooltip-content > div")
	if qDescription.Length() > 0 {
		release.Description = strings.TrimSpace(strings.ReplaceAll(strings.ReplaceAll(qDescription.Eq(1).Text(), "|", ","), " ", ""))
		for _, g := range strings.Split(release.Description, ",") {
			found := false
			for _, existing := range release.Genres {
				if existing == g {
					found = true
					break
				}
			}
			if !found {
				release.Genres = append(release.Genres, g)
			}
		}
	}

	qLink := row.Find(`a[href*="download.php"]`).First()
	if qLink.Length() == 0 {
		return nil, errors.New("row has no download link")
	}
	dlHref, _ := qLink.Attr("href")
	link, err := imsResolve(i.SiteLink(), dlHref)
	if err != nil {
		return nil, err
	}
	release.Link = link
	release.Guid = link
	detailsHref, _ := qDetails.Attr("href")
	details, err := imsResolve(i.SiteLink(), detailsHref)
	if err != nil {
		return nil, err
	}
	release.Details = details

	// 2021-03-17 03:39 AM
	// requests can be 'Pre Release Time: 2013-04-22 02:00 AM Uploaded: ...'
	qdate := strings.TrimSpace(row.Find("td:nth-of-type(2) > div").Last().Text())
	if qdate != "" {
		if m := imsDateMatchRegex.FindString(qdate); m != "" {
			// upstream: "yyyy-MM-dd hh:mm tt" (12-hour clock)
			if parsed, err := time.ParseInLocation("2006-01-02 03:04 PM", m, time.Local); err == nil {
				release.PublishDate = parsed
			}
		}
	} else {
		release.PublishDate = time.Now()
	}

	sizeCell := row.Find("td:nth-of-type(5)").First()
	if sizeCell.Length() == 0 {
		return nil, errors.New("row has no size cell")
	}
	size := parseutil.GetBytes(strings.TrimSpace(sizeCell.Text()))
	release.Size = &size

	seedersCell := row.Find("td:nth-of-type(7)").First()
	leechersCell := row.Find("td:nth-of-type(8)").First()
	if seedersCell.Length() == 0 || leechersCell.Length() == 0 {
		return nil, errors.New("row has no seeder/leecher cells")
	}
	seeders := parseutil.MustCoerceLong(strings.TrimSpace(seedersCell.Text()))
	release.Seeders = &seeders
	leechers := parseutil.MustCoerceLong(strings.TrimSpace(leechersCell.Text()))
	peers := leechers + seeders
	release.Peers = &peers

	categoryLink := row.Find("td:nth-of-type(1) a").First()
	if categoryLink.Length() == 0 {
		return nil, errors.New("row has no category link")
	}
	catHref, _ := categoryLink.Attr("href")
	release.Category = i.Caps.Categories.MapTrackerCatToNewznab(parseutil.GetArgumentFromQueryString(catHref, "category"))

	grabsCell := row.Find("td:nth-child(6)").First()
	if grabsCell.Length() == 0 {
		return nil, errors.New("row has no grabs cell")
	}
	grabs := parseutil.MustCoerceLong(grabsCell.Text())
	release.Grabs = &grabs

	if cover, ok := row.Find("td:nth-of-type(2) > div > img[src]").First().Attr("src"); ok {
		cover = strings.TrimSpace(cover)
		if cover != "" && strings.HasPrefix(cover, "/") {
			release.Poster, _ = url.Parse(i.SiteLink() + strings.TrimLeft(cover, "/"))
		}
	}

	return release, nil
}

// log is a nil-safe logger for the manual package (Base.Logger may be nil).
func (i *ImmortalSeed) log() *slog.Logger {
	if i.Logger != nil {
		return i.Logger
	}
	return slog.Default()
}

// parseError mirrors upstream OnParseError.
func (i *ImmortalSeed) parseError(dump string, err error) error {
	i.log().Error(fmt.Sprintf("Error while parsing result: %v\n%s", err, dump))
	return &indexer.IndexerError{IndexerID: i.ID(), Kind: indexer.KindParse, Message: "Parse error"}
}

func imsF64(v float64) *float64 { return &v }

// imsCookieHeader renders a response's Set-Cookie headers in
// "name=value; name2=value2" Cookie-header form (upstream WebResult.Cookies).
func imsCookieHeader(res *httpclient.Result) string {
	var parts []string
	for _, line := range res.Headers.Values("Set-Cookie") {
		if j := strings.IndexByte(line, ';'); j >= 0 {
			line = line[:j]
		}
		if line != "" {
			parts = append(parts, line)
		}
	}
	return strings.Join(parts, "; ")
}

// imsMergeCookies unions two Cookie-header strings (first occurrence wins),
// mirroring shared.mergeCookieHeaders.
func imsMergeCookies(a, b string) string {
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

// imsResolve resolves possibly-relative hrefs against the site link (upstream
// `new Uri(href)` requires absolute hrefs; see file header).
func imsResolve(siteLink, href string) (*url.URL, error) {
	u, err := url.Parse(href)
	if err != nil {
		return nil, err
	}
	if u.IsAbs() {
		return u, nil
	}
	base, err := url.Parse(siteLink)
	if err != nil {
		return nil, err
	}
	return base.ResolveReference(u), nil
}
