package manual

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"

	"vestt/common/httpclient"
	"vestt/common/indexer"
	"vestt/common/models"
	"vestt/common/services"
)

type epubLibreSite struct {
	srv     *httptest.Server
	mu      sync.Mutex
	hits    []string
	headers []http.Header
	serve   map[string]string
}

func newEpubLibreSite(t *testing.T, serve map[string]string) *epubLibreSite {
	t.Helper()
	s := &epubLibreSite{serve: serve}
	s.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		s.hits = append(s.hits, r.URL.RequestURI())
		s.headers = append(s.headers, r.Header.Clone())
		body, ok := s.serve[r.URL.Path]
		s.mu.Unlock()
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

func (s *epubLibreSite) paths() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.hits...)
}

func newTestEpubLibre(t *testing.T, site *epubLibreSite) *EpubLibre {
	t.Helper()
	client, err := httpclient.New(httpclient.Config{}, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("httpclient.New: %v", err)
	}
	ix, err := NewEpubLibre(services.Deps{Client: client})
	if err != nil {
		t.Fatalf("NewEpubLibre: %v", err)
	}
	e := ix.(*EpubLibre)
	e.SiteLinkValue = site.srv.URL + "/"
	client.SetRequestDelay(e.ID(), 0)
	return e
}

// epbRowTemplate is one catalog item; the data-content tooltip carries the
// language class and the four description values (rev/pages/rating/likes).
const epbRowTemplate = `<div class="span2">
  <h2>%s</h2><h1>%s</h1>
  <img id="catalog" src="/img/cover%03d.jpg"/>
  <a href="/libro/%03d" data-content="<div class='pull-right sprite idioma_%s'></div><div class='row-fluid'><div>3</div><div>256</div><div>8,5</div><div>12</div></div>">link</a>
</div>`

func epbCatalogJSON(rows ...string) string {
	return `{"contenido":"` + epbEscapeJSON(strings.Join(rows, "")) + `"}`
}

// epbEscapeJSON renders s as a JSON string literal (quotes, backslashes and
// control characters escaped).
func epbEscapeJSON(s string) string {
	q := strconv.Quote(s)
	return q[1 : len(q)-1]
}

// epbFixturePages builds a full 18-item first page (pagination continues)
// and a short second page (pagination stops).
func epbFixturePages(t *testing.T) map[string]string {
	t.Helper()
	page0 := fmt.Sprintf(epbRowTemplate, "Autor Uno", "Título Primero", 1, 1, "1") +
		fmt.Sprintf(epbRowTemplate, "Autor Dos", "Segundo Libro", 2, 2, "5")
	for i := 3; i <= epubLibreMaxItemsPerPage; i++ {
		page0 += fmt.Sprintf(epbRowTemplate, "Relleno", fmt.Sprintf("Libro %02d", i), i, i, "1")
	}
	page1 := fmt.Sprintf(epbRowTemplate, "Autor Tres", "Tercer Libro", 19, 19, "11")
	return map[string]string{
		"/catalogo/index/0/nuevo/todos/sin/todos/--/ajax":  epbCatalogJSON(page0),
		"/catalogo/index/18/nuevo/todos/sin/todos/--/ajax": epbCatalogJSON(page1),
	}
}

// epbFindRelease returns the release whose title contains substr.
func epbFindRelease(t *testing.T, releases []*models.ReleaseInfo, substr string) *models.ReleaseInfo {
	t.Helper()
	for _, r := range releases {
		if strings.Contains(r.Title, substr) {
			return r
		}
	}
	t.Fatalf("release %q not found in %d releases", substr, len(releases))
	return nil
}

func TestEpubLibreCaps(t *testing.T) {
	ix, err := NewEpubLibre(services.Deps{})
	if err != nil {
		t.Fatalf("NewEpubLibre: %v", err)
	}
	caps := ix.Capabilities()
	if strings.Join(caps.BookSearchParams, ",") != "q" {
		t.Fatalf("BookSearchParams: %v", caps.BookSearchParams)
	}
	if got := caps.Categories.MapTrackerCatToNewznab("1"); len(got) != 1 || got[0] != 7020 {
		t.Fatalf("category 1: %v", got)
	}
	e := ix.(*EpubLibre)
	if e.Language() != "es-ES" || e.Type() != "public" {
		t.Fatalf("lang/type: %s %s", e.Language(), e.Type())
	}
	if e.SiteLink() != "https://www.epublibre.org/" {
		t.Fatalf("sitelink: %s", e.SiteLink())
	}
	links := e.AlternativeSiteLinks()
	if len(links) != 1 || links[0] != "https://www.epublibre.org/" {
		t.Fatalf("alt links: %v", links)
	}
}

