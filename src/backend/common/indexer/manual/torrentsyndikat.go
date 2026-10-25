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
	"vestt/common/indexer/shared"
	"vestt/common/models"
	"vestt/common/services"
)

// TorrentSyndikat ports Jackett.Common/Indexers/Definitions/TorrentSyndikat.cs.
//
// mechanism: apikey json (ConfigurationDataAPIKey + custom JSON API; not an
// HTML login — the batch table entry "html-login" is a mislabel)
//
// 澄清记录:
//   - upstream registers keyInfo/productsOnly/productsOnlyInfo/releaseType/
//     releaseTypeInfo via AddDynamic; the Go port declares them as exported
//     fields (auto-registered, same ids derived from the item names).
//   - upstream CheckResponseStatus throws plain Exceptions for non-200 API
//     statuses (cs:290-305); the Go port returns those messages verbatim as
//     errors (no IndexerError kind mapping, matching upstream behaviour for
//     an unconfigured-key call during ApplyConfiguration).
//   - upstream Download appends "&apikey=" to the download link (cs:283-288);
//     the port overrides Download directly (Base.DownloadWith would treat the
//     401/403 statuses with the generic "Remote server returned N" message).
//   - upstream parses the JSON rows inside try/catch → OnParseError; the port
//     surfaces a KindParse IndexerError like the other manual ports.
//
// Ported from: Definitions/TorrentSyndikat.cs,
// Models/IndexerConfig/ConfigurationDataAPIKey.cs.
type TorrentSyndikat struct {
	indexer.Base
	cfg *torrentSyndikatConfig
}

// torrentSyndikatConfig ports upstream ConfigurationDataAPIKey plus the
// dynamic key info, "Products only" bool and "Release Type" multi-select
// (declared as fields instead of AddDynamic — same registration/ordering).
type torrentSyndikatConfig struct {
	configdata.ConfigData
	Key              *configdata.StringItem
	KeyInfo          *configdata.InfoItem
	ProductsOnly     *configdata.BoolItem
	ProductsOnlyInfo *configdata.InfoItem
	ReleaseType      *configdata.MultiSelectItem
	ReleaseTypeInfo  *configdata.InfoItem
}

const torrentSyndikatKeyInfo = "Generate a new key <a href=\"https://torrent-syndikat.org/keymgm/keys.php\" target=_blank>here</a>, set <i>download</i> and <i>browse</i> scopes."

func newTorrentSyndikatConfig() *torrentSyndikatConfig {
	return &torrentSyndikatConfig{
		ConfigData:       *configdata.NewConfigData(),
		Key:              configdata.NewStringItem("APIKey"),
		KeyInfo:          configdata.NewInfoItem("", torrentSyndikatKeyInfo),
		ProductsOnly:     configdata.NewBoolItem("Products only"),
		ProductsOnlyInfo: configdata.NewInfoItem("", "Limit search to torrents linked to a product."),
		ReleaseType: configdata.NewMultiSelectItem("Release Type", []configdata.Option{
			{Value: "P2P", Label: "P2P"},
			{Value: "Scene", Label: "Scene"},
			{Value: "O-Scene", Label: "O-Scene"},
		}),
		ReleaseTypeInfo: configdata.NewInfoItem("", "Limit search to specific release types."),
	}
}

// NewTorrentSyndikat builds the indexer (services.Factory shape).
func NewTorrentSyndikat(deps services.Deps) (indexer.Indexer, error) {
	t := &TorrentSyndikat{cfg: newTorrentSyndikatConfig()}
	// upstream: Values = new[] { "P2P", "Scene", "O-Scene" }
	t.cfg.ReleaseType.Values = []string{"P2P", "Scene", "O-Scene"}
	t.Base = indexer.Base{
		IDValue:          "torrentsyndikat",
		NameValue:        "Torrent-Syndikat",
		DescriptionValue: "Torrent-Syndikat is a GERMAN Private general tracker",
		SiteLinkValue:    "https://torrent-syndikat.org/",
		DefaultSiteLink:  "https://torrent-syndikat.org/",
		LangValue:        "de-DE",
		TypeValue:        "private",
		Caps:             torrentSyndikatCaps(),
		Config:           t.cfg,
		Client:           deps.Client,
		Saver:            deps.Saver,
		Cache:            deps.Cache,
	}
	return t, nil
}

