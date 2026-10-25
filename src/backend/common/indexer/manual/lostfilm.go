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
	"vestt/common/utils/parseutil"
)

// LostFilm ports Jackett.Common/Indexers/Definitions/LostFilm.cs.
//
// mechanism: html-login (captcha login via ajaxik.php; series search JSON
// API with keyword morphing; series/season/episode page chain; v_search.php
// tracker redirection pages)
//
// Ported from: Definitions/LostFilm.cs.
//
// 澄清记录:
//   - upstream LoadValuesFromJson pins an expired *.tw certificate
//     (34287FB53A58EC6AE590E7DD7E03C70C0263CADC); Go has no per-host cert
//     pinning hook in the client stack, so this is a documented no-op.
//   - upstream DateTime.TryParse with the ru-RU culture is ported as
//     explicit Russian-month parsing ("09 марта 2012" / "23.05.2017"); the
//     upstream fallback DateTime.Now is kept for unparseable input.
//   - upstream requestDelay is not set for this indexer (no delay to port).
type LostFilm struct {
	indexer.Base
	cfg *lostFilmConfig
}

// lostFilmConfig ports upstream ConfigurationDataCaptchaLogin. Declared
// fields keep the upstream registration order (no AddDynamic needed).
type lostFilmConfig struct {
	configdata.ConfigData
	Username      *configdata.StringItem
	Password      *configdata.StringItem
	CaptchaImage  *configdata.ImageItem
	CaptchaText   *configdata.StringItem
	CaptchaCookie *configdata.HiddenItem
	Instructions  *configdata.InfoItem
}

func newLostFilmConfig() *lostFilmConfig {
	return &lostFilmConfig{
		ConfigData:    *configdata.NewConfigData(),
		Username:      configdata.NewStringItem("Username"),
		Password:      configdata.NewStringItem("Password"),
		CaptchaImage:  configdata.NewImageItem("Captcha Image"),
		CaptchaText:   configdata.NewStringItem("Captcha Text"),
		CaptchaCookie: configdata.NewHiddenItem("Captcha Cookie"),
		Instructions:  configdata.NewInfoItem("", ""),
	}
}

var (
	lostFilmPlayEpisodeRe = regexp.MustCompile(`(?i)PlayEpisode\('(\d+)(\d{3})(\d{3})'\)`)
	lostFilmReleaseInfoRe = regexp.MustCompile(`(?i)Видео:\ (.+). Размер:\ (.+). Перевод`)
)

// NewLostFilm builds the indexer (services.Factory shape).
func NewLostFilm(deps services.Deps) (indexer.Indexer, error) {
	cfg := newLostFilmConfig()
	l := &LostFilm{cfg: cfg}
	l.Base = indexer.Base{
		IDValue:          "lostfilm",
		NameValue:        "LostFilm.tv",
		DescriptionValue: "LostFilm is a RUSSIAN Semi-Private site. Unique portal for foreign series",
		SiteLinkValue:    "https://www.lostfilm.tv/",
		DefaultSiteLink:  "https://www.lostfilm.tv/",
		AltSiteLinks: []string{
			"https://www.lostfilm.tv/",
			"https://www.lostfilmtv5.site/",
			"https://www.lostfilmtv2.site/",
			"https://www.lostfilmtv3.site/",
			"https://www.lostfilm.today/",
			"https://www.lostfilm.download/",
			"https://www.lostfilm.run/",
			"https://lostfilm.site/",
			"https://www.lostfilm.life/",
			"https://www.lostfilm.uno/",
			"https://www.lostfilm.tw/",
		},
		LegacySiteLinks: []string{
			"https://lostfilm.tw/",
			"https://www.lostfilm.win/",
			"https://www.lostfilmtv.site/",
		},
		LangValue: "ru-RU",
		TypeValue: "semi-private",
		Caps:      lostFilmCaps(),
		Config:    cfg,
		Client:    deps.Client,
		Saver:     deps.Saver,
		Cache:     deps.Cache,
	}
	return l, nil
}

func lostFilmCaps() *models.Capabilities {
	caps := models.NewCapabilities()
	caps.TVSearchParams = []string{models.TVParamQ, models.TVParamSeason, models.TVParamEp}
	caps.MovieSearchParams = []string{models.MovieParamQ}
	// TODO: review if there is only this category (movie search is enabled)
	caps.Categories.AddCategoryMapping("1", models.CatTV, "")
	return caps
}

// log is a nil-safe logger (Base.Logger may be nil).
func (l *LostFilm) log() *slog.Logger {
	if l.Logger != nil {
		return l.Logger
	}
	return slog.Default()
}

func (l *LostFilm) loginURL() string     { return l.SiteLink() + "login" }
func (l *LostFilm) apiURL() string       { return l.SiteLink() + "ajaxik.php" }
func (l *LostFilm) discoveryURL() string { return l.SiteLink() + "new" }
func (l *LostFilm) releaseURL() string   { return l.SiteLink() + "v_search.php" }

