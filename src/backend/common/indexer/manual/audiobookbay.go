// Package manual holds the hand-written indexers (one file per site).
package manual

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
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
	"vestt/common/utils/stringutil"
)

// AudioBookBay ports Jackett.Common/Indexers/Definitions/AudioBookBay.cs.
//
// mechanism: html (public site, plain ConfigurationData — no login; the
// SPEC-08 table pre-labelled this "cookie html", corrected here)
//
// Ported from: Definitions/AudioBookBay.cs.
//
// 澄清记录:
//   - upstream sets webclient.requestDelay = 6.1 in the constructor; the Go
//     stack applies the delay per indexer via Client.SetRequestDelay.
//   - upstream PageSize => 9 has no Go counterpart (no per-indexer page-size
//     field in the Base port); the two hard-coded page requests below are
//     the behaviour that matters.
type AudioBookBay struct {
	indexer.Base
	cfg *configdata.ConfigData
}

// NewAudioBookBay builds the indexer (services.Factory shape).
func NewAudioBookBay(deps services.Deps) (indexer.Indexer, error) {
	cfg := configdata.NewConfigData()
	a := &AudioBookBay{cfg: cfg}
	a.Base = indexer.Base{
		IDValue:          "audiobookbay",
		NameValue:        "AudioBook Bay",
		DescriptionValue: "AudioBook Bay (ABB) is a Public Torrent Tracker for AUDIOBOOKS",
		SiteLinkValue:    "https://audiobookbay.lu/",
		DefaultSiteLink:  "https://audiobookbay.lu/",
		AltSiteLinks: []string{
			"https://audiobookbay.lu/",
			"http://audiobookbay.is/",
			"http://audiobookbay.se/",
			"http://audiobookbay.fi/",
			"http://audiobookbay.ws/",
			"http://theaudiobookbay.se/",
		},
		LegacySiteLinks: []string{
			"https://audiobookbay.la/",
			"http://audiobookbay.net/",
			"http://audiobookbay.nl/",  // redirects to .lu
			"https://audiobookbay.nl/", // redirects to .lu
			"http://audiobookbayabb.com/",
			"https://audiobookbay.fi/", // redirects to http
			"https://audiobookbay.li/",
			"http://audiobookbay.se/",     // redirectds to http
			"https://theaudiobookbay.se/", // invaid CA cn=nicereads.org
			"https://audiobookbay.is/",    // redirects to http
		},
		LangValue: "en-US",
		TypeValue: "public",
		Caps:      audiobookbayCaps(),
		Config:    cfg,
		Client:    deps.Client,
		Saver:     deps.Saver,
		Cache:     deps.Cache,
	}
	// upstream: webclient.requestDelay = 6.1.
	if deps.Client != nil {
		deps.Client.SetRequestDelay(a.IDValue, 6100*time.Millisecond)
	}
	return a, nil
}

func audiobookbayCaps() *models.Capabilities {
	caps := models.NewCapabilities()
	caps.BookSearchParams = []string{models.BookParamQ}
	caps.Categories.AddCategoryMapping("1", models.CatAudioAudiobook, "")
	return caps
}

// log is a nil-safe logger (Base.Logger may be nil).
func (a *AudioBookBay) log() *slog.Logger {
	if a.Logger != nil {
		return a.Logger
	}
	return slog.Default()
}

// ApplyConfiguration ports upstream AudioBookBay.ApplyConfiguration: run a
// plain query and require at least one release.
func (a *AudioBookBay) ApplyConfiguration(ctx context.Context, items []indexer.ConfigItem) (indexer.ConfigStatus, error) {
	if err := a.ApplyItems(items); err != nil {
		return indexer.ConfigCompleted, err
	}
	if err := a.LoadValuesFromJSON(nil, false); err != nil {
		return indexer.ConfigCompleted, err
	}
	releases, err := a.perform(ctx, models.NewTorznabQuery())
	if err != nil {
		return indexer.ConfigCompleted, err
	}
	if len(releases) == 0 {
		return indexer.ConfigCompleted, errors.New("Could not find releases from this URL") //nolint:staticcheck // upstream AudioBookBay.cs error text, kept 1:1
	}
	a.SetConfigured(true)
	a.SaveConfig(ctx)
	return indexer.ConfigCompleted, nil
}

