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

// Toloka ports Jackett.Common/Indexers/Definitions/Toloka.cs.
//
// mechanism: html-login (POST login.php with autologin/ssl, "logout=true"
// check, toloka_*_u cookie filtering) with tracker.php search and HTML
// results-table parsing (table.forumline rows).
//
// 澄清记录:
//   - upstream ResolveCookies strips every cookie whose name starts with
//     "toloka_" and ends with "_u" (cs:240-248); the port applies the filter
//     when persisting the cookie header after login (the Go client jar keeps
//     serving the cookies, mirroring upstream where ResolveCookies is only
//     consulted for the stored CookieHeader).
//   - upstream searches are re-issued after a re-login when the page lacks
//     "logout=true" (cs:291-296); the port mirrors this, calling
//     ApplyConfiguration with nil items (LoadValuesFromJson(null) keeps the
//     stored values upstream).
//   - upstream ConfigurationDataToloka extends ConfigurationDataBasicLogin;
//     the Go port declares Username/Password/Instructions as fields.
//   - upstream parses each row inside its own try/catch and only logs the
//     error (cs:360-364): a broken row is skipped, not fatal. The outer
//     try/catch (cs:365-369) aborts the whole query via OnParseError.
//   - Toloka.TitleParser nested class is ported file-locally as
//     tkTitleParser; the balanced-parens/brackets tag regexes use .NET
//     balancing groups (?<c>)/(?(c)(?!)) (cs:378-379) which Go RE2 cannot
//     express — a scanner yields the same non-overlapping balanced groups.
//     Go's \W is ASCII-only (upstream .NET \W is Unicode-aware); for
//     Cyrillic text the observable behaviour matches because ASCII
//     punctuation dominates the tag boundaries.
//   - the empty-search branch adds nm="" (the upstream comment says "use the
//     getnew view" but the code adds the nm parameter, cs:267-270); ported
//     as written.
//
// Ported from: Definitions/Toloka.cs,
// Models/IndexerConfig/Bespoke/ConfigurationDataToloka.cs.
type Toloka struct {
	indexer.Base
	cfg *tolokaConfig
}

// tolokaConfig ports upstream ConfigurationDataToloka (BasicLogin +
// FreeleechOnly + StripCyrillicLetters).
type tolokaConfig struct {
	configdata.ConfigData
	Username             *configdata.StringItem
	Password             *configdata.PasswordItem
	Instructions         *configdata.InfoItem
	FreeleechOnly        *configdata.BoolItem
	StripCyrillicLetters *configdata.BoolItem
}

func newTolokaConfig() *tolokaConfig {
	return &tolokaConfig{
		ConfigData:           *configdata.NewConfigData(),
		Username:             configdata.NewStringItem("Username"),
		Password:             configdata.NewPasswordItem("Password"),
		Instructions:         configdata.NewInfoItem("", ""),
		FreeleechOnly:        configdata.NewBoolItem("Show freeleech only"),
		StripCyrillicLetters: configdata.NewBoolItem("Strip Cyrillic Letters"),
	}
}

// NewToloka builds the indexer (services.Factory shape).
func NewToloka(deps services.Deps) (indexer.Indexer, error) {
	t := &Toloka{cfg: newTolokaConfig()}
	// upstream { Value = true } for StripCyrillicLetters
	t.cfg.StripCyrillicLetters.Value = true
	t.Base = indexer.Base{
		IDValue:          "toloka",
		NameValue:        "Toloka.to",
		DescriptionValue: "Toloka is a UKRAINIAN Semi-Private site with a thriving file-sharing community",
		SiteLinkValue:    "https://toloka.to/",
		DefaultSiteLink:  "https://toloka.to/",
		LangValue:        "uk-UA",
		TypeValue:        "semi-private",
		Caps:             tolokaCaps(),
		Config:           t.cfg,
		Client:           deps.Client,
		Saver:            deps.Saver,
		Cache:            deps.Cache,
	}
	return t, nil
}

