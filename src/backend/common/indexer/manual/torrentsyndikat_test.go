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

	"vestt/common/httpclient"
	"vestt/common/indexer"
	"vestt/common/models"
	"vestt/common/services"
)

type torrentSyndikatSite struct {
	srv   *httptest.Server
	mu    sync.Mutex
	hits  []string
	serve map[string]func(w http.ResponseWriter, r *http.Request)
}

func newTorrentSyndikatSite(t *testing.T, serve map[string]func(w http.ResponseWriter, r *http.Request)) *torrentSyndikatSite {
	t.Helper()
	s := &torrentSyndikatSite{serve: serve}
	s.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		s.hits = append(s.hits, r.Method+" "+r.URL.RequestURI())
		fn, ok := s.serve[r.URL.Path]
		s.mu.Unlock()
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		fn(w, r)
	}))
	t.Cleanup(s.srv.Close)
	return s
}

func (s *torrentSyndikatSite) paths() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.hits...)
}

func newTestTorrentSyndikat(t *testing.T, site *torrentSyndikatSite) *TorrentSyndikat {
	t.Helper()
	client, err := httpclient.New(httpclient.Config{}, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("httpclient.New: %v", err)
	}
	ix, err := NewTorrentSyndikat(services.Deps{Client: client})
	if err != nil {
		t.Fatalf("NewTorrentSyndikat: %v", err)
	}
	ts := ix.(*TorrentSyndikat)
	ts.SiteLinkValue = site.srv.URL + "/"
	client.SetRequestDelay(ts.ID(), 0)
	return ts
}

const torrentSyndikatBrowseJSON = `{"rows":[
  {"id":"123","name":"Some.Movie.2019.1080p","category":9,"seeders":15,"leechers":3,
   "added":1700000000,"size":2147483648,"numfiles":42,"snatched":7,
   "imdbId":1234567,"tvdbId":12345,"tmdbId":456,
   "poster":"/img/poster.jpg","title":"Some Movie","title_origin":"Orig Title","year":2019,
   "pid":88,"release_type":"P2P","tags":["x264","hd"],"genres":["Action","Drama"]},
  {"id":"124","name":"Other.Show.S01E01.720p","category":53,"seeders":5,"leechers":1,
   "added":1700000100,"size":1073741824,"numfiles":1,"snatched":3,
   "poster":"","title":"","year":null,"tags":[],"genres":null}
]}`

