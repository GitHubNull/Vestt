// JSON response selector engine (SPEC-06).
//
// Ported from: Jackett.Common/Indexers/Definitions/CardigannIndexer.cs
// handleJsonSelector (1368-1420), JsonParseRowsSelector (2491-2508),
// JsonParseFieldSelector (2510-2580).
package engine

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// parseJSON decodes a JSON document preserving number representations
// (upstream JToken semantics: integers stay integers).
func parseJSON(data []byte) (any, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, err
	}
	return v, nil
}

// jsonSelectToken walks a Newtonsoft-SelectToken-style path: dot-separated
// keys with optional [index] array access and a leading '$'. Returns the
// value and whether it was found.
func jsonSelectToken(root any, path string) (any, bool) {
	path = strings.TrimPrefix(path, "$")
	path = strings.TrimPrefix(path, ".")
	cur := root
	if path == "" {
		return cur, true
	}
	for _, seg := range strings.Split(path, ".") {
		if seg == "" {
			continue
		}
		// split key[N][M] suffixes
		for {
			open := strings.IndexByte(seg, '[')
			if open < 0 {
				break
			}
			key := seg[:open]
			if key != "" {
				m, ok := cur.(map[string]any)
				if !ok {
					return nil, false
				}
				cur, ok = m[key]
				if !ok {
					return nil, false
				}
			}
			close := strings.IndexByte(seg, ']')
			if close < 0 {
				return nil, false
			}
			idx, err := strconv.Atoi(seg[open+1 : close])
			if err != nil {
				return nil, false
			}
			arr, ok := cur.([]any)
			if !ok || idx < 0 || idx >= len(arr) {
				return nil, false
			}
			cur = arr[idx]
			seg = seg[close+1:]
		}
		if seg == "" {
			continue
		}
		m, ok := cur.(map[string]any)
		if !ok {
			return nil, false
		}
		cur, ok = m[seg]
		if !ok {
			return nil, false
		}
	}
	return cur, true
}

