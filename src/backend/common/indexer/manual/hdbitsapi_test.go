package manual

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"

	"vestt/common/httpclient"
	"vestt/common/indexer"
	"vestt/common/models"
	"vestt/common/services"
)

const hdbitsTorrentsFixture = `{
  "status": 0,
  "data": [
    {"id": "123", "filename": "Some.Movie.2020.1080p.BluRay.x264-GRP.torrent",
     "name": "Some Movie 2020 1080p BluRay x264-GRP",
     "type_category": 1, "type_medium": 3, "type_origin": 0, "freeleech": "yes",
     "seeders": 10, "leechers": 2, "size": 2147483648, "numfiles": 5,
     "times_completed": 99, "utadded": 1699968000,
     "imdb": {"id": "tt0112471"}, "tvdb": {"id": 0}},
    {"id": "124", "filename": "Show.S01.720p.torrent",
     "name": "Show S01 720p",
     "type_category": 2, "type_medium": 3, "type_origin": 0, "freeleech": "no",
     "seeders": 3, "leechers": 1, "size": 1073741824, "numfiles": 10,
     "times_completed": 7, "utadded": 1699881600},
    {"id": "125", "filename": "Adult.Title.torrent",
     "name": "Adult Title",
     "type_category": 7, "type_medium": 3, "type_origin": 0, "freeleech": "no",
     "seeders": 1, "leechers": 0, "size": 536870912, "numfiles": 1,
     "times_completed": 2, "utadded": 1699795200},
    {"id": "126", "filename": "Disc.Remux.torrent",
     "name": "Disc Remux",
     "type_category": 1, "type_medium": 1, "type_origin": 0, "freeleech": "no",
     "seeders": 4, "leechers": 0, "size": 32212254720, "numfiles": 1,
     "times_completed": 1, "utadded": 1699708800},
    {"id": "127", "filename": "Internal.Encode.torrent",
     "name": "Internal Encode",
     "type_category": 1, "type_medium": 3, "type_origin": 1, "freeleech": "no",
     "seeders": 2, "leechers": 0, "size": 1073741824, "numfiles": 1,
     "times_completed": 0, "utadded": 1699622400}
  ]
}`

// hdbitsRequestProbe captures the decoded fields of the last /api/torrents
// request for assertion.
type hdbitsRequestProbe struct {
	tvdbID   *int
	season   *int
	episode  *int
	search   string
	imdbID   string
	page     *int
	category []any
	codec    []any
	limit    *float64
}

type hdbitsSite struct {
	*httptest.Server

	mu      sync.Mutex
	testOK  bool
	badJSON bool
	bodies  []string
	probe   hdbitsRequestProbe
}

func newHDBitsSite(t *testing.T, testOK bool) *hdbitsSite {
	t.Helper()
	s := &hdbitsSite{testOK: testOK}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		body, _ := io.ReadAll(r.Body)
		s.mu.Lock()
		defer s.mu.Unlock()
		s.bodies = append(s.bodies, string(body))
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/test":
			if s.badJSON {
				_, _ = fmt.Fprint(w, `<html>cloudflare</html>`)
				return
			}
			if s.testOK {
				_, _ = fmt.Fprint(w, `{"status":0}`)
				return
			}
			_, _ = fmt.Fprint(w, `{"status":5,"message":"Wrong passkey"}`)
		case "/api/torrents":
			var req map[string]any
			if err := json.Unmarshal(body, &req); err != nil {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			p := hdbitsRequestProbe{}
			if v, ok := req["tvdb"].(map[string]any); ok {
				if id, ok := v["id"].(float64); ok {
					n := int(id)
					p.tvdbID = &n
				}
				if sea, ok := v["season"].(float64); ok {
					n := int(sea)
					p.season = &n
				}
				if ep, ok := v["episode"].(float64); ok {
					n := int(ep)
					p.episode = &n
				}
			}
			if v, ok := req["search"].(string); ok {
				p.search = v
			}
			if v, ok := req["imdb"].(map[string]any); ok {
				p.imdbID = strconv.FormatInt(int64(v["id"].(float64)), 10)
			}
			if v, ok := req["page"].(float64); ok {
				n := int(v)
				p.page = &n
			}
			if v, ok := req["category"].([]any); ok {
				p.category = v
			}
			if v, ok := req["codec"].([]any); ok {
				p.codec = v
			}
			if v, ok := req["limit"].(float64); ok {
				p.limit = &v
			}
			s.probe = p
			_, _ = fmt.Fprint(w, hdbitsTorrentsFixture)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(s.Close)
	return s
}

func (s *hdbitsSite) lastBody() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.bodies) == 0 {
		return ""
	}
	return s.bodies[len(s.bodies)-1]
}

func (s *hdbitsSite) lastProbe() hdbitsRequestProbe {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.probe
}

