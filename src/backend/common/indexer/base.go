package indexer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"vestt/common/httpclient"
	"vestt/common/indexer/configdata"
	"vestt/common/models"
)

// Status validity windows mirror upstream BaseIndexer:
// HealthyStatusValidity = 2 × cache TTL; ErrorStatusValidity = 10 min
// exponential backoff; MaxStatusValidity = 1 day.
const (
	defaultCacheTTL     = 2100 * time.Second
	errorStatusValidity = 10 * time.Minute
	maxStatusValidity   = 24 * time.Hour
)

// magnetTrackers mirrors MagnetUtil._Trackers (upstream list, URL-encoded).
var magnetTrackers = "tr=" + strings.Join([]string{
	"udp://tracker.opentrackr.org:1337/announce",
	"udp://open.demonii.com:1337/announce",
	"udp://open.stealth.si:80/announce",
	"udp://tracker.torrent.eu.org:451/announce",
	"udp://vito-tracker.space:6969/announce",
	"udp://vito-tracker.duckdns.org:6969/announce",
	"udp://tracker.theoks.net:6969/announce",
	"udp://tracker.srv00.com:6969/announce",
	"udp://tracker.qu.ax:6969/announce",
	"udp://tracker.corpscorp.online:80/announce",
}, "&tr=")

// ConfigSaver persists the forDisplay=false config JSON of one indexer
// (indexer_config.config_json).
type ConfigSaver func(ctx context.Context, indexerID string, configJSON []byte) error

// ResultCache is the cache seam (SPEC-07 provides the implementation);
// nil disables caching. CacheResults carries the indexer's display name and
// type so the cache view can label entries without a manager lookup
// (upstream passes the IIndexer itself).
type ResultCache interface {
	Search(indexerID string, q *models.TorznabQuery) []*models.ReleaseInfo
	CacheResults(indexerID, indexerName, indexerType string, q *models.TorznabQuery, releases []*models.ReleaseInfo)
}

// Base carries the behaviour shared by every indexer implementation
// (upstream BaseIndexer + BaseWebIndexer). Concrete indexers embed it and
// delegate through the hooks (Perform-style functions are passed explicitly
// because Go has no abstract methods).
//
// Ported from: Jackett.Common/Indexers/BaseIndexer.cs, BaseWebIndexer.cs.
type Base struct {
	// identity — populate before use
	IDValue          string
	NameValue        string
	DescriptionValue string
	SiteLinkValue    string
	DefaultSiteLink  string
	AltSiteLinks     []string
	LegacySiteLinks  []string
	LangValue        string
	TypeValue        string // public | semi-private | private
	EncodingName     string // IANA encoding label, default utf-8

	// Caps must be non-nil before queries are served.
	Caps *models.Capabilities

	// Config is a pointer to the concrete configuration struct embedding
	// configdata.ConfigData (see configdata.Collect).
	Config any

	// Client is the per-indexer HTTP stack (nil until wired by the
	// assembly / tests).
	Client *httpclient.Client

	Saver ConfigSaver
	Cache ResultCache

	Logger *slog.Logger

	// ResolveDownload optionally maps a download link to the real URL
	// (HTML sites that hide the .torrent behind a details page).
	ResolveDownload func(ctx context.Context, link *url.URL) (string, error)

	configured bool
	errorCount int
	expireAt   time.Time
}

// --- identity getters -------------------------------------------------

func (b *Base) ID() string          { return b.IDValue }
func (b *Base) Name() string        { return b.NameValue }
func (b *Base) Description() string { return b.DescriptionValue }
func (b *Base) SiteLink() string    { return b.SiteLinkValue }
func (b *Base) AlternativeSiteLinks() []string {
	return append([]string(nil), b.AltSiteLinks...)
}
func (b *Base) Language() string {
	if b.LangValue == "" {
		return "en-US"
	}
	return b.LangValue
}
func (b *Base) Type() string { return b.TypeValue }
func (b *Base) Tags() []string {
	cd := configdata.Find(b.Config)
	if cd == nil || cd.Tags == nil {
		return nil
	}
	tags := cd.Tags.SortedValues()
	return append([]string(nil), tags...)
}
func (b *Base) Capabilities() *models.Capabilities { return b.Caps }
func (b *Base) IsConfigured() bool                 { return b.configured }

