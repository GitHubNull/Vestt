// Row-parsing matrix (SPEC-06 acceptance #2): httptest fake sites + fixture
// pages covering text/attribute/remove/case/optional/defaults/row filters/
// andmatch/dateheaders/after/preprocessing/error blocks/limit, plus JSON
// rows (count, attribute, multiple, missing-attribute) and XML responses.
package engine

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"vestt/common/models"
)

// rowsSite serves one static body per path.
func rowsSite(t *testing.T, pages map[string]string) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, ok := pages[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, body)
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

func rowsIndexer(t *testing.T, site string, search *SearchBlock, catMappings []CategoryMappingBlock) *Cardigann {
	t.Helper()
	def := &IndexerDefinition{
		ID:    "rowstest",
		Name:  "Rows Test",
		Type:  "private",
		Links: []string{site},
		Caps: &CapsBlock{
			Categorymappings: catMappings,
			Modes: StringListMap{
				{Key: "search", Values: []string{"q"}},
				{Key: "tv-search", Values: []string{"q", "season", "ep"}},
				{Key: "movie-search", Values: []string{"q"}},
			},
		},
		Search: search,
	}
	x, err := NewCardigann(def, discardLogger())
	if err != nil {
		t.Fatalf("NewCardigann: %v", err)
	}
	x.Client = mustTestClient(t)
	return x
}

const rowsFixtureHTML = `<html><body>
<table id="torrents">
  <tr class="group"><td>24.05.2023</td></tr>
  <tr class="row">
    <td class="cat"><a href="/browse?c=7">TV</a></td>
    <td class="title"><a class="details" href="/details/100">Some Show S01E01 1080p</a><span class="flag">[free]</span></td>
    <td class="size">1.5 GB</td>
    <td class="seeds">15</td>
    <td class="leech">3</td>
    <td class="imdb"><a href="https://www.imdb.com/title/tt1234567/">i</a></td>
    <td class="dl"><a href="/download/100">DL</a></td>
  </tr>
  <tr class="group"><td>23.05.2023</td></tr>
  <tr class="row">
    <td class="cat"><a href="/browse?c=4">Movies</a></td>
    <td class="title"><a class="details" href="/details/200">Unrelated Documentary</a></td>
    <td class="size">700 MB</td>
    <td class="seeds">6000000</td>
    <td class="leech">2</td>
    <td class="dl"><a href="/download/200">DL</a></td>
  </tr>
</table></body></html>`

func rowsTestSearch(extra ...*SearchPathBlock) *SearchBlock {
	fields := []FieldBlock{
		{Name: "category", Selector: &SelectorBlock{
			Selector: "td.cat a", Attribute: "href",
			Filters: []FilterBlock{{Name: "querystring", Args: "c"}},
		}},
		// remove: strip the flag span before taking the text
		{Name: "title", Selector: &SelectorBlock{Selector: "td.title", Remove: "span.flag"}},
		{Name: "details", Selector: &SelectorBlock{Selector: "td.title a.details", Attribute: "href"}},
		{Name: "size", Selector: &SelectorBlock{Selector: "td.size"}},
		{Name: "seeders", Selector: &SelectorBlock{Selector: "td.seeds"}},
		{Name: "leechers", Selector: &SelectorBlock{Selector: "td.leech"}},
		{Name: "download", Selector: &SelectorBlock{Selector: "td.dl a", Attribute: "href"}},
		// optional: absent on the second fixture row
		{Name: "imdb", Selector: &SelectorBlock{Selector: "td.imdb a", Attribute: "href", Optional: true}},
	}
	paths := extra
	if len(paths) == 0 {
		paths = []*SearchPathBlock{{
			RequestBlock:  RequestBlock{Path: "browse"},
			Inheritinputs: true,
		}}
	}
	return &SearchBlock{
		Inputs: StringMap{{Key: "q", Value: "{{ .Keywords }}"}},
		Paths:  paths,
		Rows: RowsBlock{
			SelectorBlock: SelectorBlock{Selector: "tr.row"},
			Dateheaders:   &SelectorBlock{Selector: "tr.group td"},
		},
		Fields: fields,
	}
}

func rowsTestCats() []CategoryMappingBlock {
	return []CategoryMappingBlock{
		{ID: "7", Cat: "TV", Desc: "TV Shows"},
		{ID: "4", Cat: "Movies", Desc: "Movies"},
	}
}

func queryRows(t *testing.T, x *Cardigann, term string, cats ...int) []*models.ReleaseInfo {
	t.Helper()
	rels, err := x.performQuery(context.Background(), searchQuery(term, cats...))
	if err != nil {
		t.Fatalf("performQuery: %v", err)
	}
	return rels
}

