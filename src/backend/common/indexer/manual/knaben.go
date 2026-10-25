// Package manual holds the hand-written indexers (one file per site).
package manual

import (
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

	"vestt/common/httpclient"
	"vestt/common/indexer"
	"vestt/common/indexer/configdata"
	"vestt/common/models"
	"vestt/common/services"
)

// Knaben ports Jackett.Common/Indexers/Definitions/Knaben.cs.
//
// mechanism: json-api (single POST to the Elastic-style API endpoint)
//
// 澄清记录:
//   - upstream hardcodes the API URL "https://api.knaben.org/v1" (it is not
//     derived from the site link); here it is an overridable unexported
//     field (default identical) so the httptest fixtures can point the
//     indexer at a fake site.
//   - Newtonsoft (no global contract resolver) serializes the request
//     dictionary verbatim and matches response properties case-sensitively:
//     wire keys are "Hits"/"Title"/"Seeders"/"Date"/"TrackerId"/"Tracker"/
//     "MagnetUrl" plus the [JsonProperty]-annotated camelCase ones
//     (categoryId/hash/details/link/bytes/peers).
//
// Ported from: Definitions/Knaben.cs.
type Knaben struct {
	indexer.Base
	cfg *knabenConfig
	// apiBase is the upstream const request URL; overridable for tests.
	apiBase string
}

// knabenConfig ports upstream ConfigurationData (no indexer-specific
// items: only the base site link + tags).
type knabenConfig struct {
	configdata.ConfigData
}

func newKnabenConfig() *knabenConfig {
	return &knabenConfig{ConfigData: *configdata.NewConfigData()}
}

// NewKnaben builds the indexer (services.Factory shape).
func NewKnaben(deps services.Deps) (indexer.Indexer, error) {
	k := &Knaben{cfg: newKnabenConfig(), apiBase: "https://api.knaben.org/v1"}
	k.Base = indexer.Base{
		IDValue:          "knaben",
		NameValue:        "Knaben",
		DescriptionValue: "Knaben is a Public torrent meta-search engine",
		SiteLinkValue:    "https://knaben.org/",
		DefaultSiteLink:  "https://knaben.org/",
		LegacySiteLinks:  []string{"https://knaben.eu/"},
		LangValue:        "en-US",
		TypeValue:        "public",
		Caps:             knabenCaps(),
		Config:           k.cfg,
		Client:           deps.Client,
		Saver:            deps.Saver,
		Cache:            deps.Cache,
	}
	return k, nil
}

