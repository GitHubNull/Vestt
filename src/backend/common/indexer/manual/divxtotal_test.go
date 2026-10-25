package manual

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"vestt/common/httpclient"
	"vestt/common/indexer"
	"vestt/common/models"
	"vestt/common/services"
)

type divxSite struct {
	srv   *httptest.Server
	mu    sync.Mutex
	hits  []string
	serve map[string]string
}

func newDivxSite(t *testing.T, serve map[string]string) *divxSite {
	t.Helper()
	s := &divxSite{serve: serve}
	s.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		s.hits = append(s.hits, r.URL.RequestURI())
		body, ok := s.serve[r.URL.Path]
		s.mu.Unlock()
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		body = strings.ReplaceAll(body, "@HOST@", s.srv.URL)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(s.srv.Close)
	return s
}

func (s *divxSite) paths() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.hits...)
}

func newTestDivxTotal(t *testing.T, site *divxSite) *DivxTotal {
	t.Helper()
	client, err := httpclient.New(httpclient.Config{}, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("httpclient.New: %v", err)
	}
	ix, err := NewDivxTotal(services.Deps{Client: client})
	if err != nil {
		t.Fatalf("NewDivxTotal: %v", err)
	}
	d := ix.(*DivxTotal)
	d.SiteLinkValue = site.srv.URL + "/"
	client.SetRequestDelay(d.ID(), 0)
	return d
}

const divxSearchPage = `<html><body>
<table class="table"><tbody>
<tr>
  <td><a href="@HOST@/peliculas/12345/la-maldicion">La Maldicion ( HD-CAM)</a></td>
  <td>x</td><td>01-02-2024</td><td>1,4 GB</td>
</tr>
<tr>
  <td><a href="@HOST@/series/777/american-horror-story">American Horror Story</a></td>
  <td>x</td><td>15-03-2024</td><td>500 MB</td>
</tr>
<tr>
  <td><a href="@HOST@/programas/55/win-tool">Win Tool 4K</a></td>
  <td>x</td><td>bad-date</td><td></td>
</tr>
</tbody></table>
</body></html>`

const divxSeriesDetails = `<html><body>
<table class="rwd-table"><tbody>
<tr>
  <td><a href="@HOST@/descarga/win-tool-s01">American Horror Story6x04</a></td>
  <td>x</td>
  <td><a href="@HOST@/download_tt.php?id=99">torrent</a></td>
</tr>
<tr>
  <td><a href="@HOST@/descarga/win-tool-s01e07">American Horror Story6x07</a></td>
  <td>x</td>
  <td><a href="@HOST@/download_tt.php?id=100">torrent</a></td>
</tr>
</tbody></table>
</body></html>`

