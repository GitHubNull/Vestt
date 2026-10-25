package manual

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
	"golang.org/x/text/encoding/htmlindex"

	"vestt/common/httpclient"
	"vestt/common/indexer"
	"vestt/common/indexer/configdata"
	"vestt/common/indexer/shared"
	"vestt/common/models"
	"vestt/common/services"
	"vestt/common/utils/parseutil"
)

// BitHDTV ports Jackett.Common/Indexers/Definitions/BitHDTV.cs.
//
// mechanism: cookie html
//
// Correction to the SPEC-08 batch-1 table ("passkey html"): upstream
// 0.24.2900 BitHDTV authenticates with a manually pasted cookie
// (Models/IndexerConfig/ConfigurationDataCookie.cs); there is no passkey.
//
// 澄清记录:
//   - upstream Encoding = iso-8859-1: response bodies are decoded from
//     ISO-8859-1 before parsing (mirrors engine cardigann handling).
//   - upstream `new Uri(qLink.GetAttribute("href"))` requires absolute
//     details hrefs; root-relative/relative hrefs are resolved against the
//     site link (net/url ResolveReference) instead of crashing.
//
// Ported from: Definitions/BitHDTV.cs.
type BitHDTV struct {
	indexer.Base
	cfg *bitHDTVConfig
}

type bitHDTVConfig struct {
	configdata.ConfigData
	Cookie             *configdata.StringItem
	CookieInstructions *configdata.InfoItem
	Instructions       *configdata.InfoItem
	Freeleech          *configdata.BoolItem
	AccountInactivity  *configdata.InfoItem
	RateLimits         *configdata.InfoItem
}

func newBitHDTVConfig() *bitHDTVConfig {
	return &bitHDTVConfig{
		ConfigData: *configdata.NewConfigData(),
		Cookie:     configdata.NewStringItem("Cookie"),
		CookieInstructions: configdata.NewInfoItem("Cookie Instructions",
			"Please enter the cookie for the site manually. <a href=\"https://github.com/Jackett/Jackett/wiki/Finding-cookies\" target=\"_blank\">See here</a> on how get the cookies."+
				"<br>Example cookie header (usually longer than this):<br><code>PHPSESSID=8rk27odm; ipsconnect_63ad9c=1; more_stuff=etc;</code>"),
		Instructions: configdata.NewInfoItem("",
			"For best results, change the 'Torrents per page' setting to 100 in your profile."),
		Freeleech: configdata.NewBoolItem("Search freeleech only"),
		AccountInactivity: configdata.NewInfoItem("Account Inactivity",
			"Inactive accounts are disabled after 2 months for User class, 3 months for Power User class, 4 months for Elite User, 6 months Insane User class, 9 months for Veteran User, 11 months for VIP, 12 months for Dedicated BHD User and 12 months for Staff. Parking your account doubles the maximum inactive time. Only the login and browsing the site is considered activity."),
		RateLimits: configdata.NewInfoItem("Rate Limits",
			"BitHDTV has rate limits, the indexer will fail if you exceed them. Read the post at <a href=\"https://www.bit-hdtv.com/forums/viewtopic.php?id=18657\" target=\"_blank\">forum topic 18657</a>"),
	}
}

// NewBitHDTV builds the indexer (services.Factory shape).
func NewBitHDTV(deps services.Deps) (indexer.Indexer, error) {
	b := &BitHDTV{cfg: newBitHDTVConfig()}
	b.Base = indexer.Base{
		IDValue:          "bithdtv",
		NameValue:        "BIT-HDTV",
		DescriptionValue: "BIT-HDTV is a Private site - Home of High Definition",
		SiteLinkValue:    "https://www.bit-hdtv.com/",
		DefaultSiteLink:  "https://www.bit-hdtv.com/",
		LangValue:        "en-US",
		TypeValue:        "private",
		EncodingName:     "iso-8859-1",
		Caps:             bitHDTVCaps(),
		Config:           b.cfg,
		Client:           deps.Client,
		Saver:            deps.Saver,
		Cache:            deps.Cache,
	}
	return b, nil
}

