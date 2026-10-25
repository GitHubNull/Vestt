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
	"strconv"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
	"golang.org/x/net/html"

	"vestt/common/httpclient"
	"vestt/common/indexer"
	"vestt/common/indexer/configdata"
	"vestt/common/indexer/shared"
	"vestt/common/models"
	"vestt/common/services"
	"vestt/common/utils/dateutil"
)

// Shazbat ports Jackett.Common/Indexers/Definitions/Shazbat.cs.
//
// mechanism: html-login (form login against /login, /search show discovery
// with per-show /show torrent sub-requests, torrent-table row parsing)
//
// 澄清记录:
//   - upstream webclient.requestDelay = 5.1 (Shazbat.cs:52); the port sets a
//     5.1s per-indexer request delay (tests override it to 0).
//   - upstream ReloginIfNecessaryAsync also reacts to IsRedirect → login
//     (Shazbat.cs:286); the Go http stack follows redirects transparently,
//     so only the body markers ("sign in now", "fullRedirect"+"login") are
//     observable and ported.
//   - FixSearchTerm is applied to the SANITIZED search term for the request,
//     while MatchQueryStringAND filters titles against the raw sanitized
//     term (Shazbat.cs:107-108, 216) — the port keeps both.
//   - ParseTitle reads the first direct TEXT child of the third cell
//     (Shazbat.cs:266-271); the port walks the raw child nodes.
//   - the info cell regex is `\((\d+)\)\s*:(\d+) / :(\d+)$`
//     (Shazbat.cs:209); parse failures default to 0.
//   - publishDate is a RUNNING value initialised to DateTime.Now at every
//     ParseResults call (it does NOT carry across pages): a row without a
//     data-timestamp attribute (or with an unparseable one) gets the
//     previous row's time minus one minute (Shazbat.cs:207, 230-233).
//   - the global freeleech flag is detected once on the search response and
//     carried into the per-show page parsing (Shazbat.cs:105, 130, 184).
//   - upstream `span:contains("Freeleech until:"):has(span.datetime)` uses
//     AngleSharp-only pseudo selectors; the port filters spans by text and
//     descendant manually (same precedent as ImmortalSeed).
//   - ShowPagesFetchLimit getter falls back to 2 for any value outside
//     1..5 (Shazbat.cs:72), which makes the PerformQuery range check
//     unreachable; the port keeps both anyway.
//   - poster style regex `url\('(.*)'\);` (Shazbat.cs:256): no-match yields
//     Groups["poster"].Value == "" and release.Poster = new Uri(SiteLink);
//     the port replicates that.
//   - OnParseError aborts the whole query with "Parse error"
//     (Shazbat.cs:186-189).
//
// Ported from: Definitions/Shazbat.cs,
// Models/IndexerConfig/Bespoke/ConfigurationDataShazbat.cs.
type Shazbat struct {
	indexer.Base
	cfg *shazbatConfig
}

type shazbatConfig struct {
	configdata.ConfigData
	Username                        *configdata.StringItem
	Password                        *configdata.StringItem
	ShowPagesFetchLimit             *configdata.SelectItem
	ShowPagesFetchLimitInstructions *configdata.InfoItem
}

func newShazbatConfig() *shazbatConfig {
	limit := configdata.NewSelectItem("Show Pages Fetch Limit (sub-requests when searching)", []configdata.Option{
		{Value: "1", Label: "1"},
		{Value: "2", Label: "2"},
		{Value: "3", Label: "3"},
		{Value: "4", Label: "4"},
		{Value: "5", Label: "5"},
	})
	limit.Value = "2"
	return &shazbatConfig{
		ConfigData:                      *configdata.NewConfigData(),
		Username:                        configdata.NewStringItem("Username"),
		Password:                        configdata.NewStringItem("Password"),
		ShowPagesFetchLimit:             limit,
		ShowPagesFetchLimitInstructions: configdata.NewInfoItem("Show Pages Fetch Limit Warning", "Higher values may risk your account being flagged for bot activity when used with automation software such as Sonarr."),
	}
}

// NewShazbat builds the indexer (services.Factory shape).
func NewShazbat(deps services.Deps) (indexer.Indexer, error) {
	s := &Shazbat{cfg: newShazbatConfig()}
	s.Base = indexer.Base{
		IDValue:          "shazbat",
		NameValue:        "Shazbat",
		DescriptionValue: "Shazbat is a Private site with highly curated TV content",
		SiteLinkValue:    "https://www.shazbat.tube/",
		DefaultSiteLink:  "https://www.shazbat.tube/",
		LegacySiteLinks: []string{
			"https://www.shazbat.tv/",
		},
		LangValue: "en-US",
		TypeValue: "private",
		Caps:      shazbatCaps(),
		Config:    s.cfg,
		Client:    deps.Client,
		Saver:     deps.Saver,
		Cache:     deps.Cache,
	}
	// upstream: webclient.requestDelay = 5.1
	if deps.Client != nil {
		deps.Client.SetRequestDelay(s.IDValue, 5100*time.Millisecond)
	}
	return s, nil
}

