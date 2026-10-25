package shared

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"vestt/common/indexer"
	"vestt/common/indexer/configdata"
	"vestt/common/models"
)

func couchPotatoTestCaps() *models.Capabilities {
	caps := models.NewCapabilities()
	caps.MovieSearchParams = []string{models.MovieParamQ, models.MovieParamImdbID}
	caps.TVSearchParams = []string{models.TVParamQ, models.TVParamSeason, models.TVParamEp}
	caps.Categories.AddCategoryMapping("movies", models.CatMovies, "")
	caps.Categories.AddCategoryMapping("tv", models.CatTV, "")
	return caps
}

func newCouchPotato(site, endpoint string) *CouchPotato {
	c := &CouchPotato{Endpoint: endpoint}
	c.IDValue = "cptest"
	c.NameValue = "CouchPotato Test"
	c.SiteLinkValue = site
	c.DefaultSiteLink = site
	c.LangValue = "en-US"
	c.TypeValue = "private"
	c.Caps = couchPotatoTestCaps()
	c.Bind(NewCouchPotatoConfig("find the passkey on your profile"))
	return c
}

const couchPotatoFixture = `{
  "total_results": 2,
  "results": [
    {
      "release_name": "Some Release 1080p",
      "details_url": "https://cp.faketest/details/1",
      "download_url": "https://cp.faketest/dl/1.torrent",
      "imdb_id": "tt0111161",
      "freeleech": true,
      "type": "movies",
      "size": 1500,
      "seeders": 10,
      "leechers": 5,
      "publish_date": "2024-01-02 03:04:05"
    },
    {
      "release_name": "Other Release",
      "details_url": "https://cp.faketest/details/2",
      "download_url": "https://cp.faketest/dl/2.torrent",
      "imdb_id": "",
      "freeleech": false,
      "type": "tv",
      "size": 500,
      "seeders": null,
      "leechers": null,
      "publish_date": "2023-06-07"
    }
  ]
}`

// couchPotatoSite is a fake CouchPotato-protocol API site.
type couchPotatoSite struct {
	*httptest.Server

	mu         sync.Mutex
	lastQuery  string
	lastCookie string
	mode       string // "", "error", "invalidjson", "empty"
}

func newCouchPotatoSite(t *testing.T) *couchPotatoSite {
	t.Helper()
	s := &couchPotatoSite{}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()
		if r.URL.Path != "/couchpotato.php" {
			http.NotFound(w, r)
			return
		}
		s.lastQuery = r.URL.RawQuery
		s.lastCookie = r.Header.Get("Cookie")
		w.Header().Set("Content-Type", "application/json")
		switch s.mode {
		case "error":
			_, _ = fmt.Fprint(w, `{"error":"Some upstream error"}`)
		case "invalidjson":
			_, _ = fmt.Fprint(w, `<html>not json at all</html>`)
		case "empty":
			_, _ = fmt.Fprint(w, `{"total_results":0}`)
		default:
			_, _ = fmt.Fprint(w, couchPotatoFixture)
		}
	}))
	t.Cleanup(s.Close)
	return s
}

func TestCouchPotatoConfigJSON(t *testing.T) {
	cfg := NewCouchPotatoConfig("find the passkey on your profile")
	items, err := configdata.ItemsJSON(cfg)
	if err != nil {
		t.Fatalf("ItemsJSON: %v", err)
	}
	got := make([]string, 0, len(items))
	for _, m := range items {
		got = append(got, fmt.Sprintf("%s:%s", m["id"], m["type"]))
	}
	want := []string{"sitelink:inputstring", "username:inputstring", "passkey:inputstring", ":displayinfo", "tags:inputtags"}
	if fmt.Sprintf("%v", got) != fmt.Sprintf("%v", want) {
		t.Fatalf("items = %v, want %v", got, want)
	}
	if items[3]["value"] != "find the passkey on your profile" {
		t.Fatalf("instructions = %q", items[3]["value"])
	}
}

func TestCouchPotatoSearchURL(t *testing.T) {
	c := newCouchPotato("https://cp.example/", "couchpotato.php")
	c.Config.Username.Value = "user"
	c.Config.Passkey.Value = "pk"

	q := models.NewTorznabQuery()
	q.SearchTerm = "My Movie"
	got := c.SearchURL(q)
	want := "https://cp.example/couchpotato.php?search=My+Movie&passkey=pk&user=user"
	if got != want {
		t.Fatalf("url = %q want %q", got, want)
	}

	q2 := models.NewTorznabQuery()
	q2.ImdbID = "tt0111161"
	got2 := c.SearchURL(q2)
	want2 := "https://cp.example/couchpotato.php?imdbid=tt0111161&search=&passkey=pk&user=user"
	if got2 != want2 {
		t.Fatalf("url = %q want %q", got2, want2)
	}

	// GetSearchString hook
	c.GetSearchString = func(q *models.TorznabQuery) string { return "hooked string" }
	if got := c.SearchURL(models.NewTorznabQuery()); !strings.Contains(got, "search=hooked+string") {
		t.Fatalf("url = %q", got)
	}
}

