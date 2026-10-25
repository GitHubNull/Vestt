// Package manual holds the hand-written indexers (one file per site).
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
	"strconv"
	"strings"

	"vestt/common/httpclient"
	"vestt/common/indexer"
	"vestt/common/indexer/configdata"
	"vestt/common/indexer/shared"
	"vestt/common/models"
	"vestt/common/services"
	"vestt/common/utils/dateutil"
)

// TorrentNetwork ports Jackett.Common/Indexers/Definitions/TorrentNetwork.cs.
//
// mechanism: json-api login (username/password against api/auth, token +
// passkey stored in hidden config items, JSON api/browse results)
//
// Correction to the SPEC-10 batch-3 table ("html-login"): upstream
// authenticates via a JSON API (POST api/auth → token, POST api/curuser →
// passkey) and searches api/browse; there is no HTML form.
//
// 澄清记录:
//   - upstream LoadValuesFromJson injects the saved token into the shared
//     APIHeaders dictionary (TorrentNetwork.cs:133-150); the port keeps an
//     apiHeaders map rebuilt from the config items on every load, and
//     ApplyConfiguration drops the Authorization header before re-auth (as
//     upstream does, TorrentNetwork.cs:166).
//   - SendAPIRequest posts JsonConvert.SerializeObject(data): the auth body
//     is {"username":..,"password":..} and the curuser body is the literal
//     "null" (TorrentNetwork.cs:152-161); the port mirrors both.
//   - a response not starting with "{" is an error whose message is the raw
//     body (ExceptionWithConfigData, TorrentNetwork.cs:157-158, 228-229);
//     the port maps it to IndexerError{Kind: Config} with the body verbatim.
//   - the download-volume-factor switch reads torrent[10]: 2 → 0 (only up),
//     1 → 0.5 (50 % down), everything else → 1 (TorrentNetwork.cs:240-248).
//   - upstream unions Genres with genres.Split(',') unconditionally
//     (TorrentNetwork.cs:285-287): an empty genre string yields [""]; the
//     port replicates that quirk.
//   - upstream casts publish date from (double)torrent[3]
//     (TorrentNetwork.cs:266); the port truncates through int64.
//   - OnParseError aborts the whole query with "Parse error"
//     (TorrentNetwork.cs:291-294); the result["error"] check sits inside the
//     parse try, so an API error field surfaces as "Parse error" too
//     (TorrentNetwork.cs:233-234).
//
// Ported from: Definitions/TorrentNetwork.cs,
// Models/IndexerConfig/ConfigurationDataBasicLogin.cs.
type TorrentNetwork struct {
	indexer.Base
	cfg *torrentNetworkConfig

	apiHeaders map[string]string
	passkey    string
}

type torrentNetworkConfig struct {
	configdata.ConfigData
	Username          *configdata.StringItem
	Password          *configdata.StringItem
	Instructions      *configdata.InfoItem // ConfigurationDataBasicLogin ships an empty-named info item (ConfigurationDataBasicLogin.cs:13-21)
	Token             *configdata.HiddenItem
	Passkey           *configdata.HiddenItem
	Freeleech         *configdata.BoolItem
	AccountInactivity *configdata.InfoItem
}

const torrentNetworkInactivity = "After four weeks of inactivity (six weeks for power users) your account will be deleted (= finally and irrevocably removed from the database). To avoid deletion, you can park your account. Running torrents do not count as active, only logging in counts!!!"

func newTorrentNetworkConfig() *torrentNetworkConfig {
	freeleech := configdata.NewBoolItem("Filter freeleech only")
	freeleech.Value = false
	return &torrentNetworkConfig{
		ConfigData:        *configdata.NewConfigData(),
		Username:          configdata.NewStringItem("Username"),
		Password:          configdata.NewStringItem("Password"),
		Instructions:      configdata.NewInfoItem("", ""), // ConfigurationDataBasicLogin.cs:20 (instructionMessageOptional = null)
		Token:             configdata.NewHiddenItem("token"),
		Passkey:           configdata.NewHiddenItem("passkey"),
		Freeleech:         freeleech,
		AccountInactivity: configdata.NewInfoItem("Account Inactivity", torrentNetworkInactivity),
	}
}

