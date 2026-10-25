// Package manual holds the hand-written indexers (one file per site).
package manual

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/PuerkitoBio/goquery"
	"golang.org/x/net/html"

	"vestt/common/httpclient"
	"vestt/common/indexer"
	"vestt/common/indexer/configdata"
	"vestt/common/indexer/shared"
	"vestt/common/models"
	"vestt/common/services"
	"vestt/common/utils/parseutil"
)

// BakaBT ports Jackett.Common/Indexers/Definitions/BakaBT.cs.
//
// mechanism: html-login (the login form carries a hidden loginKey field —
// upstream's "pin/captcha class" label; no image captcha is parsed)
//
// Ported from: Definitions/BakaBT.cs.
type BakaBT struct {
	indexer.Base
	cfg *bakabtConfig
}

// bakabtConfig ports upstream ConfigurationDataBakaBT
// (BasicLogin + FreeleechOnly/AddRomajiTitle/AppendSeason + the dynamic
// "Account Inactivity" display info; declared as a field instead of
// AddDynamic — same registration and ordering).
type bakabtConfig struct {
	configdata.ConfigData
	Username          *configdata.StringItem
	Password          *configdata.StringItem
	Instructions      *configdata.InfoItem
	FreeleechOnly     *configdata.BoolItem
	AddRomajiTitle    *configdata.BoolItem
	AppendSeason      *configdata.BoolItem
	AccountInactivity *configdata.InfoItem
}

const bakabtAccountInactivity = "Accounts are pruned after 11 months of disuse (i.e. not logging into the tracker account). Accounts with 0 download and 0 upload are pruned earlier. Donator accounts (people who've donated to BakaBT and received a donator status) have the option of disabling inactivity pruning, and safeguarding their account for as long as they choose."

func newBakabtConfig() *bakabtConfig {
	return &bakabtConfig{
		ConfigData:        *configdata.NewConfigData(),
		Username:          configdata.NewStringItem("Username"),
		Password:          configdata.NewStringItem("Password"),
		Instructions:      configdata.NewInfoItem("", "To show more results and prevent a 0 results error, go to your BakaBT account settings and in the 'Browse' section enable the 'Show Adult Content' option and change 'Torrents per page' to '100'."),
		FreeleechOnly:     configdata.NewBoolItem("Show freeleech only"),
		AddRomajiTitle:    configdata.NewBoolItem("Add releases for Romaji Title"),
		AppendSeason:      configdata.NewBoolItem("Append Season for Sonarr Compatibility"),
		AccountInactivity: configdata.NewInfoItem("Account Inactivity", bakabtAccountInactivity),
	}
}

var bakabtEpSearchRe = regexp.MustCompile(`\s(?:[Ee]\d+|\d+)$`)

// NewBakaBT builds the indexer (services.Factory shape).
func NewBakaBT(deps services.Deps) (indexer.Indexer, error) {
	cfg := newBakabtConfig()
	// upstream: AddRomajiTitle = true.
	cfg.AddRomajiTitle.Value = true
	b := &BakaBT{cfg: cfg}
	b.Base = indexer.Base{
		IDValue:          "bakabt",
		NameValue:        "BakaBT",
		DescriptionValue: "BakaBT is a Private site for the Anime Community",
		SiteLinkValue:    "https://bakabt.me/",
		DefaultSiteLink:  "https://bakabt.me/",
		LangValue:        "en-US",
		TypeValue:        "private",
		Caps:             bakabtCaps(),
		Config:           cfg,
		Client:           deps.Client,
		Saver:            deps.Saver,
		Cache:            deps.Cache,
	}
	return b, nil
}

