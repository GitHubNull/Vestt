package models

import (
	"net/url"
	"testing"
)

func TestCategoryTable(t *testing.T) {
	all := AllCategories()
	if len(all) != 71 {
		t.Fatalf("expected 71 categories (TorznabCatType.AllCats), got %d", len(all))
	}
	if len(ParentCats) != 8 {
		t.Fatalf("expected 8 parent categories, got %d", len(ParentCats))
	}

	// 15 representative descriptions (SPEC-03 §5.1)
	cases := map[int]string{
		1000: "Console", 2000: "Movies", 2010: "Movies/Foreign", 2040: "Movies/HD",
		3000: "Audio", 3030: "Audio/Audiobook", 4000: "PC", 4050: "PC/Games",
		5000: "TV", 5030: "TV/SD", 5040: "TV/HD", 6000: "XXX", 7000: "Books",
		7030: "Books/Comics", 8000: "Other",
	}
	for id, want := range cases {
		if got := GetCatDesc(id); got != want {
			t.Fatalf("GetCatDesc(%d) = %q, want %q", id, got, want)
		}
	}
	if got := GetCatDesc(999999); got != "" {
		t.Fatalf("unknown id should give empty string, got %q", got)
	}
	if GetCatByName("Movies/HD") == nil || GetCatByName("Nope") != nil {
		t.Fatal("GetCatByName mismatch")
	}

	// sub-category tree integrity
	if len(CatMovies.SubCategories) != 9 || len(CatConsole.SubCategories) != 14 {
		t.Fatalf("sub-category tree mismatch: movies=%d console=%d",
			len(CatMovies.SubCategories), len(CatConsole.SubCategories))
	}
	if !CatTV.Contains(CatTVHD) || CatTV.Contains(CatMoviesHD) {
		t.Fatal("Contains mismatch")
	}
}

func TestParseTorznabCategories(t *testing.T) {
	if got := ParseTorznabCategories(""); len(got) != 0 {
		t.Fatalf("empty csv should give empty slice, got %v", got)
	}
	got := ParseTorznabCategories("2000,2040, 3000 ,,abc,999999")
	want := []int{2000, 2040, 3000, 999999}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
}

func mustParse(t *testing.T, query string) url.Values {
	t.Helper()
	v, err := url.ParseQuery(query)
	if err != nil {
		t.Fatalf("parse %q: %v", query, err)
	}
	return v
}

