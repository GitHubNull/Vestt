// Package manual holds the hand-written indexers (one file per site).
package manual

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"log/slog"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"vestt/common/httpclient"
	"vestt/common/indexer"
	"vestt/common/indexer/configdata"
	"vestt/common/indexer/shared"
	"vestt/common/models"
	"vestt/common/services"
	"vestt/common/utils/parseutil"
)

// MyAnonamouse ports Jackett.Common/Indexers/Definitions/MyAnonamouse.cs.
//
// mechanism: json-api (mam_id cookie session)
//
// 澄清记录:
//   - upstream _SanitizeSearchQueryRegex uses the .NET regex class \w, which
//     is Unicode-aware; the Go port uses [^\p{L}\p{N}_]+ to match that
//     behaviour (Go's \w is ASCII-only).
//   - upstream parses the response inside try/catch and only logs via
//     OnParseError, returning the releases accumulated so far; the Go port
//     logs and returns the partial set as well.
//   - upstream throws a plain Exception when the response body starts with
//     "Error" (a dead mam_id session). Per SPEC-09 §4 this maps to
//     IndexerError{Kind: Auth} with the body preserved as the message.
//   - upstream webclient.EmulateBrowser = false is a per-indexer client
//     setting; the Go port passes EmulateBrowser=false on the request.
//
// Ported from: Definitions/MyAnonamouse.cs,
// Models/IndexerConfig/Bespoke/ConfigurationDataMyAnonamouse.cs.
type MyAnonamouse struct {
	indexer.Base
	cfg *myAnonamouseConfig
}

type myAnonamouseConfig struct {
	configdata.ConfigData
	MamId               *configdata.StringItem
	MamIdHint           *configdata.InfoItem
	SearchType          *configdata.SelectItem
	SearchInDescription *configdata.BoolItem
	SearchInSeries      *configdata.BoolItem
	SearchInFilenames   *configdata.BoolItem
	SearchLanguages     *configdata.MultiSelectItem
	AccountActivity     *configdata.InfoItem
}

const myAnonamouseMamIdHint = "Go to your <a href=\"https://www.myanonamouse.net/preferences/index.php?view=security\" target=\"_blank\">security preferences</a> and create a new session for the IP used by the Jackett server. Then paste the resulting mam_id value into the mam_id field here."

const myAnonamouseAccountActivity = "To prevent your account from being disabled for inactivity, you must log in on a regular basis. You must also use your account - if you do not, your account will be disabled. If you know that you will not be able to login for an extended period of time, you can park your account in your preferences and it will not be disabled."

func newMyAnonamouseConfig() *myAnonamouseConfig {
	return &myAnonamouseConfig{
		ConfigData:          *configdata.NewConfigData(),
		MamId:               configdata.NewStringItem("mam_id"),
		MamIdHint:           configdata.NewInfoItem("mam_id instructions", myAnonamouseMamIdHint),
		SearchType:          configdata.NewSelectItem("Search Type", myAnonamouseSearchTypeOptions()),
		SearchInDescription: configdata.NewBoolItem("Also search text in the description"),
		SearchInSeries:      configdata.NewBoolItem("Also search text in the series"),
		SearchInFilenames:   configdata.NewBoolItem("Also search text in the filenames"),
		SearchLanguages:     configdata.NewMultiSelectItem("Search Languages", myAnonamouseLanguageOptions()),
		AccountActivity:     configdata.NewInfoItem("Account Inactivity", myAnonamouseAccountActivity),
	}
}

// NewMyAnonamouse builds the indexer (services.Factory shape).
func NewMyAnonamouse(deps services.Deps) (indexer.Indexer, error) {
	m := &MyAnonamouse{cfg: newMyAnonamouseConfig()}
	m.cfg.SearchType.Value = "all" // upstream { Value = "all" }
	m.Base = indexer.Base{
		IDValue:          "myanonamouse",
		NameValue:        "MyAnonamouse",
		DescriptionValue: "MyAnonamouse is a Private site. Friendliness, Warmth and Sharing",
		SiteLinkValue:    "https://www.myanonamouse.net/",
		DefaultSiteLink:  "https://www.myanonamouse.net/",
		LangValue:        "en-US",
		TypeValue:        "private",
		Caps:             myAnonamouseCaps(),
		Config:           m.cfg,
		Client:           deps.Client,
		Saver:            deps.Saver,
		Cache:            deps.Cache,
	}
	return m, nil
}

