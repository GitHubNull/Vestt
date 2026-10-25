package manual

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"vestt/common/httpclient"
	"vestt/common/indexer"
	"vestt/common/models"
	"vestt/common/services"
)

type btnSite struct {
	srv    *httptest.Server
	mu     sync.Mutex
	hits   []string
	bodies []string
	serve  string
}

func newBTNSite(t *testing.T, serve string) *btnSite {
	t.Helper()
	s := &btnSite{serve: serve}
	s.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		s.mu.Lock()
		s.hits = append(s.hits, r.Method+" "+r.URL.RequestURI())
		s.bodies = append(s.bodies, string(body))
		s.mu.Unlock()
		w.Header().Set("Content-Type", "application/json-rpc")
		_, _ = w.Write([]byte(s.serve))
	}))
	t.Cleanup(s.srv.Close)
	return s
}

const btnTestAPIKey = "btnapikey0123456789abcdef"

func newTestBroadcastheNet(t *testing.T, site *btnSite) *BroadcasTheNet {
	t.Helper()
	client, err := httpclient.New(httpclient.Config{}, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("httpclient.New: %v", err)
	}
	ix, err := NewBroadcastheNet(services.Deps{Client: client})
	if err != nil {
		t.Fatalf("NewBroadcastheNet: %v", err)
	}
	b := ix.(*BroadcasTheNet)
	b.apiBase = site.srv.URL
	b.cfg.Key.Value = btnTestAPIKey
	// tests must not pay the upstream 5s requestDelay
	client.SetRequestDelay(b.ID(), 0)
	return b
}

func TestBroadcastheNetCaps(t *testing.T) {
	ix, err := NewBroadcastheNet(services.Deps{})
	if err != nil {
		t.Fatalf("NewBroadcastheNet: %v", err)
	}
	caps := ix.Capabilities()
	if caps.LimitsDefault == nil || *caps.LimitsDefault != 100 || caps.LimitsMax == nil || *caps.LimitsMax != 1000 {
		t.Fatalf("limits: %v/%v", caps.LimitsDefault, caps.LimitsMax)
	}
	if !caps.TVSearchImdbAvailable {
		t.Fatal("TVSearchImdbAvailable must be true")
	}
	wantTV := []string{models.TVParamQ, models.TVParamSeason, models.TVParamEp, models.TVParamImdbID, models.TVParamTvdbID}
	if strings.Join(caps.TVSearchParams, ",") != strings.Join(wantTV, ",") {
		t.Fatalf("TVSearchParams: %v", caps.TVSearchParams)
	}
	for _, tc := range []struct {
		tracker string
		want    int
	}{
		{"SD", 5030}, {"720p", 5040}, {"1080p", 5040}, {"1080i", 5040},
		{"2160p", 5045}, {"Portable Device", 5030},
	} {
		got := caps.Categories.MapTrackerCatToNewznab(tc.tracker)
		if !containsInt(got, tc.want) {
			t.Fatalf("MapTrackerCatToNewznab(%q) = %v, want it to contain %d", tc.tracker, got, tc.want)
		}
	}
}

func TestBroadcastheNetConfigForm(t *testing.T) {
	ix, err := NewBroadcastheNet(services.Deps{})
	if err != nil {
		t.Fatalf("NewBroadcastheNet: %v", err)
	}
	items, err := ix.ConfigurationForSetup(context.Background())
	if err != nil {
		t.Fatalf("ConfigurationForSetup: %v", err)
	}
	var ids []string
	for _, it := range items {
		ids = append(ids, it["id"].(string))
	}
	// hidden items excluded; only sitelink, the APIKey item and tags
	if len(ids) != 3 || ids[0] != "sitelink" || ids[1] != "apikey" || ids[2] != "tags" {
		t.Fatalf("config ids: %v", ids)
	}
}

// btnBody decodes one captured JSON-RPC request for assertions.
type btnBody struct {
	JSONRPC string            `json:"jsonrpc"`
	Method  string            `json:"method"`
	Params  []json.RawMessage `json:"params"`
	ID      string            `json:"id"`
}

