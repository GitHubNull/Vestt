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

type mteamtpSite struct {
	srv    *httptest.Server
	mu     sync.Mutex
	posts  []string
	bodies []string
	apiKey []string
	serve  map[string]string
}

func newMTeamtpSite(t *testing.T, serve map[string]string) *mteamtpSite {
	t.Helper()
	s := &mteamtpSite{serve: serve}
	s.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		s.posts = append(s.posts, r.Method+" "+r.URL.RequestURI())
		s.apiKey = append(s.apiKey, r.Header.Get("x-api-key"))
		if r.Method == http.MethodPost {
			buf := make([]byte, r.ContentLength)
			_, _ = io.ReadFull(r.Body, buf)
			s.bodies = append(s.bodies, string(buf))
		}
		page, ok := s.serve[r.URL.Path]
		s.mu.Unlock()
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_, _ = w.Write([]byte(page))
	}))
	t.Cleanup(s.srv.Close)
	return s
}

func (s *mteamtpSite) postsSnapshot() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.posts...)
}

func (s *mteamtpSite) bodiesSnapshot() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.bodies...)
}

func newTestMTeamtp(t *testing.T, site *mteamtpSite) *MTeamTp {
	t.Helper()
	client, err := httpclient.New(httpclient.Config{}, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("httpclient.New: %v", err)
	}
	ix, err := NewMTeamTp(services.Deps{Client: client})
	if err != nil {
		t.Fatalf("NewMTeamTp: %v", err)
	}
	m := ix.(*MTeamTp)
	m.SiteLinkValue = site.srv.URL + "/"
	m.apiBase = site.srv.URL + "/"
	// tests must not pay the upstream 5s request delay
	client.SetRequestDelay(m.ID(), 0)
	return m
}

const mteamtpSearchJSON = `{
  "data": {
    "data": [
      {
        "id": "123456",
        "name": "Some  Movie   2020  1080p",
        "smallDescr": "1080p WEB-DL",
        "category": "419",
        "numfiles": "3",
        "imdb": "tt1234567",
        "size": "1610612736",
        "status": {
          "createdDate": "2024-03-15 21:08:37",
          "discount": "_2X_FREE",
          "timesCompleted": "42",
          "seeders": "12",
          "leechers": "3"
        }
      },
      {
        "id": "123457",
        "name": "Other Show S01 720p",
        "smallDescr": "",
        "category": "402",
        "numfiles": "1",
        "imdb": "",
        "size": "524288000",
        "status": {
          "createdDate": "2024-03-16 08:00:00",
          "discount": "PERCENT_50",
          "timesCompleted": "7",
          "seeders": "5",
          "leechers": "0"
        }
      }
    ]
  },
  "message": "SUCCESS"
}`

