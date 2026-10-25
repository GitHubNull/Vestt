// Sampled end-to-end 对拍 (SPEC-06 acceptance #3): 20 real definitions run
// against hand-written fixture pages served by a fake site, asserting key
// ReleaseInfo fields. The sample list is pinned in testdata/samples.txt.
package engine

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"vestt/common/indexer/configdata"
	"vestt/common/models"
)

// sampleRelease holds per-release expectations; only non-zero fields assert.
type sampleRelease struct {
	title         string
	titleContains string
	linkSuffix    string
	detailsSuffix string
	magnet        bool
	catContains   int
	size          int64
	seeders       int64
	imdb          int64
	dvf           *float64
	dateYear      int
}

type sampleCase struct {
	id      string
	fixture string
	query   *models.TorznabQuery
	config  map[string]string
	// stripHTTPS rewrites "https://{{ .Config.apiurl }}/"-prefixed search
	// paths to scheme-less templates so the httptest server (http) can serve
	// them; the JSON/XML parsing under test is unaffected.
	stripHTTPS bool
	want       []sampleRelease
}

func f64(v float64) *float64 { return &v }

// setConfigValue assigns a string to any scalar config item (bool parsed).
func setConfigValue(t *testing.T, id, key string, item configdata.Item, v string) {
	t.Helper()
	switch it := item.(type) {
	case *configdata.StringItem:
		it.Value = v
	case *configdata.PasswordItem:
		it.Value = v
	case *configdata.HiddenItem:
		it.Value = v
	case *configdata.SelectItem:
		it.Value = v
	case *configdata.BoolItem:
		b, err := strconv.ParseBool(v)
		if err != nil {
			t.Fatalf("%s: config %q: %v", id, key, err)
		}
		it.Value = b
	default:
		t.Fatalf("%s: config %q has unsupported item type %T", id, key, item)
	}
}

func loadSamplesDefs(t *testing.T) map[string]*IndexerDefinition {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	defs, _, err := LoadAll(logger, "")
	if err != nil {
		t.Fatal(err)
	}
	return defs
}

func runSample(t *testing.T, defs map[string]*IndexerDefinition, tc sampleCase) []*models.ReleaseInfo {
	t.Helper()
	def := defs[tc.id]
	if def == nil {
		t.Fatalf("definition %s not found", tc.id)
	}
	fixture, err := os.ReadFile(filepath.Join("testdata", "pages", tc.fixture))
	if err != nil {
		t.Fatalf("fixture %s: %v", tc.fixture, err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, string(fixture))
	}))
	t.Cleanup(srv.Close)

	def = cloneDefinition(def)
	def.Links = []string{srv.URL + "/"}
	if tc.stripHTTPS {
		for _, p := range def.Search.Paths {
			p.Path = strings.Replace(p.Path, "https://{{ .Config.apiurl }}", "{{ .Config.apiurl }}", 1)
			p.Path = strings.Replace(p.Path, "https://{{ .Config.api-url }}", "{{ .Config.api-url }}", 1)
		}
	}
	x, err := NewCardigann(def, discardLogger())
	if err != nil {
		t.Fatalf("NewCardigann(%s): %v", tc.id, err)
	}
	for k, v := range tc.config {
		if v == "$SERVER" {
			v = srv.URL
		}
		item := x.config.GetDynamic(k)
		if item == nil {
			t.Fatalf("%s: no config item %q", tc.id, k)
		}
		setConfigValue(t, tc.id, k, item, v)
	}
	x.Client = mustTestClient(t)

	res, err := x.ResultsForQuery(context.Background(), tc.query, false)
	if err != nil {
		t.Fatalf("ResultsForQuery(%s): %v", tc.id, err)
	}
	return res.Releases
}

// cloneDefinition deep-copies the parts the engine mutates at runtime
// (login defaults, path promotion) so LoadAll's cache stays pristine.
func cloneDefinition(def *IndexerDefinition) *IndexerDefinition {
	c := *def
	c.Links = append([]string(nil), def.Links...)
	c.Search = def.Search
	return &c
}

