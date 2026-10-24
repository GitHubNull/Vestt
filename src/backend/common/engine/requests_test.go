package engine

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"vestt/common/httpclient"
	"vestt/common/models"
)

func mustTestClient(t *testing.T) *httpclient.Client {
	t.Helper()
	client, err := httpclient.New(httpclient.Config{}, nil, discardLogger())
	if err != nil {
		t.Fatalf("httpclient: %v", err)
	}
	return client
}

// searchDef builds a minimal definition for search-generation unit tests.
func searchDef(t *testing.T, search *SearchBlock, catMappings []CategoryMappingBlock) (*Cardigann, *loginFixture) {
	t.Helper()
	f := &loginFixture{}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.record(r)
	}))
	t.Cleanup(f.srv.Close)
	def := &IndexerDefinition{
		ID:    "searchtest",
		Name:  "Search Test",
		Type:  "private",
		Links: []string{f.srv.URL},
		Caps: &CapsBlock{
			Categorymappings: catMappings,
			Modes: StringListMap{
				{Key: "search", Values: []string{"q"}},
				{Key: "tv-search", Values: []string{"q", "season", "ep"}},
			},
		},
		Search: search,
	}
	x, err := NewCardigann(def, discardLogger())
	if err != nil {
		t.Fatalf("NewCardigann: %v", err)
	}
	x.Client = mustTestClient(t)
	return x, f
}

// TestSearchCategoryNegation covers the upstream `categories: ["!", ...]`
// path-skip logic (no shipped YAML currently exercises it).
func TestSearchCategoryNegation(t *testing.T) {
	x, _ := searchDef(t, &SearchBlock{
		Paths: []*SearchPathBlock{
			{RequestBlock: RequestBlock{Path: "browse.php"}, Categories: []string{"!", "7"}},
			{RequestBlock: RequestBlock{Path: "search.php"}, Categories: []string{"7"}},
		},
	}, []CategoryMappingBlock{
		{ID: "7", Cat: "TV"},
		{ID: "4", Cat: "Movies"},
	})

	// TV query → mapped cat 7 intersects, so the "!" path is skipped and
	// the plain categories path (7) is used.
	reqs, err := x.searchRequests(context.Background(), searchQuery("show", 5000), x.queryVariables(searchQuery("show", 5000)))
	if err != nil {
		t.Fatal(err)
	}
	if len(reqs) != 1 || !strings.HasSuffix(reqs[0].URL, "search.php") {
		t.Fatalf("TV query: got %v", reqs)
	}

	// Movie query → no intersect with 7 → the "!" path matches, the
	// categories path is skipped.
	reqs, err = x.searchRequests(context.Background(), searchQuery("movie", 2000), x.queryVariables(searchQuery("movie", 2000)))
	if err != nil {
		t.Fatal(err)
	}
	if len(reqs) != 1 || !strings.HasSuffix(reqs[0].URL, "browse.php") {
		t.Fatalf("movie query: got %v", reqs)
	}
}

// TestSearchGETDedupe verifies identical GET URLs are only requested once
// (upstream searchUrls list).
func TestSearchGETDedupe(t *testing.T) {
	x, _ := searchDef(t, &SearchBlock{
		Paths: []*SearchPathBlock{
			{RequestBlock: RequestBlock{Path: "search.php", Inputs: StringMap{{Key: "q", Value: "{{ .Keywords }}"}}}},
			{RequestBlock: RequestBlock{Path: "search.php", Inputs: StringMap{{Key: "q", Value: "{{ .Keywords }}"}}}},
		},
	}, nil)
	reqs, err := x.searchRequests(context.Background(), searchQuery("dup"), x.queryVariables(searchQuery("dup")))
	if err != nil {
		t.Fatal(err)
	}
	if len(reqs) != 1 {
		t.Fatalf("dedupe: got %d requests", len(reqs))
	}
}

