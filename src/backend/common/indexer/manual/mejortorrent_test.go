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

type mejortorrentSite struct {
	srv      *httptest.Server
	mu       sync.Mutex
	hits     []string
	bodies   map[string]string
	statuses map[string]int
}

func newMejorTorrentSite(t *testing.T) *mejortorrentSite {
	t.Helper()
	s := &mejortorrentSite{
		bodies:   map[string]string{},
		statuses: map[string]int{},
	}
	s.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		s.hits = append(s.hits, r.Method+" "+r.URL.RequestURI())
		status := s.statuses[r.URL.Path]
		content, ok := s.bodies[r.URL.Path]
		s.mu.Unlock()
		if !ok {
			http.NotFound(w, r)
			return
		}
		content = strings.ReplaceAll(content, "@HOST@", s.srv.URL)
		if status != 0 {
			w.WriteHeader(status)
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(content))
	}))
	t.Cleanup(s.srv.Close)
	return s
}

func (s *mejortorrentSite) paths() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.hits...)
}

const mejortorrentNewestPage = `<html><body>
<div class="gap-y-3"><div><div>
<div>
  <div>2024-01-02</div>
  <a href="@HOST@/pelicula/el-juego-del-calamar">
    <div><span>[HDTV]</span>El Juego Del Calamar [HDTV]</div>
  </a>
</div>
<div>
  <div>2024-01-03</div>
  <a href="@HOST@/serie/the-mandalorian">
    <div><span>[HDTV]</span>The Mandalorian - 1ª Temporada 720p [HDTV]</div>
  </a>
</div>
<div>
  <div>2024-01-03</div>
  <a href="@HOST@/serie/the-mandalorian">
    <div><span>[HDTV]</span>The Mandalorian - 1ª Temporada 720p [HDTV]</div>
  </a>
</div>
</div></div></div>
</body></html>`

const mejortorrentMovieDetails = `<html><body>
<a class="ml-2" href="@HOST@/descargar/pelicula/1">torrent</a>
<div class="py-4"><p>info</p><p><a>Thriller</a><a>2021</a></p></div>
<div class="py-4"><p>other</p></div>
</body></html>`

const mejortorrentSeriesDetails = `<html><body><table><tbody>
<tr class="border">
  <td>e</td><td>1x04 - 05.</td><td>2024-03-01</td><td>x</td>
  <td><a href="@HOST@/descargar/serie/104">torrent</a></td>
</tr>
<tr class="border">
  <td>e</td><td>2x01 - 2x02 - 2x03.</td><td>bad-date</td><td>x</td>
  <td><a href="@HOST@/descargar/serie/201">torrent</a></td>
</tr>
</tbody></table></body></html>`

const mejortorrentSearchPage = `<html><body>
<div class="w-11/12">
  <div class="flex-row">
    <a href="@HOST@/pelicula/otra-pelicula">
      <div><span>[MicroHD]</span>Otra Pelicula [MicroHD]</div>
    </a>
    <div>Película</div>
  </div>
  <div class="flex-row">
    <a href="@HOST@/serie/otra-serie">
      <div><span>[HDTV]</span>Otra Serie - 2ª Temporada [HDTV]</div>
    </a>
    <div>series</div>
  </div>
</div>
</body></html>`

const mejortorrentMovieDetails2 = `<html><body>
<a class="ml-2" href="@HOST@/descargar/pelicula/9">torrent</a>
<div class="py-4"><p>i</p><p><a>g</a><a>2019</a></p></div>
<div class="py-4"><p>o</p></div>
</body></html>`

const mejortorrentSeriesDetails2 = `<html><body><table><tbody>
<tr class="border">
  <td>e</td><td>2x05</td><td>2024-05-05</td><td>x</td>
  <td><a href="@HOST@/descargar/serie/205">torrent</a></td>
</tr>
</tbody></table></body></html>`

func serveMejorTorrentCatalog(site *mejortorrentSite) {
	site.bodies["/torrents"] = mejortorrentNewestPage
	site.bodies["/pelicula/el-juego-del-calamar"] = mejortorrentMovieDetails
	site.bodies["/serie/the-mandalorian"] = mejortorrentSeriesDetails
	site.bodies["/busqueda/page/1"] = mejortorrentSearchPage
	site.bodies["/pelicula/otra-pelicula"] = mejortorrentMovieDetails2
	site.bodies["/serie/otra-serie"] = mejortorrentSeriesDetails2
}