func assertSampleReleases(t *testing.T, tc sampleCase, rels []*models.ReleaseInfo) {
	t.Helper()
	if len(rels) != len(tc.want) {
		for _, r := range rels {
			t.Logf("  got: title=%q link=%v cat=%v", r.Title, r.Link, r.Category)
		}
		t.Fatalf("%s: got %d releases, want %d", tc.id, len(rels), len(tc.want))
	}
	for i, w := range tc.want {
		r := rels[i]
		if w.title != "" && r.Title != w.title {
			t.Errorf("%s[%d]: title = %q, want %q", tc.id, i, r.Title, w.title)
		}
		if w.titleContains != "" && !strings.Contains(r.Title, w.titleContains) {
			t.Errorf("%s[%d]: title = %q, want contains %q", tc.id, i, r.Title, w.titleContains)
		}
		if w.linkSuffix != "" {
			if r.Link == nil || !strings.HasSuffix(r.Link.String(), w.linkSuffix) {
				t.Errorf("%s[%d]: link = %v, want suffix %q", tc.id, i, r.Link, w.linkSuffix)
			}
		}
		if w.detailsSuffix != "" {
			if r.Details == nil || !strings.HasSuffix(r.Details.String(), w.detailsSuffix) {
				t.Errorf("%s[%d]: details = %v, want suffix %q", tc.id, i, r.Details, w.detailsSuffix)
			}
		}
		if w.magnet {
			if r.MagnetUri == nil || !strings.HasPrefix(r.MagnetUri.String(), "magnet:?xt=") {
				t.Errorf("%s[%d]: magnet = %v", tc.id, i, r.MagnetUri)
			}
		}
		if w.catContains != 0 {
			found := false
			for _, c := range r.Category {
				if c == w.catContains {
					found = true
				}
			}
			if !found {
				t.Errorf("%s[%d]: categories %v lack %d", tc.id, i, r.Category, w.catContains)
			}
		}
		if w.size != 0 {
			var got int64
			if r.Size != nil {
				got = *r.Size
			}
			if r.Size == nil || got != w.size {
				t.Errorf("%s[%d]: size = %d, want %d", tc.id, i, got, w.size)
			}
		}
		if w.seeders != 0 {
			if r.Seeders == nil || *r.Seeders != w.seeders {
				t.Errorf("%s[%d]: seeders = %v, want %d", tc.id, i, r.Seeders, w.seeders)
			}
		}
		if w.imdb != 0 {
			if r.Imdb == nil || *r.Imdb != w.imdb {
				t.Errorf("%s[%d]: imdb = %v, want %d", tc.id, i, r.Imdb, w.imdb)
			}
		}
		if w.dvf != nil {
			if r.DownloadVolumeFactor == nil || *r.DownloadVolumeFactor != *w.dvf {
				t.Errorf("%s[%d]: dvf = %v, want %v", tc.id, i, r.DownloadVolumeFactor, *w.dvf)
			}
		}
		if w.dateYear != 0 && r.PublishDate.Year() != w.dateYear {
			t.Errorf("%s[%d]: year = %d, want %d", tc.id, i, r.PublishDate.Year(), w.dateYear)
		}
	}
}

// TestSampleListPinned verifies samples.txt lists exactly the sampled ids.
func TestSampleListPinned(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("testdata", "samples.txt"))
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		ids = append(ids, strings.Fields(line)[0])
	}
	if len(ids) != 20 {
		t.Fatalf("samples.txt lists %d ids, want 20", len(ids))
	}
	defs := loadSamplesDefs(t)
	for _, id := range ids {
		if defs[id] == nil {
			t.Errorf("sample %s not in definitions", id)
		}
	}
}

// --- Sample batch A: classic HTML keyword-search definitions ---

func TestSample1337x(t *testing.T) {
	defs := loadSamplesDefs(t)
	tc := sampleCase{
		id:      "1337x",
		fixture: "1337x.html",
		query:   searchQuery("example"),
		want: []sampleRelease{
			{title: "Example Movie 2023 1080p", linkSuffix: "/torrent/12345/Example%20Movie%202023%201080p/",
				catContains: 2040, size: 524288000, seeders: 12, dvf: f64(0), dateYear: 2026},
			{title: "Old Show S01E01", linkSuffix: "/torrent/23456/Old%20Show%20S01E01/",
				catContains: 5000, size: 314572800, seeders: 7, dvf: f64(0), dateYear: 2011},
		},
	}
	assertSampleReleases(t, tc, runSample(t, defs, tc))
}

func TestSampleEztv(t *testing.T) {
	defs := loadSamplesDefs(t)
	tc := sampleCase{
		id:      "eztv",
		fixture: "eztv.html",
		query:   searchQuery("example"),
		want: []sampleRelease{
			{title: "Example Show S01E01", magnet: true, catContains: 5000,
				size: 367001600, seeders: 15, dvf: f64(0), dateYear: 2026},
			{title: "Example Movie 2020", magnet: true, catContains: 5000,
				size: 734003200, seeders: 3, dvf: f64(0), dateYear: 2026},
		},
	}
	assertSampleReleases(t, tc, runSample(t, defs, tc))
}

