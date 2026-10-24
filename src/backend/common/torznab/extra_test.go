package torznab

import (
	"strings"
	"testing"

	"vestt/common/models"
)

func TestWriteCaps(t *testing.T) {
	caps := models.NewCapabilities()
	var out []byte
	WriteCaps(&byteWriter{b: &out}, caps)
	if string(out) != caps.XML() {
		t.Fatal("WriteCaps must equal Capsabilities.XML output")
	}
	if !strings.HasPrefix(string(out), `<?xml version="1.0" encoding="UTF-8"?>`) {
		t.Fatalf("caps must carry the XML declaration: %q", out)
	}
}

func TestCapsXMLNil(t *testing.T) {
	if CapsXML(nil) != models.NewCapabilities().XML() {
		t.Fatal("nil capabilities must render the default caps document")
	}
}

func TestWriteIndexers(t *testing.T) {
	caps := models.NewCapabilities()
	entries := []IndexerEntry{
		{
			ID:          "exampletracker",
			Configured:  true,
			Title:       "Example & <Tracker>",
			Description: "An example",
			Link:        "https://example.com/",
			Language:    "en-US",
			Type:        "private",
			Caps:        caps,
		},
		{
			ID:    "unconfigured",
			Title: "No Caps",
			// Caps nil: no <caps> node embedded
		},
	}
	var out []byte
	WriteIndexers(&byteWriter{b: &out}, entries)
	got := string(out)

	if !strings.HasPrefix(got, `<?xml version="1.0" encoding="UTF-8"?>`+"\n<indexers>") {
		t.Fatalf("indexers document header: %q", got)
	}
	if !strings.Contains(got, `<indexer id="exampletracker" configured="true">`) {
		t.Fatalf("indexer element: %q", got)
	}
	if !strings.Contains(got, `<indexer id="unconfigured" configured="false">`) {
		t.Fatalf("unconfigured indexer element: %q", got)
	}
	// title is element text: & and < escaped, no &quot;.
	if !strings.Contains(got, `<title>Example &amp; &lt;Tracker&gt;</title>`) {
		t.Fatalf("title must be text-escaped: %q", got)
	}
	// embedded caps: present for the first, absent for the second
	if !strings.Contains(got, `<search available="yes" supportedParams="q" />`) {
		t.Fatalf("embedded caps searching: %q", got)
	}
	if strings.Count(got, "<server title=") != 1 {
		t.Fatalf("exactly one embedded caps document: %q", got)
	}
	// the caps XML declaration must be stripped from the embedded copy
	rest := strings.SplitN(got, "<server", 2)[0]
	if strings.Count(rest, `<?xml`) != 1 {
		t.Fatalf("embedded caps must not repeat the XML declaration: %q", got)
	}
	if !strings.HasSuffix(got, "</indexers>") {
		t.Fatalf("document must end with </indexers>: %q", got)
	}
}

func TestWriteIndexersEmpty(t *testing.T) {
	var out []byte
	WriteIndexers(&byteWriter{b: &out}, nil)
	if string(out) != `<?xml version="1.0" encoding="UTF-8"?>`+"\n<indexers></indexers>" {
		t.Fatalf("empty indexers: %q", out)
	}
}

func TestRSSStripsInvalidXMLChars(t *testing.T) {
	r := &models.ReleaseInfo{
		// NUL, vertical tab, 0x1F, DEL, BOM all stripped from the title.
		Title:       "Bad\x00Title\x0B\x1F\x7F\uFEFFEnd",
		Guid:        mustURL(t, "https://example.com/t/bad"),
		PublishDate: fixedDate(t),
		Origin:      models.ReleaseOrigin{ID: "example", Name: "Example Tracker", Type: "public"},
	}
	var out []byte
	WriteRSS(&byteWriter{b: &out}, testChannel(), []*models.ReleaseInfo{r}, testSelfAtom())
	got := string(out)
	if !strings.Contains(got, "<title>BadTitleEnd</title>") {
		t.Fatalf("invalid XML chars must be stripped: %q", got)
	}
	for _, bad := range []string{"\x00", "\x0B", "\x1F", "\x7F", "\uFEFF"} {
		if strings.Contains(got, bad) {
			t.Fatalf("output contains invalid char %q: %q", bad, got)
		}
	}
}