func torrentSyndikatCaps() *models.Capabilities {
	caps := models.NewCapabilities()
	caps.TVSearchParams = []string{models.TVParamQ, models.TVParamSeason, models.TVParamEp}
	caps.MovieSearchParams = []string{models.MovieParamQ, models.MovieParamImdbID}
	caps.MusicSearchParams = []string{models.MusicParamQ}
	caps.BookSearchParams = []string{models.BookParamQ}
	c := caps.Categories

	c.AddCategoryMapping("2", models.CatPC, "Apps / Windows")
	c.AddCategoryMapping("13", models.CatPC, "Apps / Linux")
	c.AddCategoryMapping("4", models.CatPCMac, "Apps / MacOS")
	c.AddCategoryMapping("6", models.CatPC, "Apps / Misc")

	c.AddCategoryMapping("50", models.CatPCGames, "Spiele / Windows")
	c.AddCategoryMapping("51", models.CatPCGames, "Spiele / MacOS")
	c.AddCategoryMapping("52", models.CatPCGames, "Spiele / Linux")
	c.AddCategoryMapping("8", models.CatConsoleOther, "Spiele / Playstation")
	c.AddCategoryMapping("7", models.CatConsoleOther, "Spiele / Nintendo")
	c.AddCategoryMapping("32", models.CatConsoleOther, "Spiele / XBOX")

	c.AddCategoryMapping("42", models.CatMoviesUHD, "Filme / 2160p")
	c.AddCategoryMapping("9", models.CatMoviesHD, "Filme / 1080p")
	c.AddCategoryMapping("20", models.CatMoviesHD, "Filme / 720p")
	c.AddCategoryMapping("10", models.CatMoviesSD, "Filme / SD")

	c.AddCategoryMapping("43", models.CatTVUHD, "Serien / 2160p")
	c.AddCategoryMapping("53", models.CatTVHD, "Serien / 1080p")
	c.AddCategoryMapping("54", models.CatTVHD, "Serien / 720p")
	c.AddCategoryMapping("15", models.CatTVSD, "Serien / SD")
	c.AddCategoryMapping("30", models.CatTVSport, "Serien / Sport")

	c.AddCategoryMapping("44", models.CatTVUHD, "Serienpacks / 2160p")
	c.AddCategoryMapping("55", models.CatTVHD, "Serienpacks / 1080p")
	c.AddCategoryMapping("56", models.CatTVHD, "Serienpacks / 720p")
	c.AddCategoryMapping("27", models.CatTVSD, "Serienpacks / SD")

	c.AddCategoryMapping("24", models.CatAudioLossless, "Audio / Musik / FLAC")
	c.AddCategoryMapping("25", models.CatAudioMP3, "Audio / Musik / MP3")
	c.AddCategoryMapping("35", models.CatAudioOther, "Audio / Other")
	c.AddCategoryMapping("18", models.CatAudioAudiobook, "Audio / aBooks")
	c.AddCategoryMapping("33", models.CatAudioVideo, "Audio / Videos")

	c.AddCategoryMapping("17", models.CatBooks, "Misc / eBooks")
	c.AddCategoryMapping("5", models.CatPCMobileOther, "Misc / Mobile")
	c.AddCategoryMapping("39", models.CatOther, "Misc / Bildung")

	c.AddCategoryMapping("36", models.CatTVForeign, "Englisch / Serien")
	c.AddCategoryMapping("57", models.CatTVForeign, "Englisch / Serienpacks")
	c.AddCategoryMapping("37", models.CatMoviesForeign, "Englisch / Filme")
	c.AddCategoryMapping("47", models.CatBooks, "Englisch / eBooks")
	c.AddCategoryMapping("48", models.CatOther, "Englisch / Bildung")
	c.AddCategoryMapping("49", models.CatTVSport, "Englisch / Sport")
	return caps
}

// apiBase ports upstream ApiBase (cs:32).
func (t *TorrentSyndikat) apiBase() string { return t.SiteLink() + "api_9djWe8Tb2NE3p6opyqnh/v1" }

// log is a nil-safe logger for the manual package (Base.Logger may be nil).
func (t *TorrentSyndikat) log() *slog.Logger {
	if t.Logger != nil {
		return t.Logger
	}
	return slog.Default()
}

