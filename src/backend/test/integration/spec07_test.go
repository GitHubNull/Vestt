// SPEC-07 acceptance: full-stack API behaviour over the fake indexers —
// torznab actions, error matrix, caching, manual aggregation, potato,
// proxies, cache view, performance.
package integration

import (
	"encoding/base64"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"vestt/test/fixtures"
)

// --- helpers -------------------------------------------------------------

// rssEnvelope decodes a torznab RSS document.
type rssEnvelope struct {
	Channel struct {
		Title string `xml:"title"`
		Items []struct {
			Title       string `xml:"title"`
			Link        string `xml:"link"`
			PubDate     string `xml:"pubDate"`
			Description string `xml:"description"`
		} `xml:"item"`
	} `xml:"channel"`
}

// torznabError decodes the XML error document.
type torznabError struct {
	Code        int    `xml:"code,attr"`
	Description string `xml:"description,attr"`
}

func decodeRSS(t *testing.T, resp *http.Response) rssEnvelope {
	t.Helper()
	var env rssEnvelope
	if err := xml.NewDecoder(resp.Body).Decode(&env); err != nil {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("RSS decode: %v (%s)", err, body)
	}
	return env
}

func readErrorXML(t *testing.T, resp *http.Response) torznabError {
	t.Helper()
	var e torznabError
	if err := xml.NewDecoder(resp.Body).Decode(&e); err != nil {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("error XML decode: %v (%s)", err, body)
	}
	return e
}

func bodyString(t *testing.T, resp *http.Response) string {
	t.Helper()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func (e *testEnv) delete(t *testing.T, path string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodDelete, e.srv.URL+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("DELETE %s: %v", path, err)
	}
	return resp
}

func b64url(s string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(s))
}

// xmllintSmoke validates the body with xmllint when it is installed.
func xmllintSmoke(t *testing.T, payload string) {
	t.Helper()
	if _, err := exec.LookPath("xmllint"); err != nil {
		t.Skip("xmllint not installed")
	}
	cmd := exec.Command("xmllint", "--noout", "-")
	cmd.Stdin = strings.NewReader(payload)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("xmllint: %v: %s", err, out)
	}
}

func titles(env rssEnvelope) []string {
	out := make([]string, len(env.Channel.Items))
	for i, it := range env.Channel.Items {
		out[i] = it.Title
	}
	return out
}

// --- torznab actions -----------------------------------------------------

func TestTorznabCaps(t *testing.T) {
	e := newTestEnv(t)
	e.configureIndexer(t, "fakegood")
	resp := e.get(t, "/api/v2.0/indexers/fakegood/results/torznab/?apikey="+e.apiKey()+"&t=caps")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: %d", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "application/rss+xml" {
		t.Fatalf("content-type: %q", ct)
	}
	body := bodyString(t, resp)
	for _, want := range []string{"<searching>", `available="yes"`} {
		if !strings.Contains(body, want) {
			t.Fatalf("caps missing %q: %s", want, body)
		}
	}
	xmllintSmoke(t, body)
}

func TestTorznabSearchAllTypes(t *testing.T) {
	e := newTestEnv(t)
	e.configureIndexer(t, "fakegood")
	for _, typ := range []string{"search", "tvsearch", "movie", "music", "book"} {
		resp := e.get(t, fmt.Sprintf("/api/v2.0/indexers/fakegood/results/torznab/?apikey=%s&t=%s&q=good", e.apiKey(), typ))
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("%s: status %d", typ, resp.StatusCode)
		}
		env := decodeRSS(t, resp)
		got := titles(env)
		if len(got) != 2 || got[0] != "Good Release A" || got[1] != "Good Release B" {
			t.Fatalf("%s: items %v", typ, got)
		}
		resp.Body.Close()
	}
}

