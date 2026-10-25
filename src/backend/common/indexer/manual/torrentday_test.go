package manual

import (
	"context"
	"errors"
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

type torrentDaySite struct {
	srv   *httptest.Server
	mu    sync.Mutex
	hits  []string
	serve map[string]string
}

func newTorrentDaySite(t *testing.T, serve map[string]string) *torrentDaySite {
	t.Helper()
	s := &torrentDaySite{serve: serve}
	s.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		s.hits = append(s.hits, r.URL.RequestURI())
		body, ok := s.serve[r.URL.Path]
		s.mu.Unlock()
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(s.srv.Close)
	return s
}

func (s *torrentDaySite) paths() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.hits...)
}

func newTestTorrentDay(t *testing.T, site *torrentDaySite) *TorrentDay {
	t.Helper()
	client, err := httpclient.New(httpclient.Config{}, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("httpclient.New: %v", err)
	}
	ix, err := NewTorrentDay(services.Deps{Client: client})
	if err != nil {
		t.Fatalf("NewTorrentDay: %v", err)
	}
	td := ix.(*TorrentDay)
	td.SiteLinkValue = site.srv.URL + "/"
	client.SetRequestDelay(td.ID(), 0)
	return td
}

const torrentDayResultsJSON = `[
  {"name":"Some Movie 2024 1080p","t":123456,"seeders":10,"leechers":2,
   "imdb-id":"tt1234567","download-multiplier":0,"ctime":1700000000,
   "c":11,"size":2147483648,"files":5,"completed":100},
  {"name":"Another Show S01E01","t":123457,"seeders":3,"leechers":1,
   "imdb-id":"","ctime":1700000100,
   "c":7,"size":1073741824,"files":1,"completed":42}
]`

func TestTorrentDayCaps(t *testing.T) {
	ix, err := NewTorrentDay(services.Deps{})
	if err != nil {
		t.Fatalf("NewTorrentDay: %v", err)
	}
	caps := ix.Capabilities()
	wantTV := []string{models.TVParamQ, models.TVParamSeason, models.TVParamEp, models.TVParamImdbID, models.TVParamGenre}
	if strings.Join(caps.TVSearchParams, ",") != strings.Join(wantTV, ",") {
		t.Fatalf("TVSearchParams: %v", caps.TVSearchParams)
	}
	wantMovie := []string{models.MovieParamQ, models.MovieParamImdbID, models.MovieParamGenre}
	if strings.Join(caps.MovieSearchParams, ",") != strings.Join(wantMovie, ",") {
		t.Fatalf("MovieSearchParams: %v", caps.MovieSearchParams)
	}
	if len(caps.MusicSearchParams) != 1 || caps.MusicSearchParams[0] != models.MusicParamQ {
		t.Fatalf("MusicSearchParams: %v", caps.MusicSearchParams)
	}
	if len(caps.BookSearchParams) != 1 || caps.BookSearchParams[0] != models.BookParamQ {
		t.Fatalf("BookSearchParams: %v", caps.BookSearchParams)
	}
	if caps.TVSearchImdbAvailable {
		t.Fatal("TVSearchImdbAvailable must stay false (upstream does not enable it)")
	}
}

