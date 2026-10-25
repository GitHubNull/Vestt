package manual

import (
	"context"
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

type magneticoSite struct {
	srv   *httptest.Server
	mu    sync.Mutex
	hits  []string
	auth  []string
	serve map[string]string
	code  int
}

func newMagneticoSite(t *testing.T, serve map[string]string) *magneticoSite {
	t.Helper()
	s := &magneticoSite{serve: serve}
	s.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		s.hits = append(s.hits, r.URL.RequestURI())
		s.auth = append(s.auth, r.Header.Get("Authorization"))
		code := s.code
		body, ok := s.serve[r.URL.Path]
		s.mu.Unlock()
		if code != 0 {
			w.WriteHeader(code)
			return
		}
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(s.srv.Close)
	return s
}

func (s *magneticoSite) paths() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.hits...)
}

func (s *magneticoSite) authHeaders() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.auth...)
}

func newTestMagnetico(t *testing.T, site *magneticoSite) *Magnetico {
	t.Helper()
	client, err := httpclient.New(httpclient.Config{}, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("httpclient.New: %v", err)
	}
	ix, err := NewMagnetico(services.Deps{Client: client})
	if err != nil {
		t.Fatalf("NewMagnetico: %v", err)
	}
	m := ix.(*Magnetico)
	m.SiteLinkValue = site.srv.URL + "/"
	m.cfg.Username.Value = "username"
	m.cfg.Password.Value = "password"
	client.SetRequestDelay(m.ID(), 0)
	return m
}

const magneticoTorrentsJSON = `[
  {
    "infoHash": "0123456789abcdef0123456789abcdef01234567",
    "id": 42,
    "name": "Some Ubuntu ISO",
    "size": 2147483648,
    "discoveredOn": 1700000000,
    "nFiles": 12,
    "relevance": 3
  },
  {
    "infoHash": "89abcdef0123456789abcdef0123456789abcdef01",
    "id": 43,
    "name": "Another Release",
    "size": 1073741824,
    "discoveredOn": 1690000000,
    "nFiles": 1,
    "relevance": 1
  }
]`

func TestMagneticoCaps(t *testing.T) {
	m, err := NewMagnetico(services.Deps{})
	if err != nil {
		t.Fatalf("NewMagnetico: %v", err)
	}
	caps := m.Capabilities()
	cases := []struct {
		got  []string
		want []string
	}{
		{caps.TVSearchParams, []string{models.TVParamQ, models.TVParamSeason, models.TVParamEp}},
		{caps.MovieSearchParams, []string{models.MovieParamQ}},
		{caps.MusicSearchParams, []string{models.MusicParamQ}},
		{caps.BookSearchParams, []string{models.BookParamQ}},
	}
	for _, tc := range cases {
		if strings.Join(tc.got, ",") != strings.Join(tc.want, ",") {
			t.Fatalf("search params: got %v want %v", tc.got, tc.want)
		}
	}
	if got := caps.Categories.MapTrackerCatToNewznab("1"); !containsInt(got, models.CatOther.ID) {
		t.Fatalf("category mapping 1 → %v, want it to contain %d", got, models.CatOther.ID)
	}
}

func TestMagneticoConfigForm(t *testing.T) {
	m, err := NewMagnetico(services.Deps{})
	if err != nil {
		t.Fatalf("NewMagnetico: %v", err)
	}
	items, err := m.ConfigurationForSetup(context.Background())
	if err != nil {
		t.Fatalf("ConfigurationForSetup: %v", err)
	}
	var ids []string
	for _, it := range items {
		ids = append(ids, it["id"].(string))
	}
	want := []string{
		"sitelink", "username", "password", "",
		"sortrequestedfromsite", "orderrequestedfromsite", "tags",
	}
	if strings.Join(ids, ",") != strings.Join(want, ",") {
		t.Fatalf("config ids:\n got: %#v\nwant: %#v", ids, want)
	}
	// upstream defaults: sort "discovered" (upstream quirk: the default value
	// is a display label, not an option key), order "false".
	if items[4]["value"] != "discovered" || items[5]["value"] != "false" {
		t.Fatalf("select defaults: %v %v", items[4]["value"], items[5]["value"])
	}
	sortOpts, ok := items[4]["options"].(map[string]any)
	if !ok || sortOpts["DISCOVERED_ON"] != "discovered" || sortOpts["TOTAL_SIZE"] != "size" ||
		sortOpts["N_FILES"] != "files" || sortOpts["RELEVANCE"] != "relevance" {
		t.Fatalf("sort options: %v", items[4]["options"])
	}
	orderOpts, ok := items[5]["options"].(map[string]any)
	if !ok || orderOpts["false"] != "desc" || orderOpts["true"] != "asc" {
		t.Fatalf("order options: %v", items[5]["options"])
	}
}

