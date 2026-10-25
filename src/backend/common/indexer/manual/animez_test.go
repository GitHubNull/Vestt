package manual

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	"vestt/common/httpclient"
	"vestt/common/models"
	"vestt/common/services"
)

type animezSite struct {
	srv *httptest.Server

	mu          sync.Mutex
	hits        []string
	downloads   int
	lastAccept  string
	lastAuthHdr string
}

func newAnimezSite(t *testing.T) *animezSite {
	t.Helper()
	s := &animezSite{}
	s.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		s.hits = append(s.hits, r.URL.RequestURI())
		s.mu.Unlock()
		switch {
		case r.URL.Path == "/api/v1/jackett/auth":
			if err := r.ParseForm(); err != nil {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			if r.PostForm.Get("username") == "user" && r.PostForm.Get("password") == "pass" && r.PostForm.Get("pid") == "pid" {
				_, _ = fmt.Fprint(w, `{"token":"tok123"}`)
				return
			}
			_, _ = fmt.Fprint(w, `{"token":null,"message":"Invalid credentials"}`)
		case r.URL.Path == "/api/v1/jackett/torrents":
			if r.Header.Get("Authorization") != "Bearer tok123" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			switch r.URL.Query().Get("search") {
			case "servererror":
				w.WriteHeader(http.StatusInternalServerError)
			case "notjson":
				_, _ = fmt.Fprint(w, `<html>oops</html>`)
			default:
				w.Header().Set("Content-Type", "application/json")
				_, _ = fmt.Fprint(w, animezFixture)
			}
		case strings.HasPrefix(r.URL.Path, "/download/"):
			s.mu.Lock()
			s.downloads++
			s.lastAccept = r.Header.Get("Accept")
			s.lastAuthHdr = r.Header.Get("Authorization")
			s.mu.Unlock()
			switch r.Header.Get("Authorization") {
			case "Bearer expired":
				w.WriteHeader(http.StatusUnauthorized)
			case "Bearer tok123":
				if r.URL.Path == "/download/10" {
					w.Header().Set("Content-Type", "application/json")
					_, _ = fmt.Fprint(w, "d8:announce")
					return
				}
				w.WriteHeader(http.StatusInternalServerError)
			default:
				w.WriteHeader(http.StatusInternalServerError)
			}
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(s.srv.Close)
	return s
}

func (s *animezSite) paths() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.hits...)
}

func newTestAnimeZ(t *testing.T, site *animezSite) *AnimeZ {
	t.Helper()
	client, err := httpclient.New(httpclient.Config{}, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("httpclient.New: %v", err)
	}
	ix, err := NewAnimeZ(services.Deps{Client: client})
	if err != nil {
		t.Fatalf("NewAnimeZ: %v", err)
	}
	a := ix.(*AnimeZ)
	a.SiteLinkValue = site.srv.URL + "/"
	a.AuthorizationToken = "tok123"
	// tests must not pay the upstream 6s request delay
	client.SetRequestDelay(a.ID(), 0)
	return a
}

const animezFixture = `{
  "data": [
    {
      "url": "https://animez.faketest/torrent/10-one-piece",
      "download": "https://animez.faketest/download/10",
      "category": {"TV": "Anime > TV"},
      "movie_tv": {"tmdb": "12345", "tvdb": "", "imdb": "tt0388629"},
      "created_at_iso": "2024-05-01 10:20:30",
      "file_name": "One.Piece.S01E01.1080p.BluRay.x264-GRP",
      "release_title": "One Piece S01E01 1080p",
      "info_hash": "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA",
      "leech": 3,
      "completed": 10,
      "seed": 12,
      "file_size": 1073741824,
      "file_count": 4,
      "download_multiply": 0.0,
      "upload_multiply": 1.0,
      "video_quality": "1080p",
      "type": "TV-SHOW",
      "format": "TV",
      "audio": [{"id": 79, "language": "Japanese"}],
      "subtitle": [{"id": 45, "language": "English"}]
    },
    {
      "url": "https://animez.faketest/torrent/20-manga",
      "download": "https://animez.faketest/download/20",
      "category": {"MANGA": "Manga > Manga"},
      "movie_tv": null,
      "created_at_iso": "2024-05-02 09:00:00",
      "file_name": "Some.Manga.v01.cbz",
      "release_title": "",
      "info_hash": "",
      "leech": 1,
      "completed": 2,
      "seed": 5,
      "file_size": 52428800,
      "file_count": 1,
      "download_multiply": 1.0,
      "upload_multiply": 1.0,
      "video_quality": "",
      "type": "MUSIC",
      "format": "MANGA",
      "audio": [],
      "subtitle": []
    }
  ]
}`