func myAnonamouseCaps() *models.Capabilities {
	caps := models.NewCapabilities()
	caps.BookSearchParams = []string{models.BookParamQ}
	c := caps.Categories
	c.AddCategoryMapping("13", models.CatAudioAudiobook, "AudioBooks")
	c.AddCategoryMapping("14", models.CatBooksEBook, "E-Books")
	c.AddCategoryMapping("15", models.CatAudioAudiobook, "Musicology")
	c.AddCategoryMapping("16", models.CatAudioAudiobook, "Radio")
	c.AddCategoryMapping("39", models.CatAudioAudiobook, "Audiobooks - Action/Adventure")
	c.AddCategoryMapping("49", models.CatAudioAudiobook, "Audiobooks - Art")
	c.AddCategoryMapping("50", models.CatAudioAudiobook, "Audiobooks - Biographical")
	c.AddCategoryMapping("83", models.CatAudioAudiobook, "Audiobooks - Business")
	c.AddCategoryMapping("51", models.CatAudioAudiobook, "Audiobooks - Computer/Internet")
	c.AddCategoryMapping("97", models.CatAudioAudiobook, "Audiobooks - Crafts")
	c.AddCategoryMapping("40", models.CatAudioAudiobook, "Audiobooks - Crime/Thriller")
	c.AddCategoryMapping("41", models.CatAudioAudiobook, "Audiobooks - Fantasy")
	c.AddCategoryMapping("106", models.CatAudioAudiobook, "Audiobooks - Food")
	c.AddCategoryMapping("42", models.CatAudioAudiobook, "Audiobooks - General Fiction")
	c.AddCategoryMapping("52", models.CatAudioAudiobook, "Audiobooks - General Non-Fic")
	c.AddCategoryMapping("98", models.CatAudioAudiobook, "Audiobooks - Historical Fiction")
	c.AddCategoryMapping("54", models.CatAudioAudiobook, "Audiobooks - History")
	c.AddCategoryMapping("55", models.CatAudioAudiobook, "Audiobooks - Home/Garden")
	c.AddCategoryMapping("43", models.CatAudioAudiobook, "Audiobooks - Horror")
	c.AddCategoryMapping("99", models.CatAudioAudiobook, "Audiobooks - Humor")
	c.AddCategoryMapping("84", models.CatAudioAudiobook, "Audiobooks - Instructional")
	c.AddCategoryMapping("44", models.CatAudioAudiobook, "Audiobooks - Juvenile")
	c.AddCategoryMapping("56", models.CatAudioAudiobook, "Audiobooks - Language")
	c.AddCategoryMapping("45", models.CatAudioAudiobook, "Audiobooks - Literary Classics")
	c.AddCategoryMapping("57", models.CatAudioAudiobook, "Audiobooks - Math/Science/Tech")
	c.AddCategoryMapping("85", models.CatAudioAudiobook, "Audiobooks - Medical")
	c.AddCategoryMapping("87", models.CatAudioAudiobook, "Audiobooks - Mystery")
	c.AddCategoryMapping("119", models.CatAudioAudiobook, "Audiobooks - Nature")
	c.AddCategoryMapping("88", models.CatAudioAudiobook, "Audiobooks - Philosophy")
	c.AddCategoryMapping("58", models.CatAudioAudiobook, "Audiobooks - Pol/Soc/Relig")
	c.AddCategoryMapping("59", models.CatAudioAudiobook, "Audiobooks - Recreation")
	c.AddCategoryMapping("46", models.CatAudioAudiobook, "Audiobooks - Romance")
	c.AddCategoryMapping("47", models.CatAudioAudiobook, "Audiobooks - Science Fiction")
	c.AddCategoryMapping("53", models.CatAudioAudiobook, "Audiobooks - Self-Help")
	c.AddCategoryMapping("89", models.CatAudioAudiobook, "Audiobooks - Travel/Adventure")
	c.AddCategoryMapping("100", models.CatAudioAudiobook, "Audiobooks - True Crime")
	c.AddCategoryMapping("108", models.CatAudioAudiobook, "Audiobooks - Urban Fantasy")
	c.AddCategoryMapping("48", models.CatAudioAudiobook, "Audiobooks - Western")
	c.AddCategoryMapping("111", models.CatAudioAudiobook, "Audiobooks - Young Adult")
	c.AddCategoryMapping("60", models.CatBooksEBook, "Ebooks - Action/Adventure")
	c.AddCategoryMapping("71", models.CatBooksEBook, "Ebooks - Art")
	c.AddCategoryMapping("72", models.CatBooksEBook, "Ebooks - Biographical")
	c.AddCategoryMapping("90", models.CatBooksEBook, "Ebooks - Business")
	c.AddCategoryMapping("61", models.CatBooksComics, "Ebooks - Comics/Graphic novels")
	c.AddCategoryMapping("73", models.CatBooksEBook, "Ebooks - Computer/Internet")
	c.AddCategoryMapping("101", models.CatBooksEBook, "Ebooks - Crafts")
	c.AddCategoryMapping("62", models.CatBooksEBook, "Ebooks - Crime/Thriller")
	c.AddCategoryMapping("63", models.CatBooksEBook, "Ebooks - Fantasy")
	c.AddCategoryMapping("107", models.CatBooksEBook, "Ebooks - Food")
	c.AddCategoryMapping("64", models.CatBooksEBook, "Ebooks - General Fiction")
	c.AddCategoryMapping("74", models.CatBooksEBook, "Ebooks - General Non-Fiction")
	c.AddCategoryMapping("102", models.CatBooksEBook, "Ebooks - Historical Fiction")
	c.AddCategoryMapping("76", models.CatBooksEBook, "Ebooks - History")
	c.AddCategoryMapping("77", models.CatBooksEBook, "Ebooks - Home/Garden")
	c.AddCategoryMapping("65", models.CatBooksEBook, "Ebooks - Horror")
	c.AddCategoryMapping("103", models.CatBooksEBook, "Ebooks - Humor")
	c.AddCategoryMapping("115", models.CatBooksEBook, "Ebooks - Illusion/Magic")
	c.AddCategoryMapping("91", models.CatBooksEBook, "Ebooks - Instructional")
	c.AddCategoryMapping("66", models.CatBooksEBook, "Ebooks - Juvenile")
	c.AddCategoryMapping("78", models.CatBooksEBook, "Ebooks - Language")
	c.AddCategoryMapping("67", models.CatBooksEBook, "Ebooks - Literary Classics")
	c.AddCategoryMapping("79", models.CatBooksMags, "Ebooks - Magazines/Newspapers")
	c.AddCategoryMapping("80", models.CatBooksTechnical, "Ebooks - Math/Science/Tech")
	c.AddCategoryMapping("92", models.CatBooksEBook, "Ebooks - Medical")
	c.AddCategoryMapping("118", models.CatBooksEBook, "Ebooks - Mixed Collections")
	c.AddCategoryMapping("94", models.CatBooksEBook, "Ebooks - Mystery")
	c.AddCategoryMapping("120", models.CatBooksEBook, "Ebooks - Nature")
	c.AddCategoryMapping("95", models.CatBooksEBook, "Ebooks - Philosophy")
	c.AddCategoryMapping("81", models.CatBooksEBook, "Ebooks - Pol/Soc/Relig")
	c.AddCategoryMapping("82", models.CatBooksEBook, "Ebooks - Recreation")
	c.AddCategoryMapping("68", models.CatBooksEBook, "Ebooks - Romance")
	c.AddCategoryMapping("69", models.CatBooksEBook, "Ebooks - Science Fiction")
	c.AddCategoryMapping("75", models.CatBooksEBook, "Ebooks - Self-Help")
	c.AddCategoryMapping("96", models.CatBooksEBook, "Ebooks - Travel/Adventure")
	c.AddCategoryMapping("104", models.CatBooksEBook, "Ebooks - True Crime")
	c.AddCategoryMapping("109", models.CatBooksEBook, "Ebooks - Urban Fantasy")
	c.AddCategoryMapping("70", models.CatBooksEBook, "Ebooks - Western")
	c.AddCategoryMapping("112", models.CatBooksEBook, "Ebooks - Young Adult")
	c.AddCategoryMapping("19", models.CatAudioAudiobook, "Guitar/Bass Tabs")
	c.AddCategoryMapping("20", models.CatAudioAudiobook, "Individual Sheet")
	c.AddCategoryMapping("24", models.CatAudioAudiobook, "Individual Sheet MP3")
	c.AddCategoryMapping("126", models.CatAudioAudiobook, "Instructional Book with Video")
	c.AddCategoryMapping("22", models.CatAudioAudiobook, "Instructional Media - Music")
	c.AddCategoryMapping("113", models.CatAudioAudiobook, "Lick Library - LTP/Jam With")
	c.AddCategoryMapping("114", models.CatAudioAudiobook, "Lick Library - Techniques/QL")
	c.AddCategoryMapping("17", models.CatAudioAudiobook, "Music - Complete Editions")
	c.AddCategoryMapping("26", models.CatAudioAudiobook, "Music Book")
	c.AddCategoryMapping("27", models.CatAudioAudiobook, "Music Book MP3")
	c.AddCategoryMapping("30", models.CatAudioAudiobook, "Sheet Collection")
	c.AddCategoryMapping("31", models.CatAudioAudiobook, "Sheet Collection MP3")
	c.AddCategoryMapping("127", models.CatAudioAudiobook, "Radio -  Comedy")
	c.AddCategoryMapping("130", models.CatAudioAudiobook, "Radio - Drama")
	c.AddCategoryMapping("128", models.CatAudioAudiobook, "Radio - Factual/Documentary")
	c.AddCategoryMapping("132", models.CatAudioAudiobook, "Radio - Reading")
	return caps
}