func shazbatCaps() *models.Capabilities {
	caps := models.NewCapabilities()
	caps.TVSearchParams = []string{models.TVParamQ, models.TVParamSeason, models.TVParamEp}
	c := caps.Categories
	c.AddCategoryMapping("1", models.CatTV, "")
	c.AddCategoryMapping("2", models.CatTVSD, "")
	c.AddCategoryMapping("3", models.CatTVHD, "")
	return caps
}

func (s *Shazbat) loginURL() string    { return s.SiteLink() + "login" }
func (s *Shazbat) searchURL() string   { return s.SiteLink() + "search" }
func (s *Shazbat) torrentsURL() string { return s.SiteLink() + "torrents" }
func (s *Shazbat) showURL() string     { return s.SiteLink() + "show" }

// showPagesFetchLimit ports the upstream getter (Shazbat.cs:72).
func (s *Shazbat) showPagesFetchLimit() int {
	if n, err := strconv.Atoi(strings.TrimSpace(s.cfg.ShowPagesFetchLimit.Value)); err == nil && n > 0 && n <= 5 {
		return n
	}
	return 2
}

// ApplyConfiguration ports upstream Shazbat.ApplyConfiguration
// (Shazbat.cs:74-96): POST the login form; success is the presence of
// "glyphicon-log-out" in the response body. Returns RequiresTesting.
func (s *Shazbat) ApplyConfiguration(ctx context.Context, items []indexer.ConfigItem) (indexer.ConfigStatus, error) {
	if err := s.ApplyItems(items); err != nil {
		return indexer.ConfigCompleted, err
	}
	if err := s.LoadValuesFromJSON(nil, false); err != nil {
		return indexer.ConfigCompleted, err
	}
	if err := s.doLogin(ctx); err != nil {
		return indexer.ConfigCompleted, err
	}
	return indexer.ConfigRequiresTesting, nil
}

func (s *Shazbat) doLogin(ctx context.Context) error {
	if s.Client == nil {
		return errors.New("http client not configured")
	}

	pairs := shFormPairs{
		{"referer", ""},
		{"query", ""},
		{"tv_timezone", "0"},
		{"username", s.cfg.Username.Value},
		{"password", s.cfg.Password.Value},
	}

	// Get cookie
	result, err := s.Client.Do(ctx, s.ID(), &httpclient.Request{
		Method:  http.MethodPost,
		URL:     s.loginURL(),
		Body:    pairs.values(),
		Headers: shFormContentType,
		Cookies: s.CookieHeader(),
		Referer: s.loginURL(),
	})
	if err != nil {
		return err
	}

	if strings.Contains(string(result.Body), "glyphicon-log-out") {
		s.SetCookieHeader(shSetCookiePairs(result.Headers))
		s.SetConfigured(true)
		s.SaveConfig(ctx)
		return nil
	}
	s.SetConfigured(false)
	return &indexer.IndexerError{
		IndexerID: s.ID(),
		Kind:      indexer.KindConfig,
		Message:   "The username and password entered do not match.",
	}
}

func (s *Shazbat) ResultsForQuery(ctx context.Context, q *models.TorznabQuery, isMeta bool) (*indexer.IndexerResult, error) {
	return s.ResultsForQueryWith(ctx, q, isMeta, s.perform)
}

func (s *Shazbat) Test(ctx context.Context) error {
	return s.Base.Test(ctx, s.perform)
}

var shInfoRe = regexp.MustCompile(`\((\d+)\)\s*:(\d+) / :(\d+)$`)