// SetConfigured mirrors the upstream IsConfigured setter (ApplyConfiguration
// marks the indexer configured after a successful login+test).
func (b *Base) SetConfigured(v bool) { b.configured = v }

// LastError mirrors upstream (configData.LastError).
func (b *Base) LastError() string {
	if cd := configdata.Find(b.Config); cd != nil && cd.LastError != nil {
		return cd.LastError.Value
	}
	return ""
}

// SetLastError mirrors upstream: persist when configured and changed.
func (b *Base) SetLastError(msg string) {
	cd := configdata.Find(b.Config)
	if cd == nil || cd.LastError == nil {
		return
	}
	saveNeeded := cd.LastError.Value != msg && b.configured
	cd.LastError.Value = msg
	if saveNeeded {
		b.SaveConfig(context.Background())
	}
}

// IsHealthy / IsFailing mirror upstream (errorCount × validity windows).
func (b *Base) IsHealthy() bool {
	return b.errorCount == 0 && time.Now().Before(b.expireAt)
}
func (b *Base) IsFailing() bool {
	return b.errorCount > 0 && time.Now().Before(b.expireAt)
}

// healthyValidity mirrors HealthyStatusValidity = 2 × cache TTL.
func (b *Base) healthyValidity() time.Duration {
	if b.Cache != nil {
		if t, ok := b.Cache.(interface{ TTL() time.Duration }); ok {
			return 2 * t.TTL()
		}
	}
	return 2 * defaultCacheTTL
}

func (b *Base) logger() *slog.Logger {
	if b.Logger != nil {
		return b.Logger
	}
	return slog.Default()
}

// --- configuration lifecycle ------------------------------------------

// ConfigData returns the embedded base configuration.
func (b *Base) ConfigData() *configdata.ConfigData { return configdata.Find(b.Config) }

// PrefillDefaultSiteLink mirrors the upstream setup-form behavior: an
// unconfigured indexer's Site Link field shows the default link (the
// stored value only exists after the first LoadValuesFromJSON).
func (b *Base) PrefillDefaultSiteLink(cfg any) {
	if cd := configdata.Find(cfg); cd != nil && strings.TrimSpace(cd.SiteLink.Value) == "" {
		cd.SiteLink.Value = b.DefaultSiteLink
	}
}

// ConfigurationForSetup renders the display form (backend-api.md §2.2).
func (b *Base) ConfigurationForSetup(_ context.Context) ([]map[string]any, error) {
	b.PrefillDefaultSiteLink(b.Config)
	return configdata.ItemsJSON(b.Config)
}

// LoadValuesFromJSON mirrors upstream LoadValuesFromJson + site-link fixes.
func (b *Base) LoadValuesFromJSON(raw []byte, _ bool) error {
	if err := configdata.UnmarshalItems(b.Config, raw); err != nil {
		return err
	}
	cd := configdata.Find(b.Config)
	if cd == nil {
		return nil
	}
	if strings.TrimSpace(cd.SiteLink.Value) == "" {
		cd.SiteLink.Value = b.DefaultSiteLink
	}
	if !strings.HasSuffix(cd.SiteLink.Value, "/") {
		cd.SiteLink.Value += "/"
	}
	for _, legacy := range b.LegacySiteLinks {
		if cd.SiteLink.Value == legacy {
			b.logger().Debug(fmt.Sprintf("changing legacy site link from %s to %s", cd.SiteLink.Value, b.DefaultSiteLink))
			cd.SiteLink.Value = b.DefaultSiteLink
		}
	}
	if _, err := url.Parse(cd.SiteLink.Value); err != nil {
		return err
	}
	b.SiteLinkValue = cd.SiteLink.Value
	return nil
}