// ApplyConfiguration ports upstream: load values, set the mam_id cookie,
// test-query the JSON endpoint and wrap every failure in
// "Your mam_id did not work: …".
func (m *MyAnonamouse) ApplyConfiguration(ctx context.Context, items []indexer.ConfigItem) (indexer.ConfigStatus, error) {
	if err := m.ApplyItems(items); err != nil {
		return indexer.ConfigCompleted, err
	}
	if err := m.LoadValuesFromJSON(nil, false); err != nil {
		return indexer.ConfigCompleted, err
	}

	m.SetCookieHeader("mam_id=" + m.cfg.MamId.Value)
	results, err := m.perform(ctx, models.NewTorznabQuery())
	if err != nil {
		m.SetConfigured(false)
		return indexer.ConfigCompleted, fmt.Errorf("Your mam_id did not work: %s", err.Error()) //nolint:staticcheck // upstream MyAnonamouse.cs text (port fixes upstream "man_id" typo), kept 1:1
	}
	if len(results) == 0 {
		m.SetConfigured(false)
		return indexer.ConfigCompleted, errors.New("Your mam_id did not work") //nolint:staticcheck // upstream MyAnonamouse.cs text (port fixes upstream "man_id" typo), kept 1:1
	}

	m.SetConfigured(true)
	m.SaveConfig(ctx)
	return indexer.ConfigCompleted, nil
}