// TestSearchKeywordTokens verifies the Q/Series/Movie/Year + Episode token
// order and the .Keywords variable expansion.
func TestSearchKeywordTokens(t *testing.T) {
	x, _ := searchDef(t, &SearchBlock{
		Paths: []*SearchPathBlock{
			{RequestBlock: RequestBlock{Path: "s.php", Inputs: StringMap{{Key: "q", Value: "{{ .Keywords }}"}}}},
		},
	}, nil)
	q := models.NewTorznabQuery()
	q.QueryType = "tvsearch"
	q.SearchTerm = "Show"
	q.Season = intPtr(2)
	q.Episode = "3"
	q.Year = intPtr(2020)
	reqs, err := x.searchRequests(context.Background(), q, x.queryVariables(q))
	if err != nil {
		t.Fatal(err)
	}
	// upstream token order: Q, Series, Movie, Year, then Episode last.
	want := "q=Show+2020+S02E03"
	if len(reqs) != 1 || !strings.Contains(reqs[0].URL, want) {
		t.Fatalf("URL %v does not contain %q", reqs, want)
	}
}

// TestSearchPostEncoding verifies the POST body: keys AND values percent
// encoded, pairs in definition order (incl. duplicates from $raw).
func TestSearchPostEncoding(t *testing.T) {
	x, _ := searchDef(t, &SearchBlock{
		Paths: []*SearchPathBlock{
			{
				RequestBlock: RequestBlock{
					Path:   "post.php",
					Method: "post",
					Inputs: StringMap{
						{Key: "$raw", Value: "{{ range .Categories }}cat[]={{.}}&{{end}}"},
						{Key: "q", Value: "{{ .Keywords }}"},
					},
				},
				Inheritinputs: true,
			},
		},
		Inputs: StringMap{{Key: "inherited", Value: "yes"}},
	}, []CategoryMappingBlock{
		{ID: "a b", Cat: "TV"},
		{ID: "7", Cat: "TV"},
	})
	reqs, err := x.searchRequests(context.Background(), searchQuery("x", 5000), x.queryVariables(searchQuery("x", 5000)))
	if err != nil {
		t.Fatal(err)
	}
	if len(reqs) != 1 {
		t.Fatalf("got %d requests", len(reqs))
	}
	r := reqs[0]
	if r.Method != http.MethodPost {
		t.Fatalf("method %s", r.Method)
	}
	// upstream double-encodes $raw values: the template modifier UrlEncodes
	// "a b" → "a+b", then FormUrlEncodedContentWithEncoding encodes again
	// ("+" → %2B); keys are encoded once ([] → %5B%5D).
	wantBody := "inherited=yes&cat%5B%5D=a%2Bb&cat%5B%5D=7&q=x"
	if r.RawBody != wantBody {
		t.Fatalf("body %q want %q", r.RawBody, wantBody)
	}
	if r.Headers["Content-Type"] != "application/x-www-form-urlencoded" {
		t.Fatalf("content-type %v", r.Headers)
	}
}

// TestSearchAllowEmptyInputs verifies empty inputs are dropped unless the
// search sets allowemptyinputs.
func TestSearchAllowEmptyInputs(t *testing.T) {
	mk := func(allow bool) []searchRequest {
		x, _ := searchDef(t, &SearchBlock{
			AllowEmptyInputs: allow,
			Paths: []*SearchPathBlock{
				{RequestBlock: RequestBlock{Path: "s.php", Inputs: StringMap{
					{Key: "q", Value: "{{ .Keywords }}"},
					{Key: "fl", Value: "{{ .Config.freeleech }}"},
				}}},
			},
		}, nil)
		reqs, err := x.searchRequests(context.Background(), searchQuery(""), x.queryVariables(searchQuery("")))
		if err != nil {
			t.Fatal(err)
		}
		return reqs
	}
	if u := mk(false)[0].URL; strings.Contains(u, "fl=") {
		t.Fatalf("empty input must be dropped: %s", u)
	}
	if u := mk(true)[0].URL; !strings.Contains(u, "fl=") {
		t.Fatalf("allowemptyinputs must keep empty input: %s", u)
	}
}