func decodeBTNBodies(t *testing.T, bodies []string) []btnBody {
	t.Helper()
	out := make([]btnBody, 0, len(bodies))
	for _, raw := range bodies {
		var b btnBody
		if err := json.Unmarshal([]byte(raw), &b); err != nil {
			t.Fatalf("unmarshal body %q: %v", raw, err)
		}
		out = append(out, b)
	}
	return out
}

func assertBTNRequest(t *testing.T, b btnBody, wantParams string, results, offset int) {
	t.Helper()
	if b.JSONRPC != "2.0" || b.Method != "getTorrents" {
		t.Fatalf("envelope: %+v", b)
	}
	if len(b.ID) != 8 {
		t.Fatalf("id: %q", b.ID)
	}
	for _, c := range b.ID {
		if !strings.ContainsRune("0123456789abcdef", c) {
			t.Fatalf("id not hex: %q", b.ID)
		}
	}
	if len(b.Params) != 4 {
		t.Fatalf("params: %v", b.Params)
	}
	var key string
	if err := json.Unmarshal(b.Params[0], &key); err != nil || key != btnTestAPIKey {
		t.Fatalf("api key param: %v (%v)", key, err)
	}
	var params map[string]string
	if err := json.Unmarshal(b.Params[1], &params); err != nil {
		t.Fatalf("params[1]: %v", err)
	}
	var want map[string]string
	if err := json.Unmarshal([]byte(wantParams), &want); err != nil {
		t.Fatalf("bad wantParams: %v", err)
	}
	if len(params) != len(want) {
		t.Fatalf("params %v want %v", params, want)
	}
	for k, v := range want {
		if params[k] != v {
			t.Fatalf("params[%q] = %q want %q (all %v)", k, params[k], v, params)
		}
	}
	var gotResults, gotOffset int
	if err := json.Unmarshal(b.Params[2], &gotResults); err != nil || gotResults != results {
		t.Fatalf("results param: %d (%v)", gotResults, err)
	}
	if err := json.Unmarshal(b.Params[3], &gotOffset); err != nil || gotOffset != offset {
		t.Fatalf("offset param: %d (%v)", gotOffset, err)
	}
}

const btnEmptyResult = `{"id":"abcd1234","result":{"torrents":{}},"error":null}`

func TestBroadcastheNetSearchRequestBodies(t *testing.T) {
	season2 := 2
	year2024 := 2024

	cases := []struct {
		name       string
		query      *models.TorznabQuery
		wantBodies []string // JSON param objects, one per POST
	}{
		{
			name: "plain search",
			query: func() *models.TorznabQuery {
				q := models.NewTorznabQuery()
				q.QueryType = "tvsearch"
				q.SearchTerm = "my show"
				return q
			}(),
			wantBodies: []string{`{"search":"my%show"}`},
		},
		{
			name: "imdb query",
			query: func() *models.TorznabQuery {
				q := models.NewTorznabQuery()
				q.QueryType = "tvsearch"
				q.ImdbID = "tt1234567"
				return q
			}(),
			wantBodies: []string{`{"imdb":"tt1234567"}`},
		},
		{
			name: "tvdb query with term",
			query: func() *models.TorznabQuery {
				q := models.NewTorznabQuery()
				q.QueryType = "tvsearch"
				q.SearchTerm = "my show"
				tvdb := 12345
				q.TvdbID = &tvdb
				return q
			}(),
			wantBodies: []string{`{"search":"my%show","tvdb":"12345"}`},
		},
		{
			name: "season only produces two requests",
			query: func() *models.TorznabQuery {
				q := models.NewTorznabQuery()
				q.QueryType = "tvsearch"
				q.SearchTerm = "my show"
				q.Season = &season2
				return q
			}(),
			wantBodies: []string{
				`{"search":"my%show","category":"Season","name":"Season 2%"}`,
				`{"search":"my%show","category":"Episode","name":"S02E%"}`,
			},
		},
		{
			name: "daily episode",
			query: func() *models.TorznabQuery {
				q := models.NewTorznabQuery()
				q.QueryType = "tvsearch"
				q.Season = &year2024
				q.Episode = "01/05"
				return q
			}(),
			wantBodies: []string{`{"name":"2024.01.05","category":"Episode"}`},
		},
		{
			name: "standard episode",
			query: func() *models.TorznabQuery {
				q := models.NewTorznabQuery()
				q.QueryType = "tvsearch"
				s := 1
				q.Season = &s
				q.Episode = "3"
				return q
			}(),
			wantBodies: []string{`{"name":"S01E%03%","category":"Episode"}`},
		},
		{
			name: "pagination",
			query: func() *models.TorznabQuery {
				q := models.NewTorznabQuery()
				q.QueryType = "tvsearch"
				q.SearchTerm = "my show"
				q.Limit = 100
				q.Offset = 100
				return q
			}(),
			wantBodies: []string{`{"search":"my%show"}`}, // results/offset asserted separately
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			site := newBTNSite(t, btnEmptyResult)
			b := newTestBroadcastheNet(t, site)
			if _, err := b.ResultsForQuery(context.Background(), tc.query, false); err != nil {
				t.Fatalf("ResultsForQuery: %v", err)
			}
			site.mu.Lock()
			bodies := append([]string(nil), site.bodies...)
			site.mu.Unlock()
			if len(bodies) != len(tc.wantBodies) {
				t.Fatalf("requests: %d want %d (%v)", len(bodies), len(tc.wantBodies), bodies)
			}
			decoded := decodeBTNBodies(t, bodies)
			results, offset := 100, 0
			if tc.query.Limit > 0 {
				results, offset = tc.query.Limit, tc.query.Offset
			}
			for i, body := range decoded {
				assertBTNRequest(t, body, tc.wantBodies[i], results, offset)
			}
		})
	}
}

