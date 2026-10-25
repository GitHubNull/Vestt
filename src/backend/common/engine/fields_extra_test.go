// parseFields unit tests (SPEC-06 §3.4): every field case, the append /
// noappend modifiers and the peers/seeders accumulation rules.
package engine

import (
	"net/url"
	"strings"
	"testing"

	"vestt/common/models"
)

func mustParseURL(raw string) *url.URL {
	u, _ := url.Parse(raw)
	return u
}

func fieldsCardigann(t *testing.T) *Cardigann {
	t.Helper()
	x, err := NewCardigann(&IndexerDefinition{
		ID:    "fieldstest",
		Name:  "Fields Test",
		Links: []string{"https://example.test/"},
		Caps: &CapsBlock{
			Categorymappings: []CategoryMappingBlock{
				{ID: "7", Cat: "TV", Desc: "TV Shows"},
				{ID: "4", Cat: "Movies"},
			},
			Modes: StringListMap{{Key: "search", Values: []string{"q"}}},
		},
		Search: &SearchBlock{Path: "/search"},
	}, discardLogger())
	if err != nil {
		t.Fatalf("NewCardigann: %v", err)
	}
	return x
}

func TestParseFieldsScalars(t *testing.T) {
	x := fieldsCardigann(t)
	cases := []struct {
		name  string
		field string
		value string
		want  string
	}{
		{"infohash", "infohash", "ABC123", "ABC123"},
		{"title plain", "title", "Hello", "Hello"},
		{"description", "description", "desc text", "desc text"},
		{"size", "size", "1.5 GB", "1610612736"},
		{"files", "files", "12", "12"},
		{"grabs", "grabs", "34", "34"},
		{"dvf", "downloadvolumefactor", "0.5", "0.5"},
		{"uvf", "uploadvolumefactor", "1", "1"},
		{"minratio", "minimumratio", "1.5", "1.5"},
		{"minseedtime", "minimumseedtime", "3600", "3600"},
		{"imdb", "imdb", "tt1234567", "1234567"},
		{"imdbid alias", "imdbid", "tt7654321", "7654321"},
		{"tmdbid", "tmdbid", "999", "999"},
		{"rageid", "rageid", "11", "11"},
		{"tvdbid", "tvdbid", "12", "12"},
		{"tvmazeid", "tvmazeid", "13", "13"},
		{"traktid", "traktid", "14", "14"},
		{"doubanid", "doubanid", "15", "15"},
		{"year", "year", "2020", "2020"},
		{"author", "author", "A", "A"},
		{"booktitle", "booktitle", "B", "B"},
		{"publisher", "publisher", "P", "P"},
		{"artist", "artist", "Ar", "Ar"},
		{"album", "album", "Al", "Al"},
		{"label", "label", "L", "L"},
		{"track", "track", "T", "T"},
		{"unknown passthrough", "totallyunknown", "kept", "kept"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rel := &models.ReleaseInfo{}
			got, err := x.parseFields(tc.value, tc.field, rel, nil, "")
			if err != nil {
				t.Fatalf("parseFields: %v", err)
			}
			if got != tc.want {
				t.Errorf("value = %q, want %q", got, tc.want)
			}
			switch tc.field {
			case "infohash":
				if rel.InfoHash != "ABC123" {
					t.Errorf("InfoHash = %q", rel.InfoHash)
				}
			case "size":
				if rel.Size == nil || *rel.Size != 1610612736 {
					t.Errorf("Size = %v", rel.Size)
				}
			case "files":
				if rel.Files == nil || *rel.Files != 12 {
					t.Errorf("Files = %v", rel.Files)
				}
			case "grabs":
				if rel.Grabs == nil || *rel.Grabs != 34 {
					t.Errorf("Grabs = %v", rel.Grabs)
				}
			case "downloadvolumefactor":
				if rel.DownloadVolumeFactor == nil || *rel.DownloadVolumeFactor != 0.5 {
					t.Errorf("DVF = %v", rel.DownloadVolumeFactor)
				}
			case "uploadvolumefactor":
				if rel.UploadVolumeFactor == nil || *rel.UploadVolumeFactor != 1 {
					t.Errorf("UVF = %v", rel.UploadVolumeFactor)
				}
			case "minimumratio":
				if rel.MinimumRatio == nil || *rel.MinimumRatio != 1.5 {
					t.Errorf("MinimumRatio = %v", rel.MinimumRatio)
				}
			case "minimumseedtime":
				if rel.MinimumSeedTime == nil || *rel.MinimumSeedTime != 3600 {
					t.Errorf("MinimumSeedTime = %v", rel.MinimumSeedTime)
				}
			case "imdb", "imdbid":
				want := int64(1234567)
				if tc.field == "imdbid" {
					want = 7654321
				}
				if rel.Imdb == nil || *rel.Imdb != want {
					t.Errorf("Imdb = %v, want %d", rel.Imdb, want)
				}
			case "tmdbid":
				if rel.TMDb == nil || *rel.TMDb != 999 {
					t.Errorf("TMDb = %v", rel.TMDb)
				}
			case "rageid":
				if rel.RageID == nil || *rel.RageID != 11 {
					t.Errorf("RageID = %v", rel.RageID)
				}
			case "tvdbid":
				if rel.TVDBId == nil || *rel.TVDBId != 12 {
					t.Errorf("TVDBId = %v", rel.TVDBId)
				}
			case "tvmazeid":
				if rel.TVMazeId == nil || *rel.TVMazeId != 13 {
					t.Errorf("TVMazeId = %v", rel.TVMazeId)
				}
			case "traktid":
				if rel.TraktId == nil || *rel.TraktId != 14 {
					t.Errorf("TraktId = %v", rel.TraktId)
				}
			case "doubanid":
				if rel.DoubanId == nil || *rel.DoubanId != 15 {
					t.Errorf("DoubanId = %v", rel.DoubanId)
				}
			case "year":
				if rel.Year == nil || *rel.Year != 2020 {
					t.Errorf("Year = %v", rel.Year)
				}
			}
		})
	}
}

