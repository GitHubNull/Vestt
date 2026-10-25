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

type brSite struct {
	srv   *httptest.Server
	mu    sync.Mutex
	hits  []string
	serve map[string]string
}

func newBrSite(t *testing.T, serve map[string]string) *brSite {
	t.Helper()
	s := &brSite{serve: serve}
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
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(s.srv.Close)
	return s
}

func (s *brSite) paths() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.hits...)
}

func newTestBrasilTracker(t *testing.T, site *brSite) *BrasilTracker {
	t.Helper()
	client, err := httpclient.New(httpclient.Config{}, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("httpclient.New: %v", err)
	}
	ix, err := NewBrasilTracker(services.Deps{Client: client})
	if err != nil {
		t.Fatalf("NewBrasilTracker: %v", err)
	}
	b := ix.(*BrasilTracker)
	b.SiteLinkValue = site.srv.URL + "/"
	client.SetRequestDelay(b.ID(), 0)
	return b
}

// the year/category text must immediately follow the details anchor
// (upstream reads anchor.NextSibling) — keep it on the same line.
const brBrowsePage = `<html><body>
<table class="torrent_table"><tbody>
<tr class="colhead"><td>type</td><td>name</td><td>size</td><td>snatches</td><td>seeders</td><td>leechers</td></tr>
<tr class="group">
 <td class="center cats">x</td>
 <td class="big_info">
  <a href="torrents.php?id=777">Breaking Bad [Breaking Bad]</a> [2019] [Séries]
  <img alt="Cover" src="https://brasiltracker.org/covers/777.jpg"/>
  <a href="https://www.imdb.com/title/tt0903747">IMDb</a>
  <a href="https://www.themoviedb.org/tv/1396">TMDb</a>
  <div class="tags">Drama, Crime</div>
 </td>
</tr>
<tr class="group_torrent">
 <td class="center cats">x</td>
 <td class="nobr">
  <a href="torrents.php?id=777" class="tooltip">Breaking Bad S02E05 [1080p / WEB / Dual Áudio / Free]</a>
  <a href="torrents.php?torrentid=888">S02E05</a>
  <strong>Free</strong>
  <a href="torrents.php?action=download&amp;id=888">DL</a>
 </td>
 <td class="nobr">1,4 GB</td>
 <td>15</td>
 <td>5</td>
 <td>2</td>
</tr>
<tr class="torrent">
 <td class="nobr">
  <a href="torrents.php?id=555">Some Movie [Some Movie]</a> [2020] [Filmes]
  <img alt="Cover" src="https://brasiltracker.org/covers/555.jpg"/>
  <a href="https://www.imdb.com/title/tt1234567">IMDb</a>
  <div class="tags">Action</div>
  <div class="torrent_info">Some Movie 4K / Nacional / SD</div>
  <strong>Free</strong>
  <a href="torrents.php?action=download&amp;id=555">DL</a>
 </td>
 <td>2,1 GB</td>
 <td>10</td>
 <td>8</td>
 <td>1</td>
</tr>
</tbody></table>
</body></html>`