func TestSampleLimetorrents(t *testing.T) {
	defs := loadSamplesDefs(t)
	tc := sampleCase{
		id:      "limetorrents",
		fixture: "limetorrents.html",
		query:   searchQuery("example"),
		want: []sampleRelease{
			{title: "Example Show S01E01", linkSuffix: "/Example-Show-S01E01-torrent-12345.html",
				catContains: 5000, size: 734003200, seeders: 88, dvf: f64(0), dateYear: 2026},
			{title: "Another Movie 2020", linkSuffix: "/Another-Movie-2020-torrent-67890.html",
				catContains: 2000, size: 1468006400, seeders: 200, dvf: f64(0), dateYear: 2025},
		},
	}
	assertSampleReleases(t, tc, runSample(t, defs, tc))
}

func TestSampleTorrentdownloads(t *testing.T) {
	defs := loadSamplesDefs(t)
	tc := sampleCase{
		id:      "torrentdownloads",
		fixture: "torrentdownloads.html",
		query:   searchQuery("example"),
		want: []sampleRelease{
			{title: "Example Downloads", linkSuffix: "/torrent/12345/example+downloads",
				catContains: 5000, size: 524288000, seeders: 150, dvf: f64(0), dateYear: 2026},
			{title: "Another Movie", linkSuffix: "/torrent/67890/another+movie",
				catContains: 2000, size: 1073741824, seeders: 60, dvf: f64(0), dateYear: 2026},
		},
	}
	assertSampleReleases(t, tc, runSample(t, defs, tc))
}

func TestSampleKickasstorrentsWs(t *testing.T) {
	defs := loadSamplesDefs(t)
	tc := sampleCase{
		id:      "kickasstorrents-ws",
		fixture: "kickasstorrents-ws.html",
		query:   searchQuery("example"),
		want: []sampleRelease{
			{title: "Example Release 2023", magnet: true, catContains: 2000,
				size: 838860800, dvf: f64(0), dateYear: 2026},
			{title: "Other Stuff S02E03", magnet: true, catContains: 8000,
				size: 419430400, seeders: 42, dvf: f64(0), dateYear: 2026},
		},
	}
	assertSampleReleases(t, tc, runSample(t, defs, tc))
}

// --- Sample batch B: NexusPHP/TorrentPier/UNIT3D-style private trackers ---

func TestSampleNyaasi(t *testing.T) {
	defs := loadSamplesDefs(t)
	tc := sampleCase{
		id:      "nyaasi",
		fixture: "nyaasi.html",
		query:   searchQuery("example"),
		config:  map[string]string{"prefer_magnet_links": "false"},
		want: []sampleRelease{
			{title: "Example Anime S01E01 1080p", linkSuffix: "/download/12345.torrent",
				magnet: true, catContains: 5070, size: 629145600, seeders: 25, dvf: f64(0), dateYear: 2023},
			{title: "Example Book Volume 1", linkSuffix: "/download/67890.torrent",
				magnet: true, catContains: 7000, size: 10485760, seeders: 9, dvf: f64(0), dateYear: 2023},
		},
	}
	assertSampleReleases(t, tc, runSample(t, defs, tc))
}

func TestSampleAzusa(t *testing.T) {
	defs := loadSamplesDefs(t)
	tc := sampleCase{
		id:      "azusa",
		fixture: "azusa.html",
		query:   searchQuery("example"),
		want: []sampleRelease{
			{title: "Example Comic 2023 digital", linkSuffix: "download.php?id=12345",
				catContains: 7030, size: 629145600, seeders: 10, dvf: f64(0), dateYear: 2023},
			{title: "Example Album 2023 FLAC", linkSuffix: "download.php?id=67890",
				catContains: 3000, size: 314572800, seeders: 5, dvf: f64(1), dateYear: 2023},
		},
	}
	assertSampleReleases(t, tc, runSample(t, defs, tc))
}

func TestSampleMazepa(t *testing.T) {
	defs := loadSamplesDefs(t)
	tc := sampleCase{
		id:      "mazepa",
		fixture: "mazepa.html",
		query:   searchQuery("example"),
		want: []sampleRelease{
			{title: "Example Show S2E3 1080p", magnet: true,
				catContains: 5040, size: 629145600, seeders: 15, dvf: f64(0), dateYear: 2023},
			{title: "Example Movie 2023 1080p", magnet: true,
				catContains: 2040, size: 1073741824, seeders: 40, dvf: f64(0), dateYear: 2023},
		},
	}
	assertSampleReleases(t, tc, runSample(t, defs, tc))
}

