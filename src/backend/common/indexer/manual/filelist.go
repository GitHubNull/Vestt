// Package manual holds the hand-written indexers (one file per site).
package manual

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
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

// FileList ports Jackett.Common/Indexers/Definitions/FileList.cs.
//
// mechanism: json-api (HTTP Basic auth against api.php, JSON list responses)
//
// 澄清记录:
//   - upstream `query.SanitizedSearchTerm` is used verbatim; the Go port
//     trims it as upstream's searchQuery.Trim() does.
//
// Ported from: Definitions/FileList.cs,
// Models/IndexerConfig/Bespoke/ConfigurationDataFileList.cs.
type FileList struct {
	indexer.Base
	cfg *fileListConfig
}

// fileListConfig ports upstream ConfigurationDataFileList
// (ConfigurationDataUserPasskey + Freeleech + CatWarning).
type fileListConfig struct {
	configdata.ConfigData
	Username     *configdata.StringItem
	Passkey      *configdata.StringItem
	Instructions *configdata.InfoItem
	Freeleech    *configdata.BoolItem
	CatWarning   *configdata.InfoItem
}

const fileListInstructions = "Note this is <b>not</b> your <i>password</i>.<ul><li>Login to the FileList Website</li><li>Click on the <b>Profile</b> link</li><li>Scroll down to the <b>Reset Passkey</b> section</li><li>Copy the <b>passkey</b>.</li><li>Also be aware of not leaving a trailing blank at the end of the passkey after pasting it here.</li></ul>BTW, you will not see your current passkey on your Profile until after you have downloaded your first .torrent"

func newFileListConfig() *fileListConfig {
	return &fileListConfig{
		ConfigData:   *configdata.NewConfigData(),
		Username:     configdata.NewStringItem("Username"),
		Passkey:      configdata.NewStringItem("Passkey"),
		Instructions: configdata.NewInfoItem("", fileListInstructions),
		Freeleech:    configdata.NewBoolItem("Search freeleech only"),
		CatWarning: configdata.NewInfoItem("Category Warning",
			"When mapping TV ensure you add category 5000 in addition to 5020, 5030, 5040, 5045."),
	}
}

// NewFileList builds the indexer (services.Factory shape).
func NewFileList(deps services.Deps) (indexer.Indexer, error) {
	f := &FileList{cfg: newFileListConfig()}
	f.Base = indexer.Base{
		IDValue:          "filelist",
		NameValue:        "FileList",
		DescriptionValue: "FileList is a Private Torrent Tracker. The best Romanian site.",
		SiteLinkValue:    "https://filelist.io/",
		DefaultSiteLink:  "https://filelist.io/",
		AltSiteLinks:     []string{"https://filelist.io/", "https://thefl.org/"},
		LegacySiteLinks: []string{
			"https://filelist.ro/", "http://filelist.ro/", "http://flro.org/", "https://flro.org/",
		},
		LangValue: "ro-RO",
		TypeValue: "private",
		Caps:      fileListCaps(),
		Config:    f.cfg,
		Client:    deps.Client,
		Saver:     deps.Saver,
		Cache:     deps.Cache,
	}
	return f, nil
}

