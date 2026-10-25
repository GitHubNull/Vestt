// Package manual holds the hand-written indexers (one file per site).
package manual

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"strconv"
	"strings"

	"vestt/common/httpclient"
	"vestt/common/indexer"
	"vestt/common/indexer/configdata"
	"vestt/common/indexer/shared"
	"vestt/common/models"
	"vestt/common/services"
	"vestt/common/utils/dateutil"
	"vestt/common/utils/parseutil"
)

// TorrentDay ports Jackett.Common/Indexers/Definitions/TorrentDay.cs.
//
// mechanism: cookie json (t.json search API authenticated by a manually
// pasted cookie)
//
// Correction to the SPEC-10 batch-3 table ("html-login（分类 URL 模板，同
// IPT 系）"): upstream TorrentDay 0.24.2900 authenticates with a manually
// pasted cookie (ConfigurationDataCookie) and searches the JSON endpoint
// SiteLink + "t.json"; it is the same family as IPTorrents but with its own
// host, paths and category table (transcribed below from TorrentDay.cs, NOT
// copied from IPTorrents).
//
// 澄清记录:
//   - upstream detects a logged-out session via results.IsRedirect /
//     RedirectingTo.Contains("login.php") (TorrentDay.cs:213-217); the Go
//     http stack follows redirects transparently, so the port sniffs the
//     response body instead: anything that is not a JSON array yields the
//     same "The user is not logged in..." IndexerError (Kind: Auth).
//   - the imdb branch appends ";q=" + query.ImdbID WITHOUT URL-encoding
//     (TorrentDay.cs:200); the port keeps that.
//   - the category query is a bare path suffix: "t.json?25;11;q=...;free=on"
//     (TorrentDay.cs:196-208); no re-encoding of the category list.
//   - upstream OnParseError (invalid JSON / row cast) throws "Parse error"
//     and aborts the whole query (TorrentDay.cs:261-263); the port returns
//     IndexerError{Kind: Parse}.
//   - the row filter is ((!IsImdbQuery || !MovieSearchImdbAvailable) &&
//     !MatchQueryStringAND(title)) (TorrentDay.cs:226);
//     MovieSearchImdbAvailable is computed from MovieSearchParams containing
//     ImdbId → true for this indexer.
//
// Ported from: Definitions/TorrentDay.cs,
// Models/IndexerConfig/ConfigurationDataCookie.cs.
type TorrentDay struct {
	indexer.Base
	cfg *torrentDayConfig
}

type torrentDayConfig struct {
	configdata.ConfigData
	Cookie             *configdata.StringItem
	CookieInstructions *configdata.InfoItem
	Instructions       *configdata.InfoItem
	Freeleech          *configdata.BoolItem
}

const torrentDayInstructions = "Make sure you get the cookies from the same torrent day domain as configured above."

func newTorrentDayConfig() *torrentDayConfig {
	return &torrentDayConfig{
		ConfigData:         *configdata.NewConfigData(),
		Cookie:             configdata.NewStringItem("Cookie"),
		CookieInstructions: configdata.NewInfoItem("Cookie Instructions", torrentDayCookieInstructions),
		Instructions:       configdata.NewInfoItem("", torrentDayInstructions),
		Freeleech:          configdata.NewBoolItem("Search freeleech only"),
	}
}

// torrentDayCookieInstructions mirrors upstream ConfigurationDataCookie.
const torrentDayCookieInstructions = "Please enter the cookie for the site manually. <a href=\"https://github.com/Jackett/Jackett/wiki/Finding-cookies\" target=\"_blank\">See here</a> on how get the cookies." +
	"<br>Example cookie header (usually longer than this):<br><code>PHPSESSID=8rk27odm; ipsconnect_63ad9c=1; more_stuff=etc;</code>"

// NewTorrentDay builds the indexer (services.Factory shape).
func NewTorrentDay(deps services.Deps) (indexer.Indexer, error) {
	t := &TorrentDay{cfg: newTorrentDayConfig()}
	t.Base = indexer.Base{
		IDValue:          "torrentday",
		NameValue:        "TorrentDay",
		DescriptionValue: "TorrentDay (TD) is a Private site for TV / MOVIES / GENERAL",
		SiteLinkValue:    "https://tday.love/",
		DefaultSiteLink:  "https://tday.love/",
		AltSiteLinks: []string{
			"https://tday.love/",
			"https://torrentday.cool/",
			"https://secure.torrentday.com/",
			"https://classic.torrentday.com/",
			"https://www.torrentday.com/",
			"https://www.torrentday.me/",
			"https://torrentday.it/",
			"https://td.findnemo.net/",
			"https://td.getcrazy.me/",
			"https://td.workisboring.net/",
			"https://tday.findnemo.net/",
			"https://tday.getcrazy.me/",
			"https://tday.workisboring.net/",
		},
		LegacySiteLinks: []string{
			"https://torrentday.com/",
			"https://tdonline.org/",
			"https://torrentday.eu/",
			"https://td-update.com/",
			"https://www.torrentday.ru/",
			"https://www.td.af/",
			"https://td.venom.global/",
			"https://tday.venom.global/",
		},
		LangValue: "en-US",
		TypeValue: "private",
		Caps:      torrentDayCaps(),
		Config:    t.cfg,
		Client:    deps.Client,
		Saver:     deps.Saver,
		Cache:     deps.Cache,
	}
	return t, nil
}