func TestSample0daykiev(t *testing.T) {
	defs := loadSamplesDefs(t)
	tc := sampleCase{
		id:      "0daykiev",
		fixture: "0daykiev.html",
		query:   searchQuery("example"),
		want: []sampleRelease{
			{title: "Example Release 2023 1080p", linkSuffix: "download.php?id=12345",
				catContains: 2040, size: 629145600, seeders: 15, dvf: f64(0), dateYear: 2023},
			{title: "Another Show S02E01 1080p", linkSuffix: "download.php?id=67890",
				catContains: 5000, size: 1073741824, seeders: 0, dvf: f64(0.5), dateYear: 2023},
		},
	}
	assertSampleReleases(t, tc, runSample(t, defs, tc))
}

func TestSampleBjshare(t *testing.T) {
	defs := loadSamplesDefs(t)
	tc := sampleCase{
		id:      "bjshare",
		fixture: "bjshare.html",
		query:   searchQuery("example"),
		want: []sampleRelease{
			{title: "Example Show S01E01 1080p", linkSuffix: "torrents.php?action=download&id=12345",
				catContains: 5000, size: 734003200, seeders: 10, imdb: 1234567, dvf: f64(1), dateYear: 2023},
			{title: "Example Movie 2020 720p", linkSuffix: "torrents.php?action=download&id=67890",
				catContains: 2000, size: 838860800, seeders: 5, imdb: 7654321, dvf: f64(1), dateYear: 2023},
		},
	}
	assertSampleReleases(t, tc, runSample(t, defs, tc))
}

// --- Sample batch C: JSON/API definitions ---

func TestSampleYts(t *testing.T) {
	defs := loadSamplesDefs(t)
	tc := sampleCase{
		id:         "yts",
		fixture:    "yts.json",
		query:      searchQuery("example"),
		config:     map[string]string{"apiurl": "$SERVER"},
		stripHTTPS: true,
		want: []sampleRelease{
			{title: "Example Movie (2023) 1080p WEBRip 5.1 10Bit x264 -YTS", linkSuffix: "torrent/download/AAAA1111",
				catContains: 2040, size: 1610612736, seeders: 15, imdb: 1234567, dvf: f64(0), dateYear: 2023},
			{title: "Other Film (2020) 720p BRRip x264 -YTS", linkSuffix: "torrent/download/BBBB2222",
				catContains: 2040, size: 734003200, seeders: 8, imdb: 7654321, dvf: f64(0), dateYear: 2020},
		},
	}
	assertSampleReleases(t, tc, runSample(t, defs, tc))
}

func TestSampleThepiratebay(t *testing.T) {
	defs := loadSamplesDefs(t)
	tc := sampleCase{
		id:         "thepiratebay",
		fixture:    "thepiratebay.json",
		query:      searchQuery("example"),
		config:     map[string]string{"apiurl": "$SERVER"},
		stripHTTPS: true,
		want: []sampleRelease{
			{title: "Example Show S01E01 1080p", detailsSuffix: "description.php?id=111", magnet: true,
				catContains: 2000, size: 1610612736, seeders: 15, imdb: 1234567, dvf: f64(0), dateYear: 2023},
			{title: "Example Movie 2020 720p", detailsSuffix: "description.php?id=222", magnet: true,
				catContains: 5000, size: 734003200, seeders: 8, imdb: 7654321, dvf: f64(0), dateYear: 2023},
		},
	}
	assertSampleReleases(t, tc, runSample(t, defs, tc))
}

func TestSampleAcgrip(t *testing.T) {
	defs := loadSamplesDefs(t)
	tc := sampleCase{
		id:      "acgrip",
		fixture: "acgrip.html",
		query:   searchQuery("example"),
		want: []sampleRelease{
			{title: "Example Show S01E01", linkSuffix: "/dl/12345.torrent",
				catContains: 5000, size: 629145600, dvf: f64(0), dateYear: 2023},
			{title: "Example Show S01E02", linkSuffix: "/dl/67890.torrent",
				catContains: 5000, size: 314572800, dvf: f64(0), dateYear: 2023},
		},
	}
	assertSampleReleases(t, tc, runSample(t, defs, tc))
}

func TestSampleAnthelionApi(t *testing.T) {
	defs := loadSamplesDefs(t)
	tc := sampleCase{
		id:      "anthelion-api",
		fixture: "anthelion-api.json",
		query:   searchQuery("example"),
		config:  map[string]string{"apikey": "dummy"},
		want: []sampleRelease{
			{title: "Example Movie 2023 1080p WEB-DL MKV x264 AAC-GRP", linkSuffix: "torrents/download.php?id=12345",
				catContains: 2000, size: 1610612736, seeders: 15, imdb: 1234567, dvf: f64(1), dateYear: 2022},
		},
	}
	assertSampleReleases(t, tc, runSample(t, defs, tc))
}

