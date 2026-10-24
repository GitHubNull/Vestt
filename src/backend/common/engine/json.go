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

// jsonColonFilterRe mirrors _JsonSelectorRegex: ":filter(key)" pairs.
var jsonColonFilterRe = regexp.MustCompile(`:([A-Za-z]+)\(([^)]*)\)`)

// jsonParseFieldSelector ports JsonParseFieldSelector: validates the
// ":has(...)"/":not(...)"/":contains(...)" filter chain of a rows selector
// and returns the base selector to feed SelectToken ("" when the row is
// filtered out).
func jsonParseFieldSelector(parsed any, rowSelector string) string {
	selector := strings.SplitN(rowSelector, ":", 2)[0]
	var parsedObject any
	var ok bool
	if strings.TrimSpace(selector) == "" {
		parsedObject = parsed
	} else if parsedObject, ok = jsonSelectToken(parsed, selector); !ok {
		return ""
	}

	rest := rowSelector[len(selector):]
	for len(rest) > 0 {
		m := jsonColonFilterRe.FindStringSubmatchIndex(rest)
		if m == nil {
			break
		}
		filter := rest[m[2]:m[3]]
		key := rest[m[4]:m[5]]
		rest = rest[m[1]:]
		switch filter {
		case "has":
			if strings.Contains(key, ":") {
				if jsonParseFieldSelector(parsedObject, key) == "" {
					return ""
				}
			} else if _, ok := jsonSelectToken(parsedObject, key); !ok {
				return ""
			}
		case "not":
			if strings.Contains(key, ":") {
				if jsonParseFieldSelector(parsedObject, key) != "" {
					return ""
				}
			} else if _, ok := jsonSelectToken(parsedObject, key); ok {
				return ""
			}
		case "contains":
			if !strings.Contains(jsonTokenString(parsedObject), key) {
				return ""
			}
		default:
			// upstream logs the error and continues with the next match
		}
	}
	return selector
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
		if rest == "" || jsonParseFieldSelector(t, rest) != "" {
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
		fieldSelector := jsonParseFieldSelector(parent, selectorSelector)

		var selection any
		found := false
		if fieldSelector != "" {
			selection, found = jsonSelectToken(parent, fieldSelector)
		}

		if !found {
			if required {
				b, _ := json.Marshal(parent)
				return "", fmt.Errorf("Selector %q didn't match %s", selectorSelector, string(b))
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
				haveValue = true
				matched = true
				break
			}
		}
		if !matched {
			if required {
				b, _ := json.Marshal(parent)
				return "", fmt.Errorf("None of the case selectors matched %s", string(b))
			}
			return "", nil
		}
	}

	return x.applyFilters(normalizeSpace(value), sel.Filters, vars), nil
}
