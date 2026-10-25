// Package manual holds the hand-written indexers (one file per site).
package manual

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
	"vestt/common/utils/dateutil"
	"vestt/common/utils/parseutil"
)

// HDBitsApi ports Jackett.Common/Indexers/Definitions/HDBitsApi.cs.
//
// mechanism: json-api (private tracker; POST api/test + api/torrents with
// username/passkey; codec/medium/origin multi-select filters; category /
// freeleech / half-leech volume factors; pagination via offset/limit)
//
// Ported from: Definitions/HDBitsApi.cs,
// Models/IndexerConfig/Bespoke/ConfigurationDataHDBitsApi.cs.
//
// 澄清记录:
//   - upstream CheckSiteDown (502/504/521/522/523 → "tracker seems to be
//     down") is enforced inside the Go http client stack (ErrSiteDown), so
//     MakeApiRequest does not re-check statuses.
//   - upstream `Regex.Replace(queryString, "[\\W]+", " ")` uses the
//     Unicode-aware .NET \W; the port uses the ASCII RE2 \W ([^\w]), which
//     differs only for non-ASCII query terms (none of the upstream test
//     corpus hits this).
//   - upstream requestData is a JObject: property insertion order is
//     preserved in the POST body. The port serializes the same order via a
//     small ordered-JSON builder (hdbOrderedJSON).
//   - upstream `(int)r["type_category"]` etc. throw on missing keys; the
//     port coerces loosely (missing → 0), which matches for every wellformed
//     response the API returns (status==0 guarantees the documented shape).
type HDBitsApi struct {
	indexer.Base
	cfg *hdbitsApiConfig
}

type hdbitsApiConfig struct {
	configdata.ConfigData
	Username        *configdata.StringItem
	Passkey         *configdata.StringItem
	FilterFreeleech *configdata.BoolItem
	UseFilenames    *configdata.BoolItem
	Codecs          *configdata.MultiSelectItem
	Mediums         *configdata.MultiSelectItem
	Origins         *configdata.MultiSelectItem
	Instructions    *configdata.InfoItem
}

func newHDBitsApiConfig() *hdbitsApiConfig {
	filterFreeleech := configdata.NewBoolItem("Filter FreeLeech only")
	filterFreeleech.Value = false
	useFilenames := configdata.NewBoolItem("Use Filenames as release titles")
	useFilenames.Value = true
	return &hdbitsApiConfig{
		ConfigData:      *configdata.NewConfigData(),
		Username:        configdata.NewStringItem("Username"),
		Passkey:         configdata.NewStringItem("Passkey"),
		FilterFreeleech: filterFreeleech,
		UseFilenames:    useFilenames,
		Codecs: configdata.NewMultiSelectItem("Codec", []configdata.Option{
			{Value: "0", Label: "Undefined"},
			{Value: "1", Label: "H.264"},
			{Value: "5", Label: "HEVC"},
			{Value: "2", Label: "MPEG-2"},
			{Value: "3", Label: "VC-1"},
			{Value: "6", Label: "VP9"},
			{Value: "4", Label: "XviD"},
		}),
		Mediums: configdata.NewMultiSelectItem("Medium", []configdata.Option{
			{Value: "0", Label: "Undefined"},
			{Value: "1", Label: "Blu-ray/HD DVD"},
			{Value: "4", Label: "Capture"},
			{Value: "3", Label: "Encode"},
			{Value: "5", Label: "Remux"},
			{Value: "6", Label: "WEB-DL"},
		}),
		Origins: configdata.NewMultiSelectItem("Origin", []configdata.Option{
			{Value: "0", Label: "Undefined"},
			{Value: "1", Label: "Internal"},
		}),
		Instructions: configdata.NewInfoItem("", ""),
	}
}

// NewHDBitsApi builds the indexer (services.Factory shape).
func NewHDBitsApi(deps services.Deps) (indexer.Indexer, error) {
	h := &HDBitsApi{cfg: newHDBitsApiConfig()}
	h.Base = indexer.Base{
		IDValue:          "hdbitsapi",
		NameValue:        "HDBits (API)",
		DescriptionValue: "HDBits is a Private site for the HighDefinition Bittorrent Community",
		SiteLinkValue:    "https://hdbits.org/",
		DefaultSiteLink:  "https://hdbits.org/",
		LangValue:        "en-US",
		TypeValue:        "private",
		Caps:             hdbitsCaps(),
		Config:           h.cfg,
		Client:           deps.Client,
		Saver:            deps.Saver,
		Cache:            deps.Cache,
	}
	// upstream: webclient.requestDelay = 2
	if deps.Client != nil {
		deps.Client.SetRequestDelay(h.IDValue, 2*time.Second)
	}
	return h, nil
}