// perform ports upstream PerformQuery (Shazbat.cs:98-193).
func (s *Shazbat) perform(ctx context.Context, query *models.TorznabQuery) ([]*models.ReleaseInfo, error) {
	if s.Client == nil {
		return nil, errors.New("http client not configured")
	}

	var releases []*models.ReleaseInfo
	var searchURLs []shPageRequest

	hasGlobalFreeleech := false

	searchTerm := query.SanitizedSearchTerm()
	term := shFixSearchTerm(searchTerm)

	if strings.TrimSpace(term) != "" {
		searchParameters := shared.QueryParams{}
		searchParameters.Add("search", term)
		searchParameters.Add("portlet", "true")

		response, err := s.requestWithRelogin(ctx, s.searchURL()+"?"+searchParameters.GetQueryString(), s.torrentsURL(), map[string]string{
			"X-Requested-With": "XMLHttpRequest",
		})
		if err != nil {
			return nil, err
		}

		hasGlobalFreeleech = shHasGlobalFreeleech(string(response.Body))

		pageReleases, err := s.parseResults(response.Body, query, searchTerm, hasGlobalFreeleech)
		if err != nil {
			return nil, err
		}
		releases = append(releases, pageReleases...)

		doc, err := goquery.NewDocumentFromReader(strings.NewReader(string(response.Body)))
		if err != nil {
			return nil, s.parseFailure(string(response.Body), err)
		}

		shows := doc.Find("div.show[data-id]")
		if shows.Length() > 0 {
			showPagesFetchLimit := s.showPagesFetchLimit()

			if showPagesFetchLimit < 1 || showPagesFetchLimit > 5 {
				return nil, fmt.Errorf("Value for Show Pages Fetch Limit should be between 1 and 5. Current value: %d.", showPagesFetchLimit) //nolint:staticcheck // upstream Shazbat.cs error text, kept 1:1
			}

			if shows.Length() > showPagesFetchLimit {
				s.log().Debug(fmt.Sprintf("Your search returned %d shows. Use a more specific search term for more relevant results.", shows.Length()))
			}

			shows.Slice(0, showPagesFetchLimit).Each(func(_ int, show *goquery.Selection) {
				showTorrentsQueryParams := shared.QueryParams{}
				showTorrentsQueryParams.Add("id", show.AttrOr("data-id", ""))
				showTorrentsQueryParams.Add("show_mode", "torrents")
				showTorrentsQueryParams.Add("portlet", "true")
				showTorrentsQueryParams.Add("tab", "true")

				searchURLs = append(searchURLs, shPageRequest{
					url:     s.showURL() + "?" + showTorrentsQueryParams.GetQueryString(),
					headers: map[string]string{"X-Requested-With": "XMLHttpRequest"},
				})
			})
		}
	} else {
		searchURLs = append(searchURLs, shPageRequest{url: s.torrentsURL()})
	}

	for _, searchURL := range searchURLs {
		response, err := s.requestWithRelogin(ctx, searchURL.url, "", searchURL.headers)
		if err != nil {
			return nil, err
		}

		pageReleases, err := s.parseResults(response.Body, query, searchTerm, hasGlobalFreeleech)
		if err != nil {
			return nil, s.parseFailure(string(response.Body), err)
		}
		releases = append(releases, pageReleases...)
	}

	return releases, nil
}

type shPageRequest struct {
	url     string
	headers map[string]string
}

// requestWithRelogin performs one GET (with cookies + optional referer and
// headers), refreshing the session and retrying once when the response looks
// logged out (upstream ReloginIfNecessaryAsync, Shazbat.cs:284-299).
func (s *Shazbat) requestWithRelogin(ctx context.Context, target, referer string, headers map[string]string) (*httpclient.Result, error) {
	request := func() (*httpclient.Result, error) {
		return s.Client.Do(ctx, s.ID(), &httpclient.Request{
			URL:     target,
			Cookies: s.CookieHeader(),
			Referer: referer,
			Headers: headers,
		})
	}

	response, err := request()
	if err != nil {
		return nil, err
	}

	body := strings.ToLower(string(response.Body))
	if strings.Contains(body, "sign in now") ||
		(strings.Contains(body, "fullredirect") && strings.Contains(body, "login")) {
		s.log().Debug("Shazbat session expired, relogin.")

		if _, err := s.ApplyConfiguration(ctx, nil); err != nil {
			return nil, err
		}

		response, err = request()
		if err != nil {
			return nil, err
		}
	}

	return response, nil
}

// shHasGlobalFreeleech mirrors the upstream selector
// `span:contains("Freeleech until:"):has(span.datetime)` (Shazbat.cs:130,204).
func shHasGlobalFreeleech(content string) bool {
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(content))
	if err != nil {
		return false
	}
	found := false
	doc.Find("span").EachWithBreak(func(_ int, sp *goquery.Selection) bool {
		if strings.Contains(sp.Text(), "Freeleech until:") && sp.Find("span.datetime").Length() > 0 {
			found = true
			return false
		}
		return true
	})
	return found
}