func knabenCaps() *models.Capabilities {
	caps := models.NewCapabilities()
	caps.TVSearchParams = []string{models.TVParamQ, models.TVParamSeason, models.TVParamEp}
	caps.MovieSearchParams = []string{models.MovieParamQ}
	caps.MusicSearchParams = []string{models.MusicParamQ}
	caps.BookSearchParams = []string{models.BookParamQ}
	c := caps.Categories
	c.AddCategoryMapping("1000000", models.CatAudio, "Audio")
	c.AddCategoryMapping("1001000", models.CatAudioMP3, "MP3")
	c.AddCategoryMapping("1002000", models.CatAudioLossless, "Lossless")
	c.AddCategoryMapping("1003000", models.CatAudioAudiobook, "Audiobook")
	c.AddCategoryMapping("1004000", models.CatAudioVideo, "Audio Video")
	c.AddCategoryMapping("1005000", models.CatAudioOther, "Radio")
	c.AddCategoryMapping("1006000", models.CatAudioOther, "Audio Other")
	c.AddCategoryMapping("2000000", models.CatTV, "TV")
	c.AddCategoryMapping("2001000", models.CatTVHD, "TV HD")
	c.AddCategoryMapping("2002000", models.CatTVSD, "TV SD")
	c.AddCategoryMapping("2003000", models.CatTVUHD, "TV UHD")
	c.AddCategoryMapping("2004000", models.CatTVDocumentary, "Documentary")
	c.AddCategoryMapping("2005000", models.CatTVForeign, "TV Foreign")
	c.AddCategoryMapping("2006000", models.CatTVSport, "Sport")
	c.AddCategoryMapping("2007000", models.CatTVOther, "Cartoon")
	c.AddCategoryMapping("2008000", models.CatTVOther, "TV Other")
	c.AddCategoryMapping("3000000", models.CatMovies, "Movies")
	c.AddCategoryMapping("3001000", models.CatMoviesHD, "Movies HD")
	c.AddCategoryMapping("3002000", models.CatMoviesSD, "Movies SD")
	c.AddCategoryMapping("3003000", models.CatMoviesUHD, "Movies UHD")
	c.AddCategoryMapping("3004000", models.CatMoviesDVD, "Movies DVD")
	c.AddCategoryMapping("3005000", models.CatMoviesForeign, "Movies Foreign")
	c.AddCategoryMapping("3006000", models.CatMoviesForeign, "Movies Bollywood")
	c.AddCategoryMapping("3007000", models.CatMovies3D, "Movies 3D")
	c.AddCategoryMapping("3008000", models.CatMoviesOther, "Movies Other")
	c.AddCategoryMapping("4000000", models.CatPC, "PC")
	c.AddCategoryMapping("4001000", models.CatPCGames, "Games")
	c.AddCategoryMapping("4002000", models.CatPC0day, "Software")
	c.AddCategoryMapping("4003000", models.CatPCMac, "Mac")
	c.AddCategoryMapping("4004000", models.CatPCISO, "Unix")
	c.AddCategoryMapping("5000000", models.CatXXX, "XXX")
	c.AddCategoryMapping("5001000", models.CatXXXx264, "XXX Video")
	c.AddCategoryMapping("5002000", models.CatXXXImageSet, "XXX ImageSet")
	c.AddCategoryMapping("5003000", models.CatXXXOther, "XXX Games")
	c.AddCategoryMapping("5004000", models.CatXXXOther, "XXX Hentai")
	c.AddCategoryMapping("5005000", models.CatXXXOther, "XXX Other")
	c.AddCategoryMapping("6000000", models.CatTVAnime, "Anime")
	c.AddCategoryMapping("6001000", models.CatTVAnime, "Anime Subbed")
	c.AddCategoryMapping("6002000", models.CatTVAnime, "Anime Dubbed")
	c.AddCategoryMapping("6003000", models.CatTVAnime, "Anime Dual audio")
	c.AddCategoryMapping("6004000", models.CatTVAnime, "Anime Raw")
	c.AddCategoryMapping("6005000", models.CatAudioVideo, "Music Video")
	c.AddCategoryMapping("6006000", models.CatBooksOther, "Literature")
	c.AddCategoryMapping("6007000", models.CatAudioOther, "Music")
	c.AddCategoryMapping("6008000", models.CatTVAnime, "Anime non-english translated")
	c.AddCategoryMapping("7000000", models.CatConsole, "Console")
	c.AddCategoryMapping("7001000", models.CatConsolePS4, "PS4")
	c.AddCategoryMapping("7002000", models.CatConsolePS3, "PS3")
	c.AddCategoryMapping("7003000", models.CatConsolePS3, "PS2")
	c.AddCategoryMapping("7004000", models.CatConsolePS3, "PS1")
	c.AddCategoryMapping("7005000", models.CatConsolePSVita, "PS Vita")
	c.AddCategoryMapping("7006000", models.CatConsolePSP, "PSP")
	c.AddCategoryMapping("7007000", models.CatConsoleXBox360, "Xbox 360")
	c.AddCategoryMapping("7008000", models.CatConsoleXBox, "Xbox")
	c.AddCategoryMapping("7009000", models.CatConsoleNDS, "Switch")
	c.AddCategoryMapping("7010000", models.CatConsoleNDS, "NDS")
	c.AddCategoryMapping("7011000", models.CatConsoleWii, "Wii")
	c.AddCategoryMapping("7012000", models.CatConsoleWiiU, "WiiU")
	c.AddCategoryMapping("7013000", models.CatConsole3DS, "3DS")
	c.AddCategoryMapping("7014000", models.CatConsoleWii, "GameCube")
	c.AddCategoryMapping("7015000", models.CatConsoleOther, "Other")
	c.AddCategoryMapping("8000000", models.CatPCMobileOther, "Mobile")
	c.AddCategoryMapping("8001000", models.CatPCMobileAndroid, "Android")
	c.AddCategoryMapping("8002000", models.CatPCMobileiOS, "IOS")
	c.AddCategoryMapping("8003000", models.CatPCMobileOther, "PC Other")
	c.AddCategoryMapping("9000000", models.CatBooks, "Books")
	c.AddCategoryMapping("9001000", models.CatBooksEBook, "EBooks")
	c.AddCategoryMapping("9002000", models.CatBooksComics, "Comics")
	c.AddCategoryMapping("9003000", models.CatBooksMags, "Magazines")
	c.AddCategoryMapping("9004000", models.CatBooksTechnical, "Technical")
	c.AddCategoryMapping("9005000", models.CatBooksOther, "Books Other")
	c.AddCategoryMapping("10000000", models.CatOther, "Other")
	c.AddCategoryMapping("10001000", models.CatOtherMisc, "Other Misc")
	return caps
}

