package manual

import (
	"context"
	"errors"
	"fmt"
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

type torrentsCSVSite struct {
	srv *httptest.Server

	mu        sync.Mutex
	hits      []string
	status    int
	nullEntry bool
}

func newTorrentsCSVSite(t *testing.T) *torrentsCSVSite {
	t.Helper()
	s := &torrentsCSVSite{}
	s.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		s.hits = append(s.hits, r.URL.RequestURI())
		status := s.status
		nullEntry := s.nullEntry
		s.mu.Unlock()
		if status != 0 {
			w.WriteHeader(status)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if nullEntry {
			_, _ = fmt.Fprint(w, `{"torrents":[`+torrentsCSVRow1+`,null]}`)
			return
		}
		_, _ = fmt.Fprint(w, `{"torrents":[`+torrentsCSVRow1+","+torrentsCSVRow2+`]}`)
	}))
	t.Cleanup(s.srv.Close)
	return s
}

const torrentsCSVRow1 = `{
	"infohash": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
	"name": "Old Linux ISO",
	"size_bytes": 2147483648,
	"created_unix": 1600000000,
	"leechers": 2,
	"seeders": 8,
	"completed": 30
}`

const torrentsCSVRow2 = `{
	"infohash": "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
	"name": "New Linux ISO",
	"size_bytes": 3221225472,
	"created_unix": 1700000000,
	"leechers": null,
	"seeders": null,
	"completed": null
}`

func (s *torrentsCSVSite) paths() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.hits...)
}

func (s *torrentsCSVSite) setStatus(status int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.status = status
}

func (s *torrentsCSVSite) setNullEntry(v bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.nullEntry = v
}

func newTestTorrentsCSV(t *testing.T, site *torrentsCSVSite) *TorrentsCSV {
	t.Helper()
	client, err := httpclient.New(httpclient.Config{}, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("httpclient.New: %v", err)
	}
	ix, err := NewTorrentsCSV(services.Deps{Client: client})
	if err != nil {
		t.Fatalf("NewTorrentsCSV: %v", err)
	}
	tv := ix.(*TorrentsCSV)
	tv.SiteLinkValue = site.srv.URL + "/"
	client.SetRequestDelay(tv.ID(), 0)
	return tv
}

func TestTorrentsCSVCaps(t *testing.T) {
	ix, err := NewTorrentsCSV(services.Deps{})
	if err != nil {
		t.Fatalf("NewTorrentsCSV: %v", err)
	}
	caps := ix.Capabilities()
	wantTV := []string{"q", "season", "ep"}
	if strings.Join(caps.TVSearchParams, ",") != strings.Join(wantTV, ",") {
		t.Fatalf("TVSearchParams: %v", caps.TVSearchParams)
	}
	if strings.Join(caps.MovieSearchParams, ",") != "q" {
		t.Fatalf("MovieSearchParams: %v", caps.MovieSearchParams)
	}
	if got := caps.Categories.MapTrackerCatToNewznab("1"); len(got) != 1 || got[0] != models.CatOther.ID {
		t.Fatalf("MapTrackerCatToNewznab(1) = %v", got)
	}
}

func TestTorrentsCSVConfigForm(t *testing.T) {
	ix, err := NewTorrentsCSV(services.Deps{})
	if err != nil {
		t.Fatalf("NewTorrentsCSV: %v", err)
	}
	items, err := ix.ConfigurationForSetup(context.Background())
	if err != nil {
		t.Fatalf("ConfigurationForSetup: %v", err)
	}
	var ids []string
	for _, it := range items {
		ids = append(ids, it["id"].(string))
	}
	// the self-hosted Site Link is the only setup item besides tags
	want := []string{"sitelink", "tags"}
	if fmt.Sprintf("%v", ids) != fmt.Sprintf("%v", want) {
		t.Fatalf("ids = %v, want %v", ids, want)
	}
}

