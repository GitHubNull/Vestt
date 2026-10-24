package torznab

import (
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"vestt/common/models"
)

func mustURL(t *testing.T, s string) *url.URL {
	t.Helper()
	u, err := url.Parse(s)
	if err != nil {
		t.Fatalf("parse url %q: %v", s, err)
	}
	return u
}

func golden(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read golden %s: %v", name, err)
	}
	// golden files carry a trailing newline for editor convenience; the
	// serialized documents (like upstream XDocument.ToString) end without one.
	return strings.TrimRight(string(data), "\n")
}

func testChannel() ChannelInfo {
	ch := NewChannelInfo()
	ch.Title = "Example Tracker"
	ch.Description = "This is an example tracker"
	link, _ := url.Parse("https://example.com/")
	ch.Link = link
	return ch
}

func testSelfAtom() *url.URL {
	u, _ := url.Parse("http://127.0.0.1:9117/api/v2.0/indexers/example/results/torznab/")
	return u
}

func fixedDate(t *testing.T) time.Time {
	t.Helper()
	dt, err := time.Parse(time.RFC3339, "2026-10-20T18:30:00Z")
	if err != nil {
		t.Fatal(err)
	}
	return dt
}

func TestGoldenCapsFull(t *testing.T) {
	caps := models.NewCapabilities()
	caps.TVSearchParams = []string{"q", "season", "ep", "imdbid", "tvdbid", "tmdbid"}
	caps.TVSearchImdbAvailable = true
	caps.MovieSearchParams = []string{"q", "imdbid", "tmdbid"}
	for _, c := range models.CatMovies.SubCategories {
		caps.Categories.AddCategoryTree(c)
	}
	got := CapsXML(caps)
	want := golden(t, "caps_full.xml")
	if got != want {
		t.Fatalf("caps mismatch:\ngot:  %s\nwant: %s", got, want)
	}
}

func TestGoldenCapsMinimal(t *testing.T) {
	got := CapsXML(models.NewCapabilities())
	want := golden(t, "caps_minimal.xml")
	if got != want {
		t.Fatalf("caps mismatch:\ngot:  %s\nwant: %s", got, want)
	}
}

func TestGoldenRSSStandard(t *testing.T) {
	size := int64(14680064000)
	grabs := int64(42)
	seeders := int64(88)
	peers := int64(95)
	imdb := int64(123456)
	dvf := 0.5
	uvf := 1.0
	r := &models.ReleaseInfo{
		Title:                "Example Movie 2020 1080p BluRay",
		Guid:                 mustURL(t, "https://example.com/t/123"),
		Link:                 mustURL(t, "http://127.0.0.1:9117/dl/example?path=t%2F123&file=Example.Movie.2020&apikey=secret"),
		Details:              mustURL(t, "https://example.com/t/123"),
		PublishDate:          fixedDate(t),
		Category:             []int{2000, 2040},
		Size:                 &size,
		Grabs:                &grabs,
		Description:          "A movie",
		Imdb:                 &imdb,
		Seeders:              &seeders,
		Peers:                &peers,
		InfoHash:             "ABC123",
		DownloadVolumeFactor: &dvf,
		UploadVolumeFactor:   &uvf,
		Origin:               models.ReleaseOrigin{ID: "example", Name: "Example Tracker", Type: "private"},
	}
	ch := testChannel()
	var out []byte
	WriteRSS(&byteWriter{b: &out}, ch, []*models.ReleaseInfo{r}, testSelfAtom())
	got := string(out)
	want := golden(t, "rss_standard.xml")
	if got != want {
		t.Fatalf("rss mismatch:\ngot:  %s\nwant: %s", got, want)
	}
}

type byteWriter struct{ b *[]byte }

func (w *byteWriter) Write(p []byte) (int, error) {
	*w.b = append(*w.b, p...)
	return len(p), nil
}

func TestGoldenRSSMagnet(t *testing.T) {
	r := &models.ReleaseInfo{
		Title:       "Magnet Only Release",
		Guid:        mustURL(t, "https://example.com/t/456"),
		MagnetUri:   mustURL(t, "magnet:?xt=urn:btih:def456&dn=Magnet+Only+Release"),
		PublishDate: fixedDate(t),
		Origin:      models.ReleaseOrigin{ID: "example", Name: "Example Tracker", Type: "public"},
	}
	var out []byte
	WriteRSS(&byteWriter{b: &out}, testChannel(), []*models.ReleaseInfo{r}, testSelfAtom())
	got := string(out)
	want := golden(t, "rss_magnet.xml")
	if got != want {
		t.Fatalf("rss mismatch:\ngot:  %s\nwant: %s", got, want)
	}
}

func TestGoldenRSSFreeleech(t *testing.T) {
	seeders := int64(0)
	dvf := 0.0
	uvf := 1.0
	r := &models.ReleaseInfo{
		Title:                "Freeleech Release",
		Guid:                 mustURL(t, "https://example.com/t/789"),
		Link:                 mustURL(t, "https://example.com/download/789"),
		PublishDate:          fixedDate(t),
		Category:             []int{2000},
		Seeders:              &seeders,
		DownloadVolumeFactor: &dvf,
		UploadVolumeFactor:   &uvf,
		Origin:               models.ReleaseOrigin{ID: "example", Name: "Example Tracker", Type: "private"},
	}
	var out []byte
	WriteRSS(&byteWriter{b: &out}, testChannel(), []*models.ReleaseInfo{r}, testSelfAtom())
	got := string(out)
	want := golden(t, "rss_freeleech.xml")
	if got != want {
		t.Fatalf("rss mismatch:\ngot:  %s\nwant: %s", got, want)
	}
}

func TestGoldenRSSEmpty(t *testing.T) {
	var out []byte
	WriteRSS(&byteWriter{b: &out}, testChannel(), nil, testSelfAtom())
	got := string(out)
	want := golden(t, "rss_empty.xml")
	if got != want {
		t.Fatalf("rss mismatch:\ngot:  %s\nwant: %s", got, want)
	}
}

func TestGoldenErrors(t *testing.T) {
	cases := []struct {
		file string
		code int
		desc string
	}{
		{"error_100.xml", 100, "Invalid API Key"},
		{"error_201.xml", 201, "Indexer is not specified"},
		{"error_203.xml", 203, "Function Not Available"},
		{"error_900.xml", 900, "Exception: login failed"},
	}
	for _, tc := range cases {
		t.Run(tc.file, func(t *testing.T) {
			got := ErrorXMLString(tc.code, tc.desc)
			want := golden(t, tc.file)
			if got != want {
				t.Fatalf("error xml mismatch:\ngot:  %s\nwant: %s", got, want)
			}
		})
	}
}