// ApplyConfiguration ports upstream: run an empty query and require at
// least one release.
func (k *Knaben) ApplyConfiguration(ctx context.Context, items []indexer.ConfigItem) (indexer.ConfigStatus, error) {
	if err := k.ApplyItems(items); err != nil {
		return indexer.ConfigCompleted, err
	}
	if err := k.LoadValuesFromJSON(nil, false); err != nil {
		return indexer.ConfigCompleted, err
	}
	k.SetConfigured(false)
	results, err := k.perform(ctx, models.NewTorznabQuery())
	if err != nil {
		return indexer.ConfigCompleted, err
	}
	if len(results) == 0 {
		return indexer.ConfigCompleted, errors.New("Could not find releases from this URL") //nolint:staticcheck // upstream Knaben.cs error text, kept 1:1
	}
	k.SetConfigured(true)
	k.SaveConfig(ctx)
	return indexer.ConfigCompleted, nil
}

func (k *Knaben) ResultsForQuery(ctx context.Context, q *models.TorznabQuery, isMeta bool) (*indexer.IndexerResult, error) {
	return k.ResultsForQueryWith(ctx, q, isMeta, k.perform)
}

func (k *Knaben) Test(ctx context.Context) error {
	return k.Base.Test(ctx, k.perform)
}

var knabenDateTimezoneRe = regexp.MustCompile(`[+-]\d{2}:\d{2}$`)

// perform ports KnabenRequestGenerator.GetSearchRequests (the single POST)
// plus KnabenParser.ParseResponse.
func (k *Knaben) perform(ctx context.Context, query *models.TorznabQuery) ([]*models.ReleaseInfo, error) {
	if k.Client == nil {
		return nil, errors.New("http client not configured")
	}

	body, err := k.buildPostData(query)
	if err != nil {
		return nil, err
	}

	res, err := k.Client.Do(ctx, k.ID(), &httpclient.Request{
		Method: http.MethodPost,
		URL:    k.apiBase,
		Headers: map[string]string{
			"Accept":       "application/json",
			"Content-Type": "application/json",
		},
		RawBody: body,
	})
	if err != nil {
		return nil, err
	}

	if res.StatusCode != http.StatusOK {
		// upstream logs a warning when the response was a redirect; the Go
		// client stack follows redirects transparently.
		return nil, fmt.Errorf("Unexpected response status '%d' code from indexer request", res.StatusCode) //nolint:staticcheck // upstream Knaben.cs error text, kept 1:1
	}
	k.log().Debug(string(res.Body))

	return k.parseResponse(res.Body)
}

// buildPostData ports the upstream request generator. The upstream
// Dictionary preserves insertion order through Newtonsoft serialization;
// the ordered field list reproduces the exact wire format
// (compact JSON, no indentation).
func (k *Knaben) buildPostData(query *models.TorznabQuery) (string, error) {
	type field struct {
		key string
		raw string
	}
	fields := []field{
		{key: "order_by", raw: `"date"`},
		{key: "order_direction", raw: `"desc"`},
		{key: "from", raw: "0"},
		{key: "size", raw: "100"},
		{key: "hide_unsafe", raw: "true"},
	}

	searchString := strings.TrimSpace(query.GetQueryString())
	if searchString != "" {
		q, _ := json.Marshal(searchString)
		fields = append(fields,
			field{key: "search_type", raw: `"100%"`},
			field{key: "search_field", raw: `"title"`},
			field{key: "query", raw: string(q)},
		)
	}

	categories := k.Caps.Categories.MapTorznabCapsToTrackers(query, false)
	if len(categories) > 0 {
		seen := map[string]bool{}
		var ints []string
		for _, cat := range categories {
			if seen[cat] {
				continue
			}
			seen[cat] = true
			n, err := strconv.Atoi(cat)
			if err != nil {
				return "", err
			}
			ints = append(ints, strconv.Itoa(n))
		}
		fields = append(fields, field{key: "categories", raw: "[" + strings.Join(ints, ",") + "]"})
	}

	var sb strings.Builder
	sb.WriteString("{")
	for i, f := range fields {
		if i > 0 {
			sb.WriteString(",")
		}
		key, _ := json.Marshal(f.key)
		sb.WriteString(string(key))
		sb.WriteString(":")
		sb.WriteString(f.raw)
	}
	sb.WriteString("}")
	return sb.String(), nil
}

