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

type norbitsSite struct {
	srv      *httptest.Server
	mu       sync.Mutex
	hits     []string
	posts    []string
	bodies   map[string]string
	cookies  map[string][]string
	statuses map[string]int
}

func newNorBitsSite(t *testing.T) *norbitsSite {
	t.Helper()
	s := &norbitsSite{
		bodies:   map[string]string{},
		cookies:  map[string][]string{},
		statuses: map[string]int{},
	}
	s.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		s.hits = append(s.hits, r.Method+" "+r.URL.RequestURI())
		if r.Method == http.MethodPost {
			_ = r.ParseForm()
			if len(r.PostForm) > 0 {
				s.posts = append(s.posts, r.PostForm.Encode())
			}
		}
		cookies := s.cookies[r.URL.Path]
		status := s.statuses[r.URL.Path]
		content, ok := s.bodies[r.URL.Path]
		s.mu.Unlock()
		if !ok {
			http.NotFound(w, r)
			return
		}
		for _, c := range cookies {
			w.Header().Add("Set-Cookie", c)
		}
		if status != 0 {
			w.WriteHeader(status)
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(content))
	}))
	t.Cleanup(s.srv.Close)
	return s
}

func (s *norbitsSite) paths() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.hits...)
}

func (s *norbitsSite) postBodies() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.posts...)
}

// loginOK wires the upstream login chain: index page cookie, login page
// simulation, takelogin.php setting uid/pass cookies.
func (s *norbitsSite) loginOK() {
	s.bodies["/"] = "index page"
	s.cookies["/"] = []string{"sid=abc123; path=/"}
	s.bodies["/login.php"] = "login page"
	s.bodies["/takelogin.php"] = "logged in"
	s.cookies["/takelogin.php"] = []string{"uid=42; path=/", "pass=xyz; path=/"}
}

const norbitsBrowsePage = `<html><body>
<table id="torrentTable"><tbody>
<tr><th><a href="logout.php">Log out</a></th></tr>
<tr>
  <td><a href="browse.php?main_cat[]=1">Filmer</a><a href="browse.php?sub2_cat[]=19">HD1080</a></td>
  <td>
    <a href="download.php?id=101">DL</a>
    <a href="details.php?id=101" title="Some Movie 2024 1080p">Some Movie</a>
    <span class="genres">( Action | Thriller )</span>
    <a href="https://www.imdb.com/title/tt1234567/"><img src="imdb.png"></a>
    <img title="100% freeleech" src="fl.png">
  </td>
  <td><a href="details.php?id=101">3</a></td>
  <td></td>
  <td>2024-03-0210:20:30</td>
  <td></td>
  <td>1,5 GB</td>
  <td>12 <a href="x">c</a></td>
  <td>25</td>
  <td>5</td>
</tr>
<tr>
  <td><a href="browse.php?main_cat[]=2">TV</a></td>
  <td>
    <a href="download.php?id=202">DL</a>
    <a href="details.php?id=202" title="Some Show S01E01">Some Show</a>
    <img title="Halfleech" src="hl.png">
  </td>
  <td><a href="details.php?id=202">7</a></td>
  <td></td>
  <td>2024-04-0112:00:00</td>
  <td></td>
  <td>500 MB</td>
  <td>3 <a href="x">c</a></td>
  <td>10</td>
  <td>2</td>
</tr>
</tbody></table>
</body></html>`

func newTestNorBits(t *testing.T, site *norbitsSite) *NorBits {
	t.Helper()
	client, err := httpclient.New(httpclient.Config{}, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("httpclient.New: %v", err)
	}
	ix, err := NewNorBits(services.Deps{Client: client})
	if err != nil {
		t.Fatalf("NewNorBits: %v", err)
	}
	n := ix.(*NorBits)
	n.SiteLinkValue = site.srv.URL + "/"
	client.SetRequestDelay(n.ID(), 0)
	return n
}

func configureNorBits(t *testing.T, n *NorBits, site *norbitsSite) {
	t.Helper()
	status, err := n.ApplyConfiguration(context.Background(), []indexer.ConfigItem{
		{ID: "sitelink", Value: site.srv.URL + "/"},
		{ID: "username", Value: "noruser"},
		{ID: "password", Value: "norpass"},
	})
	if err != nil {
		t.Fatalf("ApplyConfiguration: %v", err)
	}
	if status != indexer.ConfigRequiresTesting {
		t.Fatalf("status: %v, want ConfigRequiresTesting", status)
	}
}