func tolokaCaps() *models.Capabilities {
	caps := models.NewCapabilities()
	caps.TVSearchParams = []string{models.TVParamQ, models.TVParamSeason, models.TVParamEp}
	caps.MovieSearchParams = []string{models.MovieParamQ}
	caps.MusicSearchParams = []string{models.MusicParamQ}
	caps.BookSearchParams = []string{models.BookParamQ}
	c := caps.Categories

	// movies
	c.AddCategoryMapping("117", models.CatMovies, "Українське кіно")
	c.AddCategoryMapping("84", models.CatMovies, "|-Мультфільми і казки")
	c.AddCategoryMapping("42", models.CatMovies, "|-Художні фільми")
	c.AddCategoryMapping("124", models.CatTV, "|-Телесеріали")
	c.AddCategoryMapping("125", models.CatTV, "|-Мультсеріали")
	c.AddCategoryMapping("129", models.CatMovies, "|-АртХаус")
	c.AddCategoryMapping("219", models.CatMovies, "|-Аматорське відео")
	c.AddCategoryMapping("118", models.CatMovies, "Українське озвучення")
	c.AddCategoryMapping("16", models.CatMovies, "|-Фільми")
	c.AddCategoryMapping("32", models.CatTV, "|-Телесеріали")
	c.AddCategoryMapping("19", models.CatMovies, "|-Мультфільми")
	c.AddCategoryMapping("44", models.CatTV, "|-Мультсеріали")
	c.AddCategoryMapping("127", models.CatTVAnime, "|-Аніме")
	c.AddCategoryMapping("55", models.CatMovies, "|-АртХаус")
	c.AddCategoryMapping("94", models.CatMoviesOther, "|-Трейлери")
	c.AddCategoryMapping("144", models.CatMovies, "|-Короткометражні")
	c.AddCategoryMapping("190", models.CatMovies, "Українські субтитри")
	c.AddCategoryMapping("70", models.CatMovies, "|-Фільми")
	c.AddCategoryMapping("192", models.CatTV, "|-Телесеріали")
	c.AddCategoryMapping("193", models.CatMovies, "|-Мультфільми")
	c.AddCategoryMapping("195", models.CatTV, "|-Мультсеріали")
	c.AddCategoryMapping("194", models.CatTVAnime, "|-Аніме")
	c.AddCategoryMapping("196", models.CatMovies, "|-АртХаус")
	c.AddCategoryMapping("197", models.CatMovies, "|-Короткометражні")
	c.AddCategoryMapping("225", models.CatTVDocumentary, "Документальні фільми українською")
	c.AddCategoryMapping("21", models.CatTVDocumentary, "|-Українські наукові документальні фільми")
	c.AddCategoryMapping("131", models.CatTVDocumentary, "|-Українські історичні документальні фільми")
	c.AddCategoryMapping("226", models.CatTVDocumentary, "|-BBC")
	c.AddCategoryMapping("227", models.CatTVDocumentary, "|-Discovery")
	c.AddCategoryMapping("228", models.CatTVDocumentary, "|-National Geographic")
	c.AddCategoryMapping("229", models.CatTVDocumentary, "|-History Channel")
	c.AddCategoryMapping("230", models.CatTVDocumentary, "|-Інші іноземні документальні фільми")
	c.AddCategoryMapping("119", models.CatTVOther, "Телепередачі українською")
	c.AddCategoryMapping("18", models.CatTVOther, "|-Музичне відео")
	c.AddCategoryMapping("132", models.CatTVOther, "|-Телевізійні шоу та програми")
	c.AddCategoryMapping("157", models.CatTVSport, "Український спорт")
	c.AddCategoryMapping("235", models.CatTVSport, "|-Олімпіада")
	c.AddCategoryMapping("170", models.CatTVSport, "|-Чемпіонати Європи з футболу")
	c.AddCategoryMapping("162", models.CatTVSport, "|-Чемпіонати світу з футболу")
	c.AddCategoryMapping("166", models.CatTVSport, "|-Чемпіонат та Кубок України з футболу")
	c.AddCategoryMapping("167", models.CatTVSport, "|-Єврокубки")
	c.AddCategoryMapping("168", models.CatTVSport, "|-Збірна України")
	c.AddCategoryMapping("169", models.CatTVSport, "|-Закордонні чемпіонати")
	c.AddCategoryMapping("54", models.CatTVSport, "|-Футбольне відео")
	c.AddCategoryMapping("158", models.CatTVSport, "|-Баскетбол, хоккей, волейбол, гандбол, футзал")
	c.AddCategoryMapping("159", models.CatTVSport, "|-Бокс, реслінг, бойові мистецтва")
	c.AddCategoryMapping("160", models.CatTVSport, "|-Авто, мото")
	c.AddCategoryMapping("161", models.CatTVSport, "|-Інший спорт, активний відпочинок")
	c.AddCategoryMapping("136", models.CatMoviesHD, "HD українською")
	c.AddCategoryMapping("96", models.CatMoviesHD, "|-Фільми в HD")
	c.AddCategoryMapping("173", models.CatTVHD, "|-Серіали в HD")
	c.AddCategoryMapping("139", models.CatMoviesHD, "|-Мультфільми в HD")
	c.AddCategoryMapping("174", models.CatTVHD, "|-Мультсеріали в HD")
	c.AddCategoryMapping("140", models.CatTVDocumentary, "|-Документальні фільми в HD")
	c.AddCategoryMapping("120", models.CatMoviesDVD, "DVD українською")
	c.AddCategoryMapping("66", models.CatMoviesDVD, "|-Художні фільми та серіали в DVD")
	c.AddCategoryMapping("137", models.CatMoviesDVD, "|-Мультфільми та мультсеріали в DVD")
	c.AddCategoryMapping("137", models.CatTV, "|-Мультфільми та мультсеріали в DVD")
	c.AddCategoryMapping("138", models.CatMoviesDVD, "|-Документальні фільми в DVD")
	c.AddCategoryMapping("237", models.CatMovies, "Відео для мобільних (iOS, Android, Windows Phone)")
	c.AddCategoryMapping("33", models.CatAudioVideo, "Звукові доріжки та субтитри")
	// music
	c.AddCategoryMapping("8", models.CatAudio, "Українська музика (lossy)")
	c.AddCategoryMapping("23", models.CatAudio, "|-Поп, Естрада")
	c.AddCategoryMapping("24", models.CatAudio, "|-Джаз, Блюз")
	c.AddCategoryMapping("43", models.CatAudio, "|-Етно, Фольклор, Народна, Бардівська")
	c.AddCategoryMapping("35", models.CatAudio, "|-Інструментальна, Класична та неокласична")
	c.AddCategoryMapping("37", models.CatAudio, "|-Рок, Метал, Альтернатива, Панк, СКА")
	c.AddCategoryMapping("36", models.CatAudio, "|-Реп, Хіп-хоп, РнБ")
	c.AddCategoryMapping("38", models.CatAudio, "|-Електронна музика")
	c.AddCategoryMapping("56", models.CatAudio, "|-Невидане")
	c.AddCategoryMapping("98", models.CatAudioLossless, "Українська музика (lossless)")
	c.AddCategoryMapping("100", models.CatAudioLossless, "|-Поп, Естрада")
	c.AddCategoryMapping("101", models.CatAudioLossless, "|-Джаз, Блюз")
	c.AddCategoryMapping("102", models.CatAudioLossless, "|-Етно, Фольклор, Народна, Бардівська")
	c.AddCategoryMapping("103", models.CatAudioLossless, "|-Інструментальна, Класична та неокласична")
	c.AddCategoryMapping("104", models.CatAudioLossless, "|-Рок, Метал, Альтернатива, Панк, СКА")
	c.AddCategoryMapping("105", models.CatAudioLossless, "|-Реп, Хіп-хоп, РнБ")
	c.AddCategoryMapping("106", models.CatAudioLossless, "|-Електронна музика")
	// books
	c.AddCategoryMapping("11", models.CatBooks, "Друкована література")
	c.AddCategoryMapping("134", models.CatBooks, "|-Українська художня література (до 1991 р.)")
	c.AddCategoryMapping("177", models.CatBooks, "|-Українська художня література (після 1991 р.)")
	c.AddCategoryMapping("178", models.CatBooks, "|-Зарубіжна художня література")
	c.AddCategoryMapping("179", models.CatBooks, "|-Наукова література (гуманітарні дисципліни)")
	c.AddCategoryMapping("180", models.CatBooks, "|-Наукова література (природничі дисципліни)")
	c.AddCategoryMapping("183", models.CatBooks, "|-Навчальна та довідкова")
	c.AddCategoryMapping("181", models.CatBooksMags, "|-Періодика")
	c.AddCategoryMapping("182", models.CatBooks, "|-Батькам та малятам")
	c.AddCategoryMapping("184", models.CatBooksComics, "|-Графіка (комікси, манґа, BD та інше)")
	c.AddCategoryMapping("185", models.CatAudioAudiobook, "Аудіокниги українською")
	c.AddCategoryMapping("135", models.CatAudioAudiobook, "|-Українська художня література")
	c.AddCategoryMapping("186", models.CatAudioAudiobook, "|-Зарубіжна художня література")
	c.AddCategoryMapping("187", models.CatAudioAudiobook, "|-Історія, біографістика, спогади")
	c.AddCategoryMapping("189", models.CatAudioAudiobook, "|-Сирий матеріал")
	// software
	c.AddCategoryMapping("9", models.CatPC, "Windows")
	c.AddCategoryMapping("25", models.CatPC, "|-Windows")
	c.AddCategoryMapping("199", models.CatPC, "|-Офіс")
	c.AddCategoryMapping("200", models.CatPC, "|-Антивіруси та безпека")
	c.AddCategoryMapping("201", models.CatPC, "|-Мультимедія")
	c.AddCategoryMapping("202", models.CatPC, "|-Утиліти, обслуговування, мережа")
	c.AddCategoryMapping("239", models.CatPC, "Linux, Mac OS")
	c.AddCategoryMapping("26", models.CatPC, "|-Linux")
	c.AddCategoryMapping("27", models.CatPCMac, "|-Mac OS")
	c.AddCategoryMapping("240", models.CatPC, "Інші OS")
	c.AddCategoryMapping("211", models.CatPCMobileAndroid, "|-Android")
	c.AddCategoryMapping("122", models.CatPCMobileiOS, "|-iOS")
	c.AddCategoryMapping("40", models.CatPCMobileOther, "|-Інші мобільні платформи")
	c.AddCategoryMapping("241", models.CatOther, "Інше")
	c.AddCategoryMapping("203", models.CatOther, "|-Інфодиски, електронні підручники, відеоуроки")
	c.AddCategoryMapping("12", models.CatOther, "|-Шпалери, фотографії та зображення")
	c.AddCategoryMapping("249", models.CatOther, "|-Веб-скрипти")
	// games
	c.AddCategoryMapping("10", models.CatPCGames, "Ігри українською")
	c.AddCategoryMapping("28", models.CatPCGames, "|-PC ігри")
	c.AddCategoryMapping("259", models.CatPCGames, "|-Mac ігри")
	c.AddCategoryMapping("29", models.CatPCGames, "|-Українізації, доповнення, патчі...")
	c.AddCategoryMapping("30", models.CatPCGames, "|-Мобільні та консольні ігри")
	c.AddCategoryMapping("41", models.CatPCMobileiOS, "|-iOS")
	c.AddCategoryMapping("212", models.CatPCMobileAndroid, "|-Android")
	c.AddCategoryMapping("205", models.CatPCGames, "Переклад ігор українською")
	// archive and trash
	c.AddCategoryMapping("236", models.CatOther, "Закритий розділ")
	c.AddCategoryMapping("71", models.CatOther, "Архіви")
	c.AddCategoryMapping("72", models.CatOther, "Архів відео")
	c.AddCategoryMapping("73", models.CatOther, "Архів музики")
	c.AddCategoryMapping("74", models.CatOther, "Архів програм")
	c.AddCategoryMapping("75", models.CatOther, "Архів ігор")
	c.AddCategoryMapping("76", models.CatOther, "Архів літератури")
	c.AddCategoryMapping("121", models.CatOther, "Неоформлені")
	c.AddCategoryMapping("45", models.CatOther, "Неоформлене відео")
	c.AddCategoryMapping("46", models.CatOther, "Неоформлена музика")
	c.AddCategoryMapping("47", models.CatOther, "Неоформлене програмне забезпечення")
	c.AddCategoryMapping("48", models.CatOther, "Неоформлені ігри")
	c.AddCategoryMapping("208", models.CatOther, "Неоформлена література")
	return caps
}

