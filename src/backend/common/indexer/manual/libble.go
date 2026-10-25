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

	"github.com/PuerkitoBio/goquery"

	"vestt/common/httpclient"
	"vestt/common/indexer"
	"vestt/common/indexer/configdata"
	"vestt/common/indexer/shared"
	"vestt/common/models"
	"vestt/common/services"
	"vestt/common/utils/dateutil"
	"vestt/common/utils/parseutil"
)

// Libble ports Jackett.Common/Indexers/Definitions/Libble.cs.
//
// mechanism: html-login (form login with a 2FA code field)
//
// 澄清记录:
//   - upstream SupportsPagination = true has no Go Base equivalent; the
//     page parameter (offset/limit + 1) is implemented as upstream does.
//   - upstream re-logins when the search response IsRedirect; the Go client
//     stack follows redirects transparently, so this branch cannot be
//     observed and is not ported.
//
// Ported from: Definitions/Libble.cs.
type Libble struct {
	indexer.Base
	cfg *libbleConfig
}

// libbleConfig ports upstream ConfigurationDataBasicLoginWith2FA plus the
// dynamic "freeleech" bool and "Account Inactivity" display info (declared
// as fields instead of AddDynamic — same registration and ordering).
type libbleConfig struct {
	configdata.ConfigData
	Username          *configdata.StringItem
	Password          *configdata.StringItem
	TwoFactorAuth     *configdata.StringItem
	Instructions      *configdata.InfoItem
	Freeleech         *configdata.BoolItem
	AccountInactivity *configdata.InfoItem
}

func newLibbleConfig() *libbleConfig {
	return &libbleConfig{
		ConfigData:    *configdata.NewConfigData(),
		Username:      configdata.NewStringItem("Username"),
		Password:      configdata.NewStringItem("Password"),
		TwoFactorAuth: configdata.NewStringItem("Two-Factor Auth"),
		Instructions:  configdata.NewInfoItem("", ""),
		Freeleech:     configdata.NewBoolItem("Search freeleech only"),
		AccountInactivity: configdata.NewInfoItem("Account Inactivity",
			"Accounts are disabled for inactivity after twelve months. To prevent inactivity pruning, browse the site while being logged in. Seeding does not count as 'activity' (actively using site)."),
	}
}

// NewLibble builds the indexer (services.Factory shape).
func NewLibble(deps services.Deps) (indexer.Indexer, error) {
	l := &Libble{cfg: newLibbleConfig()}
	l.Base = indexer.Base{
		IDValue:          "libble",
		NameValue:        "Libble",
		DescriptionValue: "Libble is a Private Torrent Tracker for MUSIC",
		SiteLinkValue:    "https://libble.me/",
		DefaultSiteLink:  "https://libble.me/",
		LangValue:        "en-US",
		TypeValue:        "private",
		Caps:             libbleCaps(),
		Config:           l.cfg,
		Client:           deps.Client,
		Saver:            deps.Saver,
		Cache:            deps.Cache,
	}
	return l, nil
}

func libbleCaps() *models.Capabilities {
	caps := models.NewCapabilities()
	caps.MusicSearchParams = []string{
		models.MusicParamQ, models.MusicParamAlbum, models.MusicParamArtist,
		models.MusicParamLabel, models.MusicParamYear, models.MusicParamGenre,
	}
	c := caps.Categories
	c.AddCategoryMapping("1", models.CatAudio, "Music")
	c.AddCategoryMapping("2", models.CatAudio, "Libble Mixtapes")
	c.AddCategoryMapping("7", models.CatAudioVideo, "Music Videos")
	return caps
}

func (l *Libble) landingURL() string { return l.SiteLink() + "login.php" }
func (l *Libble) loginURL() string   { return l.SiteLink() + "login.php" }
func (l *Libble) searchURL() string  { return l.SiteLink() + "torrents.php" }

// libbleCategoryMappings mirrors the upstream CSS-class → description table
// (used to map the .cats_col div classes to newznab categories).
var libbleCategoryMappings = map[string]string{
	"cats_music":          "Music",
	"cats_libblemixtapes": "Libble Mixtapes",
	"cats_musicvideos":    "Music Videos",
}

// libbleVolumeTagMappings mirrors the upstream tag → volume-factor table.
var libbleVolumeTagMappings = map[string][2]float64{
	"Neutral!":   {0, 0},
	"Freeleech!": {0, 1},
}

var (
	libbleReleaseGroupRe = regexp.MustCompile(`torrents\.php\?id=([0-9]+)`)
	libbleReleaseYearRe  = regexp.MustCompile(`\[(\d{4})\]$`)
)