func TestBroadcastheNetAbsoluteNumberDisablesIDSearch(t *testing.T) {
	site := newBTNSite(t, btnEmptyResult)
	b := newTestBroadcastheNet(t, site)

	q := models.NewTorznabQuery()
	q.QueryType = "tvsearch"
	q.SearchTerm = "5"
	tvdb := 5
	q.TvdbID = &tvdb

	res, err := b.ResultsForQuery(context.Background(), q, false)
	if err != nil {
		t.Fatalf("ResultsForQuery: %v", err)
	}
	if len(res.Releases) != 0 {
		t.Fatalf("releases: %d", len(res.Releases))
	}
	site.mu.Lock()
	defer site.mu.Unlock()
	if len(site.hits) != 0 {
		t.Fatalf("expected no requests, got %v", site.hits)
	}
}

const btnResultsFixture = `{
  "id": "abcd1234",
  "result": {
    "torrents": {
      "20": {
        "TorrentID": 20,
        "DownloadURL": "https://broadcasthe.net/download/20",
        "GroupName": "GRP2",
        "GroupID": 66,
        "Series": "Other Series",
        "Category": "Season",
        "Snatched": 100,
        "Seeders": 10,
        "Leechers": 5,
        "Resolution": "2160p",
        "Container": "M2TS",
        "ReleaseName": "Other.Series.S02.2160p.H.265-GRP",
        "Size": 5368709120,
        "Time": 1700000100,
        "InfoHash": "9999999999999999999999999999999999999999"
      },
      "10": {
        "TorrentID": 10,
        "DownloadURL": "https://broadcasthe.net/download/10",
        "GroupName": "GRP",
        "GroupID": 55,
        "Series": "My Series",
        "SeriesBanner": "//images/btn/banner.jpg",
        "YoutubeTrailer": "http://youtu.be/x",
        "Category": "Episode",
        "Snatched": 7,
        "Seeders": 3,
        "Leechers": 1,
        "Source": "WEB-DL",
        "Container": "MKV",
        "Codec": "x264",
        "Resolution": "1080p",
        "Origin": "SCENE",
        "ReleaseName": "My.Series.S01E01.1080p.WEB-DL",
        "Size": 2147483648,
        "Time": 1700000000,
        "TvdbID": 123,
        "TvrageID": 456,
        "ImdbID": "tt1234567",
        "InfoHash": "abcdef0123456789abcdef0123456789abcdef01"
      }
    }
  },
  "error": null
}`