func TestRowsHTMLMatrix(t *testing.T) {
	site := rowsSite(t, map[string]string{"/browse": rowsFixtureHTML})
	x := rowsIndexer(t, site, rowsTestSearch(), rowsTestCats())
	rels := queryRows(t, x, "show", 5000)
	if len(rels) != 2 {
		t.Fatalf("got %d releases, want 2", len(rels))
	}

	r0 := rels[0]
	// text + remove: the flag span is stripped
	if r0.Title != "Some Show S01E01 1080p" {
		t.Errorf("title = %q", r0.Title)
	}
	// attribute + querystring filter + category mapping (7 → TV 5000 plus
	// the upstream custom-desc category 100007)
	if len(r0.Category) != 2 || r0.Category[0] != 5000 || r0.Category[1] != 100007 {
		t.Errorf("category = %v", r0.Category)
	}
	// attribute resolved against the search URL
	if r0.Details == nil || r0.Details.String() != site+"/details/100" {
		t.Errorf("details = %v", r0.Details)
	}
	if r0.Link == nil || r0.Link.String() != site+"/download/100" {
		t.Errorf("link = %v", r0.Link)
	}
	if r0.Size == nil || *r0.Size != 1610612736 {
		t.Errorf("size = %v", r0.Size)
	}
	if r0.Seeders == nil || *r0.Seeders != 15 {
		t.Errorf("seeders = %v", r0.Seeders)
	}
	// peers linkage: seeders(15) + leechers(3)
	if r0.Peers == nil || *r0.Peers != 18 {
		t.Errorf("peers = %v", r0.Peers)
	}
	// imdb optional field: tt prefix stripped
	if r0.Imdb == nil || *r0.Imdb != 1234567 {
		t.Errorf("imdb = %v", r0.Imdb)
	}
	// dateheaders: row inherits the date of the preceding group row
	if r0.PublishDate.Year() != 2023 || int(r0.PublishDate.Month()) != 5 || r0.PublishDate.Day() != 24 {
		t.Errorf("publish date = %v", r0.PublishDate)
	}

	r1 := rels[1]
	// optional selector missing on this row → field skipped, no error
	if r1.Imdb != nil {
		t.Errorf("imdb = %v, want nil", r1.Imdb)
	}
	// seeders clamped at >= 5000000, peers = 0 + 2
	if r1.Seeders == nil || *r1.Seeders != 0 {
		t.Errorf("seeders = %v, want 0 (clamped)", r1.Seeders)
	}
	if r1.Peers == nil || *r1.Peers != 2 {
		t.Errorf("peers = %v, want 2", r1.Peers)
	}
	if len(r1.Category) != 2 || r1.Category[0] != 2000 || r1.Category[1] != 100004 {
		t.Errorf("category = %v", r1.Category)
	}
	if r1.PublishDate.Day() != 23 {
		t.Errorf("publish date = %v, want day 23", r1.PublishDate)
	}
}

func TestRowsTextShortcut(t *testing.T) {
	site := rowsSite(t, map[string]string{"/browse": rowsFixtureHTML})
	search := rowsTestSearch()
	search.Fields = append(search.Fields, FieldBlock{
		Name: "description", Selector: &SelectorBlock{Text: "static: {{ .Query.Q }}"},
	})
	x := rowsIndexer(t, site, search, rowsTestCats())
	rels := queryRows(t, x, "hello", 5000)
	if rels[0].Description != "static: hello" {
		t.Errorf("description = %q", rels[0].Description)
	}
}

func TestRowsOptionalDefault(t *testing.T) {
	site := rowsSite(t, map[string]string{"/browse": rowsFixtureHTML})
	search := rowsTestSearch()
	search.Fields = append(search.Fields, FieldBlock{
		Name:     "downloadvolumefactor",
		Selector: &SelectorBlock{Selector: "td.doesnotexist", Optional: true, Default: "0"},
	})
	x := rowsIndexer(t, site, search, rowsTestCats())
	rels := queryRows(t, x, "show", 5000)
	if rels[0].DownloadVolumeFactor == nil || *rels[0].DownloadVolumeFactor != 0 {
		t.Errorf("dvf = %v, want 0 from default", rels[0].DownloadVolumeFactor)
	}
	if rels[1].DownloadVolumeFactor == nil || *rels[1].DownloadVolumeFactor != 0 {
		t.Errorf("dvf = %v, want 0 from default", rels[1].DownloadVolumeFactor)
	}
}

func TestRowsNonOptionalMissingDropsRow(t *testing.T) {
	site := rowsSite(t, map[string]string{"/browse": rowsFixtureHTML})
	search := rowsTestSearch()
	// non-optional selector that only matches the first row
	search.Fields = append(search.Fields, FieldBlock{
		Name: "grabs", Selector: &SelectorBlock{Selector: "td.imdb a"},
	})
	x := rowsIndexer(t, site, search, rowsTestCats())
	rels := queryRows(t, x, "show", 5000)
	// upstream: per-row catch logs and continues → second row dropped
	if len(rels) != 1 || !strings.Contains(rels[0].Title, "Some Show") {
		t.Fatalf("got %d releases: %+v", len(rels), rels)
	}
}

func TestRowsAndmatch(t *testing.T) {
	site := rowsSite(t, map[string]string{"/browse": rowsFixtureHTML})
	search := rowsTestSearch()
	search.Rows.Filters = []FilterBlock{{Name: "andmatch", Args: "50"}}
	x := rowsIndexer(t, site, search, rowsTestCats())

	// both tokens must appear in the title
	rels := queryRows(t, x, "Some Show", 5000)
	if len(rels) != 1 || !strings.Contains(rels[0].Title, "Some Show") {
		t.Fatalf("andmatch kept %v", rels)
	}
	// single-token query matches both titles
	rels = queryRows(t, x, "e", 5000)
	if len(rels) != 2 {
		t.Fatalf("single token: got %d", len(rels))
	}
}