// ConfigurationForSetup ports upstream GetConfigurationForSetup: fetch the
// login page, extract the captcha image (when present) and remember the
// captcha cookie.
func (l *LostFilm) ConfigurationForSetup(ctx context.Context) ([]map[string]any, error) {
	l.PrefillDefaultSiteLink(l.cfg)
	if l.Client == nil {
		// no client (construction-time form inspection): serve the form as-is
		return configdata.ItemsJSON(l.cfg)
	}
	loginPage, err := l.Client.Do(ctx, l.ID(), &httpclient.Request{
		URL:     l.loginURL(),
		Cookies: l.CookieHeader(),
	})
	if err != nil {
		return nil, err
	}
	doc, err := goquery.NewDocumentFromReader(bytes.NewReader(loginPage.Body))
	if err != nil {
		return nil, l.parseFailure(string(loginPage.Body), err)
	}
	if src, ok := doc.Find("img#captcha_pictcha").First().Attr("src"); ok {
		captchaURL := l.SiteLink() + strings.TrimLeft(src, "/")
		captchaImage, err := l.Client.Do(ctx, l.ID(), &httpclient.Request{
			URL:     captchaURL,
			Cookies: lostFilmCookieHeader(loginPage),
		})
		if err != nil {
			return nil, err
		}
		l.cfg.CaptchaImage.Value = captchaImage.Body
	} else {
		l.cfg.CaptchaImage.Value = []byte{}
	}
	cookies := lostFilmCookieHeader(loginPage)
	l.cfg.CaptchaCookie.Value = cookies
	l.SetCookieHeader(cookies)
	return configdata.ItemsJSON(l.cfg)
}

// ApplyConfiguration ports upstream LostFilm.ApplyConfiguration: e-mail
// validation, logout (to invalidate any previous session), login post and
// the upstream error mapping.
func (l *LostFilm) ApplyConfiguration(ctx context.Context, items []indexer.ConfigItem) (indexer.ConfigStatus, error) {
	if err := l.ApplyItems(items); err != nil {
		return indexer.ConfigCompleted, err
	}
	if err := l.LoadValuesFromJSON(nil, false); err != nil {
		return indexer.ConfigCompleted, err
	}

	if !strings.Contains(l.cfg.Username.Value, "@") {
		return indexer.ConfigCompleted, &indexer.IndexerError{
			IndexerID: l.ID(), Kind: indexer.KindConfig,
			Message: "Username must be an e-mail address",
		}
	}

	// Performing Logout is required to invalidate previous session otherwise
	// the `{"error":1,"result":"ok"}` will be returned.
	if err := l.logout(ctx); err != nil {
		l.log().Error("LostFilm: logout failed: " + err.Error())
	}

	data := formPairs{
		{"act", "users"},
		{"type", "login"},
		{"mail", l.cfg.Username.Value},
		{"pass", l.cfg.Password.Value},
		{"rem", "1"},
	}
	if strings.TrimSpace(l.cfg.CaptchaText.Value) != "" {
		data = append(data, [2]string{"need_captcha", "1"}, [2]string{"captcha", l.cfg.CaptchaText.Value})
	}

	res, err := l.Client.Do(ctx, l.ID(), &httpclient.Request{
		Method:  http.MethodPost,
		URL:     l.apiURL(),
		RawBody: data.encode(),
		Headers: formContentType,
		Cookies: l.CookieHeader(),
		Referer: l.SiteLink(),
	})
	if err != nil {
		return indexer.ConfigCompleted, err
	}
	content := string(res.Body)

	// upstream ConfigureIfOK(result.Cookies, content.Contains("\"success\":true"), ...)
	if !strings.Contains(content, `"success":true`) {
		errorMessage := content
		if strings.Contains(errorMessage, `"error":1`) || strings.Contains(errorMessage, `"error":2`) || strings.Contains(errorMessage, `"error":4`) {
			errorMessage = "Captcha is incorrect"
		}
		if strings.Contains(errorMessage, `"error":3`) {
			errorMessage = "E-mail or password is incorrect"
		}
		return indexer.ConfigCompleted, &indexer.IndexerError{
			IndexerID: l.ID(), Kind: indexer.KindConfig, Message: errorMessage,
		}
	}

	cookies := lostFilmCookieHeader(res)
	if u, err := url.Parse(l.SiteLink()); err == nil {
		cookies = lostFilmMergeCookies(cookies, l.Client.CookieHeader(l.ID(), u))
	}
	l.SetCookieHeader(cookies)
	l.SetConfigured(true)
	l.SaveConfig(ctx)
	return indexer.ConfigRequiresTesting, nil
}

// logout ports upstream LostFilm.Logout.
func (l *LostFilm) logout(ctx context.Context) error {
	if l.Client == nil {
		return errors.New("http client not configured")
	}
	l.log().Info("Performing logout")
	data := formPairs{
		{"act", "users"},
		{"type", "logout"},
	}
	res, err := l.Client.Do(ctx, l.ID(), &httpclient.Request{
		Method:  http.MethodPost,
		URL:     l.apiURL(),
		RawBody: data.encode(),
		Headers: formContentType,
		Cookies: l.CookieHeader(),
	})
	if err != nil {
		return err
	}
	l.log().Debug("Logout result: " + string(res.Body))
	if res.StatusCode != http.StatusOK {
		l.log().Error("Logout failed with response: " + string(res.Body))
		return fmt.Errorf("logout failed with status %d", res.StatusCode)
	}
	return nil
}