// Download ports upstream AudioBookBay.Download: the details page hides the
// info hash; build a public magnet out of it.
func (a *AudioBookBay) Download(ctx context.Context, link *url.URL) ([]byte, error) {
	if a.Client == nil {
		return nil, errors.New("http client not configured")
	}
	res, err := a.Client.Do(ctx, a.ID(), &httpclient.Request{
		URL:     link.String(),
		Cookies: a.CookieHeader(),
	})
	if err != nil {
		return nil, err
	}

	doc, err := goquery.NewDocumentFromReader(bytes.NewReader(res.Body))
	if err != nil {
		return nil, a.parseFailure(string(res.Body), err)
	}

	// upstream selector: td:contains("Info Hash:") ~ td (cascadia has no
	// :contains; the td whose text contains the label, then its next td
	// sibling — QuerySelector returns the first match).
	hash := ""
	hashLabel := doc.Find("td").FilterFunction(func(_ int, s *goquery.Selection) bool {
		return strings.Contains(s.Text(), "Info Hash:")
	}).First()
	if hashLabel.Length() > 0 {
		hash = strings.TrimSpace(hashLabel.NextFiltered("td").First().Text())
	}
	if hash == "" {
		return nil, fmt.Errorf("Failed to fetch hash from %s", link) //nolint:staticcheck // upstream AudioBookBay.cs error text, kept 1:1
	}

	titleNode := doc.Find("div.postTitle h1").First()
	if titleNode.Length() == 0 {
		return nil, fmt.Errorf("Failed to fetch title from %s", link) //nolint:staticcheck // upstream AudioBookBay.cs error text, kept 1:1
	}
	title := stringutil.MakeValidFileName(strings.TrimSpace(titleNode.Text()), '_', false)

	magnet := indexer.InfoHashToPublicMagnet(hash, title)
	if magnet == nil {
		return nil, fmt.Errorf("Failed to build magnet from hash %s", hash) //nolint:staticcheck // port-authored guard message (upstream passes null through), kept stable
	}
	return a.Base.Download(ctx, magnet)
}

func (a *AudioBookBay) ResultsForQuery(ctx context.Context, q *models.TorznabQuery, isMeta bool) (*indexer.IndexerResult, error) {
	return a.ResultsForQueryWith(ctx, q, isMeta, a.perform)
}

func (a *AudioBookBay) Test(ctx context.Context) error {
	return a.Base.Test(ctx, a.perform)
}

// perform ports upstream AudioBookBayRequestGenerator: the front page and
// page/2/ are always fetched (with ?s=&tt=1 when a search term is present).
func (a *AudioBookBay) perform(ctx context.Context, query *models.TorznabQuery) ([]*models.ReleaseInfo, error) {
	var params []string
	searchString := strings.TrimSpace(query.GetQueryString())
	if searchString != "" {
		searchString = audiobookbaySearchCleanupRe.ReplaceAllString(searchString, " ")
		searchString = strings.TrimSpace(searchString)
		searchString = strings.ToLower(searchString)
		params = []string{"s=" + shared.Encode(searchString), "tt=1"}
	}

	pageURLs := []string{a.SiteLink(), a.SiteLink() + "page/2/"}

	var releases []*models.ReleaseInfo
	for _, pageURL := range pageURLs {
		searchURL := pageURL
		if len(params) > 0 {
			searchURL += "?" + strings.Join(params, "&")
		}
		pageReleases, err := a.fetchPage(ctx, searchURL)
		if err != nil {
			return nil, err
		}
		releases = append(releases, pageReleases...)
	}
	return releases, nil
}

var audiobookbaySearchCleanupRe = regexp.MustCompile(`[\W]+`)

func (a *AudioBookBay) fetchPage(ctx context.Context, searchURL string) ([]*models.ReleaseInfo, error) {
	res, err := a.Client.Do(ctx, a.ID(), &httpclient.Request{
		URL:     searchURL,
		Headers: map[string]string{"Accept": "text/html"},
		Cookies: a.CookieHeader(),
	})
	if err != nil {
		return nil, err
	}
	return a.parseResponse(res.Body)
}

var (
	audiobookbayFormatRe    = regexp.MustCompile(`(?i)Format: (.+) \/`)
	audiobookbayBitrateRe   = regexp.MustCompile(`(?i)Bitrate: (.+)File`)
	audiobookbaySizeRe      = regexp.MustCompile(`(?i)File Size: (.+?)s?\r?\n?$`)
	audiobookbayDateAddedRe = regexp.MustCompile(`(?i)Posted: (\d{1,2} \D{3} \d{4})`)
	audiobookbayCategoryRe  = regexp.MustCompile(`(?i)Category: (.+)$`)
	// CleanTitle strips control chars and everything non-ASCII
	// (upstream regex removes \u0100-\uFFFF verbatim).
	audiobookbayCleanTitleRe1 = regexp.MustCompile(`[\x{0000}-\x{0008}\x{000A}-\x{001F}\x{0100}-\x{FFFF}]`)
	audiobookbayCleanTitleRe2 = regexp.MustCompile(`\s+`)
)

