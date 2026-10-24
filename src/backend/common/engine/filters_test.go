// Filter coverage tests (SPEC-06 acceptance #1).
//
// Every upstream 0.24.2900 applyFilters name (CardigannIndexer.cs 1114-1275)
// is exercised with normal/boundary/error inputs; time-dependent filters are
// asserted as windows to stay deterministic.
package engine

import (
	"os"
	"regexp"
	"strconv"
	"testing"
	"time"
)

func filterTestIndexer(t *testing.T, encoding string) *Cardigann {
	t.Helper()
	def := &IndexerDefinition{
		ID:       "filtertest",
		Name:     "Filter Test",
		Type:     "public",
		Links:    []string{"https://example.test/"},
		Encoding: encoding,
		Caps: &CapsBlock{
			Modes: StringListMap{{Key: "search", Values: []string{"q"}}},
		},
		Search: &SearchBlock{},
	}
	x, err := NewCardigann(def, discardLogger())
	if err != nil {
		t.Fatalf("NewCardigann: %v", err)
	}
	return x
}

type filterCase struct {
	name    string
	data    string
	filters []FilterBlock
	// exactly one of want / wantRFC / wantNear is used
	want     string
	wantRFC  [6]int // year month day hour min sec
	wantNear time.Duration
}

func F(name string, args any) FilterBlock { return FilterBlock{Name: name, Args: args} }
func FA(args ...any) []any                { return args }

func runFilterCases(t *testing.T, x *Cardigann, vars TemplateVars, cases []filterCase) {
	t.Helper()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := x.applyFilters(tc.data, tc.filters, vars)
			switch {
			case tc.wantNear != 0:
				parsed, err := time.Parse("Mon, 02 Jan 2006 15:04:05 -0700", got)
				if err != nil {
					t.Fatalf("result %q is not RFC1123Z: %v", got, err)
				}
				want := time.Now().Add(-tc.wantNear)
				if parsed.Before(want.Add(-2*time.Minute)) || parsed.After(want.Add(2*time.Minute)) {
					t.Fatalf("got %s, want near %s (-%s)", parsed, want, tc.wantNear)
				}
			case tc.wantRFC != [6]int{}:
				parsed, err := time.Parse("Mon, 02 Jan 2006 15:04:05 -0700", got)
				if err != nil {
					t.Fatalf("result %q is not RFC1123Z: %v", got, err)
				}
				w := tc.wantRFC
				if parsed.Year() != w[0] || int(parsed.Month()) != w[1] || parsed.Day() != w[2] ||
					parsed.Hour() != w[3] || parsed.Minute() != w[4] || parsed.Second() != w[5] {
					t.Fatalf("got %s, want %d-%02d-%02d %02d:%02d:%02d", parsed, w[0], w[1], w[2], w[3], w[4], w[5])
				}
			default:
				if got != tc.want {
					t.Fatalf("applyFilters(%q) = %q, want %q", tc.data, got, tc.want)
				}
			}
		})
	}
}

func TestApplyFiltersQuerystring(t *testing.T) {
	x := filterTestIndexer(t, "utf-8")
	runFilterCases(t, x, nil, []filterCase{
		{"last param", "https://x.test/d?a=1&b=two", []FilterBlock{F("querystring", "b")}, "two", [6]int{}, 0},
		{"first param", "https://x.test/d?a=1&b=two", []FilterBlock{F("querystring", "a")}, "1", [6]int{}, 0},
		{"missing param", "https://x.test/d?a=1", []FilterBlock{F("querystring", "zz")}, "", [6]int{}, 0},
		{"plus becomes space", "https://x.test/d?a=hello+world", []FilterBlock{F("querystring", "a")}, "hello world", [6]int{}, 0},
		{"encoded value", "https://x.test/d?a=x%2Fy%3D1", []FilterBlock{F("querystring", "a")}, "x/y=1", [6]int{}, 0},
		{"no query string", "https://x.test/d", []FilterBlock{F("querystring", "a")}, "", [6]int{}, 0},
	})
}