func TestRowsAndmatchSkippedForImdbSearch(t *testing.T) {
	site := rowsSite(t, map[string]string{"/browse": rowsFixtureHTML})
	search := rowsTestSearch()
	search.Rows.Filters = []FilterBlock{{Name: "andmatch", Args: "50"}}
	def := &IndexerDefinition{
		ID: "rowstest", Name: "Rows Test", Type: "private", Links: []string{site},
		Caps: &CapsBlock{
			Categorymappings: rowsTestCats(),
			Modes: StringListMap{
				{Key: "search", Values: []string{"q"}},
				{Key: "movie-search", Values: []string{"q", "imdbid"}},
			},
			Allowtvsearchimdb: true,
		},
		Search: search,
	}
	x, err := NewCardigann(def, discardLogger())
	if err != nil {
		t.Fatal(err)
	}
	x.Client = mustTestClient(t)

	// imdb-only query: andmatch is skipped (title would not match "tt1234567")
	q := searchQuery("", 2000)
	q.ImdbID = "tt1234567"
	rels, err := x.performQuery(context.Background(), q)
	if err != nil {
		t.Fatal(err)
	}
	if len(rels) != 2 {
		t.Fatalf("imdb search: andmatch should be skipped, got %d releases", len(rels))
	}
}

func TestRowsErrorblock(t *testing.T) {
	site := rowsSite(t, map[string]string{
		"/browse": `<html><body><div class="error">Site is broken</div></body></html>`,
	})
	search := rowsTestSearch()
	search.Error = []ErrorBlock{{Selector: "div.error"}}
	x := rowsIndexer(t, site, search, rowsTestCats())
	_, err := x.performQuery(context.Background(), searchQuery("show", 5000))
	if err == nil || !strings.Contains(err.Error(), "Site is broken") {
		t.Fatalf("err = %v", err)
	}
}

func TestRowsErrorblockMessageSelector(t *testing.T) {
	site := rowsSite(t, map[string]string{
		"/browse": `<html><body><div class="error"><span class="msg">bad thing</span></div></body></html>`,
	})
	search := rowsTestSearch()
	search.Error = []ErrorBlock{{Selector: "div.error", Message: &SelectorBlock{Selector: "span.msg"}}}
	x := rowsIndexer(t, site, search, rowsTestCats())
	_, err := x.performQuery(context.Background(), searchQuery("show", 5000))
	if err == nil || !strings.Contains(err.Error(), "bad thing") {
		t.Fatalf("err = %v", err)
	}
}

func TestRowsPreprocessingfilters(t *testing.T) {
	site := rowsSite(t, map[string]string{
		"/browse": `<html><body><table><tr class="row"><td class="t">Hello&nbsp;World</td></tr></table></body></html>`,
	})
	search := rowsTestSearch()
	search.Preprocessingfilters = []FilterBlock{{Name: "replace", Args: []any{"&nbsp;", " "}}}
	search.Fields = []FieldBlock{{Name: "title", Selector: &SelectorBlock{Selector: "td.t"}}}
	search.Rows = RowsBlock{SelectorBlock: SelectorBlock{Selector: "tr.row"}}
	x := rowsIndexer(t, site, search, rowsTestCats())
	rels := queryRows(t, x, "show", 5000)
	if rels[0].Title != "Hello World" {
		t.Errorf("title = %q", rels[0].Title)
	}
}

func TestRowsAfterMerge(t *testing.T) {
	site := rowsSite(t, map[string]string{
		"/browse": `<html><body><table>
<tr class="top"><td class="title">Alpha Release</td></tr>
<tr class="sub"><td class="meta">1.2 GB <a href="/dl/1">download</a></td></tr>
<tr class="top"><td class="title">Beta Release</td></tr>
<tr class="sub"><td class="meta">3.4 GB <a href="/dl/2">download</a></td></tr>
</table></body></html>`,
	})
	search := &SearchBlock{
		Paths: []*SearchPathBlock{{RequestBlock: RequestBlock{Path: "browse"}, Inheritinputs: true}},
		Rows: RowsBlock{
			// every row matches; After merges each pair (upstream semantics)
			SelectorBlock: SelectorBlock{Selector: "tr"},
			After:         1,
		},
		Fields: []FieldBlock{
			{Name: "title", Selector: &SelectorBlock{Selector: "td.title"}},
			{Name: "size", Selector: &SelectorBlock{Selector: "td.meta", Filters: []FilterBlock{
				{Name: "regexp", Args: `([\d.]+ GB)`},
			}}},
			{Name: "download", Selector: &SelectorBlock{Selector: "a", Attribute: "href"}},
		},
	}
	x := rowsIndexer(t, site, search, rowsTestCats())
	rels := queryRows(t, x, "show", 5000)
	if len(rels) != 2 {
		t.Fatalf("got %d releases", len(rels))
	}
	if rels[0].Title != "Alpha Release" || rels[0].Link == nil ||
		!strings.HasSuffix(rels[0].Link.String(), "/dl/1") {
		t.Errorf("row0 = %+v", rels[0])
	}
	if rels[1].Title != "Beta Release" || !strings.HasSuffix(rels[1].Link.String(), "/dl/2") {
		t.Errorf("row1 = %+v", rels[1])
	}
	if rels[0].Size == nil || *rels[0].Size != 1288490188 { // 1.2 GB
		t.Errorf("size = %v", rels[0].Size)
	}
}