// parseResponse ports upstream AudioBookBayParser.ParseResponse.
func (a *AudioBookBay) parseResponse(body []byte) ([]*models.ReleaseInfo, error) {
	doc, err := a.parseHTMLDocument(body)
	if err != nil {
		return nil, a.parseFailure(string(body), err)
	}

	var releases []*models.ReleaseInfo
	doc.Find(`div.post:has(div[class="postTitle"])`).Each(func(_ int, row *goquery.Selection) {
		detailsLink := strings.TrimLeft(strings.TrimSpace(row.Find("div.postTitle h2 a").First().AttrOr("href", "")), "/")
		details, _ := url.Parse(a.SiteLink() + detailsLink)

		title := strings.TrimSpace(row.Find("div.postTitle").First().Text())

		infoString := strings.TrimSpace(row.Find("div.postContent").First().Text())

		if matchFormat := audiobookbayFormatRe.FindStringSubmatch(infoString); matchFormat != nil && len(matchFormat[1]) > 0 && matchFormat[1] != "?" {
			title += fmt.Sprintf(" [%s]", strings.TrimSpace(matchFormat[1]))
		}

		if matchBitrate := audiobookbayBitrateRe.FindStringSubmatch(infoString); matchBitrate != nil && len(matchBitrate[1]) > 0 && matchBitrate[1] != "?" {
			title += fmt.Sprintf(" [%s]", strings.TrimSpace(matchBitrate[1]))
		}

		var size int64
		if matchSize := audiobookbaySizeRe.FindStringSubmatch(infoString); matchSize != nil {
			size = parseutil.GetBytes(matchSize[1])
		}

		var publishDate time.Time
		if matchDateAdded := audiobookbayDateAddedRe.FindStringSubmatch(infoString); matchDateAdded != nil {
			if parsedDate, err := time.ParseInLocation("2 Jan 2006", matchDateAdded[1], time.UTC); err == nil {
				publishDate = parsedDate
			}
		}
		if publishDate.IsZero() {
			publishDate = time.Now()
		}

		postInfo := ""
		postInfoNode := row.Find("div.postInfo").First()
		if postInfoNode.Length() > 0 && postInfoNode.Get(0).FirstChild != nil {
			postInfo = strings.TrimSpace(postInfoNode.Get(0).FirstChild.Data)
		}
		postInfo = strings.ReplaceAll(postInfo, "\u00A0", ";")
		var genres []string
		if matchCategory := audiobookbayCategoryRe.FindStringSubmatch(postInfo); matchCategory != nil {
			for _, g := range strings.Split(matchCategory[1], ";") {
				genres = append(genres, strings.TrimSpace(g))
			}
		}

		seeders := int64(1)
		peers := int64(1)
		dvf := 0.0
		uvf := 1.0
		releases = append(releases, &models.ReleaseInfo{
			Guid:                 details,
			Details:              details,
			Link:                 details,
			Title:                audiobookbayCleanTitle(title),
			Category:             []int{models.CatAudioAudiobook.ID},
			Size:                 &size,
			Seeders:              &seeders,
			Peers:                &peers,
			Poster:               a.posterURL(row.Find("img[src]").First().AttrOr("src", "")),
			PublishDate:          publishDate,
			DownloadVolumeFactor: &dvf,
			UploadVolumeFactor:   &uvf,
			Genres:               genres,
		})
	})
	return releases, nil
}

// parseHTMLDocument ports upstream AudioBookBayParser.ParseHtmlDocument:
// hidden div.post.re-ab elements carry base64-encoded post bodies.
func (a *AudioBookBay) parseHTMLDocument(body []byte) (*goquery.Document, error) {
	doc, err := goquery.NewDocumentFromReader(bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	var decodeErr error
	doc.Find("div.post.re-ab").EachWithBreak(func(_ int, element *goquery.Selection) bool {
		decoded, err := base64.StdEncoding.DecodeString(element.Text())
		if err != nil {
			decodeErr = err
			return false
		}
		frag, err := goquery.NewDocumentFromReader(strings.NewReader(`<div class="post">` + string(decoded) + `</div>`))
		if err != nil {
			decodeErr = err
			return false
		}
		replacement := frag.Find("div.post").First()
		if replacement.Length() == 0 {
			decodeErr = errors.New("failed to build replacement post element")
			return false
		}
		element.ReplaceWithSelection(replacement)
		return true
	})
	if decodeErr != nil {
		return nil, decodeErr
	}
	return doc, nil
}

func (a *AudioBookBay) posterURL(cover string) *url.URL {
	cover = strings.TrimSpace(cover)
	if cover != "" {
		if !strings.HasPrefix(cover, "http") {
			cover = a.SiteLink() + cover
		}
		if poster, err := url.Parse(cover); err == nil && poster.IsAbs() &&
			(poster.Scheme == "http" || poster.Scheme == "https") {
			return poster
		}
	}
	return nil
}

func audiobookbayCleanTitle(title string) string {
	title = audiobookbayCleanTitleRe1.ReplaceAllString(title, "")
	title = audiobookbayCleanTitleRe2.ReplaceAllString(title, " ")
	return strings.TrimSpace(title)
}

func (a *AudioBookBay) parseFailure(dump string, err error) error {
	a.log().Error(fmt.Sprintf("Error while parsing result: %v\n%s", err, dump))
	return &indexer.IndexerError{IndexerID: a.ID(), Kind: indexer.KindParse, Message: "Parse error"}
}