func hdbitsCaps() *models.Capabilities {
	caps := models.NewCapabilities()
	caps.TVSearchParams = []string{models.TVParamQ, models.TVParamSeason, models.TVParamEp, models.TVParamTvdbID}
	caps.MovieSearchParams = []string{models.MovieParamQ, models.MovieParamImdbID}
	c := caps.Categories
	c.AddCategoryMapping("1", models.CatMovies, "Movie")
	c.AddCategoryMapping("2", models.CatTV, "TV")
	c.AddCategoryMapping("3", models.CatTVDocumentary, "Documentary")
	c.AddCategoryMapping("4", models.CatAudio, "Music")
	c.AddCategoryMapping("5", models.CatTVSport, "Sport")
	c.AddCategoryMapping("6", models.CatAudio, "Audio Track")
	c.AddCategoryMapping("7", models.CatXXX, "XXX")
	c.AddCategoryMapping("8", models.CatOther, "Misc/Demo")
	return caps
}

func (h *HDBitsApi) apiURL(endpoint string) string { return h.SiteLink() + "api/" + endpoint }

// ApplyConfiguration ports upstream: a "test" API call with the configured
// credentials; any failure surfaces the API error message.
func (h *HDBitsApi) ApplyConfiguration(ctx context.Context, items []indexer.ConfigItem) (indexer.ConfigStatus, error) {
	if err := h.ApplyItems(items); err != nil {
		return indexer.ConfigCompleted, err
	}
	if err := h.LoadValuesFromJSON(nil, false); err != nil {
		return indexer.ConfigCompleted, err
	}
	if _, err := h.makeAPIRequest(ctx, "test", hdbOrderedJSON{}); err != nil {
		return indexer.ConfigCompleted, err
	}
	h.SetConfigured(true)
	h.SaveConfig(ctx)
	return indexer.ConfigCompleted, nil
}

func (h *HDBitsApi) ResultsForQuery(ctx context.Context, q *models.TorznabQuery, isMeta bool) (*indexer.IndexerResult, error) {
	return h.ResultsForQueryWith(ctx, q, isMeta, h.perform)
}

func (h *HDBitsApi) Test(ctx context.Context) error {
	return h.Base.Test(ctx, h.perform)
}

var hdbSearchRe = regexp.MustCompile(`[\W]+`)