func (m *MyAnonamouse) ResultsForQuery(ctx context.Context, q *models.TorznabQuery, isMeta bool) (*indexer.IndexerResult, error) {
	return m.ResultsForQueryWith(ctx, q, isMeta, m.perform)
}

func (m *MyAnonamouse) Test(ctx context.Context) error {
	return m.Base.Test(ctx, m.perform)
}

func (m *MyAnonamouse) searchURL() string { return m.SiteLink() + "tor/js/loadSearchJSONbasic.php" }

// mamSanitizeRe mirrors upstream "[^\\w]+" (IgnoreCase); .NET \w is
// Unicode-aware, hence the \p{L}\p{N} classes (see file-header 澄清记录).
var mamSanitizeRe = regexp.MustCompile(`[^\p{L}\p{N}_]+`)

func (m *MyAnonamouse) perform(ctx context.Context, query *models.TorznabQuery) ([]*models.ReleaseInfo, error) {
	var releases []*models.ReleaseInfo

	term := strings.TrimSpace(mamSanitizeRe.ReplaceAllString(query.GetQueryString(), " "))

	if strings.TrimSpace(query.SearchTerm) != "" && strings.TrimSpace(term) == "" {
		m.log().Debug(fmt.Sprintf("Search term is empty after being sanitized, stopping search. Initial search term: '%s'", query.SearchTerm))
		return releases, nil
	}

	limit := query.Limit
	if limit <= 0 {
		limit = 100
	}
	offset := query.Offset
	if offset < 0 {
		offset = 0
	}

	parameters := shared.QueryParams{}
	parameters.Add("tor[text]", term)
	parameters.Add("tor[searchType]", m.cfg.SearchType.Value)
	parameters.Add("tor[srchIn][title]", "true")
	parameters.Add("tor[srchIn][author]", "true")
	parameters.Add("tor[srchIn][narrator]", "true")
	parameters.Add("tor[searchIn]", "torrents")
	parameters.Add("tor[sortType]", "default")
	parameters.Add("tor[startNumber]", strconv.Itoa(offset))
	parameters.Add("perpage", strconv.Itoa(limit))
	parameters.Add("thumbnails", "1")  // gives links for thumbnail sized versions of their posters
	parameters.Add("description", "1") // include the description

	if m.cfg.SearchInDescription.Value {
		parameters.Add("tor[srchIn][description]", "true")
	}
	if m.cfg.SearchInSeries.Value {
		parameters.Add("tor[srchIn][series]", "true")
	}
	if m.cfg.SearchInFilenames.Value {
		parameters.Add("tor[srchIn][filenames]", "true")
	}

	if len(m.cfg.SearchLanguages.Values) > 0 {
		for index, language := range m.cfg.SearchLanguages.Values {
			if language == "" {
				continue
			}
			parameters.Set(fmt.Sprintf("tor[browse_lang][%d]", index), language)
		}
	}

	catList := mamDistinct(m.Caps.Categories.MapTorznabCapsToTrackers(query, false))

	if len(catList) > 0 {
		for index, category := range catList {
			parameters.Set(fmt.Sprintf("tor[cat][%d]", index), category)
		}
	} else {
		parameters.Add("tor[cat][]", "0")
	}

	urlSearch := m.searchURL()
	if len(parameters) > 0 {
		urlSearch += "?" + parameters.GetQueryString()
	}

	if m.Client == nil {
		return nil, errors.New("http client not configured")
	}
	res, err := m.Client.Do(ctx, m.ID(), &httpclient.Request{
		URL:            urlSearch,
		Headers:        map[string]string{"Accept": "application/json"},
		Cookies:        m.CookieHeader(),
		EmulateBrowser: httpclient.Bool(false), // upstream webclient.EmulateBrowser = false
	})
	if err != nil {
		return nil, err
	}

	if strings.HasPrefix(string(res.Body), "Error") {
		return nil, &indexer.IndexerError{IndexerID: m.ID(), Kind: indexer.KindAuth, Message: string(res.Body)}
	}

	siteLink, err := url.Parse(m.SiteLink())
	if err != nil {
		return nil, err
	}

	var jsonResponse myAnonamouseResponse
	if err := json.Unmarshal(res.Body, &jsonResponse); err != nil {
		// upstream OnParseError: log only, return the releases so far
		m.log().Error(fmt.Sprintf("Error while parsing result: %v\n%s", err, string(res.Body)))
		return releases, nil
	}

	if strings.TrimSpace(jsonResponse.Error) != "" && strings.HasPrefix(strings.ToLower(jsonResponse.Error), "nothing returned, out of") {
		return releases, nil
	}

	if jsonResponse.Data == nil {
		message := jsonResponse.Message
		if strings.TrimSpace(message) == "" {
			message = "Check the logs for more information."
		}
		return nil, fmt.Errorf("Unexpected response content from indexer request: %s", message) //nolint:staticcheck // upstream MyAnonamouse.cs error text, kept 1:1
	}

	for _, item := range jsonResponse.Data {
		release, err := m.mapTorrent(item, siteLink)
		if err != nil {
			// upstream try/catch: OnParseError logs and the partial list
			// is returned.
			m.log().Error(fmt.Sprintf("Error while parsing result: %v\n%s", err, string(res.Body)))
			return releases, nil
		}
		releases = append(releases, release)
	}

	return releases, nil
}

