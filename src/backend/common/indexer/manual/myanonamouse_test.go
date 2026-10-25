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

type mamSite struct {
	srv *httptest.Server

	mu         sync.Mutex
	hits       []string
	lastCookie string
	body       string
}

func newMAMSite(t *testing.T, body string) *mamSite {
	t.Helper()
	s := &mamSite{body: body}
	s.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		s.hits = append(s.hits, r.URL.RequestURI())
		s.lastCookie = r.Header.Get("Cookie")
		s.mu.Unlock()
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_, _ = w.Write([]byte(s.body))
	}))
	t.Cleanup(s.srv.Close)
	return s
}

func (s *mamSite) paths() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.hits...)
}

func (s *mamSite) cookie() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lastCookie
}

func newTestMAM(t *testing.T, site *mamSite) *MyAnonamouse {
	t.Helper()
	client, err := httpclient.New(httpclient.Config{}, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("httpclient.New: %v", err)
	}
	ix, err := NewMyAnonamouse(services.Deps{Client: client})
	if err != nil {
		t.Fatalf("NewMyAnonamouse: %v", err)
	}
	m := ix.(*MyAnonamouse)
	m.SiteLinkValue = site.srv.URL + "/"
	client.SetRequestDelay(m.ID(), 0)
	return m
}

const mamSearchFixture = `{
  "error": "",
  "data": [
    {
      "id": 321,
      "title": "Some Book: A Story",
      "author_info": "{\"111\":\"Author One &amp; Co\",\"222\":\"Second Writer\",\"333\":\"Third\",\"444\":\"Fourth\",\"555\":\"Fifth\",\"666\":\"Sixth\"}",
      "description": "A nice book",
      "lang_code": "en",
      "filetype": "epub",
      "vip": true,
      "free": true,
      "personal_freeleech": false,
      "fl_vip": false,
      "category": "14",
      "added": "2024-03-01 12:34:56",
      "times_completed": 42,
      "seeders": 7,
      "leechers": 2,
      "num_files": 3,
      "size": "1.2 MB"
    },
    {
      "id": 322,
      "title": "Radio Drama Show",
      "author_info": "{broken json",
      "description": "drama",
      "lang_code": "",
      "filetype": "",
      "vip": false,
      "free": false,
      "personal_freeleech": false,
      "fl_vip": false,
      "category": "130",
      "added": "2024-03-02 01:02:03",
      "times_completed": 1,
      "seeders": 1,
      "leechers": 0,
      "num_files": 1,
      "size": "500 KB"
    }
  ],
  "message": ""
}`