// TestTorrentsCSVSiteLinkRouting: overriding the site_link config item must
// route every request to the configured (self-hosted) instance. The
// indexer is built with the default torrents-csv.com SiteLinkValue, so any
// hit on the httptest server proves the config item drives the URLs.
func TestTorrentsCSVSiteLinkRouting(t *testing.T) {
	site := newTorrentsCSVSite(t)
	client, err := httpclient.New(httpclient.Config{}, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("httpclient.New: %v", err)
	}
	ix, err := NewTorrentsCSV(services.Deps{Client: client})
	if err != nil {
		t.Fatalf("NewTorrentsCSV: %v", err)
	}
	tv := ix.(*TorrentsCSV)
	client.SetRequestDelay(tv.ID(), 0)

	status, err := tv.ApplyConfiguration(context.Background(), []indexer.ConfigItem{
		{ID: "sitelink", Value: site.srv.URL + "/"},
	})
	if err != nil {
		t.Fatalf("ApplyConfiguration: %v", err)
	}
	if status != indexer.ConfigCompleted || !tv.IsConfigured() {
		t.Fatalf("status/configured: %v/%v", status, tv.IsConfigured())
	}

	if got := tv.baseURL(); got != site.srv.URL+"/" {
		t.Fatalf("baseURL after config: %q", got)
	}
	paths := site.paths()
	// the empty self-test query returns the synthetic [NOT IMPLEMENTED]
	// release without any network request
	if len(paths) != 0 {
		t.Fatalf("no request expected for the empty self-test query: %v", paths)
	}

	// an actual search must hit the configured instance
	q := models.NewTorznabQuery()
	q.QueryType = "search"
	q.SearchTerm = "linux"
	if _, err := tv.ResultsForQuery(context.Background(), q, false); err != nil {
		t.Fatalf("ResultsForQuery: %v", err)
	}
	paths = site.paths()
	if len(paths) != 1 || paths[0] != "/service/search?size=100&q=linux" {
		t.Fatalf("paths: %v", paths)
	}
}

func TestTorrentsCSVSearchRequestAndParse(t *testing.T) {
	site := newTorrentsCSVSite(t)
	tv := newTestTorrentsCSV(t, site)

	releases, err := tv.PerformQuery(context.Background(), &models.TorznabQuery{
		QueryType: "search", SearchTerm: "linux",
	})
	if err != nil {
		t.Fatalf("PerformQuery: %v", err)
	}
	if len(releases) != 2 {
		t.Fatalf("releases: %d", len(releases))
	}

	// ordered by publish date descending (New first)
	r0 := releases[0]
	if r0.Title != "New Linux ISO" {
		t.Fatalf("title: %q", r0.Title)
	}
	if r0.InfoHash != "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb" {
		t.Fatalf("infohash: %q", r0.InfoHash)
	}
	if r0.Guid == nil || r0.Guid.String() != "magnet:?xt=urn:btih:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb" {
		t.Fatalf("guid: %v", r0.Guid)
	}
	if r0.Size == nil || *r0.Size != 3221225472 {
		t.Fatalf("size: %v", r0.Size)
	}
	if r0.Seeders == nil || *r0.Seeders != 0 || r0.Peers == nil || *r0.Peers != 0 {
		t.Fatalf("seeders/peers (null → 0): %v/%v", r0.Seeders, r0.Peers)
	}
	if r0.Grabs == nil || *r0.Grabs != 0 {
		t.Fatalf("grabs (null → 0): %v", r0.Grabs)
	}
	if r0.PublishDate.Unix() != 1700000000 {
		t.Fatalf("date: %v", r0.PublishDate)
	}
	if r0.Category == nil || len(r0.Category) != 1 || r0.Category[0] != models.CatOther.ID {
		t.Fatalf("category: %v", r0.Category)
	}
	if r0.DownloadVolumeFactor == nil || *r0.DownloadVolumeFactor != 0 {
		t.Fatalf("dvf: %v", r0.DownloadVolumeFactor)
	}
	if r0.UploadVolumeFactor == nil || *r0.UploadVolumeFactor != 1 {
		t.Fatalf("uvf: %v", r0.UploadVolumeFactor)
	}
	// upstream interpolates the title raw into new Uri($"{SiteLink}search?q={title}")
	// (TorrentsCSV.cs:145); the Go url.URL keeps the space verbatim in the
	// query, so compare the full String() rather than a "+"-encoded prefix
	if r0.Details == nil || r0.Details.String() != site.srv.URL+"/search?q=New Linux ISO" {
		t.Fatalf("details: %v", r0.Details)
	}

	r1 := releases[1]
	if r1.Title != "Old Linux ISO" {
		t.Fatalf("title[1]: %q", r1.Title)
	}
	if r1.Seeders == nil || *r1.Seeders != 8 || r1.Peers == nil || *r1.Peers != 10 {
		t.Fatalf("seeders/peers[1]: %v/%v", r1.Seeders, r1.Peers)
	}
	if r1.Grabs == nil || *r1.Grabs != 30 {
		t.Fatalf("grabs[1]: %v", r1.Grabs)
	}
}

