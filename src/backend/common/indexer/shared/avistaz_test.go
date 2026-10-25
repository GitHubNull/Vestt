package shared

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"vestt/common/httpclient"
	"vestt/common/indexer"
	"vestt/common/indexer/configdata"
	"vestt/common/models"
)

func avistazTestCaps() *models.Capabilities {
	caps := models.NewCapabilities()
	caps.MovieSearchParams = []string{models.MovieParamQ, models.MovieParamImdbID, models.MovieParamTmdbID}
	caps.TVSearchParams = []string{models.TVParamQ, models.TVParamSeason, models.TVParamEp, models.TVParamImdbID, models.TVParamTvdbID, models.TVParamTmdbID}
	caps.Categories.AddCategoryMapping("1", models.CatMovies, "Movies")
	caps.Categories.AddCategoryMapping("2", models.CatTV, "TV")
	caps.Categories.AddCategoryMapping("3", models.CatAudio, "Music")
	return caps
}

func newAvistaz(site string, caps *models.Capabilities) *Avistaz {
	a := &Avistaz{}
	a.IDValue = "avistaztest"
	a.NameValue = "Avistaz Test"
	a.SiteLinkValue = site
	a.DefaultSiteLink = site
	a.LangValue = "en-US"
	a.TypeValue = "private"
	a.Caps = caps
	a.Bind(NewAvistazConfig(""))
	return a
}

const avistazFixture = `{
  "data": [
    {
      "url": "https://avistaz.faketest/torrent/10-movie-1080p",
      "download": "https://avistaz.faketest/download/10",
      "category": {"1": "Movies"},
      "movie_tv": {"tmdb": "1234", "tvdb": "", "imdb": "tt0133093"},
      "created_at_iso": "2024-03-01 12:34:56",
      "file_name": "Movie.Name.1999.1080p.BluRay.x264",
      "release_title": "Movie Name 1999",
      "info_hash": "ABCDEF0123456789ABCDEF0123456789ABCDEF01",
      "leech": 2,
      "completed": 5,
      "seed": 7,
      "file_size": 2147483648,
      "file_count": 12,
      "download_multiply": 0.5,
      "upload_multiply": 1.0,
      "video_quality": "1080p",
      "type": "MOVIE",
      "format": "x264",
      "audio": [{"id": 1, "language": "English"}],
      "subtitle": [{"id": 2, "language": "Spanish"}]
    },
    {
      "url": "https://avistaz.faketest/torrent/20-show-sd",
      "download": "https://avistaz.faketest/download/20",
      "category": {"2": "TV"},
      "movie_tv": null,
      "created_at_iso": "2024-03-02 08:00:00",
      "file_name": "Show.Name.S01E02.480p",
      "release_title": "Show Name",
      "info_hash": "0123456789ABCDEF0123456789ABCDEF01234567",
      "leech": 0,
      "completed": 1,
      "seed": 3,
      "file_size": 524288000,
      "file_count": 5,
      "download_multiply": 1.0,
      "upload_multiply": 1.0,
      "video_quality": "480p",
      "type": "TV-SHOW",
      "format": "x264",
      "audio": [],
      "subtitle": []
    },
    {
      "url": "https://avistaz.faketest/torrent/30-music",
      "download": "https://avistaz.faketest/download/30",
      "category": {"3": "Music"},
      "movie_tv": null,
      "created_at_iso": "2024-03-03 09:00:00",
      "file_name": "Artist - Album FLAC",
      "release_title": "Album",
      "info_hash": "",
      "leech": null,
      "completed": null,
      "seed": null,
      "file_size": 0,
      "file_count": null,
      "download_multiply": null,
      "upload_multiply": null,
      "video_quality": "",
      "type": "MUSIC",
      "format": "FLAC",
      "audio": [{"id": 3, "language": "Japanese"}],
      "subtitle": []
    }
  ]
}`

// avistazSite is a fake AvistaZ-family API site.
type avistazSite struct {
	*httptest.Server

	mu           sync.Mutex
	authPosts    int
	searchGets   int
	lastAuth     string
	lastAccept   string
	lastAuthForm map[string]string
	rejectToken  bool // when true the current token is rejected once (401)
}