func TestParseFieldsAppendModifiers(t *testing.T) {
	x := fieldsCardigann(t)
	rel := &models.ReleaseInfo{}
	v, err := x.parseFields("Alpha", "title", rel, nil, "")
	if err != nil || v != "Alpha" || rel.Title != "Alpha" {
		t.Fatalf("title = %q %v %q", v, err, rel.Title)
	}
	// append modifier concatenates
	v, err = x.parseFields(" 1080p", "title", rel, []string{"append"}, "")
	if err != nil || v != "Alpha 1080p" || rel.Title != "Alpha 1080p" {
		t.Fatalf("title append = %q %v %q", v, err, rel.Title)
	}
	v, err = x.parseFields("first", "description", rel, nil, "")
	if err != nil || rel.Description != "first" {
		t.Fatalf("description = %q %v", v, err)
	}
	v, err = x.parseFields("+more", "description", rel, []string{"append"}, "")
	if err != nil || rel.Description != "first+more" {
		t.Fatalf("description append = %q %v", v, err)
	}
}

func TestParseFieldsDownloadVariants(t *testing.T) {
	x := fieldsCardigann(t)
	// empty download clears the link
	rel := &models.ReleaseInfo{Link: mustParseURL("https://example.test/old")}
	v, err := x.parseFields("", "download", rel, nil, "")
	if err != nil || v != "" || rel.Link != nil {
		t.Fatalf("empty download = %q %v link=%v", v, err, rel.Link)
	}
	// magnet download sets MagnetUri only
	rel = &models.ReleaseInfo{}
	magnet := "magnet:?xt=urn:btih:ABCDEF0123456789&dn=x"
	v, err = x.parseFields(magnet, "download", rel, nil, "")
	if err != nil || rel.Link != nil || rel.MagnetUri == nil || !strings.HasPrefix(v, "magnet:?") {
		t.Fatalf("magnet download = %q %v %+v", v, err, rel)
	}
	// relative download URL resolves against the search URL
	rel = &models.ReleaseInfo{}
	v, err = x.parseFields("/dl/1", "download", rel, nil, "https://example.test/search")
	if err != nil || rel.Link == nil || rel.Link.String() != "https://example.test/dl/1" || v != rel.Link.String() {
		t.Fatalf("relative download = %q %v %+v", v, err, rel.Link)
	}
	// magnet field parses the URI
	rel = &models.ReleaseInfo{}
	v, err = x.parseFields(magnet, "magnet", rel, nil, "")
	if err != nil || rel.MagnetUri == nil || rel.MagnetUri.String() != magnet {
		t.Fatalf("magnet field = %q %v %+v", v, err, rel.MagnetUri)
	}
	// details resolve like download
	rel = &models.ReleaseInfo{}
	v, err = x.parseFields("/details/9", "details", rel, nil, "https://example.test/search")
	if err != nil || rel.Details == nil || rel.Details.String() != "https://example.test/details/9" {
		t.Fatalf("details = %q %v %+v", v, err, rel.Details)
	}
	// poster: empty → empty value, no URL
	rel = &models.ReleaseInfo{}
	v, err = x.parseFields("", "poster", rel, nil, "https://example.test/search")
	if err != nil || v != "" || rel.Poster != nil {
		t.Fatalf("empty poster = %q %v %+v", v, err, rel.Poster)
	}
	// poster: relative URL resolved
	v, err = x.parseFields("/p.jpg", "poster", rel, nil, "https://example.test/search")
	if err != nil || rel.Poster == nil || rel.Poster.String() != "https://example.test/p.jpg" || v != rel.Poster.String() {
		t.Fatalf("poster = %q %v %+v", v, err, rel.Poster)
	}
}