func torrentDayCaps() *models.Capabilities {
	caps := models.NewCapabilities()
	caps.TVSearchParams = []string{
		models.TVParamQ, models.TVParamSeason, models.TVParamEp, models.TVParamImdbID, models.TVParamGenre,
	}
	caps.MovieSearchParams = []string{models.MovieParamQ, models.MovieParamImdbID, models.MovieParamGenre}
	caps.MusicSearchParams = []string{models.MusicParamQ}
	caps.BookSearchParams = []string{models.BookParamQ}

	c := caps.Categories
	// Movies (TorrentDay.cs:99-110)
	c.AddCategoryMapping("25", models.CatMoviesSD, "Movies/480p")
	c.AddCategoryMapping("96", models.CatMoviesUHD, "Movie/4K")
	c.AddCategoryMapping("11", models.CatMoviesBluRay, "Movies/Bluray")
	c.AddCategoryMapping("5", models.CatMoviesBluRay, "Movies/Bluray-Full")
	c.AddCategoryMapping("103", models.CatMoviesSD, "Movies/Cam")
	c.AddCategoryMapping("3", models.CatMoviesDVD, "Movies/DVD-R")
	c.AddCategoryMapping("21", models.CatMoviesSD, "Movies/MP4")
	c.AddCategoryMapping("22", models.CatMoviesForeign, "Movies/Non-English")
	c.AddCategoryMapping("13", models.CatMovies, "Movies/Packs")
	c.AddCategoryMapping("44", models.CatMoviesSD, "Movies/SD/x264")
	c.AddCategoryMapping("48", models.CatMovies, "Movies/x265")
	c.AddCategoryMapping("1", models.CatMoviesSD, "Movies/XviD")
	// TV (TorrentDay.cs:112-123)
	c.AddCategoryMapping("24", models.CatTVSD, "TV/480p")
	c.AddCategoryMapping("104", models.CatTVUHD, "TV/4K")
	c.AddCategoryMapping("32", models.CatTVHD, "TV/Bluray")
	c.AddCategoryMapping("31", models.CatTVSD, "TV/DVD-R")
	c.AddCategoryMapping("33", models.CatTVSD, "TV/DVD-Rip")
	c.AddCategoryMapping("46", models.CatTVSD, "TV/Mobile")
	c.AddCategoryMapping("82", models.CatTVForeign, "TV/Non-English")
	c.AddCategoryMapping("14", models.CatTV, "TV/Packs")
	c.AddCategoryMapping("26", models.CatTVSD, "TV/SD/x264")
	c.AddCategoryMapping("7", models.CatTVHD, "TV/x264")
	c.AddCategoryMapping("34", models.CatTVUHD, "TV/x265")
	c.AddCategoryMapping("2", models.CatTVSD, "TV/XviD")
	// Games (TorrentDay.cs:125-129)
	c.AddCategoryMapping("10", models.CatConsoleNDS, "Nintendo")
	c.AddCategoryMapping("4", models.CatPCGames, "PC/Games")
	c.AddCategoryMapping("18", models.CatConsolePS3, "PS")
	c.AddCategoryMapping("8", models.CatConsolePSP, "PSP")
	c.AddCategoryMapping("9", models.CatConsoleXBox, "Xbox")
	// Music (TorrentDay.cs:131-135)
	c.AddCategoryMapping("17", models.CatAudioMP3, "Music/Audio")
	c.AddCategoryMapping("27", models.CatAudio, "Music/Flac")
	c.AddCategoryMapping("23", models.CatAudioForeign, "Music/Non-English")
	c.AddCategoryMapping("41", models.CatAudio, "Music/Packs")
	c.AddCategoryMapping("16", models.CatAudioVideo, "Music/Video")
	// Other (TorrentDay.cs:137-147; note the upstream "Softwa/Packs" typo)
	c.AddCategoryMapping("29", models.CatTVAnime, "Anime")
	c.AddCategoryMapping("42", models.CatAudioAudiobook, "Audio Books")
	c.AddCategoryMapping("20", models.CatBooks, "Books")
	c.AddCategoryMapping("102", models.CatBooksForeign, "Books/Non-English")
	c.AddCategoryMapping("30", models.CatTVDocumentary, "Documentary")
	c.AddCategoryMapping("95", models.CatTVDocumentary, "Educational")
	c.AddCategoryMapping("47", models.CatOther, "Fonts")
	c.AddCategoryMapping("43", models.CatPCMac, "Mac")
	c.AddCategoryMapping("45", models.CatAudioOther, "Podcast")
	c.AddCategoryMapping("28", models.CatPC, "Softwa/Packs")
	c.AddCategoryMapping("12", models.CatPC, "Software")
	// XXX (TorrentDay.cs:149-151)
	c.AddCategoryMapping("19", models.CatXXX, "XXX/0Day")
	c.AddCategoryMapping("6", models.CatXXX, "XXX/Movies")
	c.AddCategoryMapping("15", models.CatXXXPack, "XXX/Packs")
	return caps
}

