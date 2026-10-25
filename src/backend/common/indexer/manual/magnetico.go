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

	"vestt/common/httpclient"
	"vestt/common/indexer"
	"vestt/common/indexer/configdata"
	"vestt/common/indexer/shared"
	"vestt/common/models"
	"vestt/common/services"
	"vestt/common/utils/dateutil"
)

// Magnetico ports Jackett.Common/Indexers/Definitions/Magnetico.cs.
//
// mechanism: json-api (HTTP Basic auth against the local magneticow
// api/v0.1/torrents endpoint, JSON array responses)
//
// Ported from: Definitions/Magnetico.cs.
type Magnetico struct {
	indexer.Base
	cfg *magneticoConfig
}

// magneticoConfig ports upstream ConfigurationDataBasicLogin (Username +
// Password + Instructions) plus the dynamic "sort"/"order" single selects
// (declared as fields instead of AddDynamic — same registration and
// ordering).
type magneticoConfig struct {
	configdata.ConfigData
	Username     *configdata.StringItem
	Password     *configdata.StringItem
	Instructions *configdata.InfoItem
	Sort         *configdata.SelectItem
	Order        *configdata.SelectItem
}

const magneticoInstructions = "Configure the URL, username and password from your local magneticow.<br>" +
	"Default credentials are: username=username, password=password.<br>" +
	"If you have many torrents, it is recommended to use PostgreSQL database to make queries faster. With SQLite, timeouts may occur."

func newMagneticoConfig() *magneticoConfig {
	return &magneticoConfig{
		ConfigData:   *configdata.NewConfigData(),
		Username:     configdata.NewStringItem("Username"),
		Password:     configdata.NewStringItem("Password"),
		Instructions: configdata.NewInfoItem("", magneticoInstructions),
		Sort: configdata.NewSelectItem("Sort requested from site", []configdata.Option{
			{Value: "DISCOVERED_ON", Label: "discovered"},
			{Value: "TOTAL_SIZE", Label: "size"},
			{Value: "N_FILES", Label: "files"},
			{Value: "RELEVANCE", Label: "relevance"},
		}),
		Order: configdata.NewSelectItem("Order requested from site", []configdata.Option{
			{Value: "false", Label: "desc"},
			{Value: "true", Label: "asc"},
		}),
	}
}

// NewMagnetico builds the indexer (services.Factory shape).
func NewMagnetico(deps services.Deps) (indexer.Indexer, error) {
	cfg := newMagneticoConfig()
	// upstream: sort.Value = "discovered"; order.Value = "false".
	cfg.Sort.Value = "discovered"
	cfg.Order.Value = "false"
	m := &Magnetico{cfg: cfg}
	m.Base = indexer.Base{
		IDValue:          "magnetico",
		NameValue:        "Magnetico (Local DHT)",
		DescriptionValue: "Magnetico is a self-hosted BitTorrent DHT search engine",
		SiteLinkValue:    "http://127.0.0.1:8080/",
		DefaultSiteLink:  "http://127.0.0.1:8080/",
		LangValue:        "en-US",
		TypeValue:        "semi-private",
		Caps:             magneticoCaps(),
		Config:           cfg,
		Client:           deps.Client,
		Saver:            deps.Saver,
		Cache:            deps.Cache,
	}
	return m, nil
}

func magneticoCaps() *models.Capabilities {
	caps := models.NewCapabilities()
	caps.TVSearchParams = []string{models.TVParamQ, models.TVParamSeason, models.TVParamEp}
	caps.MovieSearchParams = []string{models.MovieParamQ}
	caps.MusicSearchParams = []string{models.MusicParamQ}
	caps.BookSearchParams = []string{models.BookParamQ}
	caps.Categories.AddCategoryMapping("1", models.CatOther, "")
	return caps
}

func (m *Magnetico) searchURL() string   { return m.SiteLink() + "api/v0.1/torrents" }
func (m *Magnetico) torrentsURL() string { return m.SiteLink() + "torrents" }

// ApplyConfiguration ports upstream: load values, run an empty query (throws
// on error), mark configured and save.
func (m *Magnetico) ApplyConfiguration(ctx context.Context, items []indexer.ConfigItem) (indexer.ConfigStatus, error) {
	if err := m.ApplyItems(items); err != nil {
		return indexer.ConfigCompleted, err
	}
	if err := m.LoadValuesFromJSON(nil, false); err != nil {
		return indexer.ConfigCompleted, err
	}
	m.SetConfigured(false)
	if _, err := m.perform(ctx, models.NewTorznabQuery()); err != nil {
		return indexer.ConfigCompleted, err
	}
	m.SetConfigured(true)
	m.SaveConfig(ctx)
	return indexer.ConfigCompleted, nil
}