func TestParseTorznabQueryMatrix(t *testing.T) {
	cases := []struct {
		name  string
		query string
		check func(t *testing.T, q *TorznabQuery)
	}{
		{"defaults", "", func(t *testing.T, q *TorznabQuery) {
			if q.QueryType != "search" || q.Cache != true || len(q.Categories) != 0 {
				t.Fatalf("defaults: %+v", q)
			}
		}},
		{"search q", "t=search&q=foo+bar", func(t *testing.T, q *TorznabQuery) {
			if q.SearchTerm != "foo bar" || !q.IsSearch() {
				t.Fatalf("q: %+v", q)
			}
		}},
		{"tvsearch", "t=tvsearch&q=show&season=2&ep=3", func(t *testing.T, q *TorznabQuery) {
			if !q.IsTVSearch() || q.Season == nil || *q.Season != 2 || q.Episode != "3" {
				t.Fatalf("tv: %+v", q)
			}
		}},
		{"movie", "t=movie&q=film", func(t *testing.T, q *TorznabQuery) {
			if !q.IsMovieSearch() {
				t.Fatalf("movie: %+v", q)
			}
		}},
		{"music", "t=music&album=a&artist=b&label=l&track=tr&year=1999&genre=g", func(t *testing.T, q *TorznabQuery) {
			if !q.IsMusicSearch() || q.Album != "a" || q.Artist != "b" || q.Label != "l" ||
				q.Track != "tr" || q.Year == nil || *q.Year != 1999 || q.Genre != "g" {
				t.Fatalf("music: %+v", q)
			}
		}},
		{"book", "t=book&title=tt&author=aa&publisher=pp", func(t *testing.T, q *TorznabQuery) {
			// upstream bug preserved: publisher lands in Author (overwrites)
			if !q.IsBookSearch() || q.Title != "tt" || q.Author != "pp" {
				t.Fatalf("book: %+v", q)
			}
		}},
		{"cats", "t=search&cat=2000,2040", func(t *testing.T, q *TorznabQuery) {
			if len(q.Categories) != 2 || q.Categories[1] != 2040 {
				t.Fatalf("cats: %+v", q)
			}
		}},
		{"cats invalid tolerated", "t=search&cat=2000,abc,2040", func(t *testing.T, q *TorznabQuery) {
			if len(q.Categories) != 2 {
				t.Fatalf("cats invalid: %+v", q)
			}
		}},
		{"movie imdbid default cats", "t=movie&imdbid=tt1234567", func(t *testing.T, q *TorznabQuery) {
			if len(q.Categories) != 1 || q.Categories[0] != 2000 {
				t.Fatalf("movie imdb cats: %+v", q)
			}
		}},
		{"imdbid tt prefix", "t=movie&imdbid=tt7654321", func(t *testing.T, q *TorznabQuery) {
			if q.ImdbID != "tt7654321" || q.ImdbIDShort() != "7654321" {
				t.Fatalf("imdb: %+v", q)
			}
		}},
		{"extended limit offset", "t=search&q=x&extended=1&limit=50&offset=10", func(t *testing.T, q *TorznabQuery) {
			if q.Extended != 1 || q.Limit != 50 || q.Offset != 10 {
				t.Fatalf("ext: %+v", q)
			}
		}},
		{"invalid ints tolerated", "t=search&q=x&extended=zz&limit=&offset=-1", func(t *testing.T, q *TorznabQuery) {
			if q.Extended != 0 || q.Limit != 0 || q.Offset != -1 {
				t.Fatalf("invalid ints: %+v", q)
			}
		}},
		{"cache false", "t=search&q=x&cache=false", func(t *testing.T, q *TorznabQuery) {
			if q.Cache != false {
				t.Fatalf("cache: %+v", q)
			}
		}},
		{"cache invalid keeps default", "t=search&q=x&cache=maybe", func(t *testing.T, q *TorznabQuery) {
			if q.Cache != true {
				t.Fatalf("cache invalid: %+v", q)
			}
		}},
		{"rid tvdbid tvmazeid", "t=tvsearch&rid=123&tvdbid=456&tvmazeid=789", func(t *testing.T, q *TorznabQuery) {
			if q.RageID == nil || *q.RageID != 123 || q.TvdbID == nil || *q.TvdbID != 456 ||
				q.TvmazeID == nil || *q.TvmazeID != 789 {
				t.Fatalf("ids: %+v", q)
			}
		}},
		{"tmdbid traktid doubanid", "t=movie&tmdbid=1&traktid=2&doubanid=3", func(t *testing.T, q *TorznabQuery) {
			if q.TmdbID == nil || *q.TmdbID != 1 || q.TraktID == nil || *q.TraktID != 2 ||
				q.DoubanID == nil || *q.DoubanID != 3 {
				t.Fatalf("ids2: %+v", q)
			}
		}},
		{"season invalid tolerated", "t=tvsearch&season=abc", func(t *testing.T, q *TorznabQuery) {
			if q.Season != nil {
				t.Fatalf("season invalid: %+v", q)
			}
		}},
		{"whitespace season", "t=tvsearch&season=%202%20", func(t *testing.T, q *TorznabQuery) {
			if q.Season == nil || *q.Season != 2 {
				t.Fatalf("season ws: %+v", q)
			}
		}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tc.check(t, ParseTorznabQuery(mustParse(t, tc.query)))
		})
	}
}

func TestQueryPredicates(t *testing.T) {
	q := ParseTorznabQuery(mustParse(t, "t=movie&imdbid=tt1234567"))
	if !q.IsMovieSearch() || !q.IsImdbQuery() || q.IsIdSearch() != true {
		t.Fatalf("movie imdb predicates: %+v", q)
	}
	if q.IsRssSearch() {
		t.Fatal("id query must not be rss")
	}

	// TorrentPotato with term counts as movie search
	potato := NewTorznabQuery()
	potato.QueryType = "TorrentPotato"
	potato.SearchTerm = "some movie"
	if !potato.IsMovieSearch() {
		t.Fatal("potato with term should be movie search")
	}

	// plain term query is rss
	rss := NewTorznabQuery()
	rss.QueryType = "search"
	if !rss.IsRssSearch() {
		t.Fatal("empty search should be rss")
	}

	// season counts as id search
	tv := ParseTorznabQuery(mustParse(t, "t=tvsearch&season=1"))
	if !tv.IsIdSearch() || tv.IsRssSearch() {
		t.Fatalf("season id search: %+v", tv)
	}
	if tv.GetEpisodeSearchString() != "S01" {
		t.Fatalf("episode string: %q", tv.GetEpisodeSearchString())
	}

	// year counts as id search
	y := ParseTorznabQuery(mustParse(t, "t=music&year=2020"))
	if !y.IsIdSearch() {
		t.Fatal("year should be id search")
	}
}