// ConfigurationForSetup ports upstream GetConfigurationForSetup: seed the
// session from the landing page, then render the form.
func (l *Libble) ConfigurationForSetup(ctx context.Context) ([]map[string]any, error) {
	if l.Client != nil {
		if _, err := l.Client.Do(ctx, l.ID(), &httpclient.Request{
			URL:     l.landingURL(),
			Cookies: l.CookieHeader(),
		}); err != nil {
			return nil, err
		}
	}
	return l.Base.ConfigurationForSetup(ctx)
}

// ApplyConfiguration ports upstream: load values, POST the login form
// (username/password/2FA code), then verify the response carries the
// logout link. RequiresTesting (the manager runs the test afterwards).
func (l *Libble) ApplyConfiguration(ctx context.Context, items []indexer.ConfigItem) (indexer.ConfigStatus, error) {
	if err := l.ApplyItems(items); err != nil {
		return indexer.ConfigCompleted, err
	}
	if err := l.LoadValuesFromJSON(nil, false); err != nil {
		return indexer.ConfigCompleted, err
	}
	if l.Client == nil {
		return indexer.ConfigCompleted, errors.New("http client not configured")
	}

	pairs := formPairs{
		{"username", l.cfg.Username.Value},
		{"password", l.cfg.Password.Value},
		{"code", l.cfg.TwoFactorAuth.Value},
		{"keeplogged", "1"},
		{"login", "Login"},
	}

	res, err := l.Client.Do(ctx, l.ID(), &httpclient.Request{
		Method:  http.MethodPost,
		URL:     l.loginURL(),
		RawBody: pairs.encode(),
		Headers: formContentType,
		Cookies: l.CookieHeader(),
	})
	if err != nil {
		return indexer.ConfigCompleted, err
	}

	if !bytes.Contains(res.Body, []byte("logout.php")) {
		errMessage := ""
		if doc, docErr := goquery.NewDocumentFromReader(bytes.NewReader(res.Body)); docErr == nil {
			errMessage = strings.TrimSpace(doc.Find("#loginform > .warning").First().Text())
		}
		if errMessage == "" {
			errMessage = "Login failed."
		}
		return indexer.ConfigCompleted, &indexer.IndexerError{IndexerID: l.ID(), Kind: indexer.KindConfig, Message: errMessage}
	}

	if u, err := url.Parse(l.SiteLink()); err == nil {
		l.SetCookieHeader(l.Client.CookieHeader(l.ID(), u))
	}
	l.SetConfigured(true)
	l.SaveConfig(ctx)
	return indexer.ConfigRequiresTesting, nil
}

func (l *Libble) ResultsForQuery(ctx context.Context, q *models.TorznabQuery, isMeta bool) (*indexer.IndexerResult, error) {
	return l.ResultsForQueryWith(ctx, q, isMeta, l.perform)
}

func (l *Libble) Test(ctx context.Context) error {
	return l.Base.Test(ctx, l.perform)
}

// perform ports upstream Libble.PerformQuery.
func (l *Libble) perform(ctx context.Context, query *models.TorznabQuery) ([]*models.ReleaseInfo, error) {
	if l.Client == nil {
		return nil, errors.New("http client not configured")
	}

	// https://libble.me/torrents.php?searchstr=the+used&taglist=&tags_type=1&order_by=time&order_way=desc
	qc := shared.QueryParams{}
	qc.Add("order_by", "time")
	qc.Add("order_way", "desc")

	if l.cfg.Freeleech.Value {
		qc.Set("freetorrent", "1")
	}

	// Search String
	searchString := query.GetQueryString()
	if strings.TrimSpace(query.ImdbID) != "" {
		qc.Set("cataloguenumber", query.ImdbID)
	} else if strings.TrimSpace(searchString) != "" {
		qc.Set("searchstr", searchString)
	}

	// Filter Categories
	if query.HasSpecifiedCategories() {
		for _, cat := range l.Caps.Categories.MapTorznabCapsToTrackers(query, false) {
			qc.Set("filter_cat["+cat+"]", "1")
		}
	}

	if strings.TrimSpace(query.Artist) != "" && query.Artist != "VA" {
		qc.Set("artistname", query.Artist)
	}

	if strings.TrimSpace(query.Label) != "" {
		qc.Set("recordlabel", query.Label)
	}

	if query.Year != nil {
		qc.Set("year", strconv.Itoa(*query.Year))
	}

	if strings.TrimSpace(query.Album) != "" {
		qc.Set("groupname", query.Album)
	}

	if query.IsGenreQuery() {
		qc.Set("taglist", query.Genre)
		qc.Set("tags_type", "0")
	}

	if query.Limit > 0 && query.Offset > 0 {
		page := query.Offset/query.Limit + 1
		qc.Add("page", strconv.Itoa(page))
	}

	searchPage, err := l.Client.Do(ctx, l.ID(), &httpclient.Request{
		URL:     l.searchURL() + "?" + qc.GetQueryString(),
		Cookies: l.CookieHeader(),
	})
	if err != nil {
		return nil, err
	}

	return l.parseSearchPage(searchPage.Body)
}