func TestParseFieldsSeedersLeechers(t *testing.T) {
	x := fieldsCardigann(t)
	rel := &models.ReleaseInfo{}
	// seeders first: sets seeders and peers
	if _, err := x.parseFields("10", "seeders", rel, nil, ""); err != nil {
		t.Fatal(err)
	}
	if rel.Seeders == nil || *rel.Seeders != 10 || rel.Peers == nil || *rel.Peers != 10 {
		t.Fatalf("seeders: %+v", rel)
	}
	// leechers next: accumulates into peers
	if _, err := x.parseFields("5", "leechers", rel, nil, ""); err != nil {
		t.Fatal(err)
	}
	if rel.Peers == nil || *rel.Peers != 15 {
		t.Fatalf("peers = %v, want 15", rel.Peers)
	}
	// runaway values clamp to 0 (upstream fix #6558)
	rel2 := &models.ReleaseInfo{}
	if _, err := x.parseFields("6000000", "seeders", rel2, nil, ""); err != nil {
		t.Fatal(err)
	}
	if *rel2.Seeders != 0 || *rel2.Peers != 0 {
		t.Fatalf("clamped: %+v", rel2)
	}
}

func TestParseFieldsDateError(t *testing.T) {
	x := fieldsCardigann(t)
	rel := &models.ReleaseInfo{}
	if _, err := x.parseFields("definitely not a date", "date", rel, nil, ""); err == nil {
		t.Fatal("date parse failure must error (propagates to row drop)")
	}
	if _, err := x.parseFields("24.05.2023", "date", rel, nil, ""); err != nil {
		t.Fatalf("valid date: %v", err)
	}
	if rel.PublishDate.IsZero() {
		t.Fatal("publish date not set")
	}
}

func TestParseFieldsCategoryUnion(t *testing.T) {
	x := fieldsCardigann(t)
	// first category field: replaced
	rel := &models.ReleaseInfo{}
	v, err := x.parseFields("7", "category", rel, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	// mapping "7" → TV 5000 + desc-custom 100007
	if len(rel.Category) != 2 || rel.Category[0] != 5000 || rel.Category[1] != 100007 || v != "5000,100007" {
		t.Fatalf("category = %v value %q", rel.Category, v)
	}
	// second category field without noappend: unions
	_, err = x.parseFields("4", "category", rel, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	// "4" maps to Movies 2000; union appends 2000 after the existing pair
	if len(rel.Category) != 3 || rel.Category[2] != 2000 {
		t.Fatalf("union = %v", rel.Category)
	}
	// unknown category keeps going and warns (value untouched)
	rel2 := &models.ReleaseInfo{}
	v, err = x.parseFields("999", "category", rel2, nil, "")
	if err != nil || len(rel2.Category) != 0 {
		t.Fatalf("unknown category = %v %v", rel2.Category, err)
	}
	if v != "999" {
		t.Fatalf("unknown category value = %q", v)
	}
	// categorydesc maps via the desc mappings
	rel3 := &models.ReleaseInfo{}
	v, err = x.parseFields("tv shows", "categorydesc", rel3, nil, "")
	if err != nil || len(rel3.Category) != 2 || rel3.Category[1] != 100007 {
		t.Fatalf("categorydesc = %v %v value %q", rel3.Category, err, v)
	}
}

func TestUnionIntsAndStrings(t *testing.T) {
	// order-preserving union with dedup, nil-safe on both sides
	got := unionInts([]int{3, 1, 2}, []int{2, 5, 1})
	if len(got) != 4 || got[0] != 3 || got[1] != 1 || got[2] != 2 || got[3] != 5 {
		t.Fatalf("unionInts = %v", got)
	}
	if got := unionInts(nil, []int{7}); len(got) != 1 || got[0] != 7 {
		t.Fatalf("unionInts nil-a = %v", got)
	}
	if got := unionInts([]int{7}, nil); len(got) != 1 || got[0] != 7 {
		t.Fatalf("unionInts nil-b = %v", got)
	}
	gotS := unionStrings([]string{"a", "b"}, []string{"b", "c"})
	if len(gotS) != 3 || gotS[0] != "a" || gotS[1] != "b" || gotS[2] != "c" {
		t.Fatalf("unionStrings = %v", gotS)
	}
	if got := unionStrings(nil, nil); len(got) != 0 {
		t.Fatalf("unionStrings nils = %v", got)
	}
}

func TestParseFieldsGenre(t *testing.T) {
	x := fieldsCardigann(t)
	rel := &models.ReleaseInfo{}
	// delimiters split; underscores become spaces; duplicates collapse
	v, err := x.parseFields("Action_Adventure, Sci-Fi (Comedy). Drama", "genre", rel, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"Action Adventure", "Sci-Fi", "Comedy", "Drama"}
	if len(rel.Genres) != 4 {
		t.Fatalf("genres = %v", rel.Genres)
	}
	for i, w := range want {
		if rel.Genres[i] != w {
			t.Errorf("genre[%d] = %q, want %q", i, rel.Genres[i], w)
		}
	}
	if v != "Action Adventure,Sci-Fi,Comedy,Drama" {
		t.Errorf("value = %q", v)
	}
	// second genre field unions with the first
	if _, err := x.parseFields("Comedy / Thriller", "genre", rel, nil, ""); err != nil {
		t.Fatal(err)
	}
	if len(rel.Genres) != 5 || rel.Genres[4] != "Thriller" {
		t.Fatalf("unioned genres = %v", rel.Genres)
	}
}