// ResultsForQuery implements indexer.Indexer.
func (l *LostFilm) ResultsForQuery(ctx context.Context, q *models.TorznabQuery, isMeta bool) (*indexer.IndexerResult, error) {
	return l.ResultsForQueryWith(ctx, q, isMeta, l.perform)
}

// Test implements indexer.Indexer.
func (l *LostFilm) Test(ctx context.Context) error {
	return l.Base.Test(ctx, l.perform)
}

// perform ports upstream LostFilm.PerformQuery: empty/test queries go to
// the discovery page, anything else to the keyword-morphing search.
func (l *LostFilm) perform(ctx context.Context, query *models.TorznabQuery) ([]*models.ReleaseInfo, error) {
	// If the search string is empty use the latest releases
	if query.IsTest || strings.TrimSpace(query.SearchTerm) == "" {
		return l.fetchNewReleases(ctx)
	}
	return l.performSearch(ctx, query)
}

func (l *LostFilm) get(ctx context.Context, target string) (*httpclient.Result, error) {
	if l.Client == nil {
		return nil, errors.New("http client not configured")
	}
	res, err := l.Client.Do(ctx, l.ID(), &httpclient.Request{
		URL:     target,
		Cookies: l.CookieHeader(),
	})
	if err != nil {
		return nil, err
	}
	return res, nil
}

// requestStringAndRelogin ports upstream RequestStringAndRelogin: a "503
// Service" page is a config error; a login redirect triggers a re-login and
// a retry.
func (l *LostFilm) requestStringAndRelogin(ctx context.Context, target string) (*httpclient.Result, error) {
	results, err := l.get(ctx, target)
	if err != nil {
		return nil, err
	}
	if bytes.Contains(results.Body, []byte("503 Service")) {
		return nil, &indexer.IndexerError{
			IndexerID: l.ID(), Kind: indexer.KindConfig,
			Message: string(results.Body),
		}
	}
	if bytes.Contains(results.Body, []byte(`href="/login"`)) {
		// Re-login
		if _, err := l.ApplyConfiguration(ctx, nil); err != nil {
			return nil, err
		}
		return l.get(ctx, target)
	}
	return results, nil
}

// --- search -----------------------------------------------------------------

// performSearch ports upstream LostFilm.PerformSearch: the keyword-morphing
// algorithm (reduce the series search term word by word, filter candidate
// series, then apply the remaining words as an episode filter on the series
// page).
func (l *LostFilm) performSearch(ctx context.Context, query *models.TorznabQuery) ([]*models.ReleaseInfo, error) {
	l.log().Debug(fmt.Sprintf("PerformSearch: %s [%s]", query.SanitizedSearchTerm(), query.QueryType))
	var releases []*models.ReleaseInfo

	// Search query words. Consists of Series keywords that will be used for
	// series search request, and Episode keywords that will be used for
	// episode filtering.
	keywords := strings.Split(query.SanitizedSearchTerm(), " ")
	// Keywords count related to Series Search.
	searchKeywords := len(keywords)
	// Keywords count related to Series Filter.
	serieFilterKeywords := 0
	// Overall (keywords.count - searchKeywords - serieFilterKeywords) are
	// related to episode filter

	for {
		searchString := strings.Join(keywords[:searchKeywords], " ")
		data := formPairs{
			{"act", "common"},
			{"type", "search"},
			{"val", searchString},
		}
		l.log().Debug("> Searching: " + searchString)
		res, err := l.Client.Do(ctx, l.ID(), &httpclient.Request{
			Method:  http.MethodPost,
			URL:     l.apiURL(),
			RawBody: data.encode(),
			Headers: formContentType,
			Cookies: l.CookieHeader(),
		})
		if err != nil {
			return nil, err
		}
		reduce := false
		if len(res.Body) == 0 {
			l.log().Debug("> Empty series response for query: " + searchString)
			reduce = true
		} else {
			more, err := l.handleSeriesSearchResponse(ctx, res.Body, query, keywords, searchKeywords, &serieFilterKeywords, &releases)
			if err != nil {
				return nil, err
			}
			reduce = more
		}
		if !reduce {
			break // Search loop
		}
		searchKeywords--
		if searchKeywords <= 0 {
			break
		}
	}
	return releases, nil
}

