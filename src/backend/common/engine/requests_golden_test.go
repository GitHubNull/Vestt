package engine

import (
	"context"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"vestt/common/models"
)

// SPEC-05 acceptance #5: 选取上游代表定义 ≥ 12 个，断言生成的
// URL/Method/Body/Headers 与 golden 文件一致。
//
// Note: 上游 0.24.2900 的 searchPathBlock 没有 pagination 字段，PerformQuery
// 也没有 before/keywordsall 块（见 SPEC-05 澄清记录），因此覆盖维度为
// tvsearch/movie/search/music/book、category 模板与 intersect/"!" 取反、
// $raw、keywordsfilters、POST body、AllowEmptyInputs、querystring 编码。

var updateGolden = flag.Bool("update", false, "rewrite golden files")

func intPtr(v int) *int { return &v }

func tvQuery(term string, season int, ep string, cats ...int) *models.TorznabQuery {
	q := models.NewTorznabQuery()
	q.QueryType = "tvsearch"
	q.SearchTerm = term
	q.Season = intPtr(season)
	q.Episode = ep
	q.Categories = cats
	return q
}

func searchQuery(term string, cats ...int) *models.TorznabQuery {
	q := models.NewTorznabQuery()
	q.QueryType = "search"
	q.SearchTerm = term
	q.Categories = cats
	return q
}

func TestSearchRequestsGolden(t *testing.T) {
	cases := []struct {
		id  string
		q   *models.TorznabQuery
		doc string
	}{
		{"1337x", tvQuery("Some Show", 2023, "", 5000), "tvsearch + keywordsfilters(S2023→2023) + if/else 路径"},
		{"thepiratebay", searchQuery("ubuntu iso", 2000), "search q + category"},
		{"torrentdownloads", tvQuery("Some Show", 2, "5"), "tvsearch SxxEyy token"},
		{"eztv", tvQuery("Some Show", 1, "2"), "tvsearch"},
		{"yts", searchQuery("Some Movie", 2000), "movie keywords"},
		{"limetorrents", searchQuery("ubuntu"), "search q"},
		{"kickasstorrents-ws", searchQuery("Some Show"), "search q"},
		{"nyaasi", searchQuery("ubuntu", 5070), "anime category 模板"},
		{"acgrip", searchQuery("ubuntu"), "search q"},
		{"bjshare", searchQuery("Some Show", 5000), "多 search paths + categories intersect"},
		{"0daykiev", tvQuery("Some Show", 1, "2", 5000), "$raw + keywordsfilters(乌克兰语 windows-1251) + GET"},
		{"0dayfiles-api", tvQuery("Some Show", 2, "3", 5000), "json api + headers + $raw + id params"},
		{"anthelion-api", searchQuery("Some Movie"), "movie api + imdb/tmdb"},
		{"azusa", searchQuery(""), "AllowEmptyInputs + rss"},
		{"mazepa", tvQuery("Some Show", 1, "2", 5000), "POST body + $raw if/else categories"},
		{"0dayfiles-api#music", musicQuery(), "music search"},
		{"0dayfiles-api#book", bookQuery(), "book search"},
	}

	defs, _, err := LoadAll(discardLogger(), "")
	if err != nil {
		t.Fatalf("LoadAll: %v", err)
	}

	for _, tc := range cases {
		t.Run(tc.id, func(t *testing.T) {
			baseID := strings.TrimSuffix(strings.TrimSuffix(tc.id, "#music"), "#book")
			def, ok := defs[baseID]
			if !ok {
				t.Fatalf("definition %s not loaded", baseID)
			}
			// deterministic site link (generation only, no network)
			def.Links = []string{"https://example.test/"}

			x, err := NewCardigann(def, discardLogger())
			if err != nil {
				t.Fatalf("NewCardigann: %v", err)
			}

			requests, err := x.searchRequests(context.Background(), tc.q, x.queryVariables(tc.q))
			if err != nil {
				t.Fatalf("searchRequests: %v", err)
			}
			if len(requests) == 0 {
				t.Fatalf("no requests generated")
			}

			golden := filepath.Join("testdata", "golden", "search", tc.id+".json")
			got := goldenJSON(t, requests)

			if *updateGolden {
				if err := os.MkdirAll(filepath.Dir(golden), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(golden, got, 0o644); err != nil {
					t.Fatal(err)
				}
				return
			}
			want, err := os.ReadFile(golden)
			if err != nil {
				t.Fatalf("read golden %s: %v (run with -update)", golden, err)
			}
			if string(got) != string(want) {
				t.Errorf("requests mismatch for %s (%s)\n got: %s\nwant: %s", tc.id, tc.doc, got, want)
			}
		})
	}
}

// goldenJSON serializes the generated request chain deterministically.
func goldenJSON(t *testing.T, requests []searchRequest) []byte {
	t.Helper()
	type goldenReq struct {
		Method  string            `json:"method"`
		URL     string            `json:"url"`
		Headers map[string]string `json:"headers,omitempty"`
		Body    string            `json:"body,omitempty"`
	}
	out := make([]goldenReq, 0, len(requests))
	for _, r := range requests {
		out = append(out, goldenReq{Method: r.Method, URL: r.URL, Headers: r.Headers, Body: r.RawBody})
	}
	b, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	return append(b, '\n')
}

func musicQuery() *models.TorznabQuery {
	q := models.NewTorznabQuery()
	q.QueryType = "music"
	q.Artist = "Some Artist"
	q.Album = "Some Album"
	return q
}

func bookQuery() *models.TorznabQuery {
	q := models.NewTorznabQuery()
	q.QueryType = "book"
	q.Author = "Some Author"
	q.Title = "Some Title"
	return q
}