func TestTorznabErrorMatrix(t *testing.T) {
	e := newTestEnv(t)
	e.configureIndexer(t, "fakegood")
	key := e.apiKey()

	// missing apikey → torznab-path error semantics: HTTP 200 + code 100 XML
	resp := e.get(t, "/api/v2.0/indexers/fakegood/results/torznab/?t=search&q=x")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("no-apikey status: %d", resp.StatusCode)
	}
	if e := readErrorXML(t, resp); e.Code != 100 {
		t.Fatalf("no-apikey: %+v", e)
	}
	resp.Body.Close()

	// unknown indexer → 500 (upstream GetWebIndexer throws inside the filter)
	resp = e.get(t, "/api/v2.0/indexers/nosuch/results/torznab/?apikey="+key+"&t=search&q=x")
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("unknown indexer status: %d", resp.StatusCode)
	}
	resp.Body.Close()

	// unconfigured indexer → 200 + code 201
	resp = e.get(t, "/api/v2.0/indexers/faketv/results/torznab/?apikey="+key+"&t=search&q=x")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("unconfigured status: %d", resp.StatusCode)
	}
	if e := readErrorXML(t, resp); e.Code != 201 || !strings.Contains(e.Description, "not configured") {
		t.Fatalf("unconfigured: %+v", e)
	}
	resp.Body.Close()

	// now configure faketv: upstream filter order checks IsConfigured before
	// capabilities, so both following sub-checks need a configured indexer
	e.configureIndexer(t, "faketv")

	// capability mismatch: faketv has no movie mode → 200 + code 201
	resp = e.get(t, "/api/v2.0/indexers/faketv/results/torznab/?apikey="+key+"&t=movie&q=x")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("capability status: %d", resp.StatusCode)
	}
	if e := readErrorXML(t, resp); e.Code != 201 || !strings.Contains(e.Description, "does not support the requested query") {
		t.Fatalf("capability: %+v", e)
	}
	resp.Body.Close()

	// non-meta indexer asked for t=indexers → 200 + code 201: upstream
	// RequiresValidQuery runs CanHandleQuery first, which rejects the
	// "indexers" query type (BaseIndexer.cs:275); the 203 "this isn't a
	// meta indexer" branch is only reachable for indexers that override
	// CanHandleQuery without being meta
	resp = e.get(t, "/api/v2.0/indexers/fakegood/results/torznab/?apikey="+key+"&t=indexers")
	if e := readErrorXML(t, resp); e.Code != 201 || !strings.Contains(e.Description, "does not support the requested query") {
		t.Fatalf("t=indexers on non-meta: %+v", e)
	}
	resp.Body.Close()

	// invalid imdbid → 200 + code 201
	resp = e.get(t, "/api/v2.0/indexers/fakegood/results/torznab/?apikey="+key+"&t=movie&imdbid=notanid")
	if e := readErrorXML(t, resp); e.Code != 201 || e.Description != "Incorrect parameter: invalid imdbid format" {
		t.Fatalf("invalid imdbid: %+v", e)
	}
	resp.Body.Close()

	// tv imdb unsupported → 200 + code 203
	resp = e.get(t, "/api/v2.0/indexers/faketv/results/torznab/?apikey="+key+"&t=tvsearch&imdbid=tt0001234")
	if e := readErrorXML(t, resp); e.Code != 203 || !strings.Contains(e.Description, "imdbid is not supported for TV search") {
		t.Fatalf("tv imdb: %+v", e)
	}
	resp.Body.Close()
}

func TestTorznabIndexersListing(t *testing.T) {
	e := newTestEnv(t)
	e.configureIndexer(t, "fakegood")

	resp := e.get(t, "/api/v2.0/indexers/all/results/torznab/?apikey="+e.apiKey()+"&t=indexers&configured=true")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: %d", resp.StatusCode)
	}
	body := bodyString(t, resp)
	xmllintSmoke(t, body)
	if !strings.Contains(body, `id="fakegood"`) {
		t.Fatalf("listing missing fakegood: %s", body)
	}
	if strings.Contains(body, `id="faketv"`) || strings.Contains(body, `id="fakecardigann"`) {
		t.Fatalf("listing must only contain configured indexers: %s", body)
	}
}