func TestNorBitsCaps(t *testing.T) {
	ix, err := NewNorBits(services.Deps{})
	if err != nil {
		t.Fatalf("NewNorBits: %v", err)
	}
	caps := ix.Capabilities()
	if strings.Join(caps.TVSearchParams, ",") != "q,season,ep" {
		t.Fatalf("TVSearchParams: %v", caps.TVSearchParams)
	}
	if strings.Join(caps.MovieSearchParams, ",") != "q,imdbid" {
		t.Fatalf("MovieSearchParams: %v", caps.MovieSearchParams)
	}
	if strings.Join(caps.MusicSearchParams, ",") != "q" || strings.Join(caps.BookSearchParams, ",") != "q" {
		t.Fatalf("music/book params: %v %v", caps.MusicSearchParams, caps.BookSearchParams)
	}
	for _, tc := range []struct {
		tracker string
		want    int
	}{
		{"main_cat[]=1", 2000},
		{"main_cat[]=1&sub2_cat[]=49", 2045},
		{"main_cat[]=1&sub2_cat[]=19", 2040},
		{"main_cat[]=1&sub2_cat[]=20", 2040},
		{"main_cat[]=1&sub2_cat[]=22", 2030},
		{"main_cat[]=2", 5000},
		{"main_cat[]=2&sub2_cat[]=49", 5045},
		{"main_cat[]=2&sub2_cat[]=19", 5040},
		{"main_cat[]=2&sub2_cat[]=20", 5040},
		{"main_cat[]=2&sub2_cat[]=22", 5030},
		{"main_cat[]=3", 4000},
		{"main_cat[]=4", 1000},
		{"main_cat[]=5", 3000},
		{"main_cat[]=5&sub2_cat[]=42", 3010},
		{"main_cat[]=5&sub2_cat[]=46", 3040},
		{"main_cat[]=6", 7000},
		{"main_cat[]=7", 3030},
		{"main_cat[]=8", 3020},
		{"main_cat[]=40", 3050},
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
	if ix.Language() != "nb-NO" || ix.Type() != "private" || ix.SiteLink() != "https://norbits.net/" {
		t.Fatalf("identity: %s %s %s", ix.Language(), ix.Type(), ix.SiteLink())
	}
}

func TestNorBitsConfigForm(t *testing.T) {
	ix, err := NewNorBits(services.Deps{})
	if err != nil {
		t.Fatalf("NewNorBits: %v", err)
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
		"sitelink", "credentials", "username", "password", "2facode(optional)",
		"searchfreeleechonly", "preferences", "maxpagestoprocess(required)",
		"enablesearchindescription.", "accountinactivity", "tags",
	}
	if strings.Join(ids, ",") != strings.Join(want, ",") {
		t.Fatalf("config ids: %v", ids)
	}
}

func TestNorBitsApplyConfiguration(t *testing.T) {
	site := newNorBitsSite(t)
	site.loginOK()
	n := newTestNorBits(t, site)
	configureNorBits(t, n, site)

	if !n.IsConfigured() {
		t.Fatal("must be configured after login")
	}
	// upstream line 196: indexCookies + " " + responseCookies + " ts_username=<user>"
	wantHeader := "sid=abc123 uid=42 pass=xyz ts_username=noruser"
	if got := n.CookieHeader(); got != wantHeader {
		t.Fatalf("cookie header: %q want %q", got, wantHeader)
	}

	paths := site.paths()
	if len(paths) != 3 || paths[0] != "GET /" || paths[1] != "GET /login.php" || paths[2] != "POST /takelogin.php" {
		t.Fatalf("paths: %v", paths)
	}
	posts := site.postBodies()
	if len(posts) != 1 || posts[0] != "logout=no&password=norpass&returnto=%2F&username=noruser" {
		t.Fatalf("posts: %v", posts)
	}
}

func TestNorBitsApplyConfigurationWith2FA(t *testing.T) {
	site := newNorBitsSite(t)
	site.loginOK()
	n := newTestNorBits(t, site)
	_, err := n.ApplyConfiguration(context.Background(), []indexer.ConfigItem{
		{ID: "sitelink", Value: site.srv.URL + "/"},
		{ID: "username", Value: "noruser"},
		{ID: "password", Value: "norpass"},
		{ID: "2facode(optional)", Value: "987654"},
	})
	if err != nil {
		t.Fatalf("ApplyConfiguration: %v", err)
	}
	posts := site.postBodies()
	if len(posts) != 1 || !strings.Contains(posts[0], "code=987654") {
		t.Fatalf("posts: %v", posts)
	}
}

func TestNorBitsApplyConfigurationLoginFails(t *testing.T) {
	site := newNorBitsSite(t)
	site.bodies["/"] = "index page"
	site.cookies["/"] = []string{"sid=abc123; path=/"}
	site.bodies["/login.php"] = "login page"
	site.bodies["/takelogin.php"] = "login failed"
	// no uid cookie on the response
	n := newTestNorBits(t, site)
	_, err := n.ApplyConfiguration(context.Background(), []indexer.ConfigItem{
		{ID: "sitelink", Value: site.srv.URL + "/"},
		{ID: "username", Value: "noruser"},
		{ID: "password", Value: "norpass"},
	})
	var ie *indexer.IndexerError
	if !errors.As(err, &ie) || ie.Message != "Login failed: Error during attempt !" {
		t.Fatalf("err: %v", err)
	}
	if n.IsConfigured() {
		t.Fatal("must not be configured after failed login")
	}
}

func TestNorBitsValidateConfig(t *testing.T) {
	cases := []struct {
		name    string
		items   []indexer.ConfigItem
		message string
	}{
		{
			"missing username",
			[]indexer.ConfigItem{{ID: "sitelink", Value: "https://norbits.net/"}, {ID: "password", Value: "x"}, {ID: "maxpagestoprocess(required)", Value: "4"}},
			"You must provide a username for this tracker to login !",
		},
		{
			"missing password",
			[]indexer.ConfigItem{{ID: "sitelink", Value: "https://norbits.net/"}, {ID: "username", Value: "x"}, {ID: "maxpagestoprocess(required)", Value: "4"}},
			"You must provide a password with your username for this tracker to login !",
		},
		{
			"non-numeric pages",
			[]indexer.ConfigItem{{ID: "sitelink", Value: "https://norbits.net/"}, {ID: "username", Value: "x"}, {ID: "password", Value: "y"}, {ID: "maxpagestoprocess(required)", Value: "abc"}},
			"Please enter a numeric maximum number of pages to crawl !",
		},
		{
			"empty pages",
			[]indexer.ConfigItem{{ID: "sitelink", Value: "https://norbits.net/"}, {ID: "username", Value: "x"}, {ID: "password", Value: "y"}, {ID: "maxpagestoprocess(required)", Value: ""}},
			"Please enter a maximum number of pages to crawl !",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ix, err := NewNorBits(services.Deps{})
			if err != nil {
				t.Fatalf("NewNorBits: %v", err)
			}
			n := ix.(*NorBits)
			_, err = n.ApplyConfiguration(context.Background(), tc.items)
			var ie *indexer.IndexerError
			if !errors.As(err, &ie) || ie.Kind != indexer.KindConfig || ie.Message != tc.message {
				t.Fatalf("err: %v want KindConfig %q", err, tc.message)
			}
		})
	}
}

