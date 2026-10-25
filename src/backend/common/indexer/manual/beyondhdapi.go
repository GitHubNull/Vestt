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
	"strconv"
	"strings"
	"time"

	"vestt/common/httpclient"
	"vestt/common/indexer"
	"vestt/common/indexer/configdata"
	"vestt/common/models"
	"vestt/common/services"
	"vestt/common/utils/parseutil"
)

// BeyondHDAPI ports Jackett.Common/Indexers/Definitions/BeyondHDAPI.cs.
//
// mechanism: json-api
//
// Ported from: Definitions/BeyondHDAPI.cs,
// Models/IndexerConfig/Bespoke/ConfigurationDataBeyondHDApi.cs.
//
// 澄清记录:
//   - apiBase is an upstream const ("https://beyond-hd.me/api/torrents/");
//     here it is an overridable unexported field (default identical) so the
//     httptest fixtures can point the indexer at a fake site.
type BeyondHDAPI struct {
	indexer.Base
	cfg     *beyondHDAPIConfig
	apiBase string // upstream const ApiBase; test seam
}

type beyondHDAPIConfig struct {
	configdata.ConfigData
	ApiKey                   *configdata.StringItem
	RSSKey                   *configdata.StringItem
	Instructions             *configdata.InfoItem
	AddHybridFeaturesToTitle *configdata.BoolItem
	FilterFreeleech          *configdata.BoolItem
	FilterLimited            *configdata.BoolItem
	FilterRefund             *configdata.BoolItem
	FilterRewind             *configdata.BoolItem
	SearchTypes              *configdata.MultiSelectItem
}

const beyondHDAPIInstructions = "Find the API and RSS keys under your security settings (your profile picture -> my security)"

func newBeyondHDAPIConfig() *beyondHDAPIConfig {
	return &beyondHDAPIConfig{
		ConfigData:               *configdata.NewConfigData(),
		ApiKey:                   configdata.NewStringItem("API Key"),
		RSSKey:                   configdata.NewStringItem("RSS Key"),
		Instructions:             configdata.NewInfoItem("", beyondHDAPIInstructions),
		AddHybridFeaturesToTitle: configdata.NewBoolItem("Include DV/HDR10 in title when release has multiple HDR formats."),
		FilterFreeleech:          configdata.NewBoolItem("Filter freeleech"),
		FilterLimited:            configdata.NewBoolItem("Filter freeleech (limited UL)"),
		FilterRefund:             configdata.NewBoolItem("Filter refund"),
		FilterRewind:             configdata.NewBoolItem("Filter rewind"),
		SearchTypes: configdata.NewMultiSelectItem("Select the types of releases that you are interested in. Leave empty for all.", []configdata.Option{
			{Value: "UHD 100", Label: "UHD 100"},
			{Value: "UHD 66", Label: "UHD 66"},
			{Value: "UHD 50", Label: "UHD 50"},
			{Value: "UHD Remux", Label: "UHD Remux"},
			{Value: "BD 50", Label: "BD 50"},
			{Value: "BD 25", Label: "BD 25"},
			{Value: "BD Remux", Label: "BD Remux"},
			{Value: "2160p", Label: "2160p"},
			{Value: "1080p", Label: "1080p"},
			{Value: "1080i", Label: "1080i"},
			{Value: "720p", Label: "720p"},
			{Value: "576p", Label: "576p"},
			{Value: "540p", Label: "540p"},
			{Value: "DVD 9", Label: "DVD 9"},
			{Value: "DVD 5", Label: "DVD 5"},
			{Value: "DVD Remux", Label: "DVD Remux"},
			{Value: "480p", Label: "480p"},
			{Value: "Other", Label: "Other"},
		}),
	}
}

const (
	beyondHDAPIKeyLength = 32
	beyondHDRSSKeyLength = 32
)