// TestSearchWindows1251Encoding verifies definition-encoding transcoding of
// query values (upstream Encoding.GetBytes + UrlEncode).
func TestSearchWindows1251Encoding(t *testing.T) {
	x, _ := searchDef(t, &SearchBlock{
		Paths: []*SearchPathBlock{
			{RequestBlock: RequestBlock{Path: "s.php", Inputs: StringMap{{Key: "q", Value: "{{ .Keywords }}"}}}},
		},
	}, nil)
	x.EncodingName = "windows-1251"
	reqs, err := x.searchRequests(context.Background(), searchQuery("сезон"), x.queryVariables(searchQuery("сезон")))
	if err != nil {
		t.Fatal(err)
	}
	// "сезон" in windows-1251
	if !strings.Contains(reqs[0].URL, "q=%F1%E5%E7%EE%ED") {
		t.Fatalf("URL %s", reqs[0].URL)
	}
}

// TestHandleRequestQueryseparator verifies the download-before querystring
// separator (no shipped YAML uses queryseparator today).
func TestHandleRequestQueryseparator(t *testing.T) {
	var gotQuery string
	f := newLoginFixture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		fmt.Fprint(w, "ok")
	}))
	def := &IndexerDefinition{
		ID:     "dlsep",
		Name:   "DL Sep",
		Links:  []string{f.srv.URL},
		Caps:   &CapsBlock{Modes: StringListMap{{Key: "search", Values: []string{"q"}}}},
		Search: &SearchBlock{Path: "s"},
	}
	x, err := NewCardigann(def, discardLogger())
	if err != nil {
		t.Fatal(err)
	}
	x.Client = mustTestClient(t)

	res, err := x.handleRequestAsync(context.Background(), &RequestBlock{
		Path:           "before.php",
		Inputs:         StringMap{{Key: "a", Value: "1"}, {Key: "b", Value: "2"}},
		Queryseparator: ";",
	}, x.GetBaseTemplateVariables(), "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status %d", res.StatusCode)
	}
	if gotQuery != "a=1;b=2" {
		t.Fatalf("query %q", gotQuery)
	}
}

// --- download request generation -------------------------------------------

func downloadDef(t *testing.T, srvURL string, dl *DownloadBlock) *Cardigann {
	t.Helper()
	def := &IndexerDefinition{
		ID:       "dltest",
		Name:     "DL Test",
		Links:    []string{srvURL},
		Caps:     &CapsBlock{Modes: StringListMap{{Key: "search", Values: []string{"q"}}}},
		Search:   &SearchBlock{Path: "s"},
		Download: dl,
	}
	x, err := NewCardigann(def, discardLogger())
	if err != nil {
		t.Fatal(err)
	}
	x.Client = mustTestClient(t)
	return x
}

func TestDownloadViaSelector(t *testing.T) {
	f := newLoginFixture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/tor/1":
			w.Header().Set("Content-Type", "text/html")
			fmt.Fprint(w, `<html><body><a id="dl" href="/file.torrent">x</a></body></html>`)
		case "/file.torrent":
			w.Write([]byte("d8:announce4:fake"))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	x := downloadDef(t, f.srv.URL, &DownloadBlock{
		Selectors: []*SelectorField{{Selector: "#dl", Attribute: "href"}},
	})
	link, _ := url.Parse(f.srv.URL + "/tor/1")
	data, err := x.Download(context.Background(), link)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "d8:announce4:fake" {
		t.Fatalf("data %q", data)
	}
}