// ApplyConfiguration ports upstream: load values, run an empty query, and
// fail with "Could not find any releases" when nothing comes back (cs:137-148).
func (t *TorrentSyndikat) ApplyConfiguration(ctx context.Context, items []indexer.ConfigItem) (indexer.ConfigStatus, error) {
	if err := t.ApplyItems(items); err != nil {
		return indexer.ConfigCompleted, err
	}
	if err := t.LoadValuesFromJSON(nil, false); err != nil {
		return indexer.ConfigCompleted, err
	}
	releases, err := t.perform(ctx, models.NewTorznabQuery())
	if err != nil {
		return indexer.ConfigCompleted, err
	}
	if len(releases) == 0 {
		return indexer.ConfigCompleted, errors.New("Could not find any releases") //nolint:staticcheck // upstream TorrentSyndikat.cs error text, kept 1:1
	}
	t.SetConfigured(true)
	t.SaveConfig(ctx)
	return indexer.ConfigCompleted, nil
}

func (t *TorrentSyndikat) ResultsForQuery(ctx context.Context, q *models.TorznabQuery, isMeta bool) (*indexer.IndexerResult, error) {
	return t.ResultsForQueryWith(ctx, q, isMeta, t.perform)
}

func (t *TorrentSyndikat) Test(ctx context.Context) error {
	return t.Base.Test(ctx, t.perform)
}

// tsSeasonEpisodeSuffixRe ports upstream (cs:174): suffix the first
// occurrence of `s01` surrounded by whitespace with * so a whole-season
// search also finds single episodes.
var tsSeasonEpisodeSuffixRe = regexp.MustCompile(`(?i)(^|\s)(s\d{2})(\s|$)`)

func (t *TorrentSyndikat) perform(ctx context.Context, query *models.TorznabQuery) ([]*models.ReleaseInfo, error) {
	if t.Client == nil {
		return nil, errors.New("http client not configured")
	}

	qc := shared.QueryParams{}
	qc.Add("apikey", t.cfg.Key.Value)
	qc.Add("limit", "50") // Default 30
	qc.Add("ponly", strconv.FormatBool(t.cfg.ProductsOnly.Value))
	for _, releaseType := range t.cfg.ReleaseType.Values {
		qc.Add("release_type", releaseType)
	}

	if query.ImdbID != "" {
		qc.Add("imdbId", query.ImdbIDShort())
	} else if searchString := query.GetQueryString(); strings.TrimSpace(searchString) != "" {
		qc.Add("searchstring", tsSeasonEpisodeSuffixRe.ReplaceAllString(strings.TrimSpace(searchString), "${1}${2}*${3}"))
	}

	cats := t.Caps.Categories.MapTorznabCapsToTrackers(query, false)
	if joined := strings.Join(cats, ","); joined != "" {
		qc.Add("cats", joined)
	}

	searchURL := t.apiBase() + "/browse.php?" + qc.GetQueryString()
	res, err := t.Client.Do(ctx, t.ID(), &httpclient.Request{
		URL:     searchURL,
		Cookies: t.CookieHeader(),
	})
	if err != nil {
		return nil, err
	}
	if err := tsCheckResponseStatus(res.StatusCode, "browse"); err != nil {
		return nil, err
	}

	var payload struct {
		Rows []tsRow `json:"rows"`
	}
	if err := json.Unmarshal(res.Body, &payload); err != nil {
		t.log().Error(fmt.Sprintf("Error while parsing result: %v\n%s", err, res.Body))
		return nil, &indexer.IndexerError{IndexerID: t.ID(), Kind: indexer.KindParse, Message: "Parse error"}
	}

	var releases []*models.ReleaseInfo
	for _, row := range payload.Rows {
		details, _ := url.Parse(t.SiteLink() + "details.php?id=" + row.ID)
		link, _ := url.Parse(t.SiteLink() + "download.php?id=" + row.ID)
		seeders := row.Seeders
		leechers := row.Leechers
		peers := seeders + leechers
		size := row.Size
		files := row.NumFiles
		grabs := row.Snatched
		minimumRatio := 1.0
		minimumSeedTime := int64(96 * 60 * 60)
		downloadVolumeFactor := 1.0
		uploadVolumeFactor := 1.0

		release := &models.ReleaseInfo{
			MinimumRatio:         &minimumRatio,
			MinimumSeedTime:      &minimumSeedTime,
			DownloadVolumeFactor: &downloadVolumeFactor,
			UploadVolumeFactor:   &uploadVolumeFactor,
			Guid:                 details,
			Details:              details,
			Link:                 link,
			Title:                row.Name,
			Category:             t.Caps.Categories.MapTrackerCatToNewznab(strconv.FormatInt(row.Category, 10)),
			PublishDate:          time.Unix(row.Added, 0),
			Size:                 &size,
			Files:                &files,
			Seeders:              &seeders,
			Peers:                &peers,
			Grabs:                &grabs,
			Imdb:                 row.ImdbID,
			TVDBId:               row.TVDBID,
			TMDb:                 row.TMDbID,
		}

		if strings.TrimSpace(row.Poster) != "" {
			// upstream: SiteLink + poster.Substring(1) (cs:226)
			release.Poster, _ = url.Parse(t.SiteLink() + row.Poster[1:])
		}

		var descriptions []string
		if strings.TrimSpace(row.Title) != "" {
			descriptions = append(descriptions, "Title: "+row.Title)
		}
		if strings.TrimSpace(row.TitleOrigin) != "" {
			descriptions = append(descriptions, "Original Title: "+row.TitleOrigin)
		}
		if row.Year != nil && *row.Year > 0 {
			descriptions = append(descriptions, "Year: "+strconv.FormatInt(*row.Year, 10))
		}
		if row.PID != nil && *row.PID > 0 {
			descriptions = append(descriptions, "Product-Link: "+t.SiteLink()+"product.php?pid="+strconv.FormatInt(*row.PID, 10))
		}
		if len(row.Genres) > 0 {
			descriptions = append(descriptions, "Genres: "+strings.Join(row.Genres, ", "))
		}
		if len(row.Tags) > 0 {
			descriptions = append(descriptions, "Tags: "+strings.Join(row.Tags, ", "))
		}
		if strings.TrimSpace(row.ReleaseType) != "" {
			descriptions = append(descriptions, "Release Type: "+row.ReleaseType)
		}
		if len(descriptions) > 0 {
			release.Description = strings.Join(descriptions, "\n") // Environment.NewLine
		}

		releases = append(releases, release)
	}
	return releases, nil
}