// log is a nil-safe logger for the manual package (Base.Logger may be nil).
func (t *Toloka) log() *slog.Logger {
	if t.Logger != nil {
		return t.Logger
	}
	return slog.Default()
}

func (t *Toloka) loginURL() string  { return t.SiteLink() + "login.php" }
func (t *Toloka) searchURL() string { return t.SiteLink() + "tracker.php" }

// ApplyConfiguration ports upstream (cs:213-238): POST login.php with
// autologin/ssl and check the followed page for "logout=true". RequiresTesting
// (the manager runs the test afterwards).
func (t *Toloka) ApplyConfiguration(ctx context.Context, items []indexer.ConfigItem) (indexer.ConfigStatus, error) {
	if err := t.ApplyItems(items); err != nil {
		return indexer.ConfigCompleted, err
	}
	if err := t.LoadValuesFromJSON(nil, false); err != nil {
		return indexer.ConfigCompleted, err
	}
	if t.Client == nil {
		return indexer.ConfigCompleted, errors.New("http client not configured")
	}

	pairs := formPairs{
		{"username", t.cfg.Username.Value},
		{"password", t.cfg.Password.Value},
		{"autologin", "on"},
		{"ssl", "on"},
		{"redirect", ""},
		{"login", "Вхід"},
	}

	res, err := t.Client.Do(ctx, t.ID(), &httpclient.Request{
		Method:  http.MethodPost,
		URL:     t.loginURL(),
		RawBody: pairs.encode(),
		Headers: formContentType,
		Cookies: t.CookieHeader(),
		Referer: t.loginURL(),
	})
	if err != nil {
		return indexer.ConfigCompleted, err
	}

	if !strings.Contains(string(res.Body), "logout=true") {
		errorMessage := ""
		if doc, docErr := goquery.NewDocumentFromReader(strings.NewReader(string(res.Body))); docErr == nil {
			errorMessage = strings.TrimSpace(doc.Find("table.forumline table span.gen").First().Contents().First().Text())
		}
		if errorMessage == "" {
			errorMessage = "Unknown error message, please report."
		}
		return indexer.ConfigCompleted, &indexer.IndexerError{IndexerID: t.ID(), Kind: indexer.KindConfig, Message: errorMessage}
	}

	// upstream ConfigureIfOK stores the response cookies (through the
	// ResolveCookies filter, cs:240-248).
	cookies := setCookiePairs(res.Headers)
	if cookies == "" {
		if u, err := url.Parse(t.SiteLink()); err == nil {
			cookies = t.Client.CookieHeader(t.ID(), u)
		}
	}
	t.SetCookieHeader(tkResolveCookies(cookies))
	t.SetConfigured(true)
	t.SaveConfig(ctx)
	return indexer.ConfigRequiresTesting, nil
}