func fileListCaps() *models.Capabilities {
	caps := models.NewCapabilities()
	caps.TVSearchParams = []string{models.TVParamQ, models.TVParamSeason, models.TVParamEp}
	caps.MovieSearchParams = []string{models.MovieParamQ, models.MovieParamImdbID}
	caps.MusicSearchParams = []string{models.MusicParamQ}
	caps.BookSearchParams = []string{models.BookParamQ}
	caps.TVSearchImdbAvailable = true
	c := caps.Categories
	c.AddCategoryMapping("24", models.CatTVAnime, "Anime")
	c.AddCategoryMapping("8", models.CatPC, "Apps")
	c.AddCategoryMapping("29", models.CatBooks, "Books")
	c.AddCategoryMapping("15", models.CatTV, "Cartoons")
	c.AddCategoryMapping("30", models.CatOther, "Courses")
	c.AddCategoryMapping("16", models.CatBooks, "Docs")
	c.AddCategoryMapping("5", models.CatAudioLossless, "FLAC")
	c.AddCategoryMapping("10", models.CatConsole, "Games Console")
	c.AddCategoryMapping("9", models.CatPCGames, "Games PC")
	c.AddCategoryMapping("32", models.CatXXXOther, "Games XXX")
	c.AddCategoryMapping("31", models.CatTVForeign, "K-Drama")
	c.AddCategoryMapping("17", models.CatPC, "Linux")
	c.AddCategoryMapping("22", models.CatPCMobileOther, "Mobile")
	c.AddCategoryMapping("25", models.CatMovies3D, "Movies 3D")
	c.AddCategoryMapping("6", models.CatMoviesUHD, "Movies 4K")
	c.AddCategoryMapping("26", models.CatMoviesBluRay, "Movies 4K Blu-Ray")
	c.AddCategoryMapping("20", models.CatMoviesBluRay, "Movies Blu-Ray")
	c.AddCategoryMapping("2", models.CatMoviesDVD, "Movies DVD")
	c.AddCategoryMapping("3", models.CatMoviesForeign, "Movies DVD-RO")
	c.AddCategoryMapping("4", models.CatMoviesHD, "Movies HD")
	c.AddCategoryMapping("19", models.CatMoviesForeign, "Movies HD-RO")
	c.AddCategoryMapping("1", models.CatMoviesSD, "Movies SD")
	c.AddCategoryMapping("11", models.CatAudio, "Music")
	c.AddCategoryMapping("28", models.CatMoviesForeign, "RO Dubbed")
	c.AddCategoryMapping("28", models.CatTVForeign, "RO Dubbed")
	c.AddCategoryMapping("13", models.CatTVSport, "Sport")
	c.AddCategoryMapping("27", models.CatTVUHD, "TV-Series 4K")
	c.AddCategoryMapping("21", models.CatTVHD, "TV-Series HD")
	c.AddCategoryMapping("23", models.CatTVSD, "TV-Series SD")
	c.AddCategoryMapping("18", models.CatOther, "Various")
	c.AddCategoryMapping("12", models.CatAudioVideo, "Videoclips")
	c.AddCategoryMapping("7", models.CatXXX, "XXX")
	return caps
}

func (f *FileList) apiURL() string     { return f.SiteLink() + "api.php" }
func (f *FileList) detailsURL() string { return f.SiteLink() + "details.php" }

// ApplyConfiguration ports upstream: load values, run an empty query and
// require at least one release.
func (f *FileList) ApplyConfiguration(ctx context.Context, items []indexer.ConfigItem) (indexer.ConfigStatus, error) {
	if err := f.ApplyItems(items); err != nil {
		return indexer.ConfigCompleted, err
	}
	if err := f.LoadValuesFromJSON(nil, false); err != nil {
		return indexer.ConfigCompleted, err
	}
	f.SetConfigured(false)
	results, err := f.perform(ctx, models.NewTorznabQuery())
	if err != nil {
		return indexer.ConfigCompleted, err
	}
	if len(results) == 0 {
		return indexer.ConfigCompleted, errors.New("Could not find releases.") //nolint:staticcheck // upstream FileList.cs error text, kept 1:1
	}
	f.SetConfigured(true)
	f.SaveConfig(ctx)
	return indexer.ConfigCompleted, nil
}

func (f *FileList) ResultsForQuery(ctx context.Context, q *models.TorznabQuery, isMeta bool) (*indexer.IndexerResult, error) {
	return f.ResultsForQueryWith(ctx, q, isMeta, f.perform)
}

func (f *FileList) Test(ctx context.Context) error {
	return f.Base.Test(ctx, f.perform)
}

