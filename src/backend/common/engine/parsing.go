// Search response parsing pipeline (SPEC-06).
//
// Ported from: Jackett.Common/Indexers/Definitions/CardigannIndexer.cs
// PerformQuery response half (1553-1919), CheckIfLoginIsNeeded (951-998),
// Relogin (874-890).
package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/PuerkitoBio/goquery"
	"golang.org/x/net/html"
	"golang.org/x/text/encoding/unicode"

	"vestt/common/httpclient"
	"vestt/common/models"
	"vestt/common/utils/dateutil"
)

// decodeBody renders response bytes with the definition encoding (upstream
// WebResult.ContentString semantics for the indexer's encoding).
func (x *Cardigann) decodeBody(body []byte) string {
	enc := x.encoder()
	if enc == unicode.UTF8 {
		return string(body)
	}
	decoded, err := enc.NewDecoder().Bytes(body)
	if err != nil {
		return string(body)
	}
	if !utf8.Valid(decoded) {
		return string(body)
	}
	return string(decoded)
}

// checkIfLoginNeeded ports CheckIfLoginIsNeeded (951-998); the error return
// carries the upstream cross-domain ExceptionWithConfigData.
func (x *Cardigann) checkIfLoginNeeded(res *httpclient.Result, requestURL string) (bool, error) {
	if res.StatusCode >= 300 && res.StatusCode < 400 {
		location := res.Headers.Get("Location")
		if hint := x.redirectDomainHint(requestURL, location); hint != "" {
			errMsg := "Got redirected to another domain. Try changing the indexer URL to " + hint + "."
			if x.Definition.Followredirect {
				if cd := x.ConfigData(); cd != nil && cd.SiteLink != nil {
					cd.SiteLink.Value = hint
					x.SiteLinkValue = hint
					x.SaveConfig(context.Background())
				}
				errMsg += " Updated site link, please try again."
			}
			return false, errors.New(errMsg)
		}
		x.log().Error(fmt.Sprintf("Redirected to: %s", location))
		return true, nil
	}

	login := x.Definition.Login
	if login == nil || login.Test == nil {
		return false, nil
	}

	contentType := res.Headers.Get("Content-Type")
	if login.Test.Selector != "" && (contentType == "" || strings.Contains(contentType, "text/html")) {
		doc, err := parseHTML(x.decodeBody(res.Body))
		if err == nil && querySelectorAll(doc.Selection, login.Test.Selector).Length() == 0 {
			return true, nil
		}
	}
	return false, nil
}

// relogin ports Relogin (874-890).
func (x *Cardigann) relogin(ctx context.Context) error {
	x.log().Info(fmt.Sprintf("CardigannIndexer(%s): Relogin required", x.ID()))
	gotCaptcha, err := x.doLogin(ctx, x.CookieHeader())
	if err != nil {
		return err
	}
	if gotCaptcha {
		return errors.New("Relogin failed") //nolint:staticcheck // 上游文案 CardigannIndexer.cs:882
	}
	if _, err := x.testLogin(ctx); err != nil {
		return err
	}
	// the new cookies may not be saved by UpdateCookieHeader if the next
	// responses don't set cookies
	if x.IsConfigured() {
		x.SaveConfig(ctx)
	}
	return nil
}

// parseSearchResponse ports the response half of PerformQuery. vars is the
// request-generation template variable set (upstream shares it across all
// search paths and pollutes it with ".Result.*" entries).
func (x *Cardigann) parseSearchResponse(ctx context.Context, res *httpclient.Result, req searchRequest, vars TemplateVars, q *models.TorznabQuery) ([]*models.ReleaseInfo, error) {
	search := x.Definition.Search

	if req.searchPath.Response != nil && req.searchPath.Response.Type == "json" {
		return x.parseJSONResponse(ctx, res, req, vars, q)
	}

	results := x.decodeBody(res.Body)

	if req.searchPath.Response != nil && req.searchPath.Response.Type == "xml" {
		return x.parseRowsResponse(res, req, vars, q, results, false)
	}

	// HTML path: check if we need to login again
	loginNeeded, err := x.checkIfLoginNeeded(res, req.URL)
	if err != nil {
		return nil, err
	}
	if loginNeeded {
		if err := x.relogin(ctx); err != nil {
			return nil, err
		}
		res2, err := x.requestWithCookies(ctx, req.URL, x.CookieHeader(), req.Method, "", nil, req.RawBody, req.Headers)
		if err != nil {
			return nil, err
		}
		res = res2
		results = x.decodeBody(res.Body)
	}

	if err := x.checkForError(res, search.Error); err != nil {
		return nil, err
	}

	return x.parseRowsResponse(res, req, vars, q, results, true)
}