func newAvistazSite(t *testing.T, authOK bool) *avistazSite {
	t.Helper()
	s := &avistazSite{}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()
		switch r.URL.Path {
		case "/api/v1/jackett/auth":
			s.authPosts++
			s.lastAccept = r.Header.Get("Accept")
			if err := r.ParseForm(); err != nil {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			s.lastAuthForm = map[string]string{
				"username": r.PostForm.Get("username"),
				"password": r.PostForm.Get("password"),
				"pid":      r.PostForm.Get("pid"),
			}
			w.Header().Set("Content-Type", "application/json")
			if authOK && r.PostForm.Get("username") == "user" && r.PostForm.Get("password") == "pass" && r.PostForm.Get("pid") == "pid" {
				_, _ = fmt.Fprint(w, `{"token":"tok123"}`)
				return
			}
			_, _ = fmt.Fprint(w, `{"token":null,"message":"Invalid credentials"}`)
		case "/api/v1/jackett/torrents":
			s.searchGets++
			auth := r.Header.Get("Authorization")
			s.lastAuth = auth
			if s.rejectToken && auth == "Bearer tok123" {
				s.rejectToken = false
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			if auth != "Bearer tok123" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			q := r.URL.Query()
			switch {
			case q.Get("imdb") == "tt9999999":
				w.WriteHeader(http.StatusNotFound)
			case q.Get("search") == "servererror":
				w.WriteHeader(http.StatusInternalServerError)
			case q.Get("search") == "ratelimited":
				w.Header().Set("Retry-After", "7")
				w.WriteHeader(http.StatusTooManyRequests)
			case q.Get("search") == "notjson":
				_, _ = fmt.Fprint(w, `<html>oops</html>`)
			default:
				w.Header().Set("Content-Type", "application/json")
				_, _ = fmt.Fprint(w, avistazFixture)
			}
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(s.Close)
	return s
}

func TestAvistazConfigJSON(t *testing.T) {
	cfg := NewAvistazConfig("")
	items, err := configdata.ItemsJSON(cfg)
	if err != nil {
		t.Fatalf("ItemsJSON: %v", err)
	}
	got := make([]string, 0, len(items))
	for _, m := range items {
		got = append(got, fmt.Sprintf("%s:%s", m["id"], m["type"]))
	}
	want := []string{
		"sitelink:inputstring", "username:inputstring", "password:inputstring",
		"pid:inputstring", ":displayinfo", "searchfreeleechonly:inputbool",
		"accountinactivity:displayinfo", "tags:inputtags",
	}
	if fmt.Sprintf("%v", got) != fmt.Sprintf("%v", want) {
		t.Fatalf("items = %v, want %v", got, want)
	}
	// the default instruction message is the upstream PID hint
	if items[4]["value"] != avistazInstructions {
		t.Fatalf("instructions = %q", items[4]["value"])
	}
}

func TestAvistazSearchURL(t *testing.T) {
	site := "https://avistaz.example/"
	a := newAvistaz(site, avistazTestCaps())

	// plain search with a movie category: in=1&type=1&limit=50&search=...
	q := models.NewTorznabQuery()
	q.SearchTerm = "Some Movie"
	q.Categories = []int{models.CatMovies.ID}
	got := a.SearchURL(q)
	want := site + "api/v1/jackett/torrents?in=1&type=1&limit=50&search=Some+Movie"
	if got != want {
		t.Fatalf("url = %q want %q", got, want)
	}

	// pagination
	q2 := models.NewTorznabQuery()
	q2.SearchTerm = "x"
	q2.Limit = 50
	q2.Offset = 100
	if got := a.SearchURL(q2); !strings.Contains(got, "&page=3&") {
		t.Fatalf("pagination url = %s", got)
	}

	// imdb query uses the episode search term as the search param
	q3 := models.NewTorznabQuery()
	q3.QueryType = "tvsearch"
	q3.ImdbID = "tt0133093"
	season := 1
	q3.Season = &season
	q3.Episode = "2"
	got3 := a.SearchURL(q3)
	for _, part := range []string{"imdb=tt0133093", "search=S01E02", "type=0"} {
		if !strings.Contains(got3, part) {
			t.Fatalf("%s missing in %s", part, got3)
		}
	}

	// UHD/HD categories add video_quality filters and keep type=0
	q4 := models.NewTorznabQuery()
	q4.Categories = []int{models.CatMoviesUHD.ID, models.CatMoviesHD.ID}
	got4 := a.SearchURL(q4)
	for _, part := range []string{"video_quality[]=6", "video_quality[]=2", "video_quality[]=7", "video_quality[]=3", "search="} {
		if !strings.Contains(got4, part) {
			t.Fatalf("%s missing in %s", part, got4)
		}
	}

	// freeleech config adds discount[]; SD categories map back to type
	a.Config.Freeleech.Value = true
	q5 := models.NewTorznabQuery()
	q5.Categories = []int{models.CatTVSD.ID}
	got5 := a.SearchURL(q5)
	if !strings.Contains(got5, "discount[]=1") {
		t.Fatalf("discount missing in %s", got5)
	}
}

func TestAvistazAuthAndSearch(t *testing.T) {
	site := newAvistazSite(t, true)
	a := newAvistaz(site.URL+"/", avistazTestCaps())
	a.Client = testClient(t)

	status, err := a.ApplyConfiguration(context.Background(), []indexer.ConfigItem{
		{ID: "username", Value: "user"},
		{ID: "password", Value: "pass"},
		{ID: "pid", Value: "pid"},
	})
	if err != nil {
		t.Fatalf("ApplyConfiguration: %v", err)
	}
	if status != indexer.ConfigCompleted || !a.IsConfigured() {
		t.Fatalf("status=%v configured=%v", status, a.IsConfigured())
	}
	if site.lastAccept != "application/json" {
		t.Fatalf("Accept = %q", site.lastAccept)
	}
	wantForm := map[string]string{"username": "user", "password": "pass", "pid": "pid"}
	if fmt.Sprintf("%v", site.lastAuthForm) != fmt.Sprintf("%v", wantForm) {
		t.Fatalf("auth form = %v", site.lastAuthForm)
	}

	// bearer token on search requests
	res, err := a.ResultsForQuery(context.Background(), &models.TorznabQuery{
		QueryType: "search", SearchTerm: "movie", Cache: true,
	}, false)
	if err != nil {
		t.Fatalf("ResultsForQuery: %v", err)
	}
	if site.lastAuth != "Bearer tok123" {
		t.Fatalf("Authorization = %q", site.lastAuth)
	}
	releases := res.Releases
	if len(releases) != 3 {
		t.Fatalf("got %d releases, want 3", len(releases))
	}

	r0 := releases[0]
	if r0.Title != "Movie.Name.1999.1080p.BluRay.x264" {
		t.Fatalf("title = %q", r0.Title)
	}
	if r0.Category == nil || len(r0.Category) != 1 || r0.Category[0] != models.CatMoviesHD.ID {
		t.Fatalf("category = %v", r0.Category)
	}
	if r0.Imdb == nil || *r0.Imdb != 133093 {
		t.Fatalf("imdb = %v", r0.Imdb)
	}
	if r0.TMDb == nil || *r0.TMDb != 1234 {
		t.Fatalf("tmdb = %v", r0.TMDb)
	}
	if r0.TVDBId != nil {
		t.Fatalf("tvdb = %v", r0.TVDBId)
	}
	if r0.PublishDate.Format("2006-01-02 15:04:05") != "2024-03-01 12:34:56" {
		t.Fatalf("date = %v", r0.PublishDate)
	}
	if r0.Description != "Audio: English<br/>Subtitles: Spanish" {
		t.Fatalf("description = %q", r0.Description)
	}
	if r0.Size == nil || *r0.Size != 2147483648 {
		t.Fatalf("size = %v", r0.Size)
	}
	if r0.Files == nil || *r0.Files != 12 || r0.Grabs == nil || *r0.Grabs != 5 {
		t.Fatalf("files/grabs = %v/%v", r0.Files, r0.Grabs)
	}
	if r0.Seeders == nil || *r0.Seeders != 7 || r0.Peers == nil || *r0.Peers != 9 {
		t.Fatalf("seeders/peers = %v/%v", r0.Seeders, r0.Peers)
	}
	if r0.DownloadVolumeFactor == nil || *r0.DownloadVolumeFactor != 0.5 || r0.UploadVolumeFactor == nil || *r0.UploadVolumeFactor != 1.0 {
		t.Fatalf("dvf/uvf = %v/%v", r0.DownloadVolumeFactor, r0.UploadVolumeFactor)
	}
	if r0.MinimumRatio == nil || *r0.MinimumRatio != 1 {
		t.Fatalf("min ratio = %v", r0.MinimumRatio)
	}
	// 2 GiB <= 50 → 259200 + 2*7200
	if r0.MinimumSeedTime == nil || *r0.MinimumSeedTime != 273600 {
		t.Fatalf("min seed time = %v", r0.MinimumSeedTime)
	}
	if len(r0.Languages) != 1 || r0.Languages[0] != "English" || len(r0.Subs) != 1 || r0.Subs[0] != "Spanish" {
		t.Fatalf("languages/subs = %v/%v", r0.Languages, r0.Subs)
	}
	if r0.Details == nil || r0.Details.String() != "https://avistaz.faketest/torrent/10-movie-1080p" {
		t.Fatalf("details = %v", r0.Details)
	}
	if r0.Link == nil || r0.Link.String() != "https://avistaz.faketest/download/10" {
		t.Fatalf("link = %v", r0.Link)
	}
	if r0.Guid != r0.Details {
		t.Fatal("guid must equal details")
	}

	// TV-SHOW SD
	r1 := releases[1]
	if r1.Category[0] != models.CatTVSD.ID {
		t.Fatalf("category[1] = %v", r1.Category)
	}

	// MUSIC with null numerics
	r2 := releases[2]
	if r2.Category[0] != models.CatAudio.ID {
		t.Fatalf("category[2] = %v", r2.Category)
	}
	if r2.Seeders != nil || r2.Peers != nil || r2.Size == nil || *r2.Size != 0 {
		t.Fatalf("null numerics: seeders=%v peers=%v size=%v", r2.Seeders, r2.Peers, r2.Size)
	}
	if r2.MinimumSeedTime == nil || *r2.MinimumSeedTime != 259200 {
		t.Fatalf("zero size min seed time = %v", r2.MinimumSeedTime)
	}
}

func TestAvistazMinimumSeedTime(t *testing.T) {
	// default / zero / negative
	if got := AvistazMinimumSeedTime(nil); got != 259200 {
		t.Fatalf("nil = %d", got)
	}
	if got := AvistazMinimumSeedTime(ptrInt64(0)); got != 259200 {
		t.Fatalf("zero = %d", got)
	}
	// 2 GiB: 259200 + 2*7200
	if got := AvistazMinimumSeedTime(ptrInt64(2 * 1024 * 1024 * 1024)); got != 273600 {
		t.Fatalf("2GiB = %d", got)
	}
	// 100 GiB: (long)(100*ln(100) - 219.2023) * 3600
	want := int64(100*math.Log(100)-219.2023) * 3600
	if got := AvistazMinimumSeedTime(ptrInt64(100 * 1024 * 1024 * 1024)); got != want {
		t.Fatalf("100GiB = %d, want %d", got, want)
	}
}

func TestAvistazAuthErrors(t *testing.T) {
	// bad credentials surface the upstream message
	site := newAvistazSite(t, true)
	a := newAvistaz(site.URL+"/", avistazTestCaps())
	a.Client = testClient(t)
	_, err := a.ApplyConfiguration(context.Background(), []indexer.ConfigItem{
		{ID: "username", Value: "user"},
		{ID: "password", Value: "wrong"},
		{ID: "pid", Value: "pid"},
	})
	if err == nil || err.Error() != "Invalid credentials" {
		t.Fatalf("err = %v", err)
	}

	// non-JSON auth response
	site2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, `<html>not json</html>`)
	}))
	t.Cleanup(site2.Close)
	a2 := newAvistaz(site2.URL+"/", avistazTestCaps())
	a2.Client = testClient(t)
	err = a2.RenewalToken(context.Background())
	if err == nil || err.Error() != "Invalid response from AvistaZ, the response is not valid JSON" {
		t.Fatalf("err = %v", err)
	}

	// {"token": null} without message
	site3 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, `{"token":null}`)
	}))
	t.Cleanup(site3.Close)
	a3 := newAvistaz(site3.URL+"/", avistazTestCaps())
	a3.Client = testClient(t)
	err = a3.RenewalToken(context.Background())
	if err == nil || err.Error() != "Unauthorized request to indexer" {
		t.Fatalf("err = %v", err)
	}
}