// tkResolveCookies ports upstream Toloka.ResolveCookies (cs:240-248): drop
// every cookie whose name starts with "toloka_" and ends with "_u". Tokens
// are accepted both "; "-joined and space-joined (the manual package's
// setCookiePairs joins with spaces).
func tkResolveCookies(incoming string) string {
	tokens := strings.FieldsFunc(incoming, func(r rune) bool { return r == ';' || r == ' ' })
	var kept []string
	for _, p := range tokens {
		name := p
		if eq := strings.Index(p, "="); eq >= 0 {
			name = p[:eq]
		}
		if strings.HasPrefix(name, "toloka_") && strings.HasSuffix(name, "_u") {
			continue
		}
		kept = append(kept, p)
	}
	return strings.Join(kept, "; ")
}

func (t *Toloka) ResultsForQuery(ctx context.Context, q *models.TorznabQuery, isMeta bool) (*indexer.IndexerResult, error) {
	return t.ResultsForQueryWith(ctx, q, isMeta, t.perform)
}

func (t *Toloka) Test(ctx context.Context) error {
	return t.Base.Test(ctx, t.perform)
}

// perform ports upstream PerformQuery (cs:250-372), including the re-login
// when the tracker page does not show "logout=true".
func (t *Toloka) perform(ctx context.Context, query *models.TorznabQuery) ([]*models.ReleaseInfo, error) {
	if t.Client == nil {
		return nil, errors.New("http client not configured")
	}

	searchString := query.SanitizedSearchTerm()

	// NameValueCollection doesn't support cat[]=19&cat[]=6 upstream, hence
	// the ordered list (cs:255-259).
	qc := shared.QueryParams{}
	qc.Add("o", "1")
	qc.Add("s", "2")

	if t.cfg.FreeleechOnly.Value {
		qc.Add("sds", "1")
	}

	// if the search string is empty use the getnew view (upstream comment;
	// the code adds nm either way, cs:267-270)
	if strings.TrimSpace(searchString) == "" {
		qc.Add("nm", searchString)
	} else {
		searchString = strings.ReplaceAll(searchString, "-", " ")

		if query.Season != nil && *query.Season > 0 {
			searchString += " Сезон " + strconv.Itoa(*query.Season)
		}

		qc.Add("nm", searchString)
	}

	for _, cat := range t.Caps.Categories.MapTorznabCapsToTrackers(query, false) {
		qc.Add("f[]", cat)
	}

	searchURL := t.searchURL() + "?" + qc.GetQueryString()
	res, err := t.Client.Do(ctx, t.ID(), &httpclient.Request{
		URL:     searchURL,
		Cookies: t.CookieHeader(),
	})
	if err != nil {
		return nil, err
	}

	if !strings.Contains(string(res.Body), "logout=true") {
		// re-login (upstream ApplyConfiguration(null), cs:293-294)
		if _, err := t.ApplyConfiguration(ctx, nil); err != nil {
			return nil, err
		}
		res, err = t.Client.Do(ctx, t.ID(), &httpclient.Request{
			URL:     searchURL,
			Cookies: t.CookieHeader(),
		})
		if err != nil {
			return nil, err
		}
	}

	return t.parseSearchPage(string(res.Body))
}