// parseJSONResponse ports the JSON branch of PerformQuery.
func (x *Cardigann) parseJSONResponse(ctx context.Context, res *httpclient.Result, req searchRequest, vars TemplateVars, q *models.TorznabQuery) ([]*models.ReleaseInfo, error) {
	search := x.Definition.Search
	resp := req.searchPath.Response

	// check if we need to login again
	loginNeeded, err := x.checkIfLoginNeeded(res, req.URL)
	if err != nil {
		return nil, err
	}
	if loginNeeded {
		if err := x.relogin(ctx); err != nil {
			return nil, err
		}
		res2, err := x.requestWithCookies(ctx, req.URL, x.CookieHeader(), req.Method, "", nil, req.RawBody, req.Headers)
		if err != nil {
			return nil, err
		}
		res = res2
	}

	results := x.decodeBody(res.Body)

	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Error Parsing Json Response: Status=%d Response=%s", res.StatusCode, results) //nolint:staticcheck // 上游文案 CardigannIndexer.cs:1637
	}

	if resp.NoResultsMessage != "" && strings.Contains(results, resp.NoResultsMessage) ||
		(resp.NoResultsMessage == "" && results == "") {
		return nil, nil
	}

	parsed, err := parseJSON(res.Body)
	if err != nil {
		x.log().Warn(fmt.Sprintf("Unexpected response content (%d bytes): %s", len(res.Body), results))
		return nil, fmt.Errorf("Error Parsing Json Response: %w", err) //nolint:staticcheck // 上游文案 CardigannIndexer.cs:1657
	}
	if parsed == nil {
		return nil, errors.New("Error Parsing Json Response") //nolint:staticcheck // 上游文案 CardigannIndexer.cs:1662
	}

	if search.Rows.Count != nil {
		func() {
			defer func() {
				if r := recover(); r != nil {
					x.log().Debug(fmt.Sprintf("CardigannIndexer (%s): Failed to parse JSON rows count: %v", x.ID(), r))
				}
			}()
			countVal, err := x.handleJSONSelector(search.Rows.Count, parsed, vars, false)
			if err == nil {
				if n, err := strconv.Atoi(strings.TrimSpace(countVal)); err == nil && n < 1 {
					// zero rows → this path yields nothing
					parsed = nil
				}
			}
		}()
		if parsed == nil {
			return nil, nil
		}
	}

	rowsArray := x.jsonParseRowsSelector(parsed, search.Rows.Selector)
	if rowsArray == nil {
		if search.Rows.MissingAttributeEqualsNoResults {
			return nil, nil
		}
		return nil, errors.New("Error Parsing Rows Selector. There are 0 rows.") //nolint:staticcheck // 上游文案 CardigannIndexer.cs:1691
	}
	if len(rowsArray) == 0 {
		return nil, nil
	}

	searchURL := req.URL
	var releases []*models.ReleaseInfo

	for _, row := range rowsArray {
		selObj := row

		if search.Rows.Attribute != "" {
			attr, ok := jsonSelectToken(row, search.Rows.Attribute)
			if !ok {
				if search.Rows.MissingAttributeEqualsNoResults {
					continue
				}
				return nil, fmt.Errorf("Error Parsing Rows Selector. There are 0 rows.") //nolint:staticcheck // 上游文案 CardigannIndexer.cs:1691
			}
			selObj = attr
		}

		var mulRows []any
		if search.Rows.Multiple {
			switch t := selObj.(type) {
			case []any:
				mulRows = t
			case map[string]any:
				// JObject.Values<JObject>() — Go maps lose insertion order;
				// keys are sorted for determinism (see SPEC-06 澄清记录)
				mulRows = sortedMapValues(t)
			default:
				mulRows = []any{selObj}
			}
		} else {
			mulRows = []any{selObj}
		}

		for _, mulRow := range mulRows {
			release := &models.ReleaseInfo{}
			skip, err := x.parseJSONFields(mulRow, row, release, vars, searchURL, q)
			if err != nil {
				return nil, err
			}
			if skip {
				continue
			}
			releases = append(releases, release)
		}
	}

	if q.Limit > 0 && len(releases) > q.Limit {
		releases = releases[:q.Limit]
	}
	return releases, nil
}