func TestRowsCaseSelector(t *testing.T) {
	site := rowsSite(t, map[string]string{
		"/browse": `<html><body><table>
<tr class="row"><td class="t">Alpha</td><td><span class="st free">FREE</span></td><td class="dl"><a href="/dl/1">x</a></td></tr>
<tr class="row"><td class="t">Beta</td><td><span class="st half">Half</span></td><td class="dl"><a href="/dl/2">x</a></td></tr>
</table></body></html>`,
	})
	search := &SearchBlock{
		Paths: []*SearchPathBlock{{RequestBlock: RequestBlock{Path: "browse"}, Inheritinputs: true}},
		Rows:  RowsBlock{SelectorBlock: SelectorBlock{Selector: "tr.row"}},
		Fields: []FieldBlock{
			{Name: "title", Selector: &SelectorBlock{Selector: "td.t"}},
			{Name: "downloadvolumefactor", Selector: &SelectorBlock{
				Selector: "span.st",
				Case: StringMap{
					{Key: ".free", Value: "0"},
					{Key: "*", Value: "1"},
				},
			}},
			{Name: "download", Selector: &SelectorBlock{Selector: "td.dl a", Attribute: "href"}},
		},
	}
	x := rowsIndexer(t, site, search, rowsTestCats())
	rels := queryRows(t, x, "show", 5000)
	if len(rels) != 2 {
		t.Fatalf("got %d", len(rels))
	}
	if rels[0].DownloadVolumeFactor == nil || *rels[0].DownloadVolumeFactor != 0 {
		t.Errorf("row0 dvf = %v", rels[0].DownloadVolumeFactor)
	}
	if rels[1].DownloadVolumeFactor == nil || *rels[1].DownloadVolumeFactor != 1 {
		t.Errorf("row1 dvf = %v", rels[1].DownloadVolumeFactor)
	}
}

func TestRowsMagnetField(t *testing.T) {
	magnet := "magnet:?xt=urn:btih:ABCDEF0123456789ABCDEF0123456789ABCDEF01&dn=Alpha"
	site := rowsSite(t, map[string]string{
		"/browse": `<html><body><table>
<tr class="row"><td class="t">Alpha</td><td class="dl"><a href="` + magnet + `">m</a></td></tr>
</table></body></html>`,
	})
	search := &SearchBlock{
		Paths: []*SearchPathBlock{{RequestBlock: RequestBlock{Path: "browse"}, Inheritinputs: true}},
		Rows:  RowsBlock{SelectorBlock: SelectorBlock{Selector: "tr.row"}},
		Fields: []FieldBlock{
			{Name: "title", Selector: &SelectorBlock{Selector: "td.t"}},
			{Name: "download", Selector: &SelectorBlock{Selector: "td.dl a", Attribute: "href"}},
		},
	}
	x := rowsIndexer(t, site, search, rowsTestCats())
	rels := queryRows(t, x, "show", 5000)
	if rels[0].MagnetUri == nil || !strings.HasPrefix(rels[0].MagnetUri.String(), "magnet:?xt=") {
		t.Errorf("magnet = %v", rels[0].MagnetUri)
	}
	if rels[0].Link != nil {
		t.Errorf("link = %v, want nil for magnet download", rels[0].Link)
	}
}

func TestRowsLimit(t *testing.T) {
	site := rowsSite(t, map[string]string{"/browse": rowsFixtureHTML})
	x := rowsIndexer(t, site, rowsTestSearch(), rowsTestCats())
	q := searchQuery("show", 5000)
	q.Limit = 1
	rels, err := x.performQuery(context.Background(), q)
	if err != nil {
		t.Fatal(err)
	}
	if len(rels) != 1 {
		t.Fatalf("got %d, want 1 (limit)", len(rels))
	}
}