func TestAnimeZCaps(t *testing.T) {
	ix, err := NewAnimeZ(services.Deps{})
	if err != nil {
		t.Fatalf("NewAnimeZ: %v", err)
	}
	caps := ix.Capabilities()
	if caps.LimitsDefault == nil || *caps.LimitsDefault != 50 || caps.LimitsMax == nil || *caps.LimitsMax != 50 {
		t.Fatalf("limits: %v/%v", caps.LimitsDefault, caps.LimitsMax)
	}
	if strings.Join(caps.TVSearchParams, ",") != "q,season,ep" {
		t.Fatalf("TVSearchParams: %v", caps.TVSearchParams)
	}
	if strings.Join(caps.MovieSearchParams, ",") != "q" {
		t.Fatalf("MovieSearchParams: %v", caps.MovieSearchParams)
	}
	if strings.Join(caps.BookSearchParams, ",") != "q" {
		t.Fatalf("BookSearchParams: %v", caps.BookSearchParams)
	}
	for _, tc := range []struct {
		tracker string
		want    int
	}{
		{"TV", 5070}, {"TV_SHORT", 5070}, {"MOVIE", 2000}, {"SPECIAL", 5070},
		{"OVA", 5070}, {"ONA", 5070}, {"MUSIC", 5070}, {"MANGA", 7030},
		{"NOVEL", 7060}, {"ONE_SHOT", 7060},
	} {
		got := caps.Categories.MapTrackerCatToNewznab(tc.tracker)
		// AddCategoryMapping with a desc also creates a custom 1:1 category
		// (id ≥ 100000) that rides along — upstream parity.
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

func TestAnimeZConfigForm(t *testing.T) {
	ix, err := NewAnimeZ(services.Deps{})
	if err != nil {
		t.Fatalf("NewAnimeZ: %v", err)
	}
	items, err := ix.ConfigurationForSetup(context.Background())
	if err != nil {
		t.Fatalf("ConfigurationForSetup: %v", err)
	}
	var ids []string
	for _, it := range items {
		ids = append(ids, fmt.Sprintf("%s:%s", it["id"].(string), it["type"].(string)))
	}
	want := []string{
		"sitelink:inputstring", "username:inputstring", "password:inputstring",
		"pid:inputstring", ":displayinfo", "searchfreeleechonly:inputbool",
		"accountinactivity:displayinfo", "tags:inputtags",
	}
	if fmt.Sprintf("%v", ids) != fmt.Sprintf("%v", want) {
		t.Fatalf("items = %v, want %v", ids, want)
	}
}

func TestAnimeZSearchRequest(t *testing.T) {
	site := newAnimezSite(t)
	a := newTestAnimeZ(t, site)
	a.Config.Freeleech.Value = true

	q := models.NewTorznabQuery()
	q.QueryType = "tvsearch"
	q.SearchTerm = "Show Name"
	season := 1
	q.Season = &season
	q.Episode = "1"
	q.Categories = []int{models.CatTVAnime.ID}
	q.Limit = 50
	q.Offset = 50

	res, err := a.ResultsForQuery(context.Background(), q, false)
	if err != nil {
		t.Fatalf("ResultsForQuery: %v", err)
	}
	// ResultsForQuery keeps only releases matching the query categories:
	// the MANGA row (Books/Comics) is filtered out for a TV/Anime query.
	if len(res.Releases) != 1 {
		t.Fatalf("releases: %d", len(res.Releases))
	}

	paths := site.paths()
	wantURI := "/api/v1/jackett/torrents?limit=50" +
		"&format[]=TV&format[]=TV_SHORT&format[]=SPECIAL" +
		"&format[]=OVA&format[]=ONA&format[]=MUSIC" +
		"&search=Show+Name+S01E01&page=2&freeleech=1"
	if len(paths) != 1 || paths[0] != wantURI {
		t.Fatalf("uri = %v, want %q", paths, wantURI)
	}
}

func TestAnimeZParseFixture(t *testing.T) {
	site := newAnimezSite(t)
	a := newTestAnimeZ(t, site)

	releases, err := a.PerformQuery(context.Background(), &models.TorznabQuery{QueryType: "search"})
	if err != nil {
		t.Fatalf("PerformQuery: %v", err)
	}
	if len(releases) != 2 {
		t.Fatalf("releases: %d", len(releases))
	}

	r0 := releases[0]
	// ParseTitle: ReleaseTitle takes precedence over FileName
	if r0.Title != "One Piece S01E01 1080p" {
		t.Fatalf("title: %q", r0.Title)
	}
	if r0.Size == nil || *r0.Size != 1073741824 {
		t.Fatalf("size: %v", r0.Size)
	}
	if r0.Seeders == nil || *r0.Seeders != 12 || r0.Peers == nil || *r0.Peers != 15 {
		t.Fatalf("seeders/peers: %v/%v", r0.Seeders, r0.Peers)
	}
	if r0.PublishDate.Format("2006-01-02 15:04:05") != "2024-05-01 10:20:30" {
		t.Fatalf("date: %v", r0.PublishDate)
	}
	// ParseCategories maps row.Format ("TV") back to newznab
	found := false
	for _, id := range r0.Category {
		if id == models.CatTVAnime.ID {
			found = true
		}
	}
	if !found {
		t.Fatalf("category: %v", r0.Category)
	}
	if r0.Link == nil || r0.Link.String() != "https://animez.faketest/download/10" {
		t.Fatalf("link: %v", r0.Link)
	}
	if r0.InfoHash != "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA" {
		t.Fatalf("infohash: %q", r0.InfoHash)
	}
	if r0.Imdb == nil || *r0.Imdb != 388629 {
		t.Fatalf("imdb: %v", r0.Imdb)
	}
	if r0.TMDb == nil || *r0.TMDb != 12345 {
		t.Fatalf("tmdb: %v", r0.TMDb)
	}
	if r0.DownloadVolumeFactor == nil || *r0.DownloadVolumeFactor != 0 {
		t.Fatalf("dvf: %v", r0.DownloadVolumeFactor)
	}

	// empty ReleaseTitle falls back to FileName; MANGA → Books/Comics
	r1 := releases[1]
	if r1.Title != "Some.Manga.v01.cbz" {
		t.Fatalf("title[1]: %q", r1.Title)
	}
	found = false
	for _, id := range r1.Category {
		if id == models.CatBooksComics.ID {
			found = true
		}
	}
	if !found {
		t.Fatalf("category[1]: %v", r1.Category)
	}
}

func TestAnimeZDownload(t *testing.T) {
	site := newAnimezSite(t)
	a := newTestAnimeZ(t, site)

	link, _ := url.Parse(site.srv.URL + "/download/10")
	body, err := a.Download(context.Background(), link)
	if err != nil {
		t.Fatalf("Download: %v", err)
	}
	if string(body) != "d8:announce" {
		t.Fatalf("body: %q", body)
	}
	if site.lastAccept != "application/json" || site.lastAuthHdr != "Bearer tok123" {
		t.Fatalf("headers: accept=%q auth=%q", site.lastAccept, site.lastAuthHdr)
	}

	// 401 → RenewalTokenAsync + one retry
	a.AuthorizationToken = "expired"
	a.Config.Username.Value = "user"
	a.Config.Password.Value = "pass"
	a.Config.Pid.Value = "pid"
	body, err = a.Download(context.Background(), link)
	if err != nil {
		t.Fatalf("Download after renewal: %v", err)
	}
	if string(body) != "d8:announce" {
		t.Fatalf("body: %q", body)
	}
	site.mu.Lock()
	dl := site.downloads
	site.mu.Unlock()
	if dl != 3 { // first download + 401 + retry
		t.Fatalf("download hits: %d", dl)
	}

	// other failures surface the upstream (byte[]-interpolated) message
	a.AuthorizationToken = "tok123"
	bad, _ := url.Parse(site.srv.URL + "/download/99")
	_, err = a.Download(context.Background(), bad)
	if err == nil || err.Error() != "Unknown error in download: System.Byte[]" {
		t.Fatalf("err: %v", err)
	}
}

func TestAnimeZErrorPath(t *testing.T) {
	site := newAnimezSite(t)
	a := newTestAnimeZ(t, site)

	_, err := a.PerformQuery(context.Background(), &models.TorznabQuery{
		QueryType: "search", SearchTerm: "servererror",
	})
	if err == nil || err.Error() != "Invalid status code 500 (InternalServerError) received from indexer" {
		t.Fatalf("err: %v", err)
	}
}