// perform ports upstream FileList.PerformQuery (429 / error-payload /
// status handling plus the per-torrent mapping).
func (f *FileList) perform(ctx context.Context, query *models.TorznabQuery) ([]*models.ReleaseInfo, error) {
	res, err := f.callProvider(ctx, query)
	if err != nil {
		return nil, err
	}
	if res == nil {
		// upstream: daily-episode imdb searches skip the provider call
		return nil, nil
	}

	if res.StatusCode == http.StatusTooManyRequests {
		return nil, &httpclient.TooManyRequests{Message: "Rate limited"}
	}

	body := string(res.Body)
	if strings.HasPrefix(body, `{"error"`) {
		var errorResponse fileListErrorResponse
		if err := json.Unmarshal(res.Body, &errorResponse); err == nil {
			// upstream: ExceptionWithConfigData(errorResponse.Error)
			return nil, &indexer.IndexerError{IndexerID: f.ID(), Kind: indexer.KindConfig, Message: errorResponse.Error}
		}
	}

	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Unknown status code: %d (%s)", res.StatusCode, shared.StatusName(res.StatusCode)) //nolint:staticcheck // upstream FileList.cs error text, kept 1:1
	}

	var results []fileListTorrent
	if err := json.Unmarshal(res.Body, &results); err != nil {
		return nil, f.parseFailure(body, err)
	}

	var releases []*models.ReleaseInfo
	for _, row := range results {
		isFreeleech := row.FreeLeech

		// skip non-freeleech results when freeleech only is set
		if f.cfg.Freeleech.Value && !isFreeleech {
			continue
		}

		detailsURI, err := url.Parse(fmt.Sprintf("%s?id=%d", f.detailsURL(), row.ID))
		if err != nil {
			return nil, f.parseFailure(body, err)
		}
		link, err := url.Parse(row.DownloadLink)
		if err != nil {
			return nil, f.parseFailure(body, err)
		}
		var imdbID *int64
		if strings.TrimSpace(row.ImdbID) != "" {
			if id, ok := parseutil.GetImdbID(row.ImdbID); ok {
				imdbID = &id
			}
		}

		// upstream: DateTime.Parse(uploadDate + " +0200", AdjustToUniversal)
		publishDate, err := time.Parse("2006-01-02 15:04:05 -0700", row.UploadDate+" +0200")
		if err != nil {
			return nil, f.parseFailure(body, err)
		}
		publishDate = publishDate.UTC()

		size := row.Size
		files := int64(row.Files)
		grabs := int64(row.TimesCompleted)
		seeders := int64(row.Seeders)
		peers := seeders + int64(row.Leechers)
		dvf := 1.0
		if isFreeleech {
			dvf = 0
		}
		uvf := 1.0
		if row.DoubleUp {
			uvf = 2
		}
		minimumRatio := 1.0
		minimumSeedTime := int64(172800) // 48 hours

		releases = append(releases, &models.ReleaseInfo{
			Guid:                 detailsURI,
			Details:              detailsURI,
			Link:                 link,
			Title:                strings.TrimSpace(row.Name),
			Category:             f.Caps.Categories.MapTrackerCatDescToNewznab(row.Category),
			Size:                 &size,
			Files:                &files,
			Grabs:                &grabs,
			Seeders:              &seeders,
			Peers:                &peers,
			Imdb:                 imdbID,
			PublishDate:          publishDate,
			DownloadVolumeFactor: &dvf,
			UploadVolumeFactor:   &uvf,
			MinimumRatio:         &minimumRatio,
			MinimumSeedTime:      &minimumSeedTime,
		})
	}
	return releases, nil
}