func TestEpubLibreConfigForm(t *testing.T) {
	ix, err := NewEpubLibre(services.Deps{})
	if err != nil {
		t.Fatalf("NewEpubLibre: %v", err)
	}
	items, err := ix.ConfigurationForSetup(context.Background())
	if err != nil {
		t.Fatalf("ConfigurationForSetup: %v", err)
	}
	var ids []string
	for _, it := range items {
		ids = append(ids, it["id"].(string))
	}
	// base ConfigurationData only: sitelink + tags (no login items at all)
	if len(ids) != 2 || ids[0] != "sitelink" || ids[1] != "tags" {
		t.Fatalf("config ids: %v", ids)
	}
}

func TestEpubLibreApplyConfigurationAndRecentRequest(t *testing.T) {
	site := newEpubLibreSite(t, epbFixturePages(t))
	e := newTestEpubLibre(t, site)

	_, err := e.ApplyConfiguration(context.Background(), []indexer.ConfigItem{
		{ID: "sitelink", Value: site.srv.URL + "/"},
	})
	if err != nil {
		t.Fatalf("ApplyConfiguration: %v", err)
	}
	if !e.IsConfigured() {
		t.Fatal("must be configured")
	}
	paths := site.paths()
	// recent-torrent scrape stops after the short page: 2 requests
	if len(paths) != 2 ||
		paths[0] != "/catalogo/index/0/nuevo/todos/sin/todos/--/ajax" ||
		paths[1] != "/catalogo/index/18/nuevo/todos/sin/todos/--/ajax" {
		t.Fatalf("paths: %v", paths)
	}
	// the AJAX header must be sent
	site.mu.Lock()
	hdr := site.headers[0].Get("X-Requested-With")
	site.mu.Unlock()
	if hdr != "XMLHttpRequest" {
		t.Fatalf("X-Requested-With: %q", hdr)
	}
}

func TestEpubLibreParseReleaseFields(t *testing.T) {
	site := newEpubLibreSite(t, epbFixturePages(t))
	e := newTestEpubLibre(t, site)

	res, err := e.ResultsForQuery(context.Background(), &models.TorznabQuery{QueryType: "search", IsTest: true}, false)
	if err != nil {
		t.Fatalf("ResultsForQuery: %v", err)
	}
	if len(res.Releases) != epubLibreMaxItemsPerPage+1 {
		t.Fatalf("releases: %d", len(res.Releases))
	}

	r := epbFindRelease(t, res.Releases, "Título Primero")
	if r.Title != "Autor Uno - Título Primero [español] [epub]" {
		t.Fatalf("title: %q", r.Title)
	}
	if r.Description != "Rev: 3 Páginas: 256 Puntación: 8,5 Likes: 12" {
		t.Fatalf("description: %q", r.Description)
	}
	if len(r.Category) != 1 || r.Category[0] != 7020 {
		t.Fatalf("category: %v", r.Category)
	}
	if r.Size == nil || *r.Size != 5242880 {
		t.Fatalf("size: %v", r.Size)
	}
	if r.Seeders == nil || *r.Seeders != 1 || r.Peers == nil || *r.Peers != 2 {
		t.Fatalf("seeders/peers: %v %v", r.Seeders, r.Peers)
	}
	if r.DownloadVolumeFactor == nil || *r.DownloadVolumeFactor != 0 {
		t.Fatalf("dvf: %v", r.DownloadVolumeFactor)
	}
	if r.UploadVolumeFactor == nil || *r.UploadVolumeFactor != 1 {
		t.Fatalf("uvf: %v", r.UploadVolumeFactor)
	}
	if r.Poster == nil || r.Poster.Path != "/img/cover001.jpg" {
		t.Fatalf("poster: %v", r.Poster)
	}
	if r.Details == nil || r.Details.Path != "/libro/001" {
		t.Fatalf("details: %v", r.Details)
	}
	if r.Link == nil || r.Link.String() != r.Details.String() || r.Guid.String() != r.Details.String() {
		t.Fatalf("link/guid: %v %v", r.Link, r.Guid)
	}
	// publish dates decrease by one minute per entry (relative sorting)
	for i := 1; i < len(res.Releases); i++ {
		if !res.Releases[i-1].PublishDate.After(res.Releases[i].PublishDate) {
			t.Fatalf("publish dates not descending at %d", i)
		}
	}

	r3 := epbFindRelease(t, res.Releases, "Tercer Libro")
	if r3.Title != "Autor Tres - Tercer Libro [portugués] [epub]" {
		t.Fatalf("title 3: %q", r3.Title)
	}
	if r3.Poster == nil || r3.Poster.Path != "/img/cover019.jpg" {
		t.Fatalf("poster 3: %v", r3.Poster)
	}
}