func TestApplyFiltersTimeparse(t *testing.T) {
	x := filterTestIndexer(t, "utf-8")
	runFilterCases(t, x, nil, []filterCase{
		{"date only", "24.05.2023", []FilterBlock{F("timeparse", "02.01.2006")}, "", [6]int{2023, 5, 24, 0, 0, 0}, 0},
		{"date and time", "24.05.2023 13:37", []FilterBlock{F("timeparse", "02.01.2006 15:04")}, "", [6]int{2023, 5, 24, 13, 37, 0}, 0},
		{"month name layout", "May 24, 2023", []FilterBlock{F("timeparse", "January 2, 2006")}, "", [6]int{2023, 5, 24, 0, 0, 0}, 0},
		{"multi spaces normalized", "24.05.2023   13:37", []FilterBlock{F("timeparse", "02.01.2006 15:04")}, "", [6]int{2023, 5, 24, 13, 37, 0}, 0},
		{"bad data unchanged", "not a date", []FilterBlock{F("timeparse", "02.01.2006")}, "not a date", [6]int{}, 0},
		{"bad layout unchanged", "24.05.2023", []FilterBlock{F("timeparse", "15:04")}, "24.05.2023", [6]int{}, 0},
	})
}

func TestApplyFiltersDateparse(t *testing.T) {
	x := filterTestIndexer(t, "utf-8")
	runFilterCases(t, x, nil, []filterCase{
		{"iso date", "2023-05-24", []FilterBlock{F("dateparse", "2006-01-02")}, "", [6]int{2023, 5, 24, 0, 0, 0}, 0},
		{"iso datetime", "2023-05-24 13:37:55", []FilterBlock{F("dateparse", "2006-01-02 15:04:05")}, "", [6]int{2023, 5, 24, 13, 37, 55}, 0},
		{"bad data unchanged", "xx", []FilterBlock{F("dateparse", "2006-01-02")}, "xx", [6]int{}, 0},
	})
}

func TestApplyFiltersRegexp(t *testing.T) {
	x := filterTestIndexer(t, "utf-8")
	runFilterCases(t, x, nil, []filterCase{
		{"group extract", "Size 15 MB", []FilterBlock{F("regexp", `Size (\d+) MB`)}, "15", [6]int{}, 0},
		{"first of two groups", "a1b2", []FilterBlock{F("regexp", `a(\d)b(\d)`)}, "1", [6]int{}, 0},
		{"no groups yields empty", "abc", []FilterBlock{F("regexp", "abc")}, "", [6]int{}, 0},
		{"no match yields empty", "abc", []FilterBlock{F("regexp", "zzz")}, "", [6]int{}, 0},
		{"invalid pattern unchanged", "abc", []FilterBlock{F("regexp", "(")}, "abc", [6]int{}, 0},
	})
}

func TestApplyFiltersReReplace(t *testing.T) {
	x := filterTestIndexer(t, "utf-8")
	vars := x.GetBaseTemplateVariables()
	runFilterCases(t, x, vars, []filterCase{
		{"literal replace", "hello world", []FilterBlock{F("re_replace", FA("world", "there"))}, "hello there", [6]int{}, 0},
		{"group reference", "a1b", []FilterBlock{F("re_replace", FA(`a(\d)`, "x$1x"))}, "x1xb", [6]int{}, 0},
		{"template replacement", "data", []FilterBlock{F("re_replace", FA("data", "year-{{ .Today.Year }}"))}, "year-" + strconv.Itoa(time.Now().Year()), [6]int{}, 0},
		{"empty replacement deletes", "a-b-c", []FilterBlock{F("re_replace", FA("-", ""))}, "abc", [6]int{}, 0},
		{"invalid pattern unchanged", "abc", []FilterBlock{F("re_replace", FA("(", "x"))}, "abc", [6]int{}, 0},
	})
}

func TestApplyFiltersSplit(t *testing.T) {
	x := filterTestIndexer(t, "utf-8")
	runFilterCases(t, x, nil, []filterCase{
		{"basic", "a b c", []FilterBlock{F("split", FA(" ", "1"))}, "b", [6]int{}, 0},
		{"first index", "a b c", []FilterBlock{F("split", FA(" ", "0"))}, "a", [6]int{}, 0},
		{"negative index is from end", "a b c", []FilterBlock{F("split", FA(" ", "-1"))}, "c", [6]int{}, 0},
		{"multi char sep uses first char", "aXXb", []FilterBlock{F("split", FA("XY", "1"))}, "", [6]int{}, 0},
		{"out of range unchanged", "a b", []FilterBlock{F("split", FA(" ", "9"))}, "a b", [6]int{}, 0},
		{"pipe separator", "a|b|c", []FilterBlock{F("split", FA("|", "2"))}, "c", [6]int{}, 0},
		{"non-numeric pos unchanged", "a b", []FilterBlock{F("split", FA(" ", "x"))}, "a b", [6]int{}, 0},
	})
}

