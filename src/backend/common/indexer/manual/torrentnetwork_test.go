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

type torrentNetworkSite struct {
	srv    *httptest.Server
	mu     sync.Mutex
	hits   []string
	bodies map[string]string
	// authOK=false makes /api/auth answer with a non-JSON body
	authOK bool
	// passkey returned by /api/curuser
	passkey string
}

func newTorrentNetworkSite(t *testing.T) *torrentNetworkSite {
	t.Helper()
	s := &torrentNetworkSite{
		bodies:  map[string]string{},
		authOK:  true,
		passkey: "pk42",
	}
	s.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		s.hits = append(s.hits, r.Method+" "+r.URL.RequestURI())
		s.mu.Unlock()
		switch r.URL.Path {
		case "/api/auth":
			if !s.authOK {
				w.Header().Set("Content-Type", "text/plain")
				_, _ = w.Write([]byte("Invalid credentials"))
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"token":"tok123"}`))
		case "/api/curuser":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"passkey":"` + s.passkey + `"}`))
		default:
			s.mu.Lock()
			body, ok := s.bodies[r.URL.Path]
			s.mu.Unlock()
			if !ok {
				http.NotFound(w, r)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(body))
		}
	}))
	t.Cleanup(s.srv.Close)
	return s
}

func (s *torrentNetworkSite) paths() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.hits...)
}

func newTestTorrentNetwork(t *testing.T, site *torrentNetworkSite) *TorrentNetwork {
	t.Helper()
	client, err := httpclient.New(httpclient.Config{}, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("httpclient.New: %v", err)
	}
	ix, err := NewTorrentNetwork(services.Deps{Client: client})
	if err != nil {
		t.Fatalf("NewTorrentNetwork: %v", err)
	}
	tn := ix.(*TorrentNetwork)
	tn.SiteLinkValue = site.srv.URL + "/"
	client.SetRequestDelay(tn.ID(), 0)
	return tn
}

const torrentNetworkBrowseJSON = `{
  "data": [
    ["24","Movie GER SD 2024",101,1700000000,0,2147483648,10,2,75,"Action,Adventure",2,100,0,"",0],
    ["18","Movie GER 720p",102,1700000100,0,1073741824,3,1,80,"Drama",1,42,0,"",0],
    ["27","Series GER SD S01E01",103,1700000200,0,536870912,5,0,70,"",0,7,0,"",0]
  ]
}`

func TestTorrentNetworkCaps(t *testing.T) {
	ix, err := NewTorrentNetwork(services.Deps{})
	if err != nil {
		t.Fatalf("NewTorrentNetwork: %v", err)
	}
	caps := ix.Capabilities()
	wantTV := []string{models.TVParamQ, models.TVParamSeason, models.TVParamEp, models.TVParamGenre}
	if strings.Join(caps.TVSearchParams, ",") != strings.Join(wantTV, ",") {
		t.Fatalf("TVSearchParams: %v", caps.TVSearchParams)
	}
	wantMovie := []string{models.MovieParamQ, models.MovieParamGenre}
	if strings.Join(caps.MovieSearchParams, ",") != strings.Join(wantMovie, ",") {
		t.Fatalf("MovieSearchParams: %v", caps.MovieSearchParams)
	}
	if got := caps.Categories.MapTrackerCatToNewznab("24"); !containsInt(got, models.CatMoviesSD.ID) {
		t.Fatalf("cat 24: %v", got)
	}
	if got := caps.Categories.MapTrackerCatToNewznab("33"); !containsInt(got, models.CatXXX.ID) {
		t.Fatalf("cat 33: %v", got)
	}
}

func TestTorrentNetworkConfigForm(t *testing.T) {
	ix, err := NewTorrentNetwork(services.Deps{})
	if err != nil {
		t.Fatalf("NewTorrentNetwork: %v", err)
	}
	items, err := ix.ConfigurationForSetup(context.Background())
	if err != nil {
		t.Fatalf("ConfigurationForSetup: %v", err)
	}
	var ids []string
	for _, it := range items {
		ids = append(ids, it["id"].(string))
	}
	// token/passkey are hidden items → excluded from the setup form; "" is
	// the empty-named Instructions item that ships with upstream
	// ConfigurationDataBasicLogin (ConfigurationDataBasicLogin.cs:13-21); ids
	// are name-derived (ConfigurationData.cs:105), so the AddDynamic key
	// "freeleech" is only a lookup key and the id is "filterfreeleechonly".
	want := []string{"sitelink", "username", "password", "", "filterfreeleechonly", "accountinactivity", "tags"}
	if strings.Join(ids, "|") != strings.Join(want, "|") {
		t.Fatalf("config ids: %v", ids)
	}
}