// parseJSONFields runs the per-row field loop of the JSON branch (upstream
// has NO per-row try/catch here: non-optional field errors are fatal).
func (x *Cardigann) parseJSONFields(mulRow, row any, release *models.ReleaseInfo, vars TemplateVars, searchURL string, q *models.TorznabQuery) (bool, error) {
	search := x.Definition.Search

	for _, field := range search.Fields {
		fieldParts := strings.Split(field.Name, "|")
		fieldName := fieldParts[0]
		var modifiers []string
		for i := 1; i < len(fieldParts); i++ {
			modifiers = append(modifiers, fieldParts[i])
		}

		var value string
		variablesKey := ".Result." + fieldName
		isOptional := isOptionalField(field.Name) || stringInSlice("optional", modifiers) || (field.Selector != nil && field.Selector.Optional)

		parentObj := mulRow
		if field.Selector != nil && field.Selector.Selector != "" && strings.HasPrefix(field.Selector.Selector, "..") {
			parentObj = row
		}

		v, err := x.handleJSONSelector(field.Selector, parentObj, vars, !isOptional)
		if err == nil {
			value = v
			if isOptional && strings.TrimSpace(value) == "" {
				defaultValue := ApplyTemplate(field.Selector.Default, vars, nil)
				if strings.TrimSpace(defaultValue) == "" {
					vars[variablesKey] = nil
					continue
				}
				value = defaultValue
			}
			parsed, perr := x.parseFields(value, fieldName, release, modifiers, searchURL)
			if perr != nil {
				return false, perr
			}
			vars[variablesKey] = parsed
		} else {
			if _, seen := vars[variablesKey]; !seen || isOptional {
				vars[variablesKey] = nil
			}
			if isOptional {
				continue
			}
			return false, fmt.Errorf("Error while parsing field=%s, selector=%s, value=%s: %w", //nolint:staticcheck // 上游文案 CardigannIndexer.cs:1762
				field.Name, selectorName(field.Selector), value, err)
		}
	}

	if x.parseRowFilters(search.Rows.Filters, release, q, vars, jsonDebugString(row)) {
		return true, nil
	}
	return false, nil
}

// parseRowsResponse covers the shared XML/HTML rows loop (upstream keeps the
// loops textually separate; both share row walking, After merging, field
// extraction, row filters and dateheaders).
func (x *Cardigann) parseRowsResponse(res *httpclient.Result, req searchRequest, vars TemplateVars, q *models.TorznabQuery, results string, isHTML bool) ([]*models.ReleaseInfo, error) {
	search := x.Definition.Search

	if search.Preprocessingfilters != nil {
		results = x.applyFilters(results, search.Preprocessingfilters, vars)
		x.log().Debug(fmt.Sprintf("CardigannIndexer (%s): result after preprocessingfilters: %s", x.ID(), results))
	}

	// upstream parses XML with AngleSharp's XmlParser; the HTML parser is a
	// faithful stand-in for the selector subsets the definitions use
	doc, err := parseHTML(results)
	if err != nil {
		x.onParseError(results, err)
		return nil, nil
	}

	rowsSelector := ApplyTemplate(search.Rows.Selector, vars, nil)
	rowsSel := querySelectorAll(doc.Selection, rowsSelector)
	var rows []*goquery.Selection
	rowsSel.Each(func(_ int, s *goquery.Selection) {
		rows = append(rows, s)
	})

	// merge following rows for the After selector
	if after := search.Rows.After; after > 0 {
		merged := make([]*goquery.Selection, 0, len(rows))
		for i := 0; i < len(rows); i++ {
			cur := rows[i]
			for j := 0; j < after; j++ {
				mergeIdx := i + j + 1
				if mergeIdx >= len(rows) {
					break
				}
				for _, n := range rows[mergeIdx].Children().Nodes {
					cur.AppendNodes(n)
				}
			}
			merged = append(merged, cur)
			i += after // skip consumed rows (upstream RemoveRange)
		}
		rows = merged
	}

	searchURL := req.URL
	var releases []*models.ReleaseInfo

	for _, row := range rows {
		parsed, err := x.parseHTMLRowFields(row, vars, searchURL, q)
		if err != nil {
			// upstream catches per row, logs and continues
			x.log().Error(fmt.Sprintf("CardigannIndexer (%s): Error while parsing row '%s':\n\n%v", x.ID(), goqueryOuterHTML(row), err))
			continue
		}
		if parsed == nil {
			continue
		}
		releases = append(releases, parsed)
	}

	if q.Limit > 0 && len(releases) > q.Limit {
		releases = releases[:q.Limit]
	}
	return releases, nil
}