func bakabtCaps() *models.Capabilities {
	caps := models.NewCapabilities()
	caps.TVSearchParams = []string{models.TVParamQ, models.TVParamSeason, models.TVParamEp}
	caps.MusicSearchParams = []string{models.MusicParamQ}
	caps.BookSearchParams = []string{models.BookParamQ}
	c := caps.Categories
	c.AddCategoryMapping("1", models.CatTVAnime, "Anime Series")
	c.AddCategoryMapping("2", models.CatTVAnime, "OVA")
	c.AddCategoryMapping("3", models.CatAudioOther, "Soundtrack")
	c.AddCategoryMapping("4", models.CatBooksComics, "Manga")
	c.AddCategoryMapping("5", models.CatTVAnime, "Anime Movie")
	c.AddCategoryMapping("6", models.CatTVOther, "Live Action")
	c.AddCategoryMapping("7", models.CatBooksOther, "Artbook")
	c.AddCategoryMapping("8", models.CatAudioVideo, "Music Video")
	c.AddCategoryMapping("9", models.CatBooksEBook, "Light Novel")
	c.AddCategoryMapping("11", models.CatXXX, "Hentai Series")
	c.AddCategoryMapping("12", models.CatXXX, "Hentai OVA")
	c.AddCategoryMapping("13", models.CatXXX, "Hentai Soundtrack")
	c.AddCategoryMapping("14", models.CatXXX, "Hentai Manga")
	c.AddCategoryMapping("15", models.CatXXX, "Hentai Movie")
	c.AddCategoryMapping("16", models.CatXXX, "Hentai Live Action")
	c.AddCategoryMapping("17", models.CatXXX, "Hentai Artbook")
	c.AddCategoryMapping("18", models.CatXXX, "Hentai Music Video")
	c.AddCategoryMapping("19", models.CatXXX, "Hentai Light Novel")
	return caps
}

// log is a nil-safe logger (Base.Logger may be nil).
func (b *BakaBT) log() *slog.Logger {
	if b.Logger != nil {
		return b.Logger
	}
	return slog.Default()
}

func (b *BakaBT) searchURL() string {
	return b.SiteLink() + "browse.php?only=0&hentai=1&incomplete=1&lossless=1&hd=1&multiaudio=1&bonus=1&reorder=1&q="
}
func (b *BakaBT) loginURL() string { return b.SiteLink() + "login.php" }

// ApplyConfiguration ports upstream BakaBT.ApplyConfiguration: login only;
// the manager runs the test afterwards (RequiresTesting).
func (b *BakaBT) ApplyConfiguration(ctx context.Context, items []indexer.ConfigItem) (indexer.ConfigStatus, error) {
	if err := b.ApplyItems(items); err != nil {
		return indexer.ConfigCompleted, err
	}
	if err := b.LoadValuesFromJSON(nil, false); err != nil {
		return indexer.ConfigCompleted, err
	}
	if err := b.doLogin(ctx); err != nil {
		return indexer.ConfigCompleted, err
	}
	return indexer.ConfigRequiresTesting, nil
}

// doLogin ports upstream DoLogin (GET the login form for the loginKey,
// POST credentials, ConfigureIfOK on the absence of "loginForm").
func (b *BakaBT) doLogin(ctx context.Context) error {
	if b.Client == nil {
		return errors.New("http client not configured")
	}
	loginForm, err := b.Client.Do(ctx, b.ID(), &httpclient.Request{
		URL:     b.loginURL(),
		Cookies: b.CookieHeader(),
	})
	if err != nil {
		return err
	}

	pairs := formPairs{
		{"username", b.cfg.Username.Value},
		{"password", b.cfg.Password.Value},
		{"returnto", "/index.php"},
	}

	doc, err := goquery.NewDocumentFromReader(bytes.NewReader(loginForm.Body))
	if err != nil {
		return b.parseFailure(string(loginForm.Body), err)
	}
	if loginKey := doc.Find(`input[name="loginKey"]`).First(); loginKey.Length() > 0 {
		pairs = append(pairs, [2]string{"loginKey", loginKey.AttrOr("value", "")})
	}

	res, err := b.Client.Do(ctx, b.ID(), &httpclient.Request{
		Method:  http.MethodPost,
		URL:     b.loginURL(),
		RawBody: pairs.encode(),
		Headers: formContentType,
		Cookies: b.CookieHeader(),
	})
	if err != nil {
		return err
	}

	if bytes.Contains(res.Body, []byte("loginForm")) {
		errMessage := ""
		if errorDoc, docErr := goquery.NewDocumentFromReader(bytes.NewReader(res.Body)); docErr == nil {
			errMessage = strings.TrimSpace(errorDoc.Find("#loginError, .error").First().Text())
		}
		if errMessage == "" {
			errMessage = "Login failed."
		}
		return &indexer.IndexerError{IndexerID: b.ID(), Kind: indexer.KindConfig, Message: errMessage}
	}

	if u, err := url.Parse(b.loginURL()); err == nil {
		b.SetCookieHeader(b.Client.CookieHeader(b.ID(), u))
	}
	b.SetConfigured(true)
	b.SaveConfig(ctx)
	return nil
}

