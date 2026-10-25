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

type animeBytesSite struct {
	srv   *httptest.Server
	mu    sync.Mutex
	hits  []string
	serve map[string]animeBytesResponse
}

func newAnimeBytesSite(t *testing.T, serve map[string]animeBytesResponse) *animeBytesSite {
	t.Helper()
	s := &animeBytesSite{serve: serve}
	s.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		s.hits = append(s.hits, r.URL.RequestURI())
		body, ok := s.serve[r.URL.Path]
		s.mu.Unlock()
		if !ok {
			http.Error(w, "<html>not found</html>", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(animeBytesFixtureJSON(body)))
	}))
	t.Cleanup(s.srv.Close)
	return s
}

func (s *animeBytesSite) paths() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.hits...)
}

const testAnimeBytesPasskey = "0123456789abcdef0123456789abcdef" // 32 chars

func newTestAnimeBytes(t *testing.T, site *animeBytesSite) *AnimeBytes {
	t.Helper()
	client, err := httpclient.New(httpclient.Config{}, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("httpclient.New: %v", err)
	}
	ix, err := NewAnimeBytes(services.Deps{Client: client})
	if err != nil {
		t.Fatalf("NewAnimeBytes: %v", err)
	}
	a := ix.(*AnimeBytes)
	a.SiteLinkValue = site.srv.URL + "/"
	a.cfg.Username.Value = "testuser"
	a.cfg.Passkey.Value = testAnimeBytesPasskey
	// tests must not pay the upstream 3.1s API delay
	client.SetRequestDelay(a.ID(), 0)
	return a
}

func animeBytesFixtureJSON(r animeBytesResponse) string {
	var sb strings.Builder
	sb.WriteString(`{"Matches":`)
	sb.WriteString(abItoa64(r.Matches))
	sb.WriteString(`,"Groups":[`)
	for gi, g := range r.Groups {
		if gi > 0 {
			sb.WriteString(",")
		}
		sb.WriteString(`{"ID":`)
		sb.WriteString(abItoa64(g.ID))
		sb.WriteString(`,"CategoryName":`)
		sb.WriteString(abQuoteJSON(g.CategoryName))
		sb.WriteString(`,"FullName":`)
		sb.WriteString(abQuoteJSON(g.FullName))
		sb.WriteString(`,"GroupName":`)
		sb.WriteString(abQuoteJSON(g.GroupName))
		sb.WriteString(`,"SeriesName":`)
		sb.WriteString(abQuoteJSON(g.SeriesName))
		sb.WriteString(`,"Year":`)
		sb.WriteString(g.Year)
		sb.WriteString(`,"Image":`)
		sb.WriteString(abQuoteJSON(g.Image))
		sb.WriteString(`,"SynonymnsV2":{`)
		first := true
		for k, v := range g.Synonymns {
			if !first {
				sb.WriteString(",")
			}
			first = false
			sb.WriteString(abQuoteJSON(k))
			sb.WriteString(":")
			sb.WriteString(abQuoteJSON(v))
		}
		sb.WriteString(`},"Description":`)
		sb.WriteString(abQuoteJSON(g.Description))
		sb.WriteString(`,"Tags":[],"Torrents":[`)
		for ti, tr := range g.Torrents {
			if ti > 0 {
				sb.WriteString(",")
			}
			sb.WriteString(`{"ID":`)
			sb.WriteString(abItoa64(tr.ID))
			sb.WriteString(`,"EditionData":{"EditionTitle":`)
			sb.WriteString(abQuoteJSON(tr.EditionTitle))
			sb.WriteString(`},"RawDownMultiplier":`)
			sb.WriteString(tr.RawDownMultiplier)
			sb.WriteString(`,"RawUpMultiplier":`)
			sb.WriteString(tr.RawUpMultiplier)
			sb.WriteString(`,"Link":`)
			sb.WriteString(abQuoteJSON(tr.Link))
			sb.WriteString(`,"Property":`)
			sb.WriteString(abQuoteJSON(tr.Property))
			sb.WriteString(`,"Snatched":`)
			sb.WriteString(abItoa64(tr.Snatched))
			sb.WriteString(`,"Seeders":`)
			sb.WriteString(abItoa64(tr.Seeders))
			sb.WriteString(`,"Leechers":`)
			sb.WriteString(abItoa64(tr.Leechers))
			sb.WriteString(`,"Size":`)
			sb.WriteString(abItoa64(tr.Size))
			sb.WriteString(`,"FileCount":`)
			sb.WriteString(abItoa64(tr.FileCount))
			sb.WriteString(`,"FileList":[`)
			for fi, f := range tr.Files {
				if fi > 0 {
					sb.WriteString(",")
				}
				sb.WriteString(`{"filename":`)
				sb.WriteString(abQuoteJSON(f.FileName))
				sb.WriteString(`,"size":`)
				sb.WriteString(abItoa64(f.FileSize))
				sb.WriteString(`}`)
			}
			sb.WriteString(`],"UploadTime":`)
			sb.WriteString(abQuoteJSON(tr.UploadTime))
			sb.WriteString(`}`)
		}
		sb.WriteString(`]}`)
	}
	sb.WriteString(`],"Error":`)
	sb.WriteString(abQuoteJSON(r.Error))
	sb.WriteString(`}`)
	return sb.String()
}