func bitHDTVCaps() *models.Capabilities {
	caps := models.NewCapabilities()
	caps.TVSearchParams = []string{
		models.TVParamQ, models.TVParamSeason, models.TVParamEp, models.TVParamImdbID, models.TVParamGenre,
	}
	caps.MovieSearchParams = []string{models.MovieParamQ, models.MovieParamImdbID, models.MovieParamGenre}
	c := caps.Categories
	c.AddCategoryMapping("6", models.CatAudioLossless, "HQ Audio")
	c.AddCategoryMapping("7", models.CatMovies, "Movies")
	c.AddCategoryMapping("8", models.CatAudioVideo, "Music Videos")
	c.AddCategoryMapping("9", models.CatOther, "Other")
	c.AddCategoryMapping("10", models.CatTV, "TV")
	c.AddCategoryMapping("12", models.CatTV, "TV/Seasonpack")
	c.AddCategoryMapping("11", models.CatXXX, "XXX")
	return caps
}

// ApplyConfiguration ports upstream: load the cookie, test-query the tracker,
// and wrap every failure in "Your cookie did not work: …".
func (b *BitHDTV) ApplyConfiguration(ctx context.Context, items []indexer.ConfigItem) (indexer.ConfigStatus, error) {
	if err := b.ApplyItems(items); err != nil {
		return indexer.ConfigCompleted, err
	}
	if err := b.LoadValuesFromJSON(nil, false); err != nil {
		return indexer.ConfigCompleted, err
	}

	b.SetCookieHeader(b.cfg.Cookie.Value)
	results, err := b.perform(ctx, models.NewTorznabQuery())
	if err != nil {
		b.SetConfigured(false)
		return indexer.ConfigCompleted, fmt.Errorf("Your cookie did not work: %s", err.Error()) //nolint:staticcheck // upstream BitHDTV.cs error text, kept 1:1
	}
	if len(results) == 0 {
		b.SetConfigured(false)
		return indexer.ConfigCompleted, errors.New("Your cookie did not work: Found 0 results in the tracker") //nolint:staticcheck // upstream BitHDTV.cs error text, kept 1:1
	}
	b.SetConfigured(true)
	b.SaveConfig(ctx)
	return indexer.ConfigCompleted, nil
}

func (b *BitHDTV) ResultsForQuery(ctx context.Context, q *models.TorznabQuery, isMeta bool) (*indexer.IndexerResult, error) {
	return b.ResultsForQueryWith(ctx, q, isMeta, b.perform)
}

func (b *BitHDTV) Test(ctx context.Context) error {
	return b.Base.Test(ctx, b.perform)
}