// parseSearchPage ports the upstream parsing block (cs:298-370).
func (t *Toloka) parseSearchPage(content string) ([]*models.ReleaseInfo, error) {
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(content))
	if err != nil {
		t.log().Error(fmt.Sprintf("Error while parsing result: %v\n%s", err, content))
		return nil, &indexer.IndexerError{IndexerID: t.ID(), Kind: indexer.KindParse, Message: "Parse error"}
	}

	var releases []*models.ReleaseInfo
	var parseErr error
	doc.Find(`table.forumline > tbody > tr[class*="prow"]`).EachWithBreak(func(_ int, row *goquery.Selection) bool {
		release, err := t.parseRow(row)
		if err != nil {
			// upstream: row-level failures are logged and the row skipped
			// (cs:360-364); the selector-level failure aborts everything.
			if errors.Is(err, errTkRowSkipped) {
				t.log().Error(fmt.Sprintf("%s: Error while parsing row '%s':\n\n%v", t.ID(), rowText(row), err))
				return true
			}
			parseErr = err
			return false
		}
		releases = append(releases, release)
		return true
	})
	if parseErr != nil {
		t.log().Error(fmt.Sprintf("Error while parsing result: %v\n%s", parseErr, content))
		return nil, &indexer.IndexerError{IndexerID: t.ID(), Kind: indexer.KindParse, Message: "Parse error"}
	}
	return releases, nil
}

