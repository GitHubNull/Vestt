// mechanism: json-api (username / passkey query auth)
//
// Ported from: Abstract/CouchPotatoTracker.cs
//
// CouchPotato is the shared base for the CouchPotato-protocol trackers
// (e.g. DanishBytes? no — TorrentLeech? no: the couchpotato.php endpoints
// such as the original CouchPotato tracker APIs). Concrete indexers embed
// it, set identity/Caps via indexer.Base, set Endpoint (the upstream
// constructor argument) and Bind a NewCouchPotatoConfig. Caching semantics
// come from indexer.Base.ResultsForQueryWith (upstream BaseCachingWebIndexer
// cache seam).
//
// 澄清记录:
//   - upstream checks `searchString != null`; GetQueryString() never returns
//     null (empty string at worst), so the Go port always adds the search
//     parameter, matching the observable upstream behaviour.
//   - upstream wraps the whole results loop in try/catch → OnParseError
//     (which rethrows "Parse error"); the JSON envelope parse (JObject.Parse)
//     is OUTSIDE that loop and throws "Error while parsing json: <content>"
//     directly. The Go port keeps the two error paths separate.
package shared

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"vestt/common/httpclient"
	"vestt/common/indexer"
	"vestt/common/indexer/configdata"
	"vestt/common/models"
	"vestt/common/utils/dateutil"
	"vestt/common/utils/parseutil"
)

// CouchPotatoConfig ports upstream ConfigurationDataUserPasskey
// (Username / Passkey / Instructions).
type CouchPotatoConfig struct {
	configdata.ConfigData
	Username     *configdata.StringItem
	Passkey      *configdata.StringItem
	Instructions *configdata.InfoItem
}

// NewCouchPotatoConfig mirrors the upstream constructor
// (ConfigurationDataUserPasskey(instructionMessageOptional)).
func NewCouchPotatoConfig(instructionMessage string) *CouchPotatoConfig {
	return &CouchPotatoConfig{
		ConfigData:   *configdata.NewConfigData(),
		Username:     configdata.NewStringItem("Username"),
		Passkey:      configdata.NewStringItem("Passkey"),
		Instructions: configdata.NewInfoItem("", instructionMessage),
	}
}

// CouchPotato ports upstream CouchPotatoTracker.
type CouchPotato struct {
	indexer.Base

	// Config is the typed configuration; also wired into Base via Bind.
	Config *CouchPotatoConfig

	// Endpoint is the upstream constructor's endpoint argument; APIUrl is
	// SiteLink + Endpoint.
	Endpoint string

	// GetSearchString mirrors the upstream virtual; nil = query.GetQueryString().
	GetSearchString func(q *models.TorznabQuery) string
}

// Bind wires the typed config into Base.
func (c *CouchPotato) Bind(cfg *CouchPotatoConfig) {
	c.Config = cfg
	c.Base.Config = cfg
}

func (c *CouchPotato) apiURL() string { return c.SiteLinkValue + c.Endpoint }

// getSearchString mirrors the upstream virtual GetSearchString.
func (c *CouchPotato) getSearchString(q *models.TorznabQuery) string {
	if c.GetSearchString != nil {
		return c.GetSearchString(q)
	}
	return q.GetQueryString()
}

// SearchURL builds the API URL (searchUrl + "?" + queryCollection).
func (c *CouchPotato) SearchURL(q *models.TorznabQuery) string {
	searchString := c.getSearchString(q)

	var qc QueryParams
	if strings.TrimSpace(q.ImdbID) != "" {
		qc.Add("imdbid", q.ImdbID)
	}
	qc.Add("search", searchString)
	qc.Add("passkey", c.Config.Passkey.Value)
	qc.Add("user", c.Config.Username.Value)

	return c.apiURL() + "?" + qc.GetQueryString()
}