func TestCouchPotatoApplyConfiguration(t *testing.T) {
	srv := newCouchPotatoSite(t)
	c := newCouchPotato(srv.URL+"/", "couchpotato.php")
	c.Client = testClient(t)

	status, err := c.ApplyConfiguration(context.Background(), []indexer.ConfigItem{
		{ID: "username", Value: "user"},
		{ID: "passkey", Value: "pk"},
	})
	if err != nil {
		t.Fatalf("ApplyConfiguration: %v", err)
	}
	if status != indexer.ConfigRequiresTesting {
		t.Fatalf("status = %v, want RequiresTesting", status)
	}
	if !c.IsConfigured() {
		t.Fatal("expected configured")
	}
}

func TestCouchPotatoPerformQuery(t *testing.T) {
	srv := newCouchPotatoSite(t)
	c := newCouchPotato(srv.URL+"/", "couchpotato.php")
	c.Config.Username.Value = "user"
	c.Config.Passkey.Value = "pk"
	c.Client = testClient(t)

	res, err := c.ResultsForQuery(context.Background(), &models.TorznabQuery{
		QueryType: "search", SearchTerm: "movie", Cache: true,
	}, false)
	if err != nil {
		t.Fatalf("ResultsForQuery: %v", err)
	}
	// user/passkey always appended, cookie header forwarded
	if !strings.Contains(srv.lastQuery, "passkey=pk&user=user") {
		t.Fatalf("query = %s", srv.lastQuery)
	}

	releases := res.Releases
	if len(releases) != 2 {
		t.Fatalf("releases = %d", len(releases))
	}

	r0 := releases[0]
	if r0.Title != "Some Release 1080p" {
		t.Fatalf("title = %q", r0.Title)
	}
	if r0.Imdb == nil || *r0.Imdb != 111161 {
		t.Fatalf("imdb = %v", r0.Imdb)
	}
	if r0.DownloadVolumeFactor == nil || *r0.DownloadVolumeFactor != 0 || r0.UploadVolumeFactor == nil || *r0.UploadVolumeFactor != 1 {
		t.Fatalf("dvf/uvf = %v/%v", r0.DownloadVolumeFactor, r0.UploadVolumeFactor)
	}
	if fmt.Sprintf("%v", r0.Category) != "[2000]" {
		t.Fatalf("category = %v", r0.Category)
	}
	if r0.Size == nil || *r0.Size != 1500*1024*1024 {
		t.Fatalf("size = %v", r0.Size)
	}
	if r0.Seeders == nil || *r0.Seeders != 10 || r0.Peers == nil || *r0.Peers != 15 {
		t.Fatalf("seeders/peers = %v/%v", r0.Seeders, r0.Peers)
	}
	if r0.PublishDate.Format("2006-01-02 15:04:05") != "2024-01-02 03:04:05" {
		t.Fatalf("date = %v", r0.PublishDate)
	}
	if r0.Details == nil || r0.Details.String() != "https://cp.faketest/details/1" {
		t.Fatalf("details = %v", r0.Details)
	}
	if r0.Link == nil || r0.Link.String() != "https://cp.faketest/dl/1.torrent" {
		t.Fatalf("link = %v", r0.Link)
	}
	if r0.Guid != r0.Link {
		t.Fatal("guid must be the link")
	}

	// non-freeleech + null numerics
	r1 := releases[1]
	if r1.Imdb != nil {
		t.Fatalf("imdb = %v", r1.Imdb)
	}
	if r1.DownloadVolumeFactor == nil || *r1.DownloadVolumeFactor != 1 {
		t.Fatalf("dvf = %v", r1.DownloadVolumeFactor)
	}
	if fmt.Sprintf("%v", r1.Category) != "[5000]" {
		t.Fatalf("category = %v", r1.Category)
	}
	if r1.Seeders != nil || r1.Peers != nil {
		t.Fatalf("seeders/peers = %v/%v", r1.Seeders, r1.Peers)
	}
	if r1.Size == nil || *r1.Size != 500*1024*1024 {
		t.Fatalf("size = %v", r1.Size)
	}
}

func TestCouchPotatoErrorPaths(t *testing.T) {
	srv := newCouchPotatoSite(t)
	c := newCouchPotato(srv.URL+"/", "couchpotato.php")
	c.Config.Username.Value = "user"
	c.Config.Passkey.Value = "pk"
	c.Client = testClient(t)
	q := &models.TorznabQuery{QueryType: "search", SearchTerm: "x", Cache: true}

	// zero results
	srv.mode = "empty"
	res, err := c.PerformQuery(context.Background(), q)
	if err != nil || len(res) != 0 {
		t.Fatalf("empty: res=%v err=%v", res, err)
	}

	// upstream error field
	srv.mode = "error"
	_, err = c.PerformQuery(context.Background(), q)
	if err == nil || err.Error() != "Some upstream error" {
		t.Fatalf("err = %v", err)
	}

	// unparseable JSON
	srv.mode = "invalidjson"
	_, err = c.PerformQuery(context.Background(), q)
	if err == nil || !strings.HasPrefix(err.Error(), "Error while parsing json: ") {
		t.Fatalf("err = %v", err)
	}
}