func TestDivxTotalCaps(t *testing.T) {
	ix, err := NewDivxTotal(services.Deps{})
	if err != nil {
		t.Fatalf("NewDivxTotal: %v", err)
	}
	caps := ix.Capabilities()
	if strings.Join(caps.TVSearchParams, ",") != "q,season,ep" {
		t.Fatalf("TVSearchParams: %v", caps.TVSearchParams)
	}
	if strings.Join(caps.MovieSearchParams, ",") != "q" {
		t.Fatalf("MovieSearchParams: %v", caps.MovieSearchParams)
	}
	for _, tc := range []struct {
		tracker string
		want    int
	}{
		{"peliculas", 2030}, {"peliculas-hd", 2040}, {"peliculas-3-d", 2060},
		{"peliculas-dvdr", 2070}, {"series", 5030}, {"programas", 4000}, {"otros", 8010},
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
	if ix.Language() != "es-ES" || ix.Type() != "public" || ix.SiteLink() != "https://divxtotal.foo/" {
		t.Fatalf("identity: %s %s %s", ix.Language(), ix.Type(), ix.SiteLink())
	}
}

func TestDivxTotalConfigForm(t *testing.T) {
	ix, err := NewDivxTotal(services.Deps{})
	if err != nil {
		t.Fatalf("NewDivxTotal: %v", err)
	}
	items, err := ix.ConfigurationForSetup(context.Background())
	if err != nil {
		t.Fatalf("ConfigurationForSetup: %v", err)
	}
	var ids []string
	for _, it := range items {
		ids = append(ids, it["id"].(string))
	}
	// hidden items (cookieheader/lasterror) are excluded from the setup form
	if len(ids) != 4 || ids[0] != "sitelink" || ids[1] != "matchwordsintitle" || ids[2] != "flaresolverr" || ids[3] != "tags" {
		t.Fatalf("config ids: %v", ids)
	}
}

func TestDivxTotalSearchRequestAndParse(t *testing.T) {
	site := newDivxSite(t, map[string]string{
		"/page/1/":                          divxSearchPage,
		"/series/777/american-horror-story": divxSeriesDetails,
	})
	d := newTestDivxTotal(t, site)

	q := models.NewTorznabQuery()
	q.QueryType = "search"
	q.SearchTerm = "American.Horror.Story.2014"
	res, err := d.ResultsForQuery(context.Background(), q, false)
	if err != nil {
		t.Fatalf("ResultsForQuery: %v", err)
	}
	// matchWords is enabled by default: only the series whose title matches
	// the query words survives (its episodes come from the details page)
	if len(res.Releases) != 2 {
		t.Fatalf("releases: %d", len(res.Releases))
	}

	// disable word matching to also get the movie and programas rows
	d.cfg.MatchWords.Value = false
	res, err = d.ResultsForQuery(context.Background(), q, false)
	if err != nil {
		t.Fatalf("ResultsForQuery (no matchWords): %v", err)
	}
	if len(res.Releases) != 4 {
		t.Fatalf("releases (no matchWords): %d", len(res.Releases))
	}

	// movie release (first row of the page)
	movie := res.Releases[0]
	if movie.Title != "La Maldicion 2014 HD-CAM SPANISH DVDRip XviD" {
		t.Fatalf("movie title: %q", movie.Title)
	}
	if movie.Size == nil || *movie.Size != 1503238553 {
		t.Fatalf("movie size: %v", movie.Size)
	}
	if movie.PublishDate.Format("2006-01-02") != "2024-02-01" {
		t.Fatalf("movie publish date: %v", movie.PublishDate)
	}
	foundCat := false
	for _, id := range movie.Category {
		if id == 2030 {
			foundCat = true
		}
	}
	if !foundCat {
		t.Fatalf("movie category: %v", movie.Category)
	}
	if movie.Seeders == nil || *movie.Seeders != 1 || movie.Peers == nil || *movie.Peers != 2 {
		t.Fatalf("movie seeders/peers: %v %v", movie.Seeders, movie.Peers)
	}
	if movie.DownloadVolumeFactor == nil || *movie.DownloadVolumeFactor != 0 {
		t.Fatalf("movie dvf: %v", movie.DownloadVolumeFactor)
	}
	if movie.Files == nil || *movie.Files != 1 {
		t.Fatalf("movie files: %v", movie.Files)
	}
	// movie download link points at the details page (resolved at download time)
	if movie.Link == nil || !strings.Contains(movie.Link.Path, "/peliculas/12345/") {
		t.Fatalf("movie link: %v", movie.Link)
	}

	// series episodes from the details page
	ep := res.Releases[1]
	if ep.Title != "American Horror Story S06E04 SPANISH SDTV XviD" {
		t.Fatalf("episode title: %q", ep.Title)
	}
	if ep.Size == nil || *ep.Size != 536870912 {
		t.Fatalf("episode size: %v", ep.Size)
	}
	if ep.Link == nil || !strings.HasSuffix(ep.Link.Path, "/download_tt.php") {
		t.Fatalf("episode link: %v", ep.Link)
	}
	foundCat = false
	for _, id := range ep.Category {
		if id == 5030 {
			foundCat = true
		}
	}
	if !foundCat {
		t.Fatalf("episode category: %v", ep.Category)
	}

	// search URL construction: page/1/?s= with the year stripped, words cleaned
	paths := site.paths()
	if len(paths) == 0 || paths[0] != "/page/1/?s=American+Horror+Story" {
		t.Fatalf("paths: %v", paths)
	}
}

func TestDivxTotalDownloadResolvesDetailsPage(t *testing.T) {
	site := newDivxSite(t, map[string]string{
		"/peliculas/12345/la-maldicion": `<html><body><a href="@HOST@/download_tt.php?id=5">dl</a></body></html>`,
		"/download_tt.php":              "torrent-bytes",
	})
	d := newTestDivxTotal(t, site)

	details, _ := url.Parse(site.srv.URL + "/peliculas/12345/la-maldicion")
	content, err := d.Download(context.Background(), details)
	if err != nil {
		t.Fatalf("Download: %v", err)
	}
	if string(content) != "torrent-bytes" {
		t.Fatalf("content: %q", content)
	}
	paths := site.paths()
	if len(paths) != 2 || paths[0] != "/peliculas/12345/la-maldicion" || paths[1] != "/download_tt.php?id=5" {
		t.Fatalf("paths: %v", paths)
	}

	// series links already contain /download_tt.php and are fetched directly
	series, _ := url.Parse(site.srv.URL + "/download_tt.php?id=99")
	if _, err := d.Download(context.Background(), series); err != nil {
		t.Fatalf("Download series: %v", err)
	}
	paths = site.paths()
	if paths[len(paths)-1] != "/download_tt.php?id=99" {
		t.Fatalf("paths: %v", paths)
	}
}

func TestDivxTotalApplyConfigurationEmptyIsError(t *testing.T) {
	site := newDivxSite(t, map[string]string{
		"/page/1/": `<html><body><p>no table here</p></body></html>`,
	})
	d := newTestDivxTotal(t, site)
	_, err := d.ApplyConfiguration(context.Background(), []indexer.ConfigItem{
		{ID: "sitelink", Value: site.srv.URL + "/"},
	})
	if err == nil || err.Error() != "Could not find releases from this URL" {
		t.Fatalf("err: %v", err)
	}
	if d.IsConfigured() {
		t.Fatal("must not be configured after failure")
	}
}

func TestDivxTotalCheckTitleMatchWords(t *testing.T) {
	// accent folding: both sides go through the same lossy ISO-8859-8 →
	// UTF-8 transform, so matching still works
	if !divxCheckTitleMatchWords("american horror", "American Horror Story6x04 SPANISH SDTV XviD") {
		t.Fatal("expected match")
	}
	if divxCheckTitleMatchWords("american horror motel", "American Horror Story6x04 SPANISH SDTV XviD") {
		t.Fatal("expected mismatch")
	}
	if !divxCheckTitleMatchWords("español castellano", "Pelicula ESPAÑOL CASTELLANO") {
		t.Fatal("expected accent-insensitive match")
	}
}

func TestDivxTotalHelpers(t *testing.T) {
	// year parsing strips the trailing 4-digit year and sets query.Year
	// (season/episode have already been removed from the search term upstream)
	q := models.NewTorznabQuery()
	q.SearchTerm = "Marco.Polo.2014"
	parsed := divxParseQuery(q.Clone())
	if parsed.Year == nil || *parsed.Year != 2014 {
		t.Fatalf("year: %v", parsed.Year)
	}
	if parsed.SearchTerm != "Marco Polo" {
		t.Fatalf("search term: %q", parsed.SearchTerm)
	}

	// language words are removed
	q2 := models.NewTorznabQuery()
	q2.SearchTerm = "Mascotas 2 spanish"
	parsed2 := divxParseQuery(q2.Clone())
	if strings.TrimSpace(parsed2.SearchTerm) != "Mascotas 2" && parsed2.SearchTerm != "Mascotas 2 " {
		t.Fatalf("search term: %q", parsed2.SearchTerm)
	}

	// scene title conversion with a scene-format query (year put back)
	sceneQ := models.NewTorznabQuery()
	sceneQ.Season = divxIntPtr(6)
	sceneQ.Episode = "4"
	sceneQ.Year = divxIntPtr(2014)
	if got := divxParseSeriesTitle("American Horror Story6x04", sceneQ); got != "American Horror Story 2014 S06E04 SPANISH SDTV XviD" {
		t.Fatalf("scene title: %q", got)
	}
	// non-scene query: no year
	plainQ := models.NewTorznabQuery()
	if got := divxParseSeriesTitle("American Horror Story6x04", plainQ); got != "American Horror Story S06E04 SPANISH SDTV XviD" {
		t.Fatalf("plain title: %q", got)
	}

	fallback := time.Now()
	if got := divxTryToParseDate("bad-date", fallback); got != fallback {
		t.Fatalf("date fallback: %v", got)
	}
	if got := divxTryToParseSize("", divxSizeOtros); got != divxSizeOtros {
		t.Fatalf("size fallback: %v", got)
	}
}

func divxIntPtr(n int) *int { return &n }
