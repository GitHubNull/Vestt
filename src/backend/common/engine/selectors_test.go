package engine

import (
	"strings"
	"testing"

	"github.com/PuerkitoBio/goquery"
)

// TestQuerySelectorAllNotContains: the :contains() emulation must invert
// the filter inside :not() — a dangling empty :not() matched nothing and
// dropped every row for the 47 definitions using :not(:contains(...)).
func TestQuerySelectorAllNotContains(t *testing.T) {
	html := `<html><body>
<table><tbody>
<tr><td><a href="/t/1">fake 2026 release 720p</a></td><td class="result-meta"><div>500 MB</div></td></tr>
<tr><td><a href="/t/2">We couldn't find anything for it</a></td><td class="result-meta"><div>0 MB</div></td></tr>
</tbody></table>
</body></html>`

	cases := []struct {
		name    string
		sel     string
		wantLen int
		wantTxt string
	}{
		{
			name:    "0magnet row selector",
			sel:     `table > tbody > tr:has(td):not(:contains("We couldn't find anything for"))`,
			wantLen: 1,
			wantTxt: "fake 2026 release 720p500 MB",
		},
		{
			name:    "plain :not(:contains()) compound",
			sel:     `tr:not(:contains("couldn't find"))`,
			wantLen: 1,
			wantTxt: "fake 2026 release 720p500 MB",
		},
		{
			name:    "plain :contains() still filters",
			sel:     `tr:contains("fake")`,
			wantLen: 1,
			wantTxt: "fake 2026 release 720p500 MB",
		},
		{
			name:    ":not(:contains()) excluding everything",
			sel:     `tr:not(:contains("a"))`,
			wantLen: 0,
		},
		{
			name:    "mixed :not() with :not(:contains())",
			sel:     `tr:not(.hidden):not(:contains("couldn't find"))`,
			wantLen: 1,
			wantTxt: "fake 2026 release 720p500 MB",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			doc, err := goquery.NewDocumentFromReader(strings.NewReader(html))
			if err != nil {
				t.Fatal(err)
			}
			got := querySelectorAll(doc.Selection, tc.sel)
			if got.Length() != tc.wantLen {
				t.Fatalf("selector %q matched %d rows, want %d", tc.sel, got.Length(), tc.wantLen)
			}
			if tc.wantTxt != "" && strings.TrimSpace(got.First().Text()) != tc.wantTxt {
				t.Fatalf("first row text = %q, want %q", strings.TrimSpace(got.First().Text()), tc.wantTxt)
			}
		})
	}
}