func TestMTeamtpCaps(t *testing.T) {
	ix, err := NewMTeamTp(services.Deps{})
	if err != nil {
		t.Fatalf("NewMTeamTp: %v", err)
	}
	caps := ix.Capabilities()
	for _, tc := range []struct {
		params []string
		want   []string
	}{
		{caps.TVSearchParams, []string{models.TVParamQ, models.TVParamSeason, models.TVParamEp, models.TVParamImdbID}},
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
		{"401", 2030}, {"419", 2040}, {"420", 2070}, {"421", 2050},
		{"403", 5030}, {"402", 5040}, {"404", 5080}, {"434", 3000},
		{"406", 3020}, {"423", 4050}, {"448", 1090}, {"405", 5070},
		{"407", 5060}, {"427", 7020}, {"442", 3030}, {"409", 8000},
		{"410", 6000}, {"424", 6080}, {"433", 6060}, {"413", 6000},
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

func TestMTeamtpConfigForm(t *testing.T) {
	ix, err := NewMTeamTp(services.Deps{})
	if err != nil {
		t.Fatalf("NewMTeamTp: %v", err)
	}
	items, err := ix.ConfigurationForSetup(context.Background())
	if err != nil {
		t.Fatalf("ConfigurationForSetup: %v", err)
	}
	var ids []string
	for _, it := range items {
		ids = append(ids, it["id"].(string))
	}
	want := []string{"sitelink", "apikey", "apikeyinfo", "searchfreeleechonly", "tags"}
	if strings.Join(ids, "|") != strings.Join(want, "|") {
		t.Fatalf("config ids: %v", ids)
	}
}

func TestMTeamtpApplyConfigurationOK(t *testing.T) {
	site := newMTeamtpSite(t, map[string]string{
		"/api/torrent/search": mteamtpSearchJSON,
	})
	m := newTestMTeamtp(t, site)

	status, err := m.ApplyConfiguration(context.Background(), []indexer.ConfigItem{
		{ID: "sitelink", Value: site.srv.URL + "/"},
		{ID: "apikey", Value: "secret-api-key"},
	})
	if err != nil {
		t.Fatalf("ApplyConfiguration: %v", err)
	}
	if status != indexer.ConfigCompleted {
		t.Fatalf("status: %v, want ConfigCompleted", status)
	}
	if !m.IsConfigured() {
		t.Fatal("must be configured after a successful test query")
	}
	if got := site.apiKey; len(got) == 0 || got[0] != "secret-api-key" {
		t.Fatalf("x-api-key headers: %v", got)
	}
}

func TestMTeamtpApplyConfigurationMissingKey(t *testing.T) {
	site := newMTeamtpSite(t, map[string]string{
		"/api/torrent/search": mteamtpSearchJSON,
	})
	m := newTestMTeamtp(t, site)

	_, err := m.ApplyConfiguration(context.Background(), []indexer.ConfigItem{
		{ID: "sitelink", Value: site.srv.URL + "/"},
	})
	if err == nil || err.Error() != "Missing API Key." {
		t.Fatalf("err: %v", err)
	}
	if m.IsConfigured() {
		t.Fatal("must not be configured without an API key")
	}
	if len(site.postsSnapshot()) != 0 {
		t.Fatalf("no request must be issued without an API key: %v", site.postsSnapshot())
	}
}

func TestMTeamtpApplyConfigurationNoReleases(t *testing.T) {
	site := newMTeamtpSite(t, map[string]string{
		"/api/torrent/search": `{"data": null, "message": "SUCCESS"}`,
	})
	m := newTestMTeamtp(t, site)

	_, err := m.ApplyConfiguration(context.Background(), []indexer.ConfigItem{
		{ID: "sitelink", Value: site.srv.URL + "/"},
		{ID: "apikey", Value: "secret-api-key"},
	})
	if err == nil || err.Error() != "Could not find releases." {
		t.Fatalf("err: %v", err)
	}
}

func TestMTeamtpSearchRequestAndParse(t *testing.T) {
	site := newMTeamtpSite(t, map[string]string{
		"/api/torrent/search": mteamtpSearchJSON,
	})
	m := newTestMTeamtp(t, site)
	m.cfg.FreeleechOnly.Value = true

	q := models.NewTorznabQuery()
	q.QueryType = "search"
	q.SearchTerm = "Some  Movie"

	res, err := m.ResultsForQuery(context.Background(), q, false)
	if err != nil {
		t.Fatalf("ResultsForQuery: %v", err)
	}
	if len(res.Releases) != 2 {
		t.Fatalf("releases: %d", len(res.Releases))
	}

	posts := site.postsSnapshot()
	if len(posts) != 1 || posts[0] != "POST /api/torrent/search" {
		t.Fatalf("posts: %v", posts)
	}

	// upstream STJson wire format (indented, camelCase, nulls omitted);
	// the batch-2 requirement: the upstream pagination semantics
	// pageNumber=1 / pageSize=100 must be serialized on every search.
	wantBody := "{\n" +
		`  "mode": "normal",` + "\n" +
		`  "categories": [],` + "\n" +
		`  "discount": "FREE",` + "\n" +
		`  "keyword": "Some  Movie",` + "\n" +
		`  "pageNumber": 1,` + "\n" +
		`  "pageSize": 100` + "\n" +
		"}"
	bodies := site.bodiesSnapshot()
	if len(bodies) != 1 || bodies[0] != wantBody {
		t.Fatalf("body:\n%q\nwant:\n%q", bodies, wantBody)
	}

	// upstream sorts by descending publish date: 03-16 first
	r := res.Releases[0]
	if r.Title != "Other Show S01 720p" {
		t.Fatalf("title: %q", r.Title)
	}
	r2 := res.Releases[1]
	if r2.Title != "Some Movie 2020 1080p" {
		t.Fatalf("title 2: %q (CleanTitle must collapse whitespace)", r2.Title)
	}

	if r2.Guid == nil || !strings.HasSuffix(r2.Guid.Path, "/detail/123456") {
		t.Fatalf("guid: %v", r2.Guid)
	}
	if r2.Details == nil || r2.Details.String() != r2.Guid.String() {
		t.Fatalf("details: %v", r2.Details)
	}
	if r2.Link == nil || !strings.HasSuffix(r2.Link.Path, "api/torrent/genDlToken") ||
		r2.Link.Query().Get("id") != "123456" {
		t.Fatalf("link: %v", r2.Link)
	}
	if r2.Description != "1080p WEB-DL" {
		t.Fatalf("description: %q", r2.Description)
	}
	if r2.Files == nil || *r2.Files != 3 {
		t.Fatalf("files: %v", r2.Files)
	}
	if r2.Size == nil || *r2.Size != 1610612736 {
		t.Fatalf("size: %v", r2.Size)
	}
	if r2.Grabs == nil || *r2.Grabs != 42 {
		t.Fatalf("grabs: %v", r2.Grabs)
	}
	if r2.Seeders == nil || *r2.Seeders != 12 {
		t.Fatalf("seeders: %v", r2.Seeders)
	}
	if r2.Peers == nil || *r2.Peers != 15 {
		t.Fatalf("peers: %v", r2.Peers)
	}
	// 2024-03-15 21:08:37 +08:00 → 13:08:37 UTC
	if r2.PublishDate.UTC().Format("2006-01-02 15:04:05") != "2024-03-15 13:08:37" {
		t.Fatalf("publish date: %v", r2.PublishDate)
	}
	found := false
	for _, id := range r2.Category {
		if id == 2040 { // Movie HD
			found = true
		}
	}
	if !found {
		t.Fatalf("category: %v, want it to contain 2040", r2.Category)
	}
	// _2X_FREE → dvf 0 / uvf 2
	if r2.DownloadVolumeFactor == nil || *r2.DownloadVolumeFactor != 0 {
		t.Fatalf("dvf (_2X_FREE): %v", r2.DownloadVolumeFactor)
	}
	if r2.UploadVolumeFactor == nil || *r2.UploadVolumeFactor != 2 {
		t.Fatalf("uvf (_2X_FREE): %v", r2.UploadVolumeFactor)
	}
	if r2.MinimumRatio == nil || *r2.MinimumRatio != 1 {
		t.Fatalf("minimum ratio: %v", r2.MinimumRatio)
	}
	if r2.MinimumSeedTime == nil || *r2.MinimumSeedTime != 172800 {
		t.Fatalf("minimum seed time: %v", r2.MinimumSeedTime)
	}

	// PERCENT_50 → dvf 0.5 / uvf 1
	if r.DownloadVolumeFactor == nil || *r.DownloadVolumeFactor != 0.5 {
		t.Fatalf("dvf (PERCENT_50): %v", r.DownloadVolumeFactor)
	}
	if r.UploadVolumeFactor == nil || *r.UploadVolumeFactor != 1 {
		t.Fatalf("uvf (PERCENT_50): %v", r.UploadVolumeFactor)
	}
}

func TestMTeamtpAdultCategorySplit(t *testing.T) {
	site := newMTeamtpSite(t, map[string]string{
		"/api/torrent/search": mteamtpSearchJSON,
	})
	m := newTestMTeamtp(t, site)

	q := models.NewTorznabQuery()
	q.QueryType = "search"
	q.Categories = []int{2040, 6000} // Movies HD (normal) + XXX (adult)

	_, err := m.ResultsForQuery(context.Background(), q, false)
	if err != nil {
		t.Fatalf("ResultsForQuery: %v", err)
	}
	bodies := site.bodiesSnapshot()
	if len(bodies) != 2 {
		t.Fatalf("expected normal+adult requests: %v", bodies)
	}
	if !strings.Contains(bodies[0], `"mode": "normal"`) ||
		!strings.Contains(bodies[0], `"419"`) ||
		!strings.Contains(bodies[0], `"439"`) {
		t.Fatalf("normal body: %s", bodies[0])
	}
	if !strings.Contains(bodies[1], `"mode": "adult"`) ||
		!strings.Contains(bodies[1], `"410"`) {
		t.Fatalf("adult body: %s", bodies[1])
	}
}

func TestMTeamtpImdbRequest(t *testing.T) {
	site := newMTeamtpSite(t, map[string]string{
		"/api/torrent/search": mteamtpSearchJSON,
	})
	m := newTestMTeamtp(t, site)

	q := models.NewTorznabQuery()
	q.QueryType = "movie"
	q.ImdbID = "tt7654321"

	_, err := m.ResultsForQuery(context.Background(), q, false)
	if err != nil {
		t.Fatalf("ResultsForQuery: %v", err)
	}
	body := site.bodiesSnapshot()[0]
	if !strings.Contains(body, `"imdb": "tt7654321"`) {
		t.Fatalf("body: %s", body)
	}
	// no keyword for an id-only query, pagination still present
	if strings.Contains(body, `"keyword"`) ||
		!strings.Contains(body, `"pageNumber": 1`) ||
		!strings.Contains(body, `"pageSize": 100`) {
		t.Fatalf("body: %s", body)
	}
}

func TestMTeamtpAPIKeyHeader(t *testing.T) {
	site := newMTeamtpSite(t, map[string]string{
		"/api/torrent/search": mteamtpSearchJSON,
	})
	m := newTestMTeamtp(t, site)
	m.cfg.ApiKey.Value = "my-key"

	q := models.NewTorznabQuery()
	q.QueryType = "search"
	if _, err := m.ResultsForQuery(context.Background(), q, false); err != nil {
		t.Fatalf("ResultsForQuery: %v", err)
	}
	for _, k := range site.apiKey {
		if k != "my-key" {
			t.Fatalf("x-api-key: %q", k)
		}
	}
}

func TestMTeamtpUnknownStatus(t *testing.T) {
	// 4xx reaches the indexer untouched (5xx would be retried by the
	// client stack and surface as ErrSiteDown)
	site := newMTeamtpSite(t, map[string]string{})
	site.srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
	})
	m := newTestMTeamtp(t, site)

	q := models.NewTorznabQuery()
	q.QueryType = "search"
	_, err := m.ResultsForQuery(context.Background(), q, false)
	if err == nil || !strings.Contains(err.Error(), "Unknown status code: 400 (BadRequest)") {
		t.Fatalf("err: %v", err)
	}
}