func newTestHDBits(t *testing.T, site *hdbitsSite) *HDBitsApi {
	t.Helper()
	client, err := httpclient.New(httpclient.Config{}, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("httpclient.New: %v", err)
	}
	ix, err := NewHDBitsApi(services.Deps{Client: client})
	if err != nil {
		t.Fatalf("NewHDBitsApi: %v", err)
	}
	h := ix.(*HDBitsApi)
	h.SiteLinkValue = site.URL + "/"
	h.DefaultSiteLink = site.URL + "/"
	// tests must not pay the upstream 2s requestDelay
	client.SetRequestDelay(h.ID(), 0)
	return h
}

func hdbitsConfigured(t *testing.T, site *hdbitsSite) *HDBitsApi {
	t.Helper()
	h := newTestHDBits(t, site)
	status, err := h.ApplyConfiguration(context.Background(), []indexer.ConfigItem{
		{ID: "sitelink", Value: site.URL + "/"},
		{ID: "username", Value: "okuser"},
		{ID: "passkey", Value: "pk123"},
	})
	if err != nil {
		t.Fatalf("ApplyConfiguration: %v", err)
	}
	if status != indexer.ConfigCompleted || !h.IsConfigured() {
		t.Fatalf("status/configured: %v %v", status, h.IsConfigured())
	}
	return h
}

