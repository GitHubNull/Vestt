package engine

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"html"
	"strconv"
	"strings"
	"unicode"

	"github.com/dlclark/regexp2"
	"golang.org/x/text/runes"
	"golang.org/x/text/transform"
	"golang.org/x/text/unicode/norm"

	"vestt/common/utils/dateutil"
	"vestt/common/utils/parseutil"
)

// applyFilters ports CardigannIndexer.applyFilters (CardigannIndexer.cs
// 1114-1275). Filter Args shapes: scalar (string/int64/float64) or []any of
// scalars, mirroring the upstream dynamic yaml decoding.
func (x *Cardigann) applyFilters(data string, filters []FilterBlock, vars TemplateVars) string {
	if filters == nil {
		return data
	}
	for _, filter := range filters {
		args, _ := filter.Args.([]any)
		argStr := func(i int) string {
			if i >= len(args) {
				return ""
			}
			return fmt.Sprintf("%v", args[i])
		}
		switch filter.Name {
		case "querystring":
			param, _ := filter.Args.(string)
			data = parseutil.GetArgumentFromQueryString(data, param)
		case "timeparse", "dateparse":
			layout, _ := filter.Args.(string)
			dt, err := dateutil.ParseDateTimeGoLang(data, layout)
			if err != nil {
				x.log().Debug(fmt.Sprintf("CardigannIndexer (%s): %v", x.ID(), err))
			} else {
				data = dateutil.FormatRFC1123Z(dt)
			}
		case "regexp":
			// upstream uses System.Text.RegularExpressions; regexp2 is its
			// faithful port (lookbehind, \p{IsCyrillic}, .NET group syntax).
			pattern, _ := filter.Args.(string)
			re, err := regexp2.Compile(pattern, regexp2.None)
			if err != nil {
				break
			}
			// upstream: Match.Groups[1].Value — group 1 or empty, never the
			// whole match (a group-less pattern yields "")
			m, err := re.FindStringMatch(data)
			if err != nil || m == nil {
				data = ""
				break
			}
			data = dotnetGroupValue(m, 1)
		case "re_replace":
			pattern := argStr(0)
			replacement := ApplyTemplate(argStr(1), vars, nil)
			re, err := regexp2.Compile(pattern, regexp2.None)
			if err != nil {
				break
			}
			// regexp2 implements .NET substitution semantics ($1, ${name},
			// $$) natively.
			if replaced, err := re.Replace(data, replacement, 0, -1); err == nil {
				data = replaced
			}
		case "split":
			sep := argStr(0)
			posStr := argStr(1)
			pos, err := strconv.Atoi(posStr)
			if err != nil || sep == "" {
				break
			}
			// upstream splits on sep[0] (first char only)
			if sep == "" {
				break
			}
			parts := strings.Split(data, sep[:1])
			if pos < 0 {
				pos += len(parts)
			}
			if pos >= 0 && pos < len(parts) {
				data = parts[pos]
			}
			// out-of-range index: upstream throws (row dropped by the HTML
			// per-row catch / query failure in JSON); the port keeps the
			// original value (see SPEC-06 澄清记录)
		case "replace":
			from := argStr(0)
			to := ApplyTemplate(argStr(1), vars, nil)
			data = strings.ReplaceAll(data, from, to)
		case "trim":
			cutset, _ := filter.Args.(string)
			if cutset != "" {
				data = strings.Trim(data, cutset[:1])
			} else {
				data = strings.TrimSpace(data)
			}
		case "prepend":
			s, _ := filter.Args.(string)
			data = ApplyTemplate(s, vars, nil) + data
		case "append":
			s, _ := filter.Args.(string)
			data += ApplyTemplate(s, vars, nil)
		case "tolower":
			data = strings.ToLower(data)
		case "toupper":
			data = strings.ToUpper(data)
		case "base64decode":
			// upstream WebEncoders.Base64UrlDecode: URL-safe, padding optional
			if decoded, err := base64.RawURLEncoding.DecodeString(data); err == nil {
				data = string(decoded)
			} else if decoded, err := base64.URLEncoding.DecodeString(data); err == nil {
				data = string(decoded)
			} else if decoded, err := base64.StdEncoding.DecodeString(data); err == nil {
				data = string(decoded)
			}
		case "base64encode":
			// upstream WebEncoders.Base64UrlEncode: URL-safe without padding
			data = base64.RawURLEncoding.EncodeToString([]byte(data))
		case "urldecode":
			data = x.urlDecode(data)
		case "urlencode":
			data = netUrlEncode(data, x.encoder())
		case "htmldecode":
			data = html.UnescapeString(data)
		case "htmlencode":
			data = html.EscapeString(data)
		case "timeago", "reltime":
			if dt, err := dateutil.FromTimeAgo(data); err == nil {
				data = dateutil.FormatRFC1123Z(dt)
			}
		case "fuzzytime":
			if dt, err := dateutil.FromUnknown(data, ""); err == nil {
				data = dateutil.FormatRFC1123Z(dt)
			}
		case "validfilename":
			data = makeValidFileName(data, '_')
		case "diacritics":
			op, _ := filter.Args.(string)
			if op == "replace" {
				data = removeDiacritics(data)
			}
		case "jsonjoinarray":
			// port: join a JSON array selected by a JSONPath with a separator
			// (minimal JSONPath: dotted keys with numeric indexes)
			sep := argStr(1)
			if values, ok := jsonSelectStrings(data, argStr(0)); ok {
				data = strings.Join(values, sep)
			}
		case "hexdump":
			var sb strings.Builder
			for _, c := range data {
				sb.WriteString(fmt.Sprintf("%c(%02X)", c, c))
			}
			x.log().Debug(fmt.Sprintf("CardigannIndexer (%s): strdump: %s", x.ID(), sb.String()))
		case "strdump":
			tag, _ := filter.Args.(string)
			if tag != "" {
				tag = "(" + tag + "):"
			} else {
				tag = ":"
			}
			debugData := strings.ReplaceAll(data, "\r", "\\r")
			debugData = strings.ReplaceAll(debugData, "\n", "\\n")
			debugData = strings.ReplaceAll(debugData, " ", "\\xA0")
			x.log().Debug(fmt.Sprintf("CardigannIndexer (%s): strdump%s %s", x.ID(), tag, debugData))
		case "validate":
			delim := func(r rune) bool {
				switch r {
				case ',', ' ', '/', ')', '(', '.', ';', '[', ']', '"', '|', ':':
					return true
				}
				return false
			}
			argsStr, _ := filter.Args.(string)
			validList := strings.FieldsFunc(strings.ToLower(argsStr), delim)
			dataParts := strings.FieldsFunc(strings.ToLower(data), delim)
			inData := map[string]bool{}
			for _, d := range dataParts {
				inData[d] = true
			}
			// upstream List.Intersect preserves the validList order and is
			// distinct on both sides
			var intersect []string
			seen := map[string]bool{}
			for _, v := range validList {
				if inData[v] && !seen[v] {
					seen[v] = true
					intersect = append(intersect, v)
				}
			}
			data = strings.Join(intersect, ",")
		default:
			// unknown filter: upstream ignores it
		}
	}
	return data
}