// LoadFromSavedConfiguration mirrors upstream: a JSON array marks the
// indexer configured; anything else is ignored with a warning.
func (b *Base) LoadFromSavedConfiguration(_ context.Context, raw []byte) error {
	var arr []json.RawMessage
	if err := json.Unmarshal(raw, &arr); err != nil {
		b.logger().Warn("Some of the configuration files (.json) are in the old format. Please, update Jackett.")
		return nil
	}
	if err := b.LoadValuesFromJSON(raw, true); err != nil {
		return err
	}
	b.configured = true
	return nil
}

// SaveConfig persists the forDisplay=false JSON via the saver.
func (b *Base) SaveConfig(ctx context.Context) {
	if b.Saver == nil || b.Config == nil {
		return
	}
	raw, err := configdata.MarshalItems(b.Config)
	if err != nil {
		b.logger().Error(fmt.Sprintf("[%s] SaveConfig: %v", b.IDValue, err))
		return
	}
	if err := b.Saver(ctx, b.IDValue, raw); err != nil {
		b.logger().Error(fmt.Sprintf("[%s] SaveConfig: %v", b.IDValue, err))
	}
}

// ResetBaseConfig mirrors upstream ResetBaseConfig.
func (b *Base) ResetBaseConfig() {
	if cd := configdata.Find(b.Config); cd != nil && cd.CookieHeader != nil {
		cd.CookieHeader.Value = ""
	}
	b.configured = false
	b.errorCount = 0
	b.expireAt = time.Time{}
}

// Unconfigure mirrors upstream: clear configured flag, restore default
// site link, clear cookies.
func (b *Base) Unconfigure(_ context.Context) error {
	b.configured = false
	b.SiteLinkValue = b.DefaultSiteLink
	if cd := configdata.Find(b.Config); cd != nil && cd.CookieHeader != nil {
		cd.CookieHeader.Value = ""
	}
	return nil
}

// ApplyItems loads posted ConfigItems into the matching items
// (backend-api.md §2.3). A Cookie field is applied as the item value.
func (b *Base) ApplyItems(items []ConfigItem) error {
	itemsJSON := make([]map[string]any, 0, len(items))
	for _, item := range items {
		m := map[string]any{"id": item.ID}
		if item.Value != "" {
			m["value"] = item.Value
		}
		if item.Values != nil {
			values := make([]any, len(item.Values))
			for i, v := range item.Values {
				values[i] = v
			}
			m["values"] = values
		}
		if item.Cookie != "" {
			m["value"] = item.Cookie
		}
		itemsJSON = append(itemsJSON, m)
	}
	raw, err := json.Marshal(itemsJSON)
	if err != nil {
		return err
	}
	return configdata.UnmarshalItems(b.Config, raw)
}

// --- query handling ----------------------------------------------------

// CanHandleQuery mirrors upstream (query-type checks only).
func (b *Base) CanHandleQuery(q *models.TorznabQuery) bool {
	ok, _ := b.Caps.CanHandleQuery(q)
	return ok
}

// PerformFunc runs one query against the tracker and returns raw releases.
type PerformFunc func(ctx context.Context, q *models.TorznabQuery) ([]*models.ReleaseInfo, error)