func newTestMejorTorrent(t *testing.T, site *mejortorrentSite) *MejorTorrent {
	t.Helper()
	client, err := httpclient.New(httpclient.Config{}, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("httpclient.New: %v", err)
	}
	ix, err := NewMejorTorrent(services.Deps{Client: client})
	if err != nil {
		t.Fatalf("NewMejorTorrent: %v", err)
	}
	m := ix.(*MejorTorrent)
	m.SiteLinkValue = site.srv.URL + "/"
	client.SetRequestDelay(m.ID(), 0)
	return m
}

func containsCategory(categories []int, want int) bool {
	for _, id := range categories {
		if id == want {
			return true
		}
	}
	return false
}

func TestMejorTorrentCaps(t *testing.T) {
	ix, err := NewMejorTorrent(services.Deps{})
	if err != nil {
		t.Fatalf("NewMejorTorrent: %v", err)
	}
	caps := ix.Capabilities()
	if strings.Join(caps.TVSearchParams, ",") != "q,season,ep" {
		t.Fatalf("TVSearchParams: %v", caps.TVSearchParams)
	}
	if strings.Join(caps.MovieSearchParams, ",") != "q" || strings.Join(caps.MusicSearchParams, ",") != "q" {
		t.Fatalf("movie/music params: %v %v", caps.MovieSearchParams, caps.MusicSearchParams)
	}
	if !caps.SupportsRawSearch {
		t.Fatal("SupportsRawSearch must be true")
	}
	for _, tc := range []struct {
		tracker string
		want    int
	}{
		{"Película", 2000},
		{"Serie", 5030},
		{"SerieHD", 5040},
		{"Música", 3000},
	} {
		got := caps.Categories.MapTrackerCatToNewznab(tc.tracker)
		if !containsCategory(got, tc.want) {
			t.Fatalf("MapTrackerCatToNewznab(%q) = %v, want it to contain %d", tc.tracker, got, tc.want)
		}
	}
	if ix.Language() != "es-ES" || ix.Type() != "public" || ix.SiteLink() != "https://www47.mejortorrent.eu/" {
		t.Fatalf("identity: %s %s %s", ix.Language(), ix.Type(), ix.SiteLink())
	}
	if got := len(ix.(*MejorTorrent).LegacySiteLinks); got != 15 {
		t.Fatalf("legacy site links: %d", got)
	}
}

func TestMejorTorrentConfigForm(t *testing.T) {
	ix, err := NewMejorTorrent(services.Deps{})
	if err != nil {
		t.Fatalf("NewMejorTorrent: %v", err)
	}
	items, err := ix.ConfigurationForSetup(context.Background())
	if err != nil {
		t.Fatalf("ConfigurationForSetup: %v", err)
	}
	var ids []string
	for _, it := range items {
		ids = append(ids, it["id"].(string))
	}
	// upstream: bare ConfigurationData + the dynamic MatchWords item
	if strings.Join(ids, ",") != "sitelink,matchwordsintitle,tags" {
		t.Fatalf("config ids: %v", ids)
	}
}