func TestTorrentNetworkApplyConfiguration(t *testing.T) {
	site := newTorrentNetworkSite(t)
	tn := newTestTorrentNetwork(t, site)

	status, err := tn.ApplyConfiguration(context.Background(), []indexer.ConfigItem{
		{ID: "sitelink", Value: site.srv.URL + "/"},
		{ID: "username", Value: "user"},
		{ID: "password", Value: "pass"},
	})
	if err != nil {
		t.Fatalf("ApplyConfiguration: %v", err)
	}
	if status != indexer.ConfigRequiresTesting {
		t.Fatalf("status: %v", status)
	}
	if !tn.IsConfigured() {
		t.Fatal("must be configured")
	}
	if tn.passkey != "pk42" {
		t.Fatalf("passkey: %q", tn.passkey)
	}
	if tn.cfg.Token.Value != "tok123" || tn.cfg.Passkey.Value != "pk42" {
		t.Fatalf("hidden items not stored: token=%q passkey=%q", tn.cfg.Token.Value, tn.cfg.Passkey.Value)
	}
	if tn.apiHeaders["Authorization"] != "tok123" {
		t.Fatalf("authorization header: %v", tn.apiHeaders)
	}

	paths := site.paths()
	if len(paths) != 2 || paths[0] != "POST /api/auth" || paths[1] != "POST /api/curuser" {
		t.Fatalf("paths: %v", paths)
	}
}

func TestTorrentNetworkLoginFailPath(t *testing.T) {
	site := newTorrentNetworkSite(t)
	site.authOK = false
	tn := newTestTorrentNetwork(t, site)

	_, err := tn.ApplyConfiguration(context.Background(), []indexer.ConfigItem{
		{ID: "sitelink", Value: site.srv.URL + "/"},
		{ID: "username", Value: "user"},
		{ID: "password", Value: "pass"},
	})
	var ie *indexer.IndexerError
	if !errors.As(err, &ie) || ie.Kind != indexer.KindConfig {
		t.Fatalf("err: %v", err)
	}
	if ie.Message != "Invalid credentials" {
		t.Fatalf("message: %q", ie.Message)
	}
	if tn.IsConfigured() {
		t.Fatal("must not be configured after failure")
	}
}

func TestTorrentNetworkEmptyPasskey(t *testing.T) {
	site := newTorrentNetworkSite(t)
	site.passkey = ""
	tn := newTestTorrentNetwork(t, site)

	_, err := tn.ApplyConfiguration(context.Background(), []indexer.ConfigItem{
		{ID: "sitelink", Value: site.srv.URL + "/"},
		{ID: "username", Value: "user"},
		{ID: "password", Value: "pass"},
	})
	// IndexerError.Error() renders "Exception (torrentnetwork): <message>"
	// (upstream IndexerException formatting); the upstream payload is
	// "got empty passkey: <curuser json>" (TorrentNetwork.cs:180-181).
	var ie *indexer.IndexerError
	if !errors.As(err, &ie) || ie.Kind != indexer.KindConfig || !strings.Contains(ie.Message, "got empty passkey: ") {
		t.Fatalf("err: %v", err)
	}
}