// handleSeriesSearchResponse parses one series-search response and walks the
// candidate series. It reports whether the outer search loop should continue
// reducing the search term (more == true).
func (l *LostFilm) handleSeriesSearchResponse(ctx context.Context, body []byte, query *models.TorznabQuery, keywords []string, searchKeywords int, serieFilterKeywords *int, releases *[]*models.ReleaseInfo) (more bool, err error) {
	var parsed any
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber()
	if err := dec.Decode(&parsed); err != nil {
		return false, l.parseFailure(string(body), err)
	}
	if _, isArray := parsed.([]any); isArray {
		// Search loop
		return true, nil
	}
	jsonObj, ok := parsed.(map[string]any)
	if !ok {
		return true, nil
	}
	// Protect from {"data":false,"result":"ok"}
	jsonData, ok := jsonObj["data"].(map[string]any)
	if !ok {
		return true, nil // Search loop
	}
	jsonSeries, ok := jsonData["series"].([]any)
	if !ok || len(jsonSeries) == 0 {
		return true, nil // Search loop
	}

	var series []map[string]any
	for _, raw := range jsonSeries {
		if s, ok := raw.(map[string]any); ok {
			series = append(series, s)
		}
	}
	l.log().Debug(fmt.Sprintf("> Found %d series: [%s]", len(series), strings.Join(lostFilmSeriesTitles(series), ", ")))

	// Filter found series
	if len(series) > 1 {
		*serieFilterKeywords = len(keywords) - searchKeywords
		for {
			serieFilter := strings.Join(keywords[searchKeywords:searchKeywords+*serieFilterKeywords], " ")
			l.log().Debug("> Filtering: " + serieFilter)
			var filtered []map[string]any
			for _, s := range series {
				if title, ok := s["title_orig"].(string); ok && containsFold(title, serieFilter) {
					filtered = append(filtered, s)
				}
			}
			if len(filtered) > 0 {
				l.log().Debug("> Series filtered: [" + strings.Join(lostFilmSeriesTitles(filtered), ", ") + "]")
				series = filtered
				break // Serie Filter loop
			}
			*serieFilterKeywords--
			if *serieFilterKeywords <= 0 {
				break
			}
		}
	}

	for _, serie := range series {
		link := lostFilmJSONStr(serie["link"])
		season := "/seasons"
		if query.Season != nil && *query.Season > 0 {
			season = "/season_" + strconv.Itoa(*query.Season)
		}
		target := l.SiteLink() + strings.TrimLeft(link, "/") + season

		if query.Episode != "" { // Fetch single episode releases
			// TODO: Add a togglable Quick Path via v_search.php in Indexer Settings
			target += "/episode_" + query.Episode
			episodeReleases, err := l.fetchEpisodeReleases(ctx, target)
			if err != nil {
				return false, err
			}
			*releases = append(*releases, episodeReleases...)
		} else { // Fetch the whole series OR episode with filter applied
			episodeKeywords := keywords[searchKeywords+*serieFilterKeywords:]
			episodeFilterKeywords := len(episodeKeywords)

			// Search for episodes dropping 1 filter word each time when no
			// results has found. Last search will be performed with empty
			// filter
			for {
				filter := strings.Join(episodeKeywords[:episodeFilterKeywords], " ")
				l.log().Debug("> Searching episodes with filter [" + filter + "]")
				taskReleases, err := l.fetchSeriesReleases(ctx, target, query, filter)
				if err != nil {
					return false, err
				}
				if len(taskReleases) > 0 {
					l.log().Debug(fmt.Sprintf("> Found %d episodes", len(taskReleases)))
					*releases = append(*releases, taskReleases...)
					break // Episodes Filter loop
				}
				episodeFilterKeywords--
				if episodeFilterKeywords < 0 {
					break
				}
			}
		}
	}
	return false, nil
}

// --- page parsing -----------------------------------------------------------

// fetchNewReleases ports upstream FetchNewReleases: the discovery page
// lists episode links, each expanded via the episode page.
func (l *LostFilm) fetchNewReleases(ctx context.Context) ([]*models.ReleaseInfo, error) {
	l.log().Debug("FetchNewReleases: " + l.discoveryURL())
	res, err := l.Client.Do(ctx, l.ID(), &httpclient.Request{
		URL:     l.discoveryURL(),
		Referer: l.SiteLink(),
		Cookies: l.CookieHeader(),
	})
	if err != nil {
		return nil, err
	}
	var releases []*models.ReleaseInfo

	doc, err := goquery.NewDocumentFromReader(bytes.NewReader(res.Body))
	if err != nil {
		return nil, l.parseFailure(string(res.Body), err)
	}
	var parseErr error
	doc.Find("div.row").EachWithBreak(func(_ int, row *goquery.Selection) bool {
		href, ok := row.Find("a").First().Attr("href")
		if !ok {
			return true
		}
		episodeURL := l.SiteLink() + strings.TrimLeft(href, "/")
		episodeReleases, err := l.fetchEpisodeReleases(ctx, episodeURL)
		if err != nil {
			parseErr = err
			return false
		}
		releases = append(releases, episodeReleases...)
		return true
	})
	if parseErr != nil {
		return nil, parseErr
	}
	return releases, nil
}