func abItoa64(v int64) string {
	if v == 0 {
		return "0"
	}
	neg := v < 0
	if neg {
		v = -v
	}
	var buf [20]byte
	i := len(buf)
	for v > 0 {
		i--
		buf[i] = byte('0' + v%10)
		v /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

func abQuoteJSON(s string) string {
	var sb strings.Builder
	sb.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			sb.WriteString(`\"`)
		case '\\':
			sb.WriteString(`\\`)
		default:
			sb.WriteRune(r)
		}
	}
	sb.WriteByte('"')
	return sb.String()
}

type animeBytesResponse struct {
	Matches int64
	Error   string
	Groups  []animeBytesGroupFixture
}

type animeBytesGroupFixture struct {
	ID           int64
	CategoryName string
	FullName     string
	GroupName    string
	SeriesName   string
	Year         string // raw JSON literal, e.g. "2019" or "\"2019\""
	Image        string
	Synonymns    map[string]string
	Description  string
	Torrents     []animeBytesTorrentFixture
}

type animeBytesTorrentFixture struct {
	ID                int64
	EditionTitle      string
	RawDownMultiplier string // raw JSON literal
	RawUpMultiplier   string
	Link              string
	Property          string
	Snatched          int64
	Seeders           int64
	Leechers          int64
	Size              int64
	FileCount         int64
	Files             []animeBytesFileFixture
	UploadTime        string
}

type animeBytesFileFixture struct {
	FileName string
	FileSize int64
}

func animeBytesTVGroupFixture() animeBytesGroupFixture {
	return animeBytesGroupFixture{
		ID:           12345,
		CategoryName: "Anime",
		FullName:     "Some Show [2019]",
		GroupName:    "TV Series",
		SeriesName:   "Some Show",
		Year:         "2019",
		Image:        "https://img.example.org/poster.jpg",
		Synonymns:    map[string]string{"Japanese": "サムショー", "Romaji": "Samu Shō"},
		Description:  "A show about things.",
		Torrents: []animeBytesTorrentFixture{
			{
				ID:                999001,
				EditionTitle:      "",
				RawDownMultiplier: "1",
				RawUpMultiplier:   "1",
				Link:              "https://animebytes.tv/torrent/999001/download/aaaaaaaa",
				Property:          "Web|MKV|h264|10-bit|Softsubs (GroupName)|Freeleech",
				Snatched:          42,
				Seeders:           5,
				Leechers:          2,
				Size:              2147483648,
				FileCount:         1,
				Files:             []animeBytesFileFixture{{FileName: "[GroupName] Some Show - 01 [1080p].mkv", FileSize: 2147483648}},
				UploadTime:        "2024-03-02 14:25:36",
			},
		},
	}
}