// torrentDayWantTable is the FULL category table transcribed from
// TorrentDay.cs:99-151 (id, torznab category, tracker description).
var torrentDayWantTable = []struct {
	id   string
	cat  int
	desc string
}{
	{"25", models.CatMoviesSD.ID, "Movies/480p"},
	{"96", models.CatMoviesUHD.ID, "Movie/4K"},
	{"11", models.CatMoviesBluRay.ID, "Movies/Bluray"},
	{"5", models.CatMoviesBluRay.ID, "Movies/Bluray-Full"},
	{"103", models.CatMoviesSD.ID, "Movies/Cam"},
	{"3", models.CatMoviesDVD.ID, "Movies/DVD-R"},
	{"21", models.CatMoviesSD.ID, "Movies/MP4"},
	{"22", models.CatMoviesForeign.ID, "Movies/Non-English"},
	{"13", models.CatMovies.ID, "Movies/Packs"},
	{"44", models.CatMoviesSD.ID, "Movies/SD/x264"},
	{"48", models.CatMovies.ID, "Movies/x265"},
	{"1", models.CatMoviesSD.ID, "Movies/XviD"},
	{"24", models.CatTVSD.ID, "TV/480p"},
	{"104", models.CatTVUHD.ID, "TV/4K"},
	{"32", models.CatTVHD.ID, "TV/Bluray"},
	{"31", models.CatTVSD.ID, "TV/DVD-R"},
	{"33", models.CatTVSD.ID, "TV/DVD-Rip"},
	{"46", models.CatTVSD.ID, "TV/Mobile"},
	{"82", models.CatTVForeign.ID, "TV/Non-English"},
	{"14", models.CatTV.ID, "TV/Packs"},
	{"26", models.CatTVSD.ID, "TV/SD/x264"},
	{"7", models.CatTVHD.ID, "TV/x264"},
	{"34", models.CatTVUHD.ID, "TV/x265"},
	{"2", models.CatTVSD.ID, "TV/XviD"},
	{"10", models.CatConsoleNDS.ID, "Nintendo"},
	{"4", models.CatPCGames.ID, "PC/Games"},
	{"18", models.CatConsolePS3.ID, "PS"},
	{"8", models.CatConsolePSP.ID, "PSP"},
	{"9", models.CatConsoleXBox.ID, "Xbox"},
	{"17", models.CatAudioMP3.ID, "Music/Audio"},
	{"27", models.CatAudio.ID, "Music/Flac"},
	{"23", models.CatAudioForeign.ID, "Music/Non-English"},
	{"41", models.CatAudio.ID, "Music/Packs"},
	{"16", models.CatAudioVideo.ID, "Music/Video"},
	{"29", models.CatTVAnime.ID, "Anime"},
	{"42", models.CatAudioAudiobook.ID, "Audio Books"},
	{"20", models.CatBooks.ID, "Books"},
	{"102", models.CatBooksForeign.ID, "Books/Non-English"},
	{"30", models.CatTVDocumentary.ID, "Documentary"},
	{"95", models.CatTVDocumentary.ID, "Educational"},
	{"47", models.CatOther.ID, "Fonts"},
	{"43", models.CatPCMac.ID, "Mac"},
	{"45", models.CatAudioOther.ID, "Podcast"},
	{"28", models.CatPC.ID, "Softwa/Packs"},
	{"12", models.CatPC.ID, "Software"},
	{"19", models.CatXXX.ID, "XXX/0Day"},
	{"6", models.CatXXX.ID, "XXX/Movies"},
	{"15", models.CatXXXPack.ID, "XXX/Packs"},
}

// TestTorrentDayCategoryTable is the batch-acceptance full-table
// bidirectional assertion: every row maps tracker id → torznab category (and
// the auto-created custom 1:1 category), description → torznab category, and
// torznab category → tracker id (reverse).
func TestTorrentDayCategoryTable(t *testing.T) {
	ix, err := NewTorrentDay(services.Deps{})
	if err != nil {
		t.Fatalf("NewTorrentDay: %v", err)
	}
	cats := ix.Capabilities().Categories

	// forward: tracker id → torznab
	for _, row := range torrentDayWantTable {
		got := cats.MapTrackerCatToNewznab(row.id)
		if !containsInt(got, row.cat) {
			t.Fatalf("MapTrackerCatToNewznab(%q) = %v, want it to contain %d", row.id, got, row.cat)
		}
		// every row carries a description → custom 1:1 category rides along
		if !containsInt(got, torrentdayMustAtoi(row.id)+100000) {
			t.Fatalf("MapTrackerCatToNewznab(%q) = %v, want it to contain custom %d", row.id, got, torrentdayMustAtoi(row.id)+100000)
		}
		// forward by description
		if got := cats.MapTrackerCatDescToNewznab(row.desc); !containsInt(got, row.cat) {
			t.Fatalf("MapTrackerCatDescToNewznab(%q) = %v, want it to contain %d", row.desc, got, row.cat)
		}
		// reverse: torznab → tracker id
		q := models.NewTorznabQuery()
		q.Categories = []int{row.cat}
		rev := cats.MapTorznabCapsToTrackers(q, false)
		found := false
		for _, id := range rev {
			if id == row.id {
				found = true
			}
		}
		if !found {
			t.Fatalf("MapTorznabCapsToTrackers(%d) = %v, want it to contain %q", row.cat, rev, row.id)
		}
	}

	// the full tracker category list (empty query → GetAllTrackerCategories)
	var wantIDs []string
	for _, row := range torrentDayWantTable {
		wantIDs = append(wantIDs, row.id)
	}
	got := cats.GetTrackerCategories()
	if strings.Join(got, ",") != strings.Join(wantIDs, ",") {
		t.Fatalf("GetTrackerCategories() = %v, want %v", got, wantIDs)
	}
}

func torrentdayMustAtoi(s string) int {
	n, err := strconv.Atoi(s)
	if err != nil {
		panic(err)
	}
	return n
}

