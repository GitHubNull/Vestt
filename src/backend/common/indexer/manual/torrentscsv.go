// mechanism: json-api (self-hostable torrents-csv service/search endpoint,
// magnet links generated from the infohash, no authentication)
//
// Ported from: Definitions/TorrentsCSV.cs.
//
// 澄清记录:
//   - torrents-csv is self-hostable, so the Site Link config item is the
//     URL every request is built from; the port reads the config item
//     directly (falling back to the base SiteLink before the first config
//     load) instead of assuming the default domain.
//   - upstream has no HTTP status check at all (TorrentsCSV.cs:122-163): a
//     failing body simply fails JSON parsing → OnParseError. Vestt SPEC-09
//     §4 maps an explicit 401/403 to KindAuth before the parse attempt;
//     every other failing body still surfaces as KindParse, like upstream.
//   - upstream Details is interpolated raw, $"search?q={title}"
//     (TorrentsCSV.cs:145); the Go port url.Parses it (Go tolerates spaces
//     in the query where .NET escapes them).
//   - the "empty search" branch returns a synthetic "[NOT IMPLEMENTED]"
//     release (TorrentsCSV.cs:88-107), so the upstream self-test succeeds
//     without any network request; replicated verbatim.
//   - a null entry inside the torrents array throws "Error: No data
//     returned!" from inside the parse loop (TorrentsCSV.cs:130-133),
//     which the outer catch turns into OnParseError — replicated.
package manual

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"sort"
	"strings"

	"vestt/common/httpclient"
	"vestt/common/indexer"
	"vestt/common/indexer/configdata"
	"vestt/common/indexer/shared"
	"vestt/common/models"
	"vestt/common/services"
	"vestt/common/utils/dateutil"
)

// TorrentsCSV ports Jackett.Common/Indexers/Definitions/TorrentsCSV.cs.
type TorrentsCSV struct {
	indexer.Base
	cfg *torrentsCSVConfig
}

// torrentsCSVConfig ports the upstream base ConfigurationData. The Site
// Link item (embedded ConfigData.SiteLink) selects the self-hosted
// torrents-csv instance and is what every request URL is built from.
type torrentsCSVConfig struct {
	configdata.ConfigData
}

func newTorrentsCSVConfig() *torrentsCSVConfig {
	return &torrentsCSVConfig{ConfigData: *configdata.NewConfigData()}
}

// NewTorrentsCSV builds the indexer (services.Factory shape).
func NewTorrentsCSV(deps services.Deps) (indexer.Indexer, error) {
	t := &TorrentsCSV{cfg: newTorrentsCSVConfig()}
	t.Base = indexer.Base{
		IDValue:          "torrentscsv",
		NameValue:        "Torrents.csv",
		DescriptionValue: "Torrents.csv is a self-hostable, open source torrent search engine and database",
		SiteLinkValue:    "https://torrents-csv.com/",
		DefaultSiteLink:  "https://torrents-csv.com/",
		LegacySiteLinks:  []string{"https://torrents-csv.ml/"},
		LangValue:        "en-US",
		TypeValue:        "public",
		Caps:             torrentsCSVCaps(),
		Config:           t.cfg,
		Client:           deps.Client,
		Saver:            deps.Saver,
		Cache:            deps.Cache,
	}
	return t, nil
}

func torrentsCSVCaps() *models.Capabilities {
	caps := models.NewCapabilities()
	caps.TVSearchParams = []string{models.TVParamQ, models.TVParamSeason, models.TVParamEp}
	caps.MovieSearchParams = []string{models.MovieParamQ}

	// torrents.csv doesn't return categories
	caps.Categories.AddCategoryMapping("1", models.CatOther, "")
	return caps
}

// baseURL is the configured Site Link (self-hosted instance); before the
// first configuration load it falls back to the base default.
func (t *TorrentsCSV) baseURL() string {
	if v := strings.TrimSpace(t.cfg.SiteLink.Value); v != "" {
		return v
	}
	return t.SiteLink()
}