// Download ports upstream (cs:283-288): the apikey is appended to the
// download link and the status is validated like the browse endpoint.
func (t *TorrentSyndikat) Download(ctx context.Context, link *url.URL) ([]byte, error) {
	if t.Client == nil {
		return nil, errors.New("http client not configured")
	}
	res, err := t.Client.Do(ctx, t.ID(), &httpclient.Request{
		URL:     link.String() + "&apikey=" + t.cfg.Key.Value,
		Cookies: t.CookieHeader(),
	})
	if err != nil {
		return nil, err
	}
	if err := tsCheckResponseStatus(res.StatusCode, "download"); err != nil {
		return nil, err
	}
	return res.Body, nil
}

// tsCheckResponseStatus ports upstream (cs:290-305).
func tsCheckResponseStatus(status int, scope string) error {
	switch status {
	case http.StatusOK:
		return nil
	case http.StatusBadRequest:
		return errors.New("Unknown or missing parameters") //nolint:staticcheck // upstream TorrentSyndikat.cs error text, kept 1:1
	case http.StatusUnauthorized:
		return errors.New("Wrong API-Key") //nolint:staticcheck // upstream TorrentSyndikat.cs error text, kept 1:1
	case http.StatusForbidden:
		return fmt.Errorf("API-Key has no authorization for the endpoint / scope %s", scope)
	default:
		return fmt.Errorf("Unexpected response status code %d", status) //nolint:staticcheck // upstream TorrentSyndikat.cs error text, kept 1:1
	}
}

// tsRow is one "rows" entry of the browse.php JSON response (cs:193-273).
type tsRow struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Category    int64    `json:"category"`
	Seeders     int64    `json:"seeders"`
	Leechers    int64    `json:"leechers"`
	Added       int64    `json:"added"`
	Size        int64    `json:"size"`
	NumFiles    int64    `json:"numfiles"`
	Snatched    int64    `json:"snatched"`
	ImdbID      *int64   `json:"imdbId"`
	TVDBID      *int64   `json:"tvdbId"`
	TMDbID      *int64   `json:"tmdbId"`
	Poster      string   `json:"poster"`
	Title       string   `json:"title"`
	TitleOrigin string   `json:"title_origin"`
	Year        *int64   `json:"year"`
	PID         *int64   `json:"pid"`
	ReleaseType string   `json:"release_type"`
	Tags        []string `json:"tags"`
	Genres      []string `json:"genres"`
}