func TestAnimeBytesCaps(t *testing.T) {
	ix, err := NewAnimeBytes(services.Deps{})
	if err != nil {
		t.Fatalf("NewAnimeBytes: %v", err)
	}
	caps := ix.Capabilities()
	if !caps.SupportsRawSearch {
		t.Fatal("SupportsRawSearch must be true")
	}
	if strings.Join(caps.TVSearchParams, ",") != "q,season,ep" {
		t.Fatalf("TVSearchParams: %v", caps.TVSearchParams)
	}
	if strings.Join(caps.MovieSearchParams, ",") != "q" ||
		strings.Join(caps.MusicSearchParams, ",") != "q" ||
		strings.Join(caps.BookSearchParams, ",") != "q" {
		t.Fatalf("movie/music/book params: %v %v %v", caps.MovieSearchParams, caps.MusicSearchParams, caps.BookSearchParams)
	}
	for _, tc := range []struct {
		tracker string
		want    int
	}{
		{"anime[tv_series]", 5070}, {"anime[tv_special]", 5070}, {"anime[ova]", 5070},
		{"anime[ona]", 5070}, {"anime[dvd_special]", 5070}, {"anime[bd_special]", 5070},
		{"anime[movie]", 2000}, {"audio", 3000}, {"gamec[game]", 4050},
		{"gamec[visual_novel]", 4050}, {"printedtype[manga]", 7030},
		{"printedtype[oneshot]", 7030}, {"printedtype[anthology]", 7030},
		{"printedtype[manhwa]", 7030}, {"printedtype[light_novel]", 7030},
		{"printedtype[artbook]", 7030},
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

func TestAnimeBytesConfigForm(t *testing.T) {
	ix, err := NewAnimeBytes(services.Deps{})
	if err != nil {
		t.Fatalf("NewAnimeBytes: %v", err)
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
		"sitelink", "username", "passkey", "",
		"searchfreeleechonly", "excludehentaifromresults", "includerawinresults",
		"searchbyyearasadifferentargumentintherequest", "padepisodenumberforsonarrcompatibility",
		"addreleasesforjapanesetitle", "addreleasesforromajititle",
		"addreleasesforalternativetitle(s)", "addreleasesbasedonsinglefilename",
		"filterresultsbyseason/episode", "tags",
	}
	if len(ids) != len(want) {
		t.Fatalf("config ids: %v", ids)
	}
	for i := range want {
		if ids[i] != want[i] {
			t.Fatalf("config id %d: %q want %q (all: %v)", i, ids[i], want[i], ids)
		}
	}
	// instructions displayinfo carries the upstream passkey note
	if items[3]["value"] != "Note: Go to AnimeBytes site and open your account settings. Go to 'Account' tab, move cursor over black part near 'Passkey' and copy its value. Your username is case sensitive." {
		t.Fatalf("instructions value: %v", items[3]["value"])
	}
}

func TestAnimeBytesApplyConfigurationInvalidPasskey(t *testing.T) {
	site := newAnimeBytesSite(t, map[string]animeBytesResponse{})
	a := newTestAnimeBytes(t, site)
	a.cfg.Passkey.Value = "short"
	_, err := a.ApplyConfiguration(context.Background(), []indexer.ConfigItem{
		{ID: "sitelink", Value: site.srv.URL + "/"},
	})
	if err == nil || err.Error() != "invalid passkey configured: expected length: 32, 48, or 56, got 5" {
		t.Fatalf("err: %v", err)
	}
	if len(site.paths()) != 0 {
		t.Fatalf("no request expected, got %v", site.paths())
	}
}

func TestAnimeBytesApplyConfigurationAndSearchRequest(t *testing.T) {
	site := newAnimeBytesSite(t, map[string]animeBytesResponse{
		"/scrape.php": {Matches: 1, Groups: []animeBytesGroupFixture{animeBytesTVGroupFixture()}},
	})
	a := newTestAnimeBytes(t, site)

	_, err := a.ApplyConfiguration(context.Background(), []indexer.ConfigItem{
		{ID: "sitelink", Value: site.srv.URL + "/"},
	})
	if err != nil {
		t.Fatalf("ApplyConfiguration: %v", err)
	}
	if !a.IsConfigured() {
		t.Fatal("must be configured")
	}
	if a.SiteLink() != site.srv.URL+"/" {
		t.Fatalf("site link rewritten to default: %q", a.SiteLink())
	}

	// RSS-style query (no search term): limit=15, both anime+music requests
	site.mu.Lock()
	site.hits = nil
	site.mu.Unlock()
	res, err := a.ResultsForQuery(context.Background(), &models.TorznabQuery{QueryType: "search", IsTest: true}, false)
	if err != nil {
		t.Fatalf("ResultsForQuery: %v", err)
	}
	if len(res.Releases) != 1 {
		t.Fatalf("releases: %d", len(res.Releases))
	}
	paths := site.paths()
	if len(paths) != 2 {
		t.Fatalf("paths: %v", paths)
	}
	for _, p := range paths {
		if !strings.HasPrefix(p, "/scrape.php?") {
			t.Fatalf("path: %s", p)
		}
		q := strings.TrimPrefix(p, "/scrape.php?")
		for _, part := range []string{
			"username=testuser", "torrent_pass=" + testAnimeBytesPasskey,
			"sort=grouptime", "way=desc", "limit=15", "searchstr=",
		} {
			if !strings.Contains(q, part) {
				t.Fatalf("query %q missing %q", q, part)
			}
		}
		if strings.Contains(q, "type=anime") == strings.Contains(q, "type=music") {
			t.Fatalf("expected one anime and one music request: %v", paths)
		}
	}

	// search query: episode stripped by CleanSearchTerm, limit=50
	site.mu.Lock()
	site.hits = nil
	site.mu.Unlock()
	q := models.NewTorznabQuery()
	q.QueryType = "tvsearch"
	q.SearchTerm = "Some Show 12"
	s2, err := a.ResultsForQuery(context.Background(), q, false)
	if err != nil {
		t.Fatalf("ResultsForQuery: %v", err)
	}
	if len(s2.Releases) != 1 {
		t.Fatalf("releases: %d", len(s2.Releases))
	}
	found := false
	for _, p := range site.paths() {
		if strings.Contains(p, "type=anime") {
			found = true
			q := strings.TrimPrefix(p, "/scrape.php?")
			if !strings.Contains(q, "limit=50") || !strings.Contains(q, "searchstr=Some+Show") {
				t.Fatalf("anime query: %q", q)
			}
		}
	}
	if !found {
		t.Fatalf("paths: %v", site.paths())
	}
}

func TestAnimeBytesParseReleaseFields(t *testing.T) {
	site := newAnimeBytesSite(t, map[string]animeBytesResponse{
		"/scrape.php": {Matches: 1, Groups: []animeBytesGroupFixture{animeBytesTVGroupFixture()}},
	})
	a := newTestAnimeBytes(t, site)
	a.cfg.AddFileNameTitles.Value = true

	res, err := a.ResultsForQuery(context.Background(), &models.TorznabQuery{QueryType: "search", IsTest: true}, false)
	if err != nil {
		t.Fatalf("ResultsForQuery: %v", err)
	}
	if len(res.Releases) != 2 {
		t.Fatalf("releases: %d (synonym title + filename title)", len(res.Releases))
	}

	r := res.Releases[0]
	if r.Title != "[GroupName] Some Show S01 [Web][MKV][h264][10-bit][Softsubs (GroupName)]" {
		t.Fatalf("title: %q", r.Title)
	}
	if r.Year == nil || *r.Year != 2019 {
		t.Fatalf("year: %v", r.Year)
	}
	if r.Size == nil || *r.Size != 2147483648 {
		t.Fatalf("size: %v", r.Size)
	}
	if r.Seeders == nil || *r.Seeders != 5 {
		t.Fatalf("seeders: %v", r.Seeders)
	}
	if r.Peers == nil || *r.Peers != 7 {
		t.Fatalf("peers: %v", r.Peers)
	}
	if r.Grabs == nil || *r.Grabs != 42 {
		t.Fatalf("grabs: %v", r.Grabs)
	}
	if r.Files == nil || *r.Files != 1 {
		t.Fatalf("files: %v", r.Files)
	}
	if r.PublishDate.Format("2006-01-02 15:04:05") != "2024-03-02 14:25:36" {
		t.Fatalf("publish date: %v", r.PublishDate)
	}
	if len(r.Category) == 0 || r.Category[0] != 5070 {
		t.Fatalf("category: %v", r.Category)
	}
	if r.Description != "A show about things." {
		t.Fatalf("description: %q", r.Description)
	}
	if r.Details == nil || !strings.HasSuffix(r.Details.Path, "/torrent/999001/group") {
		t.Fatalf("details: %v", r.Details)
	}
	if r.Link == nil || r.Link.String() != "https://animebytes.tv/torrent/999001/download/aaaaaaaa" {
		t.Fatalf("link: %v", r.Link)
	}
	if r.Poster == nil || r.Poster.String() != "https://img.example.org/poster.jpg" {
		t.Fatalf("poster: %v", r.Poster)
	}
	if r.MinimumRatio == nil || *r.MinimumRatio != 1 {
		t.Fatalf("minimum ratio: %v", r.MinimumRatio)
	}
	if r.MinimumSeedTime == nil || *r.MinimumSeedTime != 259200+2*18000 {
		t.Fatalf("minimum seed time: %v", r.MinimumSeedTime)
	}
	if r.DownloadVolumeFactor == nil || *r.DownloadVolumeFactor != 1 {
		t.Fatalf("dvf: %v", r.DownloadVolumeFactor)
	}
	if r.UploadVolumeFactor == nil || *r.UploadVolumeFactor != 1 {
		t.Fatalf("uvf: %v", r.UploadVolumeFactor)
	}
	if r.Guid == nil || !strings.Contains(r.Guid.String(), "&nh=") {
		t.Fatalf("guid: %v", r.Guid)
	}

	// filename title (AddFileNameTitles default true)
	fn := res.Releases[1]
	if fn.Title != "[GroupName] Some Show - 01 [1080p].mkv" {
		t.Fatalf("filename title: %q", fn.Title)
	}
}

func TestAnimeBytesYearAsStringAndSearchParams(t *testing.T) {
	g := animeBytesTVGroupFixture()
	g.Year = `"2019"` // AllowReadingFromString
	g.GroupName = "TV Series"
	g.Synonymns = nil
	site := newAnimeBytesSite(t, map[string]animeBytesResponse{
		"/scrape.php": {Matches: 1, Groups: []animeBytesGroupFixture{g}},
	})
	a := newTestAnimeBytes(t, site)
	a.cfg.SearchByYear.Value = true
	a.cfg.ExcludeHentai.Value = true

	q := models.NewTorznabQuery()
	q.QueryType = "tvsearch"
	q.SearchTerm = "Some Show 2019"
	q.Categories = []int{5070}
	season := 2
	q.Season = &season
	if _, err := a.ResultsForQuery(context.Background(), q, false); err != nil {
		t.Fatalf("ResultsForQuery: %v", err)
	}
	var animeQuery string
	for _, p := range site.paths() {
		if strings.Contains(p, "type=anime") {
			animeQuery = strings.TrimPrefix(p, "/scrape.php?")
		}
	}
	if animeQuery == "" {
		t.Fatalf("paths: %v", site.paths())
	}
	// year parsed from the raw search term; specials/ova cats removed for
	// non-zero season TV searches; hentai excluded (shared.Encode keeps
	// square brackets unencoded, mirroring the Go port's WebUtility.UrlEncode)
	if !strings.Contains(animeQuery, "year=2019") {
		t.Fatalf("year param missing: %q", animeQuery)
	}
	if strings.Contains(animeQuery, "anime[tv_special]=1") || strings.Contains(animeQuery, "anime[ova]=1") ||
		strings.Contains(animeQuery, "anime[dvd_special]=1") || strings.Contains(animeQuery, "anime[bd_special]=1") {
		t.Fatalf("specials must be removed: %q", animeQuery)
	}
	if !strings.Contains(animeQuery, "anime[tv_series]=1") {
		t.Fatalf("tv_series cat missing: %q", animeQuery)
	}
	if !strings.Contains(animeQuery, "hentai=0") {
		t.Fatalf("hentai=0 missing: %q", animeQuery)
	}
}

func TestAnimeBytesErrorPaths(t *testing.T) {
	// 401 with non-JSON body => credentials error
	site401 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
	}))
	t.Cleanup(site401.Close)
	a := newTestAnimeBytes(t, newAnimeBytesSite(t, nil))
	a.SiteLinkValue = site401.URL + "/"
	_, err := a.ResultsForQuery(context.Background(), &models.TorznabQuery{QueryType: "search", IsTest: true}, false)
	if err == nil || !strings.Contains(err.Error(), "Invalid response, please check if your credentials are valid.") {
		t.Fatalf("err: %v", err)
	}

	// 400 with non-JSON body => "Unexpected response (not JSON)"
	// (400 is not retried by the client stack, unlike 5xx)
	site400 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte("<html>oops</html>"))
	}))
	t.Cleanup(site400.Close)
	a.SiteLinkValue = site400.URL + "/"
	_, err = a.ResultsForQuery(context.Background(), &models.TorznabQuery{QueryType: "search", IsTest: true}, false)
	if err == nil || !strings.Contains(err.Error(), "Unexpected response (not JSON)") {
		t.Fatalf("err: %v", err)
	}

	// JSON error field => upstream wraps it into OnParseError ("Parse error")
	siteErr := newAnimeBytesSite(t, map[string]animeBytesResponse{
		"/scrape.php": {Matches: 0, Error: "rate limit hit"},
	})
	a.SiteLinkValue = siteErr.srv.URL + "/"
	_, err = a.ResultsForQuery(context.Background(), &models.TorznabQuery{QueryType: "search", IsTest: true}, false)
	if err == nil || !strings.Contains(err.Error(), "Parse error") {
		t.Fatalf("err: %v", err)
	}
}

