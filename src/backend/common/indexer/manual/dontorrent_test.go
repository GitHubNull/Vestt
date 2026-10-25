package manual

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
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

type donSite struct {
	srv   *httptest.Server
	mu    sync.Mutex
	hits  []donHit
	serve map[string]donResponse
}

type donHit struct {
	method string
	uri    string
	body   string
}

type donResponse struct {
	status int
	body   string
}

func newDonSite(t *testing.T, serve map[string]donResponse) *donSite {
	t.Helper()
	s := &donSite{serve: serve}
	s.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body []byte
		if r.Body != nil {
			body, _ = io.ReadAll(r.Body)
		}
		s.mu.Lock()
		s.hits = append(s.hits, donHit{method: r.Method, uri: r.URL.RequestURI(), body: string(body)})
		// upstream builds newest-list links as SiteLink + href, producing a
		// double slash; collapse it for the fixture lookup
		lookup := "/" + strings.TrimLeft(r.URL.Path, "/")
		resp, ok := s.serve[lookup]
		s.mu.Unlock()
		if !ok {
			http.NotFound(w, r)
			return
		}
		status := resp.status
		if status == 0 {
			status = http.StatusOK
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(strings.ReplaceAll(resp.body, "@HOST@", s.srv.URL)))
	}))
	t.Cleanup(s.srv.Close)
	return s
}

func (s *donSite) paths() []donHit {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]donHit(nil), s.hits...)
}

func newTestDonTorrent(t *testing.T, site *donSite) *DonTorrent {
	t.Helper()
	client, err := httpclient.New(httpclient.Config{}, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("httpclient.New: %v", err)
	}
	ix, err := NewDonTorrent(services.Deps{Client: client})
	if err != nil {
		t.Fatalf("NewDonTorrent: %v", err)
	}
	d := ix.(*DonTorrent)
	d.SiteLinkValue = site.srv.URL + "/"
	// tests must not pay the upstream 2.1s Cloudflare delay
	client.SetRequestDelay(d.ID(), 0)
	return d
}

const donSearchPage = `<html><body>
<div class="seccion" id="buscador"><div class="card"><div class="card-body">
<p class="lead">Resultados: <b>2</b> en <b>2</b> paginas</p>
<ul>
<li><span><a href="/pelicula/6981/Dune-Parte-Dos">Dune Parte Dos (V. Extendida).</a> <span>(HDRip)</span></span></li>
<li><span><a href="/serie/8800/La-Casa-de-Papel-3-Temporada">La Casa de Papel - 3ª Temporada (HDTV)</a> <span>(HDTV)</span></span></li>
</ul>
</div></div></div>
</body></html>`

const donMovieDetails = `<html><body>
<div class="d-inline-block ml-2"><p class="m-1"><a>2023</a></p></div>
<div class="descargar"><div class="card"><div class="card-body">
<a class="protected-download" data-content-id="4321" data-tabla="peliculas">descargar</a>
<div class="text-center">
<div class="d-inline-block"><p>Seed: 42</p></div>
<div class="d-inline-block"><p>1,4 GB</p></div>
</div>
</div></div></div>
</body></html>`

const donSeriesDetails = `<html><body>
<div class="descargar"><div class="card"><div class="card-body"><div class="d-inline-block">
<table class="table"><tbody>
<tr>
  <td>3x05 - El nombre del viento</td>
  <td><a class="protected-download" data-content-id="9876" data-tabla="series">dl</a></td>
  <td>2024-03-15</td>
</tr>
<tr>
  <td>3x06</td>
  <td><a class="protected-download" data-content-id="9877" data-tabla="series">dl</a></td>
  <td>2024-03-22</td>
</tr>
</tbody></table>
</div></div></div></div>
</body></html>`