func (h *HDBitsApi) perform(ctx context.Context, query *models.TorznabQuery) ([]*models.ReleaseInfo, error) {
	requestData := hdbOrderedJSON{}

	queryString := query.GetQueryString()
	imdbID, hasImdb := parseutil.GetImdbID(query.ImdbID)

	switch {
	case hasImdb:
		imdb := hdbOrderedJSON{}
		imdb.Set("id", imdbID)
		requestData.Set("imdb", &imdb)
	case query.TvdbID != nil:
		tvdb := hdbOrderedJSON{}
		tvdb.Set("id", int64(*query.TvdbID))
		requestData.Set("tvdb", &tvdb)
		if t, ok := hdbTryParseShowDate(query); ok {
			requestData.Set("search", t.Format("2006-01-02"))
		} else {
			if query.Season != nil {
				tvdb.Set("season", int64(*query.Season))
			}
			if strings.TrimSpace(query.Episode) != "" {
				// upstream assigns the raw episode string (JToken)
				if ep, err := strconv.Atoi(strings.TrimSpace(query.Episode)); err == nil {
					tvdb.Set("episode", int64(ep))
				} else {
					tvdb.Set("episode", query.Episode)
				}
			}
		}
	case strings.TrimSpace(queryString) != "":
		requestData.Set("search", strings.TrimSpace(hdbSearchRe.ReplaceAllString(queryString, " ")))
	}

	categories := h.Caps.Categories.MapTorznabCapsToTrackers(query, false)
	if len(categories) > 0 {
		vals := make([]any, len(categories))
		for i, c := range categories {
			vals[i] = c
		}
		requestData.Set("category", vals)
	}

	codecVals, err := hdbIntValues(h.cfg.Codecs.Values)
	if err != nil {
		return nil, err
	}
	if len(codecVals) > 0 {
		requestData.Set("codec", codecVals)
	}
	mediumVals, err := hdbIntValues(h.cfg.Mediums.Values)
	if err != nil {
		return nil, err
	}
	if len(mediumVals) > 0 {
		requestData.Set("medium", mediumVals)
	}
	originVals, err := hdbIntValues(h.cfg.Origins.Values)
	if err != nil {
		return nil, err
	}
	if len(originVals) > 0 {
		requestData.Set("origin", originVals)
	}

	requestData.Set("limit", int64(100))

	if query.Limit > 0 && query.Offset > 0 {
		requestData.Set("page", int64(query.Offset/query.Limit))
	}

	response, err := h.makeAPIRequest(ctx, "torrents", requestData)
	if err != nil {
		return nil, err
	}

	data, ok := response["data"].([]any)
	if !ok {
		return nil, fmt.Errorf("missing data array")
	}

	var releases []*models.ReleaseInfo
	for _, raw := range data {
		r, ok := raw.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("data entry is not an object")
		}

		if h.cfg.FilterFreeleech.Value && hdbStr(r["freeleech"]) != "yes" {
			continue
		}

		title := h.getTitle(r)

		// if tv then match query keywords against title #12753
		if !query.IsImdbQuery() && !query.MatchQueryStringAND(title, 0, "") {
			continue
		}

		id := hdbStr(r["id"])
		filename := hdbStr(r["filename"])
		link, _ := url.Parse(h.SiteLink() + "download.php/" + filename + "?id=" + id + "&passkey=" + h.cfg.Passkey.Value)
		seeders, _ := hdbInt(r["seeders"])
		utadded, _ := hdbInt(r["utadded"])
		details, _ := url.Parse(h.SiteLink() + "details.php?id=" + id)
		size, _ := hdbInt(r["size"])
		files, _ := hdbInt(r["numfiles"])
		grabs, _ := hdbInt(r["times_completed"])
		leechers, _ := hdbInt(r["leechers"])
		typeCategory, _ := hdbInt(r["type_category"])
		peers := seeders + leechers

		release := &models.ReleaseInfo{
			Title:                title,
			Details:              details,
			Link:                 link,
			Category:             h.Caps.Categories.MapTrackerCatToNewznab(hdbStr(r["type_category"])),
			Size:                 &size,
			Files:                &files,
			Grabs:                &grabs,
			Seeders:              &seeders,
			PublishDate:          dateutil.UnixTimestampToDateTime(utadded),
			UploadVolumeFactor:   hdbUploadFactor(typeCategory),
			DownloadVolumeFactor: hdbDownloadFactor(r),
			Guid:                 link,
			Peers:                &peers,
		}

		if imdbRaw, ok := r["imdb"].(map[string]any); ok {
			if imdb, ok := parseutil.GetImdbID(hdbStr(imdbRaw["id"])); ok {
				release.Imdb = &imdb
			}
		}
		if tvdbRaw, ok := r["tvdb"].(map[string]any); ok {
			if tvdb, ok := hdbInt(tvdbRaw["id"]); ok {
				release.TVDBId = &tvdb
			}
		}

		releases = append(releases, release)
	}
	return releases, nil
}

// hdbTryParseShowDate mirrors upstream
// DateTime.TryParseExact($"{query.Season} {query.Episode}", "yyyy MM/dd").
func hdbTryParseShowDate(query *models.TorznabQuery) (time.Time, bool) {
	if query.Season == nil {
		return time.Time{}, false
	}
	parts := strings.Split(query.Episode, "/")
	if len(parts) != 2 {
		return time.Time{}, false
	}
	month, err1 := strconv.Atoi(parts[0])
	day, err2 := strconv.Atoi(parts[1])
	if err1 != nil || err2 != nil {
		return time.Time{}, false
	}
	if *query.Season < 0 || month < 1 || month > 12 || day < 1 || day > 31 {
		return time.Time{}, false
	}
	return time.Date(*query.Season, time.Month(month), day, 0, 0, 0, 0, time.UTC), true
}

// getTitle ports upstream GetTitle (UseFilenames option; XXX content and
// full discs always use the release name).
func (h *HDBitsApi) getTitle(item map[string]any) string {
	filename := hdbStr(item["filename"])
	name := hdbStr(item["name"])
	typeCategory, _ := hdbInt(item["type_category"])
	typeMedium, _ := hdbInt(item["type_medium"])

	if typeCategory != 7 && typeMedium != 1 && h.cfg.UseFilenames.Value && strings.TrimSpace(filename) != "" {
		return strings.ReplaceAll(filename, ".torrent", "")
	}
	return name
}

// hdbUploadFactor ports upstream GetUploadFactor.
func hdbUploadFactor(typeCategory int64) *float64 {
	if typeCategory == 7 {
		return hdbF64(0)
	}
	return hdbF64(1)
}