func TestTorrentNetworkSearchRequestAndParse(t *testing.T) {
	site := newTorrentNetworkSite(t)
	site.bodies["/api/browse"] = torrentNetworkBrowseJSON
	tn := newTestTorrentNetwork(t, site)

	if _, err := tn.ApplyConfiguration(context.Background(), []indexer.ConfigItem{
		{ID: "sitelink", Value: site.srv.URL + "/"},
		{ID: "username", Value: "user"},
		{ID: "password", Value: "pass"},
	}); err != nil {
		t.Fatalf("ApplyConfiguration: %v", err)
	}

	// tvsearch scoped to MoviesSD (2030 → tracker 24,22). Upstream
	// BaseIndexer.FilterResults drops releases whose categories do not
	// intersect the expanded query cats; [2030] is a child cat so it expands
	// to itself (TorznabCapabilitiesCategories.cs:131-153), keeping only the
	// tracker-24 row of the fixture (BaseIndexer.cs:169-183).
	q := models.NewTorznabQuery()
	q.QueryType = "tvsearch"
	q.SearchTerm = "film"
	q.Categories = []int{models.CatMoviesSD.ID}
	res, err := tn.ResultsForQuery(context.Background(), q, false)
	if err != nil {
		t.Fatalf("ResultsForQuery: %v", err)
	}
	if len(res.Releases) != 1 {
		t.Fatalf("releases: %d", len(res.Releases))
	}

	paths := site.paths()
	if len(paths) != 3 {
		t.Fatalf("paths: %v", paths)
	}
	want := "GET /api/browse?orderC=4&orderD=desc&start=0&length=100&search=film&cats=24%2C22"
	if paths[2] != want {
		t.Fatalf("browse path: %q want %q", paths[2], want)
	}

	r := res.Releases[0]
	if r.Title != "Movie GER SD 2024" {
		t.Fatalf("title: %q", r.Title)
	}
	if !containsInt(r.Category, models.CatMoviesSD.ID) {
		t.Fatalf("category: %v", r.Category)
	}
	if r.Link == nil || !strings.HasSuffix(r.Link.Path, "/sdownload/101/pk42") {
		t.Fatalf("link: %v", r.Link)
	}
	if r.Details == nil || !strings.HasSuffix(r.Details.Path, "/torrent/101") {
		t.Fatalf("details: %v", r.Details)
	}
	if r.Size == nil || *r.Size != 2147483648 {
		t.Fatalf("size: %v", r.Size)
	}
	if r.Seeders == nil || *r.Seeders != 10 || r.Peers == nil || *r.Peers != 12 {
		t.Fatalf("seeders/peers: %v %v", r.Seeders, r.Peers)
	}
	if r.Grabs == nil || *r.Grabs != 100 {
		t.Fatalf("grabs: %v", r.Grabs)
	}
	if r.Description != "Genres: Action,Adventure" {
		t.Fatalf("description: %q", r.Description)
	}
	if len(r.Genres) != 2 || r.Genres[0] != "Action" || r.Genres[1] != "Adventure" {
		t.Fatalf("genres: %v", r.Genres)
	}
	if r.DownloadVolumeFactor == nil || *r.DownloadVolumeFactor != 0 {
		t.Fatalf("dvf (flag 2 → 0): %v", r.DownloadVolumeFactor)
	}
	if r.MinimumRatio == nil || *r.MinimumRatio != 0.8 {
		t.Fatalf("minimum ratio: %v", r.MinimumRatio)
	}
	if r.MinimumSeedTime == nil || *r.MinimumSeedTime != 172800 {
		t.Fatalf("minimum seed time: %v", r.MinimumSeedTime)
	}
	if r.PublishDate.Unix() != 1700000000 {
		t.Fatalf("publish date: %v", r.PublishDate)
	}

	// uncategorised search: no category filter → all three fixture rows
	q2 := models.NewTorznabQuery()
	q2.QueryType = "search"
	res2, err := tn.ResultsForQuery(context.Background(), q2, false)
	if err != nil {
		t.Fatalf("ResultsForQuery (browse all): %v", err)
	}
	if len(res2.Releases) != 3 {
		t.Fatalf("releases (browse all): %d", len(res2.Releases))
	}
	// flag 1 → 0.5
	if r2 := res2.Releases[1]; r2.DownloadVolumeFactor == nil || *r2.DownloadVolumeFactor != 0.5 {
		t.Fatalf("dvf row2 (flag 1 → 0.5): %v", r2.DownloadVolumeFactor)
	}
	// empty genres: description empty, Genres is [""] (upstream union quirk)
	r3 := res2.Releases[2]
	if r3.Description != "" {
		t.Fatalf("description row3: %q", r3.Description)
	}
	if len(r3.Genres) != 1 || r3.Genres[0] != "" {
		t.Fatalf("genres row3 (upstream [\"\"] quirk): %v", r3.Genres)
	}
	if r3.DownloadVolumeFactor == nil || *r3.DownloadVolumeFactor != 1 {
		t.Fatalf("dvf row3: %v", r3.DownloadVolumeFactor)
	}
}

