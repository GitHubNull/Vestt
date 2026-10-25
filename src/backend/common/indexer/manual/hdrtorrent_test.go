package manual

import (
	"context"
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

const hdrListHTML = `<html><body>
<div class="col"><a class="media-card-link" href="/filme/movie-name/">Movie Name 2023 1080p WEB-DL DUAL 5.1</a></div>
<div class="col"><a class="media-card-link" href="/serie/show-name/">Show Name S01 720p</a></div>
<div class="col"><a class="media-card-link" href="%">Broken Row</a></div>
<div class="col"><span>no anchor here</span></div>
</body></html>`

const hdrMovieDetails = `<html><head><meta property='article:published_time' content='2024-03-01T10:00:00Z'></head><body>
<div class="infos"><p><b>Tamanho:</b> 2.5 GB<br><b>Qualidade:</b> Full HD<br><b>Áudio:</b> Dual Áudio, Inglês<br><b>Gênero:</b> Ação, Drama<br><b>Legenda:</b> Português</p></div>
<div><a href="magnet:?xt=urn:btih:AAAA1111&amp;dn=movie"><span>DUBLADO MKV MAGNET</span></a></div>
<div><a href="magnet:?xt=urn:btih:BBBB2222&amp;dn=movie2">LEGENDADO MP4 LINK</a></div>
</body></html>`

const hdrShowDetails = `<html><body>
<div class="infos"><p><b>Qualidade:</b> 720p</p></div>
<div><a href="magnet:?xt=urn:btih:CCCC3333&amp;dn=show">DUAL ÁUDIO TORRENT</a></div>
</body></html>`

type hdrSite struct {
	*httptest.Server

	mu   sync.Mutex
	hits []string
}

func newHDRSite(t *testing.T) *hdrSite {
	t.Helper()
	s := &hdrSite{}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		s.hits = append(s.hits, r.URL.RequestURI())
		s.mu.Unlock()
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		switch r.URL.Path {
		case "/", "/index.php":
			_, _ = fmt.Fprint(w, hdrListHTML)
		case "/filme/movie-name/":
			_, _ = fmt.Fprint(w, hdrMovieDetails)
		case "/serie/show-name/":
			_, _ = fmt.Fprint(w, hdrShowDetails)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(s.Close)
	return s
}

func newTestHDR(t *testing.T, site *hdrSite) *HDRTorrent {
	t.Helper()
	client, err := httpclient.New(httpclient.Config{}, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("httpclient.New: %v", err)
	}
	ix, err := NewHDRTorrent(services.Deps{Client: client})
	if err != nil {
		t.Fatalf("NewHDRTorrent: %v", err)
	}
	h := ix.(*HDRTorrent)
	h.SiteLinkValue = site.URL + "/"
	h.DefaultSiteLink = site.URL + "/"
	// tests must not pay the upstream 0.5s delay
	client.SetRequestDelay(h.ID(), 0)
	return h
}

func TestHDRTorrentCaps(t *testing.T) {
	ix, err := NewHDRTorrent(services.Deps{})
	if err != nil {
		t.Fatalf("NewHDRTorrent: %v", err)
	}
	caps := ix.Capabilities()
	wantMovie := []string{models.MovieParamQ}
	if strings.Join(caps.MovieSearchParams, ",") != strings.Join(wantMovie, ",") {
		t.Fatalf("MovieSearchParams: %v", caps.MovieSearchParams)
	}
	wantTV := []string{models.TVParamQ, models.TVParamSeason, models.TVParamEp}
	if strings.Join(caps.TVSearchParams, ",") != strings.Join(wantTV, ",") {
		t.Fatalf("TVSearchParams: %v", caps.TVSearchParams)
	}
	for _, tc := range []struct {
		tracker string
		want    int
	}{
		{"filmes", models.CatMovies.ID},
		{"series", models.CatTV.ID},
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

func TestHDRTorrentConfigForm(t *testing.T) {
	ix, err := NewHDRTorrent(services.Deps{})
	if err != nil {
		t.Fatalf("NewHDRTorrent: %v", err)
	}
	items, err := ix.ConfigurationForSetup(context.Background())
	if err != nil {
		t.Fatalf("ConfigurationForSetup: %v", err)
	}
	var ids []string
	for _, it := range items {
		ids = append(ids, it["id"].(string))
	}
	// only sitelink + tags are user-visible in the plain base config
	if len(ids) != 2 || ids[0] != "sitelink" || ids[1] != "tags" {
		t.Fatalf("config ids: %v", ids)
	}
}

func TestHDRTorrentSearchAndParse(t *testing.T) {
	site := newHDRSite(t)
	h := newTestHDR(t, site)

	q := models.NewTorznabQuery()
	q.QueryType = "search"
	q.SearchTerm = "movie name"
	res, err := h.ResultsForQuery(context.Background(), q, false)
	// the broken "%" href row aborts the whole parse (upstream OnParseError
	// semantics: one bad row kills the result set).
	if err == nil {
		t.Fatalf("expected parse error, got none (res: %v)", res)
	}
	ie, ok := err.(*indexer.IndexerError)
	if !ok || ie.Kind != indexer.KindParse {
		t.Fatalf("err: %v", err)
	}
}

func TestHDRTorrentParseDetails(t *testing.T) {
	site := newHDRSite(t)
	h := newTestHDR(t, site)

	releases, err := h.parseResponse(context.Background(), `<html><body>
<div class="col"><a class="media-card-link" href="/filme/movie-name/">Movie Name 2023 1080p WEB-DL DUAL 5.1</a></div>
<div class="col"><a class="media-card-link" href="/serie/show-name/">Show Name S01 720p</a></div>
</body></html>`)
	if err != nil {
		t.Fatalf("parseResponse: %v", err)
	}
	if len(releases) != 3 {
		t.Fatalf("releases: %d", len(releases))
	}

	r := releases[0]
	// CleanTitle(raw) + parent text ("DUBLADO MKV MAGNET" → "Dubbed") + resolution ("Full HD" → "1080p")
	if r.Title != "Movie Name 2023 DUAL 5.1 Dubbed 1080p" {
		t.Fatalf("title: %q", r.Title)
	}
	if r.MagnetUri == nil || !strings.Contains(r.MagnetUri.String(), "xt=urn:btih:AAAA1111") {
		t.Fatalf("magnet: %v", r.MagnetUri)
	}
	if r.Guid == nil || r.Guid.String() != r.MagnetUri.String() {
		t.Fatalf("guid: %v", r.Guid)
	}
	if r.Details == nil || !strings.HasSuffix(r.Details.Path, "/filme/movie-name/") {
		t.Fatalf("details: %v", r.Details)
	}
	if r.Size == nil || *r.Size != 2684354560 { // 2.5 GiB
		t.Fatalf("size: %v", r.Size)
	}
	if r.Seeders == nil || *r.Seeders != 1 {
		t.Fatalf("seeders: %v", r.Seeders)
	}
	if r.PublishDate.Format("2006-01-02 15:04") != "2024-03-01 10:00" {
		t.Fatalf("publish date: %v", r.PublishDate)
	}
	if r.DownloadVolumeFactor == nil || *r.DownloadVolumeFactor != 0 || r.UploadVolumeFactor == nil || *r.UploadVolumeFactor != 1 {
		t.Fatalf("factors: %v/%v", r.DownloadVolumeFactor, r.UploadVolumeFactor)
	}
	if strings.Join(r.Languages, ",") != "Dual,Inglês" {
		t.Fatalf("languages: %v", r.Languages)
	}
	if strings.Join(r.Genres, ",") != "Ação,Drama" {
		t.Fatalf("genres: %v", r.Genres)
	}
	if strings.Join(r.Subs, ",") != "Português" {
		t.Fatalf("subs: %v", r.Subs)
	}
	found := false
	for _, c := range r.Category {
		if c == models.CatMovies.ID {
			found = true
		}
	}
	if !found {
		t.Fatalf("category: %v, want 2000 included", r.Category)
	}

	r2 := releases[1]
	if r2.Title != "Movie Name 2023 DUAL 5.1 Subbed 1080p" {
		t.Fatalf("r2 title: %q", r2.Title)
	}
	if r2.MagnetUri == nil || !strings.Contains(r2.MagnetUri.String(), "urn:btih:BBBB2222") {
		t.Fatalf("r2 magnet: %v", r2.MagnetUri)
	}

	// show: no Tamanho → size by resolution (720p → 1GB); S01 → TV category
	r3 := releases[2]
	if r3.Title != "Show Name S01 Dual 720p" {
		t.Fatalf("r3 title: %q", r3.Title)
	}
	if r3.Size == nil || *r3.Size != 1073741824 { // 1 GiB by 720p resolution
		t.Fatalf("r3 size: %v", r3.Size)
	}
	foundTV := false
	for _, c := range r3.Category {
		if c == models.CatTV.ID {
			foundTV = true
		}
	}
	if !foundTV {
		t.Fatalf("r3 category: %v, want 5000 included", r3.Category)
	}
	// no published_time meta → DateTime.Today (local midnight)
	if r3.PublishDate.Hour() != 0 || r3.PublishDate.Minute() != 0 {
		t.Fatalf("r3 publish date: %v", r3.PublishDate)
	}
}

func TestHDRTorrentSearchURL(t *testing.T) {
	ix, err := NewHDRTorrent(services.Deps{})
	if err != nil {
		t.Fatalf("NewHDRTorrent: %v", err)
	}
	h := ix.(*HDRTorrent)

	if u := h.SearchURL(models.NewTorznabQuery()); u != "https://hdrtorrents.net/" {
		t.Fatalf("empty search url: %q", u)
	}
	q := models.NewTorznabQuery()
	q.SearchTerm = "some term"
	if u := h.SearchURL(q); u != "https://hdrtorrents.net/index.php?busca=some+term" {
		t.Fatalf("search url: %q", u)
	}
	season := 2
	q.Season = &season
	if u := h.SearchURL(q); u != "https://hdrtorrents.net/index.php?busca=some+term+2" {
		t.Fatalf("season search url: %q", u)
	}
}

func TestHDRTorrentApplyConfiguration(t *testing.T) {
	site := newHDRSite(t)
	h := newTestHDR(t, site)
	status, err := h.ApplyConfiguration(context.Background(), []indexer.ConfigItem{
		{ID: "sitelink", Value: site.URL + "/"},
	})
	if err != nil {
		t.Fatalf("ApplyConfiguration: %v", err)
	}
	if status != indexer.ConfigCompleted || !h.IsConfigured() {
		t.Fatalf("status/configured: %v %v", status, h.IsConfigured())
	}
	if h.SiteLink() != site.URL+"/" {
		t.Fatalf("site link: %q", h.SiteLink())
	}
}