// NewTorrentNetwork builds the indexer (services.Factory shape).
func NewTorrentNetwork(deps services.Deps) (indexer.Indexer, error) {
	t := &TorrentNetwork{
		cfg:        newTorrentNetworkConfig(),
		apiHeaders: map[string]string{"Content-Type": "application/json"},
	}
	t.Base = indexer.Base{
		IDValue:          "torrentnetwork",
		NameValue:        "Torrent Network",
		DescriptionValue: "Torrent Network (TN) is a GERMAN Private site for TV / MOVIES / GENERAL",
		SiteLinkValue:    "https://tntracker.org/",
		DefaultSiteLink:  "https://tntracker.org/",
		LangValue:        "de-DE",
		TypeValue:        "private",
		Caps:             torrentNetworkCaps(),
		Config:           t.cfg,
		Client:           deps.Client,
		Saver:            deps.Saver,
		Cache:            deps.Cache,
	}
	return t, nil
}

func torrentNetworkCaps() *models.Capabilities {
	caps := models.NewCapabilities()
	caps.TVSearchParams = []string{
		models.TVParamQ, models.TVParamSeason, models.TVParamEp, models.TVParamGenre,
	}
	caps.MovieSearchParams = []string{models.MovieParamQ, models.MovieParamGenre}
	caps.MusicSearchParams = []string{models.MusicParamQ, models.MusicParamGenre}
	caps.BookSearchParams = []string{models.BookParamQ, models.BookParamGenre}

	c := caps.Categories
	c.AddCategoryMapping("24", models.CatMoviesSD, "Movies GER/SD")
	c.AddCategoryMapping("18", models.CatMoviesHD, "Movies GER/720p")
	c.AddCategoryMapping("17", models.CatMoviesHD, "Movies GER/1080p")
	c.AddCategoryMapping("20", models.CatMoviesUHD, "Movies GER/2160p")
	c.AddCategoryMapping("45", models.CatMoviesOther, "Movies GER/Remux")
	c.AddCategoryMapping("19", models.CatMoviesBluRay, "Movies GER/BluRay")
	c.AddCategoryMapping("34", models.CatTVAnime, "Movies GER/Anime")
	c.AddCategoryMapping("36", models.CatMovies3D, "Movies GER/3D")

	c.AddCategoryMapping("22", models.CatMoviesSD, "Movies ENG/SD")
	c.AddCategoryMapping("35", models.CatMoviesHD, "Movies ENG/720p")
	c.AddCategoryMapping("43", models.CatMoviesHD, "Movies ENG/1080p")
	c.AddCategoryMapping("48", models.CatMoviesUHD, "Movies ENG/2160p")
	c.AddCategoryMapping("46", models.CatMoviesOther, "Movies ENG/Remux")
	c.AddCategoryMapping("38", models.CatMoviesBluRay, "Movies ENG/BluRay")
	c.AddCategoryMapping("39", models.CatTVAnime, "Movies ENG/Anime")

	c.AddCategoryMapping("27", models.CatTVSD, "Series GER/SD")
	c.AddCategoryMapping("28", models.CatTVHD, "Series GER/HD")
	c.AddCategoryMapping("2", models.CatTVAnime, "Series GER/Anime")
	c.AddCategoryMapping("16", models.CatTV, "Series GER/Pack")
	c.AddCategoryMapping("6", models.CatTVDocumentary, "Docu/SD")
	c.AddCategoryMapping("7", models.CatTVDocumentary, "Docu/HD")

	c.AddCategoryMapping("29", models.CatTVSD, "Series ENG/SD")
	c.AddCategoryMapping("40", models.CatTVHD, "Series ENG/HD")
	c.AddCategoryMapping("41", models.CatTVAnime, "Series ENG/Anime")
	c.AddCategoryMapping("42", models.CatTV, "Series ENG/Pack")
	c.AddCategoryMapping("31", models.CatTVSport, "Sport")

	c.AddCategoryMapping("10", models.CatPCGames, "Games/Win")
	c.AddCategoryMapping("12", models.CatConsoleWii, "Games/Wii")
	c.AddCategoryMapping("13", models.CatConsolePS4, "Games/PSX")
	c.AddCategoryMapping("14", models.CatConsoleXBox, "Games/XBOX")

	c.AddCategoryMapping("4", models.CatPCMac, "Apps/Mac")
	c.AddCategoryMapping("5", models.CatPC0day, "Apps/Win")

	c.AddCategoryMapping("1", models.CatAudioAudiobook, "Misc/aBook")
	c.AddCategoryMapping("8", models.CatBooks, "Misc/eBook")
	c.AddCategoryMapping("30", models.CatOther, "Misc/Sonstiges")

	c.AddCategoryMapping("44", models.CatAudioLossless, "Musik/Flac")
	c.AddCategoryMapping("25", models.CatAudioMP3, "Musik/MP3")
	c.AddCategoryMapping("26", models.CatAudioVideo, "Musik/Video")

	c.AddCategoryMapping("32", models.CatXXX, "XXX/XXX")
	c.AddCategoryMapping("33", models.CatXXX, "XXX/XXX|HD")
	return caps
}

