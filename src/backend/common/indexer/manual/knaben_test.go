package manual

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"vestt/common/httpclient"
	"vestt/common/indexer"
	"vestt/common/models"
	"vestt/common/services"
)

type knabenSite struct {
	srv      *httptest.Server
	mu       sync.Mutex
	postBody string
	status   int
	body     string
}

func newKnabenSite(t *testing.T, status int, body string) *knabenSite {
	t.Helper()
	s := &knabenSite{status: status, body: body}
	s.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()
		buf := make([]byte, r.ContentLength)
		_, _ = io.ReadFull(r.Body, buf)
		s.postBody = string(buf)
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(s.status)
		_, _ = w.Write([]byte(s.body))
	}))
	t.Cleanup(s.srv.Close)
	return s
}

func newTestKnaben(t *testing.T, site *knabenSite) *Knaben {
	t.Helper()
	client, err := httpclient.New(httpclient.Config{}, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("httpclient.New: %v", err)
	}
	ix, err := NewKnaben(services.Deps{Client: client})
	if err != nil {
		t.Fatalf("NewKnaben: %v", err)
	}
	k := ix.(*Knaben)
	k.SiteLinkValue = site.srv.URL + "/"
	k.apiBase = site.srv.URL + "/v1"
	client.SetRequestDelay(k.ID(), 0)
	return k
}

const knabenResponseJSON = `{
  "Hits": [
    {
      "Title": "Some Movie 2020 1080p WEB-DL x264-GRP",
      "categoryId": [3000000, 3001000],
      "hash": "abcdef0123456789abcdef0123456789abcdef01",
      "details": "https://knaben.org/release/some-movie",
      "link": "https://torcache.example/dl/abcdef0123456789abcdef0123456789abcdef01.torrent",
      "MagnetUrl": "magnet:?xt=urn:btih:abcdef0123456789abcdef0123456789abcdef01",
      "bytes": 2147483648,
      "Seeders": 25,
      "peers": 5,
      "Date": "2024-05-01T12:00:00+02:00",
      "TrackerId": "thepiratebay",
      "Tracker": "The Pirate Bay"
    },
    {
      "Title": "Other Show S01E01 720p",
      "categoryId": [2001000],
      "hash": "1111111111111111111111111111111111111111",
      "details": "https://knaben.org/release/other-show",
      "link": "",
      "MagnetUrl": "magnet:?xt=urn:btih:1111111111111111111111111111111111111111",
      "bytes": 524288000,
      "Seeders": 7,
      "peers": 2,
      "Date": "2024-05-02T08:30:00",
      "TrackerId": "rarbg",
      "Tracker": "RARBG"
    },
    {
      "Title": "Dead Torrent",
      "categoryId": [3000000],
      "hash": "2222222222222222222222222222222222222222",
      "details": "https://knaben.org/release/dead",
      "link": "https://example.dead/x.torrent",
      "MagnetUrl": "magnet:?xt=urn:btih:2222222222222222222222222222222222222222",
      "bytes": 100,
      "Seeders": 0,
      "peers": 3,
      "Date": "2024-05-03T00:00:00+00:00",
      "TrackerId": "eztv",
      "Tracker": "EZTV"
    },
    {
      "Title": "Lime Torrent No Magnet",
      "categoryId": [4002000],
      "hash": "",
      "details": "https://knaben.org/release/limetorrents-x",
      "link": "https://limetorrents.example/x.torrent",
      "MagnetUrl": "",
      "bytes": 200,
      "Seeders": 9,
      "peers": 1,
      "Date": "2024-05-04T00:00:00+00:00",
      "TrackerId": "limetorrents",
      "Tracker": "LimeTorrents"
    }
  ]
}`

func TestKnabenCaps(t *testing.T) {
	ix, err := NewKnaben(services.Deps{})
	if err != nil {
		t.Fatalf("NewKnaben: %v", err)
	}
	caps := ix.Capabilities()
	if strings.Join(caps.TVSearchParams, ",") != strings.Join([]string{models.TVParamQ, models.TVParamSeason, models.TVParamEp}, ",") {
		t.Fatalf("TVSearchParams: %v", caps.TVSearchParams)
	}
	for _, tc := range []struct {
		tracker string
		want    int
	}{
		{"1000000", 3000}, {"1003000", 3030}, {"2006000", 5060},
		{"3003000", 2045}, {"4004000", 4020}, {"5002000", 6060},
		{"6004000", 5070}, {"7001000", 1180}, {"8001000", 4070},
		{"9004000", 7040}, {"10001000", 8010},
	} {
		got := caps.Categories.MapTrackerCatToNewznab(tc.tracker)
		found := false
		for _, id := range got {
			if id == tc.want {
				found = true
			}
		}
		if !found {
			t.Fatalf("MapTrackerCatToNewznab(%q) = %v, want it to contain %d", tc.tracker, got, tc.want)
		}
	}
}

