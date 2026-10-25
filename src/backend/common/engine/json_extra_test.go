// JSON selector engine unit tests (SPEC-06): Newtonsoft-style token
// selection, scalar rendering (invariant dates, True/False, array join) and
// the rows ":has/:not/:contains" filter chain.
package engine

import (
	"encoding/json"
	"strings"
	"testing"
)

func parseJSONMap(t *testing.T, s string) map[string]any {
	t.Helper()
	v, err := parseJSON([]byte(s))
	if err != nil {
		t.Fatalf("parseJSON: %v", err)
	}
	m, ok := v.(map[string]any)
	if !ok {
		t.Fatalf("expected object, got %T", v)
	}
	return m
}

func TestJSONTokenStringScalars(t *testing.T) {
	cases := []struct {
		name string
		in   any
		want string
	}{
		{"nil", nil, ""},
		{"string", "hello", "hello"},
		{"number", json.Number("42"), "42"},
		{"true", true, "True"},
		{"false", false, "False"},
		{"float", 2.5, "2.5"},
		// ISO-8601-shaped strings render invariant-culture (Newtonsoft
		// DateParseHandling.DateTime)
		{"iso date", "2024-01-15T10:30:00Z", "01/15/2024 10:30:00"},
		{"iso date space", "2024-01-15 10:30:00", "01/15/2024 10:30:00"},
		{"date only", "2024-01-15", "01/15/2024 00:00:00"},
		{"not a date", "not-a-date", "not-a-date"},
		{"almost a date", "2024-13-45", "2024-13-45"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := jsonTokenString(tc.in); got != tc.want {
				t.Errorf("jsonTokenString(%v) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestJSONTokenStringComposites(t *testing.T) {
	// arrays are comma-joined recursively (upstream JArray join)
	arr := []any{json.Number("1"), "a", true, []any{false, "b"}}
	if got := jsonTokenString(arr); got != "1,a,True,False,b" {
		t.Errorf("array join = %q", got)
	}
	// objects render compact JSON
	obj := map[string]any{"a": json.Number("1")}
	got := jsonTokenString(obj)
	if got != `{"a":1}` {
		t.Errorf("object = %q", got)
	}
	// empty array → empty join
	if got := jsonTokenString([]any{}); got != "" {
		t.Errorf("empty array = %q", got)
	}
}

func TestJSONSelectToken(t *testing.T) {
	root := parseJSONMap(t, `{"a":{"b":[{"c":1},{"c":2}]},"n":null,"s":"v"}`)

	cases := []struct {
		path  string
		want  any
		found bool
	}{
		{"a.b[0].c", json.Number("1"), true},
		{"$.a.b[1].c", json.Number("2"), true},
		{"s", "v", true},
		{"a.missing", nil, false},      // missing key
		{"s.deeper", nil, false},       // scalar mid-path
		{"a.b[5]", nil, false},         // index out of range
		{"a.b[-1]", nil, false},        // negative index
		{"a.b[x]", nil, false},         // non-numeric index
		{"a.b[0", nil, false},          // unclosed bracket
		{"a.b[0].missing", nil, false}, // missing after index
		{"n.anything", nil, false},     // null mid-path
	}
	for _, tc := range cases {
		got, ok := jsonSelectToken(root, tc.path)
		if ok != tc.found {
			t.Errorf("jsonSelectToken(%q) ok = %v, want %v", tc.path, ok, tc.found)
			continue
		}
		if ok && got != tc.want {
			t.Errorf("jsonSelectToken(%q) = %v (%T), want %v", tc.path, got, got, tc.want)
		}
	}
	// empty path / "$" → root itself (identity, not equality)
	for _, p := range []string{"", "$"} {
		got, ok := jsonSelectToken(root, p)
		if !ok {
			t.Errorf("jsonSelectToken(%q) not found", p)
		} else if m, isMap := got.(map[string]any); !isMap || len(m) != len(root) {
			t.Errorf("jsonSelectToken(%q) did not return the root", p)
		}
	}
	// null value at an existing key IS found
	got, ok := jsonSelectToken(root, "n")
	if !ok || got != nil {
		t.Errorf("n = (%v, %v), want (nil, true)", got, ok)
	}
}

func TestJSONParseFieldSelector(t *testing.T) {
	root := parseJSONMap(t, `{"name":"Alpha","group":{"tag":"x"},"rows":[{"name":"a"},{"name":"b"}]}`)

	cases := []struct {
		name string
		doc  any
		sel  string
		want string
		ok   bool
	}{
		{"plain base", root, "name", "name", true},
		{"base with index", root, "rows[0].name", "rows[0].name", true},
		{"missing base", root, "nope", "", false},
		// :has(...) — nested filter args resolve via the upstream lookahead
		// semantics (the closing paren must be followed by ':' or EOS)
		{"has hit", root, "group:has(tag)", "group", true},
		{"has miss", root, "group:has(nope)", "", false},
		{"has nested hit", root, "group:has(tag:contains(x))", "group", true},
		{"has nested miss", root, "group:has(tag:contains(zzz))", "", false},
		// :not(...)
		{"not keeps when missing", root, "group:not(nope)", "group", true},
		{"not drops when present", root, "group:not(tag)", "", false},
		{"not nested keeps when inner misses", root, "group:not(tag:contains(zzz))", "group", true},
		{"not nested drops when inner hits", root, "group:not(tag:contains(x))", "", false},
		// unbalanced input matches nothing upstream, so no filter applies
		{"not unbalanced keeps", root, "group:not(tag:contains(zzz", "group", true},
		// :contains(...)
		{"contains hit", root, "name:contains(Alp)", "name", true},
		{"contains miss", root, "name:contains(zzz)", "", false},
		// unknown filter names are ignored (upstream logs and continues)
		{"unknown filter", root, "name:unknown(zzz)", "name", true},
		// empty base selector: filters apply to the document root itself;
		// success still yields the empty selector (upstream returns "",
		// not null)
		{"empty base contains", root, ":contains(Alpha)", "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := jsonParseFieldSelector(tc.doc, tc.sel)
			if got != tc.want || ok != tc.ok {
				t.Errorf("jsonParseFieldSelector(%q) = %q,%v, want %q,%v", tc.sel, got, ok, tc.want, tc.ok)
			}
		})
	}

	// ":contains" against an array joins elements before matching; the empty
	// base still reports success (upstream returns the empty selector)
	arr := []any{"one", "two"}
	if got, ok := jsonParseFieldSelector(arr, ":contains(two)"); got != "" || !ok {
		t.Errorf("array contains = %q,%v, want \"\",true", got, ok)
	}
	// a selector without a leading ':' has no filter groups; the whole
	// string is the base path and an unknown path rejects
	if got, ok := jsonParseFieldSelector(root, "contains(two)"); got != "" || ok {
		t.Errorf("plain path = %q,%v, want \"\",false", got, ok)
	}
	// nested inner filter behavior, called directly
	if got, ok := jsonParseFieldSelector(root, "group.tag:contains(x)"); got != "group.tag" || !ok {
		t.Errorf("inner contains hit = %q,%v, want group.tag,true", got, ok)
	}
	if got, ok := jsonParseFieldSelector(root, "group.tag:contains(zzz)"); got != "" || ok {
		t.Errorf("inner contains miss = %q,%v, want \"\",false", got, ok)
	}

	// chained filters: all must pass
	if got, ok := jsonParseFieldSelector(root, "group:has(tag):not(nope)"); got != "group" || !ok {
		t.Errorf("chained = %q,%v, want group,true", got, ok)
	}
	if got, ok := jsonParseFieldSelector(root, "group:has(tag):has(nope)"); got != "" || ok {
		t.Errorf("chained failing = %q,%v, want \"\",false", got, ok)
	}
}

func TestJSONParseRowsSelector(t *testing.T) {
	x := testCardigann(t)

	parsed := parseJSONMap(t, `{"items":[{"name":"keep me"},{"name":"drop me"},{"other":true}]}`)
	// unfiltered
	if rows := x.jsonParseRowsSelector(parsed, "items"); len(rows) != 3 {
		t.Fatalf("rows = %d, want 3", len(rows))
	}
	// missing selector → nil
	if rows := x.jsonParseRowsSelector(parsed, "nope"); rows != nil {
		t.Fatalf("missing selector = %v, want nil", rows)
	}
	// non-array target → nil
	if rows := x.jsonParseRowsSelector(parsed, "items[0]"); rows != nil {
		t.Fatalf("non-array = %v, want nil", rows)
	}
	// filter chain: only rows passing the chain are kept (upstream
	// JsonParseRowsSelector keeps rows where JsonParseFieldSelector != null)
	if rows := x.jsonParseRowsSelector(parsed, "items:has(name)"); len(rows) != 2 {
		t.Fatalf("filtered rows = %d, want 2", len(rows))
	}
	// nested filter args (upstream _JsonSelectorRegex lookahead semantics)
	if rows := x.jsonParseRowsSelector(parsed, "items:has(name:contains(keep))"); len(rows) != 1 {
		t.Fatalf("nested filtered rows = %d, want 1", len(rows))
	}
	// negative filter keeps only rows lacking the key
	if rows := x.jsonParseRowsSelector(parsed, "items:not(name)"); len(rows) != 1 {
		t.Fatalf("negated rows = %d, want 1", len(rows))
	}
	// templated row selector (if/else form, like the shipped definitions):
	// templates run with nil vars before selection
	if rows := x.jsonParseRowsSelector(parsed, `items{{ if .Freeleech }}:has(x){{ else }}{{ end }}`); len(rows) != 3 {
		t.Fatalf("templated selector = %v, want 3", rows)
	}
}

func TestHandleJSONSelectorCases(t *testing.T) {
	x := testCardigann(t)
	row := parseJSONMap(t, `{"state":"free","name":"Alpha"}`)

	// text shortcut renders the template
	v, err := x.handleJSONSelector(&SelectorBlock{Text: "pre {{ .Query.Q }}"}, row, TemplateVars{".Query.Q": "hi"}, true)
	if err != nil || v != "pre hi" {
		t.Fatalf("text shortcut = %q, %v", v, err)
	}
	// case match
	v, err = x.handleJSONSelector(&SelectorBlock{Selector: "state", Case: StringMap{{Key: "free", Value: "0"}}}, row, nil, true)
	if err != nil || v != "0" {
		t.Fatalf("case match = %q, %v", v, err)
	}
	// case default "*"
	v, err = x.handleJSONSelector(&SelectorBlock{Selector: "state", Case: StringMap{{Key: "other", Value: "1"}, {Key: "*", Value: "9"}}}, row, nil, true)
	if err != nil || v != "9" {
		t.Fatalf("case star = %q, %v", v, err)
	}
	// no case matched, optional → empty
	v, err = x.handleJSONSelector(&SelectorBlock{Selector: "state", Case: StringMap{{Key: "other", Value: "1"}}}, row, nil, false)
	if err != nil || v != "" {
		t.Fatalf("case optional miss = %q, %v", v, err)
	}
	// no case matched, required → error
	if _, err = x.handleJSONSelector(&SelectorBlock{Selector: "state", Case: StringMap{{Key: "other", Value: "1"}}}, row, nil, true); err == nil || !strings.Contains(err.Error(), "None of the case selectors matched") {
		t.Fatalf("case required miss err = %v", err)
	}
	// missing selector, required → error mentions the row JSON
	if _, err = x.handleJSONSelector(&SelectorBlock{Selector: "nope"}, row, nil, true); err == nil || !strings.Contains(err.Error(), `didn't match`) || !strings.Contains(err.Error(), `"name":"Alpha"`) {
		t.Fatalf("required miss err = %v", err)
	}
	// missing selector, optional → empty
	if v, err = x.handleJSONSelector(&SelectorBlock{Selector: "nope"}, row, nil, false); err != nil || v != "" {
		t.Fatalf("optional miss = %q, %v", v, err)
	}
	// array values are comma-joined
	arrRow := parseJSONMap(t, `{"tags":["x","y"]}`)
	if v, err = x.handleJSONSelector(&SelectorBlock{Selector: "tags"}, arrRow, nil, true); err != nil || v != "x,y" {
		t.Fatalf("array value = %q, %v", v, err)
	}
}

func TestParseJSONErrors(t *testing.T) {
	if _, err := parseJSON([]byte(`{invalid`)); err == nil {
		t.Fatal("invalid JSON must error")
	}
	v, err := parseJSON([]byte(`{"n": 5}`))
	if err != nil {
		t.Fatal(err)
	}
	m := v.(map[string]any)
	if n, ok := m["n"].(json.Number); !ok || n.String() != "5" {
		t.Fatalf("numbers must stay json.Number: %T", m["n"])
	}
}