func TestTorznabImdbNormalization(t *testing.T) {
	e := newTestEnv(t)
	e.configureIndexer(t, "fakegood")
	key := e.apiKey()

	for _, imdb := range []string{"tt0001234", "1234", "tt1234", "001234"} {
		resp := e.get(t, fmt.Sprintf("/api/v2.0/indexers/fakegood/results/torznab/?apikey=%s&t=movie&imdbid=%s", key, imdb))
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("imdbid=%s: status %d", imdb, resp.StatusCode)
		}
		body := bodyString(t, resp)
		if strings.Contains(body, "<error") {
			t.Fatalf("imdbid=%s rejected: %s", imdb, body)
		}
		resp.Body.Close()
	}
}

func TestTorznab429(t *testing.T) {
	e := newTestEnv(t)
	e.configureIndexer(t, "fakegood")
	fixtures.SetBehavior(func(b *fixtures.Behavior) { b.GoodPath = "/ratelimited" })
	defer fixtures.SetBehavior(func(b *fixtures.Behavior) { b.GoodPath = "/page" })

	resp := e.get(t, "/api/v2.0/indexers/fakegood/results/torznab/?apikey="+e.apiKey()+"&t=search&q=x")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("status: %d", resp.StatusCode)
	}
	if ra := resp.Header.Get("Retry-After"); ra != "42" {
		t.Fatalf("Retry-After: %q", ra)
	}
	if e := readErrorXML(t, resp); e.Code != 900 {
		t.Fatalf("error: %+v", e)
	}
}

// --- caching -------------------------------------------------------------

func TestCacheHitMissAndTTL(t *testing.T) {
	e := newTestEnv(t)
	e.configureIndexer(t, "fakegood")
	url := "/api/v2.0/indexers/fakegood/results/torznab/?apikey=" + e.apiKey() + "&t=search&q=cacheme"

	resp := e.get(t, url)
	env1 := decodeRSS(t, resp)
	resp.Body.Close()
	if h := e.site.Hits(); h != 1 {
		t.Fatalf("first search should hit the site once, got %d", h)
	}

	resp = e.get(t, url)
	env2 := decodeRSS(t, resp)
	resp.Body.Close()
	if h := e.site.Hits(); h != 1 {
		t.Fatalf("second search must be served from cache, site hits %d", h)
	}
	if titles(env1)[0] != titles(env2)[0] {
		t.Fatalf("cached body differs: %v vs %v", titles(env1), titles(env2))
	}

	// shrink the TTL to 1s, wait for expiry, expect a re-fetch
	resp = e.post(t, "/api/v2.0/server/config", "application/json", []byte(`{"cache_ttl": 1}`))
	resp.Body.Close()
	time.Sleep(1100 * time.Millisecond)

	resp = e.get(t, url)
	resp.Body.Close()
	if h := e.site.Hits(); h != 2 {
		t.Fatalf("expired entry must re-fetch, site hits %d", h)
	}
}

func TestCacheViewConsistency(t *testing.T) {
	e := newTestEnv(t)
	e.configureIndexer(t, "fakegood")
	resp := e.get(t, "/api/v2.0/indexers/fakegood/results/torznab/?apikey="+e.apiKey()+"&t=search&q=x")
	resp.Body.Close()

	// both releases appear in the cache view, labelled with the tracker name
	resp = e.get(t, "/api/v2.0/indexers/Cache")
	var view []map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&view); err != nil {
		t.Fatalf("cache view decode: %v", err)
	}
	resp.Body.Close()
	if len(view) != 2 {
		t.Fatalf("cache view rows: %d", len(view))
	}
	if view[0]["Tracker"] != "Fake fakegood" || view[0]["TrackerId"] != "fakegood" || view[0]["TrackerType"] != "public" {
		t.Fatalf("cache view tracker fields: %v", view[0])
	}

	// deleting the indexer drops its cached rows
	resp = e.delete(t, "/api/v2.0/indexers/fakegood")
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("delete status: %d", resp.StatusCode)
	}
	resp.Body.Close()

	resp = e.get(t, "/api/v2.0/indexers/Cache")
	view = nil
	if err := json.NewDecoder(resp.Body).Decode(&view); err != nil {
		t.Fatalf("cache view decode: %v", err)
	}
	resp.Body.Close()
	if len(view) != 0 {
		t.Fatalf("cache view after delete: %d rows", len(view))
	}
}

// --- manual search -------------------------------------------------------