// parseHTMLRowFields is the per-row body of the HTML/XML loop; nil release
// means the row was dropped (row filters or missing download).
func (x *Cardigann) parseHTMLRowFields(row *goquery.Selection, vars TemplateVars, searchURL string, q *models.TorznabQuery) (*models.ReleaseInfo, error) {
	search := x.Definition.Search
	release := &models.ReleaseInfo{}

	for _, field := range search.Fields {
		fieldParts := strings.Split(field.Name, "|")
		fieldName := fieldParts[0]
		var modifiers []string
		for i := 1; i < len(fieldParts); i++ {
			modifiers = append(modifiers, fieldParts[i])
		}

		var value string
		variablesKey := ".Result." + fieldName
		isOptional := isOptionalField(field.Name) || stringInSlice("optional", modifiers) || (field.Selector != nil && field.Selector.Optional)

		value, herr := x.handleSelector(field.Selector, row, vars, !isOptional)
		if herr == nil {
			if isOptional && strings.TrimSpace(value) == "" {
				defaultValue := ApplyTemplate(field.Selector.Default, vars, nil)
				if strings.TrimSpace(defaultValue) == "" {
					vars[variablesKey] = nil
					continue
				}
				value = defaultValue
			}
			parsed, perr := x.parseFields(value, fieldName, release, modifiers, searchURL)
			if perr != nil {
				return nil, perr
			}
			vars[variablesKey] = parsed
		} else {
			if _, seen := vars[variablesKey]; !seen || isOptional {
				vars[variablesKey] = nil
			}
			if isOptional {
				continue
			}
			return nil, fmt.Errorf("Error while parsing field=%s, selector=%s, value=%s: %w", //nolint:staticcheck // 上游文案 CardigannIndexer.cs:1900
				field.Name, selectorName(field.Selector), value, herr)
		}
	}

	if x.parseRowFilters(search.Rows.Filters, release, q, vars, goqueryOuterHTML(row)) {
		return nil, nil
	}

	// if DateHeaders is set go through the previous rows and look for the
	// header selector
	dateHeaders := search.Rows.Dateheaders
	if release.PublishDate.Equal(zeroTime) && dateHeaders != nil {
		var value string
		prev := prevElementSibling(row)
		for prev != nil {
			cur := prev
			v, err := x.handleSelector(dateHeaders, cur, vars, false)
			if err == nil && v != "" {
				value = v
				break
			}
			prev = prevElementSibling(cur)
		}
		if value == "" && !dateHeaders.Optional {
			return nil, fmt.Errorf("No date header row found") //nolint:staticcheck // 上游文案 CardigannIndexer.cs:1945
		}
		if value != "" {
			t, _ := dateutil.FromUnknown(value, "")
			release.PublishDate = t
		}
	}

	return release, nil
}

// prevElementSibling mirrors the upstream dateheaders walk
// (`dateHeaderRow.PreviousElementSibling ?? dateHeaderRow.Parent?.PreviousElementSibling`):
// the previous element sibling, or — when at the start of a container — the
// parent's previous element sibling. Returns nil at the document start.
func prevElementSibling(sel *goquery.Selection) *goquery.Selection {
	if sel == nil || len(sel.Nodes) == 0 {
		return nil
	}
	node := sel.Nodes[0]
	prev := node.PrevSibling
	for prev != nil && prev.Type != html.ElementNode {
		prev = prev.PrevSibling
	}
	if prev == nil {
		parent := node.Parent
		if parent == nil {
			return nil
		}
		prev = parent.PrevSibling
		for prev != nil && prev.Type != html.ElementNode {
			prev = prev.PrevSibling
		}
	}
	if prev == nil {
		return nil
	}
	return &goquery.Selection{Nodes: []*html.Node{prev}}
}

// selectorName renders a selector for error messages.
func selectorName(sel *SelectorBlock) string {
	if sel == nil {
		return ""
	}
	if sel.Selector != "" {
		return sel.Selector
	}
	return sel.Text
}

// jsonDebugString renders a JSON row for strdump/debug output.
func jsonDebugString(v any) string {
	switch t := v.(type) {
	case map[string]any, []any:
		b, err := json.Marshal(t)
		if err != nil {
			return fmt.Sprintf("%v", v)
		}
		return string(b)
	default:
		return jsonTokenString(v)
	}
}

// sortedMapValues returns map values ordered by key (see 澄清记录).
func sortedMapValues(m map[string]any) []any {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]any, 0, len(m))
	for _, k := range keys {
		out = append(out, m[k])
	}
	return out
}

// onParseError ports OnParseError: logs the document excerpt.
func (x *Cardigann) onParseError(results string, err error) {
	const maxDump = 512
	dump := results
	if len(dump) > maxDump {
		dump = dump[:maxDump] + "..."
	}
	x.log().Error(fmt.Sprintf("CardigannIndexer (%s): Error while parsing a response: %v\nResponse:\n%s", x.ID(), err, dump))
}