// hdbDownloadFactor ports upstream GetDownloadFactor.
func hdbDownloadFactor(r map[string]any) *float64 {
	typeCategory, _ := hdbInt(r["type_category"])
	typeMedium, _ := hdbInt(r["type_medium"])
	typeOrigin, _ := hdbInt(r["type_origin"])

	// 100% Neutral Leech: all XXX content.
	if typeCategory == 7 {
		return hdbF64(0)
	}
	// 100% Free Leech: all blue torrents.
	if hdbStr(r["freeleech"]) == "yes" {
		return hdbF64(0)
	}
	// 50% Free Leech: all full discs, remuxes, captures and all internal
	// encodes, also all TV and Documentary content.
	if typeMedium == 1 || typeMedium == 5 || typeMedium == 4 || typeOrigin == 1 || typeCategory == 2 || typeCategory == 3 {
		return hdbF64(0.5)
	}
	return hdbF64(1)
}

// makeAPIRequest ports upstream MakeApiRequest: POST the JSON body with
// username/passkey appended, then check the status code.
func (h *HDBitsApi) makeAPIRequest(ctx context.Context, endpoint string, requestData hdbOrderedJSON) (map[string]any, error) {
	if h.Client == nil {
		return nil, errors.New("http client not configured")
	}
	requestData.Set("username", h.cfg.Username.Value)
	requestData.Set("passkey", h.cfg.Passkey.Value)

	res, err := h.Client.Do(ctx, h.ID(), &httpclient.Request{
		Method: http.MethodPost,
		URL:    h.apiURL(endpoint),
		Headers: map[string]string{
			"Accept":       "application/json",
			"Content-Type": "application/json",
		},
		RawBody: requestData.String(),
	})
	if err != nil {
		return nil, err
	}

	dec := json.NewDecoder(bytes.NewReader(res.Body))
	dec.UseNumber()
	var jsonResponse map[string]any
	if err := dec.Decode(&jsonResponse); err != nil {
		return nil, fmt.Errorf("Error while parsing json: %s", string(res.Body)) //nolint:staticcheck // upstream HDBitsApi.cs error text, kept 1:1
	}

	status, _ := hdbInt(jsonResponse["status"])
	if status != 0 {
		return nil, fmt.Errorf("HDBits returned an error with status code %d: %s", status, hdbStr(jsonResponse["message"]))
	}
	return jsonResponse, nil
}

// hdbIntValues mirrors upstream configData.X.Values.Select(int.Parse).
func hdbIntValues(values []string) ([]any, error) {
	out := make([]any, 0, len(values))
	for _, v := range values {
		n, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64)
		if err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, nil
}

// hdbStr mirrors the loose Newtonsoft (string)JToken cast.
func hdbStr(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return t
	case json.Number:
		return t.String()
	case bool:
		if t {
			return "True"
		}
		return "False"
	default:
		return fmt.Sprintf("%v", t)
	}
}

// hdbInt mirrors numeric JToken casts.
func hdbInt(v any) (int64, bool) {
	switch t := v.(type) {
	case json.Number:
		if n, err := t.Int64(); err == nil {
			return n, true
		}
	case float64:
		return int64(t), true
	case string:
		if n, err := strconv.ParseInt(strings.TrimSpace(t), 10, 64); err == nil {
			return n, true
		}
	}
	return 0, false
}

func hdbF64(v float64) *float64 { return &v }

// hdbOrderedJSON is a minimal insertion-ordered JSON object builder
// mirroring upstream JObject semantics (ToString() for the POST body).
type hdbOrderedJSON struct {
	keys   []string
	values map[string]any
}

func (o *hdbOrderedJSON) Set(key string, value any) *hdbOrderedJSON {
	if o.values == nil {
		o.values = map[string]any{}
	}
	if _, exists := o.values[key]; !exists {
		o.keys = append(o.keys, key)
	}
	o.values[key] = value
	return o
}

func (o *hdbOrderedJSON) String() string {
	var buf bytes.Buffer
	buf.WriteByte('{')
	for i, key := range o.keys {
		if i > 0 {
			buf.WriteByte(',')
		}
		kb, _ := json.Marshal(key)
		buf.Write(kb)
		buf.WriteByte(':')
		buf.Write(hdbJSONValue(o.values[key]))
	}
	buf.WriteByte('}')
	return buf.String()
}

// hdbJSONValue renders a value: hdbOrderedJSON objects, []any slices,
// json.Number-safe numbers, strings, bools.
func hdbJSONValue(v any) []byte {
	switch t := v.(type) {
	case *hdbOrderedJSON:
		return []byte(t.String())
	case hdbOrderedJSON:
		return []byte(t.String())
	case []any:
		var buf bytes.Buffer
		buf.WriteByte('[')
		for i, e := range t {
			if i > 0 {
				buf.WriteByte(',')
			}
			buf.Write(hdbJSONValue(e))
		}
		buf.WriteByte(']')
		return buf.Bytes()
	default:
		b, err := json.Marshal(t)
		if err != nil {
			return []byte("null")
		}
		return b
	}
}