// parseResults ports upstream ParseResults (Shazbat.cs:195-264).
func (s *Shazbat) parseResults(body []byte, query *models.TorznabQuery, searchTerm string, hasGlobalFreeleech bool) ([]*models.ReleaseInfo, error) {
	var releases []*models.ReleaseInfo

	doc, err := goquery.NewDocumentFromReader(strings.NewReader(string(body)))
	if err != nil {
		return nil, err
	}

	if !hasGlobalFreeleech {
		hasGlobalFreeleech = shHasGlobalFreeleech(string(body))
	}

	publishDate := time.Now()

	var parseErr error
	doc.Find("#torrent-table tr.eprow, table tr.eprow").EachWithBreak(func(_ int, row *goquery.Selection) bool {
		title := shParseTitle(row.Find("td:nth-of-type(3)").First())

		if (query.ImdbID == "" || !shMovieImdbAvailable(s.Caps)) && !query.MatchQueryStringAND(title, 0, searchTerm) {
			return true
		}

		loadHref := row.Find(`td:nth-of-type(5) a[href^="load_torrent?"]`).First().AttrOr("href", "")
		link, err := url.Parse(s.SiteLink() + loadHref)
		if err != nil {
			parseErr = err
			return false
		}
		detailsHref := row.Find(`td:nth-of-type(5) [href^="torrent_info?"]`).First().AttrOr("href", "")
		details, err := url.Parse(s.SiteLink() + detailsHref)
		if err != nil {
			parseErr = err
			return false
		}

		infoString := strings.TrimSpace(row.Find("td:nth-of-type(4)").First().Text())
		matchInfo := shInfoRe.FindStringSubmatch(infoString)
		var size, seeders, leechers int64
		if matchInfo != nil {
			if n, err := strconv.ParseInt(matchInfo[1], 10, 64); err == nil {
				size = n
			}
			if n, err := strconv.ParseInt(matchInfo[2], 10, 64); err == nil {
				seeders = n
			}
			if n, err := strconv.ParseInt(matchInfo[3], 10, 64); err == nil {
				leechers = n
			}
		}

		// upstream distinguishes a MISSING .datetime[data-timestamp] element
		// (null → running date) from a present attribute fed to TryCoerceLong
		// (Shazbat.cs:230-233); AttrOr would conflate the two and coerce ""
		var ts int64
		haveTs := false
		if attr, ok := row.Find(".datetime[data-timestamp]").First().Attr("data-timestamp"); ok {
			ts, haveTs = shTryCoerceLong(attr)
		}
		if haveTs {
			publishDate = dateutil.UnixTimestampToDateTime(ts)
		} else {
			publishDate = publishDate.Add(-time.Minute)
		}

		dvf := 1.0
		if hasGlobalFreeleech {
			dvf = 0
		}
		uvf := 1.0
		minimumRatio := 1.0
		minimumSeedTime := int64(172800) // 48 hours
		peers := seeders + leechers

		var genres []string
		row.Find("label.label-tag").Each(func(_ int, tag *goquery.Selection) {
			genres = append(genres, strings.TrimSpace(tag.Text()))
		})

		release := &models.ReleaseInfo{
			Guid:                 link,
			Link:                 link,
			Details:              details,
			Title:                title,
			Category:             shParseCategories(title),
			Size:                 &size,
			Seeders:              &seeders,
			Peers:                &peers,
			PublishDate:          publishDate,
			Genres:               genres,
			DownloadVolumeFactor: &dvf,
			UploadVolumeFactor:   &uvf,
			MinimumRatio:         &minimumRatio,
			MinimumSeedTime:      &minimumSeedTime,
		}

		posterStyle := row.Find(`div[style^="cursor: pointer; background-image:url"]`).First().AttrOr("style", "")
		if posterStyle != "" {
			posterStr := ""
			if m := shPosterRe.FindStringSubmatch(posterStyle); m != nil {
				posterStr = m[1]
			}
			poster, err := url.Parse(s.SiteLink() + posterStr)
			if err == nil {
				release.Poster = poster
			}
		}

		releases = append(releases, release)
		return true
	})
	if parseErr != nil {
		return nil, parseErr
	}

	return releases, nil
}

var shPosterRe = regexp.MustCompile(`url\('(.*)'\);`)