// dotnetGroupValue mirrors Match.Groups[i].Value: the captured text, or ""
// when the group did not participate (or the match failed).
func dotnetGroupValue(m *regexp2.Match, i int) string {
	groups := m.Groups()
	if i >= len(groups) {
		return ""
	}
	g := groups[i]
	if len(g.Captures) == 0 {
		return ""
	}
	return g.Captures[0].String()
}

// urlDecode ports WebUtilityHelpers.UrlDecode(Data, Encoding): '+' becomes
// a space, %XX sequences become raw bytes (non-escaped runes are encoded to
// the definition encoding's bytes), and the byte buffer is decoded with the
// definition encoding.
func (x *Cardigann) urlDecode(s string) string {
	enc := x.encoder()
	var buf []byte
	flush := func(r rune) {
		if b, err := enc.NewEncoder().Bytes([]byte(string(r))); err == nil {
			buf = append(buf, b...)
		} else {
			buf = append(buf, string(r)...)
		}
	}
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '+':
			buf = append(buf, ' ')
		case '%':
			if i+2 < len(s) {
				if hi, err := strconv.ParseUint(s[i+1:i+2], 16, 8); err == nil {
					if lo, err := strconv.ParseUint(s[i+2:i+3], 16, 8); err == nil {
						buf = append(buf, byte(hi<<4|lo))
						i += 2
						continue
					}
				}
			}
			buf = append(buf, '%')
		default:
			flush(rune(s[i]))
		}
	}
	decoded, err := enc.NewDecoder().Bytes(buf)
	if err != nil {
		return string(buf)
	}
	return string(decoded)
}

// makeValidFileName mirrors StringUtil.MakeValidFileName(s, '_', false):
// Path.GetInvalidFileNameChars() on Linux is {'/', '\0'}; an empty result
// becomes "_".
func makeValidFileName(s string, replacement rune) string {
	var sb strings.Builder
	changed := false
	for _, c := range s {
		if c == '/' || c == 0 {
			changed = true
			sb.WriteRune(replacement)
		} else {
			sb.WriteRune(c)
		}
	}
	if sb.Len() == 0 {
		return "_"
	}
	if !changed {
		return s
	}
	return sb.String()
}

// removeDiacritics mirrors the upstream diacritics/replace filter
// (NFD decomposition stripping non-spacing marks, then NFC recomposition).
func removeDiacritics(s string) string {
	t := transform.Chain(norm.NFD, runes.Remove(runes.In(unicode.Mn)), norm.NFC)
	out, _, err := transform.String(t, s)
	if err != nil {
		return s
	}
	return out
}

// jsonSelectStrings implements the minimal JSONPath subset used with
// jsonjoinarray (dotted keys and numeric array indexes).
func jsonSelectStrings(data, path string) ([]string, bool) {
	var v any
	if err := json.Unmarshal([]byte(data), &v); err != nil {
		return nil, false
	}
	for _, part := range strings.Split(path, ".") {
		if idx, err := strconv.Atoi(part); err == nil {
			arr, ok := v.([]any)
			if !ok || idx >= len(arr) {
				return nil, false
			}
			v = arr[idx]
			continue
		}
		obj, ok := v.(map[string]any)
		if !ok {
			return nil, false
		}
		v, ok = obj[part]
		if !ok {
			return nil, false
		}
	}
	arr, ok := v.([]any)
	if !ok {
		return nil, false
	}
	out := make([]string, 0, len(arr))
	for _, item := range arr {
		out = append(out, fmt.Sprintf("%v", item))
	}
	return out, true
}