func TestNorBitsSearchRequestAndParse(t *testing.T) {
	site := newNorBitsSite(t)
	site.loginOK()
	site.bodies["/browse.php"] = norbitsBrowsePage
	n := newTestNorBits(t, site)
	configureNorBits(t, n, site)

	q := models.NewTorznabQuery()
	q.QueryType = "search"
	q.SearchTerm = "Some Movie"
	res, err := n.ResultsForQuery(context.Background(), q, false)
	if err != nil {
		t.Fatalf("ResultsForQuery: %v", err)
	}
	if len(res.Releases) != 2 {
		t.Fatalf("releases: %d", len(res.Releases))
	}

	r0 := res.Releases[0]
	if r0.Title != "Some Movie 2024 1080p" {
		t.Fatalf("title: %q", r0.Title)
	}
	foundCat := false
	for _, id := range r0.Category {
		if id == 2040 {
			foundCat = true
		}
	}
	if !foundCat {
		t.Fatalf("category: %v want it to contain 2040", r0.Category)
	}
	if r0.Size == nil || *r0.Size != 1610612736 {
		t.Fatalf("size: %v", r0.Size)
	}
	if r0.Files == nil || *r0.Files != 3 {
		t.Fatalf("files: %v", r0.Files)
	}
	if r0.Grabs == nil || *r0.Grabs != 12 {
		t.Fatalf("grabs: %v", r0.Grabs)
	}
	if r0.Seeders == nil || *r0.Seeders != 25 || r0.Peers == nil || *r0.Peers != 30 {
		t.Fatalf("seeders/peers: %v %v", r0.Seeders, r0.Peers)
	}
	if r0.PublishDate.Format("2006-01-02 15:04:05") != "2024-03-02 10:20:30" {
		t.Fatalf("publish date: %v", r0.PublishDate)
	}
	if r0.DownloadVolumeFactor == nil || *r0.DownloadVolumeFactor != 0 {
		t.Fatalf("dvf (100%% freeleech): %v", r0.DownloadVolumeFactor)
	}
	if r0.UploadVolumeFactor == nil || *r0.UploadVolumeFactor != 1 {
		t.Fatalf("uvf: %v", r0.UploadVolumeFactor)
	}
	if r0.MinimumRatio == nil || *r0.MinimumRatio != 1 || r0.MinimumSeedTime == nil || *r0.MinimumSeedTime != 172800 {
		t.Fatalf("minimum ratio/seed time: %v %v", r0.MinimumRatio, r0.MinimumSeedTime)
	}
	if r0.Link == nil || !strings.HasSuffix(r0.Link.Path, "/download.php") || r0.Link.Query().Get("id") != "101" {
		t.Fatalf("link: %v", r0.Link)
	}
	if r0.Details == nil || !strings.HasSuffix(r0.Details.Path, "/details.php") {
		t.Fatalf("details: %v", r0.Details)
	}
	if r0.Imdb == nil || *r0.Imdb != 1234567 {
		t.Fatalf("imdb: %v", r0.Imdb)
	}
	// "( Action | Thriller )" → parens stripped → " | " replaced by ","
	if r0.Description != " Action,Thriller " {
		t.Fatalf("description (genres): %q", r0.Description)
	}
	// upstream Union(split(',')) keeps the raw parts (spaces included)
	if strings.Join(r0.Genres, "|") != " Action|Thriller " {
		t.Fatalf("genres: %q", r0.Genres)
	}

	r1 := res.Releases[1]
	foundCat = false
	for _, id := range r1.Category {
		if id == 5000 {
			foundCat = true
		}
	}
	if !foundCat {
		t.Fatalf("category: %v want it to contain 5000", r1.Category)
	}
	if r1.DownloadVolumeFactor == nil || *r1.DownloadVolumeFactor != 0.5 {
		t.Fatalf("dvf (Halfleech): %v", r1.DownloadVolumeFactor)
	}
	if r1.Size == nil || *r1.Size != 524288000 {
		t.Fatalf("size: %v", r1.Size)
	}

	// request generation: search= + latin-1 encoding + parameters order
	paths := site.paths()
	want := "GET /browse.php?search=Some+Movie&incldead=1&fullsearch=0&scenerelease=0"
	if paths[3] != want {
		t.Fatalf("search path: %q want %q", paths[3], want)
	}
}