func TestAvistazTokenRenewalOn401(t *testing.T) {
	site := newAvistazSite(t, true)
	site.rejectToken = true
	a := newAvistaz(site.URL+"/", avistazTestCaps())
	a.Client = testClient(t)
	a.AuthorizationToken = "tok123"
	a.Config.Username.Value = "user"
	a.Config.Password.Value = "pass"
	a.Config.Pid.Value = "pid"

	res, err := a.ResultsForQuery(context.Background(), &models.TorznabQuery{
		QueryType: "search", SearchTerm: "movie", Cache: true,
	}, false)
	if err != nil {
		t.Fatalf("ResultsForQuery: %v", err)
	}
	site.mu.Lock()
	auths, searches := site.authPosts, site.searchGets
	site.mu.Unlock()
	if auths != 1 {
		t.Fatalf("auth posts = %d, want 1 (401 triggers one renewal)", auths)
	}
	if searches != 2 {
		t.Fatalf("search gets = %d, want 2 (retry after renewal)", searches)
	}
	if len(res.Releases) != 3 {
		t.Fatalf("releases = %d", len(res.Releases))
	}
}

func TestAvistazErrorPaths(t *testing.T) {
	newConfigured := func(t *testing.T) *Avistaz {
		t.Helper()
		site := newAvistazSite(t, true)
		a := newAvistaz(site.URL+"/", avistazTestCaps())
		a.Client = testClient(t)
		a.AuthorizationToken = "tok123"
		return a
	}
	search := func(t *testing.T, a *Avistaz, term string) error {
		t.Helper()
		_, err := a.PerformQuery(context.Background(), &models.TorznabQuery{
			QueryType: "search", SearchTerm: term, Cache: true,
		})
		return err
	}

	// 404: empty result set, no error
	if err := search(t, newConfigured(t), ""); err != nil {
		// imdb 404 case is exercised separately; plain search succeeds
		t.Fatalf("err = %v", err)
	}

	a404 := newConfigured(t)
	res, err := a404.PerformQuery(context.Background(), &models.TorznabQuery{
		QueryType: "movie", ImdbID: "tt9999999", Cache: true,
	})
	if err != nil || len(res) != 0 {
		t.Fatalf("404: res=%v err=%v", res, err)
	}

	// 500: invalid status code
	a500 := newConfigured(t)
	err = search(t, a500, "servererror")
	if err == nil || err.Error() != "Invalid status code 500 (InternalServerError) received from indexer" {
		t.Fatalf("err = %v", err)
	}

	// 429: upstream "Rate limited"
	a429 := newConfigured(t)
	err = search(t, a429, "ratelimited")
	var tooMany *httpclient.TooManyRequests
	if !errors.As(err, &tooMany) || tooMany.Message != "Rate limited" {
		t.Fatalf("err = %v", err)
	}

	// broken JSON: parse failure
	aBad := newConfigured(t)
	err = search(t, aBad, "notjson")
	var ie *indexer.IndexerError
	if !errors.As(err, &ie) || ie.Kind != indexer.KindParse || ie.Message != "Parse error" {
		t.Fatalf("err = %v", err)
	}
}