// SendAPIRequest ports upstream SendAPIRequest (TorrentNetwork.cs:152-161).
// jsonData is the pre-serialized body (upstream JsonConvert.SerializeObject:
// auth → {"username":..,"password":..} in dictionary order, curuser →
// "null").
func (t *TorrentNetwork) sendAPIRequest(ctx context.Context, endpoint, jsonData string) (map[string]any, error) {
	if t.Client == nil {
		return nil, errors.New("http client not configured")
	}
	result, err := t.Client.Do(ctx, t.ID(), &httpclient.Request{
		Method:  http.MethodPost,
		URL:     t.apiBase() + endpoint,
		RawBody: jsonData,
		Referer: t.SiteLink(),
		Headers: t.apiHeaders,
	})
	if err != nil {
		return nil, err
	}
	if !strings.HasPrefix(string(result.Body), "{") { // not JSON => error
		return nil, &indexer.IndexerError{IndexerID: t.ID(), Kind: indexer.KindConfig, Message: string(result.Body)}
	}
	dec := json.NewDecoder(bytes.NewReader(result.Body))
	dec.UseNumber()
	var m map[string]any
	if err := dec.Decode(&m); err != nil {
		return nil, err
	}
	return m, nil
}

func (t *TorrentNetwork) apiBase() string { return t.SiteLink() + "api/" }

// ApplyConfiguration ports upstream TorrentNetwork.ApplyConfiguration
// (TorrentNetwork.cs:163-195): auth → curuser → store token/passkey;
// returns RequiresTesting.
func (t *TorrentNetwork) ApplyConfiguration(ctx context.Context, items []indexer.ConfigItem) (indexer.ConfigStatus, error) {
	if err := t.ApplyItems(items); err != nil {
		return indexer.ConfigCompleted, err
	}
	if err := t.LoadValuesFromJSON(nil, false); err != nil {
		return indexer.ConfigCompleted, err
	}
	delete(t.apiHeaders, "Authorization") // remove any token from the headers

	pairs := map[string]string{
		"username": t.cfg.Username.Value,
		"password": t.cfg.Password.Value,
	}

	// JsonConvert.SerializeObject: dictionary insertion order preserved.
	username, _ := json.Marshal(pairs["username"])
	password, _ := json.Marshal(pairs["password"])
	authBody := fmt.Sprintf(`{"username":%s,"password":%s}`, username, password)

	jsonResult, err := t.sendAPIRequest(ctx, "auth", authBody)
	if err != nil {
		t.SetConfigured(false)
		return indexer.ConfigCompleted, err
	}
	token := tnStr(jsonResult["token"])

	t.apiHeaders["Authorization"] = token

	curuser, err := t.sendAPIRequest(ctx, "curuser", "null")
	if err != nil {
		t.SetConfigured(false)
		return indexer.ConfigCompleted, err
	}
	passkey := tnStr(curuser["passkey"])
	if strings.TrimSpace(passkey) == "" {
		t.SetConfigured(false)
		return indexer.ConfigCompleted, &indexer.IndexerError{
			IndexerID: t.ID(),
			Kind:      indexer.KindConfig,
			Message:   "got empty passkey: " + tnJSON(curuser),
		}
	}
	t.passkey = passkey
	t.cfg.Passkey.Value = passkey
	t.cfg.Token.Value = token

	if len(token) == 0 {
		t.SetConfigured(false)
		return indexer.ConfigCompleted, &indexer.IndexerError{
			IndexerID: t.ID(),
			Kind:      indexer.KindConfig,
			Message:   tnJSON(jsonResult),
		}
	}
	t.SetConfigured(true)
	t.SaveConfig(ctx)
	return indexer.ConfigRequiresTesting, nil
}