// parseSearchPage ports the upstream parsing block (album group rows + their
// torrent rows), including OnParseError semantics.
func (l *Libble) parseSearchPage(body []byte) ([]*models.ReleaseInfo, error) {
	doc, err := goquery.NewDocumentFromReader(bytes.NewReader(body))
	if err != nil {
		return nil, l.parseFailure(string(body), err)
	}

	var releases []*models.ReleaseInfo
	var parseErr error

	albumRows := doc.Find(`table#torrent_table > tbody > tr.group:has(strong > a[href*="torrents.php?id="])`)
	albumRows.EachWithBreak(func(_ int, row *goquery.Selection) bool {
		rels, err := l.parseAlbumRow(doc, row)
		if err != nil {
			parseErr = err
			return false
		}
		releases = append(releases, rels...)
		return true
	})
	if parseErr != nil {
		return nil, l.parseFailure(string(body), parseErr)
	}
	return releases, nil
}

func (l *Libble) parseAlbumRow(doc *goquery.Document, row *goquery.Selection) ([]*models.ReleaseInfo, error) {
	albumNameNode := row.Find(`strong > a[href^="torrents.php?id="]`).First()
	if albumNameNode.Length() == 0 {
		return nil, errors.New("album row has no album link")
	}
	artistsNameNodes := row.Find(`strong > a[href*="artist.php?id="]`)
	albumYearNode := row.Find(`strong:has(a[href*="torrents.php?id="])`).First()
	categoryNode := row.Find(".cats_col > div").First()
	thumbnailNode := row.Find(".thumbnail").First()

	releaseGenres := []string{}
	releaseDescription := ""
	if genres := strings.TrimSpace(row.Find("div.tags").First().Text()); genres != "" {
		releaseDescription = strings.ReplaceAll(genres, ", ", ",")
		for _, g := range strings.Split(releaseDescription, ",") {
			dupe := false
			for _, existing := range releaseGenres {
				if existing == g {
					dupe = true
					break
				}
			}
			if !dupe {
				releaseGenres = append(releaseGenres, g)
			}
		}
	}

	releaseArtist := "Various Artists"
	if artistsNameNodes.Length() > 0 {
		var artists []string
		artistsNameNodes.Each(func(_ int, artist *goquery.Selection) {
			artists = append(artists, strings.TrimSpace(artist.Text()))
		})
		releaseArtist = strings.Join(artists, ", ")
	}

	releaseAlbumName := strings.TrimSpace(albumNameNode.Text())
	href := albumNameNode.AttrOr("href", "")
	groupMatch := libbleReleaseGroupRe.FindStringSubmatch(href)
	if groupMatch == nil {
		return nil, fmt.Errorf("album link %q has no group id", href)
	}
	releaseGroupID := groupMatch[1]

	releaseAlbumYear := ""
	if m := libbleReleaseYearRe.FindStringSubmatch(albumYearNode.Text()); m != nil {
		// upstream interpolates the whole Match → "[2020]"
		releaseAlbumYear = m[0]
	}

	var releasePoster *url.URL
	if thumbnailNode.Length() > 0 {
		if title := thumbnailNode.AttrOr("title", ""); title != "" {
			releasePoster, _ = url.Parse(title)
		}
	}

	var releaseNewznabCategory []int
	if categoryNode.Length() == 0 {
		return nil, errors.New("album row has no category node")
	}
	className, _ := categoryNode.Attr("class")
	for _, rawCategory := range strings.Fields(className) {
		if desc, ok := libbleCategoryMappings[rawCategory]; ok {
			if newznabCat := l.Caps.Categories.MapTrackerCatDescToNewznab(desc); len(newznabCat) != 0 {
				releaseNewznabCategory = newznabCat
			}
		}
	}

	// https://libble.me/torrents.php?id=51694&torrentid=89758
	var releases []*models.ReleaseInfo
	releaseRows := doc.Find(`table#torrent_table > tbody > tr.group_torrent.groupid_` + releaseGroupID + `:has(a[href*="torrents.php?id="])`)
	var parseErr error
	releaseRows.EachWithBreak(func(_ int, releaseDetails *goquery.Selection) bool {
		release, err := l.parseReleaseRow(releaseDetails, href, releaseArtist, releaseAlbumName, releaseAlbumYear, releasePoster, releaseNewznabCategory, releaseDescription, releaseGenres)
		if err != nil {
			parseErr = err
			return false
		}
		releases = append(releases, release)
		return true
	})
	if parseErr != nil {
		return nil, parseErr
	}
	return releases, nil
}

