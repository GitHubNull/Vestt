// Package shared ports the upstream hand-written indexer abstract base
// classes (Jackett.Common/Indexers/Definitions/Abstract/*) so concrete
// indexers can embed them. This file holds the small helpers shared by
// those ports.
package shared

import (
	"bytes"
	"encoding/json"
	"fmt"
	"html"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"vestt/common/indexer"
	"vestt/common/indexer/configdata"
)

// KV is one ordered query parameter (upstream KeyValuePair<string,string>).
type KV struct {
	Key   string
	Value string
}

// QueryParams is an ordered multi-map mirroring the upstream
// NameValueCollection usage in the abstract trackers: Add appends, Set
// replaces every existing entry of the key (or appends when absent), and
// GetQueryString encodes like upstream
// (StringUtil.GetQueryString → WebUtility.UrlEncode: '+' for spaces,
// upper-case hex, key order preserved, duplicate keys kept).
type QueryParams []KV

// Add appends one parameter.
func (p *QueryParams) Add(key, value string) { *p = append(*p, KV{Key: key, Value: value}) }

// Set mirrors NameValueCollection.Set: existing entries for key are removed
// and the new pair appended.
func (p *QueryParams) Set(key, value string) {
	out := make(QueryParams, 0, len(*p)+1)
	for _, kv := range *p {
		if kv.Key != key {
			out = append(out, kv)
		}
	}
	*p = append(out, KV{Key: key, Value: value})
}

// Encode mirrors WebUtility.UrlEncode (UTF-8): unreserved characters pass
// through, everything else becomes %XX (upper case), spaces become '+'.
func Encode(s string) string {
	return strings.ReplaceAll(url.QueryEscape(s), "%20", "+")
}

// GetQueryString renders "k=v&k=v" in insertion order (upstream
// StringUtil.GetQueryString).
func (p QueryParams) GetQueryString() string {
	parts := make([]string, len(p))
	for i, kv := range p {
		parts[i] = kv.Key + "=" + Encode(kv.Value)
	}
	return strings.Join(parts, "&")
}

// httpStatusNames maps the status codes the trackers handle to the .NET
// HttpStatusCode enum names used in upstream error messages
// (e.g. "Invalid status code 404 (NotFound) received from indexer").
var httpStatusNames = map[int]string{
	200: "OK", 201: "Created", 202: "Accepted", 204: "NoContent",
	301: "MovedPermanently", 302: "Found", 303: "SeeOther",
	307: "TemporaryRedirect", 308: "PermanentRedirect",
	400: "BadRequest", 401: "Unauthorized", 402: "PaymentRequired",
	403: "Forbidden", 404: "NotFound", 405: "MethodNotAllowed",
	408: "RequestTimeout", 409: "Conflict", 410: "Gone",
	412: "PreconditionFailed", 418: "Teapot", 429: "TooManyRequests",
	500: "InternalServerError", 501: "NotImplemented",
	502: "BadGateway", 503: "ServiceUnavailable", 504: "GatewayTimeout",
}

// StatusName returns the .NET enum name for code (empty when unknown).
func StatusName(code int) string { return httpStatusNames[code] }

// applyPostedItems mirrors upstream ApplyConfiguration's
// LoadValuesFromJson(configJson): the posted items are applied, then the
// result is reloaded through Base.LoadValuesFromJSON so the site-link
// normalisation (default link, trailing slash, legacy links) runs.
func applyPostedItems(b *indexer.Base, items []indexer.ConfigItem) error {
	if err := b.ApplyItems(items); err != nil {
		return err
	}
	raw, err := configdata.MarshalItems(b.Config)
	if err != nil {
		return err
	}
	return b.LoadValuesFromJSON(raw, false)
}

// logError writes to the indexer's logger when one is wired.
func logError(b *indexer.Base, msg string) {
	if b.Logger != nil {
		b.Logger.Error(msg)
	}
}

// parseFailure mirrors upstream OnParseError: the response dump is logged
// and the failure surfaces as Exception("Parse error", ex).
func parseFailure(b *indexer.Base, dump string, err error) error {
	logError(b, fmt.Sprintf("Error while parsing result: %v\n%s", err, dump))
	return &indexer.IndexerError{IndexerID: b.ID(), Kind: indexer.KindParse, Message: "Parse error"}
}

// --- loose JSON accessors -------------------------------------------------
// The Gazelle API returns inconsistent value types across trackers (some
// fields are strings in one tracker and numbers in another), which is why
// upstream uses Newtonsoft's loose JObject casts. These helpers mirror the
// casts; decoding uses json.Number to keep integer precision.

// decodeObject decodes one JSON object loosely (numbers as json.Number).
func decodeObject(data []byte) (map[string]any, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	var m map[string]any
	if err := dec.Decode(&m); err != nil {
		return nil, err
	}
	return m, nil
}

// jStr mirrors (string)JToken for JValue scalars: strings pass through,
// numbers render invariant, booleans render "True"/"False" (C# casing).
func jStr(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return t
	case json.Number:
		return t.String()
	case bool:
		if t {
			return "True"
		}
		return "False"
	default:
		return fmt.Sprintf("%v", t)
	}
}

// jInt mirrors numeric casts ((int)/(long)JToken).
func jInt(v any) (int64, bool) {
	switch t := v.(type) {
	case json.Number:
		n, err := t.Int64()
		if err == nil {
			return n, true
		}
		f, err := t.Float64()
		if err == nil {
			return int64(f), true
		}
	case float64:
		return int64(t), true
	case string:
		n, err := strconv.ParseInt(strings.TrimSpace(t), 10, 64)
		if err == nil {
			return n, true
		}
	}
	return 0, false
}

// jBoolLoose mirrors the two upstream coercion styles for boolean flags:
// a JSON bool, or a string/number parseable by bool.TryParse (note Go has
// no three-valued bool.TryParse on strings — "true"/"false" accepted,
// case-insensitive like .NET).
func jBoolLoose(v any) (bool, bool) {
	switch t := v.(type) {
	case nil:
		return false, false
	case bool:
		return t, true
	case string:
		b, err := strconv.ParseBool(strings.TrimSpace(t))
		if err == nil {
			return b, true
		}
	case json.Number:
		n, err := t.Int64()
		if err == nil {
			return n != 0, true
		}
	}
	return false, false
}

// firstIfSingleOrDefault mirrors upstream IEnumerableExtensions
// FirstIfSingleOrDefault: the element when the sequence holds exactly one,
// otherwise def.
func firstIfSingleOrDefault(list []string, def string) string {
	if len(list) == 1 {
		return list[0]
	}
	return def
}

// distinct preserves order while dropping duplicates (upstream .Distinct()).
func distinct(list []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(list))
	for _, s := range list {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

var tagStripRe = regexp.MustCompile(`(?s)<[^>]*>`)

// stripTags renders an HTML fragment as text: tags removed, entities
// decoded (stdlib only — goquery is intentionally not a dependency here).
func stripTags(fragment string) string {
	return html.UnescapeString(tagStripRe.ReplaceAllString(fragment, ""))
}