func (b *BitHDTV) perform(ctx context.Context, query *models.TorznabQuery) ([]*models.ReleaseInfo, error) {
	var releases []*models.ReleaseInfo

	qc := shared.QueryParams{}
	qc.Add("cat", bhdFirstIfSingleOrDefault(b.Caps.Categories.MapTorznabCapsToTrackers(query, true), "0"))

	// free=4 green:  (DL won't be counted, and UL will be counted double.)
	// free=3 grey:   (DL will be counted as normal, and UL will be counted double.)
	// free=2 yellow: (DL won't be counted, and UL will be counted as normal.)
	// free=1 normal: (DL and UL counted as normal.)
	// free=0 (any)
	if b.cfg.Freeleech.Value {
		qc.Add("free", "2")
	}

	searchURL := b.SiteLink() + "torrents.php"
	var requests []string
	if query.IsGenreQuery() {
		qc.Add("search", query.GetQueryString()+" "+query.Genre)
		qc.Add("options", "2") // Search Title and Genre
		requests = append(requests, searchURL+"?"+qc.GetQueryString())
	} else if query.IsImdbQuery() {
		qc.Add("search", query.ImdbID)
		qc.Add("options", "4") // Search URL field for IMDB link
		requests = append(requests, searchURL+"?"+qc.GetQueryString())
		// upstream qc["Options"] = "1": NameValueCollection keys are
		// case-insensitive → replaces "options"
		qc.Set("options", "1") // Search Title and Description
		requests = append(requests, searchURL+"?"+qc.GetQueryString())
	} else {
		// Site handles empty string on search param. No need to check for IsNullOrEmpty()
		qc.Add("search", query.GetQueryString())
		qc.Add("options", "0") // Search Title Only
		requests = append(requests, searchURL+"?"+qc.GetQueryString())
	}

	enc, err := htmlindex.Get("iso-8859-1")
	if err != nil {
		return nil, err
	}

	for _, reqURL := range requests {
		res, err := b.Client.Do(ctx, b.ID(), &httpclient.Request{
			URL:     reqURL,
			Cookies: b.CookieHeader(),
		})
		if err != nil {
			return nil, err
		}
		body, err := enc.NewDecoder().Bytes(res.Body)
		if err != nil {
			return nil, err
		}

		if err := b.parseSearchPage(string(body), query, &releases); err != nil {
			return nil, err
		}
	}

	return releases, nil
}

// parseSearchPage ports the upstream per-response parsing block (including
// the rate-limit banner check and OnParseError semantics).
func (b *BitHDTV) parseSearchPage(content string, query *models.TorznabQuery, releases *[]*models.ReleaseInfo) error {
	if strings.Contains(content, "Request limit per User exceeded! Please try later!") {
		return errors.New("Request limit per User exceeded! Please try later!") //nolint:staticcheck // upstream BitHDTV.cs error text, kept 1:1
	}
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(content))
	if err != nil {
		return b.parseError(content, err)
	}

	tableBody := doc.Find("#torrents-index-table > #torrents-index-table-body")
	if tableBody.Length() == 0 { // No results, so skip this search
		return nil
	}

	var parseErr error
	tableBody.Children().EachWithBreak(func(_ int, row *goquery.Selection) bool {
		if err := b.parseRow(row, query, releases); err != nil {
			parseErr = err
			return false
		}
		return true
	})
	if parseErr != nil {
		return b.parseError(content, parseErr)
	}
	return nil
}