func (t *TorrentsCSV) searchEndpoint() string { return t.baseURL() + "service/search" }

// ApplyConfiguration ports upstream TorrentsCSV.ApplyConfiguration
// (TorrentsCSV.cs:71-80): load values, run an empty query, require results.
func (t *TorrentsCSV) ApplyConfiguration(ctx context.Context, items []indexer.ConfigItem) (indexer.ConfigStatus, error) {
	if err := t.ApplyItems(items); err != nil {
		return indexer.ConfigCompleted, t.torrentsCSVConfigError(err)
	}
	if err := t.LoadValuesFromJSON(nil, false); err != nil {
		return indexer.ConfigCompleted, t.torrentsCSVConfigError(err)
	}

	releases, err := t.PerformQuery(ctx, models.NewTorznabQuery())
	if err != nil {
		return indexer.ConfigCompleted, t.torrentsCSVConfigError(err)
	}
	if len(releases) == 0 {
		return indexer.ConfigCompleted, t.torrentsCSVConfigError(errors.New("Error: 0 results found!")) //nolint:staticcheck // upstream TorrentsCSV.cs error text, kept 1:1
	}

	t.SetConfigured(true)
	t.SaveConfig(ctx)
	return indexer.ConfigCompleted, nil
}

// ResultsForQuery implements indexer.Indexer.
func (t *TorrentsCSV) ResultsForQuery(ctx context.Context, q *models.TorznabQuery, isMeta bool) (*indexer.IndexerResult, error) {
	return t.ResultsForQueryWith(ctx, q, isMeta, t.PerformQuery)
}

// Test implements indexer.Indexer (upstream TestIndexer).
func (t *TorrentsCSV) Test(ctx context.Context) error {
	return t.Base.Test(ctx, t.PerformQuery)
}

// torrentsCSVResponse is the service/search envelope (upstream
// TorrentsCSVResponse).
type torrentsCSVResponse struct {
	Torrents []*torrentsCSVTorrent `json:"torrents"`
}

// torrentsCSVTorrent is one search row (upstream TorrentsCSVTorrent).
type torrentsCSVTorrent struct {
	InfoHash    string `json:"infohash"`
	Name        string `json:"name"`
	SizeBytes   int64  `json:"size_bytes"`
	CreatedUnix int64  `json:"created_unix"`
	Leechers    *int64 `json:"leechers"`
	Seeders     *int64 `json:"seeders"`
	Completed   *int64 `json:"completed"`
}