// errTkRowSkipped marks per-row failures (upstream inner catch, cs:360-364).
var errTkRowSkipped = errors.New("toloka: row parse failure")

func rowText(s *goquery.Selection) string {
	html, _ := goquery.OuterHtml(s)
	return html
}

// parseRow ports one results-table row (cs:306-358).
func (t *Toloka) parseRow(row *goquery.Selection) (*models.ReleaseInfo, error) {
	qDownloadLink := row.Find("td:nth-child(6) > a")

	if qDownloadLink.Length() == 0 { // Expects moderation
		return nil, errTkRowSkipped
	}

	qDetailsLink := row.Find("td:nth-child(3) > a").First()
	detailsHref, ok := qDetailsLink.Attr("href")
	if !ok {
		return nil, errTkRowSkipped
	}
	details, err := url.Parse(t.SiteLink() + detailsHref)
	if err != nil {
		return nil, errTkRowSkipped
	}
	title := strings.TrimSpace(qDetailsLink.Text())
	downloadHref, ok := qDownloadLink.First().Attr("href")
	if !ok {
		return nil, errTkRowSkipped
	}
	link, err := url.Parse(t.SiteLink() + downloadHref)
	if err != nil {
		return nil, errTkRowSkipped
	}
	forumHref, ok := row.Find("td:nth-child(2) > a").First().Attr("href")
	if !ok {
		return nil, errTkRowSkipped
	}
	forumID := parseutil.GetArgumentFromQueryString(forumHref, "f")
	category := t.Caps.Categories.MapTrackerCatToNewznab(forumID)
	seedersStr := strings.TrimSpace(row.Find("td:nth-child(10) > b").Text())
	var seeders int64
	if seedersStr == "" {
		seeders = 0
	} else {
		seeders = parseutil.MustCoerceLong(seedersStr)
	}
	leechers := parseutil.MustCoerceLong(row.Find("td:nth-child(11) > b").Text())
	size := parseutil.GetBytes(row.Find("td:nth-child(7)").Text())
	publishDate, err := dateutil.FromFuzzyTime(strings.TrimSpace(row.Find("td:nth-child(13)").Text()), "")
	if err != nil {
		return nil, errTkRowSkipped
	}

	peers := leechers + seeders
	grabs := int64(0) // ParseUtil.CoerceLong(Row.QuerySelector("td:nth-child(9)").TextContent);
	minimumRatio := 1.0
	var minimumSeedTime int64
	downloadVolumeFactor := 1.0
	uploadVolumeFactor := 1.0

	release := &models.ReleaseInfo{
		Guid:                 details,
		Details:              details,
		Link:                 link,
		Title:                tkParseTitle(title, category, t.cfg.StripCyrillicLetters.Value),
		Description:          title,
		Category:             category,
		Size:                 &size,
		Seeders:              &seeders,
		Peers:                &peers,
		Grabs:                &grabs,
		PublishDate:          publishDate,
		DownloadVolumeFactor: &downloadVolumeFactor,
		UploadVolumeFactor:   &uploadVolumeFactor,
		MinimumRatio:         &minimumRatio,
		MinimumSeedTime:      &minimumSeedTime,
	}

	if row.Find(`img[src="images/gold.gif"], img[src="images/authors.gif"]`).Length() > 0 {
		release.DownloadVolumeFactor = tkF64(0)
	} else if row.Find(`img[src="images/silver.gif"]`).Length() > 0 {
		release.DownloadVolumeFactor = tkF64(0.5)
	} else if row.Find(`img[src="images/bronze.gif"]`).Length() > 0 {
		release.DownloadVolumeFactor = tkF64(0.75)
	}

	return release, nil
}

func tkF64(v float64) *float64 { return &v }

// --- TitleParser port (Toloka.cs nested class, cs:374-485) ------------------