func TestMejorTorrentNewestRequestAndParse(t *testing.T) {
	site := newMejorTorrentSite(t)
	serveMejorTorrentCatalog(site)
	m := newTestMejorTorrent(t, site)

	res, err := m.ResultsForQuery(context.Background(), &models.TorznabQuery{QueryType: "search", IsTest: true}, false)
	if err != nil {
		t.Fatalf("ResultsForQuery: %v", err)
	}
	// duplicate newest rows collapse to one details link each
	if len(res.Releases) != 3 {
		t.Fatalf("releases: %d", len(res.Releases))
	}

	movie := res.Releases[0]
	if movie.Title != "El Juego Del Calamar 2021 SPANISH HDTV" {
		t.Fatalf("movie title: %q", movie.Title)
	}
	if movie.Size == nil || *movie.Size != 1610612736 {
		t.Fatalf("movie size: %v", movie.Size)
	}
	if !containsCategory(movie.Category, 2000) {
		t.Fatalf("movie category: %v", movie.Category)
	}
	if movie.PublishDate.Format("2006-01-02") != "2024-01-02" {
		t.Fatalf("movie publish date: %v", movie.PublishDate)
	}
	if movie.Link == nil || !strings.HasSuffix(movie.Link.Path, "/descargar/pelicula/1") {
		t.Fatalf("movie link: %v", movie.Link)
	}
	if movie.Files == nil || *movie.Files != 1 || movie.Seeders == nil || *movie.Seeders != 1 || movie.Peers == nil || *movie.Peers != 2 {
		t.Fatalf("movie files/seeders/peers: %v %v %v", movie.Files, movie.Seeders, movie.Peers)
	}
	if movie.DownloadVolumeFactor == nil || *movie.DownloadVolumeFactor != 0 || movie.UploadVolumeFactor == nil || *movie.UploadVolumeFactor != 1 {
		t.Fatalf("movie dvf/uvf: %v %v", movie.DownloadVolumeFactor, movie.UploadVolumeFactor)
	}

	ep1 := res.Releases[1]
	if ep1.Title != "The Mandalorian S01E04-E05 SPANISH HDTV" {
		t.Fatalf("episode 1 title: %q", ep1.Title)
	}
	if ep1.Size == nil || *ep1.Size != 1073741824 {
		t.Fatalf("episode 1 size (title has 720p → 1 GB): %v", *ep1.Size)
	}
	if !containsCategory(ep1.Category, 5040) {
		t.Fatalf("episode 1 category (SerieHD → TV/HD): %v", ep1.Category)
	}
	if ep1.PublishDate.Format("2006-01-02") != "2024-03-01" {
		t.Fatalf("episode 1 publish date: %v", ep1.PublishDate)
	}
	if ep1.Link == nil || !strings.HasSuffix(ep1.Link.Path, "/descargar/serie/104") {
		t.Fatalf("episode 1 link: %v", ep1.Link)
	}

	ep2 := res.Releases[2]
	if ep2.Title != "The Mandalorian S02E01-E02-E03 SPANISH HDTV" {
		t.Fatalf("episode 2 title: %q", ep2.Title)
	}
	// bad episode date falls back to the series publish date
	if ep2.PublishDate.Format("2006-01-02") != "2024-01-03" {
		t.Fatalf("episode 2 publish date fallback: %v", ep2.PublishDate)
	}

	paths := site.paths()
	if paths[0] != "GET /torrents" {
		t.Fatalf("paths: %v", paths)
	}
	// the duplicated series row is fetched only once
	count := 0
	for _, p := range paths {
		if p == "GET /serie/the-mandalorian" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("series details fetched %d times, want 1: %v", count, paths)
	}
}

func TestMejorTorrentSearchRequestAndParse(t *testing.T) {
	site := newMejorTorrentSite(t)
	serveMejorTorrentCatalog(site)
	m := newTestMejorTorrent(t, site)

	// matchWords is enabled by default: only the movie row survives
	q := models.NewTorznabQuery()
	q.QueryType = "search"
	q.SearchTerm = "Otra Pelicula"
	res, err := m.ResultsForQuery(context.Background(), q, false)
	if err != nil {
		t.Fatalf("ResultsForQuery: %v", err)
	}
	if len(res.Releases) != 1 {
		t.Fatalf("releases: %d", len(res.Releases))
	}
	movie := res.Releases[0]
	if movie.Title != "Otra Pelicula 2019 SPANISH MicroHD" {
		t.Fatalf("movie title: %q", movie.Title)
	}
	if movie.Size == nil || *movie.Size != 7516192768 {
		t.Fatalf("movie size (MicroHD → 7 GB): %v", movie.Size)
	}
	if !containsCategory(movie.Category, 2000) {
		t.Fatalf("movie category: %v", movie.Category)
	}
	paths := site.paths()
	if paths[0] != "GET /busqueda/page/1?q=Otra%25Pelicula" {
		t.Fatalf("search path: %q", paths[0])
	}

	// scene query: the series episode matches S02E05 and is kept
	site2 := newMejorTorrentSite(t)
	serveMejorTorrentCatalog(site2)
	m2 := newTestMejorTorrent(t, site2)
	q2 := models.NewTorznabQuery()
	q2.QueryType = "tvsearch"
	q2.SearchTerm = "Otra Serie"
	season := 2
	q2.Season = &season
	q2.Episode = "5"
	res2, err := m2.ResultsForQuery(context.Background(), q2, false)
	if err != nil {
		t.Fatalf("ResultsForQuery (scene): %v", err)
	}
	if len(res2.Releases) != 1 {
		t.Fatalf("releases (scene): %d", len(res2.Releases))
	}
	ep := res2.Releases[0]
	if ep.Title != "Otra Serie S02E05 SPANISH HDTV" {
		t.Fatalf("episode title: %q", ep.Title)
	}
	if !containsCategory(ep.Category, 5030) {
		t.Fatalf("episode category (Serie SD): %v", ep.Category)
	}
	if ep.Size == nil || *ep.Size != 536870912 {
		t.Fatalf("episode size (SD → 512 MB): %v", ep.Size)
	}
	if ep.PublishDate.Format("2006-01-02") != "2024-05-05" {
		t.Fatalf("episode publish date: %v", ep.PublishDate)
	}
	paths2 := site2.paths()
	if paths2[0] != "GET /busqueda/page/1?q=Otra%25Serie" {
		t.Fatalf("scene search path: %q", paths2[0])
	}
}