func TestBroadcastheNetParseResponse(t *testing.T) {
	site := newBTNSite(t, btnResultsFixture)
	b := newTestBroadcastheNet(t, site)

	res, err := b.ResultsForQuery(context.Background(), &models.TorznabQuery{QueryType: "search"}, false)
	if err != nil {
		t.Fatalf("ResultsForQuery: %v", err)
	}
	if len(res.Releases) != 2 {
		t.Fatalf("releases: %d", len(res.Releases))
	}

	// torrent id 10 sorts before 20 (deterministic ascending order)
	r := res.Releases[0]
	if r.Title != "My.Series.S01E01.1080p.WEB-DL" {
		t.Fatalf("title: %q", r.Title)
	}
	wantDesc := "Series: My Series<br />\n" +
		"Group Name: GRP<br />\n" +
		"Source: WEB-DL<br />\n" +
		"Container: MKV<br />\n" +
		"Codec: x264<br />\n" +
		"Resolution: 1080p<br />\n" +
		"Origin: SCENE<br />\n" +
		`Youtube Trailer: <a href="http://youtu.be/x">http://youtu.be/x</a>`
	if r.Description != wantDesc {
		t.Fatalf("description:\n%q\nwant:\n%q", r.Description, wantDesc)
	}
	if !containsInt(r.Category, 5040) {
		t.Fatalf("category: %v", r.Category)
	}
	if r.Size == nil || *r.Size != 2147483648 {
		t.Fatalf("size: %v", r.Size)
	}
	if r.Grabs == nil || *r.Grabs != 7 {
		t.Fatalf("grabs: %v", r.Grabs)
	}
	if r.Seeders == nil || *r.Seeders != 3 {
		t.Fatalf("seeders: %v", r.Seeders)
	}
	if r.Peers == nil || *r.Peers != 4 {
		t.Fatalf("peers: %v", r.Peers)
	}
	if r.PublishDate.UTC() != time.Unix(1700000000, 0).UTC() {
		t.Fatalf("publish date: %v", r.PublishDate)
	}
	if r.Link == nil || r.Link.String() != "https://broadcasthe.net/download/10" {
		t.Fatalf("link: %v", r.Link)
	}
	if r.Guid == nil || r.Guid.String() != "https://broadcasthe.net/download/10" {
		t.Fatalf("guid: %v", r.Guid)
	}
	if r.Details == nil || r.Details.String() != "https://broadcasthe.net/torrents.php?id=55&torrentid=10" {
		t.Fatalf("details: %v", r.Details)
	}
	if r.Poster == nil || r.Poster.String() != "https://images/btn/banner.jpg" {
		t.Fatalf("poster: %v", r.Poster)
	}
	if r.InfoHash != "abcdef0123456789abcdef0123456789abcdef01" {
		t.Fatalf("infohash: %q", r.InfoHash)
	}
	if r.Imdb == nil || *r.Imdb != 1234567 {
		t.Fatalf("imdb: %v", r.Imdb)
	}
	if r.TVDBId == nil || *r.TVDBId != 123 {
		t.Fatalf("tvdb: %v", r.TVDBId)
	}
	if r.RageID == nil || *r.RageID != 456 {
		t.Fatalf("rage: %v", r.RageID)
	}
	if r.DownloadVolumeFactor == nil || *r.DownloadVolumeFactor != 0 {
		t.Fatalf("dvf: %v", r.DownloadVolumeFactor) // ratioless
	}
	if r.UploadVolumeFactor == nil || *r.UploadVolumeFactor != 1 {
		t.Fatalf("uvf: %v", r.UploadVolumeFactor)
	}
	if r.MinimumRatio == nil || *r.MinimumRatio != 1 {
		t.Fatalf("min ratio: %v", r.MinimumRatio)
	}
	if r.MinimumSeedTime == nil || *r.MinimumSeedTime != 86400 {
		t.Fatalf("min seed time: %v", r.MinimumSeedTime)
	}

	r2 := res.Releases[1]
	// M2TS container: H.265 rewritten to HEVC
	if r2.Title != "Other.Series.S02.2160p.HEVC-GRP" {
		t.Fatalf("title2: %q", r2.Title)
	}
	if !containsInt(r2.Category, 5045) {
		t.Fatalf("category2: %v", r2.Category)
	}
	if r2.MinimumSeedTime == nil || *r2.MinimumSeedTime != 432000 {
		t.Fatalf("min seed time2: %v", r2.MinimumSeedTime)
	}
}