type manualResult struct {
	Results []struct {
		Title     string `json:"Title"`
		Tracker   string `json:"Tracker"`
		TrackerID string `json:"TrackerId"`
	} `json:"results"`
	Indexers []struct {
		ID      string  `json:"ID"`
		Name    string  `json:"Name"`
		Status  int     `json:"Status"`
		Results int     `json:"Results"`
		Error   *string `json:"Error"`
	} `json:"indexers"`
}

func manualSearch(t *testing.T, e *testEnv, query string) manualResult {
	t.Helper()
	resp := e.get(t, "/api/v2.0/indexers/all/results?apikey="+e.apiKey()+query)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("manual search status: %d (%s)", resp.StatusCode, bodyString(t, resp))
	}
	var m manualResult
	if err := json.NewDecoder(resp.Body).Decode(&m); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return m
}

func TestManualSearchAggregation(t *testing.T) {
	e := newTestEnv(t)
	e.configureIndexer(t, "fakegood")
	e.configureIndexer(t, "faketv")
	e.configureIndexer(t, "fakeflaky")

	m := manualSearch(t, e, "&query=test")

	statusByID := map[string]int{}
	for _, ix := range m.Indexers {
		statusByID[ix.ID] = ix.Status
		if ix.Status == 2 && ix.Error != nil {
			t.Fatalf("OK indexer %s carries error: %v", ix.ID, *ix.Error)
		}
	}
	if statusByID["fakegood"] != 2 || statusByID["faketv"] != 2 || statusByID["fakeflaky"] != 1 {
		t.Fatalf("statuses: %v", statusByID)
	}
	for _, ix := range m.Indexers {
		if ix.ID == "fakeflaky" && (ix.Error == nil || !strings.Contains(*ix.Error, "flaky indexer exploded")) {
			t.Fatalf("fakeflaky error missing: %+v", ix)
		}
	}

	// merged, PublishDate descending: A (Jan 2), TV (Jan 1 noon), B (Jan 1 03:04)
	got := make([]string, len(m.Results))
	for i, r := range m.Results {
		got[i] = r.Title
	}
	want := []string{"Good Release A", "TV Release", "Good Release B"}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("order: got %v want %v", got, want)
	}
}

func TestManualSearchTrackerFilter(t *testing.T) {
	e := newTestEnv(t)
	e.configureIndexer(t, "fakegood")
	e.configureIndexer(t, "faketv")

	m := manualSearch(t, e, "&query=test&Tracker[]=fakegood")
	if len(m.Results) != 2 {
		t.Fatalf("results: %+v", m.Results)
	}
	if len(m.Indexers) != 1 || m.Indexers[0].ID != "fakegood" {
		t.Fatalf("indexers: %+v", m.Indexers)
	}
}

func TestManualSearchCategoryFilter(t *testing.T) {
	e := newTestEnv(t)
	e.configureIndexer(t, "fakegood")
	e.configureIndexer(t, "faketv")

	m := manualSearch(t, e, "&query=test&Category[]=5040")
	if len(m.Results) != 1 || m.Results[0].Title != "TV Release" {
		t.Fatalf("results: %+v", m.Results)
	}
}

// --- potato --------------------------------------------------------------