func TestAnimeBytesCleanSearchTermAndSeasons(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"Some Show 12", "Some Show"},
		{"Some Show S02E05", "Some Show"},
		{"Some Show 1x12", "Some Show"},
		{"Some Show", "Some Show"},
		{"The Movie Title The Movie", "The Movie Title"},
	}
	for _, tc := range cases {
		if got := abCleanSearchTerm(tc.in); got != tc.want {
			t.Fatalf("abCleanSearchTerm(%q) = %q want %q", tc.in, got, tc.want)
		}
	}

	season, ok := abParseSeasonFromTitles([]string{"Show Title"})
	if ok || season != 0 {
		t.Fatalf("season: %d %v", season, ok)
	}
	season, ok = abParseSeasonFromTitles([]string{"Show Title", "Show 2nd Season"})
	if !ok || season != 2 {
		t.Fatalf("season: %d %v", season, ok)
	}
	season, ok = abParseSeasonFromTitles([]string{"Show Title II"})
	if !ok || season != 2 {
		t.Fatalf("roman season: %d %v", season, ok)
	}
	season, ok = abParseSeasonFromTitles([]string{"Show Title 3"})
	if !ok || season != 3 {
		t.Fatalf("tail season: %d %v", season, ok)
	}
	// look-behind guards
	if _, ok = abParseSeasonFromTitles([]string{"Show Part 2"}); ok {
		t.Fatal("Part 2 must not match")
	}
	if _, ok = abParseSeasonFromTitles([]string{"Show#2"}); ok {
		t.Fatal("#2 must not match")
	}
	if _, ok = abParseSeasonFromTitles([]string{"Show 1/2"}); ok {
		t.Fatal("1/2 must not match")
	}
}

func TestAnimeBytesGuidHash(t *testing.T) {
	// MD5 of "Some Show" over ASCII bytes
	if got := abHashMD5("Some Show"); got != "ae5af1f98c7c6126a4294745986303c9" {
		t.Fatalf("abHashMD5: %q", got)
	}
}