func TestSample0dayfilesApi(t *testing.T) {
	defs := loadSamplesDefs(t)
	tc := sampleCase{
		id:      "0dayfiles-api",
		fixture: "0dayfiles-api.json",
		query:   searchQuery("example"),
		config:  map[string]string{"apikey": "dummy"},
		want: []sampleRelease{
			{title: "Example Release 2023 1080p", linkSuffix: "torrents/download/123",
				catContains: 2040, size: 1610612736, seeders: 10, imdb: 1234567, dvf: f64(0.75), dateYear: 2021},
		},
	}
	assertSampleReleases(t, tc, runSample(t, defs, tc))
}

// --- Sample batch D: torznab-XML feeds and rows features (dateheaders/after) ---

func TestSampleBitmagnet(t *testing.T) {
	defs := loadSamplesDefs(t)
	tc := sampleCase{
		id:      "bitmagnet",
		fixture: "bitmagnet.xml",
		query:   searchQuery("example"),
		want: []sampleRelease{
			{title: "Example Movie 2023 1080p", magnet: true,
				catContains: 2040, size: 1610612736, seeders: 15, dvf: f64(0), dateYear: 2023},
			{title: "Example Show S01E01 720p", magnet: true,
				catContains: 5000, size: 734003200, seeders: 9, dvf: f64(0), dateYear: 2023},
		},
	}
	assertSampleReleases(t, tc, runSample(t, defs, tc))
}

func TestSampleAnimetoshoXyz(t *testing.T) {
	defs := loadSamplesDefs(t)
	tc := sampleCase{
		id:         "animetosho-xyz",
		fixture:    "animetosho.xml",
		query:      searchQuery("example"),
		config:     map[string]string{"api-url": "$SERVER"},
		stripHTTPS: true,
		want: []sampleRelease{
			{title: "[Group] Example Anime - 01 [1080p]", linkSuffix: "/dl/12345/example.torrent",
				magnet: true, catContains: 5070, size: 1610612736, seeders: 15, dvf: f64(0), dateYear: 2026},
		},
	}
	assertSampleReleases(t, tc, runSample(t, defs, tc))
}

func TestSampleAnibt(t *testing.T) {
	defs := loadSamplesDefs(t)
	tc := sampleCase{
		id:      "anibt",
		fixture: "anibt.xml",
		query:   searchQuery("example", 5070),
		want: []sampleRelease{
			{title: "[Group] Example Anime 01 1080p", magnet: true,
				catContains: 5070, size: 1610612736, dvf: f64(0), dateYear: 2026},
			{title: "[Group] Example Anime 02 1080p", magnet: true,
				catContains: 5070, size: 734003200, dvf: f64(0), dateYear: 2026},
		},
	}
	assertSampleReleases(t, tc, runSample(t, defs, tc))
}

func TestSampleBoxingtorrents(t *testing.T) {
	defs := loadSamplesDefs(t)
	tc := sampleCase{
		id:      "boxingtorrents",
		fixture: "boxingtorrents.html",
		query:   searchQuery("example"),
		want: []sampleRelease{
			{title: "Example Bout 2023 1080p", linkSuffix: "details.php?id=12345",
				catContains: 5060, size: 629145600, seeders: 10, dvf: f64(1), dateYear: 2023},
			{title: "Example Bout Classics", linkSuffix: "details.php?id=67890",
				catContains: 5060, size: 314572800, seeders: 3, dvf: f64(0), dateYear: 2023},
		},
	}
	assertSampleReleases(t, tc, runSample(t, defs, tc))
}

func TestSampleBigcore(t *testing.T) {
	defs := loadSamplesDefs(t)
	tc := sampleCase{
		id:      "bigcore",
		fixture: "bigcore.html",
		query:   searchQuery("example"),
		want: []sampleRelease{
			{title: "Example Release 2023 1080p", linkSuffix: "download.php?torrent=12345",
				catContains: 2040, size: 629145600, seeders: 10, dvf: f64(0), dateYear: 2023},
			{title: "Example Show S01E01 720p", linkSuffix: "download.php?torrent=67890",
				catContains: 5000, size: 419430400, seeders: 5, dvf: f64(1), dateYear: 2023},
		},
	}
	assertSampleReleases(t, tc, runSample(t, defs, tc))
}