func TestRowsMultiPathMergeAndDedup(t *testing.T) {
	rowTmpl := `<tr class="row"><td class="t">%s</td><td class="cat">7</td><td class="s">1 GB</td><td class="dl"><a href="%s">x</a></td></tr>`
	site := rowsSite(t, map[string]string{
		"/a": `<html><body><table>` +
			fmt.Sprintf(rowTmpl, "Alpha", "/dl/1") +
			fmt.Sprintf(rowTmpl, "Shared", "/dl/9") +
			`</table></body></html>`,
		"/b": `<html><body><table>` +
			fmt.Sprintf(rowTmpl, "Shared", "/dl/9") +
			fmt.Sprintf(rowTmpl, "Beta", "/dl/2") +
			`</table></body></html>`,
	})
	search := &SearchBlock{
		Paths: []*SearchPathBlock{
			{RequestBlock: RequestBlock{Path: "a"}, Inheritinputs: true},
			{RequestBlock: RequestBlock{Path: "b"}, Inheritinputs: true},
		},
		Rows: RowsBlock{SelectorBlock: SelectorBlock{Selector: "tr.row"}},
		Fields: []FieldBlock{
			{Name: "title", Selector: &SelectorBlock{Selector: "td.t"}},
			{Name: "category", Selector: &SelectorBlock{Selector: "td.cat"}},
			{Name: "size", Selector: &SelectorBlock{Selector: "td.s"}},
			{Name: "download", Selector: &SelectorBlock{Selector: "td.dl a", Attribute: "href"}},
		},
	}
	x := rowsIndexer(t, site, search, rowsTestCats())

	// pipeline level: both paths contribute → 4 raw releases
	raw, err := x.performQuery(context.Background(), searchQuery("show", 5000))
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) != 4 {
		t.Fatalf("performQuery merged %d, want 4", len(raw))
	}

	// ResultsForQuery level: Base de-dupes the shared link → 3 releases
	res, err := x.ResultsForQuery(context.Background(), searchQuery("show", 5000), false)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Releases) != 3 {
		t.Fatalf("deduped = %d, want 3", len(res.Releases))
	}
}

const rowsFixtureJSON = `{
  "result": {
    "total": 2,
    "items": [
      {"id": "1", "name": "Alpha Release", "size": "2 GB", "seeders": 10, "leechers": 1,
       "download": "/dl/1", "cat": "7", "imdb": "tt1234567"},
      {"id": "2", "name": "Beta Release", "size": "3 GB", "seeders": 20, "leechers": 2,
       "download": "/dl/2", "cat": "4"}
    ]
  }
}`

func jsonTestSearch() *SearchBlock {
	return &SearchBlock{
		Paths: []*SearchPathBlock{{
			RequestBlock:  RequestBlock{Path: "api"},
			Inheritinputs: true,
			Response:      &ResponseBlock{Type: "json"},
		}},
		Inputs: StringMap{{Key: "q", Value: "{{ .Keywords }}"}},
		Rows: RowsBlock{
			SelectorBlock: SelectorBlock{Selector: "result.items"},
			Count:         &SelectorBlock{Selector: "result.total"},
		},
		Fields: []FieldBlock{
			{Name: "title", Selector: &SelectorBlock{Selector: "name"}},
			{Name: "size", Selector: &SelectorBlock{Selector: "size"}},
			{Name: "seeders", Selector: &SelectorBlock{Selector: "seeders"}},
			{Name: "leechers", Selector: &SelectorBlock{Selector: "leechers"}},
			{Name: "category", Selector: &SelectorBlock{Selector: "cat"}},
			{Name: "download", Selector: &SelectorBlock{Selector: "download"}},
			{Name: "imdb", Selector: &SelectorBlock{Selector: "imdb", Optional: true}},
		},
	}
}

func TestRowsJSONBasic(t *testing.T) {
	site := rowsSite(t, map[string]string{"/api": rowsFixtureJSON})
	x := rowsIndexer(t, site, jsonTestSearch(), rowsTestCats())
	rels := queryRows(t, x, "show", 5000)
	if len(rels) != 2 {
		t.Fatalf("got %d", len(rels))
	}
	r0 := rels[0]
	if r0.Title != "Alpha Release" {
		t.Errorf("title = %q", r0.Title)
	}
	if r0.Size == nil || *r0.Size != 2147483648 {
		t.Errorf("size = %v", r0.Size)
	}
	if r0.Seeders == nil || *r0.Seeders != 10 || r0.Peers == nil || *r0.Peers != 11 {
		t.Errorf("seeders/peers = %v/%v", r0.Seeders, r0.Peers)
	}
	if len(r0.Category) != 2 || r0.Category[0] != 5000 || r0.Category[1] != 100007 {
		t.Errorf("category = %v", r0.Category)
	}
	if r0.Link == nil || !strings.HasSuffix(r0.Link.String(), "/dl/1") {
		t.Errorf("link = %v", r0.Link)
	}
	if r0.Imdb == nil || *r0.Imdb != 1234567 {
		t.Errorf("imdb = %v", r0.Imdb)
	}
	if rels[1].Imdb != nil {
		t.Errorf("row1 imdb = %v, want nil", rels[1].Imdb)
	}
}

func TestRowsJSONCountZero(t *testing.T) {
	site := rowsSite(t, map[string]string{"/api": `{"result":{"total":0,"items":[{"name":"x"}]}}`})
	x := rowsIndexer(t, site, jsonTestSearch(), rowsTestCats())
	rels, err := x.performQuery(context.Background(), searchQuery("show", 5000))
	if err != nil {
		t.Fatal(err)
	}
	if len(rels) != 0 {
		t.Fatalf("got %d, want 0 (count gate)", len(rels))
	}
}

func TestRowsJSONNoResultsMessage(t *testing.T) {
	site := rowsSite(t, map[string]string{"/api": `{"error":"No torrents here"}`})
	search := jsonTestSearch()
	search.Paths[0].Response.NoResultsMessage = "No torrents here"
	x := rowsIndexer(t, site, search, rowsTestCats())
	rels, err := x.performQuery(context.Background(), searchQuery("show", 5000))
	if err != nil {
		t.Fatal(err)
	}
	if len(rels) != 0 {
		t.Fatalf("got %d, want 0", len(rels))
	}
}