func TestNorBitsSearchWithCategoriesFreeleechAndFullSearch(t *testing.T) {
	site := newNorBitsSite(t)
	site.loginOK()
	site.bodies["/browse.php"] = norbitsBrowsePage
	n := newTestNorBits(t, site)
	configureNorBits(t, n, site)
	n.cfg.Freeleech.Value = true
	n.cfg.UseFullSearch.Value = true

	q := models.NewTorznabQuery()
	q.QueryType = "search"
	q.SearchTerm = "Some Movie"
	q.Categories = []int{2000}
	res, err := n.ResultsForQuery(context.Background(), q, false)
	if err != nil {
		t.Fatalf("ResultsForQuery: %v", err)
	}
	if len(res.Releases) == 0 {
		t.Fatal("no releases")
	}
	search := ""
	for _, p := range site.paths() {
		if strings.HasPrefix(p, "GET /browse.php?") {
			search = p
		}
	}
	if !strings.Contains(search, "search=Some+Movie&incldead=1&fullsearch=1&scenerelease=0&FL=1") {
		t.Fatalf("search path: %q", search)
	}
	// tracker categories are appended raw (upstream string.Join, no encoding)
	if !strings.Contains(search, "main_cat[]=1&sub2_cat[]=49") {
		t.Fatalf("category params missing: %q", search)
	}
}