func TestAvistazHooks(t *testing.T) {
	a := newAvistaz("https://avistaz.example/", avistazTestCaps())
	a.GetSearchTerm = func(q *models.TorznabQuery) string { return "hooked" }
	q := models.NewTorznabQuery()
	q.SearchTerm = "ignored"
	if u := a.SearchURL(q); !strings.Contains(u, "search=hooked") {
		t.Fatalf("url = %s", u)
	}

	a.ParseCategories = func(q *models.TorznabQuery, row *AvistazRelease) ([]int, error) {
		return []int{models.CatOther.ID}, nil
	}
	cats, err := a.parseCategories(q, &AvistazRelease{Type: "MOVIE", VideoQuality: "1080p"})
	if err != nil || cats[0] != models.CatOther.ID {
		t.Fatalf("cats = %v err = %v", cats, err)
	}

	// default category parser: unknown type → upstream error
	_, err = DefaultAvistazParseCategories(&AvistazRelease{Type: "BOOKS", VideoQuality: ""})
	if err == nil || err.Error() != "Error parsing category!" {
		t.Fatalf("err = %v", err)
	}

	a.ParseTitle = func(row *AvistazRelease) string { return "custom title" }
	if got := a.parseTitle(&AvistazRelease{FileName: "x"}); got != "custom title" {
		t.Fatalf("title = %q", got)
	}
}

func ptrInt64(v int64) *int64 { return &v }