func TestRowsJSONMissingRowsAttribute(t *testing.T) {
	// rows.attribute selects a sub-object that is absent on one row → row
	// skipped (MissingAttributeEqualsNoResults)
	site := rowsSite(t, map[string]string{"/api": `{"result":{"total":2,"items":[
		{"name":"Alpha","meta":{"path":"/dl/1"}},
		{"other":true}
	]}}`})
	search := jsonTestSearch()
	search.Rows = RowsBlock{
		SelectorBlock: SelectorBlock{Selector: "result.items", Attribute: "meta"},
		Count:         &SelectorBlock{Selector: "result.total"},
	}
	search.Rows.MissingAttributeEqualsNoResults = true
	search.Fields = []FieldBlock{
		{Name: "title", Selector: &SelectorBlock{Selector: "..name"}},
		{Name: "download", Selector: &SelectorBlock{Selector: "path"}},
	}
	x := rowsIndexer(t, site, search, rowsTestCats())
	rels, err := x.performQuery(context.Background(), searchQuery("show", 5000))
	if err != nil {
		t.Fatal(err)
	}
	if len(rels) != 1 || rels[0].Title != "Alpha" {
		t.Fatalf("got %+v, want 1 release Alpha", rels)
	}
}

func TestRowsJSONMultiple(t *testing.T) {
	// rows.attribute + rows.multiple over a JObject: Go sorts map keys
	// (see 澄清记录)
	site := rowsSite(t, map[string]string{"/api": `{"result":{"total":1,"items":[
		{"name":"Alpha","variants":{
			"b":{"path":"/dl/2"},
			"a":{"path":"/dl/1"},
			"c":{"path":"/dl/3"}
		}}
	]}}`})
	search := jsonTestSearch()
	search.Rows = RowsBlock{
		SelectorBlock: SelectorBlock{Selector: "result.items", Attribute: "variants"},
		Count:         &SelectorBlock{Selector: "result.total"},
		Multiple:      true,
	}
	search.Fields = []FieldBlock{
		{Name: "title", Selector: &SelectorBlock{Selector: "..name"}},
		{Name: "download", Selector: &SelectorBlock{Selector: "path"}},
	}
	x := rowsIndexer(t, site, search, rowsTestCats())
	rels, err := x.performQuery(context.Background(), searchQuery("show", 5000))
	if err != nil {
		t.Fatal(err)
	}
	if len(rels) != 3 {
		t.Fatalf("got %d, want 3", len(rels))
	}
	// sorted keys: a, b, c
	if rels[0].Title != "Alpha" || !strings.HasSuffix(rels[0].Link.String(), "/dl/1") ||
		!strings.HasSuffix(rels[1].Link.String(), "/dl/2") ||
		!strings.HasSuffix(rels[2].Link.String(), "/dl/3") {
		t.Fatalf("rels = %+v", rels)
	}
}

func TestRowsJSONFatalFieldError(t *testing.T) {
	// JSON branch has NO per-row try/catch: a missing non-optional field
	// fails the whole query
	site := rowsSite(t, map[string]string{"/api": rowsFixtureJSON})
	search := jsonTestSearch()
	search.Fields = append(search.Fields, FieldBlock{
		Name: "grabs", Selector: &SelectorBlock{Selector: "nosuchfield"},
	})
	x := rowsIndexer(t, site, search, rowsTestCats())
	_, err := x.performQuery(context.Background(), searchQuery("show", 5000))
	if err == nil || !strings.Contains(err.Error(), "Error while parsing field=") {
		t.Fatalf("err = %v", err)
	}
}

func TestRowsJSONParentSelector(t *testing.T) {
	// ".."-prefixed field selectors climb to the outer row
	site := rowsSite(t, map[string]string{"/api": `{"result":{"total":1,"items":[
		{"name":"Alpha","file":{"path":"/dl/1"}}
	]}}`})
	search := jsonTestSearch()
	search.Rows = RowsBlock{
		SelectorBlock: SelectorBlock{Selector: "result.items", Attribute: "file"},
		Count:         &SelectorBlock{Selector: "result.total"},
	}
	search.Fields = []FieldBlock{
		{Name: "title", Selector: &SelectorBlock{Selector: "..name"}},
		{Name: "download", Selector: &SelectorBlock{Selector: "path"}},
	}
	x := rowsIndexer(t, site, search, rowsTestCats())
	rels, err := x.performQuery(context.Background(), searchQuery("show", 5000))
	if err != nil {
		t.Fatal(err)
	}
	if len(rels) != 1 || rels[0].Title != "Alpha" || !strings.HasSuffix(rels[0].Link.String(), "/dl/1") {
		t.Fatalf("rels = %+v", rels)
	}
}