// jsonIsoDateRe mirrors Newtonsoft's DateParseHandling.DateTime: strings
// shaped like an ISO-8601 date/time become DateTime tokens at parse time and
// render invariant-culture ("MM/dd/yyyy HH:mm:ss").
var jsonIsoDateRe = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}([T ][0-9]{2}:[0-9]{2}(:[0-9]{2}(\.[0-9]+)?)?(Z|[+-][0-9]{2}:?[0-9]{2})?)?$`)

var jsonIsoDateLayouts = []string{
	time.RFC3339Nano,
	"2006-01-02T15:04:05.999999999Z0700",
	"2006-01-02T15:04:05.999999999",
	"2006-01-02 15:04:05.999999999Z07:00",
	"2006-01-02 15:04:05.999999999",
	"2006-01-02T15:04Z07:00",
	"2006-01-02T15:04",
	"2006-01-02 15:04",
	"2006-01-02",
}

// tryDotnetDateToken converts an ISO-8601-shaped JSON string the way
// Newtonsoft would (DateTime token rendered invariant-culture).
func tryDotnetDateToken(s string) (string, bool) {
	if !jsonIsoDateRe.MatchString(s) {
		return "", false
	}
	for _, layout := range jsonIsoDateLayouts {
		if dt, err := time.Parse(layout, s); err == nil {
			return dt.Format("01/02/2006 15:04:05"), true
		}
	}
	return "", false
}

// jsonTokenString renders a decoded JSON value like the upstream
// selection.Value<string>() / JArray comma join: scalars via their literal
// representation, arrays comma-joined, objects compact JSON.
func jsonTokenString(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		if dt, ok := tryDotnetDateToken(t); ok {
			return dt
		}
		return t
	case json.Number:
		return t.String()
	case bool:
		if t {
			return "True"
		}
		return "False"
	case []any:
		parts := make([]string, 0, len(t))
		for _, e := range t {
			parts = append(parts, jsonTokenString(e))
		}
		return strings.Join(parts, ",")
	case map[string]any:
		b, err := json.Marshal(t)
		if err != nil {
			return ""
		}
		return string(b)
	default:
		b, err := json.Marshal(t)
		if err != nil {
			return ""
		}
		return string(b)
	}
}

// jsonNextFilterMatch locates the next ":filter(key)" group in s exactly as
// upstream _JsonSelectorRegex does (CardigannIndexer.cs:73):
//
//	\:(?<filter>.+?)\((?<key>.+?)\)(?=:|\z)
//
// Go's RE2 engine has no lookahead, so the closing-paren rule — the first
// ')' followed by ':' or end-of-string — is scanned manually. The scan also
// reproduces the regex engine's backtracking: the key may span inner parens
// (nested filters like ":has(:contains(x))") and the filter may grow past
// an inner '(' when no valid close exists. Returns the filter name, the key
// and the index one past the closing paren.
func jsonNextFilterMatch(s string) (filter, key string, end int, ok bool) {
	for i := 0; i < len(s); i++ {
		if s[i] != ':' {
			continue
		}
		for p := i + 1; p < len(s); p++ {
			if s[p] != '(' || p == i+1 {
				continue // ".+?" requires a non-empty filter name
			}
			for k := p + 1; k < len(s); k++ {
				if s[k] != ')' {
					continue
				}
				if k+1 < len(s) && s[k+1] != ':' {
					continue // lookahead (?=:|\z)
				}
				return s[i+1 : p], s[p+1 : k], k + 1, true
			}
			// no valid close for this '(': backtrack and grow the filter
			// past the inner paren by trying the next '('
		}
	}
	return "", "", 0, false
}

// jsonParseFieldSelector ports JsonParseFieldSelector (CardigannIndexer.cs
// 2510-2580): validates the ":has(...)"/":not(...)"/":contains(...)" filter
// chain of a rows selector and returns the base selector to feed
// SelectToken. ok mirrors upstream's null return: false when any filter
// rejects the node (upstream returns null, which is distinct from the empty
// string it returns on success — that distinction is load-bearing in
// JsonParseRowsSelector).
func jsonParseFieldSelector(parsed any, rowSelector string) (string, bool) {
	selector := strings.SplitN(rowSelector, ":", 2)[0]
	var parsedObject any
	var ok bool
	if strings.TrimSpace(selector) == "" {
		parsedObject = parsed
	} else if parsedObject, ok = jsonSelectToken(parsed, selector); !ok {
		return "", false
	}

	rest := rowSelector[len(selector):]
	for {
		filter, key, end, ok := jsonNextFilterMatch(rest)
		if !ok {
			break
		}
		rest = rest[end:]
		switch filter {
		case "has":
			if _, _, _, nested := jsonNextFilterMatch(key); nested {
				// upstream: innerMatch.Success → recurse with the full key
				if _, hit := jsonParseFieldSelector(parsedObject, key); !hit {
					return "", false
				}
			} else if _, hit := jsonSelectToken(parsedObject, key); !hit {
				return "", false
			}
		case "not":
			if _, _, _, nested := jsonNextFilterMatch(key); nested {
				if _, hit := jsonParseFieldSelector(parsedObject, key); hit {
					return "", false
				}
			} else if _, hit := jsonSelectToken(parsedObject, key); hit {
				return "", false
			}
		case "contains":
			if !strings.Contains(jsonTokenString(parsedObject), key) {
				return "", false
			}
		default:
			// upstream logs the error and continues with the next match
		}
	}
	return selector, true
}

// jsonParseRowsSelector ports JsonParseRowsSelector: applies the base
// selector, keeps only array elements passing the filter chain, nil on
// failure (upstream catches all exceptions and returns null). The selector
// is templated with base variables only (upstream calls
// applyGoTemplateText(rowSelector) without variables).
func (x *Cardigann) jsonParseRowsSelector(parsed any, rowSelector string) []any {
	rowSelector = ApplyTemplate(rowSelector, nil, nil)
	selector := strings.SplitN(rowSelector, ":", 2)[0]

	rowsObj, ok := jsonSelectToken(parsed, selector)
	if !ok {
		x.log().Debug(fmt.Sprintf("CardigannIndexer (%s): rows selector %q matched nothing", x.ID(), selector))
		return nil
	}
	arr, ok := rowsObj.([]any)
	if !ok {
		return nil
	}
	rest := rowSelector[len(selector):]
	out := make([]any, 0, len(arr))
	for _, t := range arr {
		// upstream keeps rows where JsonParseFieldSelector != null; with an
		// empty rest the call returns the empty selector (success), so no
		// short-circuit is needed
		if _, keep := jsonParseFieldSelector(t, rest); keep {
			out = append(out, t)
		}
	}
	return out
}

// handleJSONSelector ports handleJsonSelector (CardigannIndexer.cs
// 1368-1420): text shortcut, dotted-path selection (arrays comma-joined),
// case switching, then filters on the normalized value.
func (x *Cardigann) handleJSONSelector(sel *SelectorBlock, parent any, vars TemplateVars, required bool) (string, error) {
	if sel.Text != "" {
		return x.applyFilters(ApplyTemplate(sel.Text, vars, nil), sel.Filters, vars), nil
	}

	var value string
	haveValue := false

	if sel.Selector != "" {
		selectorSelector := ApplyTemplate(strings.TrimLeft(sel.Selector, "."), vars, nil)
		fieldSelector, selOK := jsonParseFieldSelector(parent, selectorSelector)

		var selection any
		found := false
		if selOK {
			// upstream checks fieldSelector != null only; Newtonsoft's
			// SelectToken("") returns the root document, which
			// jsonSelectToken("", → root) mirrors
			selection, found = jsonSelectToken(parent, fieldSelector)
		}

		if !found {
			if required {
				b, _ := json.Marshal(parent)
				return "", fmt.Errorf("Selector %q didn't match %s", selectorSelector, string(b)) //nolint:staticcheck // 上游文案 CardigannIndexer.cs:1393
			}
			return "", nil
		}

		if arr, ok := selection.([]any); ok {
			// turn this json array into a comma-delimited string
			value = jsonTokenString(arr)
		} else {
			value = jsonTokenString(selection)
		}
		haveValue = true
	}

	if sel.Case != nil {
		matched := false
		for _, switchCase := range sel.Case {
			if (haveValue && value == switchCase.Key) || switchCase.Key == "*" {
				value = ApplyTemplate(switchCase.Value, vars, nil)
				matched = true
				break
			}
		}
		if !matched {
			if required {
				b, _ := json.Marshal(parent)
				return "", fmt.Errorf("None of the case selectors matched %s", string(b)) //nolint:staticcheck // 上游文案 CardigannIndexer.cs:1426
			}
			return "", nil
		}
	}

	return x.applyFilters(normalizeSpace(value), sel.Filters, vars), nil
}