func TestKnabenConfigForm(t *testing.T) {
	ix, err := NewKnaben(services.Deps{})
	if err != nil {
		t.Fatalf("NewKnaben: %v", err)
	}
	items, err := ix.ConfigurationForSetup(context.Background())
	if err != nil {
		t.Fatalf("ConfigurationForSetup: %v", err)
	}
	var ids []string
	for _, it := range items {
		ids = append(ids, it["id"].(string))
	}
	// no indexer-specific items: sitelink + tags only
	if strings.Join(ids, "|") != "sitelink|tags" {
		t.Fatalf("config ids: %v", ids)
	}
}

func TestKnabenApplyConfigurationOK(t *testing.T) {
	site := newKnabenSite(t, http.StatusOK, knabenResponseJSON)
	k := newTestKnaben(t, site)

	status, err := k.ApplyConfiguration(context.Background(), []indexer.ConfigItem{
		{ID: "sitelink", Value: site.srv.URL + "/"},
	})
	if err != nil {
		t.Fatalf("ApplyConfiguration: %v", err)
	}
	if status != indexer.ConfigCompleted {
		t.Fatalf("status: %v, want ConfigCompleted", status)
	}
	if !k.IsConfigured() {
		t.Fatal("must be configured after a successful test query")
	}
}

func TestKnabenApplyConfigurationNoReleases(t *testing.T) {
	site := newKnabenSite(t, http.StatusOK, `{"Hits": []}`)
	k := newTestKnaben(t, site)

	_, err := k.ApplyConfiguration(context.Background(), []indexer.ConfigItem{
		{ID: "sitelink", Value: site.srv.URL + "/"},
	})
	if err == nil || err.Error() != "Could not find releases from this URL" {
		t.Fatalf("err: %v", err)
	}
	if k.IsConfigured() {
		t.Fatal("must not be configured when no releases are found")
	}
}

func TestKnabenSearchRequestBody(t *testing.T) {
	site := newKnabenSite(t, http.StatusOK, knabenResponseJSON)
	k := newTestKnaben(t, site)

	q := models.NewTorznabQuery()
	q.QueryType = "search"
	q.SearchTerm = "some query"

	_, err := k.ResultsForQuery(context.Background(), q, false)
	if err != nil {
		t.Fatalf("ResultsForQuery: %v", err)
	}
	wantBody := `{"order_by":"date","order_direction":"desc","from":0,"size":100,` +
		`"hide_unsafe":true,"search_type":"100%","search_field":"title","query":"some query"}`
	if site.postBody != wantBody {
		t.Fatalf("post body:\n%s\nwant:\n%s", site.postBody, wantBody)
	}
}

func TestKnabenCategoryRequestBody(t *testing.T) {
	site := newKnabenSite(t, http.StatusOK, knabenResponseJSON)
	k := newTestKnaben(t, site)

	q := models.NewTorznabQuery()
	q.QueryType = "search"
	q.SearchTerm = "some query"
	q.Categories = []int{2040} // Movies HD → 3001000

	_, err := k.ResultsForQuery(context.Background(), q, false)
	if err != nil {
		t.Fatalf("ResultsForQuery: %v", err)
	}
	if !strings.Contains(site.postBody, `"categories":[3001000]`) {
		t.Fatalf("post body: %s", site.postBody)
	}
}