func TestTorrentDayConfigForm(t *testing.T) {
	ix, err := NewTorrentDay(services.Deps{})
	if err != nil {
		t.Fatalf("NewTorrentDay: %v", err)
	}
	items, err := ix.ConfigurationForSetup(context.Background())
	if err != nil {
		t.Fatalf("ConfigurationForSetup: %v", err)
	}
	var ids []string
	for _, it := range items {
		ids = append(ids, it["id"].(string))
	}
	// ids are name-derived (upstream ConfigurationItem.ID,
	// ConfigurationData.cs:105): the AddDynamic key "freeleech"
	// (TorrentDay.cs:74) is only a lookup key, the id is "searchfreeleechonly".
	// "" is the empty-named Instructions item of ConfigurationDataCookie
	// (ConfigurationDataCookie.cs:13-22).
	want := []string{"sitelink", "cookie", "cookieinstructions", "", "searchfreeleechonly", "tags"}
	if strings.Join(ids, "|") != strings.Join(want, "|") {
		t.Fatalf("config ids: %v", ids)
	}
}

func TestTorrentDaySearchRequestAndParse(t *testing.T) {
	site := newTorrentDaySite(t, map[string]string{
		"/t.json": torrentDayResultsJSON,
	})
	td := newTestTorrentDay(t, site)

	q := models.NewTorznabQuery()
	q.QueryType = "search"
	res, err := td.ResultsForQuery(context.Background(), q, false)
	if err != nil {
		t.Fatalf("ResultsForQuery: %v", err)
	}
	if len(res.Releases) != 2 {
		t.Fatalf("releases: %d", len(res.Releases))
	}

	r := res.Releases[0]
	if r.Title != "Some Movie 2024 1080p" {
		t.Fatalf("title: %q", r.Title)
	}
	if r.Link == nil || !strings.HasSuffix(r.Link.Path, "/download.php/123456/123456.torrent") {
		t.Fatalf("link: %v", r.Link)
	}
	if r.Details == nil || !strings.HasSuffix(r.Details.String(), "/details.php?id=123456") {
		t.Fatalf("details: %v", r.Details)
	}
	if r.Guid == nil || r.Guid.String() != r.Details.String() {
		t.Fatalf("guid: %v", r.Guid)
	}
	if r.Size == nil || *r.Size != 2147483648 {
		t.Fatalf("size: %v", r.Size)
	}
	if r.Files == nil || *r.Files != 5 {
		t.Fatalf("files: %v", r.Files)
	}
	if r.Grabs == nil || *r.Grabs != 100 {
		t.Fatalf("grabs: %v", r.Grabs)
	}
	if r.Seeders == nil || *r.Seeders != 10 {
		t.Fatalf("seeders: %v", r.Seeders)
	}
	if r.Peers == nil || *r.Peers != 12 {
		t.Fatalf("peers: %v", r.Peers)
	}
	if r.Imdb == nil || *r.Imdb != 1234567 {
		t.Fatalf("imdb: %v", r.Imdb)
	}
	if r.DownloadVolumeFactor == nil || *r.DownloadVolumeFactor != 0 {
		t.Fatalf("dvf: %v", r.DownloadVolumeFactor)
	}
	if r.UploadVolumeFactor == nil || *r.UploadVolumeFactor != 1 {
		t.Fatalf("uvf: %v", r.UploadVolumeFactor)
	}
	if r.MinimumRatio == nil || *r.MinimumRatio != 1 {
		t.Fatalf("minimum ratio: %v", r.MinimumRatio)
	}
	if r.MinimumSeedTime == nil || *r.MinimumSeedTime != 259200 {
		t.Fatalf("minimum seed time: %v", r.MinimumSeedTime)
	}
	if !containsInt(r.Category, models.CatMoviesBluRay.ID) {
		t.Fatalf("category: %v", r.Category)
	}
	if r.PublishDate.Unix() != 1700000000 {
		t.Fatalf("publish date: %v", r.PublishDate)
	}

	// second row: missing download-multiplier defaults to 1
	r2 := res.Releases[1]
	if r2.DownloadVolumeFactor == nil || *r2.DownloadVolumeFactor != 1 {
		t.Fatalf("dvf row2: %v", r2.DownloadVolumeFactor)
	}
	if r2.Imdb != nil {
		t.Fatalf("imdb row2 must be nil: %v", r2.Imdb)
	}
	if !containsInt(r2.Category, models.CatTVHD.ID) {
		t.Fatalf("category row2: %v", r2.Category)
	}

	allCats := td.Caps.Categories.GetTrackerCategories()
	wantPath := "/t.json?" + strings.Join(allCats, ";") + ";q="
	paths := site.paths()
	if len(paths) != 1 || paths[0] != wantPath {
		t.Fatalf("paths: %v want %q", paths, wantPath)
	}
}