func (b *BitHDTV) parseRow(row *goquery.Selection, query *models.TorznabQuery, releases *[]*models.ReleaseInfo) error {
	cells := row.Children()
	if cells.Length() < 10 {
		return fmt.Errorf("row has %d cells, expected 10", cells.Length())
	}
	qLink := cells.Eq(2).Find("a").First()
	titleAttr, ok := qLink.Attr("title")
	if !ok {
		return errors.New("details link has no title attribute")
	}
	minimumRatio := 1.0
	minimumSeedTime := int64(172800) // 48 hours
	release := &models.ReleaseInfo{
		MinimumRatio:    &minimumRatio,
		MinimumSeedTime: &minimumSeedTime,
	}
	release.Title = strings.ReplaceAll(titleAttr, ".", " ")
	href, ok := qLink.Attr("href")
	if !ok {
		return errors.New("details link has no href attribute")
	}
	detailsLink, err := bhdResolve(b.SiteLink(), href)
	if err != nil {
		return err
	}
	// Skip irrelevant and duplicate entries
	if !query.MatchQueryStringAND(release.Title, 0, "") || bhdContainsGUID(*releases, detailsLink) {
		return nil
	}

	if genresSel := row.Find("font.small").First(); genresSel.Length() > 0 {
		genres := genresSel.Text()
		if genres != "" {
			genres = strings.ReplaceAll(genres, "[ ", "")
			genres = strings.ReplaceAll(genres, " ]", "")
			genres = strings.ReplaceAll(genres, " / ", ",")
			genres = strings.ReplaceAll(genres, " | ", ",")
			release.Description = genres
			for _, g := range strings.Split(genres, ",") {
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
	}
	files := parseutil.MustCoerceLong(cells.Eq(3).Text())
	grabs := parseutil.MustCoerceLong(cells.Eq(7).Text())
	release.Files = &files
	release.Grabs = &grabs
	release.Guid = detailsLink
	release.Details = detailsLink
	downloadHref, ok := row.Find(`a[href^="download.php"]`).First().Attr("href")
	if !ok {
		return errors.New("row has no download.php link")
	}
	downloadURL, err := bhdResolve(b.SiteLink(), downloadHref)
	if err != nil {
		return err
	}
	release.Link = downloadURL
	catHref, ok := cells.Eq(1).Children().First().Attr("href")
	if !ok {
		return errors.New("row has no category link")
	}
	catQuery, err := url.Parse(catHref)
	if err != nil {
		return err
	}
	release.Category = b.Caps.Categories.MapTrackerCatToNewznab(catQuery.Query().Get("cat"))

	dateString := strings.TrimSpace(cells.Eq(5).Text())
	// upstream format "yyyy-MM-ddHH:mm:ss" (the <br> between date and time
	// vanishes in TextContent)
	pubDate, err := time.ParseInLocation("2006-01-0215:04:05", dateString, time.Local)
	if err != nil {
		return err
	}
	release.PublishDate = pubDate
	size := parseutil.GetBytes(cells.Eq(6).Text())
	release.Size = &size
	seeders := parseutil.MustCoerceLong(strings.TrimSpace(cells.Eq(8).Text()))
	release.Seeders = &seeders
	peers := parseutil.MustCoerceLong(strings.TrimSpace(cells.Eq(9).Text())) + seeders
	release.Peers = &peers
	switch bgcolor, _ := row.Attr("bgcolor"); bgcolor {
	case "#DDDDDD": // grey
		release.DownloadVolumeFactor = bhdF64(1)
		release.UploadVolumeFactor = bhdF64(2)
	case "#FFFF99": // yellow
		release.DownloadVolumeFactor = bhdF64(0)
		release.UploadVolumeFactor = bhdF64(1)
	case "#CCFF99": // green
		release.DownloadVolumeFactor = bhdF64(0)
		release.UploadVolumeFactor = bhdF64(2)
	default: // normal
		release.DownloadVolumeFactor = bhdF64(1)
		release.UploadVolumeFactor = bhdF64(1)
	}

	*releases = append(*releases, release)
	return nil
}

// log is a nil-safe logger for the manual package (Base.Logger may be nil).
func (b *BitHDTV) log() *slog.Logger {
	if b.Logger != nil {
		return b.Logger
	}
	return slog.Default()
}

// parseError mirrors upstream OnParseError: log the response dump and
// surface Exception("Parse error", ex).
func (b *BitHDTV) parseError(dump string, err error) error {
	b.log().Error(fmt.Sprintf("Error while parsing result: %v\n%s", err, dump))
	return &indexer.IndexerError{IndexerID: b.ID(), Kind: indexer.KindParse, Message: "Parse error"}
}

func bhdF64(v float64) *float64 { return &v }

// bhdFirstIfSingleOrDefault mirrors upstream IEnumerableExtensions
// FirstIfSingleOrDefault.
func bhdFirstIfSingleOrDefault(list []string, def string) string {
	if len(list) == 1 {
		return list[0]
	}
	return def
}

func bhdContainsGUID(releases []*models.ReleaseInfo, guid *url.URL) bool {
	for _, r := range releases {
		if r.Guid != nil && r.Guid.String() == guid.String() {
			return true
		}
	}
	return false
}

// bhdResolve parses href and resolves relative references against the site
// link (upstream `new Uri(href)` requires absolute hrefs; see file header).
func bhdResolve(siteLink, href string) (*url.URL, error) {
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