// callProviderAsync ports upstream FileList.CallProviderAsync.
func (f *FileList) callProvider(ctx context.Context, query *models.TorznabQuery) (*httpclient.Result, error) {
	if f.Client == nil {
		return nil, errors.New("http client not configured")
	}

	searchQuery := strings.TrimSpace(query.SanitizedSearchTerm())

	qc := shared.QueryParams{}
	qc.Add("category", strings.Join(fileListDistinct(f.Caps.Categories.MapTorznabCapsToTrackers(query, false)), ","))

	if f.cfg.Freeleech.Value {
		qc.Set("freeleech", "1")
	}

	if query.IsImdbQuery() || strings.TrimSpace(searchQuery) != "" {
		qc.Set("action", "search-torrents")

		season := ""
		if query.Season != nil {
			season = strconv.Itoa(*query.Season)
		}
		if showDate, ok := parseShowDate(season, query.Episode); ok {
			if query.IsImdbQuery() {
				// Skip ID searches for daily episodes
				return nil, nil
			}
			searchQuery = strings.TrimSpace(searchQuery + " " + showDate.Format("2006.01.02"))
		} else {
			if query.Season != nil && *query.Season > 0 {
				qc.Set("season", strconv.Itoa(*query.Season))
			}
			if strings.TrimSpace(query.Episode) != "" {
				qc.Set("episode", query.Episode)
			}
		}

		if query.IsImdbQuery() {
			qc.Set("type", "imdb")
			qc.Set("query", query.ImdbID)
		} else if strings.TrimSpace(searchQuery) != "" {
			qc.Set("type", "name")
			qc.Set("query", searchQuery)
		}
	} else {
		qc.Set("action", "latest-torrents")
	}

	target := f.apiURL() + "?" + qc.GetQueryString()

	auth := base64.StdEncoding.EncodeToString([]byte(f.cfg.Username.Value + ":" + f.cfg.Passkey.Value))
	res, err := f.Client.Do(ctx, f.ID(), &httpclient.Request{
		URL:     target,
		Headers: map[string]string{"Authorization": "Basic " + auth},
		Cookies: f.CookieHeader(),
	})
	if err != nil {
		return nil, fmt.Errorf("Error calling provider filelist: %w", err) //nolint:staticcheck // upstream FileList.cs error text, kept 1:1
	}
	return res, nil
}

// parseShowDate mirrors upstream
// DateTime.TryParseExact($"{Season} {Episode}", "yyyy MM/dd").
func parseShowDate(season, episode string) (time.Time, bool) {
	if season == "" || strings.TrimSpace(episode) == "" {
		return time.Time{}, false
	}
	t, err := time.Parse("2006 01/02", season+" "+episode)
	return t, err == nil
}

// log is a nil-safe logger (Base.Logger may be nil).
func (f *FileList) log() *slog.Logger {
	if f.Logger != nil {
		return f.Logger
	}
	return slog.Default()
}

// parseFailure mirrors upstream OnParseError.
func (f *FileList) parseFailure(dump string, err error) error {
	f.log().Error(fmt.Sprintf("Error while parsing result: %v\n%s", err, dump))
	return &indexer.IndexerError{IndexerID: f.ID(), Kind: indexer.KindParse, Message: "Parse error"}
}

func fileListDistinct(list []string) []string {
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

// --- upstream DTOs ----------------------------------------------------------

type fileListTorrent struct {
	ID             uint   `json:"id"`
	Name           string `json:"name"`
	DownloadLink   string `json:"download_link"`
	Size           int64  `json:"size"`
	Leechers       int    `json:"leechers"`
	Seeders        int    `json:"seeders"`
	TimesCompleted uint   `json:"times_completed"`
	Files          uint   `json:"files"`
	ImdbID         string `json:"imdb"`
	Internal       bool   `json:"internal"`
	FreeLeech      bool   `json:"freeleech"`
	DoubleUp       bool   `json:"doubleup"`
	UploadDate     string `json:"upload_date"`
	Category       string `json:"category"`
	SmallDesc      string `json:"small_description"`
}

type fileListErrorResponse struct {
	Error string `json:"error"`
}