func (m *MyAnonamouse) mapTorrent(item *myAnonamouseTorrent, siteLink *url.URL) (*models.ReleaseInfo, error) {
	details := siteLink.ResolveReference(&url.URL{Path: "/t/" + strconv.Itoa(item.Id)})

	isFreeLeech := item.Free || item.PersonalFreeLeech

	// upstream: ParseExact("yyyy-MM-dd HH:mm:ss", AssumeUniversal).ToLocalTime()
	publishDate, err := time.ParseInLocation("2006-01-02 15:04:05", item.Added, time.UTC)
	if err != nil {
		return nil, err
	}
	publishDate = publishDate.Local()

	size := parseutil.GetBytes(item.Size)
	grabs := int64(item.Grabs)
	files := int64(item.NumFiles)
	seeders := int64(item.Seeders)
	peers := int64(item.Seeders + item.Leechers)
	downloadVolumeFactor := 1.0
	if isFreeLeech {
		downloadVolumeFactor = 0
	}
	uploadVolumeFactor := 1.0
	minimumSeedTime := int64(259200) // 72 hours

	downloadURL, err := url.Parse(m.SiteLink() + "tor/download.php?tid=" + strconv.Itoa(item.Id))
	if err != nil {
		return nil, err
	}

	release := &models.ReleaseInfo{
		Guid:                 details,
		Title:                strings.TrimSpace(item.Title),
		Description:          strings.TrimSpace(item.Description),
		Link:                 downloadURL,
		Details:              details,
		Category:             m.Caps.Categories.MapTrackerCatToNewznab(item.Category),
		PublishDate:          publishDate,
		Grabs:                &grabs,
		Files:                &files,
		Seeders:              &seeders,
		Peers:                &peers,
		Size:                 &size,
		DownloadVolumeFactor: &downloadVolumeFactor,
		UploadVolumeFactor:   &uploadVolumeFactor,
		// MinimumRatio = 1: global MR is 1.0 but torrents must be seeded for 3 days regardless of ratio
		MinimumSeedTime: &minimumSeedTime,
	}

	if strings.TrimSpace(item.AuthorInfo) != "" {
		authors, err := mamDecodeAuthors(item.AuthorInfo)
		if err != nil {
			// the JSON on author_info field can be malformed due to double quotes
			m.log().Warn(fmt.Sprintf("%s error parsing author_info: %s", m.Name(), item.AuthorInfo))
		} else if len(authors) > 0 {
			release.Title += " by " + strings.Join(authors, ", ")
		}
	}

	var flags []string

	if item.LanguageCode != "" {
		flags = append(flags, item.LanguageCode)
	}
	if item.Filetype != "" {
		flags = append(flags, strings.ToUpper(item.Filetype))
	}

	if len(flags) > 0 {
		release.Title += " [" + strings.Join(flags, " / ") + "]"
	}

	if item.Vip {
		release.Title += " [VIP]"
	}

	return release, nil
}

