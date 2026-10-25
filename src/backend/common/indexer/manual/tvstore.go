// Package manual holds the hand-written indexers (one file per site).
package manual

import (
	"context"
	"encoding/base64"
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
)

// TVStore ports Jackett.Common/Indexers/Definitions/TVStore.cs.
//
// mechanism: html-login (login.php → POST takelogin.php, "Főoldal" check)
// with a bespoke backslash-delimited result protocol (br_process.php) and
// per-torrent getToggle file-list requests.
//
// 澄清记录:
//   - upstream ConfigurationDataTVstore uses StringConfigurationItem for
//     Password; the Go port uses PasswordItem (package convention, masking
//     on display).
//   - upstream _seriesInfoMatch uses the .NET backreference \k<seriesID>
//     (cs:40); Go RE2 has no backreferences, so the port scans each line for
//     catl[N]=<id> and then looks for catIM[<id>]='<imdb>' in the remainder
//     of the same line (same observable behaviour, . doesn't cross \n).
//   - upstream DateTime.Parse for the publish date (cs:186) is ported as the
//     invariant layout "2006-01-02 15:04:05" local time.
//   - the season-pack episode-strip regex uses a lookbehind (?<=S\d+)
//     (cs:202-203); Go RE2 has no lookbehind, so the port removes the single
//     first E<dd> group directly following S<d> — matching .NET's
//     non-overlapping Replace semantics.
//   - row parse failures abort the remaining rows via OnParseError (cs:233-237);
//     the port surfaces a KindParse IndexerError for the whole query.
//   - TvCategoryParser (Utils/TvCategoryParser.cs) is ported file-locally as
//     tvsParseTvShowQuality.
//
// Ported from: Definitions/TVStore.cs,
// Models/IndexerConfig/Bespoke/ConfigurationDataTVstore.cs.
type TVStore struct {
	indexer.Base
	cfg *tvStoreConfig

	// imdbLookup[internalId] = imdbId (upstream _imdbLookup, cs:34)
	imdbLookup map[int64]int64
	// internalLookup[imdbId] = internalId (upstream _internalLookup, cs:36)
	internalLookup map[int64]int64
}

// tvStoreConfig ports upstream ConfigurationDataTVstore.
type tvStoreConfig struct {
	configdata.ConfigData
	Username        *configdata.StringItem
	Password        *configdata.PasswordItem
	AccountActivity *configdata.InfoItem
}

const tvStoreAccountActivity = "If a user does not log in to the site for 100 days (Tag, Storekeeper for 1 year), it is automatically deleted."

func newTVStoreConfig() *tvStoreConfig {
	return &tvStoreConfig{
		ConfigData:      *configdata.NewConfigData(),
		Username:        configdata.NewStringItem("Username"),
		Password:        configdata.NewPasswordItem("Password"),
		AccountActivity: configdata.NewInfoItem("Account Inactivity", tvStoreAccountActivity),
	}
}

// NewTVStore builds the indexer (services.Factory shape).
func NewTVStore(deps services.Deps) (indexer.Indexer, error) {
	t := &TVStore{
		cfg:            newTVStoreConfig(),
		imdbLookup:     map[int64]int64{},
		internalLookup: map[int64]int64{},
	}
	t.Base = indexer.Base{
		IDValue:          "tvstore",
		NameValue:        "TV Store",
		DescriptionValue: "TV Store is a HUNGARIAN Private Torrent Tracker for TV",
		SiteLinkValue:    "https://tvstore.me/",
		DefaultSiteLink:  "https://tvstore.me/",
		LangValue:        "hu-HU",
		TypeValue:        "private",
		Caps:             tvStoreCaps(),
		Config:           t.cfg,
		Client:           deps.Client,
		Saver:            deps.Saver,
		Cache:            deps.Cache,
	}
	return t, nil
}