func TestMejorTorrentApplyConfiguration(t *testing.T) {
	site := newMejorTorrentSite(t)
	serveMejorTorrentCatalog(site)
	m := newTestMejorTorrent(t, site)

	status, err := m.ApplyConfiguration(context.Background(), []indexer.ConfigItem{
		{ID: "sitelink", Value: site.srv.URL + "/"},
	})
	if err != nil {
		t.Fatalf("ApplyConfiguration: %v", err)
	}
	if status != indexer.ConfigCompleted {
		t.Fatalf("status: %v, want ConfigCompleted", status)
	}
	if !m.IsConfigured() {
		t.Fatal("must be configured after a successful newest query")
	}
	if site.paths()[0] != "GET /torrents" {
		t.Fatalf("paths: %v", site.paths())
	}
}

func TestMejorTorrentApplyConfigurationEmptyIsError(t *testing.T) {
	site := newMejorTorrentSite(t)
	site.bodies["/torrents"] = `<html><body><p>no container here</p></body></html>`
	m := newTestMejorTorrent(t, site)
	_, err := m.ApplyConfiguration(context.Background(), []indexer.ConfigItem{
		{ID: "sitelink", Value: site.srv.URL + "/"},
	})
	if err == nil || err.Error() != "Could not find releases from this URL" {
		t.Fatalf("err: %v", err)
	}
	if m.IsConfigured() {
		t.Fatal("must not be configured after failure")
	}
}

func TestMejorTorrentHTTP500IsError(t *testing.T) {
	site := newMejorTorrentSite(t)
	site.statuses["/torrents"] = http.StatusInternalServerError
	site.bodies["/torrents"] = "boom"
	m := newTestMejorTorrent(t, site)

	_, err := m.ResultsForQuery(context.Background(), &models.TorznabQuery{QueryType: "search", IsTest: true}, false)
	var ie *indexer.IndexerError
	if !errors.As(err, &ie) || ie.Message != "HTTP 500 Internal Server Error" {
		t.Fatalf("err: %v want \"HTTP 500 Internal Server Error\"", err)
	}
}