// PerformQuery ports upstream TorrentsCSV.PerformQuery (TorrentsCSV.cs:82-169).
func (t *TorrentsCSV) PerformQuery(ctx context.Context, query *models.TorznabQuery) ([]*models.ReleaseInfo, error) {
	if t.Client == nil {
		return nil, errors.New("http client not configured")
	}
	searchString := query.GetQueryString()

	// not supported
	if strings.TrimSpace(searchString) == "" {
		siteLink, err := url.Parse(t.baseURL())
		if err != nil {
			return nil, t.torrentsCSVParseFailure(t.baseURL(), err)
		}
		magnet, err := url.Parse("magnet:?xt=urn:btih:3333333333333333333333333333333333333333") // unknown torrent
		if err != nil {
			return nil, t.torrentsCSVParseFailure(t.baseURL(), err)
		}
		var size, grabs, seeders, peers int64
		return []*models.ReleaseInfo{{
			Title:                "[NOT IMPLEMENTED] Empty search is unsupported in this indexer",
			Guid:                 siteLink,
			Details:              siteLink,
			MagnetUri:            magnet,
			Category:             []int{models.CatOther.ID},
			Size:                 &size,
			Grabs:                &grabs,
			Seeders:              &seeders,
			Peers:                &peers,
			DownloadVolumeFactor: batch3F64(0),
			UploadVolumeFactor:   batch3F64(1),
		}}, nil
	}

	// search needs at least 3 characters
	if len(searchString) < 3 {
		return []*models.ReleaseInfo{}, nil
	}

	qc := shared.QueryParams{}
	qc.Add("size", "100")
	qc.Add("q", searchString)

	searchURL := t.searchEndpoint() + "?" + qc.GetQueryString()
	res, err := t.Client.Do(ctx, t.ID(), &httpclient.Request{
		URL:     searchURL,
		Cookies: t.CookieHeader(),
	})
	if err != nil {
		return nil, err
	}

	// Vestt SPEC-09 §4: explicit auth failure on the public API.
	if res.StatusCode == http.StatusUnauthorized || res.StatusCode == http.StatusForbidden {
		return nil, &indexer.IndexerError{
			IndexerID: t.ID(),
			Kind:      indexer.KindAuth,
			Message:   fmt.Sprintf("Remote server returned %d", res.StatusCode),
		}
	}

	var jsonResponse torrentsCSVResponse
	if err := json.Unmarshal(res.Body, &jsonResponse); err != nil {
		return nil, t.torrentsCSVParseFailure(string(res.Body), err)
	}

	var releases []*models.ReleaseInfo
	for _, torrent := range jsonResponse.Torrents {
		if torrent == nil {
			return nil, t.torrentsCSVParseFailure(string(res.Body), errors.New("Error: No data returned!")) //nolint:staticcheck // upstream TorrentsCSV.cs error text, kept 1:1
		}

		infoHash := torrent.InfoHash
		title := torrent.Name
		var seeders, leechers, grabs int64
		if torrent.Seeders != nil {
			seeders = *torrent.Seeders
		}
		if torrent.Leechers != nil {
			leechers = *torrent.Leechers
		}
		if torrent.Completed != nil {
			grabs = *torrent.Completed
		}
		publishDate := dateutil.UnixTimestampToDateTime(torrent.CreatedUnix)

		guid, err := url.Parse(fmt.Sprintf("magnet:?xt=urn:btih:%s", infoHash))
		if err != nil {
			return nil, t.torrentsCSVParseFailure(string(res.Body), err)
		}
		details, err := url.Parse(fmt.Sprintf("%ssearch?q=%s", t.baseURL(), title)) // there is no details link
		if err != nil {
			return nil, t.torrentsCSVParseFailure(string(res.Body), err)
		}

		size := torrent.SizeBytes
		peers := leechers + seeders
		releases = append(releases, &models.ReleaseInfo{
			Guid:                 guid,
			Details:              details,
			Title:                title,
			InfoHash:             infoHash, // magnet link is auto generated from infohash
			Category:             []int{models.CatOther.ID},
			PublishDate:          publishDate,
			Size:                 &size,
			Grabs:                &grabs,
			Seeders:              &seeders,
			Peers:                &peers,
			DownloadVolumeFactor: batch3F64(0),
			UploadVolumeFactor:   batch3F64(1),
		})
	}

	// OrderByDescending(o => o.PublishDate)
	sort.SliceStable(releases, func(i, j int) bool {
		return releases[i].PublishDate.After(releases[j].PublishDate)
	})

	return releases, nil
}

// torrentsCSVConfigError maps configuration/self-test failures to KindConfig.
func (t *TorrentsCSV) torrentsCSVConfigError(err error) error {
	var ie *indexer.IndexerError
	if errors.As(err, &ie) {
		return err
	}
	return &indexer.IndexerError{IndexerID: t.ID(), Kind: indexer.KindConfig, Message: err.Error()}
}

// torrentsCSVParseFailure mirrors upstream OnParseError.
func (t *TorrentsCSV) torrentsCSVParseFailure(dump string, err error) error {
	t.log().Error(fmt.Sprintf("Error while parsing result: %v\n%s", err, dump))
	return &indexer.IndexerError{IndexerID: t.ID(), Kind: indexer.KindParse, Message: "Parse error"}
}

// log is a nil-safe logger (Base.Logger may be nil).
func (t *TorrentsCSV) log() *slog.Logger {
	if t.Logger != nil {
		return t.Logger
	}
	return slog.Default()
}