func TestBrasilTrackerCaps(t *testing.T) {
	ix, err := NewBrasilTracker(services.Deps{})
	if err != nil {
		t.Fatalf("NewBrasilTracker: %v", err)
	}
	caps := ix.Capabilities()
	if strings.Join(caps.TVSearchParams, ",") != "q,season,ep,genre" {
		t.Fatalf("TVSearchParams: %v", caps.TVSearchParams)
	}
	if strings.Join(caps.MovieSearchParams, ",") != "q,imdbid,genre" {
		t.Fatalf("MovieSearchParams: %v", caps.MovieSearchParams)
	}
	if strings.Join(caps.MusicSearchParams, ",") != "q,genre" {
		t.Fatalf("MusicSearchParams: %v", caps.MusicSearchParams)
	}
	if strings.Join(caps.BookSearchParams, ",") != "q,genre" {
		t.Fatalf("BookSearchParams: %v", caps.BookSearchParams)
	}
	for _, tc := range []struct {
		tracker string
		want    int
	}{
		{"16", 3030}, {"6", 5070}, {"11", 4010}, {"15", 8000}, {"8", 5080},
		{"14", 5060}, {"3", 6000}, {"1", 2000}, {"12", 7030}, {"9", 4050},
		{"13", 7020}, {"10", 7010}, {"2", 5000}, {"5", 3020}, {"7", 5000},
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
	if ix.Type() != "private" || ix.Language() != "pt-BR" {
		t.Fatalf("identity: %s %s", ix.Type(), ix.Language())
	}
}

func TestBrasilTrackerConfigForm(t *testing.T) {
	ix, err := NewBrasilTracker(services.Deps{})
	if err != nil {
		t.Fatalf("NewBrasilTracker: %v", err)
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
	want := []string{"sitelink", "cookie", "searchfreeleechonly", "accountinactivity", "tags"}
	if strings.Join(ids, ",") != strings.Join(want, ",") {
		t.Fatalf("config ids: %v", ids)
	}
}

func TestBrasilTrackerSearchRequestAndParse(t *testing.T) {
	site := newBrSite(t, map[string]string{
		"/torrents.php": brBrowsePage,
	})
	b := newTestBrasilTracker(t, site)

	q := models.NewTorznabQuery()
	q.QueryType = "search"
	q.SearchTerm = "Breaking Bad"
	res, err := b.ResultsForQuery(context.Background(), q, false)
	if err != nil {
		t.Fatalf("ResultsForQuery: %v", err)
	}
	// the group release matches; the standalone torrent row fails the
	// upstream AND-filter for this search term
	if len(res.Releases) != 1 {
		t.Fatalf("releases: %d", len(res.Releases))
	}

	r := res.Releases[0]
	// the description elements keep their inner brackets (upstream splits on
	// " / " after trimming only the outermost [ ])
	wantTitle := "Breaking Bad [2019] [Séries] S02E05 Breaking Bad S02E05 [1080p WEB-DL Dual Brazilian"
	if r.Title != wantTitle {
		t.Fatalf("title: %q want %q", r.Title, wantTitle)
	}
	if r.Description != "Breaking Bad S02E05 [1080p / WEB-DL / Dual]" {
		t.Fatalf("description: %q", r.Description)
	}
	if r.Size == nil || *r.Size != 1503238553 {
		t.Fatalf("size: %v", r.Size)
	}
	if r.Grabs == nil || *r.Grabs != 15 {
		t.Fatalf("grabs: %v", r.Grabs)
	}
	if r.Seeders == nil || *r.Seeders != 5 {
		t.Fatalf("seeders: %v", r.Seeders)
	}
	if r.Peers == nil || *r.Peers != 7 {
		t.Fatalf("peers: %v", r.Peers)
	}
	if r.DownloadVolumeFactor == nil || *r.DownloadVolumeFactor != 0 {
		t.Fatalf("dvf: %v", r.DownloadVolumeFactor)
	}
	if r.UploadVolumeFactor == nil || *r.UploadVolumeFactor != 1 {
		t.Fatalf("uvf: %v", r.UploadVolumeFactor)
	}
	if r.MinimumRatio == nil || *r.MinimumRatio != 1 || r.MinimumSeedTime == nil || *r.MinimumSeedTime != 172800 {
		t.Fatalf("minimums: %v %v", r.MinimumRatio, r.MinimumSeedTime)
	}
	if r.Imdb == nil || *r.Imdb != 903747 {
		t.Fatalf("imdb: %v", r.Imdb)
	}
	if r.TMDb == nil || *r.TMDb != 1396 {
		t.Fatalf("tmdb: %v", r.TMDb)
	}
	if strings.Join(r.Genres, ",") != "Drama,Crime" {
		t.Fatalf("genres: %v", r.Genres)
	}
	foundCat := false
	for _, id := range r.Category {
		if id == 5000 {
			foundCat = true
		}
	}
	if !foundCat {
		t.Fatalf("category: %v", r.Category)
	}
	if r.Poster == nil || r.Poster.String() != "https://brasiltracker.org/covers/777.jpg" {
		t.Fatalf("poster: %v", r.Poster)
	}
	if r.Link == nil || !strings.HasSuffix(r.Link.Path, "/torrents.php") || !strings.Contains(r.Link.RawQuery, "action=download") {
		t.Fatalf("link: %v", r.Link)
	}
	if r.Details == nil || !strings.Contains(r.Details.RawQuery, "id=777") {
		t.Fatalf("details: %v", r.Details)
	}
	now := brToday()
	if r.PublishDate != now {
		t.Fatalf("publish date: %v want %v", r.PublishDate, now)
	}

	// search URL construction (ordered query collection)
	paths := site.paths()
	wantPrefix := "/torrents.php?searchstr=Breaking+Bad&order_by=time&order_way=desc&group_results=1&action=basic&searchsubmit=1"
	if len(paths) != 1 || paths[0] != wantPrefix {
		t.Fatalf("paths: %v", paths)
	}
}

func TestBrasilTrackerStandaloneTorrentRow(t *testing.T) {
	site := newBrSite(t, map[string]string{
		"/torrents.php": brBrowsePage,
	})
	b := newTestBrasilTracker(t, site)

	q := models.NewTorznabQuery()
	q.QueryType = "search"
	q.SearchTerm = "Some Movie"
	res, err := b.ResultsForQuery(context.Background(), q, false)
	if err != nil {
		t.Fatalf("ResultsForQuery: %v", err)
	}
	if len(res.Releases) != 1 {
		t.Fatalf("releases: %d", len(res.Releases))
	}
	r := res.Releases[0]
	wantTitle := "Some Movie [2020] [Filmes] Some Movie 2160p Nacional 480p Brazilian"
	if r.Title != wantTitle {
		t.Fatalf("title: %q want %q", r.Title, wantTitle)
	}
	if r.Imdb == nil || *r.Imdb != 1234567 {
		t.Fatalf("imdb: %v", r.Imdb)
	}
	if r.TMDb != nil {
		t.Fatalf("tmdb must be nil: %v", r.TMDb)
	}
	if strings.Join(r.Genres, ",") != "Action" {
		t.Fatalf("genres: %v", r.Genres)
	}
	foundCat := false
	for _, id := range r.Category {
		if id == 2000 {
			foundCat = true
		}
	}
	if !foundCat {
		t.Fatalf("category: %v", r.Category)
	}
	if r.Size == nil || *r.Size != 2254857830 {
		t.Fatalf("size: %v", r.Size)
	}
	if r.Grabs == nil || *r.Grabs != 10 || r.Seeders == nil || *r.Seeders != 8 || r.Peers == nil || *r.Peers != 9 {
		t.Fatalf("stats: %v %v %v", r.Grabs, r.Seeders, r.Peers)
	}
}

func TestBrasilTrackerCategoryGenreFreeleechParams(t *testing.T) {
	site := newBrSite(t, map[string]string{
		"/torrents.php": brBrowsePage,
	})
	b := newTestBrasilTracker(t, site)
	b.cfg.FreeLeech.Value = true

	q := models.NewTorznabQuery()
	q.QueryType = "search"
	q.SearchTerm = "Some Movie"
	q.Categories = []int{2000}
	q.Genre = "Action"
	res, err := b.ResultsForQuery(context.Background(), q, false)
	if err != nil {
		t.Fatalf("ResultsForQuery: %v", err)
	}
	if len(res.Releases) != 1 {
		t.Fatalf("releases: %d", len(res.Releases))
	}
	paths := site.paths()
	want := "/torrents.php?searchstr=Some+Movie&order_by=time&order_way=desc&group_results=1&action=basic&searchsubmit=1&filter_cat%5B1%5D=1&taglist=Action&freetorrent=1"
	if len(paths) != 1 || paths[0] != want {
		t.Fatalf("paths: %v", paths)
	}
}

func TestBrasilTrackerApplyConfiguration(t *testing.T) {
	site := newBrSite(t, map[string]string{
		"/torrents.php": brBrowsePage,
	})
	b := newTestBrasilTracker(t, site)
	status, err := b.ApplyConfiguration(context.Background(), []indexer.ConfigItem{
		{ID: "sitelink", Value: site.srv.URL + "/"},
		{ID: "cookie", Value: "session=abc"},
	})
	if err != nil {
		t.Fatalf("ApplyConfiguration: %v", err)
	}
	if status != indexer.ConfigCompleted {
		t.Fatalf("status: %v", status)
	}
	if !b.IsConfigured() {
		t.Fatal("must be configured")
	}
	if b.CookieHeader() != "session=abc" {
		t.Fatalf("cookie header: %q", b.CookieHeader())
	}
}

func TestBrasilTrackerApplyConfigurationZeroResults(t *testing.T) {
	site := newBrSite(t, map[string]string{
		"/torrents.php": `<html><body><table class="torrent_table"><tbody>
<tr class="colhead"><td>x</td></tr>
</tbody></table></body></html>`,
	})
	b := newTestBrasilTracker(t, site)
	_, err := b.ApplyConfiguration(context.Background(), []indexer.ConfigItem{
		{ID: "sitelink", Value: site.srv.URL + "/"},
		{ID: "cookie", Value: "session=abc"},
	})
	if err == nil || err.Error() != "Your cookie did not work: Found 0 results in the tracker" {
		t.Fatalf("err: %v", err)
	}
	if b.IsConfigured() {
		t.Fatal("must not be configured after failure")
	}
}

func TestBrasilTrackerHelpers(t *testing.T) {
	// StripSearchString removes SxxEyy tokens and trailing whitespace
	if got := brStripSearchString("Breaking Bad S02E05 "); got != "Breaking Bad" {
		t.Fatalf("strip: %q", got)
	}
	// InternationalTitle takes the first slash-separated part of the bracket
	if got := brInternationalTitle("Título [Original Title / Título Nacional]"); got != "Original Title " {
		t.Fatalf("intl title: %q", got)
	}
	if got := brInternationalTitle("No Brackets"); got != "No Brackets" {
		t.Fatalf("intl title: %q", got)
	}
	// category extraction from the year/category suffix
	if got := brCategoryFromYearStr("[2019] [Séries]"); got != "Séries" {
		t.Fatalf("category: %q", got)
	}
	if got := brCategoryFromYearStr("2019"); got != "2019" {
		t.Fatalf("category no brackets: %q", got)
	}
	// ParseTitle removes the year, folds the international title, re-adds
	// year + season/episode
	if got := brParseTitle("Breaking Bad [Breaking Bad]", "S02E05", "[2019] [Séries]"); got != "Breaking Bad [2019] [Séries] S02E05" {
		t.Fatalf("parse title: %q", got)
	}
}