func TestMTeamtpAPIMessageError(t *testing.T) {
	site := newMTeamtpSite(t, map[string]string{
		"/api/torrent/search": `{"data": null, "message": "INVALID API KEY"}`,
	})
	m := newTestMTeamtp(t, site)

	q := models.NewTorznabQuery()
	q.QueryType = "search"
	_, err := m.ResultsForQuery(context.Background(), q, false)
	if err == nil || !strings.Contains(err.Error(), "Invalid response received from M-Team. Response from API: INVALID API KEY") {
		t.Fatalf("err: %v", err)
	}
}

func TestMTeamtpInvalidJSON(t *testing.T) {
	site := newMTeamtpSite(t, map[string]string{
		"/api/torrent/search": `<html>not json</html>`,
	})
	m := newTestMTeamtp(t, site)

	q := models.NewTorznabQuery()
	q.QueryType = "search"
	_, err := m.ResultsForQuery(context.Background(), q, false)
	if err == nil || !strings.Contains(err.Error(), "Invalid response received from M-Team, not a valid JSON") {
		t.Fatalf("err: %v", err)
	}
}

func TestMTeamtpDownload(t *testing.T) {
	const torrentBytes = "d8:announce..."
	site := newMTeamtpSite(t, map[string]string{
		"/api/torrent/search":     mteamtpSearchJSON,
		"/api/torrent/genDlToken": `{"data": "` + "%DLURL%" + `"}`,
		"/dl/123456.torrent":      torrentBytes,
	})
	// rewrite the token response to point at the fake site
	site.srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/torrent/search":
			_, _ = w.Write([]byte(mteamtpSearchJSON))
		case "/api/torrent/genDlToken":
			_, _ = w.Write([]byte(`{"data": "` + site.srv.URL + `/dl/123456.torrent"}`))
		case "/dl/123456.torrent":
			_, _ = w.Write([]byte(torrentBytes))
		default:
			http.NotFound(w, r)
		}
	})
	m := newTestMTeamtp(t, site)
	m.cfg.ApiKey.Value = "my-key"

	q := models.NewTorznabQuery()
	q.QueryType = "search"
	res, err := m.ResultsForQuery(context.Background(), q, false)
	if err != nil {
		t.Fatalf("ResultsForQuery: %v", err)
	}
	data, err := m.Download(context.Background(), res.Releases[0].Link)
	if err != nil {
		t.Fatalf("Download: %v", err)
	}
	if string(data) != torrentBytes {
		t.Fatalf("data: %q", data)
	}
}
