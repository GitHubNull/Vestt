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
	"vestt/common/utils/dateutil"
)

// redactedAPIKey is a valid-length (41 chars) API key for the fake site.
const redactedAPIKey = "0123456789abcdef0123456789abcdef01234abcd"

type redactedSite struct {
	srv *httptest.Server

	mu        sync.Mutex
	hits      []string
	lastAuth  string
	authOK    bool
	failJSON  bool
	status401 bool
}

func newRedactedSite(t *testing.T) *redactedSite {
	t.Helper()
	s := &redactedSite{authOK: true}
	s.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		s.hits = append(s.hits, r.URL.RequestURI())
		auth := r.Header.Get("Authorization")
		s.lastAuth = auth
		authOK := s.authOK && auth == redactedAPIKey
		failJSON := s.failJSON
		status401 := s.status401
		s.mu.Unlock()

		if failJSON {
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprint(w, `{"status":"failure","error":"bad key"}`)
			return
		}
		if status401 {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if !authOK {
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprint(w, `{"status":"failure","error":"invalid api key"}`)
			return
		}
		switch r.URL.Path {
		case "/ajax.php":
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprint(w, redactedFixture)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(s.srv.Close)
	return s
}

func (s *redactedSite) paths() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.hits...)
}

func (s *redactedSite) setFailureMode(failJSON, status401 bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failJSON, s.status401 = failJSON, status401
}

func newTestRedacted(t *testing.T, site *redactedSite) *Redacted {
	t.Helper()
	client, err := httpclient.New(httpclient.Config{}, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("httpclient.New: %v", err)
	}
	ix, err := NewRedacted(services.Deps{Client: client})
	if err != nil {
		t.Fatalf("NewRedacted: %v", err)
	}
	r := ix.(*Redacted)
	r.SiteLinkValue = site.srv.URL + "/"
	client.SetRequestDelay(r.ID(), 0)
	return r
}

const redactedFixture = `{
  "status": "success",
  "response": {
    "results": [
      {
        "groupTime": "1700000000",
        "groupName": "Album Name",
        "artist": "Some Artist",
        "cover": "covers/1.jpg",
        "tags": ["rock", "indie"],
        "groupYear": "2020",
        "releaseType": "Album",
        "torrents": [
          {
            "torrentId": 11,
            "time": "2024-01-01 12:00:00",
            "format": "FLAC",
            "encoding": "Lossless",
            "hasLog": true,
            "logScore": "100",
            "hasCue": false,
            "media": "CD",
            "size": 123456789,
            "seeders": 5,
            "leechers": 2,
            "category": "Music",
            "fileCount": 1,
            "snatches": 10,
            "isFreeleech": true,
            "isPersonalFreeleech": false,
            "isFreeload": "False",
            "isNeutralLeech": false
          },
          {
            "torrentId": 12,
            "time": "",
            "format": "MP3",
            "encoding": "",
            "hasLog": false,
            "logScore": "0",
            "hasCue": false,
            "media": "WEB",
            "size": 987654321,
            "seeders": 1,
            "leechers": 0,
            "category": "Comedy",
            "fileCount": 2,
            "snatches": 3,
            "isFreeleech": false,
            "isPersonalFreeleech": false,
            "isFreeload": "True",
            "isNeutralLeech": false
          }
        ]
      }
    ]
  }
}`