func TestDonTorrentCaps(t *testing.T) {
	ix, err := NewDonTorrent(services.Deps{})
	if err != nil {
		t.Fatalf("NewDonTorrent: %v", err)
	}
	caps := ix.Capabilities()
	if strings.Join(caps.TVSearchParams, ",") != "q,season,ep" {
		t.Fatalf("TVSearchParams: %v", caps.TVSearchParams)
	}
	if strings.Join(caps.MovieSearchParams, ",") != "q" {
		t.Fatalf("MovieSearchParams: %v", caps.MovieSearchParams)
	}
	if !caps.SupportsRawSearch {
		t.Fatal("SupportsRawSearch must be true upstream")
	}
	for _, tc := range []struct {
		tracker string
		want    int
	}{
		{"pelicula", 2000}, {"serie", 5000}, {"documental", 5080},
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

func TestDonTorrentConfigForm(t *testing.T) {
	ix, err := NewDonTorrent(services.Deps{})
	if err != nil {
		t.Fatalf("NewDonTorrent: %v", err)
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
	if len(ids) != 3 || ids[0] != "sitelink" || ids[1] != "matchwordsintitle" || ids[2] != "tags" {
		t.Fatalf("config ids: %v", ids)
	}
}

func TestDonTorrentSearchRequestAndParse(t *testing.T) {
	site := newDonSite(t, map[string]donResponse{
		"/buscar":                       {body: donSearchPage},
		"/pelicula/6981/Dune-Parte-Dos": {body: donMovieDetails},
		"/serie/8800/La-Casa-de-Papel-3-Temporada": {body: donSeriesDetails},
	})
	d := newTestDonTorrent(t, site)
	// matchWords would drop the series row (its title does not contain
	// "dune"); disable it so both result rows are exercised
	d.cfg.MatchWords.Value = false

	q := models.NewTorznabQuery()
	q.QueryType = "search"
	q.SearchTerm = "dune"
	res, err := d.ResultsForQuery(context.Background(), q, false)
	if err != nil {
		t.Fatalf("ResultsForQuery: %v", err)
	}
	// 1 movie + 2 series episodes (both episodes share one details page)
	if len(res.Releases) != 3 {
		t.Fatalf("releases: %d", len(res.Releases))
	}

	hits := site.paths()
	if len(hits) != 3 {
		t.Fatalf("hits: %d %v", len(hits), hits)
	}
	// POST to /buscar (no trailing slash) with the upstream form fields
	if hits[0].method != http.MethodPost || hits[0].uri != "/buscar" {
		t.Fatalf("first hit: %+v", hits[0])
	}
	form, err := url.ParseQuery(hits[0].body)
	if err != nil {
		t.Fatalf("form: %v", err)
	}
	if form.Get("valor") != "dune" || form.Get("Buscar") != "Buscar" || form.Get("p") != "1" {
		t.Fatalf("form values: %v", form)
	}

	// movie: detail page expansion, protected download link, size from the page
	movie := res.Releases[0]
	if movie.Title != "Dune.Parte.Dos.2023.HDRip.Directors.Cut.SPANISH" {
		t.Fatalf("movie title: %q", movie.Title)
	}
	if movie.Link == nil || !strings.HasSuffix(movie.Link.Path, "/pelicula/6981/Dune-Parte-Dos/peliculas/4321") {
		t.Fatalf("movie link: %v", movie.Link)
	}
	if movie.Details == nil || !strings.HasSuffix(movie.Details.Path, "/pelicula/6981/Dune-Parte-Dos") {
		t.Fatalf("movie details: %v", movie.Details)
	}
	if movie.Size == nil || *movie.Size != 1503238553 {
		t.Fatalf("movie size: %v", movie.Size)
	}
	foundCat := false
	for _, id := range movie.Category {
		if id == 2000 {
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

	// series episodes: scene title building and per-episode download links
	ep1 := res.Releases[1]
	// quality and the (HDTV) title tag are both appended upstream
	if ep1.Title != "La.Casa.de.Papel.S03E05.El.nombre.del.viento.HDTV.HDTV.SPANISH" {
		t.Fatalf("episode 1 title: %q", ep1.Title)
	}
	if ep1.Link == nil || !strings.HasSuffix(ep1.Link.Path, "/serie/8800/La-Casa-de-Papel-3-Temporada/series/9876") {
		t.Fatalf("episode 1 link: %v", ep1.Link)
	}
	if ep1.PublishDate.Format("2006-01-02") != "2024-03-15" {
		t.Fatalf("episode 1 date: %v", ep1.PublishDate)
	}
	if ep1.Size == nil || *ep1.Size != 536870912 {
		t.Fatalf("episode 1 size: %v", ep1.Size)
	}
	foundCat = false
	for _, id := range ep1.Category {
		if id == 5000 {
			foundCat = true
		}
	}
	if !foundCat {
		t.Fatalf("episode category: %v", ep1.Category)
	}

	ep2 := res.Releases[2]
	if ep2.Title != "La.Casa.de.Papel.S03E06.HDTV.HDTV.SPANISH" {
		t.Fatalf("episode 2 title: %q", ep2.Title)
	}
	if ep2.Link == nil || !strings.HasSuffix(ep2.Link.Path, "/serie/8800/La-Casa-de-Papel-3-Temporada/series/9877") {
		t.Fatalf("episode 2 link: %v", ep2.Link)
	}
}

func TestDonTorrentNewest(t *testing.T) {
	site := newDonSite(t, map[string]donResponse{
		"/ultimos": {body: `<html><body>
<div class="seccion" id="ultimos_torrents">
<ul>
<li><span>2024-05-01</span><a class="text-primary" href="/pelicula/6981/Dune-Parte-Dos">Dune Parte Dos.</a><span>(HDRip)</span></li>
<li><span>2024-05-02</span><a class="text-primary" href="/pelicula/6981/Dune-Parte-Dos">Dune Parte Dos.</a><span>(HDRip)</span></li>
</ul>
</div>
</body></html>`},
		"/pelicula/6981/Dune-Parte-Dos": {body: donMovieDetails},
	})
	d := newTestDonTorrent(t, site)

	res, err := d.ResultsForQuery(context.Background(), &models.TorznabQuery{QueryType: "search", IsTest: true}, false)
	if err != nil {
		t.Fatalf("ResultsForQuery: %v", err)
	}
	// duplicate details links are collapsed upstream
	if len(res.Releases) != 1 {
		t.Fatalf("releases: %d", len(res.Releases))
	}
	if res.Releases[0].Title != "Dune.Parte.Dos.2023.HDRip.SPANISH" {
		t.Fatalf("title: %q", res.Releases[0].Title)
	}
	hits := site.paths()
	if len(hits) != 2 || hits[0].method != http.MethodGet || hits[0].uri != "/ultimos" {
		t.Fatalf("hits: %v", hits)
	}
}

func TestDonTorrentShortSearchTermYieldsNothing(t *testing.T) {
	site := newDonSite(t, map[string]donResponse{})
	d := newTestDonTorrent(t, site)

	q := models.NewTorznabQuery()
	q.QueryType = "search"
	q.SearchTerm = "x" // < 2 chars after punctuation cleanup
	res, err := d.ResultsForQuery(context.Background(), q, false)
	if err != nil {
		t.Fatalf("ResultsForQuery: %v", err)
	}
	if len(res.Releases) != 0 {
		t.Fatalf("releases: %d", len(res.Releases))
	}
	if len(site.paths()) != 0 {
		t.Fatalf("no request expected: %v", site.paths())
	}
}

func TestDonTorrentSearchErrorStatus(t *testing.T) {
	site := newDonSite(t, map[string]donResponse{
		"/buscar": {status: http.StatusInternalServerError, body: "boom"},
	})
	d := newTestDonTorrent(t, site)

	q := models.NewTorznabQuery()
	q.QueryType = "search"
	q.SearchTerm = "dune"
	if _, err := d.ResultsForQuery(context.Background(), q, false); err == nil {
		t.Fatal("expected error")
	}
}

func TestDonTorrentDownloadProofOfWork(t *testing.T) {
	site := newDonSite(t, map[string]donResponse{
		// one static body serves both the generate and the validate call:
		// generate reads "challenge", validate reads "download_url"
		"/api_validate_pow.php": {body: `{"success":true,"challenge":"abc123","download_url":"@HOST@/download/abc.torrent"}`},
		"/download/abc.torrent": {body: "torrent-bytes"},
	})
	d := newTestDonTorrent(t, site)

	link, _ := url.Parse(site.srv.URL + "/pelicula/6981/Dune-Parte-Dos/peliculas/4321")
	content, err := d.Download(context.Background(), link)
	if err != nil {
		t.Fatalf("Download: %v", err)
	}
	if string(content) != "torrent-bytes" {
		t.Fatalf("content: %q", content)
	}
	hits := site.paths()
	if len(hits) != 3 {
		t.Fatalf("hits: %v", hits)
	}
	if hits[0].uri != "/api_validate_pow.php" || hits[1].uri != "/api_validate_pow.php" || hits[2].uri != "/download/abc.torrent" {
		t.Fatalf("hit order: %v", hits)
	}
	var generate, validate map[string]string
	_ = json.Unmarshal([]byte(hits[0].body), &generate)
	_ = json.Unmarshal([]byte(hits[1].body), &validate)
	if generate["action"] != "generate" || generate["content_id"] != "4321" || generate["tabla"] != "peliculas" {
		t.Fatalf("generate payload: %v", generate)
	}
	if validate["action"] != "validate" || validate["challenge"] != "abc123" || validate["nonce"] == "" {
		t.Fatalf("validate payload: %v", validate)
	}
	// the submitted nonce must solve the challenge (3 leading zero hex digits)
	nonce, _ := strconv.ParseInt(validate["nonce"], 10, 64)
	sum := sha256.Sum256([]byte("abc123" + strconv.FormatInt(nonce, 10)))
	if hex.EncodeToString(sum[:])[:3] != "000" {
		t.Fatalf("invalid PoW nonce %d", nonce)
	}
}

func TestDonTorrentHelpers(t *testing.T) {
	if got := donGetCategoryFromURL("https://x.test/pelicula/1/a"); got != donCatPelicula {
		t.Fatalf("category: %q", got)
	}
	if got := donGetCategoryFromURL("https://x.test/otra/1/a"); got != "" {
		t.Fatalf("category: %q", got)
	}

	if got := donCleanReleaseTitle("La Casa de Papel - 3ª Temporada (HDTV)"); got != "La.Casa.de.Papel" {
		t.Fatalf("clean title: %q", got)
	}
	if got := donCleanReleaseTitle("Dune Parte Dos"); got != "Dune.Parte.Dos" {
		t.Fatalf("clean title: %q", got)
	}

	if got := donParseSeriesSeason("Show - Miniserie (HDTV)"); got != "S01" {
		t.Fatalf("miniserie season: %q", got)
	}
	if got := donParseSeriesEpisodeNumber("3x05 - x"); got != "E05" {
		t.Fatalf("episode number: %q", got)
	}
	if got := donParseSeriesEpisodeNumber("3x05 al 3x08"); got != "E05-E08" {
		t.Fatalf("episode range: %q", got)
	}
	if got := donParseSeriesEpisodeTitle("3x05 - El nombre del viento"); got != "El.nombre.del.viento" {
		t.Fatalf("episode title: %q", got)
	}
	if got := donGetEpisodeCountFromTitle("E05"); got != 1 {
		t.Fatalf("episode count: %d", got)
	}
	if got := donGetEpisodeCountFromTitle("E01-E03"); got != 3 {
		t.Fatalf("episode range count: %d", got)
	}

	if got := donGuessSize("hdrip", "pelicula"); got != 2147483648 {
		t.Fatalf("guess size: %d", got)
	}
	if got := donGuessSize("hdtv", "serie"); got != 536870912 {
		t.Fatalf("guess size: %d", got)
	}
	if got := donGuessSize("unknown", "documental"); got != 524288000 {
		t.Fatalf("guess size default: %d", got)
	}

	if got := donProcessTags("(V. Extendida)"); got != ".Directors.Cut" {
		t.Fatalf("tags: %q", got)
	}
	// upstream bug: assignment discards the earlier tag
	if got := donProcessTags("(HDTV) (Subs. integrados)"); got != ".HC" {
		t.Fatalf("tags assign bug: %q", got)
	}

	// PoW: the returned nonce solves the challenge
	nonce := donComputeProofOfWork("abc123", 3)
	sum := sha256.Sum256([]byte("abc123" + strconv.FormatInt(nonce, 10)))
	if hex.EncodeToString(sum[:])[:3] != "000" {
		t.Fatalf("invalid PoW nonce %d", nonce)
	}
}

func TestDonTorrentApplyConfigurationEmptyIsError(t *testing.T) {
	site := newDonSite(t, map[string]donResponse{
		"/ultimos": {body: `<html><body><div class="seccion" id="ultimos_torrents"></div></body></html>`},
	})
	d := newTestDonTorrent(t, site)
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