// ResultsForQueryWith is the upstream BaseIndexer.ResultsForQuery template:
// gate → cache → perform → filter → fix → dedupe → cache store, with the
// upstream health bookkeeping and IndexerError wrapping.
func (b *Base) ResultsForQueryWith(ctx context.Context, q *models.TorznabQuery, isMeta bool, perform PerformFunc) (*IndexerResult, error) {
	queryCopy := q.Clone()

	if !b.CanHandleQuery(queryCopy) {
		return &IndexerResult{Indexer: nil, Releases: []*models.ReleaseInfo{}}, nil
	}
	if ok, _ := b.supportsWithCategories(queryCopy, isMeta); !ok {
		return &IndexerResult{Releases: []*models.ReleaseInfo{}}, nil
	}

	if b.Cache != nil && queryCopy.Cache {
		if cached := b.Cache.Search(b.IDValue, queryCopy); cached != nil {
			return &IndexerResult{Indexer: nil, Releases: cached, IsFromCache: true}, nil
		}
	}

	start := time.Now()
	results, err := perform(ctx, queryCopy)
	if err != nil {
		var tooMany *httpclient.TooManyRequests
		if errors.As(err, &tooMany) {
			b.expireAt = time.Now().Add(tooMany.RetryAfter)
			return nil, &IndexerError{IndexerID: b.IDValue, Kind: KindRateLimit, Message: tooMany.Message, RetryAfter: tooMany.RetryAfter}
		}
		var siteDown *httpclient.ErrSiteDown
		if errors.As(err, &siteDown) {
			b.backoff()
			return nil, &IndexerError{IndexerID: b.IDValue, Kind: KindNetwork, Message: siteDown.Error()}
		}
		var fsErr *httpclient.ErrFlareSolverrFailed
		if errors.As(err, &fsErr) {
			b.backoff()
			return nil, &IndexerError{IndexerID: b.IDValue, Kind: KindAuth, Message: fsErr.Error()}
		}
		b.backoff()
		var ie *IndexerError
		if errors.As(err, &ie) {
			return nil, err
		}
		return nil, &IndexerError{IndexerID: b.IDValue, Kind: KindInternal, Message: rootMessage(err)}
	}

	elapsed := time.Since(start).Milliseconds()
	filtered := b.FilterResults(queryCopy, results)
	fixed := b.FixResults(filtered)

	// de-dupe by Guid (upstream GroupBy(r => r.Guid))
	seen := map[string]bool{}
	deduped := make([]*models.ReleaseInfo, 0, len(fixed))
	for _, r := range fixed {
		key := ""
		if r.Guid != nil {
			key = r.Guid.String()
		} else if r.Link != nil {
			key = "l:" + r.Link.String()
		}
		if key != "" && seen[key] {
			continue
		}
		if key != "" {
			seen[key] = true
		}
		deduped = append(deduped, r)
	}

	if b.Cache != nil {
		b.Cache.CacheResults(b.IDValue, b.NameValue, b.TypeValue, queryCopy, deduped)
	}
	b.errorCount = 0
	b.expireAt = time.Now().Add(b.healthyValidity())
	return &IndexerResult{Releases: deduped, ElapsedTime: elapsed}, nil
}

// supportsWithCategories applies the upstream CanHandleCategories gate.
func (b *Base) supportsWithCategories(q *models.TorznabQuery, isMeta bool) (bool, string) {
	if !q.HasSpecifiedCategories() {
		return true, ""
	}
	supported := b.Caps.Categories.SupportedCategories(q.Categories)
	if len(supported) == 0 {
		if !isMeta {
			b.logger().Error(fmt.Sprintf("All categories provided are unsupported in %s: %s", b.NameValue, joinInts(q.Categories)))
		}
		return false, "All categories provided are unsupported"
	}
	if len(supported) != len(q.Categories) && !isMeta {
		var unsupported []int
		for _, c := range q.Categories {
			found := false
			for _, s := range supported {
				if s == c {
					found = true
					break
				}
			}
			if !found {
				unsupported = append(unsupported, c)
			}
		}
		if len(unsupported) > 0 {
			b.logger().Warn(fmt.Sprintf("Some of the categories provided are unsupported in %s: %s", b.NameValue, joinInts(unsupported)))
		}
	}
	return true, ""
}

func (b *Base) backoff() {
	delay := errorStatusValidity
	for i := 0; i < b.errorCount; i++ {
		delay *= 2
	}
	if delay > maxStatusValidity {
		delay = maxStatusValidity
	}
	b.errorCount++
	b.expireAt = time.Now().Add(delay)
}

func rootMessage(err error) string {
	for err != nil {
		if u := errors.Unwrap(err); u == nil {
			break
		}
		err = errors.Unwrap(err)
	}
	return err.Error()
}