func (m *Magnetico) ResultsForQuery(ctx context.Context, q *models.TorznabQuery, isMeta bool) (*indexer.IndexerResult, error) {
	return m.ResultsForQueryWith(ctx, q, isMeta, m.perform)
}

func (m *Magnetico) Test(ctx context.Context) error {
	return m.Base.Test(ctx, m.perform)
}

// perform ports upstream Magnetico.PerformQuery: query/orderBy/ascending/limit
// params, Basic auth header, "Error code: <status>" on non-OK, per-torrent
// mapping inside a parse-error try/catch.
func (m *Magnetico) perform(ctx context.Context, query *models.TorznabQuery) ([]*models.ReleaseInfo, error) {
	if m.Client == nil {
		return nil, errors.New("http client not configured")
	}

	qc := shared.QueryParams{}
	qc.Add("query", query.GetQueryString())
	qc.Add("orderBy", m.cfg.Sort.Value)
	qc.Add("ascending", m.cfg.Order.Value)
	qc.Add("limit", "100")

	res, err := m.Client.Do(ctx, m.ID(), &httpclient.Request{
		URL:     m.searchURL() + "?" + qc.GetQueryString(),
		Headers: m.authHeaders(),
		Cookies: m.CookieHeader(),
	})
	if err != nil {
		return nil, err
	}
	if res.StatusCode != http.StatusOK {
		// upstream: Exception($"Error code: {results.Status}") with the
		// HttpStatusCode enum name.
		return nil, fmt.Errorf("Error code: %s", shared.StatusName(res.StatusCode)) //nolint:staticcheck // upstream Magnetico.cs error text, kept 1:1
	}

	var torrents []magneticoTorrent
	if err := json.Unmarshal(res.Body, &torrents); err != nil {
		return nil, m.parseFailure(string(res.Body), err)
	}

	var releases []*models.ReleaseInfo
	for _, t := range torrents {
		details, err := url.Parse(fmt.Sprintf("%s/%s", m.torrentsURL(), t.InfoHash))
		if err != nil {
			return nil, m.parseFailure(string(res.Body), err)
		}
		publishDate := dateutil.UnixTimestampToDateTime(t.DiscoveredOn)

		seeders := int64(1)
		peers := int64(1)
		releases = append(releases, &models.ReleaseInfo{
			Title:                t.Name,
			Details:              details,
			Guid:                 details,
			InfoHash:             t.InfoHash,
			PublishDate:          publishDate,
			Category:             []int{models.CatOther.ID},
			Size:                 &t.Size,
			Files:                &t.NFiles,
			Seeders:              &seeders,
			Peers:                &peers,
			DownloadVolumeFactor: manualF64(0),
			UploadVolumeFactor:   manualF64(1),
		})
	}
	return releases, nil
}

// authHeaders ports upstream GetAuthorizationHeaders: Basic auth over
// "username:password". Upstream encodes via ISO-8859-1 (unmappable runes
// become '?' there); the Go port sends the raw UTF-8 bytes, identical for
// the ASCII credentials magneticow uses by default.
func (m *Magnetico) authHeaders() map[string]string {
	encoded := base64.StdEncoding.EncodeToString([]byte(m.cfg.Username.Value + ":" + m.cfg.Password.Value))
	return map[string]string{"Authorization": "Basic " + encoded}
}

// log is a nil-safe logger (Base.Logger may be nil).
func (m *Magnetico) log() *slog.Logger {
	if m.Logger != nil {
		return m.Logger
	}
	return slog.Default()
}

// parseFailure mirrors upstream OnParseError: log the response dump and
// surface Exception("Parse error", ex).
func (m *Magnetico) parseFailure(dump string, err error) error {
	m.log().Error(fmt.Sprintf("Error while parsing result: %v\n%s", err, dump))
	return &indexer.IndexerError{IndexerID: m.ID(), Kind: indexer.KindParse, Message: "Parse error"}
}

// --- upstream DTOs ----------------------------------------------------------

type magneticoTorrent struct {
	InfoHash     string `json:"infoHash"`
	ID           int64  `json:"id"`
	Name         string `json:"name"`
	Size         int64  `json:"size"`
	DiscoveredOn int64  `json:"discoveredOn"`
	NFiles       int64  `json:"nFiles"`
	Relevance    int64  `json:"relevance"`
}