func TestTorrentNetworkSearchWithoutLogin(t *testing.T) {
	site := newTorrentNetworkSite(t)
	site.bodies["/api/browse"] = torrentNetworkBrowseJSON
	tn := newTestTorrentNetwork(t, site)

	// credentials posted but ApplyConfiguration never called: perform must
	// re-auth by itself (upstream: passkey empty → ApplyConfiguration(null),
	// TorrentNetwork.cs:224-225)
	if err := tn.ApplyItems([]indexer.ConfigItem{
		{ID: "sitelink", Value: site.srv.URL + "/"},
		{ID: "username", Value: "user"},
		{ID: "password", Value: "pass"},
	}); err != nil {
		t.Fatalf("ApplyItems: %v", err)
	}
	q := models.NewTorznabQuery()
	q.QueryType = "search"
	res, err := tn.ResultsForQuery(context.Background(), q, false)
	if err != nil {
		t.Fatalf("ResultsForQuery: %v", err)
	}
	if len(res.Releases) != 3 {
		t.Fatalf("releases: %d", len(res.Releases))
	}
	paths := site.paths()
	// re-auth (auth + curuser) runs inside perform, then the browse call
	if len(paths) != 3 || paths[0] != "POST /api/auth" || paths[1] != "POST /api/curuser" {
		t.Fatalf("paths: %v", paths)
	}
	// no query → all tracker categories, comma-joined
	if !strings.Contains(paths[2], "cats=") || !strings.Contains(paths[2], "%2C") {
		t.Fatalf("browse path: %q", paths[2])
	}
}

func TestTorrentNetworkErrorField(t *testing.T) {
	site := newTorrentNetworkSite(t)
	site.bodies["/api/browse"] = `{"error":"search too broad"}`
	tn := newTestTorrentNetwork(t, site)

	if _, err := tn.ApplyConfiguration(context.Background(), []indexer.ConfigItem{
		{ID: "sitelink", Value: site.srv.URL + "/"},
		{ID: "username", Value: "user"},
		{ID: "password", Value: "pass"},
	}); err != nil {
		t.Fatalf("ApplyConfiguration: %v", err)
	}
	q := models.NewTorznabQuery()
	q.QueryType = "search"
	_, err := tn.ResultsForQuery(context.Background(), q, false)
	var ie *indexer.IndexerError
	// upstream: the "error" field check sits inside the parse try, so the
	// failure surfaces through OnParseError as "Parse error"
	// (TorrentNetwork.cs:233-234, 291-294)
	if !errors.As(err, &ie) || ie.Kind != indexer.KindParse || ie.Message != "Parse error" {
		t.Fatalf("err: %v", err)
	}
}

func TestTorrentNetworkNonJSONBrowse(t *testing.T) {
	site := newTorrentNetworkSite(t)
	site.bodies["/api/browse"] = `<html>maintenance</html>`
	tn := newTestTorrentNetwork(t, site)

	if _, err := tn.ApplyConfiguration(context.Background(), []indexer.ConfigItem{
		{ID: "sitelink", Value: site.srv.URL + "/"},
		{ID: "username", Value: "user"},
		{ID: "password", Value: "pass"},
	}); err != nil {
		t.Fatalf("ApplyConfiguration: %v", err)
	}
	q := models.NewTorznabQuery()
	q.QueryType = "search"
	_, err := tn.ResultsForQuery(context.Background(), q, false)
	var ie *indexer.IndexerError
	if !errors.As(err, &ie) || ie.Kind != indexer.KindConfig || ie.Message != "<html>maintenance</html>" {
		t.Fatalf("err: %v", err)
	}
}

func TestTorrentNetworkFreeleechFilter(t *testing.T) {
	site := newTorrentNetworkSite(t)
	site.bodies["/api/browse"] = torrentNetworkBrowseJSON
	tn := newTestTorrentNetwork(t, site)

	if _, err := tn.ApplyConfiguration(context.Background(), []indexer.ConfigItem{
		{ID: "sitelink", Value: site.srv.URL + "/"},
		{ID: "username", Value: "user"},
		{ID: "password", Value: "pass"},
	}); err != nil {
		t.Fatalf("ApplyConfiguration: %v", err)
	}
	tn.cfg.Freeleech.Value = true
	q := models.NewTorznabQuery()
	q.QueryType = "search"
	res, err := tn.ResultsForQuery(context.Background(), q, false)
	if err != nil {
		t.Fatalf("ResultsForQuery: %v", err)
	}
	// only the flag==2 (only-up / dvf 0) row survives
	if len(res.Releases) != 1 || *res.Releases[0].DownloadVolumeFactor != 0 {
		t.Fatalf("releases: %+v", res.Releases)
	}
}