func (t *TorrentNetwork) ResultsForQuery(ctx context.Context, q *models.TorznabQuery, isMeta bool) (*indexer.IndexerResult, error) {
	return t.ResultsForQueryWith(ctx, q, isMeta, t.perform)
}

func (t *TorrentNetwork) Test(ctx context.Context) error {
	return t.Base.Test(ctx, t.perform)
}

// perform ports upstream PerformQuery (TorrentNetwork.cs:197-297).
func (t *TorrentNetwork) perform(ctx context.Context, query *models.TorznabQuery) ([]*models.ReleaseInfo, error) {
	queryCollection := shared.QueryParams{}
	queryCollection.Add("orderC", "4")
	queryCollection.Add("orderD", "desc")
	queryCollection.Add("start", "0")
	queryCollection.Add("length", "100")

	if query.IsGenreQuery() {
		queryCollection.Add("genre", query.Genre)
	}

	searchString := query.GetQueryString()
	if strings.TrimSpace(searchString) != "" {
		queryCollection.Add("search", searchString)
	}

	cats := t.Caps.Categories.MapTorznabCapsToTrackers(query, false)
	if len(cats) == 0 {
		cats = t.Caps.Categories.GetTrackerCategories()
	}
	queryCollection.Add("cats", strings.Join(cats, ","))

	searchURL := t.apiBase() + "browse?" + queryCollection.GetQueryString()

	if strings.TrimSpace(t.passkey) == "" {
		if _, err := t.ApplyConfiguration(ctx, nil); err != nil {
			return nil, err
		}
	}

	if t.Client == nil {
		return nil, errors.New("http client not configured")
	}
	results, err := t.Client.Do(ctx, t.ID(), &httpclient.Request{
		URL:     searchURL,
		Referer: t.SiteLink(),
		Headers: t.apiHeaders,
	})
	if err != nil {
		return nil, err
	}
	if !strings.HasPrefix(string(results.Body), "{") { // not JSON => error
		return nil, &indexer.IndexerError{IndexerID: t.ID(), Kind: indexer.KindConfig, Message: string(results.Body)}
	}
	dec := json.NewDecoder(bytes.NewReader(results.Body))
	dec.UseNumber()
	var result map[string]any
	if err := dec.Decode(&result); err != nil {
		return nil, err
	}

	if errVal, present := result["error"]; present && errVal != nil {
		// upstream throws inside the parse try, so an API error field goes
		// through OnParseError and surfaces as "Parse error"
		// (TorrentNetwork.cs:233-234, 291-294; BaseIndexer.cs:836-842).
		return nil, t.parseFailure(string(results.Body), errors.New(tnStr(errVal)))
	}
	data, ok := result["data"].([]any)
	if !ok {
		return nil, t.parseFailure(string(results.Body), errors.New("result has no data array"))
	}

	var releases []*models.ReleaseInfo
	for _, item := range data {
		torrent, ok := item.([]any)
		if !ok {
			return nil, t.parseFailure(string(results.Body), fmt.Errorf("torrent row is not an array: %v", item))
		}
		downloadVolumeFactor := 1.0
		if flag, ok := tnInt(torrent[10]); ok {
			switch flag {
			case 2: // Only Up
				downloadVolumeFactor = 0
			case 1: // 50 % Down
				downloadVolumeFactor = 0.5
			}
		}
		if t.cfg.Freeleech.Value && downloadVolumeFactor != 0 {
			continue
		}
		torrentID, ok := tnInt(torrent[2])
		if !ok {
			return nil, t.parseFailure(string(results.Body), errors.New("torrent row has no numeric id"))
		}
		details, err := url.Parse(t.SiteLink() + "torrent/" + strconv.FormatInt(torrentID, 10))
		if err != nil {
			return nil, t.parseFailure(string(results.Body), err)
		}
		seeders, _ := tnInt(torrent[6])
		genres := strings.Trim(tnStr(torrent[9]), ",")
		description := ""
		if strings.TrimSpace(genres) != "" {
			description = "Genres: " + genres
		}
		// 12/13/14 unknown, probably IDs/name of the uploader
		link, err := url.Parse(t.SiteLink() + "sdownload/" + strconv.FormatInt(torrentID, 10) + "/" + t.passkey)
		if err != nil {
			return nil, t.parseFailure(string(results.Body), err)
		}
		ctime, ok := tnInt(torrent[3])
		if !ok {
			return nil, t.parseFailure(string(results.Body), errors.New("torrent row has no numeric ctime"))
		}
		leechers, _ := tnInt(torrent[7])
		size, _ := tnInt(torrent[5])
		grabs, _ := tnInt(torrent[11])
		peers := seeders + leechers
		uvf := 1.0
		minimumRatio := 0.8
		minimumSeedTime := int64(172800) // 48 hours

		releaseGenres := make([]string, 0)
		for _, g := range strings.Split(genres, ",") {
			found := false
			for _, existing := range releaseGenres {
				if existing == g {
					found = true
					break
				}
			}
			if !found {
				releaseGenres = append(releaseGenres, g)
			}
		}

		releases = append(releases, &models.ReleaseInfo{
			MinimumRatio:         &minimumRatio,
			MinimumSeedTime:      &minimumSeedTime,
			Category:             t.Caps.Categories.MapTrackerCatToNewznab(tnStr(torrent[0])),
			Title:                tnStr(torrent[1]),
			Details:              details,
			Guid:                 details,
			Link:                 link,
			PublishDate:          dateutil.UnixTimestampToDateTime(ctime),
			Size:                 &size,
			Seeders:              &seeders,
			Peers:                &peers,
			Description:          description,
			Genres:               releaseGenres,
			UploadVolumeFactor:   &uvf,
			DownloadVolumeFactor: &downloadVolumeFactor,
			Grabs:                &grabs,
		})
	}

	return releases, nil
}

// parseFailure mirrors upstream OnParseError: log the dump, surface
// "Parse error".
func (t *TorrentNetwork) parseFailure(dump string, err error) error {
	t.log().Error(fmt.Sprintf("Error while parsing result: %v\n%s", err, dump))
	return &indexer.IndexerError{IndexerID: t.ID(), Kind: indexer.KindParse, Message: "Parse error"}
}

// tnStr mirrors JToken.ToString() for loose JSON scalars.
func tnStr(v any) string {
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

// tnJSON renders a decoded object the way JToken.ToString() does.
func tnJSON(v any) string {
	raw, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprintf("%v", v)
	}
	return string(raw)
}

// tnInt mirrors the upstream (long)/(int)/(double) JToken casts.
func tnInt(v any) (int64, bool) {
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

// log is a nil-safe logger for the manual package (Base.Logger may be nil).
func (t *TorrentNetwork) log() *slog.Logger {
	if t.Logger != nil {
		return t.Logger
	}
	return slog.Default()
}