func TestHDBitsCaps(t *testing.T) {
	ix, err := NewHDBitsApi(services.Deps{})
	if err != nil {
		t.Fatalf("NewHDBitsApi: %v", err)
	}
	caps := ix.Capabilities()
	wantTV := []string{models.TVParamQ, models.TVParamSeason, models.TVParamEp, models.TVParamTvdbID}
	if strings.Join(caps.TVSearchParams, ",") != strings.Join(wantTV, ",") {
		t.Fatalf("TVSearchParams: %v", caps.TVSearchParams)
	}
	wantMovie := []string{models.MovieParamQ, models.MovieParamImdbID}
	if strings.Join(caps.MovieSearchParams, ",") != strings.Join(wantMovie, ",") {
		t.Fatalf("MovieSearchParams: %v", caps.MovieSearchParams)
	}
	for _, tc := range []struct {
		tracker string
		want    int
	}{
		{"1", 2000}, {"2", 5000}, {"3", 5080}, {"4", 3000},
		{"5", 5060}, {"6", 3000}, {"7", 6000}, {"8", 8000},
	} {
		got := caps.Categories.MapTrackerCatToNewznab(tc.tracker)
		// AddCategoryMapping with a desc also creates a custom 1:1 category
		// (id ≥ 100000) that rides along in the mapping — upstream parity.
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

func TestHDBitsConfigForm(t *testing.T) {
	ix, err := NewHDBitsApi(services.Deps{})
	if err != nil {
		t.Fatalf("NewHDBitsApi: %v", err)
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
		"sitelink", "username", "passkey", "filterfreeleechonly",
		"usefilenamesasreleasetitles", "codec", "medium", "origin", "", "tags",
	}
	if strings.Join(ids, "|") != strings.Join(want, "|") {
		t.Fatalf("config ids = %v, want %v", ids, want)
	}
	// multi-select options render as objects in declaration order
	for _, it := range items {
		if it["id"].(string) == "medium" {
			opts := it["options"].(map[string]any)
			if opts["1"] != "Blu-ray/HD DVD" || opts["5"] != "Remux" || opts["6"] != "WEB-DL" {
				t.Fatalf("medium options: %v", opts)
			}
			if it["type"].(string) != "inputcheckbox" {
				t.Fatalf("medium type: %v", it["type"])
			}
		}
	}
}

func TestHDBitsApplyConfiguration(t *testing.T) {
	site := newHDBitsSite(t, true)
	hdbitsConfigured(t, site)
	body := site.lastBody()
	if !strings.Contains(body, `"username":"okuser"`) || !strings.Contains(body, `"passkey":"pk123"`) {
		t.Fatalf("test request body: %s", body)
	}
}

func TestHDBitsApplyConfigurationError(t *testing.T) {
	site := newHDBitsSite(t, false)
	h := newTestHDBits(t, site)
	_, err := h.ApplyConfiguration(context.Background(), []indexer.ConfigItem{
		{ID: "sitelink", Value: site.URL + "/"},
		{ID: "username", Value: "okuser"},
		{ID: "passkey", Value: "bad"},
	})
	if err == nil || err.Error() != "HDBits returned an error with status code 5: Wrong passkey" {
		t.Fatalf("err: %v", err)
	}
	if h.IsConfigured() {
		t.Fatal("must not be configured after failed test call")
	}
}

func TestHDBitsApplyConfigurationBadJSON(t *testing.T) {
	site := newHDBitsSite(t, true)
	site.badJSON = true
	h := newTestHDBits(t, site)
	_, err := h.ApplyConfiguration(context.Background(), []indexer.ConfigItem{
		{ID: "sitelink", Value: site.URL + "/"},
		{ID: "username", Value: "okuser"},
		{ID: "passkey", Value: "pk123"},
	})
	if err == nil || !strings.HasPrefix(err.Error(), "Error while parsing json: ") {
		t.Fatalf("err: %v", err)
	}
}

func TestHDBitsImdbQueryAndParse(t *testing.T) {
	site := newHDBitsSite(t, true)
	h := hdbitsConfigured(t, site)

	q := models.NewTorznabQuery()
	q.QueryType = "movie"
	q.ImdbID = "tt0112471"
	res, err := h.ResultsForQuery(context.Background(), q, false)
	if err != nil {
		t.Fatalf("ResultsForQuery: %v", err)
	}
	if len(res.Releases) != 5 {
		t.Fatalf("releases: %d", len(res.Releases))
	}

	// imdb id posted as a numeric id
	if probe := site.lastProbe(); probe.imdbID != "112471" {
		t.Fatalf("imdb id posted: %q", probe.imdbID)
	}

	r := res.Releases[0]
	// UseFilenames (default true): filename minus ".torrent"
	if r.Title != "Some.Movie.2020.1080p.BluRay.x264-GRP" {
		t.Fatalf("title: %q", r.Title)
	}
	// freeleech "yes" → 100% free
	if r.DownloadVolumeFactor == nil || *r.DownloadVolumeFactor != 0 || r.UploadVolumeFactor == nil || *r.UploadVolumeFactor != 1 {
		t.Fatalf("factors: %v/%v", r.DownloadVolumeFactor, r.UploadVolumeFactor)
	}
	if r.Size == nil || *r.Size != 2147483648 {
		t.Fatalf("size: %v", r.Size)
	}
	if r.Files == nil || *r.Files != 5 || r.Grabs == nil || *r.Grabs != 99 {
		t.Fatalf("files/grabs: %v/%v", r.Files, r.Grabs)
	}
	if r.Seeders == nil || *r.Seeders != 10 || r.Peers == nil || *r.Peers != 12 {
		t.Fatalf("seeders/peers: %v/%v", r.Seeders, r.Peers)
	}
	if r.PublishDate.Unix() != 1699968000 {
		t.Fatalf("publish date: %v", r.PublishDate)
	}
	if r.Imdb == nil || *r.Imdb != 112471 {
		t.Fatalf("imdb: %v", r.Imdb)
	}
	if r.TVDBId == nil || *r.TVDBId != 0 {
		t.Fatalf("tvdb: %v", r.TVDBId)
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
	wantLink := site.URL + "/download.php/Some.Movie.2020.1080p.BluRay.x264-GRP.torrent?id=123&passkey=pk123"
	if r.Link == nil || r.Link.String() != wantLink {
		t.Fatalf("link: %v", r.Link)
	}
	if r.Details == nil || r.Details.String() != site.URL+"/details.php?id=123" {
		t.Fatalf("details: %v", r.Details)
	}

	// TV content → 50% freeleech, TV category
	r2 := res.Releases[1]
	if r2.Title != "Show.S01.720p" {
		t.Fatalf("r2 title: %q", r2.Title)
	}
	if *r2.DownloadVolumeFactor != 0.5 {
		t.Fatalf("r2 dvf: %v", r2.DownloadVolumeFactor)
	}
	foundTV := false
	for _, c := range r2.Category {
		if c == models.CatTV.ID {
			foundTV = true
		}
	}
	if !foundTV {
		t.Fatalf("r2 category: %v, want 5000 included", r2.Category)
	}

	// XXX content: name (not filename), neutral leech (DVF 0 + UVF 0)
	r3 := res.Releases[2]
	if r3.Title != "Adult Title" {
		t.Fatalf("r3 title: %q", r3.Title)
	}
	if *r3.DownloadVolumeFactor != 0 || *r3.UploadVolumeFactor != 0 {
		t.Fatalf("r3 factors: %v/%v", r3.DownloadVolumeFactor, r3.UploadVolumeFactor)
	}
	foundXXX := false
	for _, c := range r3.Category {
		if c == models.CatXXX.ID {
			foundXXX = true
		}
	}
	if !foundXXX {
		t.Fatalf("r3 category: %v, want 6000 included", r3.Category)
	}

	// full disc (medium 1) → 50% freeleech
	if *res.Releases[3].DownloadVolumeFactor != 0.5 {
		t.Fatalf("r4 dvf: %v", res.Releases[3].DownloadVolumeFactor)
	}
	// internal origin → 50% freeleech
	if *res.Releases[4].DownloadVolumeFactor != 0.5 {
		t.Fatalf("r5 dvf: %v", res.Releases[4].DownloadVolumeFactor)
	}

	// the torrents request carries username/passkey and limit=100
	body := site.lastBody()
	if !strings.Contains(body, `"username":"okuser"`) || !strings.Contains(body, `"passkey":"pk123"`) {
		t.Fatalf("torrents request body: %s", body)
	}
	if probe := site.lastProbe(); probe.limit == nil || *probe.limit != 100 {
		t.Fatalf("limit: %v", probe.limit)
	}
}

func TestHDBitsSearchQuery(t *testing.T) {
	site := newHDBitsSite(t, true)
	h := hdbitsConfigured(t, site)

	q := models.NewTorznabQuery()
	q.QueryType = "search"
	q.SearchTerm = "some movie"
	q.Categories = []int{models.CatMovies.ID}
	res, err := h.ResultsForQuery(context.Background(), q, false)
	if err != nil {
		t.Fatalf("ResultsForQuery: %v", err)
	}
	// the upstream AND-filter keeps only the release whose title contains
	// both query words
	if len(res.Releases) != 1 {
		t.Fatalf("releases: %d", len(res.Releases))
	}
	probe := site.lastProbe()
	if probe.search != "some movie" {
		t.Fatalf("search: %q", probe.search)
	}
	// movie category maps to tracker category "1"
	if len(probe.category) != 1 || probe.category[0] != "1" {
		t.Fatalf("category: %v", probe.category)
	}
}

func TestHDBitsCodecFilterAndPagination(t *testing.T) {
	site := newHDBitsSite(t, true)
	h := hdbitsConfigured(t, site)
	h.cfg.Codecs.Values = []string{"1", "5"}

	q := models.NewTorznabQuery()
	q.QueryType = "tvsearch"
	q.Limit = 100
	q.Offset = 200
	_, err := h.ResultsForQuery(context.Background(), q, false)
	if err != nil {
		t.Fatalf("ResultsForQuery: %v", err)
	}
	probe := site.lastProbe()
	if len(probe.codec) != 2 || probe.codec[0] != float64(1) || probe.codec[1] != float64(5) {
		t.Fatalf("codec: %v", probe.codec)
	}
	if probe.page == nil || *probe.page != 2 {
		t.Fatalf("page: %v", probe.page)
	}
}

func TestHDBitsTvdbQueries(t *testing.T) {
	site := newHDBitsSite(t, true)
	h := hdbitsConfigured(t, site)

	// regular season/episode
	q := models.NewTorznabQuery()
	q.QueryType = "tvsearch"
	q.TvdbID = hdbIntPtr(991)
	season := 2
	q.Season = &season
	q.Episode = "5"
	_, err := h.ResultsForQuery(context.Background(), q, false)
	if err != nil {
		t.Fatalf("ResultsForQuery: %v", err)
	}
	probe := site.lastProbe()
	if probe.tvdbID == nil || *probe.tvdbID != 991 {
		t.Fatalf("tvdb id: %v", probe.tvdbID)
	}
	if probe.season == nil || *probe.season != 2 || probe.episode == nil || *probe.episode != 5 {
		t.Fatalf("season/episode: %v/%v", probe.season, probe.episode)
	}

	// airdate style (yyyy MM/dd) → search "yyyy-MM-dd", no season/episode
	q2 := models.NewTorznabQuery()
	q2.QueryType = "tvsearch"
	q2.TvdbID = hdbIntPtr(991)
	airSeason := 2023
	q2.Season = &airSeason
	q2.Episode = "04/15"
	_, err = h.ResultsForQuery(context.Background(), q2, false)
	if err != nil {
		t.Fatalf("ResultsForQuery: %v", err)
	}
	probe = site.lastProbe()
	if probe.search != "2023-04-15" {
		t.Fatalf("airdate search: %q", probe.search)
	}
	if probe.season != nil || probe.episode != nil {
		t.Fatalf("airdate season/episode must be absent: %v/%v", probe.season, probe.episode)
	}
}

func TestHDBitsFilterFreeleech(t *testing.T) {
	site := newHDBitsSite(t, true)
	h := hdbitsConfigured(t, site)
	h.cfg.FilterFreeleech.Value = true

	q := models.NewTorznabQuery()
	q.QueryType = "movie"
	q.ImdbID = "tt0112471"
	res, err := h.ResultsForQuery(context.Background(), q, false)
	if err != nil {
		t.Fatalf("ResultsForQuery: %v", err)
	}
	if len(res.Releases) != 1 {
		t.Fatalf("releases: %d", len(res.Releases))
	}
	if *res.Releases[0].DownloadVolumeFactor != 0 {
		t.Fatalf("dvf: %v", res.Releases[0].DownloadVolumeFactor)
	}
}

func hdbIntPtr(v int) *int { return &v }