func tvStoreCaps() *models.Capabilities {
	caps := models.NewCapabilities()
	caps.TVSearchParams = []string{models.TVParamQ, models.TVParamSeason, models.TVParamEp, models.TVParamImdbID}
	caps.MovieSearchParams = []string{models.MovieParamQ, models.MovieParamImdbID}
	c := caps.Categories
	c.AddCategoryMapping("1", models.CatTV, "")
	c.AddCategoryMapping("2", models.CatTVHD, "")
	c.AddCategoryMapping("3", models.CatTVSD, "")
	return caps
}

// log is a nil-safe logger for the manual package (Base.Logger may be nil).
func (t *TVStore) log() *slog.Logger {
	if t.Logger != nil {
		return t.Logger
	}
	return slog.Default()
}

func (t *TVStore) loginPageURL() string { return t.SiteLink() + "login.php?returnto=%2F" }
func (t *TVStore) loginURL() string     { return t.SiteLink() + "takelogin.php" }
func (t *TVStore) searchURL() string    { return t.SiteLink() + "torrent/br_process.php" }
func (t *TVStore) downloadURL() string  { return t.SiteLink() + "torrent/download.php" }
func (t *TVStore) browseURL() string    { return t.SiteLink() + "torrent/browse.php" }

// ApplyConfiguration ports upstream (cs:84-100): fetch the login page,
// POST the credentials to takelogin.php, and check the followed page for
// "Főoldal". RequiresTesting (the manager runs the test afterwards).
func (t *TVStore) ApplyConfiguration(ctx context.Context, items []indexer.ConfigItem) (indexer.ConfigStatus, error) {
	if err := t.ApplyItems(items); err != nil {
		return indexer.ConfigCompleted, err
	}
	if err := t.LoadValuesFromJSON(nil, false); err != nil {
		return indexer.ConfigCompleted, err
	}
	if t.Client == nil {
		return indexer.ConfigCompleted, errors.New("http client not configured")
	}

	loginPage, err := t.Client.Do(ctx, t.ID(), &httpclient.Request{
		URL:     t.loginPageURL(),
		Cookies: t.CookieHeader(),
	})
	if err != nil {
		return indexer.ConfigCompleted, err
	}

	pairs := formPairs{
		{"username", t.cfg.Username.Value},
		{"password", t.cfg.Password.Value},
		{"back", "%2F"},
		{"logout", "1"},
	}
	res, err := t.Client.Do(ctx, t.ID(), &httpclient.Request{
		Method:  http.MethodPost,
		URL:     t.loginURL(),
		RawBody: pairs.encode(),
		Headers: formContentType,
		Cookies: setCookiePairs(loginPage.Headers),
		Referer: t.SiteLink(),
	})
	if err != nil {
		return indexer.ConfigCompleted, err
	}

	if !strings.Contains(string(res.Body), "Főoldal") {
		return indexer.ConfigCompleted, &indexer.IndexerError{
			IndexerID: t.ID(),
			Kind:      indexer.KindConfig,
			Message:   "Error while trying to login.",
		}
	}

	// upstream ConfigureIfOK stores the login response cookies.
	cookies := setCookiePairs(res.Headers)
	if cookies == "" {
		if u, err := url.Parse(t.SiteLink()); err == nil {
			cookies = t.Client.CookieHeader(t.ID(), u)
		}
	}
	t.SetCookieHeader(cookies)
	t.SetConfigured(true)
	t.SaveConfig(ctx)
	return indexer.ConfigRequiresTesting, nil
}

func (t *TVStore) ResultsForQuery(ctx context.Context, q *models.TorznabQuery, isMeta bool) (*indexer.IndexerResult, error) {
	return t.ResultsForQueryWith(ctx, q, isMeta, t.perform)
}

func (t *TVStore) Test(ctx context.Context) error {
	return t.Base.Test(ctx, t.perform)
}

// tvsSeriesInfoSearchRegex ports upstream _seriesInfoSearchRegex (cs:42-43):
// Jackett doesn't check for lowercase s00e00 so the indexer does it here.
var tvsSeriesInfoSearchRegex = regexp.MustCompile(`(?i)S(\d{1,3})(?:E(\d{1,3}))?$`)