// fetchEpisodeReleases ports upstream FetchEpisodeReleases: parse the
// episode page, resolve the play-button episode code and follow the tracker
// redirection chain.
func (l *LostFilm) fetchEpisodeReleases(ctx context.Context, target string) ([]*models.ReleaseInfo, error) {
	l.log().Debug("FetchEpisodeReleases: " + target)
	results, err := l.requestStringAndRelogin(ctx, target)
	if err != nil {
		return nil, err
	}
	var releases []*models.ReleaseInfo

	doc, err := goquery.NewDocumentFromReader(bytes.NewReader(results.Body))
	if err != nil {
		return nil, l.parseFailure(string(results.Body), err)
	}

	playButton := doc.Find("div.external-btn").First()
	if playButton.Length() > 0 && !playButton.HasClass("inactive") {
		details, err := url.Parse(target)
		if err != nil {
			return nil, l.parseFailure(string(results.Body), err)
		}

		dateString := strings.TrimSpace(doc.Find("div.title-block > div.details-pane > div.left-box").First().Text())
		key := "eng: "
		if strings.Contains(dateString, "TBA") {
			key = "ru: "
		}
		// '... Дата выхода eng: 09 марта 2012 г. ...' -> '09 марта 2012'
		dateString = trimBetweenStrings(dateString, key, " г.")

		var date time.Time
		if len(dateString) == 4 {
			// dateString might be just a year
			parsed, err := time.ParseInLocation("2006", dateString, time.Local)
			if err != nil {
				date = time.Now()
			} else {
				date = parsed
			}
		} else {
			// dd mmmm yyyy
			parsed, ok := parseRussianDate(dateString)
			if !ok {
				date = time.Now()
			} else {
				date = parsed
			}
		}

		urlDetails := newTrackerURLDetailsFromButton(playButton)
		episodeReleases, err := l.fetchTrackerReleases(ctx, urlDetails)
		if err != nil {
			return nil, err
		}
		for _, release := range episodeReleases {
			release.Details = details
			release.PublishDate = date
		}
		releases = append(releases, episodeReleases...)
	}
	return releases, nil
}

// fetchSeriesReleases ports upstream FetchSeriesReleases: season pack
// handling plus per-episode rows of the series/season page.
func (l *LostFilm) fetchSeriesReleases(ctx context.Context, target string, query *models.TorznabQuery, filter string) ([]*models.ReleaseInfo, error) {
	seasonStr := ""
	if query.Season != nil {
		seasonStr = strconv.Itoa(*query.Season)
	}
	l.log().Debug(fmt.Sprintf("FetchSeriesReleases: %s S: %s E: %s Filter: %s",
		target, seasonStr, query.Episode, filter))

	var releases []*models.ReleaseInfo
	results, err := l.get(ctx, target)
	if err != nil {
		return nil, err
	}

	doc, err := goquery.NewDocumentFromReader(bytes.NewReader(results.Body))
	if err != nil {
		return nil, l.parseFailure(string(results.Body), err)
	}
	const rowSelector = "table.movie-parts-list > tbody > tr"

	var parseErr error
	doc.Find("div.serie-block").EachWithBreak(func(_ int, season *goquery.Selection) bool {
		// Could be null if serie-block is for Extras
		seasonButton := season.Find("div.movie-details-block > div.external-btn").First()

		// Process only season we're searching for
		if seasonButton.Length() > 0 && query.Season != nil && *query.Season > 0 {
			// If seasonButton in "inactive" it will not contain "onClick"
			// handler. Better to parse element which always exists.
			watchedButton := season.Find("div.movie-details-block > div.haveseen-btn").First()
			buttonCode := watchedButton.AttrOr("data-code", "")
			currentSeason := buttonCode
			if idx := strings.IndexByte(buttonCode, '-'); idx >= 0 {
				currentSeason = buttonCode[idx+1:]
			}

			if currentSeason != strconv.Itoa(*query.Season) {
				return true // Can't match season by regex OR season not matches to a searched one
			}

			// Stop parsing season episodes if season pack was required but
			// it's not available yet.
			if seasonButton.HasClass("inactive") {
				l.log().Debug("> No season pack is found for S" + strconv.Itoa(*query.Season))
				return false
			}
		}

		// Fetch season pack releases if no episode filtering is required. If
		// seasonButton implements "inactive" class there are no season pack
		// available and each episode should be fetched separately.
		if query.Episode == "" && filter == "" && seasonButton.Length() > 0 && !seasonButton.HasClass("inactive") {
			lastEpisode := season.Find(rowSelector).First()
			date := lostFilmDateFromEpisodeColumn(lastEpisode.Find("td.delta").First())

			details, err := url.Parse(target) // Current season(-s) page url
			if err != nil {
				parseErr = err
				return false
			}

			urlDetails := newTrackerURLDetailsFromButton(seasonButton)
			seasonReleases, err := l.fetchTrackerReleases(ctx, urlDetails)
			if err != nil {
				parseErr = err
				return false
			}
			for _, release := range seasonReleases {
				release.Details = details
				release.PublishDate = date
			}
			releases = append(releases, seasonReleases...)

			if query.Season != nil && *query.Season > 0 {
				return false // Searched season was processed
			}

			// Skip parsing separate episodes if season pack was added
			if len(seasonReleases) > 0 {
				return true
			}
		}

		// No season filtering was applied OR season pack in not available
		season.Find(rowSelector).EachWithBreak(func(_ int, row *goquery.Selection) bool {
			if row.HasClass("not-available") {
				return true
			}
			couldBreak := false // Set to `true` if searched episode was found

			if filter != "" {
				titles := row.Find("td.gamma > div").First()
				if !containsFold(titles.Text(), filter) {
					return true
				}
			}

			playButton := row.Find("td.zeta > div.external-btn").First()
			if playButton.Length() == 0 { // #9725
				return true
			}

			if query.Episode != "" {
				m := lostFilmPlayEpisodeRe.FindStringSubmatch(playButton.AttrOr("onclick", ""))
				var episode string
				if m != nil {
					episode = m[3]
				}
				if episode != query.Episode {
					return true
				}
				couldBreak = true
			}

			dateColumn := row.Find("td.delta").First() // Contains both Date and EpisodeURL
			date := lostFilmDateFromEpisodeColumn(dateColumn)

			// goTo('/series/Prison_Break/season_5/episode_9/',false)
			link := trimBetweenChars(dateColumn.AttrOr("onclick", ""), '\'', '\'')
			episodeURL := l.SiteLink() + strings.TrimLeft(link, "/")
			details, err := url.Parse(episodeURL)
			if err != nil {
				l.log().Error(fmt.Sprintf("%s: Error while parsing row: %v", l.ID(), err))
				return true
			}

			urlDetails := newTrackerURLDetailsFromButton(playButton)
			episodeReleases, err := l.fetchTrackerReleases(ctx, urlDetails)
			if err != nil {
				l.log().Error(fmt.Sprintf("%s: Error while parsing row: %v", l.ID(), err))
				return true
			}
			for _, release := range episodeReleases {
				release.Details = details
				release.PublishDate = date
			}
			releases = append(releases, episodeReleases...)

			return !couldBreak
		})
		return parseErr == nil
	})
	if parseErr != nil {
		return nil, l.parseFailure(string(results.Body), parseErr)
	}
	return releases, nil
}