var (
	// .NET balancing-group regexes (cs:378-379) emulated by tkFindBalancedTags.
	tkTvTitleCommaRegex           = regexp.MustCompile(`\s(\d+),(\d+)`)
	tkTvTitleCyrillicXRegex       = regexp.MustCompile(`(?i)([\s-])Х+([\)\]])`)
	tkTvTitleMultipleSeasonsRe    = regexp.MustCompile(`(?i)(?:Сезон|Seasons?)\s*[:]*\s+(\d+-\d+)`)
	tkTvTitleUkrSeasonEpisodeOfRe = regexp.MustCompile(`(?i)Сезон\s*[:]*\s+(\d+).+(?:Серії|Серія|Серій|Епізоди?)+\s*[:]*\s+(\d+(?:-\d+)?)\s*з\s*([\w?])`)
	tkTvTitleUkrSeasonEpisodeRe   = regexp.MustCompile(`(?i)Сезон\s*[:]*\s+(\d+).+(?:Серії|Серія|Серій|Епізоди?)+\s*[:]*\s+(\d+(?:-\d+)?)`)
	tkTvTitleUkrSeasonRe          = regexp.MustCompile(`(?i)Сезон\s*[:]*\s+(\d+)`)
	tkTvTitleUkrEpisodeOfRe       = regexp.MustCompile(`(?i)(?:Серії|Серія|Серій|Епізоди?)+\s*[:]*\s+(\d+(?:-\d+)?)\s*з\s*([\w?])`)
	tkTvTitleUkrEpisodeRe         = regexp.MustCompile(`(?i)(?:Серії|Серія|Серій|Епізоди?)+\s*[:]*\s+(\d+(?:-\d+)?)`)
	tkTvTitleEngSeasonEpisodeOfRe = regexp.MustCompile(`(?i)Season\s*[:]*\s+(\d+).+(?:Episodes?)+\s*[:]*\s+(\d+(?:-\d+)?)\s*of\s*([\w?])`)
	tkTvTitleEngSeasonEpisodeRe   = regexp.MustCompile(`(?i)Season\s*[:]*\s+(\d+).+(?:Episodes?)+\s*[:]*\s+(\d+(?:-\d+)?)`)
	tkTvTitleEngSeasonRe          = regexp.MustCompile(`(?i)Season\s*[:]*\s+(\d+(?:-\d+)?)`)
	tkTvTitleEngEpisodeOfRe       = regexp.MustCompile(`(?i)(?:Episodes?)+\s*[:]*\s+(\d+(?:-\d+)?)\s*of\s*([\w?])`)
	tkTvTitleEngEpisodeRe         = regexp.MustCompile(`(?i)(?:Episodes?)+\s*[:]+\s*[:]*\s+(\d+(?:-\d+)?)`)
	tkStripCyrillicRe             = regexp.MustCompile(`(?i)(\([\p{Cyrillic}\W]+\))|(^[\p{Cyrillic}\W\d]+\/ )|([\p{Cyrillic} \-]+,+)|([\p{Cyrillic}]+)`)
	tkDashPunctRe                 = regexp.MustCompile(`\p{Pd}`)
	tkWordRipRe                   = regexp.MustCompile(`(?i)\b-Rip\b`)
	tkWordHdtvRipRe               = regexp.MustCompile(`(?i)\bHDTVRip\b`)
	tkWordWebdlRipRe              = regexp.MustCompile(`(?i)\bWEB-DLRip\b`)
	tkWordWebdlRip2Re             = regexp.MustCompile(`(?i)\bWEBDLRip\b`)
	tkWordWebdlRe                 = regexp.MustCompile(`(?i)\bWEBDL\b`)
	tkParenSlashOpenRe            = regexp.MustCompile(`\(\s*\/\s*`)
	tkParenSlashCloseRe           = regexp.MustCompile(`\s*\/\s*\)`)
	tkEmptyBracketsRe             = regexp.MustCompile(`[\[\(]\s*[\)\]]`)
	tkMultiSpaceRe                = regexp.MustCompile(`\s+`)
)

// tkTitleCutset mirrors the upstream Trim cutset (cs:445): ' ', '&', ',',
// '.', '!', '?', '+', '-', '_', '|', '/', '\\', ':', ';', 'ʼ', '`'.
const tkTitleCutset = " &,.!?+-_|/\\:;ʼ`"