func TestRedactedCaps(t *testing.T) {
	ix, err := NewRedacted(services.Deps{})
	if err != nil {
		t.Fatalf("NewRedacted: %v", err)
	}
	caps := ix.Capabilities()
	wantMovie := []string{"q", "genre"}
	if strings.Join(caps.MovieSearchParams, ",") != strings.Join(wantMovie, ",") {
		t.Fatalf("MovieSearchParams: %v", caps.MovieSearchParams)
	}
	wantMusic := []string{"q", "album", "artist", "label", "year", "genre"}
	if strings.Join(caps.MusicSearchParams, ",") != strings.Join(wantMusic, ",") {
		t.Fatalf("MusicSearchParams: %v", caps.MusicSearchParams)
	}
	wantBook := []string{"q", "genre"}
	if strings.Join(caps.BookSearchParams, ",") != strings.Join(wantBook, ",") {
		t.Fatalf("BookSearchParams: %v", caps.BookSearchParams)
	}
	for _, tc := range []struct {
		tracker string
		want    int
	}{
		{"1", 3000}, {"2", 4000}, {"3", 7000}, {"4", 3030},
		{"5", 2000}, {"6", 3000}, {"7", 7000},
	} {
		got := caps.Categories.MapTrackerCatToNewznab(tc.tracker)
		if len(got) == 0 || got[0] != tc.want {
			t.Fatalf("MapTrackerCatToNewznab(%q) = %v, want %d", tc.tracker, got, tc.want)
		}
	}
	if got := caps.Categories.MapTrackerCatDescToNewznab("E-Books"); len(got) == 0 || got[0] != 7000 {
		t.Fatalf("MapTrackerCatDescToNewznab(E-Books) = %v", got)
	}
}

func TestRedactedConfigForm(t *testing.T) {
	ix, err := NewRedacted(services.Deps{})
	if err != nil {
		t.Fatalf("NewRedacted: %v", err)
	}
	items, err := ix.ConfigurationForSetup(context.Background())
	if err != nil {
		t.Fatalf("ConfigurationForSetup: %v", err)
	}
	var ids []string
	for _, it := range items {
		ids = append(ids, it["id"].(string))
	}
	want := []string{
		"sitelink", "apikey", "usefreeleechtokenswhenavailable", "searchfreeloadonly",
		"", "accountinactivity", "tags",
	}
	if fmt.Sprintf("%v", ids) != fmt.Sprintf("%v", want) {
		t.Fatalf("ids = %v, want %v", ids, want)
	}
}

func TestRedactedSearchRequest(t *testing.T) {
	site := newRedactedSite(t)
	r := newTestRedacted(t, site)
	if err := r.ApplyItems([]indexer.ConfigItem{
		{ID: "sitelink", Value: site.srv.URL + "/"},
		{ID: "apikey", Value: redactedAPIKey},
	}); err != nil {
		t.Fatalf("ApplyItems: %v", err)
	}

	q := models.NewTorznabQuery()
	q.QueryType = "search"
	q.SearchTerm = "Some Artist"
	if _, err := r.ResultsForQuery(context.Background(), q, false); err != nil {
		t.Fatalf("ResultsForQuery: %v", err)
	}

	// category-restricted music search
	q2 := models.NewTorznabQuery()
	q2.QueryType = "music"
	q2.Artist = "Some Artist"
	q2.Categories = []int{models.CatAudio.ID}
	if _, err := r.ResultsForQuery(context.Background(), q2, false); err != nil {
		t.Fatalf("ResultsForQuery: %v", err)
	}

	paths := site.paths()
	want := []string{
		"/ajax.php?action=browse&order_by=time&order_way=desc&searchstr=Some+Artist",
		// upstream only UrlEncodes values, not keys: filter_cat[1] stays raw
		// (Jackett.Common/Utils/StringUtil.cs GetQueryString). Query cat 3000
		// expands to its children, mapping tracker cats 1, 4 and 6.
		"/ajax.php?action=browse&order_by=time&order_way=desc&artistname=Some+Artist&filter_cat[1]=1&filter_cat[4]=1&filter_cat[6]=1",
	}
	if fmt.Sprintf("%v", paths) != fmt.Sprintf("%v", want) {
		t.Fatalf("uris = %v, want %v", paths, want)
	}
	site.mu.Lock()
	auth := site.lastAuth
	site.mu.Unlock()
	if auth != redactedAPIKey {
		t.Fatalf("Authorization: %q", auth)
	}
}