// FilterResults mirrors upstream: validity, category expansion, limit.
func (b *Base) FilterResults(q *models.TorznabQuery, results []*models.ReleaseInfo) []*models.ReleaseInfo {
	out := make([]*models.ReleaseInfo, 0, len(results))
	for _, r := range results {
		if b.IsValidRelease(r, q.InteractiveSearch) {
			out = append(out, r)
		}
	}
	if len(q.Categories) > 0 {
		expanded := b.Caps.Categories.ExpandTorznabQueryCategories(q, false)
		filtered := make([]*models.ReleaseInfo, 0, len(out))
		for _, r := range out {
			if len(r.Category) == 0 || intersectsAny(expanded, r.Category) {
				filtered = append(filtered, r)
			}
		}
		out = filtered
	}
	if q.Limit > 0 && len(out) > q.Limit {
		out = out[:q.Limit]
	}
	return out
}

func intersectsAny(a, b []int) bool {
	for _, x := range a {
		for _, y := range b {
			if x == y {
				return true
			}
		}
	}
	return false
}

// IsValidRelease mirrors upstream: title always required; size and
// category required unless interactive search.
func (b *Base) IsValidRelease(r *models.ReleaseInfo, interactiveSearch bool) bool {
	if strings.TrimSpace(r.Title) == "" {
		b.logger().Error(fmt.Sprintf("[%s] Invalid Release: '%s'. No title provided.", b.IDValue, detailsString(r)))
		return false
	}
	if interactiveSearch {
		return true
	}
	if r.Size == nil {
		b.logger().Warn(fmt.Sprintf("[%s] Invalid Release: '%s'. No size provided.", b.IDValue, detailsString(r)))
		return false
	}
	if len(r.Category) == 0 {
		b.logger().Warn(fmt.Sprintf("[%s] Invalid Release: '%s'. No categories provided.", b.IDValue, detailsString(r)))
		return false
	}
	return true
}

func detailsString(r *models.ReleaseInfo) string {
	if r.Details != nil {
		return r.Details.String()
	}
	return ""
}

// FixResults mirrors upstream: origin, publish-date clamp, magnet/infohash
// derivation, guid fallback.
func (b *Base) FixResults(results []*models.ReleaseInfo) []*models.ReleaseInfo {
	now := time.Now()
	for _, r := range results {
		r.Origin = models.ReleaseOrigin{ID: b.IDValue, Name: b.NameValue, Type: b.TypeValue}
		if r.PublishDate.After(now) {
			r.PublishDate = now
		}
		if r.MagnetUri == nil && strings.TrimSpace(r.InfoHash) != "" && b.TypeValue != "private" {
			if m := InfoHashToPublicMagnet(r.InfoHash, r.Title); m != nil {
				r.MagnetUri = m
			}
		}
		if r.MagnetUri != nil && strings.TrimSpace(r.InfoHash) == "" {
			r.InfoHash = MagnetToInfoHash(r.MagnetUri)
		}
		if r.Guid == nil {
			switch {
			case r.Link != nil:
				r.Guid = r.Link
			case r.MagnetUri != nil:
				r.Guid = r.MagnetUri
			case r.Details != nil:
				r.Guid = r.Details
			}
		}
	}
	return results
}

// InfoHashToPublicMagnet mirrors MagnetUtil.InfoHashToPublicMagnet.
func InfoHashToPublicMagnet(infoHash, title string) *url.URL {
	if strings.TrimSpace(infoHash) == "" || strings.TrimSpace(title) == "" {
		return nil
	}
	u, err := url.Parse(fmt.Sprintf("magnet:?xt=urn:btih:%s&dn=%s&%s", infoHash, url.QueryEscape(title), magnetTrackers))
	if err != nil {
		return nil
	}
	return u
}

// MagnetToInfoHash mirrors MagnetUtil.MagnetToInfoHash.
func MagnetToInfoHash(magnet *url.URL) string {
	xt := magnet.Query().Get("xt")
	if xt == "" {
		return ""
	}
	parts := strings.Split(xt, ":")
	return parts[len(parts)-1]
}

// --- test + download ----------------------------------------------------

// TestQuery is the upstream TestIndexer query.
func TestQuery() *models.TorznabQuery {
	return &models.TorznabQuery{QueryType: "search", SearchTerm: "", IsTest: true}
}