func TestTorrentDayRequestURLShapes(t *testing.T) {
	site := newTorrentDaySite(t, map[string]string{
		"/t.json": torrentDayResultsJSON,
	})
	td := newTestTorrentDay(t, site)

	// category-scoped query: MoviesSD (2030) → 25;103;21;44;1
	q := models.NewTorznabQuery()
	q.Categories = []int{models.CatMoviesSD.ID}
	if _, err := td.perform(context.Background(), q); err != nil {
		t.Fatalf("perform: %v", err)
	}

	// imdb query: raw, NOT url-encoded
	q = models.NewTorznabQuery()
	q.ImdbID = "tt1234567"
	if _, err := td.perform(context.Background(), q); err != nil {
		t.Fatalf("perform: %v", err)
	}

	// genre query: q=<query>+<genre>
	td.cfg.Freeleech.Value = true
	q = models.NewTorznabQuery()
	q.SearchTerm = "action"
	q.Genre = "comedy"
	if _, err := td.perform(context.Background(), q); err != nil {
		t.Fatalf("perform: %v", err)
	}

	got := site.paths()
	if len(got) != 3 {
		t.Fatalf("paths: %v", got)
	}
	if !strings.HasPrefix(got[0], "/t.json?25;103;21;44;1;q=") {
		t.Fatalf("category path: %q", got[0])
	}
	if !strings.HasPrefix(got[1], "/t.json?") || !strings.HasSuffix(got[1], ";q=tt1234567") {
		t.Fatalf("imdb path: %q", got[1])
	}
	if !strings.HasPrefix(got[2], "/t.json?") || !strings.HasSuffix(got[2], ";q=action+comedy;free=on") {
		t.Fatalf("genre/freeleech path: %q", got[2])
	}
}

func TestTorrentDayTitleFilter(t *testing.T) {
	site := newTorrentDaySite(t, map[string]string{
		"/t.json": torrentDayResultsJSON,
	})
	td := newTestTorrentDay(t, site)

	q := models.NewTorznabQuery()
	q.QueryType = "search"
	q.SearchTerm = "Another Show"
	res, err := td.ResultsForQuery(context.Background(), q, false)
	if err != nil {
		t.Fatalf("ResultsForQuery: %v", err)
	}
	if len(res.Releases) != 1 || res.Releases[0].Title != "Another Show S01E01" {
		t.Fatalf("releases: %+v", res.Releases)
	}
}

func TestTorrentDayNotLoggedIn(t *testing.T) {
	site := newTorrentDaySite(t, map[string]string{
		"/t.json": `<html><body>login form</body></html>`,
	})
	td := newTestTorrentDay(t, site)

	q := models.NewTorznabQuery()
	q.QueryType = "search"
	_, err := td.ResultsForQuery(context.Background(), q, false)
	if err == nil {
		t.Fatal("expected error")
	}
	var ie *indexer.IndexerError
	if !errors.As(err, &ie) || ie.Kind != indexer.KindAuth {
		t.Fatalf("err: %v", err)
	}
	if !strings.Contains(ie.Message, "not logged in") {
		t.Fatalf("message: %q", ie.Message)
	}
}

func TestTorrentDayParseError(t *testing.T) {
	site := newTorrentDaySite(t, map[string]string{
		"/t.json": `[{"name":"x"}]`,
	})
	td := newTestTorrentDay(t, site)

	q := models.NewTorznabQuery()
	q.QueryType = "search"
	_, err := td.ResultsForQuery(context.Background(), q, false)
	var ie *indexer.IndexerError
	if !errors.As(err, &ie) || ie.Kind != indexer.KindParse || ie.Message != "Parse error" {
		t.Fatalf("err: %v", err)
	}
}

func TestTorrentDayApplyConfiguration(t *testing.T) {
	site := newTorrentDaySite(t, map[string]string{
		"/t.json": torrentDayResultsJSON,
	})
	td := newTestTorrentDay(t, site)

	status, err := td.ApplyConfiguration(context.Background(), []indexer.ConfigItem{
		{ID: "sitelink", Value: site.srv.URL + "/"},
		{ID: "cookie", Value: "uid=1; pass=abc"},
	})
	if err != nil {
		t.Fatalf("ApplyConfiguration: %v", err)
	}
	if status != indexer.ConfigCompleted {
		t.Fatalf("status: %v", status)
	}
	if !td.IsConfigured() {
		t.Fatal("must be configured")
	}
	if td.CookieHeader() != "uid=1; pass=abc" {
		t.Fatalf("cookie header: %q", td.CookieHeader())
	}
}

func TestTorrentDayApplyConfigurationZeroResults(t *testing.T) {
	site := newTorrentDaySite(t, map[string]string{
		"/t.json": `[]`,
	})
	td := newTestTorrentDay(t, site)

	_, err := td.ApplyConfiguration(context.Background(), []indexer.ConfigItem{
		{ID: "sitelink", Value: site.srv.URL + "/"},
		{ID: "cookie", Value: "uid=1; pass=abc"},
	})
	if err == nil || err.Error() != "Your cookie did not work: Found 0 results in the tracker" {
		t.Fatalf("err: %v", err)
	}
	if td.IsConfigured() {
		t.Fatal("must not be configured after failure")
	}
}