func TestRedactedParseFixture(t *testing.T) {
	site := newRedactedSite(t)
	r := newTestRedacted(t, site)
	if err := r.ApplyItems([]indexer.ConfigItem{
		{ID: "sitelink", Value: site.srv.URL + "/"},
		{ID: "apikey", Value: redactedAPIKey},
		{ID: "usefreeleechtokenswhenavailable", Value: "true"},
	}); err != nil {
		t.Fatalf("ApplyItems: %v", err)
	}

	releases, err := r.performQuery(context.Background(), &models.TorznabQuery{QueryType: "search"})
	if err != nil {
		t.Fatalf("performQuery: %v", err)
	}
	if len(releases) != 2 {
		t.Fatalf("releases: %d", len(releases))
	}

	r0 := releases[0]
	if r0.Title != "Some Artist - Album Name [2020] [Album] FLAC / Lossless / Log (100%) / CD" {
		t.Fatalf("title: %q", r0.Title)
	}
	if r0.Size == nil || *r0.Size != 123456789 {
		t.Fatalf("size: %v", r0.Size)
	}
	if r0.Seeders == nil || *r0.Seeders != 5 || r0.Peers == nil || *r0.Peers != 7 {
		t.Fatalf("seeders/peers: %v/%v", r0.Seeders, r0.Peers)
	}
	if r0.Grabs == nil || *r0.Grabs != 10 || r0.Files == nil || *r0.Files != 1 {
		t.Fatalf("grabs/files: %v/%v", r0.Grabs, r0.Files)
	}
	if r0.PublishDate.Format("2006-01-02 15:04:05") != "2024-01-01 12:00:00" {
		t.Fatalf("date: %v", r0.PublishDate)
	}
	if r0.DownloadVolumeFactor == nil || *r0.DownloadVolumeFactor != 0 {
		t.Fatalf("dvf: %v", r0.DownloadVolumeFactor)
	}
	if r0.UploadVolumeFactor == nil || *r0.UploadVolumeFactor != 1 {
		t.Fatalf("uvf: %v", r0.UploadVolumeFactor)
	}
	// Redacted stamps the proportional-ratio MinimumRatio on every release.
	if r0.MinimumRatio == nil || *r0.MinimumRatio != 0.6 {
		t.Fatalf("minimum ratio: %v", r0.MinimumRatio)
	}
	wantLink := site.srv.URL + "/ajax.php?action=download&usetoken=1&id=11"
	if r0.Link == nil || r0.Link.String() != wantLink {
		t.Fatalf("link: %v", r0.Link)
	}
	if r0.Details == nil || !strings.Contains(r0.Details.String(), "torrents.php?torrentid=11") {
		t.Fatalf("details: %v", r0.Details)
	}
	if r0.Poster == nil || r0.Poster.String() != site.srv.URL+"/covers/1.jpg" {
		t.Fatalf("poster: %v", r0.Poster)
	}
	if r0.Description != "Tags: rock, indie\n" {
		t.Fatalf("description: %q", r0.Description)
	}
	if fmt.Sprintf("%v", r0.Genres) != "[rock indie]" {
		t.Fatalf("genres: %v", r0.Genres)
	}
	if len(r0.Category) == 0 || r0.Category[0] != models.CatAudio.ID {
		t.Fatalf("category: %v", r0.Category)
	}

	// second torrent: empty time keeps the groupTime; freeload zeroes both
	// volume factors.
	r1 := releases[1]
	if !r1.PublishDate.Equal(dateutil.UnixTimestampToDateTime(1700000000)) {
		t.Fatalf("date[1]: %v", r1.PublishDate)
	}
	if r1.DownloadVolumeFactor == nil || *r1.DownloadVolumeFactor != 0 {
		t.Fatalf("dvf[1]: %v", r1.DownloadVolumeFactor)
	}
	if r1.UploadVolumeFactor == nil || *r1.UploadVolumeFactor != 0 {
		t.Fatalf("uvf[1]: %v", r1.UploadVolumeFactor)
	}
	wantLink1 := site.srv.URL + "/ajax.php?action=download&usetoken=1&id=12"
	if r1.Link == nil || r1.Link.String() != wantLink1 {
		t.Fatalf("link[1]: %v", r1.Link)
	}
}