// parseResponse ports KnabenParser.ParseResponse.
func (k *Knaben) parseResponse(body []byte) ([]*models.ReleaseInfo, error) {
	var jsonResponse knabenResponse
	if err := json.Unmarshal(body, &jsonResponse); err != nil {
		return nil, k.parseFailure(string(body), err)
	}
	if jsonResponse.Hits == nil {
		return nil, nil
	}

	var releases []*models.ReleaseInfo
	for _, row := range jsonResponse.Hits {
		if row.Seeders <= 0 {
			continue
		}

		// Not all entries have the TZ in the "date" field
		publishDate := row.Date
		if strings.TrimSpace(publishDate) != "" && !knabenDateTimezoneRe.MatchString(publishDate) {
			publishDate += "+01:00"
		}
		parsedDate, err := time.Parse(time.RFC3339, publishDate)
		if err != nil {
			return nil, k.parseFailure(string(body), err)
		}

		var downloadLink *url.URL
		if strings.TrimSpace(row.DownloadURL) != "" {
			if u, err := url.Parse(row.DownloadURL); err == nil && u.IsAbs() {
				downloadLink = u
			}
		}
		// ignore .torrent links from LimeTorrents
		if strings.Contains(row.TrackerID, "limetorrents") {
			downloadLink = nil
		}
		var magnetURI *url.URL
		if strings.TrimSpace(row.MagnetURL) != "" {
			if u, err := url.Parse(row.MagnetURL); err == nil && u.IsAbs() {
				magnetURI = u
			}
		}
		// skip LimeTorrents results without magnets
		if strings.Contains(row.TrackerID, "limetorrents") && magnetURI == nil {
			continue
		}

		infoURL, err := url.Parse(row.InfoURL)
		if err != nil || !infoURL.IsAbs() {
			return nil, k.parseFailure(string(body), fmt.Errorf("invalid details url %q", row.InfoURL))
		}

		var category []int
		seen := map[int]bool{}
		for _, cat := range row.CategoryIDs {
			for _, id := range k.Caps.Categories.MapTrackerCatToNewznab(strconv.Itoa(cat)) {
				if !seen[id] {
					seen[id] = true
					category = append(category, id)
				}
			}
		}

		size := row.Size
		seeders := int64(row.Seeders)
		peers := int64(row.Leechers + row.Seeders)
		releases = append(releases, &models.ReleaseInfo{
			Guid:                 infoURL,
			Title:                row.Title,
			Details:              infoURL,
			Link:                 downloadLink,
			MagnetUri:            magnetURI,
			Category:             category,
			InfoHash:             row.InfoHash,
			Size:                 &size,
			Seeders:              &seeders,
			Peers:                &peers,
			PublishDate:          parsedDate,
			Description:          row.Tracker,
			DownloadVolumeFactor: manualF64(0),
			UploadVolumeFactor:   manualF64(1),
		})
	}
	return releases, nil
}

// log is a nil-safe logger (Base.Logger may be nil).
func (k *Knaben) log() *slog.Logger {
	if k.Logger != nil {
		return k.Logger
	}
	return slog.Default()
}

// parseFailure mirrors upstream OnParseError.
func (k *Knaben) parseFailure(dump string, err error) error {
	k.log().Error(fmt.Sprintf("Error while parsing result: %v\n%s", err, dump))
	return &indexer.IndexerError{IndexerID: k.ID(), Kind: indexer.KindParse, Message: "Parse error"}
}

// --- upstream DTOs ----------------------------------------------------------
// Wire names follow upstream Newtonsoft behaviour: PascalCase except the
// [JsonProperty]-annotated members.

type knabenResponse struct {
	Hits []knabenRelease `json:"Hits"`
}

type knabenRelease struct {
	Title       string `json:"Title"`
	CategoryIDs []int  `json:"categoryId"`
	InfoHash    string `json:"hash"`
	InfoURL     string `json:"details"`
	DownloadURL string `json:"link"`
	MagnetURL   string `json:"MagnetUrl"`
	Size        int64  `json:"bytes"`
	Seeders     int    `json:"Seeders"`
	Leechers    int    `json:"peers"`
	Date        string `json:"Date"`
	TrackerID   string `json:"TrackerId"`
	Tracker     string `json:"Tracker"`
}