func TestTorrentSyndikatCaps(t *testing.T) {
	ix, err := NewTorrentSyndikat(services.Deps{})
	if err != nil {
		t.Fatalf("NewTorrentSyndikat: %v", err)
	}
	caps := ix.Capabilities()
	for _, tc := range []struct {
		params []string
		want   []string
	}{
		{caps.TVSearchParams, []string{models.TVParamQ, models.TVParamSeason, models.TVParamEp}},
		{caps.MovieSearchParams, []string{models.MovieParamQ, models.MovieParamImdbID}},
		{caps.MusicSearchParams, []string{models.MusicParamQ}},
		{caps.BookSearchParams, []string{models.BookParamQ}},
	} {
		if strings.Join(tc.params, ",") != strings.Join(tc.want, ",") {
			t.Fatalf("params: %v want %v", tc.params, tc.want)
		}
	}
	for _, tc := range []struct {
		tracker string
		want    int
	}{
		{"2", 4000}, {"4", 4030}, {"42", 2045}, {"9", 2040}, {"20", 2040},
		{"43", 5045}, {"53", 5040}, {"15", 5030}, {"30", 5060}, {"24", 3040},
		{"25", 3010}, {"18", 3030}, {"17", 7000}, {"5", 4040}, {"36", 5020},
		{"37", 2010}, {"47", 7000}, {"49", 5060}, {"8", 1090}, {"50", 4050},
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

func TestTorrentSyndikatConfigForm(t *testing.T) {
	ix, err := NewTorrentSyndikat(services.Deps{})
	if err != nil {
		t.Fatalf("NewTorrentSyndikat: %v", err)
	}
	ts := ix.(*TorrentSyndikat)
	items, err := ts.ConfigurationForSetup(context.Background())
	if err != nil {
		t.Fatalf("ConfigurationForSetup: %v", err)
	}
	var ids []string
	for _, it := range items {
		ids = append(ids, it["id"].(string))
	}
	if ids[0] != "sitelink" || ids[1] != "apikey" || ids[len(ids)-1] != "tags" {
		t.Fatalf("config ids: %v", ids)
	}
	var hasProducts, hasReleaseType bool
	for _, it := range items {
		switch it["id"] {
		case "productsonly":
			hasProducts = true
		case "releasetype":
			hasReleaseType = true
			// MultiSelectItem.ToJSON exposes Values as a []string
			// (configdata/items.go:279-286), matching the upstream
			// MultiSelectConfigurationItem.Values string array
			values, _ := it["values"].([]string)
			if len(values) != 3 {
				t.Fatalf("releasetype default values: %v", it["values"])
			}
		}
	}
	if !hasProducts || !hasReleaseType {
		t.Fatalf("missing dynamic items: %v", ids)
	}
}

func TestTorrentSyndikatSearchRequestAndParse(t *testing.T) {
	site := newTorrentSyndikatSite(t, map[string]func(w http.ResponseWriter, r *http.Request){
		"/api_9djWe8Tb2NE3p6opyqnh/v1/browse.php": func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(torrentSyndikatBrowseJSON))
		},
	})
	ts := newTestTorrentSyndikat(t, site)
	ts.cfg.Key.Value = "testkey"

	season := 1
	q := models.NewTorznabQuery()
	q.QueryType = "search"
	q.SearchTerm = "Some Movie"
	q.Season = &season
	// both fixture categories must be requested: the upstream base drops
	// releases whose categories don't intersect the expanded query categories
	// (BaseIndexer.cs:174-183 FilterResults)
	q.Categories = []int{2040, 5040} // MoviesHD → trackers 9,20; TVHD → 53,54

	res, err := ts.ResultsForQuery(context.Background(), q, false)
	if err != nil {
		t.Fatalf("ResultsForQuery: %v", err)
	}
	if len(res.Releases) != 2 {
		t.Fatalf("releases: %d", len(res.Releases))
	}

	// upstream season suffix quirk (cs:174-175): S01 → S01*; TVHD maps to all
	// four trackers 53,54,55,56 (cs:106-115 caps) → cats=9,20,53,54,55,56
	wantURL := "/api_9djWe8Tb2NE3p6opyqnh/v1/browse.php?apikey=testkey&limit=50&ponly=false" +
		"&release_type=P2P&release_type=Scene&release_type=O-Scene" +
		"&searchstring=Some+Movie+S01%2A&cats=9%2C20%2C53%2C54%2C55%2C56"
	paths := site.paths()
	if len(paths) != 1 || paths[0] != "GET "+wantURL {
		t.Fatalf("request: %v want GET %s", paths, wantURL)
	}

	r := res.Releases[0]
	if r.Title != "Some.Movie.2019.1080p" {
		t.Fatalf("title: %q", r.Title)
	}
	// url.URL.Path excludes the query — compare the full String()
	if r.Link == nil || r.Link.String() != site.srv.URL+"/download.php?id=123" {
		t.Fatalf("link: %v", r.Link)
	}
	if r.Guid == nil || r.Guid.String() != site.srv.URL+"/details.php?id=123" {
		t.Fatalf("guid: %v", r.Guid)
	}
	if r.Size == nil || *r.Size != 2147483648 {
		t.Fatalf("size: %v", r.Size)
	}
	if r.Seeders == nil || *r.Seeders != 15 {
		t.Fatalf("seeders: %v", r.Seeders)
	}
	if r.Peers == nil || *r.Peers != 18 {
		t.Fatalf("peers: %v", r.Peers)
	}
	if r.Grabs == nil || *r.Grabs != 7 {
		t.Fatalf("grabs: %v", r.Grabs)
	}
	if r.Files == nil || *r.Files != 42 {
		t.Fatalf("files: %v", r.Files)
	}
	if !hasInt(r.Category, 2040) {
		t.Fatalf("category: %v", r.Category)
	}
	// upstream renders the timestamp in server-local time
	// (cs:195,212: Utc epoch + ToLocalTime) — the wall-clock date depends on
	// TZ, so assert the instant
	if r.PublishDate.Unix() != 1700000000 {
		t.Fatalf("publish date: %v", r.PublishDate)
	}
	if r.Imdb == nil || *r.Imdb != 1234567 {
		t.Fatalf("imdb: %v", r.Imdb)
	}
	if r.TVDBId == nil || *r.TVDBId != 12345 || r.TMDb == nil || *r.TMDb != 456 {
		t.Fatalf("tvdb/tmdb: %v %v", r.TVDBId, r.TMDb)
	}
	if r.Poster == nil || !strings.HasSuffix(r.Poster.Path, "/img/poster.jpg") {
		t.Fatalf("poster: %v", r.Poster)
	}
	if r.Description == "" ||
		!strings.Contains(r.Description, "Title: Some Movie") ||
		!strings.Contains(r.Description, "Original Title: Orig Title") ||
		!strings.Contains(r.Description, "Year: 2019") ||
		!strings.Contains(r.Description, "Product-Link: ") ||
		!strings.Contains(r.Description, "Genres: Action, Drama") ||
		!strings.Contains(r.Description, "Tags: x264, hd") ||
		!strings.Contains(r.Description, "Release Type: P2P") {
		t.Fatalf("description: %q", r.Description)
	}
	if r.MinimumRatio == nil || *r.MinimumRatio != 1 || r.MinimumSeedTime == nil || *r.MinimumSeedTime != 96*60*60 {
		t.Fatalf("minimum ratio/seedtime: %v %v", r.MinimumRatio, r.MinimumSeedTime)
	}
	if r.DownloadVolumeFactor == nil || *r.DownloadVolumeFactor != 1 || r.UploadVolumeFactor == nil || *r.UploadVolumeFactor != 1 {
		t.Fatalf("factors: %v %v", r.DownloadVolumeFactor, r.UploadVolumeFactor)
	}

	// second row: category 53 → TVHD, empty description
	r2 := res.Releases[1]
	if !hasInt(r2.Category, 5040) {
		t.Fatalf("category2: %v", r2.Category)
	}
	if r2.Description != "" {
		t.Fatalf("description2: %q", r2.Description)
	}
	if r2.Poster != nil {
		t.Fatalf("poster2 must be nil: %v", r2.Poster)
	}
}