func TestRedactedNoTokenInDownloadURL(t *testing.T) {
	site := newRedactedSite(t)
	r := newTestRedacted(t, site)
	if err := r.ApplyItems([]indexer.ConfigItem{
		{ID: "sitelink", Value: site.srv.URL + "/"},
		{ID: "apikey", Value: redactedAPIKey},
	}); err != nil {
		t.Fatalf("ApplyItems: %v", err)
	}

	releases, err := r.performQuery(context.Background(), &models.TorznabQuery{QueryType: "search"})
	if err != nil {
		t.Fatalf("performQuery: %v", err)
	}
	wantLink := site.srv.URL + "/ajax.php?action=download&id=11"
	if releases[0].Link.String() != wantLink {
		t.Fatalf("link without usetoken: %v", releases[0].Link)
	}
}

func TestRedactedFreeloadOnlyFilter(t *testing.T) {
	site := newRedactedSite(t)
	r := newTestRedacted(t, site)
	if err := r.ApplyItems([]indexer.ConfigItem{
		{ID: "sitelink", Value: site.srv.URL + "/"},
		{ID: "apikey", Value: redactedAPIKey},
		{ID: "searchfreeloadonly", Value: "true"},
	}); err != nil {
		t.Fatalf("ApplyItems: %v", err)
	}

	releases, err := r.performQuery(context.Background(), &models.TorznabQuery{QueryType: "search"})
	if err != nil {
		t.Fatalf("performQuery: %v", err)
	}
	if len(releases) != 1 {
		t.Fatalf("releases: %d (want only the freeload torrent)", len(releases))
	}
	if !strings.Contains(releases[0].Link.String(), "id=12") {
		t.Fatalf("kept release: %v", releases[0].Link)
	}
}

func TestRedactedBadKeyLength(t *testing.T) {
	site := newRedactedSite(t)
	r := newTestRedacted(t, site)

	_, err := r.ApplyConfiguration(context.Background(), []indexer.ConfigItem{
		{ID: "sitelink", Value: site.srv.URL + "/"},
		{ID: "apikey", Value: "abc"},
	})
	var ie *indexer.IndexerError
	if !errors.As(err, &ie) || ie.Kind != indexer.KindConfig {
		t.Fatalf("err: %v", err)
	}
	if !strings.HasPrefix(ie.Message, "Invalid API Key configured: expected length: 41, got 3") {
		t.Fatalf("message: %q", ie.Message)
	}
}

func TestRedactedRejectedKeyIsAuth(t *testing.T) {
	site := newRedactedSite(t)
	site.setFailureMode(true, false)
	r := newTestRedacted(t, site)
	if err := r.ApplyItems([]indexer.ConfigItem{
		{ID: "sitelink", Value: site.srv.URL + "/"},
		{ID: "apikey", Value: redactedAPIKey},
	}); err != nil {
		t.Fatalf("ApplyItems: %v", err)
	}

	_, err := r.ResultsForQuery(context.Background(), &models.TorznabQuery{QueryType: "search"}, false)
	var ie *indexer.IndexerError
	if !errors.As(err, &ie) || ie.Kind != indexer.KindAuth {
		t.Fatalf("err: %v", err)
	}
	if !strings.Contains(ie.Message, `Unexpected response from indexer request: "bad key"`) {
		t.Fatalf("message: %q", ie.Message)
	}
}

func TestRedacted401IsAuth(t *testing.T) {
	site := newRedactedSite(t)
	site.setFailureMode(false, true)
	r := newTestRedacted(t, site)
	if err := r.ApplyItems([]indexer.ConfigItem{
		{ID: "sitelink", Value: site.srv.URL + "/"},
		{ID: "apikey", Value: redactedAPIKey},
	}); err != nil {
		t.Fatalf("ApplyItems: %v", err)
	}

	_, err := r.ResultsForQuery(context.Background(), &models.TorznabQuery{QueryType: "search"}, false)
	var ie *indexer.IndexerError
	if !errors.As(err, &ie) || ie.Kind != indexer.KindAuth {
		t.Fatalf("err: %v", err)
	}
}