// ApplyConfiguration mirrors upstream CouchPotatoTracker.ApplyConfiguration:
// no login call, mark configured and require a test.
func (c *CouchPotato) ApplyConfiguration(ctx context.Context, items []indexer.ConfigItem) (indexer.ConfigStatus, error) {
	if err := applyPostedItems(&c.Base, items); err != nil {
		return indexer.ConfigCompleted, err
	}
	c.SetConfigured(true)
	c.SaveConfig(ctx)
	return indexer.ConfigRequiresTesting, nil
}

// ResultsForQuery implements indexer.Indexer.
func (c *CouchPotato) ResultsForQuery(ctx context.Context, q *models.TorznabQuery, isMeta bool) (*indexer.IndexerResult, error) {
	return c.ResultsForQueryWith(ctx, q, isMeta, c.PerformQuery)
}

// Test implements indexer.Indexer (upstream TestIndexer).
func (c *CouchPotato) Test(ctx context.Context) error {
	return c.Base.Test(ctx, c.PerformQuery)
}

// PerformQuery mirrors upstream CouchPotatoTracker.PerformQuery.
func (c *CouchPotato) PerformQuery(ctx context.Context, query *models.TorznabQuery) ([]*models.ReleaseInfo, error) {
	searchURL := c.SearchURL(query)

	res, err := c.Client.Do(ctx, c.ID(), &httpclient.Request{
		URL:     searchURL,
		Cookies: c.CookieHeader(),
	})
	if err != nil {
		return nil, err
	}

	jsonResponse, err := decodeObject(res.Body)
	if err != nil {
		return nil, fmt.Errorf("Error while parsing json: %s", string(res.Body)) //nolint:staticcheck // upstream CouchPotatoTracker.cs error text, kept 1:1
	}
	if errorText := jStr(jsonResponse["error"]); errorText != "" {
		return nil, errors.New(errorText)
	}

	totalResults, _ := jInt(jsonResponse["total_results"])
	if totalResults == 0 {
		return []*models.ReleaseInfo{}, nil
	}

	results, ok := jsonResponse["results"].([]any)
	if !ok {
		return nil, parseFailure(&c.Base, string(res.Body), fmt.Errorf("missing results array"))
	}

	var releases []*models.ReleaseInfo
	for _, rawResult := range results {
		r, ok := rawResult.(map[string]any)
		if !ok {
			return nil, parseFailure(&c.Base, string(res.Body), fmt.Errorf("result is not an object"))
		}
		release, err := c.parseRelease(r)
		if err != nil {
			return nil, parseFailure(&c.Base, string(res.Body), err)
		}
		releases = append(releases, release)
	}

	return releases, nil
}

// parseRelease converts one results entry (the upstream loop body).
func (c *CouchPotato) parseRelease(r map[string]any) (*models.ReleaseInfo, error) {
	details, err := url.Parse(jStr(r["details_url"]))
	if err != nil {
		return nil, err
	}
	link, err := url.Parse(jStr(r["download_url"]))
	if err != nil {
		return nil, err
	}

	release := &models.ReleaseInfo{
		Title:   jStr(r["release_name"]),
		Details: details,
		Link:    link,
	}
	release.Guid = release.Link

	if imdb, ok := parseutil.GetImdbID(jStr(r["imdb_id"])); ok {
		release.Imdb = &imdb
	}

	dvf := 1.0
	if freeleech, ok := jBoolLoose(r["freeleech"]); ok && freeleech {
		dvf = 0
	}
	uvf := 1.0
	release.DownloadVolumeFactor = &dvf
	release.UploadVolumeFactor = &uvf

	release.Category = c.Caps.Categories.MapTrackerCatToNewznab(jStr(r["type"]))

	if size, ok := jInt(r["size"]); ok {
		v := size * 1024 * 1024
		release.Size = &v
	}

	seeders, seedersOk := jInt(r["seeders"])
	if seedersOk {
		release.Seeders = &seeders
	}
	leechers, _ := jInt(r["leechers"])
	if release.Seeders != nil {
		peers := seeders + leechers
		release.Peers = &peers
	}

	publishDate, err := dateutil.FromUnknown(jStr(r["publish_date"]), "")
	if err != nil {
		return nil, err
	}
	release.PublishDate = publishDate

	return release, nil
}