// ApplyConfiguration ports upstream TorrentDay.ApplyConfiguration
// (TorrentDay.cs:156-176): load the cookie, test-query the tracker, wrap
// every failure in "Your cookie did not work: …".
func (t *TorrentDay) ApplyConfiguration(ctx context.Context, items []indexer.ConfigItem) (indexer.ConfigStatus, error) {
	if err := t.ApplyItems(items); err != nil {
		return indexer.ConfigCompleted, err
	}
	if err := t.LoadValuesFromJSON(nil, false); err != nil {
		return indexer.ConfigCompleted, err
	}
	t.SetCookieHeader(t.cfg.Cookie.Value)
	results, err := t.perform(ctx, models.NewTorznabQuery())
	if err != nil {
		t.SetConfigured(false)
		return indexer.ConfigCompleted, fmt.Errorf("Your cookie did not work: %s", err.Error()) //nolint:staticcheck // upstream TorrentDay.cs error text, kept 1:1
	}
	if len(results) == 0 {
		t.SetConfigured(false)
		return indexer.ConfigCompleted, errors.New("Your cookie did not work: Found 0 results in the tracker") //nolint:staticcheck // upstream TorrentDay.cs error text, kept 1:1
	}
	t.SetConfigured(true)
	t.SaveConfig(ctx)
	return indexer.ConfigCompleted, nil
}

func (t *TorrentDay) ResultsForQuery(ctx context.Context, q *models.TorznabQuery, isMeta bool) (*indexer.IndexerResult, error) {
	return t.ResultsForQueryWith(ctx, q, isMeta, t.perform)
}

func (t *TorrentDay) Test(ctx context.Context) error {
	return t.Base.Test(ctx, t.perform)
}