func (b *BakaBT) ResultsForQuery(ctx context.Context, q *models.TorznabQuery, isMeta bool) (*indexer.IndexerResult, error) {
	return b.ResultsForQueryWith(ctx, q, isMeta, b.perform)
}

func (b *BakaBT) Test(ctx context.Context) error {
	return b.Base.Test(ctx, b.perform)
}

// perform ports upstream BakaBT.PerformQuery (episode suffix stripping,
// session refresh on "loginForm", .torrents row parsing).
func (b *BakaBT) perform(ctx context.Context, query *models.TorznabQuery) ([]*models.ReleaseInfo, error) {
	searchString := query.SanitizedSearchTerm()

	// Strip a trailing episode ("... E12" / "... 12") from the search term.
	if loc := bakabtEpSearchRe.FindStringIndex(searchString); loc != nil {
		searchString = searchString[:loc[0]]
	}

	episodeSearchURL := b.searchURL() + shared.Encode(searchString)

	res, err := b.Client.Do(ctx, b.ID(), &httpclient.Request{
		URL:     episodeSearchURL,
		Cookies: b.CookieHeader(),
	})
	if err != nil {
		return nil, err
	}
	if bytes.Contains(res.Body, []byte("loginForm")) {
		// Cookie appears to expire after a period of time or logging in to
		// the site via browser
		if err := b.doLogin(ctx); err != nil {
			return nil, err
		}
		res, err = b.Client.Do(ctx, b.ID(), &httpclient.Request{
			URL:     episodeSearchURL,
			Cookies: b.CookieHeader(),
		})
		if err != nil {
			return nil, err
		}
	}

	doc, err := goquery.NewDocumentFromReader(bytes.NewReader(res.Body))
	if err != nil {
		return nil, b.parseFailure(string(res.Body), err)
	}
	rows := doc.Find(".torrents tr.torrent, .torrents tr.torrent_alt")

	currentCategories := []int{models.CatTVAnime.ID}
	var releases []*models.ReleaseInfo
	var parseErr error
	rows.EachWithBreak(func(_ int, row *goquery.Selection) bool {
		release, err := b.parseRow(row, &currentCategories)
		if err != nil {
			parseErr = err
			return false
		}
		releases = append(releases, release...)
		return true
	})
	if parseErr != nil {
		return nil, b.parseFailure(string(res.Body), parseErr)
	}
	return releases, nil
}