func TestMagneticoRequestAndParse(t *testing.T) {
	site := newMagneticoSite(t, map[string]string{
		"/api/v0.1/torrents": magneticoTorrentsJSON,
	})
	m := newTestMagnetico(t, site)

	res, err := m.ResultsForQuery(context.Background(), &models.TorznabQuery{QueryType: "search", IsTest: true}, false)
	if err != nil {
		t.Fatalf("ResultsForQuery: %v", err)
	}
	if len(res.Releases) != 2 {
		t.Fatalf("releases: %d", len(res.Releases))
	}
	r := res.Releases[0]
	if r.Title != "Some Ubuntu ISO" {
		t.Fatalf("title: %q", r.Title)
	}
	if r.InfoHash != "0123456789abcdef0123456789abcdef01234567" {
		t.Fatalf("infohash: %q", r.InfoHash)
	}
	if r.Size == nil || *r.Size != 2147483648 {
		t.Fatalf("size: %v", r.Size)
	}
	if r.Files == nil || *r.Files != 12 {
		t.Fatalf("files: %v", r.Files)
	}
	if r.Seeders == nil || *r.Seeders != 1 {
		t.Fatalf("seeders (hardcoded 1 upstream): %v", r.Seeders)
	}
	if r.Peers == nil || *r.Peers != 1 {
		t.Fatalf("peers (hardcoded 1 upstream): %v", r.Peers)
	}
	if r.PublishDate.Unix() != 1700000000 {
		t.Fatalf("publish date: %v", r.PublishDate)
	}
	if !containsInt(r.Category, models.CatOther.ID) {
		t.Fatalf("category: %v, want it to contain %d", r.Category, models.CatOther.ID)
	}
	if r.Details == nil || r.Details.Path != "/torrents/0123456789abcdef0123456789abcdef01234567" {
		t.Fatalf("details: %v", r.Details)
	}
	if r.Guid == nil || r.Guid.String() != r.Details.String() {
		t.Fatalf("guid: %v details: %v", r.Guid, r.Details)
	}
	if r.DownloadVolumeFactor == nil || *r.DownloadVolumeFactor != 0 {
		t.Fatalf("dvf: %v", r.DownloadVolumeFactor)
	}
	if r.UploadVolumeFactor == nil || *r.UploadVolumeFactor != 1 {
		t.Fatalf("uvf: %v", r.UploadVolumeFactor)
	}
	if r.Link != nil {
		t.Fatalf("link must be nil upstream (magnet-only indexer): %v", r.Link)
	}

	paths := site.paths()
	if len(paths) != 1 || paths[0] != "/api/v0.1/torrents?query=&orderBy=discovered&ascending=false&limit=100" {
		t.Fatalf("paths: %v", paths)
	}
	auth := site.authHeaders()
	if len(auth) != 1 || auth[0] != "Basic dXNlcm5hbWU6cGFzc3dvcmQ=" {
		t.Fatalf("authorization: %v", auth)
	}
}

func TestMagneticoSearchTermAndSortOrderParams(t *testing.T) {
	site := newMagneticoSite(t, map[string]string{
		"/api/v0.1/torrents": `[]`,
	})
	m := newTestMagnetico(t, site)
	m.cfg.Sort.Value = "TOTAL_SIZE"
	m.cfg.Order.Value = "true"

	q := models.NewTorznabQuery()
	q.QueryType = "search"
	q.SearchTerm = "some query"
	res, err := m.ResultsForQuery(context.Background(), q, false)
	if err != nil {
		t.Fatalf("ResultsForQuery: %v", err)
	}
	if len(res.Releases) != 0 {
		t.Fatalf("releases: %d", len(res.Releases))
	}
	paths := site.paths()
	want := "/api/v0.1/torrents?query=some+query&orderBy=TOTAL_SIZE&ascending=true&limit=100"
	if len(paths) != 1 || paths[0] != want {
		t.Fatalf("paths:\n got: %v\nwant: %v", paths, want)
	}
}

func TestMagneticoNonOKStatus(t *testing.T) {
	site := newMagneticoSite(t, map[string]string{})
	site.code = http.StatusUnauthorized
	m := newTestMagnetico(t, site)

	_, err := m.ResultsForQuery(context.Background(), &models.TorznabQuery{QueryType: "search", IsTest: true}, false)
	if err == nil || !strings.Contains(err.Error(), "Error code: Unauthorized") {
		t.Fatalf("err: %v", err)
	}
}

func TestMagneticoParseError(t *testing.T) {
	site := newMagneticoSite(t, map[string]string{
		"/api/v0.1/torrents": `not json`,
	})
	m := newTestMagnetico(t, site)

	_, err := m.ResultsForQuery(context.Background(), &models.TorznabQuery{QueryType: "search", IsTest: true}, false)
	ierr, ok := err.(*indexer.IndexerError)
	if !ok || ierr.Kind != indexer.KindParse || ierr.Message != "Parse error" {
		t.Fatalf("err: %v", err)
	}
}

func TestMagneticoApplyConfiguration(t *testing.T) {
	site := newMagneticoSite(t, map[string]string{
		"/api/v0.1/torrents": magneticoTorrentsJSON,
	})
	m := newTestMagnetico(t, site)

	status, err := m.ApplyConfiguration(context.Background(), []indexer.ConfigItem{
		{ID: "sitelink", Value: site.srv.URL + "/"},
		{ID: "username", Value: "username"},
		{ID: "password", Value: "password"},
	})
	if err != nil {
		t.Fatalf("ApplyConfiguration: %v", err)
	}
	if status != indexer.ConfigCompleted {
		t.Fatalf("status: %v", status)
	}
	if !m.IsConfigured() {
		t.Fatal("must be configured after successful apply")
	}
	if m.SiteLink() != site.srv.URL+"/" {
		t.Fatalf("site link: %q", m.SiteLink())
	}
	if len(site.paths()) != 1 || !strings.HasPrefix(site.paths()[0], "/api/v0.1/torrents?") {
		t.Fatalf("paths: %v", site.paths())
	}
}

func TestMagneticoApplyConfigurationError(t *testing.T) {
	site := newMagneticoSite(t, map[string]string{})
	site.code = http.StatusUnauthorized
	m := newTestMagnetico(t, site)

	_, err := m.ApplyConfiguration(context.Background(), []indexer.ConfigItem{
		{ID: "sitelink", Value: site.srv.URL + "/"},
	})
	if err == nil || !strings.Contains(err.Error(), "Error code: Unauthorized") {
		t.Fatalf("err: %v", err)
	}
	if m.IsConfigured() {
		t.Fatal("must not be configured after failure")
	}
}