// tvsSeriesInfoCatlRe / tvsSeriesInfoCatIMRe emulate upstream
// _seriesInfoMatch with its \k<seriesID> backreference (see file header).
var (
	tvsSeriesInfoCatlRe  = regexp.MustCompile(`catl\[\d+\]=(\d+)`)
	tvsSeriesInfoCatIMRe = regexp.MustCompile(`catIM\[(\d+)\]='(\d+)'`)
)

// tvsPublishDateLayout mirrors DateTime.Parse on "2018-03-09 08:11:53".
const tvsPublishDateLayout = "2006-01-02 15:04:05"

// tvsFileExtRe strips the 3-char file extension (upstream cs:200).
var tvsFileExtRe = regexp.MustCompile(`\.\w{3}$`)

// tvsSeasonPackEpRe matches the first E<dd> group right after S<d> (emulates
// upstream's (?<=S\d+)E\d{2,3} lookbehind, see file header).
var tvsSeasonPackEpRe = regexp.MustCompile(`(?i)S\d+E\d{2,3}`)

// populateImdbMap ports upstream PopulateImdbMapAsync (cs:245-255).
func (t *TVStore) populateImdbMap(ctx context.Context) error {
	res, err := t.Client.Do(ctx, t.ID(), &httpclient.Request{
		URL:     t.browseURL(),
		Cookies: t.CookieHeader(),
	})
	if err != nil {
		return err
	}
	for _, line := range strings.Split(string(res.Body), "\n") {
		for _, m := range tvsSeriesInfoCatlRe.FindAllStringSubmatchIndex(line, -1) {
			seriesID, _ := strconv.ParseInt(line[m[2]:m[3]], 10, 64)
			rest := line[m[1]:]
			// greedy .* upstream: take the LAST catIM entry for the id.
			var imdbID int64
			found := false
			for _, cm := range tvsSeriesInfoCatIMRe.FindAllStringSubmatchIndex(rest, -1) {
				if rest[cm[2]:cm[3]] == strconv.FormatInt(seriesID, 10) {
					imdbID, _ = strconv.ParseInt(rest[cm[4]:cm[5]], 10, 64)
					found = true
				}
			}
			if found {
				t.imdbLookup[seriesID] = imdbID
				t.internalLookup[imdbID] = seriesID
			}
		}
	}
	return nil
}