func TestRowsJSONFieldFiltersAndCase(t *testing.T) {
	site := rowsSite(t, map[string]string{"/api": `{"result":{"total":1,"items":[
		{"name":"Alpha 1080p","promotion":"free","download":"/dl/1"}
	]}}`})
	search := jsonTestSearch()
	search.Rows = RowsBlock{
		SelectorBlock: SelectorBlock{Selector: "result.items"},
		Count:         &SelectorBlock{Selector: "result.total"},
	}
	search.Fields = []FieldBlock{
		{Name: "title", Selector: &SelectorBlock{
			Selector: "name",
			Filters:  []FilterBlock{{Name: "re_replace", Args: []any{" 1080p$", ""}}},
		}},
		// JSON Case compares the VALUE (or "*"), not CSS
		{Name: "downloadvolumefactor", Selector: &SelectorBlock{
			Selector: "promotion",
			Case: StringMap{
				{Key: "free", Value: "0"},
				{Key: "*", Value: "1"},
			},
		}},
		{Name: "download", Selector: &SelectorBlock{Selector: "download"}},
	}
	x := rowsIndexer(t, site, search, rowsTestCats())
	rels, err := x.performQuery(context.Background(), searchQuery("show", 5000))
	if err != nil {
		t.Fatal(err)
	}
	if rels[0].Title != "Alpha" {
		t.Errorf("title = %q", rels[0].Title)
	}
	if rels[0].DownloadVolumeFactor == nil || *rels[0].DownloadVolumeFactor != 0 {
		t.Errorf("dvf = %v", rels[0].DownloadVolumeFactor)
	}
}

func TestRowsXMLResponse(t *testing.T) {
	site := rowsSite(t, map[string]string{"/rss": `<?xml version="1.0"?>
<rss><channel>
<item><title>Alpha Release</title><link>https://example.test/dl/1</link><size>1048576</size></item>
<item><title>Beta Release</title><link>https://example.test/dl/2</link><size>2097152</size></item>
</channel></rss>`})
	search := &SearchBlock{
		Paths: []*SearchPathBlock{{
			RequestBlock:  RequestBlock{Path: "rss"},
			Inheritinputs: true,
			Response:      &ResponseBlock{Type: "xml"},
		}},
		Rows: RowsBlock{SelectorBlock: SelectorBlock{Selector: "item"}},
		Fields: []FieldBlock{
			{Name: "title", Selector: &SelectorBlock{Selector: "title"}},
			{Name: "download", Selector: &SelectorBlock{Selector: "link"}},
			{Name: "size", Selector: &SelectorBlock{Selector: "size"}},
		},
	}
	x := rowsIndexer(t, site, search, rowsTestCats())
	rels, err := x.performQuery(context.Background(), searchQuery("show", 5000))
	if err != nil {
		t.Fatal(err)
	}
	if len(rels) != 2 {
		t.Fatalf("got %d", len(rels))
	}
	if rels[0].Title != "Alpha Release" || rels[0].Size == nil || *rels[0].Size != 1048576 {
		t.Errorf("row0 = %+v", rels[0])
	}
}

func TestRowsDateFromField(t *testing.T) {
	site := rowsSite(t, map[string]string{"/browse": `<html><body><table>
<tr class="row"><td class="t">Alpha</td><td class="d">24.05.2023</td><td class="dl"><a href="/dl/1">x</a></td></tr>
</table></body></html>`})
	search := &SearchBlock{
		Paths: []*SearchPathBlock{{RequestBlock: RequestBlock{Path: "browse"}, Inheritinputs: true}},
		Rows:  RowsBlock{SelectorBlock: SelectorBlock{Selector: "tr.row"}},
		Fields: []FieldBlock{
			{Name: "title", Selector: &SelectorBlock{Selector: "td.t"}},
			{Name: "date", Selector: &SelectorBlock{Selector: "td.d"}},
			{Name: "download", Selector: &SelectorBlock{Selector: "td.dl a", Attribute: "href"}},
		},
	}
	x := rowsIndexer(t, site, search, rowsTestCats())
	rels := queryRows(t, x, "show", 5000)
	if rels[0].PublishDate.Year() != 2023 || rels[0].PublishDate.Day() != 24 {
		t.Errorf("date = %v", rels[0].PublishDate)
	}
}

func TestRowsGenreAndYear(t *testing.T) {
	site := rowsSite(t, map[string]string{"/browse": `<html><body><table>
<tr class="row"><td class="t">Alpha (2020)</td><td class="g">Action, Adventure</td><td class="y">2020</td><td class="dl"><a href="/dl/1">x</a></td></tr>
<tr class="row"><td class="t">Beta</td><td class="g">Drama / Mystery</td><td class="y">2019</td><td class="dl"><a href="/dl/2">x</a></td></tr>
</table></body></html>`})
	search := &SearchBlock{
		Paths: []*SearchPathBlock{{RequestBlock: RequestBlock{Path: "browse"}, Inheritinputs: true}},
		Rows:  RowsBlock{SelectorBlock: SelectorBlock{Selector: "tr.row"}},
		Fields: []FieldBlock{
			{Name: "title", Selector: &SelectorBlock{Selector: "td.t"}},
			{Name: "genre", Selector: &SelectorBlock{Selector: "td.g"}},
			{Name: "year", Selector: &SelectorBlock{Selector: "td.y"}},
			{Name: "download", Selector: &SelectorBlock{Selector: "td.dl a", Attribute: "href"}},
		},
	}
	x := rowsIndexer(t, site, search, rowsTestCats())
	rels := queryRows(t, x, "show", 5000)
	if len(rels[0].Genres) != 2 || rels[0].Genres[0] != "Action" || rels[0].Genres[1] != "Adventure" {
		t.Errorf("genres = %v", rels[0].Genres)
	}
	if len(rels[1].Genres) != 2 || rels[1].Genres[0] != "Drama" || rels[1].Genres[1] != "Mystery" {
		t.Errorf("genres = %v", rels[1].Genres)
	}
	if rels[0].Year == nil || *rels[0].Year != 2020 || rels[1].Year == nil || *rels[1].Year != 2019 {
		t.Errorf("years = %v/%v", rels[0].Year, rels[1].Year)
	}
}