// NewBeyondHDAPI builds the indexer (services.Factory shape).
func NewBeyondHDAPI(deps services.Deps) (indexer.Indexer, error) {
	b := &BeyondHDAPI{cfg: newBeyondHDAPIConfig(), apiBase: "https://beyond-hd.me/api/torrents/"}
	b.Base = indexer.Base{
		IDValue:          "beyond-hd-api",
		NameValue:        "Beyond-HD (API)",
		DescriptionValue: "Beyond-HD is a Private site. Without BeyondHD, your HDTV is just a TV",
		SiteLinkValue:    "https://beyond-hd.me/",
		DefaultSiteLink:  "https://beyond-hd.me/",
		LangValue:        "en-US",
		TypeValue:        "private",
		Caps:             beyondHDAPICaps(),
		Config:           b.cfg,
		Client:           deps.Client,
		Saver:            deps.Saver,
		Cache:            deps.Cache,
	}
	return b, nil
}

func beyondHDAPICaps() *models.Capabilities {
	caps := models.NewCapabilities()
	def, max := 100, 100
	caps.LimitsDefault = &def
	caps.LimitsMax = &max
	caps.TVSearchParams = []string{models.TVParamQ, models.TVParamSeason, models.TVParamEp, models.TVParamImdbID, models.TVParamTmdbID}
	caps.MovieSearchParams = []string{models.MovieParamQ, models.MovieParamImdbID, models.MovieParamTmdbID}
	c := caps.Categories
	c.AddCategoryMapping("1", models.CatMovies, "Movies")
	c.AddCategoryMapping("2", models.CatTV, "TV")
	return caps
}

// ApplyConfiguration ports upstream: validate key lengths, test-query the
// API, mark configured and save (upstream wraps failures in
// ExceptionWithConfigData — the message is preserved verbatim).
func (b *BeyondHDAPI) ApplyConfiguration(ctx context.Context, items []indexer.ConfigItem) (indexer.ConfigStatus, error) {
	if err := b.ApplyItems(items); err != nil {
		return indexer.ConfigCompleted, err
	}
	if err := b.LoadValuesFromJSON(nil, false); err != nil {
		return indexer.ConfigCompleted, err
	}
	if strings.TrimSpace(b.cfg.ApiKey.Value) == "" {
		return indexer.ConfigCompleted, errors.New("Missing API Key.") //nolint:staticcheck // upstream BeyondHDAPI.cs:91 error text, kept 1:1
	}
	if len(b.cfg.ApiKey.Value) != beyondHDAPIKeyLength {
		return indexer.ConfigCompleted, fmt.Errorf("Invalid API Key configured: expected length: %d, got %d", beyondHDAPIKeyLength, len(b.cfg.ApiKey.Value)) //nolint:staticcheck // upstream BeyondHDAPI.cs error text, kept 1:1
	}
	if strings.TrimSpace(b.cfg.RSSKey.Value) == "" {
		return indexer.ConfigCompleted, errors.New("Missing RSS Key.") //nolint:staticcheck // upstream BeyondHDAPI.cs error text, kept 1:1
	}
	if len(b.cfg.RSSKey.Value) != beyondHDRSSKeyLength {
		return indexer.ConfigCompleted, fmt.Errorf("Invalid RSS Key configured: expected length: %d, got %d", beyondHDRSSKeyLength, len(b.cfg.RSSKey.Value)) //nolint:staticcheck // upstream BeyondHDAPI.cs error text, kept 1:1
	}
	b.SetConfigured(false)
	results, err := b.perform(ctx, models.NewTorznabQuery())
	if err != nil {
		return indexer.ConfigCompleted, err
	}
	if len(results) == 0 {
		return indexer.ConfigCompleted, errors.New("Testing returned no results!") //nolint:staticcheck // upstream BeyondHDAPI.cs error text, kept 1:1
	}
	b.SetConfigured(true)
	b.SaveConfig(ctx)
	return indexer.ConfigCompleted, nil
}

func (b *BeyondHDAPI) ResultsForQuery(ctx context.Context, q *models.TorznabQuery, isMeta bool) (*indexer.IndexerResult, error) {
	return b.ResultsForQueryWith(ctx, q, isMeta, b.perform)
}

func (b *BeyondHDAPI) Test(ctx context.Context) error {
	return b.Base.Test(ctx, b.perform)
}