// tkParseTitle ports Toloka.TitleParser.Parse (cs:401-451).
func tkParseTitle(title string, category []int, stripCyrillicLetters bool) string {
	// https://www.fileformat.info/info/unicode/category/Pd/list.htm
	title = tkDashPunctRe.ReplaceAllString(title, "-")

	if tkIsAnyTvCategory(category) {
		title = tkTvTitleCommaRegex.ReplaceAllString(title, " $1-$2")
		title = tkTvTitleCyrillicXRegex.ReplaceAllString(title, "${1}XX${2}")

		// special case for multiple seasons
		title = tkTvTitleMultipleSeasonsRe.ReplaceAllString(title, "S$1")

		title = tkTvTitleUkrSeasonEpisodeOfRe.ReplaceAllString(title, "S${1}E${2} of $3")
		title = tkTvTitleUkrSeasonEpisodeRe.ReplaceAllString(title, "S${1}E$2")
		title = tkTvTitleUkrSeasonRe.ReplaceAllString(title, "S$1")
		title = tkTvTitleUkrEpisodeOfRe.ReplaceAllString(title, "E${1} of $2")
		title = tkTvTitleUkrEpisodeRe.ReplaceAllString(title, "E$1")

		title = tkTvTitleEngSeasonEpisodeOfRe.ReplaceAllString(title, "S${1}E${2} of $3")
		title = tkTvTitleEngSeasonEpisodeRe.ReplaceAllString(title, "S${1}E$2")
		title = tkTvTitleEngSeasonRe.ReplaceAllString(title, "S$1")
		title = tkTvTitleEngEpisodeOfRe.ReplaceAllString(title, "E${1} of $2")
		title = tkTvTitleEngEpisodeRe.ReplaceAllString(title, "E$1")
	}

	if stripCyrillicLetters {
		title = strings.Trim(tkStripCyrillicRe.ReplaceAllString(title, ""), " -")
	}

	title = tkWordRipRe.ReplaceAllString(title, "Rip")
	title = tkWordHdtvRipRe.ReplaceAllString(title, "HDTV")
	title = tkWordWebdlRipRe.ReplaceAllString(title, "WEB-DL")
	title = tkWordWebdlRip2Re.ReplaceAllString(title, "WEB-DL")
	title = tkWordWebdlRe.ReplaceAllString(title, "WEB-DL")

	title = tkMoveFirstTagsToEndOfReleaseTitle(title)

	title = tkParenSlashOpenRe.ReplaceAllString(title, "(")
	title = tkParenSlashCloseRe.ReplaceAllString(title, ")")

	title = tkEmptyBracketsRe.ReplaceAllString(title, "")

	title = strings.Trim(title, tkTitleCutset)

	// replace multiple spaces with a single space
	title = tkMultiSpaceRe.ReplaceAllString(title, " ")

	return strings.TrimSpace(title)
}

// tkIsAnyTvCategory ports upstream (cs:453).
func tkIsAnyTvCategory(category []int) bool {
	ids := map[int]bool{models.CatTV.ID: true}
	for _, sub := range models.CatTV.SubCategories {
		ids[sub.ID] = true
	}
	for _, c := range category {
		if ids[c] {
			return true
		}
	}
	return false
}

// tkTagMatch is one balanced tag in the pre-mutation snapshot (the tag text
// is captured like .NET match.ToString(), cs:476).
type tkTagMatch struct {
	start, end int
	tag        string
}

// tkMoveFirstTagsToEndOfReleaseTitle ports upstream (cs:455-484). The .NET
// balancing-group tag regexes are emulated by tkFindBalancedTags (see file
// header).
func tkMoveFirstTagsToEndOfReleaseTitle(input string) string {
	output := strings.Trim(input, tkTitleCutset)
	for _, findTags := range []func(string) []tkTagMatch{tkParenTags, tkBracketTags} {
		expectedIndex := 0
		for _, m := range findTags(output) {
			if m.start > expectedIndex {
				substring := output[expectedIndex:m.start]
				if strings.TrimSpace(substring) == "" {
					expectedIndex = m.start
				} else {
					break
				}
			}

			tag := m.tag
			// upstream: regex.Escape(tag) Replace(output, "", 1) — the first
			// occurrence of the tag is removed and the tag appended.
			if idx := strings.Index(output, tag); idx >= 0 {
				output = output[:idx] + output[idx+len(tag):]
			}
			output = strings.TrimSpace(output + " " + tag)
			expectedIndex += len(tag)
		}
	}

	return strings.TrimSpace(output)
}

// tkParenTags / tkBracketTags emulate the two _FindTagsInTitlesRegexList
// entries: non-overlapping balanced (...) / [...] groups.
func tkParenTags(s string) []tkTagMatch   { return tkFindBalancedTags(s, '(', ')') }
func tkBracketTags(s string) []tkTagMatch { return tkFindBalancedTags(s, '[', ']') }

func tkFindBalancedTags(s string, open, close byte) []tkTagMatch {
	var out []tkTagMatch
	var stack []int
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case open:
			stack = append(stack, i)
		case close:
			if len(stack) == 0 {
				continue
			}
			start := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			if len(stack) == 0 {
				out = append(out, tkTagMatch{start: start, end: i + 1, tag: s[start : i+1]})
			}
		}
	}
	return out
}