func TestApplyFiltersReplace(t *testing.T) {
	x := filterTestIndexer(t, "utf-8")
	vars := x.GetBaseTemplateVariables()
	runFilterCases(t, x, vars, []filterCase{
		{"literal", "a-b", []FilterBlock{F("replace", FA("-", "+"))}, "a+b", [6]int{}, 0},
		{"template to", "x", []FilterBlock{F("replace", FA("x", "{{ .Today.Year }}"))}, strconv.Itoa(time.Now().Year()), [6]int{}, 0},
		{"empty to deletes", "a-b", []FilterBlock{F("replace", FA("-", ""))}, "ab", [6]int{}, 0},
		{"from absent unchanged", "abc", []FilterBlock{F("replace", FA("zz", "q"))}, "abc", [6]int{}, 0},
	})
}

func TestApplyFiltersTrim(t *testing.T) {
	x := filterTestIndexer(t, "utf-8")
	runFilterCases(t, x, nil, []filterCase{
		{"cutset char both ends", "...val...", []FilterBlock{F("trim", ".")}, "val", [6]int{}, 0},
		{"dash cutset", "-val-", []FilterBlock{F("trim", "-")}, "val", [6]int{}, 0},
		{"cutset only first char", "..val..", []FilterBlock{F("trim", "._")}, "val", [6]int{}, 0},
		{"middle occurrences kept", "a.b.c", []FilterBlock{F("trim", ".")}, "a.b.c", [6]int{}, 0},
		{"empty args trims space", "  val  ", []FilterBlock{F("trim", "")}, "val", [6]int{}, 0},
	})
}

func TestApplyFiltersPrependAppend(t *testing.T) {
	x := filterTestIndexer(t, "utf-8")
	vars := x.GetBaseTemplateVariables()
	runFilterCases(t, x, vars, []filterCase{
		{"prepend literal", "bar", []FilterBlock{F("prepend", "foo")}, "foobar", [6]int{}, 0},
		{"prepend template", "bar", []FilterBlock{F("prepend", "{{ .Today.Year }}-")}, strconv.Itoa(time.Now().Year()) + "-bar", [6]int{}, 0},
		{"append literal", "foo", []FilterBlock{F("append", "bar")}, "foobar", [6]int{}, 0},
		{"append template", "x", []FilterBlock{F("append", ".{{ .Today.Year }}")}, "x." + strconv.Itoa(time.Now().Year()), [6]int{}, 0},
	})
}

func TestApplyFiltersCase(t *testing.T) {
	x := filterTestIndexer(t, "utf-8")
	runFilterCases(t, x, nil, []filterCase{
		{"tolower", "HeLLo", []FilterBlock{F("tolower", nil)}, "hello", [6]int{}, 0},
		{"tolower unicode", "ÄÖÜ", []FilterBlock{F("tolower", nil)}, "äöü", [6]int{}, 0},
		{"tolower already lower", "abc", []FilterBlock{F("tolower", nil)}, "abc", [6]int{}, 0},
		{"toupper", "hello", []FilterBlock{F("toupper", nil)}, "HELLO", [6]int{}, 0},
		{"toupper unicode", "äöü", []FilterBlock{F("toupper", nil)}, "ÄÖÜ", [6]int{}, 0},
		{"toupper mixed digits", "a1b2", []FilterBlock{F("toupper", nil)}, "A1B2", [6]int{}, 0},
	})
}

func TestApplyFiltersBase64(t *testing.T) {
	x := filterTestIndexer(t, "utf-8")
	runFilterCases(t, x, nil, []filterCase{
		{"decode unpadded url", "dGVzdA", []FilterBlock{F("base64decode", nil)}, "test", [6]int{}, 0},
		{"decode padded url", "dGVzdA==", []FilterBlock{F("base64decode", nil)}, "test", [6]int{}, 0},
		{"decode url alphabet", "-_8=", []FilterBlock{F("base64decode", nil)}, string([]byte{0xfb, 0xff}), [6]int{}, 0},
		{"decode std fallback", "dGVzdA==", []FilterBlock{F("base64decode", nil)}, "test", [6]int{}, 0},
		{"decode empty", "", []FilterBlock{F("base64decode", nil)}, "", [6]int{}, 0},
		{"decode invalid unchanged", "!!!", []FilterBlock{F("base64decode", nil)}, "!!!", [6]int{}, 0},
		{"encode no padding", "test", []FilterBlock{F("base64encode", nil)}, "dGVzdA", [6]int{}, 0},
		{"encode escapes url-unsafe", "a/b+c", []FilterBlock{F("base64encode", nil)}, "YS9iK2M", [6]int{}, 0},
		{"encode unicode utf8", "héllo", []FilterBlock{F("base64encode", nil)}, "aMOpbGxv", [6]int{}, 0},
		{"encode empty", "", []FilterBlock{F("base64encode", nil)}, "", [6]int{}, 0},
	})
}