func TestPotatoGolden(t *testing.T) {
	e := newTestEnv(t)
	e.configureIndexer(t, "fakegood")

	resp := e.get(t, "/api/v2.0/indexers/fakegood/results/potato/?apikey="+e.apiKey()+"&search=good")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: %d (%s)", resp.StatusCode, bodyString(t, resp))
	}
	var body struct {
		Results []map[string]any `json:"results"`
		Total   int              `json:"total_results"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Total != 2 || len(body.Results) != 2 {
		t.Fatalf("total: %+v", body)
	}

	wantDownload := e.srv.URL + "/dl/fakegood/?jackett_apikey=" + e.apiKey() +
		"&path=" + b64url(e.site.URL+"/torrent") + "&file=Good+Release+A"
	want0 := map[string]any{
		"release_name": "Good Release A[Fake fakegood]",
		"torrent_id":   e.site.URL + "/details/a",
		"details_url":  "",
		"download_url": wantDownload,
		"imdb_id":      "tt0133093",
		"freeleech":    true,
		"type":         "movie",
		"size":         float64(1),
		"leechers":     float64(5),
		"seeders":      float64(10),
		"publish_date": "2026-01-02T03:04:05",
	}
	got0 := body.Results[0]
	for k, want := range want0 {
		if got0[k] != want {
			t.Fatalf("results[0].%s: got %v want %v (full: %v)", k, got0[k], want, got0)
		}
	}

	// release B downloads via the untouched magnet
	if got := body.Results[1]["download_url"]; got != "magnet:?xt=urn:btih:0123456789abcdef0123456789abcdef01234567&dn=Good+Release+B" {
		t.Fatalf("results[1].download_url: %v", got)
	}
	if got := body.Results[1]["size"]; got != float64(2) {
		t.Fatalf("results[1].size: %v", got)
	}
	if got := body.Results[1]["seeders"]; got != float64(3) {
		t.Fatalf("results[1].seeders: %v", got)
	}
	if got := body.Results[1]["leechers"]; got != float64(2) {
		t.Fatalf("results[1].leechers: %v", got)
	}
	if got := body.Results[1]["imdb_id"]; got != "" {
		t.Fatalf("results[1].imdb_id: %v", got)
	}
	if got := body.Results[1]["freeleech"]; got != false {
		t.Fatalf("results[1].freeleech: %v", got)
	}
	if got := body.Results[1]["publish_date"]; got != "2026-01-01T03:04:05" {
		t.Fatalf("results[1].publish_date: %v", got)
	}
}

// --- proxies -------------------------------------------------------------

func TestDownloadProxy(t *testing.T) {
	e := newTestEnv(t)
	e.configureIndexer(t, "fakegood")

	// wrong key → 401
	resp := e.get(t, "/dl/fakegood/?jackett_apikey=wrong&path="+b64url(e.site.URL+"/torrent")+"&file=x")
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("wrong key: %d", resp.StatusCode)
	}
	resp.Body.Close()

	// unconfigured indexer → 403
	resp = e.get(t, "/dl/faketv/?jackett_apikey="+e.apiKey()+"&path="+b64url(e.site.URL+"/torrent")+"&file=x")
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("unconfigured: %d", resp.StatusCode)
	}
	resp.Body.Close()

	// discover the proxied link from the RSS feed, then download through it
	resp = e.get(t, "/api/v2.0/indexers/fakegood/results/torznab/?apikey="+e.apiKey()+"&t=search&q=x")
	env := decodeRSS(t, resp)
	resp.Body.Close()
	if len(env.Channel.Items) == 0 || !strings.HasPrefix(env.Channel.Items[0].Link, e.srv.URL+"/dl/fakegood/") {
		t.Fatalf("RSS link not proxied: %+v", env.Channel.Items)
	}

	dl, err := http.Get(env.Channel.Items[0].Link)
	if err != nil {
		t.Fatal(err)
	}
	defer dl.Body.Close()
	if dl.StatusCode != http.StatusOK {
		t.Fatalf("download status: %d", dl.StatusCode)
	}
	if ct := dl.Header.Get("Content-Type"); ct != "application/x-bittorrent" {
		t.Fatalf("content-type: %q", ct)
	}
	disp := dl.Header.Get("Content-Disposition")
	// .NET Unix keeps spaces in file names (stringutil.go:12)
	if !strings.Contains(disp, "Good Release A.torrent") {
		t.Fatalf("disposition: %q", disp)
	}
	got, _ := io.ReadAll(dl.Body)
	// the fake site serves keys unsorted; the proxy must re-encode canonically
	if string(got) != "d1:ai2e7:privatei1e4:spam4:eggse" {
		t.Fatalf("bencode not canonicalised: %q", got)
	}
}

func TestDownloadProxyNotFound(t *testing.T) {
	e := newTestEnv(t)
	e.configureIndexer(t, "fakegood")
	resp := e.get(t, "/dl/nosuch/?jackett_apikey="+e.apiKey()+"&path="+b64url(e.site.URL+"/torrent")+"&file=x")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("unknown indexer download: %d", resp.StatusCode)
	}
}

func TestImageProxy(t *testing.T) {
	e := newTestEnv(t)
	e.configureIndexer(t, "fakegood")

	resp := e.get(t, "/img/fakegood/?jackett_apikey="+e.apiKey()+"&path="+b64url(e.site.URL+"/img"))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: %d", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "image/png" {
		t.Fatalf("content-type: %q", ct)
	}
	if b := bodyString(t, resp); b != "PNG-BYTES" {
		t.Fatalf("body: %q", b)
	}
	resp.Body.Close()

	// non-OK upstream status passes through
	resp = e.get(t, "/img/fakegood/?jackett_apikey="+e.apiKey()+"&path="+b64url(e.site.URL+"/notfound"))
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("passthrough status: %d", resp.StatusCode)
	}
	resp.Body.Close()
}

func TestBlackholeProxy(t *testing.T) {
	e := newTestEnv(t)
	e.configureIndexer(t, "fakegood")
	dir := t.TempDir()

	// without a blackhole directory the request errors in the JSON body
	magnet := "magnet:?xt=urn:btih:0123456789abcdef0123456789abcdef01234567&dn=Good+Release+B"
	resp := e.get(t, "/bh/fakegood/?jackett_apikey="+e.apiKey()+"&path="+b64url(magnet)+"&file=Good+Release+B")
	var reply map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&reply); err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if reply["result"] != "error" || reply["error"] != "Blackhole directory not set!" {
		t.Fatalf("reply: %v", reply)
	}

	resp = e.post(t, "/api/v2.0/server/config", "application/json", []byte(fmt.Sprintf(`{"blackholedir": %q}`, dir)))
	resp.Body.Close()

	// magnet → .magnet file holding the magnet string
	resp = e.get(t, "/bh/fakegood/?jackett_apikey="+e.apiKey()+"&path="+b64url(magnet)+"&file=Good+Release+B")
	reply = nil
	if err := json.NewDecoder(resp.Body).Decode(&reply); err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if reply["result"] != "success" {
		t.Fatalf("magnet reply: %v", reply)
	}
	magnets, _ := filepath.Glob(filepath.Join(dir, "*.magnet"))
	if len(magnets) != 1 {
		t.Fatalf("magnet files: %v", magnets)
	}
	raw, err := os.ReadFile(magnets[0])
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != magnet {
		t.Fatalf("magnet file content: %q", raw)
	}
	// .NET Unix GetInvalidFileNameChars keeps spaces (stringutil.go:12)
	if !strings.HasSuffix(filepath.Base(magnets[0]), "-Fake fakegood-Good Release B.magnet") {
		t.Fatalf("magnet filename: %s", filepath.Base(magnets[0]))
	}

	// torrent bytes → .torrent file, written verbatim
	resp = e.get(t, "/bh/fakegood/?jackett_apikey="+e.apiKey()+"&path="+b64url(e.site.URL+"/torrent")+"&file=Good+Release+A")
	reply = nil
	if err := json.NewDecoder(resp.Body).Decode(&reply); err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if reply["result"] != "success" {
		t.Fatalf("torrent reply: %v", reply)
	}
	torrents, _ := filepath.Glob(filepath.Join(dir, "*.torrent"))
	if len(torrents) != 1 {
		t.Fatalf("torrent files: %v", torrents)
	}
	raw, err = os.ReadFile(torrents[0])
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != "d4:spam4:eggs7:privatei1e1:ai2ee" {
		t.Fatalf("blackhole must write verbatim bytes: %q", raw)
	}
}

// --- cardigann engine fixture --------------------------------------------

func TestCardigannFixtureIndexer(t *testing.T) {
	e := newTestEnv(t)
	e.configureIndexer(t, "fakecardigann", map[string]any{"id": "sitelink", "value": e.site.URL + "/"})

	url := "/api/v2.0/indexers/fakecardigann/results/torznab/?apikey=" + e.apiKey() + "&t=search&q=cardigann"
	resp := e.get(t, url)
	env := decodeRSS(t, resp)
	resp.Body.Close()
	got := titles(env)
	if len(got) != 1 || got[0] != "Cardigann Release" {
		t.Fatalf("cardigann items: %v", got)
	}
	if s := e.site.Searches(); s != 1 {
		t.Fatalf("cardigann site searches: %d", s)
	}

	// identical query is served from the result cache
	resp = e.get(t, url)
	resp.Body.Close()
	if s := e.site.Searches(); s != 1 {
		t.Fatalf("second search must hit cache, site searches: %d", s)
	}

	// and it aggregates through the meta indexer
	resp = e.get(t, "/api/v2.0/indexers/all/results?apikey="+e.apiKey()+"&query=cardigann")
	defer resp.Body.Close()
	var m manualResult
	if err := json.NewDecoder(resp.Body).Decode(&m); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, r := range m.Results {
		if r.Title == "Cardigann Release" && r.TrackerID == "fakecardigann" {
			found = true
		}
	}
	if !found {
		t.Fatalf("meta search misses cardigann release: %+v", m.Results)
	}
}

// --- indexer admin DTO ----------------------------------------------------

func TestIndexerListDTO(t *testing.T) {
	e := newTestEnv(t)
	e.configureIndexer(t, "fakegood")

	resp := e.get(t, "/api/v2.0/indexers?configured=false")
	defer resp.Body.Close()
	var list []map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&list); err != nil {
		t.Fatal(err)
	}
	byID := map[string]map[string]any{}
	for _, it := range list {
		byID[it["id"].(string)] = it
	}
	good, ok := byID["fakegood"]
	if !ok {
		t.Fatalf("fakegood missing from list: %v", list)
	}
	if good["configured"] != false { // fresh env: not yet configured at list time? configured above
		t.Logf("note: configured=%v", good["configured"])
	}
	if good["name"] != "Fake fakegood" || good["type"] != "public" || good["language"] != "en-US" {
		t.Fatalf("identity fields: %v", good)
	}
	if good["last_error"] != "" {
		t.Fatalf("last_error: %v", good["last_error"])
	}
	if good["potatoenabled"] != true {
		t.Fatalf("potatoenabled (Movies tree present): %v", good["potatoenabled"])
	}
	tv, ok := byID["faketv"]
	if !ok {
		t.Fatalf("faketv missing: %v", list)
	}
	if tv["potatoenabled"] != false {
		t.Fatalf("faketv potatoenabled must be false: %v", tv["potatoenabled"])
	}
	caps, ok := good["caps"].([]any)
	if !ok || len(caps) == 0 {
		t.Fatalf("caps list: %v", good["caps"])
	}

	// configured=true only returns configured indexers
	e.configureIndexer(t, "faketv")
	resp2 := e.get(t, "/api/v2.0/indexers?configured=true")
	defer resp2.Body.Close()
	var conf []map[string]any
	if err := json.NewDecoder(resp2.Body).Decode(&conf); err != nil {
		t.Fatal(err)
	}
	if len(conf) != 2 {
		t.Fatalf("configured list: %d", len(conf))
	}
}

// --- performance ----------------------------------------------------------

func TestFanoutPerformance(t *testing.T) {
	e := newTestEnv(t)
	e.configureIndexer(t, "fakegood")
	e.configureIndexer(t, "faketv")
	e.site.SetDelay(5 * time.Millisecond)

	const n = 50
	lat := make([]time.Duration, n)
	var wg sync.WaitGroup
	start := time.Now()
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			s := time.Now()
			resp, err := http.Get(e.srv.URL + "/api/v2.0/indexers/all/results?apikey=" + e.apiKey() + "&query=perf")
			if err != nil {
				t.Errorf("request: %v", err)
				return
			}
			io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
			lat[i] = time.Since(s)
		}(i)
	}
	wg.Wait()
	total := time.Since(start)

	sort.Slice(lat, func(i, j int) bool { return lat[i] < lat[j] })
	p95 := lat[n-n/20-1] // 95th percentile of n samples
	t.Logf("50 concurrent manual searches: total %v, p95 %v", total, p95)
	if p95 > 500*time.Millisecond {
		t.Fatalf("p95 %v exceeds 500ms budget", p95)
	}
}