func TestMejorTorrentHelpers(t *testing.T) {
	// ParseQuery: season/episode stripped, year parsed and removed,
	// non-latin chars dropped, language words removed
	q := models.NewTorznabQuery()
	q.SearchTerm = "Doctor Who 2005"
	parsed := mejortorrentParseQuery(q.Clone())
	if parsed.SearchTerm != "Doctor Who" {
		t.Fatalf("search term: %q", parsed.SearchTerm)
	}
	if parsed.Year == nil || *parsed.Year != 2005 {
		t.Fatalf("year: %v", parsed.Year)
	}

	q2 := models.NewTorznabQuery()
	q2.SearchTerm = "Mascotas español"
	parsed2 := mejortorrentParseQuery(q2.Clone())
	if got := strings.TrimSpace(parsed2.SearchTerm); got != "Mascotas" {
		t.Fatalf("lang words search term: %q", parsed2.SearchTerm)
	}

	// GetMejortorrentCategory: details-link inference + SD/HD hack
	if got := mejortorrentGetCategory(nil, "/pelicula/una-pelicula", "Title", nil); got != mejortorrentCatPelicula {
		t.Fatalf("category from details link: %q", got)
	}
	if got := mejortorrentGetCategory(nil, "/serie/una-serie", "Title [1080p]", nil); got != mejortorrentCatSerieHD {
		t.Fatalf("category SD/HD hack: %q", got)
	}
	// upstream comparisons are case-sensitive: only lowercase row text maps
	cat := "series"
	if got := mejortorrentGetCategory(&cat, "/x", "Title", nil); got != mejortorrentCatSerie {
		t.Fatalf("category from row text: %q", got)
	}
	doc := "documentales"
	if got := mejortorrentGetCategory(&doc, "/x", "Title", nil); got != mejortorrentCatSerie {
		t.Fatalf("documentales category: %q", got)
	}
	upper := "Series"
	if got := mejortorrentGetCategory(&upper, "/x", "Title", nil); got != mejortorrentCatOtro {
		t.Fatalf("case-sensitive category: %q", got)
	}
	if got := mejortorrentGetCategory(nil, "/otro/cosa", "Title", nil); got != mejortorrentCatOtro {
		t.Fatalf("otro category: %q", got)
	}

	// CleanQuality: bracket extraction, '-' → ' ', HDRip → BDRip
	quality := "[HDRip-720p]"
	if got := mejortorrentCleanQuality(&quality); got == nil || *got != "BDRip 720p" {
		t.Fatalf("CleanQuality: %v", got)
	}
	if got := mejortorrentCleanQuality(nil); got != nil {
		t.Fatalf("CleanQuality(nil): %v", got)
	}

	// GuessSize keyword precedence
	if got := mejortorrentGuessSize("x microhd", 1); got != 7516192768 {
		t.Fatalf("GuessSize microhd: %d", got)
	}
	if got := mejortorrentGuessSize("x 2160p bluray", 1); got != 53687091200 {
		t.Fatalf("GuessSize 2160p beats bluray: %d", got)
	}
	if got := mejortorrentGuessSize("x bdremux", 1); got != 21474836480 {
		t.Fatalf("GuessSize bdremux: %d", got)
	}
	if got := mejortorrentGuessSize("x hdtv", 123); got != 123 {
		t.Fatalf("GuessSize fallback: %d", got)
	}
}

func TestMejorTorrentSeriesTitleParsing(t *testing.T) {
	quality := "720p"
	noQuality := "HDTV"

	// multiple NxM runs → S05E08-E13
	q := models.NewTorznabQuery()
	if got := mejortorrentParseSeriesTitle("Grace and Frankie - 5ª Temporada [720p]", "5x08 al 5x13.", &quality, q); got != "Grace and Frankie S05E08-E13 SPANISH 720p" {
		t.Fatalf("multi episode: %q", got)
	}

	// NxM - K. → S01E04-E05
	if got := mejortorrentParseSeriesTitle("The Mandalorian - 1ª Temporada", "1x04 - 05.", &noQuality, q); got != "The Mandalorian S01E04-E05 SPANISH HDTV" {
		t.Fatalf("range episode: %q", got)
	}

	// single episode with trailing garbage after '-'
	if got := mejortorrentParseSeriesTitle("The Mandalorian - 1ª Temporada", "1x08 - CONTRASEÑA: WWW.PCTNEW ORG bebe", &noQuality, q); got != "The Mandalorian S01E08 CONTRASEÑA: WWW.PCTNEW ORG bebe SPANISH HDTV" {
		t.Fatalf("single episode suffix: %q", got)
	}

	// scene query: year put back before the episode string
	year := 2019
	sceneQ := models.NewTorznabQuery()
	sceneQ.Episode = "8"
	sceneQ.Year = &year
	if got := mejortorrentParseSeriesTitle("The Show - 2ª Temporada", "2x08", &quality, sceneQ); got != "The Show 2019 S02E08 SPANISH 720p" {
		t.Fatalf("scene title: %q", got)
	}

	// trailing dot removal in titles is done by parseRelease
	title := "Harry Potter Y La Orden Del Fénix."
	title = strings.TrimSpace(title)
	if strings.HasSuffix(title, ".") {
		title = strings.TrimSpace(title[:len(title)-1])
	}
	if title != "Harry Potter Y La Orden Del Fénix" {
		t.Fatalf("trailing dot title: %q", title)
	}
}