func (b *BeyondHDAPI) perform(ctx context.Context, query *models.TorznabQuery) ([]*models.ReleaseInfo, error) {
	body, err := b.buildPostData(query)
	if err != nil {
		return nil, err
	}
	target := b.apiBase + b.cfg.ApiKey.Value
	if b.Client == nil {
		return nil, errors.New("http client not configured")
	}
	res, err := b.Client.Do(ctx, b.ID(), &httpclient.Request{
		Method: http.MethodPost,
		URL:    target,
		Headers: map[string]string{
			"Accept":       "application/json",
			"Content-Type": "application/json",
		},
		RawBody: body,
	})
	if err != nil {
		return nil, err
	}
	return b.parseResponse(res.Body)
}

// buildPostData ports BeyondHDAPIRequestGenerator.GetSearchRequests. The
// upstream Dictionary preserves insertion order through Newtonsoft
// serialization; the ordered field list reproduces the exact wire format.
func (b *BeyondHDAPI) buildPostData(query *models.TorznabQuery) (string, error) {
	searchTerm := query.GetQueryString()

	if query.IsTVSearch() && query.Season != nil && *query.Season > 0 && strings.TrimSpace(query.Episode) != "" {
		if showDate, ok := parseBHDAirDate(*query.Season, query.Episode); ok {
			searchTerm = query.SanitizedSearchTerm() + " " + showDate.Format("2006-01-02")
		}
	}

	fields := []bhdPostField{
		{key: "action", raw: `"search"`},
		{key: "rsskey", raw: jsonQuote(b.cfg.RSSKey.Value)},
		{key: "search", raw: jsonQuote(strings.TrimSpace(searchTerm))},
	}
	if b.cfg.FilterFreeleech.Value {
		fields = append(fields, bhdPostField{key: "freeleech", raw: "1"})
	}
	if b.cfg.FilterLimited.Value {
		fields = append(fields, bhdPostField{key: "limited", raw: "1"})
	}
	if b.cfg.FilterRefund.Value {
		fields = append(fields, bhdPostField{key: "refund", raw: "1"})
	}
	if b.cfg.FilterRewind.Value {
		fields = append(fields, bhdPostField{key: "rewind", raw: "1"})
	}
	if len(b.cfg.SearchTypes.Values) > 0 {
		raw, err := json.Marshal(b.cfg.SearchTypes.Values)
		if err != nil {
			return "", err
		}
		fields = append(fields, bhdPostField{key: "types", raw: string(raw)})
	}

	categories := b.Caps.Categories.MapTorznabCapsToTrackers(query, false)
	if len(categories) > 0 {
		ints := make([]int, 0, len(categories))
		for _, c := range categories {
			n, err := strconv.Atoi(c)
			if err != nil {
				return "", err
			}
			ints = append(ints, n)
		}
		raw, err := json.Marshal(ints)
		if err != nil {
			return "", err
		}
		fields = append(fields, bhdPostField{key: "categories", raw: string(raw)})
	}

	if query.IsImdbQuery() {
		fields = append(fields, bhdPostField{key: "imdb_id", raw: jsonQuote(query.ImdbIDShort())})
	} else if query.IsTmdbQuery() && query.TmdbID != nil {
		prefix := "movie/"
		if query.IsTVSearch() {
			prefix = "tv/"
		}
		fields = append(fields, bhdPostField{key: "tmdb_id", raw: jsonQuote(prefix + strconv.Itoa(*query.TmdbID))})
	}

	if query.Limit > 0 && query.Offset > 0 {
		// upstream quirk preserved: page is added as a string
		// (page.ToString()), unlike the numeric fields above.
		page := (query.Offset / query.Limit) + 1
		fields = append(fields, bhdPostField{key: "page", raw: jsonQuote(strconv.Itoa(page))})
	}

	var sb strings.Builder
	sb.WriteString("{")
	for i, f := range fields {
		if i > 0 {
			sb.WriteString(",")
		}
		sb.WriteString(jsonQuote(f.key))
		sb.WriteString(":")
		sb.WriteString(f.raw)
	}
	sb.WriteString("}")
	return sb.String(), nil
}