func (b *BakaBT) parseRow(row *goquery.Selection, currentCategories *[]int) ([]*models.ReleaseInfo, error) {
	dvf := 1.0
	if row.Find("span.freeleech").Length() > 0 {
		dvf = 0
	}

	// Skip non-freeleech results when freeleech only is set
	if b.cfg.FreeleechOnly.Value && dvf != 0 {
		return nil, nil
	}

	qTitleLink := row.Find("a.title, a.alt_title").First()
	if qTitleLink.Length() == 0 {
		return nil, nil
	}

	title := strings.TrimSpace(qTitleLink.Text())

	// Insert before the release info
	taidx := strings.Index(title, "(")
	tbidx := strings.Index(title, "[")
	if taidx == -1 {
		taidx = len(title)
	}
	if tbidx == -1 {
		tbidx = len(title)
	}
	titleSplit := taidx
	if tbidx < titleSplit {
		titleSplit = tbidx
	}
	titleSeries := title[:titleSplit]
	releaseInfo := title[titleSplit:]

	*currentCategories = b.nextCategory(row, *currentCategories)

	titles := strings.Split(titleSeries, " | ")
	var nonEmpty []string
	for _, t := range titles {
		if t != "" {
			nonEmpty = append(nonEmpty, t)
		}
	}
	titles = nonEmpty

	if len(titles) > 1 && !b.cfg.AddRomajiTitle.Value {
		titles = titles[1:]
	}

	var releases []*models.ReleaseInfo
	for _, name := range titles {
		releaseTitle := strings.TrimSpace(name + releaseInfo)

		// Ensure the season is defined as this tracker only deals with full seasons
		if b.cfg.AppendSeason.Value && !strings.Contains(releaseTitle, "Season") {
			// Insert before the release info
			aidx := strings.Index(releaseTitle, "(")
			bidx := strings.Index(releaseTitle, "[")
			if aidx == -1 {
				aidx = len(releaseTitle)
			}
			if bidx == -1 {
				bidx = len(releaseTitle)
			}
			insertPoint := aidx
			if bidx < insertPoint {
				insertPoint = bidx
			}
			releaseTitle = releaseTitle[:insertPoint] + " Season 1 " + releaseTitle[insertPoint:]
		}

		release, err := b.buildRelease(row, qTitleLink, *currentCategories, releaseTitle, dvf)
		if err != nil {
			return nil, err
		}
		releases = append(releases, release)
	}
	return releases, nil
}

func (b *BakaBT) buildRelease(row, qTitleLink *goquery.Selection, categories []int, title string, dvf float64) (*models.ReleaseInfo, error) {
	guid, err := url.Parse(b.SiteLink() + qTitleLink.AttrOr("href", ""))
	if err != nil {
		return nil, err
	}

	peersSel := row.Find(".peers").First()
	peersLinks := row.Find(".peers a")
	if peersSel.Length() == 0 || peersLinks.Length() < 2 {
		return nil, errors.New("missing peers nodes")
	}
	link, err := url.Parse(b.SiteLink() + peersLinks.First().AttrOr("href", ""))
	if err != nil {
		return nil, err
	}

	// grabs: the leading text node of the first .peers cell,
	// e.g. "123 / " → "123"; "k" suffix scales by 1000 (upstream quirk).
	grabsText := ""
	if firstChild := peersSel.Get(0).FirstChild; firstChild != nil && firstChild.Type == html.TextNode {
		grabsText = firstChild.Data
	}
	grabsText = strings.TrimRightFunc(grabsText, unicode.IsSpace)
	grabsText = strings.TrimRight(grabsText, "/")
	grabsText = strings.TrimRightFunc(grabsText, unicode.IsSpace)
	grabsText = strings.ReplaceAll(grabsText, "k", "000")
	grabs, err := strconv.ParseInt(strings.TrimSpace(grabsText), 10, 64)
	if err != nil {
		return nil, fmt.Errorf("invalid grabs %q: %w", grabsText, err)
	}

	seeders, err := strconv.ParseInt(strings.TrimSpace(peersLinks.Eq(0).Text()), 10, 64)
	if err != nil {
		return nil, err
	}
	leechers, err := strconv.ParseInt(strings.TrimSpace(peersLinks.Eq(1).Text()), 10, 64)
	if err != nil {
		return nil, err
	}
	peers := seeders + leechers

	minimumRatio := 0.5

	sizeSel := row.Find(".size").First()
	if sizeSel.Length() == 0 {
		return nil, errors.New("missing size node")
	}
	size := parseutil.GetBytes(sizeSel.Text())

	// 22 Jul 15
	dateSel := row.Find(".added").First()
	if dateSel.Length() == 0 {
		return nil, errors.New("missing added node")
	}
	dateStr := strings.ReplaceAll(dateSel.Text(), "'", "")
	if parts := strings.Split(dateStr, " "); len(parts) > 0 && len(parts[0]) == 1 {
		dateStr = "0" + dateStr
	}
	var publishDate time.Time
	switch {
	case strings.EqualFold(strings.TrimSpace(dateStr), "yesterday"):
		publishDate = time.Now().AddDate(0, 0, -1)
	case strings.EqualFold(strings.TrimSpace(dateStr), "today"):
		publishDate = time.Now()
	default:
		parsed, err := time.ParseInLocation("02 Jan 06", strings.TrimSpace(dateStr), time.Local)
		if err != nil {
			return nil, err
		}
		publishDate = parsed
	}

	uvf := 1.0
	return &models.ReleaseInfo{
		Title:                title,
		Category:             categories,
		Description:          row.Find("span.tags").First().Text(),
		Guid:                 guid,
		Details:              guid,
		Link:                 link,
		Grabs:                &grabs,
		Seeders:              &seeders,
		Peers:                &peers,
		MinimumRatio:         &minimumRatio,
		Size:                 &size,
		PublishDate:          publishDate,
		DownloadVolumeFactor: &dvf,
		UploadVolumeFactor:   &uvf,
	}, nil
}