func TestNorBitsSearchImdbAndDiacritics(t *testing.T) {
	site := newNorBitsSite(t)
	site.loginOK()
	site.bodies["/browse.php"] = norbitsBrowsePage
	n := newTestNorBits(t, site)
	configureNorBits(t, n, site)

	// imdb query → imdbsearch=<id>
	q := models.NewTorznabQuery()
	q.QueryType = "movie"
	q.ImdbID = "tt1234567"
	if _, err := n.ResultsForQuery(context.Background(), q, false); err != nil {
		t.Fatalf("ResultsForQuery: %v", err)
	}

	// diacritics: the exact term and the diacritic-stripped term both run
	q2 := models.NewTorznabQuery()
	q2.QueryType = "search"
	q2.SearchTerm = "café"
	res, err := n.ResultsForQuery(context.Background(), q2, false)
	if err != nil {
		t.Fatalf("ResultsForQuery: %v", err)
	}
	// both terms return the same rows; Base de-duplicates by Guid
	if len(res.Releases) != 2 {
		t.Fatalf("releases: %d want 2 (Guid de-dupe of the two term queries)", len(res.Releases))
	}

	var searches []string
	for _, p := range site.paths() {
		if strings.HasPrefix(p, "GET /browse.php?") {
			searches = append(searches, p)
		}
	}
	if len(searches) != 3 {
		t.Fatalf("searches: %v", searches)
	}
	if searches[0] != "GET /browse.php?imdbsearch=tt1234567&incldead=1&fullsearch=0&scenerelease=0" {
		t.Fatalf("imdb search: %q", searches[0])
	}
	// latin-1: é → byte 0xE9 → %E9; second query is the diacritic-free term
	if searches[1] != "GET /browse.php?search=caf%E9&incldead=1&fullsearch=0&scenerelease=0" {
		t.Fatalf("diacritic search: %q", searches[1])
	}
	if searches[2] != "GET /browse.php?search=cafe&incldead=1&fullsearch=0&scenerelease=0" {
		t.Fatalf("base search: %q", searches[2])
	}
}

func TestNorBitsSearchRetryThenUnauthenticated(t *testing.T) {
	site := newNorBitsSite(t)
	site.loginOK()
	// browse never authenticates; login retries succeed (uid is set) but the
	// retried search still lacks the "logout.php" marker
	site.bodies["/browse.php"] = `<html><body><p>please login</p></body></html>`
	n := newTestNorBits(t, site)
	configureNorBits(t, n, site)

	q := models.NewTorznabQuery()
	q.QueryType = "search"
	q.SearchTerm = "Some Movie"
	_, err := n.ResultsForQuery(context.Background(), q, false)
	if err == nil || !strings.Contains(err.Error(), "NorBits login succeeded, but the retried search is not authenticated.") {
		t.Fatalf("err: %v", err)
	}
}

func TestNorBitsParseError(t *testing.T) {
	site := newNorBitsSite(t)
	site.loginOK()
	site.bodies["/browse.php"] = `<html><body>
<table id="torrentTable"><tbody>
<tr><th><a href="logout.php">out</a></th></tr>
<tr>
  <td><a href="browse.php?main_cat[]=1">Filmer</a></td>
  <td><a href="download.php?id=101">DL</a><a href="details.php?id=101" title="Bad Date Movie">Bad Date Movie</a></td>
  <td><a href="details.php?id=101">1</a></td><td></td>
  <td>not-a-date</td><td></td><td>1 GB</td><td>0</td><td>1</td><td>0</td>
</tr>
</tbody></table></body></html>`
	n := newTestNorBits(t, site)
	configureNorBits(t, n, site)

	q := models.NewTorznabQuery()
	q.QueryType = "search"
	q.SearchTerm = "Bad Date Movie"
	_, err := n.ResultsForQuery(context.Background(), q, false)
	var ie *indexer.IndexerError
	if !errors.As(err, &ie) || ie.Kind != indexer.KindParse || ie.Message != "Parse error" {
		t.Fatalf("err: %v want KindParse \"Parse error\"", err)
	}
}

func TestNorBitsHelpers(t *testing.T) {
	// latin-1 encoding: é → %E9, ø/ñ are ≤ 0xFF and pass through as bytes
	if got := norbitsEncodeLatin1("café øño"); got != "caf%E9+%F8%F1o" {
		t.Fatalf("norbitsEncodeLatin1: %q", got)
	}
	// runes above 0xFF fall back to '?' (.NET ISO-8859-1 encoder fallback)
	if got := norbitsEncodeLatin1("日本"); got != "%3F%3F" {
		t.Fatalf("norbitsEncodeLatin1 fallback: %q", got)
	}
	// diacritic removal mirrors StringUtil.RemoveDiacritics (NFD, strip Mn)
	if got := norbitsRemoveDiacritics("café"); got != "cafe" {
		t.Fatalf("RemoveDiacritics: %q", got)
	}
	if got := norbitsRemoveDiacritics("café"); got == "café" {
		t.Fatal("expected change for accented term")
	}
	// cookie pairs: last value per name wins, attributes stripped, space-joined
	h := http.Header{}
	h.Add("Set-Cookie", "uid=1; path=/")
	h.Add("Set-Cookie", "uid=2; path=/")
	h.Add("Set-Cookie", "pass=x; HttpOnly")
	if got := setCookiePairs(h); got != "uid=2 pass=x" {
		t.Fatalf("setCookiePairs: %q", got)
	}
	if got := setCookiePairs(nil); got != "" {
		t.Fatalf("setCookiePairs(nil): %q", got)
	}
}