func TestMyAnonamouseCaps(t *testing.T) {
	ix, err := NewMyAnonamouse(services.Deps{})
	if err != nil {
		t.Fatalf("NewMyAnonamouse: %v", err)
	}
	caps := ix.Capabilities()
	wantParams := []string{models.BookParamQ}
	if strings.Join(caps.BookSearchParams, ",") != strings.Join(wantParams, ",") {
		t.Fatalf("BookSearchParams: %v", caps.BookSearchParams)
	}
	for _, tc := range []struct {
		tracker string
		want    int
	}{
		{"13", 3030}, {"14", 7020}, {"61", 7030}, {"79", 7010}, {"80", 7040},
		{"127", 3030}, {"130", 3030},
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

func TestMyAnonamouseConfigForm(t *testing.T) {
	ix, err := NewMyAnonamouse(services.Deps{})
	if err != nil {
		t.Fatalf("NewMyAnonamouse: %v", err)
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
		"sitelink", "mam_id", "mam_idinstructions", "searchtype",
		"alsosearchtextinthedescription", "alsosearchtextintheseries",
		"alsosearchtextinthefilenames", "searchlanguages",
		"accountinactivity", "tags",
	}
	if strings.Join(ids, "|") != strings.Join(want, "|") {
		t.Fatalf("config ids: %v", ids)
	}
}

func mamQuery() *models.TorznabQuery {
	q := models.NewTorznabQuery()
	q.QueryType = "book"
	q.SearchTerm = "Some Book"
	return q
}

func TestMyAnonamouseSearchParamsFullSequence(t *testing.T) {
	site := newMAMSite(t, mamSearchFixture)
	m := newTestMAM(t, site)

	// 搜索类型 + 语言参数 + 全部可选搜索位置（SPEC-09 §4 专项：完整序列断言）
	m.cfg.SearchType.Value = "fl"
	m.cfg.SearchInDescription.Value = true
	m.cfg.SearchInSeries.Value = true
	m.cfg.SearchInFilenames.Value = true
	m.cfg.SearchLanguages.Values = []string{"1", "36"}

	q := mamQuery()
	q.Categories = []int{7020, 3030}
	if _, err := m.ResultsForQuery(context.Background(), q, false); err != nil {
		t.Fatalf("ResultsForQuery: %v", err)
	}

	paths := site.paths()
	if len(paths) != 1 {
		t.Fatalf("paths: %v", paths)
	}
	// upstream StringUtil.GetQueryString UrlEncodes VALUES only; the
	// tor[...] keys ride along raw (shared.QueryParams mirrors that).
	wantParts := []string{
		"tor[text]=Some+Book",
		"tor[searchType]=fl",
		"tor[srchIn][title]=true",
		"tor[srchIn][author]=true",
		"tor[srchIn][narrator]=true",
		"tor[searchIn]=torrents",
		"tor[sortType]=default",
		"tor[startNumber]=0",
		"perpage=100",
		"thumbnails=1",
		"description=1",
		"tor[srchIn][description]=true",
		"tor[srchIn][series]=true",
		"tor[srchIn][filenames]=true",
		"tor[browse_lang][0]=1",
		"tor[browse_lang][1]=36",
		"tor[cat][0]=13",
		"tor[cat][1]=14",
		"tor[cat][2]=15",
		"tor[cat][3]=16",
		"tor[cat][4]=39",
	}
	for _, part := range wantParts {
		if !strings.Contains(paths[0], part) {
			t.Fatalf("request %q missing part %q", paths[0], part)
		}
	}
}

func TestMyAnonamouseSearchParamsDefaults(t *testing.T) {
	site := newMAMSite(t, mamSearchFixture)
	m := newTestMAM(t, site)

	if _, err := m.ResultsForQuery(context.Background(), mamQuery(), false); err != nil {
		t.Fatalf("ResultsForQuery: %v", err)
	}
	paths := site.paths()
	if len(paths) != 1 {
		t.Fatalf("paths: %v", paths)
	}
	// default search type + no optional srchIn + default cat "0"
	if !strings.Contains(paths[0], "tor[searchType]=all") {
		t.Fatalf("default search type missing: %q", paths[0])
	}
	if strings.Contains(paths[0], "tor[srchIn][description]") {
		t.Fatalf("unexpected description param: %q", paths[0])
	}
	if !strings.Contains(paths[0], "tor[cat][]=0") {
		t.Fatalf("default cat param missing: %q", paths[0])
	}
}

func TestMyAnonamouseParseFixture(t *testing.T) {
	site := newMAMSite(t, mamSearchFixture)
	m := newTestMAM(t, site)

	res, err := m.ResultsForQuery(context.Background(), mamQuery(), false)
	if err != nil {
		t.Fatalf("ResultsForQuery: %v", err)
	}
	if len(res.Releases) != 2 {
		t.Fatalf("releases: %d", len(res.Releases))
	}

	r := res.Releases[0]
	// title + authors (max 5, HTML-decoded) + flags + VIP
	wantTitle := "Some Book: A Story by Author One & Co, Second Writer, Third, Fourth, Fifth [en / EPUB] [VIP]"
	if r.Title != wantTitle {
		t.Fatalf("title: %q want %q", r.Title, wantTitle)
	}
	if r.Description != "A nice book" {
		t.Fatalf("description: %q", r.Description)
	}
	if r.Size == nil || *r.Size != 1258291 { // 1.2 * 1024 * 1024 (parseutil.GetBytes)
		t.Fatalf("size: %v", r.Size)
	}
	if r.Grabs == nil || *r.Grabs != 42 {
		t.Fatalf("grabs: %v", r.Grabs)
	}
	if r.Files == nil || *r.Files != 3 {
		t.Fatalf("files: %v", r.Files)
	}
	if r.Seeders == nil || *r.Seeders != 7 {
		t.Fatalf("seeders: %v", r.Seeders)
	}
	if r.Peers == nil || *r.Peers != 9 {
		t.Fatalf("peers: %v", r.Peers)
	}
	if r.PublishDate.UTC().Format("2006-01-02 15:04:05") != "2024-03-01 12:34:56" {
		t.Fatalf("publish date: %v", r.PublishDate)
	}
	if r.DownloadVolumeFactor == nil || *r.DownloadVolumeFactor != 0 {
		t.Fatalf("dvf (freeleech): %v", r.DownloadVolumeFactor)
	}
	if r.UploadVolumeFactor == nil || *r.UploadVolumeFactor != 1 {
		t.Fatalf("uvf: %v", r.UploadVolumeFactor)
	}
	if r.MinimumSeedTime == nil || *r.MinimumSeedTime != 259200 {
		t.Fatalf("minimum seed time: %v", r.MinimumSeedTime)
	}
	found := false
	for _, c := range r.Category {
		if c == 7020 {
			found = true
		}
	}
	if !found {
		t.Fatalf("category: %v", r.Category)
	}
	if r.Details == nil || !strings.HasSuffix(r.Details.Path, "/t/321") {
		t.Fatalf("details: %v", r.Details)
	}
	if r.Link == nil || !strings.HasSuffix(r.Link.Path, "/tor/download.php") || r.Link.Query().Get("tid") != "321" {
		t.Fatalf("link: %v", r.Link)
	}

	// malformed author_info: skipped with a warning, release still produced
	r2 := res.Releases[1]
	if r2.Title != "Radio Drama Show" {
		t.Fatalf("title2: %q", r2.Title)
	}
	if r2.DownloadVolumeFactor == nil || *r2.DownloadVolumeFactor != 1 {
		t.Fatalf("dvf2: %v", r2.DownloadVolumeFactor)
	}
}

func TestMyAnonamouseEmptySanitizedTermStops(t *testing.T) {
	site := newMAMSite(t, mamSearchFixture)
	m := newTestMAM(t, site)

	q := models.NewTorznabQuery()
	q.QueryType = "book"
	q.SearchTerm = "!!!"
	res, err := m.ResultsForQuery(context.Background(), q, false)
	if err != nil {
		t.Fatalf("ResultsForQuery: %v", err)
	}
	if len(res.Releases) != 0 {
		t.Fatalf("releases: %d", len(res.Releases))
	}
	if paths := site.paths(); len(paths) != 0 {
		t.Fatalf("no request expected, got %v", paths)
	}
}

func TestMyAnonamouseErrorPaths(t *testing.T) {
	// body starting with "Error" → Kind Auth (SPEC-09 §4)
	site := newMAMSite(t, `Error: session expired`)
	m := newTestMAM(t, site)
	_, err := m.ResultsForQuery(context.Background(), mamQuery(), false)
	var ie *indexer.IndexerError
	if !errors.As(err, &ie) {
		t.Fatalf("expected IndexerError, got %T: %v", err, err)
	}
	if ie.Kind != indexer.KindAuth || !strings.HasPrefix(ie.Message, "Error: session expired") {
		t.Fatalf("kind/message: %v %q", ie.Kind, ie.Message)
	}

	// "Nothing returned, out of" error → empty result, not an error
	site2 := newMAMSite(t, `{"error":"Nothing returned, out of 0+","data":null}`)
	m2 := newTestMAM(t, site2)
	res, err := m2.ResultsForQuery(context.Background(), mamQuery(), false)
	if err != nil {
		t.Fatalf("ResultsForQuery: %v", err)
	}
	if len(res.Releases) != 0 {
		t.Fatalf("releases: %d", len(res.Releases))
	}

	// data == null with a message → upstream "Unexpected response content" error
	site3 := newMAMSite(t, `{"error":"","data":null,"message":"weird response"}`)
	m3 := newTestMAM(t, site3)
	_, err = m3.ResultsForQuery(context.Background(), mamQuery(), false)
	if err == nil || !strings.Contains(err.Error(), "Unexpected response content from indexer request: weird response") {
		t.Fatalf("err: %v", err)
	}
}

func TestMyAnonamouseApplyConfiguration(t *testing.T) {
	site := newMAMSite(t, mamSearchFixture)
	m := newTestMAM(t, site)

	status, err := m.ApplyConfiguration(context.Background(), []indexer.ConfigItem{
		{ID: "sitelink", Value: site.srv.URL + "/"},
		{ID: "mam_id", Value: "secretmamid"},
	})
	if err != nil {
		t.Fatalf("ApplyConfiguration: %v", err)
	}
	if status != indexer.ConfigCompleted {
		t.Fatalf("status: %v", status)
	}
	if !m.IsConfigured() {
		t.Fatal("must be configured after success")
	}
	if got := site.cookie(); got != "mam_id=secretmamid" {
		t.Fatalf("cookie sent: %q", got)
	}
}

func TestMyAnonamouseApplyConfigurationFailure(t *testing.T) {
	site := newMAMSite(t, `{"error":"","data":[],"message":""}`)
	m := newTestMAM(t, site)

	_, err := m.ApplyConfiguration(context.Background(), []indexer.ConfigItem{
		{ID: "sitelink", Value: site.srv.URL + "/"},
		{ID: "mam_id", Value: "badid"},
	})
	if err == nil || err.Error() != "Your mam_id did not work" {
		t.Fatalf("err: %v", err)
	}
	if m.IsConfigured() {
		t.Fatal("must not be configured after failure")
	}
}