func TestRowsInfohashAndVolumeFactors(t *testing.T) {
	site := rowsSite(t, map[string]string{"/browse": `<html><body><table>
<tr class="row"><td class="t">Alpha</td><td class="h">ABCDEF0123456789ABCDEF0123456789ABCDEF01</td>
<td class="dlf">0.5</td><td class="ulf">2</td><td class="mr">1</td><td class="mst">86400</td>
<td class="dl"><a href="/dl/1">x</a></td></tr>
</table></body></html>`})
	search := &SearchBlock{
		Paths: []*SearchPathBlock{{RequestBlock: RequestBlock{Path: "browse"}, Inheritinputs: true}},
		Rows:  RowsBlock{SelectorBlock: SelectorBlock{Selector: "tr.row"}},
		Fields: []FieldBlock{
			{Name: "title", Selector: &SelectorBlock{Selector: "td.t"}},
			{Name: "infohash", Selector: &SelectorBlock{Selector: "td.h"}},
			{Name: "downloadvolumefactor", Selector: &SelectorBlock{Selector: "td.dlf"}},
			{Name: "uploadvolumefactor", Selector: &SelectorBlock{Selector: "td.ulf"}},
			{Name: "minimumratio", Selector: &SelectorBlock{Selector: "td.mr"}},
			{Name: "minimumseedtime", Selector: &SelectorBlock{Selector: "td.mst"}},
			{Name: "download", Selector: &SelectorBlock{Selector: "td.dl a", Attribute: "href"}},
		},
	}
	x := rowsIndexer(t, site, search, rowsTestCats())
	rels := queryRows(t, x, "show", 5000)
	r := rels[0]
	if r.InfoHash != "ABCDEF0123456789ABCDEF0123456789ABCDEF01" {
		t.Errorf("infohash = %q", r.InfoHash)
	}
	if r.DownloadVolumeFactor == nil || *r.DownloadVolumeFactor != 0.5 {
		t.Errorf("dvf = %v", r.DownloadVolumeFactor)
	}
	if r.UploadVolumeFactor == nil || *r.UploadVolumeFactor != 2 {
		t.Errorf("uvf = %v", r.UploadVolumeFactor)
	}
	if r.MinimumRatio == nil || *r.MinimumRatio != 1 {
		t.Errorf("minratio = %v", r.MinimumRatio)
	}
	if r.MinimumSeedTime == nil || *r.MinimumSeedTime != 86400 {
		t.Errorf("minseedtime = %v", r.MinimumSeedTime)
	}
}

func TestRowsBookMusicFields(t *testing.T) {
	site := rowsSite(t, map[string]string{"/browse": `<html><body><table>
<tr class="row"><td class="t">Track One</td><td class="a">Some Artist</td><td class="al">The Album</td>
<td class="au">Author Name</td><td class="bt">Book Title</td><td class="pub">Publisher</td>
<td class="lbl">Label X</td><td class="tr">3</td><td class="dl"><a href="/dl/1">x</a></td></tr>
</table></body></html>`})
	search := &SearchBlock{
		Paths: []*SearchPathBlock{{RequestBlock: RequestBlock{Path: "browse"}, Inheritinputs: true}},
		Rows:  RowsBlock{SelectorBlock: SelectorBlock{Selector: "tr.row"}},
		Fields: []FieldBlock{
			{Name: "title", Selector: &SelectorBlock{Selector: "td.t"}},
			{Name: "artist", Selector: &SelectorBlock{Selector: "td.a"}},
			{Name: "album", Selector: &SelectorBlock{Selector: "td.al"}},
			{Name: "author", Selector: &SelectorBlock{Selector: "td.au"}},
			{Name: "booktitle", Selector: &SelectorBlock{Selector: "td.bt"}},
			{Name: "publisher", Selector: &SelectorBlock{Selector: "td.pub"}},
			{Name: "label", Selector: &SelectorBlock{Selector: "td.lbl"}},
			{Name: "track", Selector: &SelectorBlock{Selector: "td.tr"}},
			{Name: "download", Selector: &SelectorBlock{Selector: "td.dl a", Attribute: "href"}},
		},
	}
	x := rowsIndexer(t, site, search, rowsTestCats())
	r := queryRows(t, x, "show", 5000)[0]
	if r.Artist != "Some Artist" || r.Album != "The Album" || r.Author != "Author Name" ||
		r.BookTitle != "Book Title" || r.Publisher != "Publisher" || r.Label != "Label X" ||
		r.Track != "3" {
		t.Errorf("release = %+v", r)
	}
}