func TestTorrentsCSVShortSearchIsEmpty(t *testing.T) {
	site := newTorrentsCSVSite(t)
	tv := newTestTorrentsCSV(t, site)

	// search needs at least 3 characters
	releases, err := tv.PerformQuery(context.Background(), &models.TorznabQuery{
		QueryType: "search", SearchTerm: "ab",
	})
	if err != nil {
		t.Fatalf("PerformQuery: %v", err)
	}
	if len(releases) != 0 {
		t.Fatalf("releases: %d", len(releases))
	}
	if len(site.paths()) != 0 {
		t.Fatalf("no request expected: %v", site.paths())
	}
}

func TestTorrentsCSVEmptySearchSyntheticRelease(t *testing.T) {
	site := newTorrentsCSVSite(t)
	tv := newTestTorrentsCSV(t, site)

	releases, err := tv.PerformQuery(context.Background(), &models.TorznabQuery{QueryType: "search"})
	if err != nil {
		t.Fatalf("PerformQuery: %v", err)
	}
	if len(releases) != 1 {
		t.Fatalf("releases: %d", len(releases))
	}
	r := releases[0]
	if r.Title != "[NOT IMPLEMENTED] Empty search is unsupported in this indexer" {
		t.Fatalf("title: %q", r.Title)
	}
	if r.MagnetUri == nil || !strings.HasPrefix(r.MagnetUri.String(), "magnet:?xt=urn:btih:3333") {
		t.Fatalf("magnet: %v", r.MagnetUri)
	}
	if len(site.paths()) != 0 {
		t.Fatalf("no request expected: %v", site.paths())
	}
}

func TestTorrentsCSV401IsAuth(t *testing.T) {
	site := newTorrentsCSVSite(t)
	site.setStatus(http.StatusUnauthorized)
	tv := newTestTorrentsCSV(t, site)

	_, err := tv.PerformQuery(context.Background(), &models.TorznabQuery{
		QueryType: "search", SearchTerm: "linux",
	})
	var ie *indexer.IndexerError
	if !errors.As(err, &ie) || ie.Kind != indexer.KindAuth {
		t.Fatalf("err: %v", err)
	}
}

func TestTorrentsCSVNullEntryIsParseError(t *testing.T) {
	site := newTorrentsCSVSite(t)
	site.setNullEntry(true)
	tv := newTestTorrentsCSV(t, site)

	_, err := tv.PerformQuery(context.Background(), &models.TorznabQuery{
		QueryType: "search", SearchTerm: "linux",
	})
	var ie *indexer.IndexerError
	if !errors.As(err, &ie) || ie.Kind != indexer.KindParse {
		t.Fatalf("err: %v", err)
	}
}

func TestTorrentsCSVApplyConfiguration(t *testing.T) {
	site := newTorrentsCSVSite(t)
	tv := newTestTorrentsCSV(t, site)

	// the empty self-test query returns the synthetic release, so
	// configuration succeeds without network access
	status, err := tv.ApplyConfiguration(context.Background(), []indexer.ConfigItem{
		{ID: "sitelink", Value: site.srv.URL + "/"},
	})
	if err != nil {
		t.Fatalf("ApplyConfiguration: %v", err)
	}
	if status != indexer.ConfigCompleted || !tv.IsConfigured() {
		t.Fatalf("status/configured: %v/%v", status, tv.IsConfigured())
	}
	if len(site.paths()) != 0 {
		t.Fatalf("no request expected: %v", site.paths())
	}
}