// parseResponse ports BeyondHDAPIParser.ParseResponse.
func (b *BeyondHDAPI) parseResponse(body []byte) ([]*models.ReleaseInfo, error) {
	content := string(body)
	if strings.HasPrefix(content, "<") {
		// The response was not JSON, likely a HTML page for a server outage
		b.log().Warn("The response was not JSON: " + content)
		return nil, errors.New("The response was not JSON") //nolint:staticcheck // upstream BeyondHDAPI.cs error text, kept 1:1
	}
	if strings.Contains(strings.ToLower(content), strings.ToLower("Invalid API Key")) {
		return nil, errors.New("API Key invalid or not authorized")
	}

	var jsonResponse bhdResponse
	if err := json.Unmarshal(body, &jsonResponse); err != nil {
		return nil, err
	}
	if jsonResponse.StatusCode == 0 {
		return nil, fmt.Errorf("Indexer Error: %s", jsonResponse.StatusMessage) //nolint:staticcheck // upstream BeyondHDAPI.cs error text, kept 1:1
	}

	var releases []*models.ReleaseInfo
	for _, row := range jsonResponse.Results {
		if strings.TrimSpace(row.DownloadUrl) == "" || strings.TrimSpace(row.InfoUrl) == "" {
			b.log().Warn(fmt.Sprintf("Missing download or info url for release with the Id %d, skipping.", row.Id))
			continue
		}

		// Skip invalid results when freeleech or limited filtering is set
		if (b.cfg.FilterFreeleech.Value && !row.Freeleech) || (b.cfg.FilterLimited.Value && !row.Limited) {
			continue
		}

		publishDate, err := parseBHDCreatedAt(row.CreatedAt)
		if err != nil {
			return nil, err
		}

		infoUrl, err := url.Parse(row.InfoUrl)
		if err != nil {
			return nil, err
		}
		downloadUrl, err := url.Parse(row.DownloadUrl)
		if err != nil {
			return nil, err
		}

		size := row.Size
		seeders := int64(row.Seeders)
		peers := int64(row.Leechers + row.Seeders)
		grabs := int64(row.Grabs)
		dvf := 1.0
		uvf := 1.0

		if row.Freeleech || row.Limited {
			dvf = 0
		}
		if row.Promo25 {
			dvf = .75
		}
		if row.Promo50 {
			dvf = .50
		}
		if row.Promo75 {
			dvf = .25
		}

		releaseInfo := &models.ReleaseInfo{
			Guid:                 infoUrl,
			Details:              infoUrl,
			Link:                 downloadUrl,
			Title:                b.getReleaseTitle(row),
			Category:             b.Caps.Categories.MapTrackerCatDescToNewznab(row.Category),
			InfoHash:             row.InfoHash,
			Grabs:                &grabs,
			Size:                 &size,
			Seeders:              &seeders,
			Peers:                &peers,
			PublishDate:          publishDate,
			Languages:            bhdSplitCommaList(row.Audios),
			Subs:                 bhdSplitCommaList(row.Subtitles),
			DownloadVolumeFactor: &dvf,
			UploadVolumeFactor:   &uvf,
		}

		if strings.TrimSpace(row.ImdbId) != "" {
			imdb, _ := parseutil.GetImdbID(row.ImdbId)
			releaseInfo.Imdb = &imdb
		}
		if strings.TrimSpace(row.TmdbId) != "" {
			tmdbID := int64(0)
			parts := strings.Split(row.TmdbId, "/")
			if len(parts) > 1 {
				if v, ok := parseutil.TryCoerceDouble(parts[1]); ok {
					tmdbID = int64(v)
				}
			}
			releaseInfo.TMDb = &tmdbID
		}

		releases = append(releases, releaseInfo)
	}
	return releases, nil
}