// mamDecodeAuthors mirrors upstream: author_info is a JSON object of
// id → name; the first 5 values (in document order) are HTML-decoded.
func mamDecodeAuthors(authorInfo string) ([]string, error) {
	dec := json.NewDecoder(strings.NewReader(authorInfo))
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	if d, ok := tok.(json.Delim); !ok || d != '{' {
		return nil, errors.New("author_info is not a JSON object")
	}
	var authors []string
	for dec.More() {
		keyTok, err := dec.Token()
		if err != nil {
			return nil, err
		}
		if _, ok := keyTok.(string); !ok {
			return nil, errors.New("author_info key is not a string")
		}
		var value string
		if err := dec.Decode(&value); err != nil {
			return nil, err
		}
		if len(authors) < 5 {
			authors = append(authors, html.UnescapeString(value))
		}
	}
	return authors, nil
}

// log is a nil-safe logger for the manual package (Base.Logger may be nil).
func (m *MyAnonamouse) log() *slog.Logger {
	if m.Logger != nil {
		return m.Logger
	}
	return slog.Default()
}

// mamDistinct preserves order while dropping duplicates (upstream .Distinct()).
func mamDistinct(list []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(list))
	for _, s := range list {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

// --- upstream config option tables ------------------------------------------

func myAnonamouseSearchTypeOptions() []configdata.Option {
	return []configdata.Option{
		{Value: "all", Label: "All torrents"},
		{Value: "active", Label: "Only active"},
		{Value: "fl", Label: "Freeleech"},
		{Value: "fl-VIP", Label: "Freeleech or VIP"},
		{Value: "VIP", Label: "VIP torrents"},
		{Value: "nVIP", Label: "Torrents not VIP"},
	}
}

func myAnonamouseLanguageOptions() []configdata.Option {
	return []configdata.Option{
		{Value: "1", Label: "English"},
		{Value: "17", Label: "Afrikaans"},
		{Value: "32", Label: "Arabic"},
		{Value: "35", Label: "Bengali"},
		{Value: "51", Label: "Bosnian"},
		{Value: "18", Label: "Bulgarian"},
		{Value: "6", Label: "Burmese"},
		{Value: "44", Label: "Cantonese"},
		{Value: "19", Label: "Catalan"},
		{Value: "2", Label: "Chinese"},
		{Value: "49", Label: "Croatian"},
		{Value: "20", Label: "Czech"},
		{Value: "21", Label: "Danish"},
		{Value: "22", Label: "Dutch"},
		{Value: "61", Label: "Estonian"},
		{Value: "39", Label: "Farsi"},
		{Value: "23", Label: "Finnish"},
		{Value: "36", Label: "French"},
		{Value: "37", Label: "German"},
		{Value: "26", Label: "Greek"},
		{Value: "59", Label: "Greek, Ancient"},
		{Value: "3", Label: "Gujarati"},
		{Value: "27", Label: "Hebrew"},
		{Value: "8", Label: "Hindi"},
		{Value: "28", Label: "Hungarian"},
		{Value: "63", Label: "Icelandic"},
		{Value: "53", Label: "Indonesian"},
		{Value: "56", Label: "Irish"},
		{Value: "43", Label: "Italian"},
		{Value: "38", Label: "Japanese"},
		{Value: "12", Label: "Javanese"},
		{Value: "5", Label: "Kannada"},
		{Value: "41", Label: "Korean"},
		{Value: "50", Label: "Lithuanian"},
		{Value: "46", Label: "Latin"},
		{Value: "62", Label: "Latvian"},
		{Value: "33", Label: "Malay"},
		{Value: "58", Label: "Malayalam"},
		{Value: "57", Label: "Manx"},
		{Value: "9", Label: "Marathi"},
		{Value: "48", Label: "Norwegian"},
		{Value: "45", Label: "Polish"},
		{Value: "34", Label: "Portuguese"},
		{Value: "52", Label: "Brazilian Portuguese (BP)"},
		{Value: "14", Label: "Punjabi"},
		{Value: "30", Label: "Romanian"},
		{Value: "16", Label: "Russian"},
		{Value: "24", Label: "Scottish Gaelic"},
		{Value: "60", Label: "Sanskrit"},
		{Value: "31", Label: "Serbian"},
		{Value: "54", Label: "Slovenian"},
		{Value: "4", Label: "Spanish"},
		{Value: "55", Label: "Castilian Spanish"},
		{Value: "40", Label: "Swedish"},
		{Value: "29", Label: "Tagalog"},
		{Value: "11", Label: "Tamil"},
		{Value: "10", Label: "Telugu"},
		{Value: "7", Label: "Thai"},
		{Value: "42", Label: "Turkish"},
		{Value: "25", Label: "Ukrainian"},
		{Value: "15", Label: "Urdu"},
		{Value: "13", Label: "Vietnamese"},
		{Value: "47", Label: "Other"},
	}
}

// --- upstream DTOs ----------------------------------------------------------

type myAnonamouseResponse struct {
	Error   string                 `json:"error"`
	Data    []*myAnonamouseTorrent `json:"data"`
	Message string                 `json:"message"`
}

type myAnonamouseTorrent struct {
	Id                int    `json:"id"`
	Title             string `json:"title"`
	AuthorInfo        string `json:"author_info"`
	Description       string `json:"description"`
	LanguageCode      string `json:"lang_code"`
	Filetype          string `json:"filetype"`
	Vip               bool   `json:"vip"`
	Free              bool   `json:"free"`
	PersonalFreeLeech bool   `json:"personal_freeleech"`
	FreeVip           bool   `json:"fl_vip"`
	Category          string `json:"category"`
	Added             string `json:"added"`
	Grabs             int    `json:"times_completed"`
	Seeders           int    `json:"seeders"`
	Leechers          int    `json:"leechers"`
	NumFiles          int    `json:"num_files"`
	Size              string `json:"size"`
}