// perform ports upstream PerformQuery (TorrentDay.cs:178-265).
func (t *TorrentDay) perform(ctx context.Context, query *models.TorznabQuery) ([]*models.ReleaseInfo, error) {
	/* notes:
	 * TorrentDay can search for genre (tags) using the default title&tags search
	 * qf=
	 * "" = Title and Tags
	 * ta = Tags
	 * all = Title, Tags & Description
	 * adv = Advanced
	 *
	 * But only movies and tv have tags and the t.json does not return tags in results.
	 */

	cats := t.Caps.Categories.MapTorznabCapsToTrackers(query, false)
	if len(cats) == 0 {
		cats = t.Caps.Categories.GetTrackerCategories()
	}
	searchURL := t.SiteLink() + "t.json?" + strings.Join(cats, ";")

	if query.IsImdbQuery() {
		searchURL += ";q=" + query.ImdbID
	} else if query.IsGenreQuery() {
		searchURL += ";q=" + shared.Encode(query.GetQueryString()+" "+query.Genre)
	} else {
		searchURL += ";q=" + shared.Encode(query.GetQueryString())
	}

	if t.cfg.Freeleech.Value {
		searchURL += ";free=on"
	}

	if t.Client == nil {
		return nil, errors.New("http client not configured")
	}
	res, err := t.Client.Do(ctx, t.ID(), &httpclient.Request{
		URL:     searchURL,
		Cookies: t.CookieHeader(),
	})
	if err != nil {
		return nil, err
	}

	// Upstream checks IsRedirect/RedirectingTo.Contains("login.php") here
	// (TorrentDay.cs:213-217); the Go stack follows redirects, so a sniff
	// for the JSON payload stands in for it (see file header).
	trimmed := bytes.TrimSpace(res.Body)
	if len(trimmed) == 0 || (trimmed[0] != '[' && trimmed[0] != '{') {
		return nil, &indexer.IndexerError{
			IndexerID: t.ID(),
			Kind:      indexer.KindAuth,
			Message:   "The user is not logged in. It is possible that the cookie has expired or you made a mistake when copying it. Please check the settings.",
		}
	}

	dec := json.NewDecoder(bytes.NewReader(res.Body))
	dec.UseNumber()
	var rows []map[string]any
	if err := dec.Decode(&rows); err != nil {
		return nil, t.parseFailure(string(res.Body), err)
	}

	movieImdbAvailable := false
	for _, p := range t.Caps.MovieSearchParams {
		if p == models.MovieParamImdbID {
			movieImdbAvailable = true
		}
	}

	var releases []*models.ReleaseInfo
	for _, row := range rows {
		title := tdStr(row["name"])
		if (!query.IsImdbQuery() || !movieImdbAvailable) && !query.MatchQueryStringAND(title, 0, "") {
			continue
		}
		torrentID, ok := tdInt(row["t"])
		if !ok {
			return nil, t.parseFailure(string(res.Body), fmt.Errorf("row %q has no numeric id", title))
		}
		details, err := tdResolve(t.SiteLink() + "details.php?id=" + strconv.FormatInt(torrentID, 10))
		if err != nil {
			return nil, t.parseFailure(string(res.Body), err)
		}
		seeders, _ := tdInt(row["seeders"])
		imdb, _ := parseutil.GetImdbID(tdStr(row["imdb-id"]))
		downloadMultiplier, ok := tdFloat(row["download-multiplier"])
		if !ok {
			downloadMultiplier = 1
		}
		link, err := tdResolve(t.SiteLink() + "download.php/" + strconv.FormatInt(torrentID, 10) + "/" + strconv.FormatInt(torrentID, 10) + ".torrent")
		if err != nil {
			return nil, t.parseFailure(string(res.Body), err)
		}
		ctime, ok := tdInt(row["ctime"])
		if !ok {
			return nil, t.parseFailure(string(res.Body), fmt.Errorf("row %q has no numeric ctime", title))
		}
		leechers, _ := tdInt(row["leechers"])
		size, _ := tdInt(row["size"])
		files, _ := tdInt(row["files"])
		grabs, _ := tdInt(row["completed"])
		peers := seeders + leechers
		uvf := 1.0
		minimumRatio := 1.0
		minimumSeedTime := int64(259200) // 72 hours

		releases = append(releases, &models.ReleaseInfo{
			Title:                title,
			Details:              details,
			Guid:                 details,
			Link:                 link,
			PublishDate:          dateutil.UnixTimestampToDateTime(ctime),
			Category:             t.Caps.Categories.MapTrackerCatToNewznab(tdStr(row["c"])),
			Size:                 &size,
			Files:                &files,
			Grabs:                &grabs,
			Seeders:              &seeders,
			Peers:                &peers,
			Imdb:                 tdImdbPtr(imdb),
			DownloadVolumeFactor: &downloadMultiplier,
			UploadVolumeFactor:   &uvf,
			MinimumRatio:         &minimumRatio,
			MinimumSeedTime:      &minimumSeedTime,
		})
	}
	return releases, nil
}

// parseFailure mirrors upstream OnParseError: the response dump is logged
// and the failure surfaces as Exception("Parse error", ex).
func (t *TorrentDay) parseFailure(dump string, err error) error {
	t.log().Error(fmt.Sprintf("Error while parsing result: %v\n%s", err, dump))
	return &indexer.IndexerError{IndexerID: t.ID(), Kind: indexer.KindParse, Message: "Parse error"}
}

// tdStr mirrors (string)JToken for loose JSON scalars.
func tdStr(v any) string {
	switch tv := v.(type) {
	case nil:
		return ""
	case string:
		return tv
	case json.Number:
		return tv.String()
	case bool:
		return strconv.FormatBool(tv)
	default:
		return fmt.Sprintf("%v", tv)
	}
}

// tdInt mirrors the upstream (long)/(int) JToken casts.
func tdInt(v any) (int64, bool) {
	switch tv := v.(type) {
	case json.Number:
		n, err := tv.Int64()
		if err == nil {
			return n, true
		}
		f, err := tv.Float64()
		if err == nil {
			return int64(f), true
		}
	case string:
		n, err := strconv.ParseInt(strings.TrimSpace(tv), 10, 64)
		if err == nil {
			return n, true
		}
	}
	return 0, false
}

// tdFloat mirrors the upstream (double?) cast; ok=false for null/missing.
func tdFloat(v any) (float64, bool) {
	switch tv := v.(type) {
	case nil:
		return 0, false
	case json.Number:
		f, err := tv.Float64()
		if err == nil {
			return f, true
		}
	}
	return 0, false
}

// tdImdbPtr wraps an imdb id the way upstream ReleaseInfo.Imdb does.
func tdImdbPtr(id int64) *int64 {
	if id == 0 {
		return nil
	}
	return &id
}

func tdResolve(raw string) (*url.URL, error) { return url.Parse(raw) }

// log is a nil-safe logger for the manual package (Base.Logger may be nil).
func (t *TorrentDay) log() *slog.Logger {
	if t.Logger != nil {
		return t.Logger
	}
	return slog.Default()
}