// getReleaseTitle ports BeyondHDAPIParser.GetReleaseTitle: hybrid HDR
// features are appended (as a " / "-joined list) only when more than one
// feature is present and the option is enabled.
func (b *BeyondHDAPI) getReleaseTitle(row bhdResult) string {
	title := strings.TrimSpace(row.Name)
	if !b.cfg.AddHybridFeaturesToTitle.Value {
		return title
	}
	var features []string
	if row.DolbyVision {
		features = append(features, "Dolby Vision")
	}
	if row.Hdr10 {
		features = append(features, "HDR10")
	}
	if row.Hdr10Plus {
		features = append(features, "HDR10+")
	}
	if row.Hlg {
		features = append(features, "HLG")
	}
	if len(features) > 1 {
		title += " (" + strings.Join(features, " / ") + ")"
	}
	return title
}

// log is a nil-safe logger for the manual package (Base.Logger may be nil).
func (b *BeyondHDAPI) log() *slog.Logger {
	if b.Logger != nil {
		return b.Logger
	}
	return slog.Default()
}

// --- request body helpers ---------------------------------------------------

// bhdPostField is one ordered JSON object member of the POST body.
type bhdPostField struct {
	key string
	raw string // pre-encoded JSON value
}

func jsonQuote(s string) string {
	raw, _ := json.Marshal(s)
	return string(raw)
}

// parseBHDAirDate mirrors DateTime.TryParseExact($"{season} {episode}",
// "yyyy MM/dd", InvariantCulture) used for daily TV shows.
func parseBHDAirDate(season int, episode string) (time.Time, bool) {
	t, err := time.Parse("2006 01/02", fmt.Sprintf("%d %s", season, episode))
	if err != nil {
		return time.Time{}, false
	}
	return t, true
}

// parseBHDCreatedAt mirrors DateTime.Parse(createdAt, InvariantCulture,
// AssumeUniversal): site-local timestamps are interpreted as UTC.
func parseBHDCreatedAt(s string) (time.Time, error) {
	layouts := []string{
		"2006-01-02 15:04:05",
		"2006-01-02 15:04:05.999999",
		time.RFC3339,
	}
	for _, layout := range layouts {
		if t, err := time.ParseInLocation(layout, s, time.UTC); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("Unable to parse created_at: %q", s) //nolint:staticcheck // port-authored message (upstream DateTime.Parse throws), kept stable
}

func bhdSplitCommaList(s string) []string {
	var out []string
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	if out == nil {
		return []string{}
	}
	return out
}

// --- upstream DTOs ----------------------------------------------------------

// bhdResponse mirrors upstream BHDResponse.
type bhdResponse struct {
	StatusCode    int         `json:"status_code"`
	StatusMessage string      `json:"status_message"`
	Results       []bhdResult `json:"results"`
}

// bhdResult mirrors upstream BHDResult.
type bhdResult struct {
	Id          int    `json:"id"`
	Name        string `json:"name"`
	FolderName  string `json:"folder_name"`
	InfoHash    string `json:"info_hash"`
	Size        int64  `json:"size"`
	Category    string `json:"category"`
	Type        string `json:"type"`
	Grabs       int    `json:"times_completed"`
	Seeders     int    `json:"seeders"`
	Leechers    int    `json:"leechers"`
	Audios      string `json:"audios"`
	Subtitles   string `json:"subtitles"`
	ImdbId      string `json:"imdb_id"`
	TmdbId      string `json:"tmdb_id"`
	Promo25     bool   `json:"promo25"`
	Promo50     bool   `json:"promo50"`
	Promo75     bool   `json:"promo75"`
	Freeleech   bool   `json:"freeleech"`
	Rewind      bool   `json:"rewind"`
	Refund      bool   `json:"refund"`
	Limited     bool   `json:"limited"`
	Rescue      bool   `json:"rescue"`
	CreatedAt   string `json:"created_at"`
	InfoUrl     string `json:"url"`
	DownloadUrl string `json:"download_url"`
	DolbyVision bool   `json:"dv"`
	Hdr10       bool   `json:"hdr10"`
	Hdr10Plus   bool   `json:"hdr10+"`
	Hlg         bool   `json:"hlg"`
}