func TestGetQueryString(t *testing.T) {
	cases := []struct {
		name  string
		query string
		want  string
	}{
		{"plain", "t=search&q=foo", "foo"},
		{"season ep", "t=tvsearch&q=show&season=2&ep=3", "show S02E03"},
		{"season only", "t=tvsearch&q=show&season=2", "show S02"},
		{"dash standardization", "t=search&q=foo–bar—baz", "foo-bar-baz"},
		{"quote standardization", "t=search&q=it%60s%20ok%20don%C2%B4t", "it's ok don't"},
		{"airdate", "t=tvsearch&q=show&season=2020&ep=10/24", "show 2020.10.24"},
		{"unsafe chars stripped", "t=search&q=a%3Cb%3Ec*d%3Fe%21f", "abcdef"},
		{"allowed punctuation kept", "t=search&q=a.b_c(d)%40e%2Ff%5Bg%5Dh%2Bi%25j", "a.b_c(d)@e/f[g]h+i%j"},
		{"no term season only", "t=tvsearch&season=1&ep=2", "S01E02"},
		{"multi whitespace kept as-is", "t=search&q=foo%20%20bar", "foo  bar"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			q := ParseTorznabQuery(mustParse(t, tc.query))
			if got := q.GetQueryString(); got != tc.want {
				t.Fatalf("GetQueryString = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestCloneAndFallback(t *testing.T) {
	q := ParseTorznabQuery(mustParse(t, "t=search&q=x&cat=2000"))
	cl := q.Clone()
	cl.Categories[0] = 9999
	if q.Categories[0] != 2000 {
		t.Fatal("Clone must deep-copy Categories")
	}

	fb := q.CreateFallback("fallback term")
	if fb.SearchTerm != "fallback term" || len(fb.Categories) != 1 {
		t.Fatalf("fallback with existing cats: %+v", fb)
	}
	empty := NewTorznabQuery()
	fb2 := empty.CreateFallback("term")
	if len(fb2.Categories) != 10 || fb2.Categories[0] != 2000 {
		t.Fatalf("fallback default cats: %v", fb2.Categories)
	}
}

func TestMatchQueryStringAND(t *testing.T) {
	q := ParseTorznabQuery(mustParse(t, "t=search&q=foo+bar"))
	if !q.MatchQueryStringAND("Foo Bar 2020", 0, "") {
		t.Fatal("should match")
	}
	if q.MatchQueryStringAND("Foo Only", 0, "") {
		t.Fatal("should not match")
	}
	// common words excluded
	q2 := ParseTorznabQuery(mustParse(t, "t=search&q=the+and+an"))
	if !q2.MatchQueryStringAND("anything at all", 0, "") {
		t.Fatal("common words should be ignored")
	}
	// override
	if !q.MatchQueryStringAND("just foo here", 0, "foo") {
		t.Fatal("override should match foo only")
	}
}

func TestReleaseInfo(t *testing.T) {
	size := int64(1024 * 1024 * 1024) // 1 GiB
	seeders := int64(10)
	r := &ReleaseInfo{Size: &size, Seeders: &seeders, Category: []int{2000}, Languages: []string{"en"}}
	cl := r.Clone()
	cl.Category[0] = 9999
	cl.Languages[0] = "fr"
	if r.Category[0] != 2000 || r.Languages[0] != "en" {
		t.Fatal("Clone must deep-copy collections")
	}
	gain := r.Gain()
	if gain == nil || *gain != 10.0 {
		t.Fatalf("Gain = %v, want 10", gain)
	}
	noSeeders := &ReleaseInfo{Size: &size}
	if noSeeders.Gain() != nil {
		t.Fatal("nil Seeders → nil Gain")
	}
	if GigabytesFromBytes(nil) != nil {
		t.Fatal("nil size → nil GB")
	}
}