func TestApplyFiltersUrl(t *testing.T) {
	x := filterTestIndexer(t, "utf-8")
	enc := filterTestIndexer(t, "windows-1251")
	runFilterCases(t, x, nil, []filterCase{
		{"decode plus", "a+b", []FilterBlock{F("urldecode", nil)}, "a b", [6]int{}, 0},
		{"decode hex", "%3Fx%3D1", []FilterBlock{F("urldecode", nil)}, "?x=1", [6]int{}, 0},
		{"decode mixed", "a%20b+c", []FilterBlock{F("urldecode", nil)}, "a b c", [6]int{}, 0},
		{"decode invalid percent literal", "a%ZZ", []FilterBlock{F("urldecode", nil)}, "a%ZZ", [6]int{}, 0},
		{"encode space", "a b", []FilterBlock{F("urlencode", nil)}, "a+b", [6]int{}, 0},
		{"encode safe chars kept", "a*b!c'd(e)f-g_h.i", []FilterBlock{F("urlencode", nil)}, "a*b!c'd(e)f-g_h.i", [6]int{}, 0},
		{"encode tilde escaped", "a~b", []FilterBlock{F("urlencode", nil)}, "a%7Eb", [6]int{}, 0},
		{"encode empty", "", []FilterBlock{F("urlencode", nil)}, "", [6]int{}, 0},
	})
	runFilterCases(t, enc, nil, []filterCase{
		{"encode definition encoding", "с", []FilterBlock{F("urlencode", nil)}, "%F1", [6]int{}, 0},
		{"decode definition encoding", "%F1%E5%E7%EE%ED", []FilterBlock{F("urldecode", nil)}, "сезон", [6]int{}, 0},
	})
}

func TestApplyFiltersHtml(t *testing.T) {
	x := filterTestIndexer(t, "utf-8")
	runFilterCases(t, x, nil, []filterCase{
		{"decode entities", "&amp;&lt;&gt;&quot;", []FilterBlock{F("htmldecode", nil)}, `&<>"`, [6]int{}, 0},
		{"decode numeric", "&#65;&#x42;", []FilterBlock{F("htmldecode", nil)}, "AB", [6]int{}, 0},
		{"decode plain unchanged", "plain", []FilterBlock{F("htmldecode", nil)}, "plain", [6]int{}, 0},
		{"encode", `<a href="x">&`, []FilterBlock{F("htmlencode", nil)}, `&lt;a href=&#34;x&#34;&gt;&amp;`, [6]int{}, 0},
		{"encode quote", `"quoted"`, []FilterBlock{F("htmlencode", nil)}, `&#34;quoted&#34;`, [6]int{}, 0},
		{"encode plain unchanged", "plain", []FilterBlock{F("htmlencode", nil)}, "plain", [6]int{}, 0},
	})
}

func TestApplyFiltersRelativeTime(t *testing.T) {
	x := filterTestIndexer(t, "utf-8")
	runFilterCases(t, x, nil, []filterCase{
		{"timeago now", "just now", []FilterBlock{F("timeago", nil)}, "", [6]int{}, time.Second},
		{"timeago hours", "2 hours ago", []FilterBlock{F("timeago", nil)}, "", [6]int{}, 2 * time.Hour},
		{"timeago day", "1 day ago", []FilterBlock{F("timeago", nil)}, "", [6]int{}, 24 * time.Hour},
		{"timeago weeks", "5 weeks ago", []FilterBlock{F("timeago", nil)}, "", [6]int{}, 5 * 7 * 24 * time.Hour},
		// upstream: no digit run matches → zero timespan → now
		{"timeago no digits becomes now", "blah", []FilterBlock{F("timeago", nil)}, "", [6]int{}, time.Second},
		// upstream throws on the unknown unit; the port keeps the data
		{"timeago unknown unit unchanged", "5 blarghs ago", []FilterBlock{F("timeago", nil)}, "5 blarghs ago", [6]int{}, 0},
		{"reltime days", "3 days ago", []FilterBlock{F("reltime", nil)}, "", [6]int{}, 3 * 24 * time.Hour},
		{"reltime minutes", "30 minutes ago", []FilterBlock{F("reltime", nil)}, "", [6]int{}, 30 * time.Minute},
		{"reltime year", "1 year ago", []FilterBlock{F("reltime", nil)}, "", [6]int{}, 365 * 24 * time.Hour},
		{"reltime unknown unit unchanged", "5 blarghs ago", []FilterBlock{F("reltime", nil)}, "5 blarghs ago", [6]int{}, 0},
	})
}