// TestDownloadTestlinktorrent verifies the first-byte 'd' check: the first
// selector yields an invalid torrent, the second (distinct) selector is used.
// Upstream re-matches each selector from scratch, so the selectors must be
// distinct for the retry to reach a different link.
func TestDownloadTestlinktorrent(t *testing.T) {
	f := newLoginFixture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/page":
			fmt.Fprint(w, `<html><body>
				<a id="bad" href="/bad">b</a>
				<a id="good" href="/good">g</a>
			</body></html>`)
		case "/bad":
			w.Write([]byte("x-not-a-torrent"))
		case "/good":
			w.Write([]byte("d4:fake"))
		}
	}))
	def := &IndexerDefinition{
		ID:              "dltorrent",
		Name:            "DL Torrent",
		Testlinktorrent: true,
		Links:           []string{f.srv.URL},
		Caps:            &CapsBlock{Modes: StringListMap{{Key: "search", Values: []string{"q"}}}},
		Search:          &SearchBlock{Path: "s"},
		Download: &DownloadBlock{
			Selectors: []*SelectorField{
				{Selector: "#bad", Attribute: "href"},
				{Selector: "#good", Attribute: "href"},
			},
		},
	}
	x, err := NewCardigann(def, discardLogger())
	if err != nil {
		t.Fatal(err)
	}
	x.Client = mustTestClient(t)

	link, _ := url.Parse(f.srv.URL + "/page")
	data, err := x.Download(context.Background(), link)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "d4:fake" {
		t.Fatalf("data %q", data)
	}
}

// TestDownloadInfohash verifies hash/title selectors produce a public magnet.
func TestDownloadInfohash(t *testing.T) {
	f := newLoginFixture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `<html><body>
			<div id="hash">ABCDEF0123456789ABCDEF0123456789ABCDEF01</div>
			<div id="title">Some Release 1080p</div>
		</body></html>`)
	}))
	x := downloadDef(t, f.srv.URL, &DownloadBlock{
		Infohash: &InfohashBlock{
			Hash:  &SelectorField{Selector: "#hash"},
			Title: &SelectorField{Selector: "#title"},
		},
	})
	link, _ := url.Parse(f.srv.URL + "/details/1")
	data, err := x.Download(context.Background(), link)
	if err != nil {
		t.Fatal(err)
	}
	got := string(data)
	if !strings.HasPrefix(got, "magnet:?xt=urn:btih:ABCDEF0123456789ABCDEF0123456789ABCDEF01&dn=Some+Release+1080p&tr=") {
		t.Fatalf("magnet %q", got)
	}
	if !strings.Contains(got, "tracker.opentrackr.org%3A1337%2Fannounce") {
		t.Fatalf("trackers missing in %q", got)
	}
}

// TestDownloadBeforePathselector verifies the before-block path extraction
// from the link page: the before response (carrying the real download link)
// is reused via usebeforeresponse.
func TestDownloadBeforePathselector(t *testing.T) {
	f := newLoginFixture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/details/1":
			fmt.Fprint(w, `<html><body><form id="dlform" action="/take/9"></form></body></html>`)
		case "/take/9":
			fmt.Fprint(w, `<html><body><a id="real" href="/file.torrent">dl</a></body></html>`)
		case "/file.torrent":
			w.Write([]byte("d4:fake"))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	x := downloadDef(t, f.srv.URL, &DownloadBlock{
		Before: &BeforeBlock{
			PathSelector: &SelectorField{Selector: "#dlform", Attribute: "action"},
			RequestBlock: RequestBlock{Path: ""}, // set from the selector
		},
		Selectors: []*SelectorField{
			{Selector: "#real", Attribute: "href", Usebeforeresponse: true},
		},
	})
	link, _ := url.Parse(f.srv.URL + "/details/1")
	data, err := x.Download(context.Background(), link)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "d4:fake" {
		t.Fatalf("data %q", data)
	}
}

// TestDownloadSelectorMismatch verifies the "didn't match" error when no
// selector hits.
func TestDownloadSelectorMismatch(t *testing.T) {
	f := newLoginFixture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `<html><body><p>nothing here</p></body></html>`)
	}))
	x := downloadDef(t, f.srv.URL, &DownloadBlock{
		Selectors: []*SelectorField{{Selector: "#missing", Attribute: "href"}},
	})
	link, _ := url.Parse(f.srv.URL + "/page")
	if _, err := x.Download(context.Background(), link); err == nil ||
		!strings.Contains(err.Error(), "Download selectors didn't match") {
		t.Fatalf("err %v", err)
	}
}