func (l *Libble) parseReleaseRow(releaseDetails *goquery.Selection, albumHref, releaseArtist, releaseAlbumName, releaseAlbumYear string, releasePoster *url.URL, releaseNewznabCategory []int, releaseDescription string, releaseGenres []string) (*models.ReleaseInfo, error) {
	cells := releaseDetails.Children()
	if cells.Length() < 7 {
		return nil, fmt.Errorf("release row has %d cells, expected at least 7", cells.Length())
	}
	releaseMediaType := cells.Eq(0).Children().Eq(1).Text()

	downloadHref := releaseDetails.Find(`a[href^="torrents.php?action=download&id="]`).First().AttrOr("href", "")
	if downloadHref == "" {
		return nil, errors.New("release row has no download link")
	}
	releaseLink, err := url.Parse(l.SiteLink() + downloadHref)
	if err != nil {
		return nil, err
	}
	releaseDetailsURL, err := url.Parse(l.SiteLink() + albumHref)
	if err != nil {
		return nil, err
	}

	// Aug 31 2020, 15:50
	var publishDate time.Time
	dateNode := cells.Eq(2).Find("span[title]").First()
	if dateNode.Length() == 0 {
		return nil, errors.New("release row has no dated span")
	}
	dateAdded := strings.TrimSpace(dateNode.AttrOr("title", ""))
	parsed, err := time.Parse("Jan 02 2006, 15:04", dateAdded)
	if err != nil {
		publishDate, err = dateutil.FromTimeAgo(strings.TrimSpace(cells.Eq(2).Text()))
		if err != nil {
			return nil, err
		}
	} else {
		// upstream DateTimeStyles.AssumeUniversal
		publishDate = parsed.UTC()
	}

	files := parseutil.MustCoerceLong(cells.Eq(1).Text())
	grabs := parseutil.MustCoerceLong(cells.Eq(4).Text())
	seeders := parseutil.MustCoerceLong(cells.Eq(5).Text())
	peers := seeders + parseutil.MustCoerceLong(cells.Eq(6).Text())
	size := parseutil.GetBytes(strings.TrimSpace(cells.Eq(3).Text()))
	minimumSeedTime := int64(259200) // 72 hours

	release := &models.ReleaseInfo{
		Link:                 releaseLink,
		Guid:                 releaseLink,
		Details:              releaseDetailsURL,
		PublishDate:          publishDate,
		Files:                &files,
		Grabs:                &grabs,
		Seeders:              &seeders,
		Peers:                &peers,
		Size:                 &size,
		Poster:               releasePoster,
		Category:             releaseNewznabCategory,
		MinimumSeedTime:      &minimumSeedTime,
		DownloadVolumeFactor: manualF64(1),
		UploadVolumeFactor:   manualF64(1),
	}

	// Attempt to find volume factor tag
	mediaParts := strings.Split(releaseMediaType, "/")
	var releaseTags []string
	for _, tag := range mediaParts {
		releaseTags = append(releaseTags, strings.TrimSpace(tag))
	}
	for i := len(releaseTags) - 1; i >= 0; i-- {
		releaseTag := releaseTags[i]
		if vf, ok := libbleVolumeTagMappings[releaseTag]; ok {
			release.DownloadVolumeFactor = manualF64(vf[0])
			release.UploadVolumeFactor = manualF64(vf[1])
			releaseTags = append(releaseTags[:i], releaseTags[i+1:]...)
		}
	}

	// Set title (with volume factor tags stripped)
	releaseTagsString := strings.Join(releaseTags, " / ")
	release.Title = strings.Trim(" "+releaseArtist+" - "+releaseAlbumName+" "+releaseAlbumYear+" "+releaseTagsString, " -")

	release.Description = releaseDescription
	release.Genres = releaseGenres
	return release, nil
}

// log is a nil-safe logger (Base.Logger may be nil).
func (l *Libble) log() *slog.Logger {
	if l.Logger != nil {
		return l.Logger
	}
	return slog.Default()
}

// parseFailure mirrors upstream OnParseError: log the response dump and
// surface Exception("Parse error", ex).
func (l *Libble) parseFailure(dump string, err error) error {
	l.log().Error(fmt.Sprintf("Error while parsing result: %v\n%s", err, dump))
	return &indexer.IndexerError{IndexerID: l.ID(), Kind: indexer.KindParse, Message: "Parse error"}
}

func manualF64(v float64) *float64 { return &v }