// shTryCoerceLong mirrors upstream ParseUtil.TryCoerceLong
// (ParseUtil.cs:66): long.TryParse(NormalizeNumber(str, true), Any).
// NormalizeNumber maps "" to "0" (ParseUtil.cs:47), so "" coerces to
// (0, true) — only a MISSING attribute is falsy upstream (checked by the
// caller against dateTimestamp == null, Shazbat.cs:231).
func shTryCoerceLong(str string) (int64, bool) {
	var b strings.Builder
	for _, c := range str {
		if (c >= '0' && c <= '9') || c == '.' || c == ',' {
			b.WriteRune(c)
		}
	}
	valStr := strings.TrimSpace(b.String())
	valStr = strings.ReplaceAll(valStr, "-", "0")
	if strings.Contains(valStr, ",") && strings.Contains(valStr, ".") {
		// upstream leaves both separators in; NumberStyles.Any fails to parse
		return 0, false
	}
	if valStr == "" {
		valStr = "0"
	} else {
		valStr = strings.ReplaceAll(valStr, ".", ",")
	}
	// NumberStyles.Any accepts group separators.
	valStr = strings.ReplaceAll(valStr, ",", "")
	n, err := strconv.ParseInt(valStr, 10, 64)
	return n, err == nil
}

// shParseTitle ports upstream ParseTitle (Shazbat.cs:266-271): the first
// direct text child of the title cell.
func shParseTitle(titleRow *goquery.Selection) string {
	if titleRow.Length() == 0 || len(titleRow.Nodes) == 0 {
		return ""
	}
	for n := titleRow.Nodes[0].FirstChild; n != nil; n = n.NextSibling {
		if n.Type == html.TextNode && strings.TrimSpace(n.Data) != "" {
			return strings.TrimSpace(n.Data)
		}
	}
	return ""
}

// shFixSearchTerm ports upstream FixSearchTerm (Shazbat.cs:273-280).
func shFixSearchTerm(term string) string {
	term = shSeasonEpRe.ReplaceAllString(term, "")
	term = shYearRe.ReplaceAllString(term, "$1")
	term = shSpaceRe.ReplaceAllString(term, " ")
	return strings.ToLower(strings.TrimSpace(term))
}

var (
	// upstream: \b[S|E]\d+\b (character class S, |, E)
	shSeasonEpRe = regexp.MustCompile(`(?i)\b[S|E]\d+\b`)
	shYearRe     = regexp.MustCompile(`(.+)\b\d{4}(\.\d{2}\.\d{2})?\b`)
	shSpaceRe    = regexp.MustCompile(`[\.\s\(\)\[\]]+`)
)

// shParseCategories ports upstream ParseCategories (Shazbat.cs:282).
func shParseCategories(title string) []int {
	if strings.Contains(title, "1080p") || strings.Contains(title, "1080i") || strings.Contains(title, "720p") {
		return []int{models.CatTVHD.ID}
	}
	return []int{models.CatTVSD.ID}
}

// shMovieImdbAvailable mirrors the upstream computed property
// (MovieSearchParams.Contains(ImdbId)) — Shazbat declares no movie search
// params, so this is always false here.
func shMovieImdbAvailable(caps *models.Capabilities) bool {
	for _, p := range caps.MovieSearchParams {
		if p == models.MovieParamImdbID {
			return true
		}
	}
	return false
}

// parseFailure mirrors upstream OnParseError: log the dump, surface
// "Parse error".
func (s *Shazbat) parseFailure(dump string, err error) error {
	s.log().Error(fmt.Sprintf("Error while parsing result: %v\n%s", err, dump))
	return &indexer.IndexerError{IndexerID: s.ID(), Kind: indexer.KindParse, Message: "Parse error"}
}

// --- form helpers (batch-scoped names: file-list test mode) -----------------

type shFormPairs [][2]string

func (p shFormPairs) values() url.Values {
	v := url.Values{}
	for _, kv := range p {
		v.Add(kv[0], kv[1])
	}
	return v
}

var shFormContentType = map[string]string{"Content-Type": "application/x-www-form-urlencoded"}

// shSetCookiePairs mirrors upstream WebResult.Cookies (see scdSetCookiePairs).
func shSetCookiePairs(h http.Header) string {
	if h == nil {
		return ""
	}
	values := h.Values("Set-Cookie")
	last := map[string]string{}
	var order []string
	for _, v := range values {
		eq := strings.Index(v, "=")
		if eq < 0 {
			continue
		}
		name := v[:eq]
		pair := v
		if semi := strings.Index(v, ";"); semi >= 0 {
			pair = v[:semi]
		}
		if _, seen := last[name]; !seen {
			order = append(order, name)
		}
		last[name] = pair
	}
	pairs := make([]string, 0, len(order))
	for _, name := range order {
		pairs = append(pairs, last[name])
	}
	return strings.Join(pairs, " ")
}

// log is a nil-safe logger for the manual package (Base.Logger may be nil).
func (s *Shazbat) log() *slog.Logger {
	if s.Logger != nil {
		return s.Logger
	}
	return slog.Default()
}