// --- tracker parsing --------------------------------------------------------

// fetchTrackerReleases ports upstream FetchTrackerReleases: the
// v_search.php redirector page with the generated tracker link.
func (l *LostFilm) fetchTrackerReleases(ctx context.Context, details *trackerURLDetails) ([]*models.ReleaseInfo, error) {
	episode := details.episode
	if episode == "" {
		episode = "999" // 999 is a synonym for the whole serie
	}
	var qc shared.QueryParams
	qc.Add("c", details.seriesID)
	qc.Add("s", details.season)
	qc.Add("e", episode)
	q := qc.GetQueryString()
	target := l.releaseURL() + "?" + q

	l.log().Debug("FetchTrackerReleases: " + target)

	// Get redirection page with generated link on it. This link can't be
	// constructed manually as it contains Hash field and hashing algo is
	// unknown.
	results, err := l.get(ctx, target)
	if err != nil {
		return nil, err
	}
	if len(results.Body) == 0 {
		return nil, &indexer.IndexerError{
			IndexerID: l.ID(), Kind: indexer.KindConfig,
			Message: "Empty response from " + target,
		}
	}
	if string(results.Body) == "log in first" {
		return nil, &indexer.IndexerError{
			IndexerID: l.ID(), Kind: indexer.KindConfig,
			Message: string(results.Body),
		}
	}

	doc, err := goquery.NewDocumentFromReader(bytes.NewReader(results.Body))
	if err != nil {
		return nil, l.parseFailure(string(results.Body), err)
	}
	meta := doc.Find("meta").First()
	metaContent := meta.AttrOr("content", "")

	// Follow redirection defined by async url.replace and prepend sitelink
	idx := strings.Index(metaContent, "url=")
	start := 4
	if idx >= 0 {
		start = idx + 5
	}
	if start > len(metaContent) {
		start = len(metaContent)
	}
	redirectionURL := l.SiteLink() + metaContent[start:]
	return l.followTrackerRedirection(ctx, redirectionURL, details)
}