// nextCategory ports upstream GetNextCategory: a row with a category title
// remaps the current categories, unknown names fall back to Anime.
func (b *BakaBT) nextCategory(row *goquery.Selection, currentCategories []int) []int {
	nextCategoryName := bakabtCategoryName(row)
	if nextCategoryName != "" {
		mapped := b.Caps.Categories.MapTrackerCatDescToNewznab(nextCategoryName)
		if len(mapped) == 0 {
			return []int{models.CatTVAnime.ID}
		}
		currentCategories = mapped
	}
	return currentCategories
}

func bakabtCategoryName(row *goquery.Selection) string {
	categoryElement := row.Find("td.category span").First()
	if categoryElement.Length() == 0 {
		return ""
	}
	categoryName := strings.TrimSpace(categoryElement.AttrOr("title", ""))
	if categoryName == "" {
		return ""
	}
	if row.Find("td.name span.hentai").Length() == 0 {
		return categoryName
	}
	if !strings.HasPrefix(categoryName, "Anime") {
		return "Hentai " + categoryName
	}
	return strings.ReplaceAll(categoryName, "Anime", "Hentai")
}

// Download ports upstream BakaBT.Download: the download page hides the real
// .torrent behind a .download_link.
func (b *BakaBT) Download(ctx context.Context, link *url.URL) ([]byte, error) {
	if b.Client == nil {
		return nil, errors.New("http client not configured")
	}
	downloadPage, err := b.Client.Do(ctx, b.ID(), &httpclient.Request{
		URL:     link.String(),
		Cookies: b.CookieHeader(),
	})
	if err != nil {
		return nil, err
	}
	doc, err := goquery.NewDocumentFromReader(bytes.NewReader(downloadPage.Body))
	if err != nil {
		return nil, b.parseFailure(string(downloadPage.Body), err)
	}
	downloadLink := strings.TrimSpace(doc.Find(".download_link").First().AttrOr("href", ""))
	if downloadLink == "" {
		return nil, errors.New("Unable to find download link.") //nolint:staticcheck // upstream BakaBT.cs error text, kept 1:1
	}
	res, err := b.Client.Do(ctx, b.ID(), &httpclient.Request{
		URL:     b.SiteLink() + downloadLink,
		Cookies: b.CookieHeader(),
	})
	if err != nil {
		return nil, err
	}
	return res.Body, nil
}

func (b *BakaBT) parseFailure(dump string, err error) error {
	b.log().Error(fmt.Sprintf("Error while parsing result: %v\n%s", err, dump))
	return &indexer.IndexerError{IndexerID: b.ID(), Kind: indexer.KindParse, Message: "Parse error"}
}