func TestTorrentSyndikatImdbQueryURL(t *testing.T) {
	site := newTorrentSyndikatSite(t, map[string]func(w http.ResponseWriter, r *http.Request){
		"/api_9djWe8Tb2NE3p6opyqnh/v1/browse.php": func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{"rows":[]}`))
		},
	})
	ts := newTestTorrentSyndikat(t, site)
	ts.cfg.Key.Value = "testkey"

	q := models.NewTorznabQuery()
	q.QueryType = "movie"
	q.ImdbID = "tt1234567"
	q.Categories = []int{2010} // MoviesForeign → tracker 37

	res, err := ts.ResultsForQuery(context.Background(), q, false)
	if err != nil {
		t.Fatalf("ResultsForQuery: %v", err)
	}
	if len(res.Releases) != 0 {
		t.Fatalf("releases: %d", len(res.Releases))
	}
	wantURL := "/api_9djWe8Tb2NE3p6opyqnh/v1/browse.php?apikey=testkey&limit=50&ponly=false" +
		"&release_type=P2P&release_type=Scene&release_type=O-Scene&imdbId=1234567&cats=37"
	paths := site.paths()
	if len(paths) != 1 || paths[0] != "GET "+wantURL {
		t.Fatalf("request: %v want GET %s", paths, wantURL)
	}
}

func TestTorrentSyndikatCheckResponseStatus(t *testing.T) {
	for _, tc := range []struct {
		status int
		want   string
	}{
		{http.StatusBadRequest, "Unknown or missing parameters"},
		{http.StatusUnauthorized, "Wrong API-Key"},
		{http.StatusForbidden, "API-Key has no authorization for the endpoint / scope browse"},
		{http.StatusInternalServerError, "Unexpected response status code 500"},
	} {
		site := newTorrentSyndikatSite(t, map[string]func(w http.ResponseWriter, r *http.Request){
			"/api_9djWe8Tb2NE3p6opyqnh/v1/browse.php": func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tc.status)
			},
		})
		ts := newTestTorrentSyndikat(t, site)
		ts.cfg.Key.Value = "badkey"
		_, err := ts.ResultsForQuery(context.Background(), &models.TorznabQuery{QueryType: "search", IsTest: true}, false)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("status %d: err %v want message %q", tc.status, err, tc.want)
		}
	}
}

func TestTorrentSyndikatApplyConfiguration(t *testing.T) {
	site := newTorrentSyndikatSite(t, map[string]func(w http.ResponseWriter, r *http.Request){
		"/api_9djWe8Tb2NE3p6opyqnh/v1/browse.php": func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(torrentSyndikatBrowseJSON))
		},
	})
	ts := newTestTorrentSyndikat(t, site)

	status, err := ts.ApplyConfiguration(context.Background(), []indexer.ConfigItem{
		{ID: "sitelink", Value: site.srv.URL + "/"},
		{ID: "apikey", Value: "testkey"},
	})
	if err != nil {
		t.Fatalf("ApplyConfiguration: %v", err)
	}
	if status != indexer.ConfigCompleted {
		t.Fatalf("status: %v", status)
	}
	if !ts.IsConfigured() {
		t.Fatal("must be configured after successful apply")
	}

	// failure: empty result set
	site2 := newTorrentSyndikatSite(t, map[string]func(w http.ResponseWriter, r *http.Request){
		"/api_9djWe8Tb2NE3p6opyqnh/v1/browse.php": func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{"rows":[]}`))
		},
	})
	ts2 := newTestTorrentSyndikat(t, site2)
	_, err = ts2.ApplyConfiguration(context.Background(), []indexer.ConfigItem{
		{ID: "sitelink", Value: site2.srv.URL + "/"},
		{ID: "apikey", Value: "testkey"},
	})
	if err == nil || err.Error() != "Could not find any releases" {
		t.Fatalf("err: %v", err)
	}
	if ts2.IsConfigured() {
		t.Fatal("must not be configured after failure")
	}
}

func TestTorrentSyndikatDownload(t *testing.T) {
	site := newTorrentSyndikatSite(t, map[string]func(w http.ResponseWriter, r *http.Request){
		"/download.php": func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte("torrent-bytes"))
		},
	})
	ts := newTestTorrentSyndikat(t, site)
	ts.cfg.Key.Value = "testkey"

	link, _ := url.Parse(site.srv.URL + "/download.php?id=123")
	body, err := ts.Download(context.Background(), link)
	if err != nil {
		t.Fatalf("Download: %v", err)
	}
	if string(body) != "torrent-bytes" {
		t.Fatalf("body: %q", body)
	}
	paths := site.paths()
	if len(paths) != 1 || paths[0] != "GET /download.php?id=123&apikey=testkey" {
		t.Fatalf("request: %v", paths)
	}
}

func hasInt(list []int, v int) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}