// Test mirrors upstream TestIndexer: run an empty search and require at
// least one release.
func (b *Base) Test(ctx context.Context, perform PerformFunc) error {
	result, err := b.ResultsForQueryWith(ctx, TestQuery(), false, perform)
	if err != nil {
		return err
	}
	if len(result.Releases) == 0 {
		return fmt.Errorf("Test search in %s => Found no results while trying to browse this tracker. This may be an issue with the indexer, or other indexer settings such as search freeleech only etc.", b.NameValue) //nolint:staticcheck // upstream BaseIndexer.cs error text, kept 1:1
	}
	b.logger().Info(fmt.Sprintf("Test search in %s => Found %d releases [%dms]", b.NameValue, len(result.Releases), result.ElapsedTime))
	return nil
}

// Download mirrors upstream BaseWebIndexer.Download: magnet links are
// returned as-is; other links go through the client stack (with the
// ResolveDownload hook for HTML-site resolution), with ()' URL escaping.
func (b *Base) Download(ctx context.Context, link *url.URL) ([]byte, error) {
	return b.DownloadWith(ctx, link, http.MethodGet, "", nil)
}

// DownloadWith mirrors the upstream BaseIndexer.Download(uri, method,
// referer, headers) overload used by the cardigann engine.
func (b *Base) DownloadWith(ctx context.Context, link *url.URL, method, referer string, headers map[string]string) ([]byte, error) {
	if link.Scheme == "magnet" {
		return []byte(link.String()), nil
	}
	target := link.String()
	if b.ResolveDownload != nil {
		resolved, err := b.ResolveDownload(ctx, link)
		if err != nil {
			return nil, err
		}
		target = resolved
	}
	// upstream: extra escaping for HD-Torrents
	target = strings.ReplaceAll(target, "(", "%28")
	target = strings.ReplaceAll(target, ")", "%29")
	target = strings.ReplaceAll(target, "'", "%27")

	if b.Client == nil {
		return nil, errors.New("http client not configured")
	}
	result, err := b.Client.Do(ctx, b.IDValue, &httpclient.Request{
		Method:         method,
		URL:            target,
		Referer:        referer,
		Headers:        headers,
		Cookies:        b.CookieHeader(),
		EmulateBrowser: httpclient.Bool(true),
	})
	if err != nil {
		return nil, err
	}
	if result.StatusCode != http.StatusOK && result.StatusCode != http.StatusContinue && result.StatusCode != http.StatusPartialContent {
		return nil, fmt.Errorf("Remote server returned %d", result.StatusCode) //nolint:staticcheck // upstream BaseIndexer.cs error text, kept 1:1
	}
	return result.Body, nil
}

// DownloadImage mirrors upstream BaseIndexer.DownloadImage: a GET with the
// site link as referer, following redirects, with the raw response
// (status + headers + body) handed back for the ImageController status
// passthrough. UncleanLink's downloadUrlBase rewriting is not ported (no
// Go indexer sets a download base — see SPEC-07 澄清记录).
func (b *Base) DownloadImage(ctx context.Context, link *url.URL) (*httpclient.Result, error) {
	if b.Client == nil {
		return nil, errors.New("http client not configured")
	}
	return b.Client.Do(ctx, b.IDValue, &httpclient.Request{
		Method:         http.MethodGet,
		URL:            link.String(),
		Referer:        b.SiteLinkValue,
		Cookies:        b.CookieHeader(),
		EmulateBrowser: httpclient.Bool(true),
	})
}

// CookieHeader returns the configured cookie header.
func (b *Base) CookieHeader() string {
	if cd := configdata.Find(b.Config); cd != nil && cd.CookieHeader != nil {
		return cd.CookieHeader.Value
	}
	return ""
}

// SetCookieHeader replaces the configured cookie header.
func (b *Base) SetCookieHeader(v string) {
	if cd := configdata.Find(b.Config); cd != nil && cd.CookieHeader != nil {
		cd.CookieHeader.Value = v
	}
}

func joinInts(ints []int) string {
	parts := make([]string, len(ints))
	for i, n := range ints {
		parts[i] = fmt.Sprintf("%d", n)
	}
	return strings.Join(parts, ",")
}