func TestBroadcastheNetErrorAndEmptyPaths(t *testing.T) {
	site := newBTNSite(t, `{"id":"abcd1234","result":null,"error":"Invalid API Key"}`)
	b := newTestBroadcastheNet(t, site)
	_, err := b.ResultsForQuery(context.Background(), &models.TorznabQuery{QueryType: "search"}, false)
	if err == nil || !strings.Contains(err.Error(), "Indexer API call returned an error [Invalid API Key]") {
		t.Fatalf("err: %v", err)
	}

	// result without torrents → empty, no error
	site2 := newBTNSite(t, `{"id":"abcd1234","result":{},"error":null}`)
	b2 := newTestBroadcastheNet(t, site2)
	res, err := b2.ResultsForQuery(context.Background(), &models.TorznabQuery{QueryType: "search"}, false)
	if err != nil {
		t.Fatalf("ResultsForQuery: %v", err)
	}
	if len(res.Releases) != 0 {
		t.Fatalf("releases: %d", len(res.Releases))
	}
}

func TestBroadcastheNetLastRequestCache(t *testing.T) {
	site := newBTNSite(t, btnResultsFixture)
	b := newTestBroadcastheNet(t, site)

	q := func() *models.TorznabQuery {
		x := models.NewTorznabQuery()
		x.QueryType = "tvsearch"
		x.SearchTerm = "my show"
		return x
	}

	res1, err := b.ResultsForQuery(context.Background(), q(), false)
	if err != nil {
		t.Fatalf("ResultsForQuery: %v", err)
	}
	// identical query short-circuits the repeated API call
	res2, err := b.ResultsForQuery(context.Background(), q(), false)
	if err != nil {
		t.Fatalf("ResultsForQuery: %v", err)
	}
	site.mu.Lock()
	hitsAfterDuplicate := len(site.hits)
	site.mu.Unlock()
	if hitsAfterDuplicate != 1 {
		t.Fatalf("duplicate query must hit the API once, got %d", hitsAfterDuplicate)
	}
	if len(res1.Releases) != 2 || len(res2.Releases) != 2 {
		t.Fatalf("releases: %d/%d", len(res1.Releases), len(res2.Releases))
	}

	// a different query goes through
	q3 := q()
	q3.SearchTerm = "other show"
	if _, err := b.ResultsForQuery(context.Background(), q3, false); err != nil {
		t.Fatalf("ResultsForQuery: %v", err)
	}
	site.mu.Lock()
	defer site.mu.Unlock()
	if len(site.hits) != 2 {
		t.Fatalf("hits: %v", site.hits)
	}
}

func TestBroadcastheNetApplyConfiguration(t *testing.T) {
	site := newBTNSite(t, btnResultsFixture)
	b := newTestBroadcastheNet(t, site)
	status, err := b.ApplyConfiguration(context.Background(), []indexer.ConfigItem{
		{ID: "sitelink", Value: site.srv.URL + "/"},
		{ID: "apikey", Value: btnTestAPIKey},
	})
	if err != nil {
		t.Fatalf("ApplyConfiguration: %v", err)
	}
	if status != indexer.ConfigCompleted || !b.IsConfigured() {
		t.Fatalf("status: %v configured: %v", status, b.IsConfigured())
	}

	siteEmpty := newBTNSite(t, btnEmptyResult)
	b2 := newTestBroadcastheNet(t, siteEmpty)
	_, err = b2.ApplyConfiguration(context.Background(), []indexer.ConfigItem{
		{ID: "sitelink", Value: siteEmpty.srv.URL + "/"},
		{ID: "apikey", Value: btnTestAPIKey},
	})
	if err == nil || err.Error() != "Testing returned no results!" {
		t.Fatalf("err: %v", err)
	}
	if b2.IsConfigured() {
		t.Fatal("must not be configured after failure")
	}
}