// perform ports upstream PerformQuery (cs:257-344), including the paging
// loop and the per-torrent getToggle requests.
func (t *TVStore) perform(ctx context.Context, query *models.TorznabQuery) ([]*models.ReleaseInfo, error) {
	if t.Client == nil {
		return nil, errors.New("http client not configured")
	}

	var releases []*models.ReleaseInfo
	if len(t.imdbLookup) == 0 {
		if err := t.populateImdbMap(ctx); err != nil {
			return nil, err
		}
	}

	qc := shared.QueryParams{}
	qc.Add("now", strconv.FormatInt(dateutil.DateTimeToUnixTimestamp(time.Now().UTC()), 10))
	qc.Add("p", "1")
	if query.Limit == 0 {
		query.Limit = 100
	}
	if query.IsImdbQuery() {
		if query.ImdbIDShort() != "" {
			internalID, err := strconv.ParseInt(query.ImdbIDShort(), 10, 64)
			if err != nil {
				return nil, err
			}
			mapped, ok := t.internalLookup[internalID]
			if !ok {
				return []*models.ReleaseInfo{}, nil
			}
			qc.Add("g", strconv.FormatInt(mapped, 10))
		} else {
			return []*models.ReleaseInfo{}, nil
		}
	} else {
		qc.Add("g", "0")
		if strings.TrimSpace(query.SearchTerm) != "" {
			searchString := query.SanitizedSearchTerm()

			if query.Season == nil && strings.TrimSpace(query.Episode) == "" {
				if m := tvsSeriesInfoSearchRegex.FindStringSubmatch(searchString); m != nil {
					season, _ := strconv.Atoi(m[1])
					query.Season = &season
					if m[2] != "" {
						episode, _ := strconv.Atoi(m[2])
						query.Episode = fmt.Sprintf("%02d", episode)
					} else {
						query.Episode = ""
					}
					query.SearchTerm = strings.TrimSpace(strings.Replace(searchString, m[0], "", 1))
				}
			}
		} else if query.IsTest {
			query.Limit = 20
		}

		// Search string must be converted to Base64
		qc.Add("c", base64.StdEncoding.EncodeToString([]byte(query.SanitizedSearchTerm())))
	}

	if query.Season != nil && *query.Season > 0 {
		qc.Add("s", strconv.Itoa(*query.Season))

		if strings.TrimSpace(query.Episode) != "" {
			qc.Add("e", query.Episode)
		}
	}

	res, err := t.Client.Do(ctx, t.ID(), &httpclient.Request{
		URL:     t.searchURL() + "?" + qc.GetQueryString(),
		Cookies: t.CookieHeader(),
	})
	if err != nil {
		return nil, err
	}

	// Parse page information from result
	content := string(res.Body)
	splits := strings.Split(content, `\`)
	if len(splits) < 3 {
		return nil, &indexer.IndexerError{IndexerID: t.ID(), Kind: indexer.KindParse, Message: "Parse error"}
	}
	totalFound, err := strconv.Atoi(strings.TrimSpace(splits[0]))
	if err != nil {
		return nil, err
	}
	torrentPerPage, err := strconv.Atoi(strings.TrimSpace(splits[1]))
	if err != nil {
		return nil, err
	}
	if totalFound == 0 || query.Offset > totalFound {
		return []*models.ReleaseInfo{}, nil
	}
	startPage := query.Offset/torrentPerPage + 1
	previouslyParsedOnPage := query.Offset % torrentPerPage
	pages := totalFound/torrentPerPage + 1

	// First page content is already ready
	if startPage == 1 {
		pageReleases, err := t.parseTorrents(ctx, content, len(releases), query.Limit, previouslyParsedOnPage)
		if err != nil {
			return nil, err
		}
		releases = append(releases, pageReleases...)
		previouslyParsedOnPage = 0
		startPage++
	}

	for page := startPage; page <= pages && len(releases) < query.Limit; page++ {
		qc.Set("page", strconv.Itoa(page))
		res, err := t.Client.Do(ctx, t.ID(), &httpclient.Request{
			URL:     t.searchURL() + "?" + qc.GetQueryString(),
			Cookies: t.CookieHeader(),
		})
		if err != nil {
			return nil, err
		}
		pageReleases, err := t.parseTorrents(ctx, string(res.Body), len(releases), query.Limit, previouslyParsedOnPage)
		if err != nil {
			return nil, err
		}
		releases = append(releases, pageReleases...)
		previouslyParsedOnPage = 0
	}

	return releases, nil
}

// tvsTorrentParts mirrors upstream TorrentParts (cs:346-358): each torrent
// row occupies 27 backslash-separated parts of the page payload.
const (
	tvsPartInternalID  = 1
	tvsPartTorrentID   = 2
	tvsPartEpisodeInfo = 3
	tvsPartPublishDate = 6
	tvsPartGenre       = 7
	tvsPartFiles       = 10
	tvsPartSizeBytes   = 11
	tvsPartSeeders     = 20
	tvsPartLeechers    = 21
	tvsPartGrabs       = 22
)

// parseTorrents ports upstream ParseTorrentsAsync (cs:140-239). The content
// looks like:
//
//	2\25\2\1\1727\207244\1x08 [WebDL-720p]\gb\2018-03-09 08:11:53\<genre html>\0\0\1\191170047\1\0\Anonymous\50\0\0\\0\4\0\174\0\
//
// First 3 items per page are total results, results per page, and results
// this page; each torrent is divided into 27 parts.
func (t *TVStore) parseTorrents(ctx context.Context, content string, alreadyFound, limit, previouslyParsedOnPage int) ([]*models.ReleaseInfo, error) {
	var releases []*models.ReleaseInfo

	parameters := strings.Split(content, `\`)
	if len(parameters) < 3 {
		return nil, &indexer.IndexerError{IndexerID: t.ID(), Kind: indexer.KindParse, Message: "Parse error"}
	}
	torrentsThisPage, err := strconv.Atoi(strings.TrimSpace(parameters[2]))
	if err != nil {
		return nil, err
	}
	maxTorrents := torrentsThisPage
	if limit-alreadyFound < maxTorrents {
		maxTorrents = limit - alreadyFound
	}

	parts := parameters[3:] // skip pages info
	var rows [][]string
	for i := 0; i < len(parts); i += 27 {
		end := i + 27
		if end > len(parts) {
			end = len(parts)
		}
		rows = append(rows, parts[i:end])
	}
	if previouslyParsedOnPage < len(rows) {
		rows = rows[previouslyParsedOnPage:]
	} else {
		rows = nil
	}
	if maxTorrents < len(rows) {
		rows = rows[:maxTorrents]
	}

	for _, row := range rows {
		if len(row) < 27 {
			// upstream row[(int)TorrentParts.X] would throw IndexOutOfRange
			t.log().Error(fmt.Sprintf("Error while parsing result: row has %d parts, expected 27\n%s", len(row), content))
			return nil, &indexer.IndexerError{IndexerID: t.ID(), Kind: indexer.KindParse, Message: "Parse error"}
		}
		release, err := t.parseTorrentRow(ctx, row)
		if err != nil {
			t.log().Error(fmt.Sprintf("Error while parsing result: %v\n%s", err, content))
			return nil, &indexer.IndexerError{IndexerID: t.ID(), Kind: indexer.KindParse, Message: "Parse error"}
		}
		releases = append(releases, release)
	}
	return releases, nil
}

// parseTorrentRow parses one 27-part torrent row and issues the getToggle
// file-list request (upstream cs:167-231).
func (t *TVStore) parseTorrentRow(ctx context.Context, row []string) (*models.ReleaseInfo, error) {
	torrentID := row[tvsPartTorrentID]
	downloadLink, _ := url.Parse(t.downloadURL() + "?id=" + torrentID)

	// the genre field is a html string, and we just want the text
	genres := ""
	if doc, err := goquery.NewDocumentFromReader(strings.NewReader(row[tvsPartGenre])); err == nil {
		genres = strings.ReplaceAll(doc.Find("body").Text(), "\u00A0", "")
	}
	description := ""
	if strings.TrimSpace(genres) != "" {
		description = genres
	}

	internalID, err := strconv.ParseInt(strings.TrimSpace(row[tvsPartInternalID]), 10, 64)
	if err != nil {
		return nil, err
	}
	var imdbID *int64
	if imdb, ok := t.imdbLookup[internalID]; ok {
		v := imdb
		imdbID = &v
	}
	files, err := strconv.ParseInt(strings.TrimSpace(row[tvsPartFiles]), 10, 64)
	if err != nil {
		return nil, err
	}
	size, err := strconv.ParseInt(strings.TrimSpace(row[tvsPartSizeBytes]), 10, 64)
	if err != nil {
		return nil, err
	}
	seeders, err := strconv.ParseInt(strings.TrimSpace(row[tvsPartSeeders]), 10, 64)
	if err != nil {
		return nil, err
	}
	leechers, err := strconv.ParseInt(strings.TrimSpace(row[tvsPartLeechers]), 10, 64)
	if err != nil {
		return nil, err
	}
	grabs, err := strconv.ParseInt(strings.TrimSpace(row[tvsPartGrabs]), 10, 64)
	if err != nil {
		return nil, err
	}
	publishDate, err := time.ParseInLocation(tvsPublishDateLayout, strings.TrimSpace(row[tvsPartPublishDate]), time.Local)
	if err != nil {
		return nil, err
	}
	isSeasonPack := strings.Contains(row[tvsPartEpisodeInfo], "évad")

	queryParams := shared.QueryParams{}
	queryParams.Add("func", "getToggle")
	queryParams.Add("w", "F")
	queryParams.Add("pg", "0")
	queryParams.Add("id", torrentID)
	queryParams.Add("now", strconv.FormatInt(dateutil.DateTimeToUnixTimestamp(time.Now().UTC()), 10))
	filesRes, err := t.Client.Do(ctx, t.ID(), &httpclient.Request{
		URL:     t.searchURL() + "?" + queryParams.GetQueryString(),
		Cookies: t.CookieHeader(),
	})
	if err != nil {
		return nil, err
	}
	filesList := strings.Split(string(filesRes.Body), `\\`)
	if len(filesList) < 2 {
		// upstream: IndexOutOfRangeException → OnParseError
		return nil, errors.New("getToggle response has no file list")
	}
	firstFileName := filesList[1]
	// Delete the file extension. Many first files are either mkv or nfo.
	firstFileName = tvsFileExtRe.ReplaceAllString(firstFileName, "")
	if isSeasonPack {
		// upstream (?<=S\d+)E\d{2,3} (lookbehind, see file header): .NET's
		// non-overlapping Replace removes only the first E<dd> group directly
		// following the S<d> token.
		if loc := tvsSeasonPackEpRe.FindStringIndex(firstFileName); loc != nil {
			m := firstFileName[loc[0]:loc[1]]
			eIdx := strings.IndexAny(m, "eE")
			firstFileName = firstFileName[:loc[0]+eIdx] + firstFileName[loc[1]:]
		}
	}
	category := []int{tvsParseTvShowQuality(firstFileName)}

	minimumRatio := 1.0
	minimumSeedTime := int64(172800) // 48 hours
	downloadVolumeFactor := 0.0
	uploadVolumeFactor := tvsUploadFactorCalculator(publishDate, isSeasonPack)
	peers := leechers + seeders

	release := &models.ReleaseInfo{
		Title:                firstFileName,
		Link:                 downloadLink,
		Guid:                 downloadLink,
		PublishDate:          publishDate,
		Files:                &files,
		Size:                 &size,
		Category:             category,
		Seeders:              &seeders,
		Peers:                &peers,
		Grabs:                &grabs,
		Description:          description,
		MinimumRatio:         &minimumRatio,
		MinimumSeedTime:      &minimumSeedTime,
		DownloadVolumeFactor: &downloadVolumeFactor,
		UploadVolumeFactor:   &uploadVolumeFactor,
		Imdb:                 imdbID,
	}
	for _, g := range strings.Split(genres, ",") {
		g = strings.TrimSpace(g)
		if g == "" {
			continue
		}
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
	return release, nil
}

// tvsUploadFactorCalculator ports upstream UploadFactorCalculator (cs:108-130).
func tvsUploadFactorCalculator(dateTime time.Time, isSeasonPack bool) float64 {
	dd := int(time.Since(dateTime).Hours()) / 24
	if isSeasonPack {
		if dd >= 90 {
			return 4
		}
		if dd >= 30 {
			return 2
		}
		if dd >= 14 {
			return 1.5
		}
	} else {
		if dd >= 60 {
			return 2
		}
		if dd >= 30 {
			return 1.5
		}
	}
	return 1
}

// --- TvCategoryParser port (Utils/TvCategoryParser.cs) ----------------------

var (
	// Regex sourced from Sonarr (upstream line 11-23); RE2 wants \x{…} and
	// no IgnorePatternWhitespace.
	tvsSourceRe = regexp.MustCompile(`(?i)\b(?:(?P<bluray>BluRay|Blu-Ray|HDDVD|BD)|(?P<webdl>WEB[-_. ]DL|WEBDL|WebRip|iTunesHD|WebHD)|(?P<hdtv>HDTV)|(?P<bdrip>BDRip)|(?P<brrip>BRRip)|(?P<dvd>DVD|DVDRip|NTSC|PAL|xvidvd)|(?P<dsr>WS[-_. ]DSR|DSR)|(?P<pdtv>PDTV)|(?P<sdtv>SDTV)|(?P<tvrip>TVRip))\b`)
	tvsRawHdRe  = regexp.MustCompile(`(?i)\b(?P<rawhd>TrollHD|RawHD|1080i[-_. ]HDTV|Raw[-_. ]HD|MPEG[-_. ]?2)\b`)
	tvsResRe    = regexp.MustCompile(`(?i)\b(?:(?P<q480p>480p|640x480|848x480)|(?P<q576p>576p)|(?P<q720p>720p|1280x720)|(?P<q1080p>1080p|1920x1080))\b`)
	tvsCodecRe  = regexp.MustCompile(`(?i)\b(?:(?P<x264>x264)|(?P<h264>h264)|(?P<xvidhd>XvidHD)|(?P<xvid>Xvid)|(?P<divx>divx))\b`)
	tvsHrWsRe   = regexp.MustCompile(`(?i)hr[-_. ]ws`)
)

// tvsParseTvShowQuality ports upstream TvCategoryParser.ParseTvShowQuality.
func tvsParseTvShowQuality(tvShowFileName string) int {
	normalizedName := strings.ToLower(strings.ReplaceAll(strings.TrimSpace(tvShowFileName), "_", " "))

	sourceMatch := tvsSourceRe.FindStringSubmatch(normalizedName)
	resolutionMatch := tvsResRe.FindStringSubmatch(normalizedName)
	codecMatch := tvsCodecRe.FindStringSubmatch(normalizedName)

	sourceHas := func(group string) bool {
		if sourceMatch == nil {
			return false
		}
		for i, n := range tvsSourceRe.SubexpNames() {
			if n == group && sourceMatch[i] != "" {
				return true
			}
		}
		return false
	}
	resHas := func(group string) bool {
		if resolutionMatch == nil {
			return false
		}
		for i, n := range tvsResRe.SubexpNames() {
			if n == group && resolutionMatch[i] != "" {
				return true
			}
		}
		return false
	}
	codecHas := func(group string) bool {
		if codecMatch == nil {
			return false
		}
		for i, n := range tvsCodecRe.SubexpNames() {
			if n == group && codecMatch[i] != "" {
				return true
			}
		}
		return false
	}

	if sourceHas("webdl") {
		if resHas("q1080p") || resHas("q720p") {
			return models.CatTVHD.ID
		}
		if resHas("q480p") {
			return models.CatTVSD.ID
		}
	}

	if sourceHas("hdtv") {
		if resHas("q1080p") || resHas("q720p") {
			return models.CatTVHD.ID
		}
		return models.CatTVSD.ID
	}

	if sourceHas("bluray") || sourceHas("bdrip") || sourceHas("brrip") {
		if codecHas("xvid") || codecHas("divx") {
			return models.CatTVSD.ID
		}
		if resHas("q1080p") || resHas("q720p") {
			return models.CatTVHD.ID
		}
		if resHas("q480p") || resHas("q576p") {
			return models.CatTVSD.ID
		}
	}

	if sourceHas("dvd") {
		return models.CatTVSD.ID
	}

	if sourceHas("pdtv") || sourceHas("sdtv") || sourceHas("dsr") || sourceHas("tvrip") {
		if tvsHrWsRe.MatchString(normalizedName) {
			return models.CatTVHD.ID
		}
		return models.CatTVSD.ID
	}

	if tvsRawHdRe.MatchString(normalizedName) {
		return models.CatTVHD.ID
	}

	return models.CatTV.ID
}