func TestEpubLibreSearchRequest(t *testing.T) {
	site := newEpubLibreSite(t, map[string]string{
		// r.URL.Path is percent-DECODED: escapeDataString emits %20
		"/catalogo/index/0/nuevo/todos/sin/todos/don quijote/ajax": epbCatalogJSON(""),
	})
	e := newTestEpubLibre(t, site)

	q := models.NewTorznabQuery()
	q.QueryType = "search"
	q.SearchTerm = "don quijote"
	if _, err := e.ResultsForQuery(context.Background(), q, false); err != nil {
		t.Fatalf("ResultsForQuery: %v", err)
	}
	paths := site.paths()
	// empty result page => pagination stops after the first request
	if len(paths) != 1 || paths[0] != "/catalogo/index/0/nuevo/todos/sin/todos/don%20quijote/ajax" {
		t.Fatalf("paths: %v", paths)
	}
}

func TestEpubLibreCheckTitleMatchWords(t *testing.T) {
	e, err := NewEpubLibre(services.Deps{})
	if err != nil {
		t.Fatalf("NewEpubLibre: %v", err)
	}
	el := e.(*EpubLibre)
	if !el.checkTitleMatchWords("", "anything goes") {
		t.Fatal("empty query must match")
	}
	if !el.checkTitleMatchWords("don quijote", "Don Quijote de la Mancha") {
		t.Fatal("all words present must match")
	}
	if el.checkTitleMatchWords("don quijote", "Don Juan") {
		t.Fatal("missing word must not match")
	}
	// words of 2 chars or less are ignored upstream
	if !el.checkTitleMatchWords("la mancha", "Mancha") {
		t.Fatal("2-letter words are ignored upstream")
	}
	// The ISO-8859-8 byte round-trip mangles accented letters on BOTH sides
	// ('í' is not encodable → '?'), so an accented query matches an
	// accented title (upstream behaviour, kept verbatim)…
	if !el.checkTitleMatchWords("título", "Título Primero") {
		t.Fatal("accented query must match accented title")
	}
	// …while an unaccented query word does NOT match the mangled title word
	// (upstream bug replicated: ISO-8859-8 is Hebrew, it has no Spanish
	// accented vowels).
	if el.checkTitleMatchWords("titulo", "Título Primero") {
		t.Fatal("unaccented query must not match the ISO-8859-8 mangled title")
	}
}

func TestEpubLibreErrorPaths(t *testing.T) {
	// no releases at all => upstream config error
	siteEmpty := newEpubLibreSite(t, map[string]string{
		"/catalogo/index/0/nuevo/todos/sin/todos/--/ajax": epbCatalogJSON(""),
	})
	e := newTestEpubLibre(t, siteEmpty)
	_, err := e.ApplyConfiguration(context.Background(), []indexer.ConfigItem{
		{ID: "sitelink", Value: siteEmpty.srv.URL + "/"},
	})
	if err == nil || err.Error() != "Could not find any release from this URL" {
		t.Fatalf("err: %v", err)
	}

	// garbage JSON => parse error
	siteBroken := newEpubLibreSite(t, map[string]string{
		"/catalogo/index/0/nuevo/todos/sin/todos/--/ajax": `not json at all`,
	})
	e2 := newTestEpubLibre(t, siteBroken)
	_, err = e2.ResultsForQuery(context.Background(), &models.TorznabQuery{QueryType: "search", IsTest: true}, false)
	if err == nil || !strings.Contains(err.Error(), "Parse error") {
		t.Fatalf("err: %v", err)
	}
}

func TestEpubLibreDownload(t *testing.T) {
	magnet := "magnet:?xt=urn:btih:abcdef0123456789abcdef0123456789abcdef01&dn=book"
	site := newEpubLibreSite(t, map[string]string{
		"/libro/001": `<html><body><a id="en_desc" href="` + magnet + `">descargar</a></body></html>`,
		"/libro/002": `<html><body>sobrecarga</body></html>`,
	})
	e := newTestEpubLibre(t, site)

	link, _ := url.Parse(site.srv.URL + "/libro/001")
	got, err := e.Download(context.Background(), link)
	if err != nil {
		t.Fatalf("Download: %v", err)
	}
	if string(got) != magnet {
		t.Fatalf("magnet: %q", got)
	}

	// details page without the magnet anchor => overload error (see the
	// 澄清记录 in epublibre.go about RedirectingTo)
	link2, _ := url.Parse(site.srv.URL + "/libro/002")
	_, err = e.Download(context.Background(), link2)
	if err == nil || err.Error() != "El servidor se encuentra sobrecargado en estos momentos. / The server is currently overloaded." {
		t.Fatalf("err: %v", err)
	}
}