// followTrackerRedirection ports upstream FollowTrackerRedirection: the
// generated tracker page with the actual release items.
func (l *LostFilm) followTrackerRedirection(ctx context.Context, target string, details *trackerURLDetails) ([]*models.ReleaseInfo, error) {
	l.log().Debug("FollowTrackerRedirection: " + target)
	results, err := l.get(ctx, target)
	if err != nil {
		return nil, err
	}
	var releases []*models.ReleaseInfo

	doc, err := goquery.NewDocumentFromReader(bytes.NewReader(results.Body))
	if err != nil {
		return nil, l.parseFailure(string(results.Body), err)
	}
	rows := doc.Find("div.inner-box--item")

	if rows.Length() > 0 {
		l.log().Debug(fmt.Sprintf("> Parsing %d releases", rows.Length()))

		serieTitle := doc.Find("div.inner-box--subtitle").First().Text()
		if idx := strings.LastIndex(serieTitle, ","); idx >= 0 {
			serieTitle = serieTitle[:idx]
		}

		episodeInfo := doc.Find("div.inner-box--text").First().Text()
		episodeName := trimBetweenChars(episodeInfo, '(', ')')

		rows.Each(func(_ int, row *goquery.Selection) {
			release, err := l.parseTrackerRow(row, serieTitle, details, episodeName)
			if err != nil {
				htmlStr, _ := row.Html()
				l.log().Error(fmt.Sprintf("%s: Error while parsing row '%s':\n\n%v", l.ID(), htmlStr, err))
				return
			}
			l.log().Debug("> Add: " + release.Title)
			releases = append(releases, release)
		})
	} else {
		if bytes.Contains(results.Body, []byte("Контент недоступен на территории Российской Федерации")) {
			l.log().Debug("> Content is not available in the Russian Federation")
		} else {
			message := doc.Find("p").First().Text()
			l.log().Debug("> " + message)
		}
	}
	return releases, nil
}

// parseTrackerRow ports the per-row release construction of upstream
// FollowTrackerRedirection.
func (l *LostFilm) parseTrackerRow(row *goquery.Selection, serieTitle string, details *trackerURLDetails, episodeName string) (*models.ReleaseInfo, error) {
	detailsInfo := row.Find("div.inner-box--desc").First().Text()
	m := lostFilmReleaseInfoRe.FindStringSubmatch(detailsInfo)
	if m == nil {
		return nil, fmt.Errorf("Failed to map release details string: %s", detailsInfo) //nolint:staticcheck // upstream LostFilm.cs error text, kept 1:1
	}

	/*
	 * For supported qualities see:
	 *  - TvCategoryParser.cs
	 *  - https://github.com/SickRage/SickRage/wiki/Quality-Settings#quality-names-to-recognize-the-quality-of-a-file
	 */
	quality := strings.TrimSpace(m[1])
	// Adapt shitty quality format for common algorythms
	quality = regexp.MustCompile(`(?i)-Rip`).ReplaceAllString(quality, "Rip")
	quality = regexp.MustCompile(`(?i)WEB-DLRip`).ReplaceAllString(quality, "WEBDL")
	quality = regexp.MustCompile(`(?i)WEB-DL`).ReplaceAllString(quality, "WEBDL")
	quality = regexp.MustCompile(`(?i)HDTVRip`).ReplaceAllString(quality, "HDTV")
	// Fix forgotten p-Progressive suffix in resolution index
	quality = regexp.MustCompile(`(?i)1080 `).ReplaceAllString(quality, "1080p ")
	quality = regexp.MustCompile(`(?i)720 `).ReplaceAllString(quality, "720p ")

	techComponents := []string{
		"rus",
		quality,
		"(LostFilm)",
	}
	var techParts []string
	for _, s := range techComponents {
		if s != "" {
			techParts = append(techParts, s)
		}
	}
	techInfo := strings.Join(techParts, " ")

	// Ru title: downloadLink.TextContent; En title should be manually constructed.
	titleComponents := []string{
		serieTitle,
		details.getEpisodeString(),
		episodeName,
		techInfo,
	}
	downloadLink := row.Find("div.inner-box--link > a").First()
	sizeString := strings.ToUpper(m[2])
	sizeString = strings.ReplaceAll(sizeString, "ТБ", "TB") // untested
	sizeString = strings.ReplaceAll(sizeString, "ГБ", "GB")
	sizeString = strings.ReplaceAll(sizeString, "МБ", "MB")
	sizeString = strings.ReplaceAll(sizeString, "КБ", "KB") // untested
	link, err := url.Parse(strings.TrimSpace(downloadLink.AttrOr("href", "")))
	if err != nil {
		return nil, err
	}

	var titleParts []string
	for _, s := range titleComponents {
		if s != "" {
			titleParts = append(titleParts, s)
		}
	}
	seeders := int64(1)
	peers := int64(2)
	dvf := 0.0
	uvf := 1.0
	minimumRatio := 1.0
	minimumSeedTime := int64(172800) // 48 hours
	size := parseutil.GetBytes(sizeString)

	// TODO this feels sparse compared to other trackers. Expand later
	return &models.ReleaseInfo{
		Category:             []int{models.CatTV.ID},
		Title:                strings.Join(titleParts, " - "),
		Link:                 link,
		Guid:                 link,
		Size:                 &size,
		Seeders:              &seeders,
		Peers:                &peers,
		DownloadVolumeFactor: &dvf,
		UploadVolumeFactor:   &uvf,
		MinimumRatio:         &minimumRatio,
		MinimumSeedTime:      &minimumSeedTime,
	}, nil
}

// --- helpers -----------------------------------------------------------------

// trackerURLDetails ports upstream TrackerUrlDetails (the PlayEpisode
// onclick code of a watch button).
type trackerURLDetails struct {
	seriesID string
	season   string
	episode  string
}