func TestKnabenParse(t *testing.T) {
	site := newKnabenSite(t, http.StatusOK, knabenResponseJSON)
	k := newTestKnaben(t, site)

	q := models.NewTorznabQuery()
	q.QueryType = "search"
	res, err := k.ResultsForQuery(context.Background(), q, false)
	if err != nil {
		t.Fatalf("ResultsForQuery: %v", err)
	}
	// seeders==0 row is filtered; the limetorrents row without magnet is skipped
	if len(res.Releases) != 2 {
		t.Fatalf("releases: %d", len(res.Releases))
	}

	r := res.Releases[0]
	if r.Title != "Some Movie 2020 1080p WEB-DL x264-GRP" {
		t.Fatalf("title: %q", r.Title)
	}
	if r.Guid == nil || r.Guid.String() != "https://knaben.org/release/some-movie" {
		t.Fatalf("guid: %v", r.Guid)
	}
	if r.Details == nil || r.Details.String() != r.Guid.String() {
		t.Fatalf("details: %v", r.Details)
	}
	if r.Link == nil || !strings.HasSuffix(r.Link.String(), ".torrent") {
		t.Fatalf("link: %v", r.Link)
	}
	if r.MagnetUri == nil || !strings.HasPrefix(r.MagnetUri.String(), "magnet:?xt=urn:btih:abcdef01") {
		t.Fatalf("magnet: %v", r.MagnetUri)
	}
	if r.InfoHash != "abcdef0123456789abcdef0123456789abcdef01" {
		t.Fatalf("infohash: %q", r.InfoHash)
	}
	if r.Size == nil || *r.Size != 2147483648 {
		t.Fatalf("size: %v", r.Size)
	}
	if r.Seeders == nil || *r.Seeders != 25 {
		t.Fatalf("seeders: %v", r.Seeders)
	}
	if r.Peers == nil || *r.Peers != 30 {
		t.Fatalf("peers: %v", r.Peers)
	}
	if r.PublishDate.Format("2006-01-02 15:04") != "2024-05-01 12:00" {
		t.Fatalf("publish date: %v", r.PublishDate)
	}
	if r.Description != "The Pirate Bay" {
		t.Fatalf("description: %q", r.Description)
	}
	found := false
	for _, id := range r.Category {
		if id == 2040 { // Movies HD
			found = true
		}
	}
	if !found {
		t.Fatalf("category: %v, want it to contain 2040", r.Category)
	}
	found = false
	for _, id := range r.Category {
		if id == 2000 { // Movies parent
			found = true
		}
	}
	if !found {
		t.Fatalf("category: %v, want it to contain 2000", r.Category)
	}
	if r.DownloadVolumeFactor == nil || *r.DownloadVolumeFactor != 0 {
		t.Fatalf("dvf: %v", r.DownloadVolumeFactor)
	}
	if r.UploadVolumeFactor == nil || *r.UploadVolumeFactor != 1 {
		t.Fatalf("uvf: %v", r.UploadVolumeFactor)
	}

	// second row: date without TZ gets "+01:00" appended upstream
	r2 := res.Releases[1]
	if r2.PublishDate.Format("2006-01-02 15:04") != "2024-05-02 08:30" {
		t.Fatalf("publish date 2: %v", r2.PublishDate)
	}
	found = false
	for _, id := range r2.Category {
		if id == 5040 { // TV HD
			found = true
		}
	}
	if !found {
		t.Fatalf("category 2: %v, want it to contain 5040", r2.Category)
	}
}

func TestKnabenUnexpectedStatus(t *testing.T) {
	// 4xx reaches the parser untouched (the client stack only intercepts
	// 429/5xx-site-down statuses)
	site := newKnabenSite(t, http.StatusBadRequest, `oops`)
	k := newTestKnaben(t, site)

	q := models.NewTorznabQuery()
	q.QueryType = "search"
	_, err := k.ResultsForQuery(context.Background(), q, false)
	if err == nil || !strings.Contains(err.Error(), "Unexpected response status '400'") {
		t.Fatalf("err: %v", err)
	}
}

func TestKnabenParseError(t *testing.T) {
	site := newKnabenSite(t, http.StatusOK, `{"Hits": [{"Title": "x", "Seeders": 1, "Date": "not-a-date"}]}`)
	k := newTestKnaben(t, site)

	q := models.NewTorznabQuery()
	q.QueryType = "search"
	_, err := k.ResultsForQuery(context.Background(), q, false)
	if err == nil {
		t.Fatal("expected parse error")
	}
	var ie *indexer.IndexerError
	if !errors.As(err, &ie) || ie.Kind != indexer.KindParse {
		t.Fatalf("err: %v", err)
	}
}