func TestApplyFiltersFuzzytime(t *testing.T) {
	x := filterTestIndexer(t, "utf-8")
	runFilterCases(t, x, nil, []filterCase{
		{"iso date", "2023-05-24", []FilterBlock{F("fuzzytime", nil)}, "", [6]int{2023, 5, 24, 0, 0, 0}, 0},
		{"dotted datetime", "24.05.2023 13:37", []FilterBlock{F("fuzzytime", nil)}, "", [6]int{2023, 5, 24, 13, 37, 0}, 0},
		{"month name", "May 24, 2023", []FilterBlock{F("fuzzytime", nil)}, "", [6]int{2023, 5, 24, 0, 0, 0}, 0},
		{"rfc1123z passthrough", "Wed, 24 May 2023 13:37:00 +0000", []FilterBlock{F("fuzzytime", nil)}, "", [6]int{2023, 5, 24, 13, 37, 0}, 0},
		{"invalid unchanged", "not a date at all", []FilterBlock{F("fuzzytime", nil)}, "not a date at all", [6]int{}, 0},
	})
}

func TestApplyFiltersValidfilename(t *testing.T) {
	x := filterTestIndexer(t, "utf-8")
	runFilterCases(t, x, nil, []filterCase{
		{"slash replaced", "a/b", []FilterBlock{F("validfilename", nil)}, "a_b", [6]int{}, 0},
		{"nul replaced", "a" + string(rune(0)) + "b", []FilterBlock{F("validfilename", nil)}, "a_b", [6]int{}, 0},
		{"empty becomes underscore", "", []FilterBlock{F("validfilename", nil)}, "_", [6]int{}, 0},
		{"backslash kept on linux", `a\b`, []FilterBlock{F("validfilename", nil)}, `a\b`, [6]int{}, 0},
		{"normal unchanged", "normal.name-ok", []FilterBlock{F("validfilename", nil)}, "normal.name-ok", [6]int{}, 0},
	})
}

func TestApplyFiltersDiacritics(t *testing.T) {
	x := filterTestIndexer(t, "utf-8")
	runFilterCases(t, x, nil, []filterCase{
		{"combining accent stripped", "café", []FilterBlock{F("diacritics", "replace")}, "cafe", [6]int{}, 0},
		// upstream quirk: Đ/đ have no decomposition and survive
		{"upstream serbian quirk", "ŠĐĆŽ - šđčćž", []FilterBlock{F("diacritics", "replace")}, "SĐCZ - sđccz", [6]int{}, 0},
		{"multiple words", "naïve résumé", []FilterBlock{F("diacritics", "replace")}, "naive resume", [6]int{}, 0},
		{"non decomposable unchanged", "æther", []FilterBlock{F("diacritics", "replace")}, "æther", [6]int{}, 0},
		{"non replace op ignored", "café", []FilterBlock{F("diacritics", "strip")}, "café", [6]int{}, 0},
	})
}

func TestApplyFiltersJsonjoinarray(t *testing.T) {
	x := filterTestIndexer(t, "utf-8")
	runFilterCases(t, x, nil, []filterCase{
		{"basic", `{"tags":["a","b"]}`, []FilterBlock{F("jsonjoinarray", FA("tags", ", "))}, "a, b", [6]int{}, 0},
		{"nested path", `{"x":{"y":[1,2]}}`, []FilterBlock{F("jsonjoinarray", FA("x.y", "-"))}, "1-2", [6]int{}, 0},
		{"array index path", `{"rows":[["p","q"]]}`, []FilterBlock{F("jsonjoinarray", FA("rows.0", "|"))}, "p|q", [6]int{}, 0},
		{"missing path unchanged", `{"tags":[]}`, []FilterBlock{F("jsonjoinarray", FA("zz", ", "))}, `{"tags":[]}`, [6]int{}, 0},
		{"non array unchanged", `{"tags":"x"}`, []FilterBlock{F("jsonjoinarray", FA("tags", ", "))}, `{"tags":"x"}`, [6]int{}, 0},
		{"invalid json unchanged", `not json`, []FilterBlock{F("jsonjoinarray", FA("tags", ", "))}, "not json", [6]int{}, 0},
	})
}