func newTrackerURLDetailsFromButton(button *goquery.Selection) *trackerURLDetails {
	trigger := button.AttrOr("onclick", "")
	m := lostFilmPlayEpisodeRe.FindStringSubmatch(trigger)
	if m == nil {
		return &trackerURLDetails{}
	}
	return &trackerURLDetails{
		seriesID: strings.TrimLeft(m[1], "0"),
		season:   strings.TrimLeft(m[2], "0"),
		episode:  strings.TrimLeft(m[3], "0"),
	}
}

// getEpisodeString ports upstream TrackerUrlDetails.GetEpisodeString.
func (d *trackerURLDetails) getEpisodeString() string {
	result := ""
	if d.season != "" && d.season != "0" && d.season != "999" {
		result += "S" + d.season
		if d.episode != "" && d.episode != "0" && d.episode != "999" {
			result += "E" + d.episode
		}
	}
	return result
}

// trimBetweenChars ports upstream TrimString(s, char, char).
func trimBetweenChars(s string, startChar, endChar byte) string {
	start := strings.IndexByte(s, startChar)
	end := strings.LastIndexByte(s, endChar)
	if start != -1 && end != -1 && end > start {
		return s[start+1 : end]
	}
	return ""
}

// trimBetweenStrings ports upstream TrimString(s, string, string).
func trimBetweenStrings(s, startString, endString string) string {
	start := strings.Index(s, startString)
	end := strings.LastIndex(s, endString)
	if start != -1 && end != -1 && end >= start+len(startString) {
		return s[start+len(startString) : end]
	}
	return ""
}

// containsFold mirrors upstream string.Contains(..., OrdinalIgnoreCase).
func containsFold(s, substr string) bool {
	return strings.Contains(strings.ToLower(s), strings.ToLower(substr))
}

// lostFilmDateFromEpisodeColumn ports upstream DateFromEpisodeColumn.
func lostFilmDateFromEpisodeColumn(dateColumn *goquery.Selection) time.Time {
	dateString := strings.TrimSpace(dateColumn.Find("span.small-text").First().Text())
	if dateString == "" {
		// 'Eng: 23.05.2017' -> '23.05.2017' OR '23.05.2017' -> '23.05.2017'
		dateString = strings.TrimSpace(dateColumn.Find("span").First().Text())
	} else if idx := strings.IndexByte(dateString, ':'); idx >= 0 && idx+2 <= len(dateString) {
		dateString = strings.TrimSpace(dateString[idx+2:])
	}
	// dd.mm.yyyy
	for _, layout := range []string{"02.01.2006", "2.01.2006"} {
		if t, err := time.ParseInLocation(layout, dateString, time.Local); err == nil {
			return t
		}
	}
	return time.Now()
}

// russianMonths maps Russian genitive month names (ru-RU date formatting).
var russianMonths = map[string]time.Month{
	"января":   time.January,
	"февраля":  time.February,
	"марта":    time.March,
	"апреля":   time.April,
	"мая":      time.May,
	"июня":     time.June,
	"июля":     time.July,
	"августа":  time.August,
	"сентября": time.September,
	"октября":  time.October,
	"ноября":   time.November,
	"декабря":  time.December,
}

// parseRussianDate ports DateTime.TryParse(dateString, ru-RU) for the
// "dd mmmm yyyy" shape used on episode pages.
func parseRussianDate(s string) (time.Time, bool) {
	fields := strings.Fields(s)
	if len(fields) != 3 {
		return time.Time{}, false
	}
	day, err := strconv.Atoi(fields[0])
	if err != nil {
		return time.Time{}, false
	}
	month, ok := russianMonths[strings.ToLower(fields[1])]
	if !ok {
		return time.Time{}, false
	}
	year, err := strconv.Atoi(fields[2])
	if err != nil {
		return time.Time{}, false
	}
	return time.Date(year, month, day, 0, 0, 0, 0, time.Local), true
}

// lostFilmSeriesTitles renders the title_orig values for debug logging.
func lostFilmSeriesTitles(series []map[string]any) []string {
	titles := make([]string, 0, len(series))
	for _, s := range series {
		titles = append(titles, lostFilmJSONStr(s["title_orig"]))
	}
	return titles
}

// lostFilmJSONStr renders a loosely-decoded JSON scalar as a string.
func lostFilmJSONStr(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return t
	case json.Number:
		return t.String()
	default:
		return fmt.Sprintf("%v", t)
	}
}

// lostFilmCookieHeader renders a response's Set-Cookie headers in
// "name=value; name2=value2" Cookie-header form (upstream WebResult.Cookies).
func lostFilmCookieHeader(res *httpclient.Result) string {
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

// lostFilmMergeCookies unions two Cookie-header strings (first occurrence
// wins), mirroring upstream ResolveCookies accumulation.
func lostFilmMergeCookies(a, b string) string {
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

func (l *LostFilm) parseFailure(dump string, err error) error {
	l.log().Error(fmt.Sprintf("Error while parsing result: %v\n%s", err, dump))
	return &indexer.IndexerError{IndexerID: l.ID(), Kind: indexer.KindParse, Message: "Parse error"}
}