func TestApplyFiltersDebugDump(t *testing.T) {
	x := filterTestIndexer(t, "utf-8")
	runFilterCases(t, x, nil, []filterCase{
		{"hexdump unchanged", "ab", []FilterBlock{F("hexdump", nil)}, "ab", [6]int{}, 0},
		{"hexdump non-ascii unchanged", "é", []FilterBlock{F("hexdump", nil)}, "é", [6]int{}, 0},
		{"hexdump empty unchanged", "", []FilterBlock{F("hexdump", nil)}, "", [6]int{}, 0},
		{"strdump unchanged with tag", "ab", []FilterBlock{F("strdump", "title")}, "ab", [6]int{}, 0},
		{"strdump unchanged no args", "ab", []FilterBlock{F("strdump", nil)}, "ab", [6]int{}, 0},
		{"strdump newline unchanged", "a\nb", []FilterBlock{F("strdump", nil)}, "a\nb", [6]int{}, 0},
	})
}

func TestApplyFiltersValidate(t *testing.T) {
	x := filterTestIndexer(t, "utf-8")
	runFilterCases(t, x, nil, []filterCase{
		{"basic intersect", "Movies x264 1080p", []FilterBlock{F("validate", "movies,x264")}, "movies,x264", [6]int{}, 0},
		{"validlist order wins", "a b", []FilterBlock{F("validate", "b,a")}, "b,a", [6]int{}, 0},
		{"distinct on data side", "a a a", []FilterBlock{F("validate", "a")}, "a", [6]int{}, 0},
		{"case insensitive", "MOVIES", []FilterBlock{F("validate", "movies")}, "movies", [6]int{}, 0},
		{"no match empty", "abc", []FilterBlock{F("validate", "zzz")}, "", [6]int{}, 0},
		{"delimiters split", "a,b(c)", []FilterBlock{F("validate", "a b")}, "a,b", [6]int{}, 0},
	})
}

func TestApplyFiltersMisc(t *testing.T) {
	x := filterTestIndexer(t, "utf-8")
	runFilterCases(t, x, nil, []filterCase{
		{"unknown filter ignored", "abc", []FilterBlock{F("bogus", "x")}, "abc", [6]int{}, 0},
		{"nil filters passthrough", "abc", nil, "abc", [6]int{}, 0},
		{"empty filter list", "abc", []FilterBlock{}, "abc", [6]int{}, 0},
		{"chain regexp then prepend", "Size 15 MB", []FilterBlock{
			F("regexp", `Size (\d+)`), F("prepend", "S"),
		}, "S15", [6]int{}, 0},
		{"chain trim then toupper", "  ab  ", []FilterBlock{F("trim", ""), F("toupper", nil)}, "AB", [6]int{}, 0},
		{"chain replace then append", "a-b", []FilterBlock{F("replace", FA("-", "+")), F("append", "!")}, "a+b!", [6]int{}, 0},
	})
}

// TestApplyFiltersCoverageCount pins the acceptance threshold: every upstream
// filter name has ≥3 cases and the suite stays above 120 total.
func TestApplyFiltersCoverageCount(t *testing.T) {
	counts := map[string]int{}
	src, err := os.ReadFile("filters_test.go")
	if err != nil {
		t.Fatal(err)
	}
	re := regexp.MustCompile(`F\("([a-z0-9_]+)"`)
	for _, m := range re.FindAllStringSubmatch(string(src), -1) {
		counts[m[1]]++
	}
	unique := []string{"querystring", "timeparse", "dateparse", "regexp", "re_replace", "split", "replace",
		"trim", "prepend", "append", "tolower", "toupper", "base64decode", "base64encode",
		"urldecode", "urlencode", "htmldecode", "htmlencode", "timeago", "reltime", "fuzzytime",
		"validfilename", "diacritics", "jsonjoinarray", "hexdump", "strdump", "validate"}
	total := 0
	for _, name := range unique {
		n := counts[name]
		total += n
		if n < 3 {
			t.Errorf("filter %q has only %d cases (need ≥3)", name, n)
		}
	}
	if total < 120 {
		t.Errorf("total filter cases = %d (need ≥120)", total)
	}
	t.Logf("total filter cases: %d", total)
}
