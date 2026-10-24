// Search/download request generation for the cardigann engine (SPEC-05 §3.6).
//
// Ported from: Jackett.Common/Indexers/Definitions/CardigannIndexer.cs
// PerformQuery (1438-1542), HandleRequestAsync (1968-2007),
// MatchSelector (2028-2064), Download (2083-2146) and
// Jackett.Common/Utils/MagnetUtil.cs InfoHashToPublicMagnet.
package engine

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"vestt/common/httpclient"
	"vestt/common/models"

	"golang.org/x/text/encoding"
	"golang.org/x/text/encoding/htmlindex"
	"golang.org/x/text/encoding/unicode"
)

// searchRequest is one generated search request (SPEC-05 §3.6). For GET the
// URL carries the final query string; for POST RawBody is the url-encoded
// body (upstream List<KeyValuePair> order and duplicates preserved).
type searchRequest struct {
	Method     string
	URL        string
	Headers    map[string]string
	RawBody    string
	searchPath *SearchPathBlock
}

// queryPair preserves upstream List<KeyValuePair<string,string>> semantics
// (insertion order plus duplicate keys).
type queryPair struct {
	key   string
	value string
}

// encoder resolves the definition encoding (upstream Encoding); unknown
// labels fall back to UTF-8.
func (x *Cardigann) encoder() encoding.Encoding {
	if enc, err := htmlindex.Get(strings.ToLower(x.EncodingName)); err == nil && enc != nil {
		return enc
	}
	return unicode.UTF8
}

// netUrlEncode replicates .NET WebUtility/HttpUtility UrlEncode applied to
// the encoding's bytes (WebUtilityHelpers.UrlEncode): A-Za-z0-9 and
// - _ . ! * ' ( ) pass through, space becomes '+', everything else becomes
// uppercase %XX.
func netUrlEncode(s string, enc encoding.Encoding) string {
	var b []byte
	if enc == nil {
		b = []byte(s)
	} else {
		eb, err := enc.NewEncoder().Bytes([]byte(s))
		if err != nil {
			eb = []byte(s)
		}
		b = eb
	}
	var sb strings.Builder
	const hex = "0123456789ABCDEF"
	for _, c := range b {
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9',
			c == '-', c == '_', c == '.', c == '!', c == '*', c == '\'', c == '(', c == ')':
			sb.WriteByte(c)
		case c == ' ':
			sb.WriteByte('+')
		default:
			sb.WriteByte('%')
			sb.WriteByte(hex[c>>4])
			sb.WriteByte(hex[c&0x0F])
		}
	}
	return sb.String()
}

// queryString builds the upstream GetQueryString: key raw, value encoded,
// pairs joined by separator (StringUtil.cs 155-158).
func queryString(pairs []queryPair, enc encoding.Encoding, separator string) string {
	if separator == "" {
		separator = "&"
	}
	parts := make([]string, 0, len(pairs))
	for _, p := range pairs {
		parts = append(parts, p.key+"="+netUrlEncode(p.value, enc))
	}
	return strings.Join(parts, separator)
}

// formBody builds the upstream WebClient FormUrlEncodedContentWithEncoding
// body: BOTH key and value encoded (WebClient.cs 242-262).
func formBody(pairs []queryPair, enc encoding.Encoding) string {
	parts := make([]string, 0, len(pairs))
	for _, p := range pairs {
		parts = append(parts, netUrlEncode(p.key, enc)+"="+netUrlEncode(p.value, enc))
	}
	return strings.Join(parts, "&")
}

func boolVar(b bool) any {
	if b {
		return "True"
	}
	return nil
}

func intPtrString(v *int) any {
	if v == nil {
		return nil
	}
	return strconv.Itoa(*v)
}

// queryVariables builds the ".Query.*"/".Keywords" template variable set from
// a Torznab query (upstream PerformQuery 1451-1507). The map is shared with
// parseSearchResponse: upstream pollutes it with ".Result.*" entries across
// paths and rows, and that behavior is preserved.
func (x *Cardigann) queryVariables(q *models.TorznabQuery) TemplateVars {
	variables := x.GetBaseTemplateVariables()

	variables[".Query.Type"] = q.QueryType
	variables[".Query.Q"] = q.SearchTerm
	variables[".Query.Series"] = nil
	variables[".Query.Ep"] = q.Episode
	variables[".Query.Season"] = nil
	if q.Season != nil && *q.Season > 0 {
		variables[".Query.Season"] = strconv.Itoa(*q.Season)
	}
	variables[".Query.Movie"] = nil
	variables[".Query.Year"] = intPtrString(q.Year)
	variables[".Query.Limit"] = strconv.Itoa(q.Limit)
	variables[".Query.Offset"] = strconv.Itoa(q.Offset)
	variables[".Query.Extended"] = strconv.Itoa(q.Extended)
	variables[".Query.Categories"] = q.Categories
	variables[".Query.APIKey"] = q.ApiKey
	variables[".Query.TVDBID"] = intPtrString(q.TvdbID)
	variables[".Query.TVRageID"] = intPtrString(q.RageID)
	variables[".Query.IMDBID"] = q.ImdbID
	variables[".Query.IMDBIDShort"] = q.ImdbIDShort()
	variables[".Query.TMDBID"] = intPtrString(q.TmdbID)
	variables[".Query.TVMazeID"] = intPtrString(q.TvmazeID)
	variables[".Query.TraktID"] = intPtrString(q.TraktID)
	variables[".Query.DoubanID"] = intPtrString(q.DoubanID)
	variables[".Query.Album"] = q.Album
	variables[".Query.Artist"] = q.Artist
	variables[".Query.Label"] = q.Label
	variables[".Query.Track"] = q.Track
	variables[".Query.Genre"] = q.Genre
	variables[".Query.Episode"] = q.GetEpisodeSearchString()
	variables[".Query.Author"] = q.Author
	variables[".Query.Title"] = q.Title
	variables[".Query.Publisher"] = q.Publisher
	// boolean queries
	variables[".Query.IsBookSearch"] = boolVar(q.IsBookSearch())
	variables[".Query.IsDoubanQuery"] = boolVar(q.IsDoubanQuery())
	variables[".Query.IsGenreQuery"] = boolVar(q.IsGenreQuery())
	variables[".Query.IsIdSearch"] = boolVar(q.IsIdSearch())
	variables[".Query.IsImdbQuery"] = boolVar(q.IsImdbQuery())
	variables[".Query.IsMovieSearch"] = boolVar(q.IsMovieSearch())
	variables[".Query.IsMusicSearch"] = boolVar(q.IsMusicSearch())
	variables[".Query.IsRssSearch"] = boolVar(q.IsRssSearch())
	variables[".Query.IsSearch"] = boolVar(q.IsSearch())
	variables[".Query.IsTVRageQuery"] = boolVar(q.IsTVRageQuery())
	variables[".Query.IsTVSearch"] = boolVar(q.IsTVSearch())
	variables[".Query.IsTmdbQuery"] = boolVar(q.IsTmdbQuery())
	variables[".Query.IsTraktQuery"] = boolVar(q.IsTraktQuery())
	variables[".Query.IsTvdbQuery"] = boolVar(q.IsTvdbQuery())
	variables[".Query.IsTvmazeQuery"] = boolVar(q.IsTvmazeQuery())

	var keywordTokens []string
	for _, key := range []string{"Q", "Series", "Movie", "Year"} {
		if v, ok := variables[".Query."+key].(string); ok && strings.TrimSpace(v) != "" {
			keywordTokens = append(keywordTokens, v)
		}
	}
	if ep, ok := variables[".Query.Episode"].(string); ok && strings.TrimSpace(ep) != "" {
		keywordTokens = append(keywordTokens, ep)
	}
	variables[".Query.Keywords"] = strings.Join(keywordTokens, " ")
	variables[".Keywords"] = x.applyFilters(variables[".Query.Keywords"].(string), x.Definition.Search.Keywordsfilters, variables)

	return variables
}

// searchRequests ports the per-path request generation of PerformQuery
// (CardigannIndexer.cs 1508-1542); variables comes from queryVariables and
// is shared with the response parser.
func (x *Cardigann) searchRequests(ctx context.Context, q *models.TorznabQuery, variables TemplateVars) ([]searchRequest, error) {
	_ = ctx

	search := x.Definition.Search

	mappedCategories := x.Caps.Categories.MapTorznabCapsToTrackers(q, false)
	if len(mappedCategories) == 0 {
		mappedCategories = x.DefaultCategories
	}

	enc := x.encoder()
	urlEncode := func(s string) string { return netUrlEncode(s, unicode.UTF8) }

	var searchURLs []string
	var requests []searchRequest

	for _, searchPath := range search.Paths {
		variables[".Categories"] = mappedCategories

		// skip path if categories don't match
		if len(searchPath.Categories) > 0 {
			hasIntersect := false
			intersect := []string{}
			for _, c := range mappedCategories {
				if stringInSlice(c, searchPath.Categories) {
					hasIntersect = true
					intersect = append(intersect, c)
				}
			}
			if searchPath.Categories[0] == "!" {
				hasIntersect = !hasIntersect
			}
			if !hasIntersect {
				variables[".Categories"] = exceptStrings(mappedCategories, searchPath.Categories)
				continue
			}
			variables[".Categories"] = intersect
		}

		// build search URL (UrlPathEncode only encodes spaces upstream, so
		// UrlEncode + "+ → %20" is used instead)
		searchURL, err := x.resolvePath(strings.ReplaceAll(ApplyTemplate(searchPath.Path, variables, urlEncode), "+", "%20"), nil)
		if err != nil {
			return nil, err
		}
		var queryCollection []queryPair
		method := http.MethodGet
		if strings.EqualFold(searchPath.Method, "post") {
			method = http.MethodPost
		}

		var inputsList []StringMap
		if searchPath.Inheritinputs {
			inputsList = append(inputsList, search.Inputs)
		}
		inputsList = append(inputsList, searchPath.Inputs)

		for _, inputs := range inputsList {
			for _, input := range inputs {
				if input.Key == "$raw" {
					rawStr := ApplyTemplate(input.Value, variables, urlEncode)
					for _, part := range strings.Split(rawStr, "&") {
						kv := strings.SplitN(part, "=", 2)
						key := kv[0]
						if key == "" {
							continue
						}
						value := ""
						if len(kv) == 2 {
							value = kv[1]
						}
						queryCollection = append(queryCollection, queryPair{key, value})
					}
				} else {
					inputValue := ApplyTemplate(input.Value, variables, nil)
					if strings.TrimSpace(inputValue) != "" || search.AllowEmptyInputs {
						queryCollection = append(queryCollection, queryPair{input.Key, inputValue})
					}
				}
			}
		}

		if method == http.MethodGet && len(queryCollection) > 0 {
			searchURL += "?" + queryString(queryCollection, enc, "&")
		}

		if method == http.MethodGet && stringInSlice(searchURL, searchURLs) {
			x.log().Debug(fmt.Sprintf("Skipping duplicated request for %s: %s", x.Definition.Name, searchURL))
			continue
		}

		searchURLs = append(searchURLs, searchURL)
		headers := x.parseCustomHeaders(search.Headers, variables)

		req := searchRequest{Method: method, URL: searchURL, Headers: headers, searchPath: searchPath}
		if method == http.MethodPost {
			// upstream WebClient form-urlencodes the pair list with the
			// definition encoding and sets the form content type
			req.RawBody = formBody(queryCollection, enc)
			if req.Headers == nil {
				req.Headers = map[string]string{}
			}
			req.Headers["Content-Type"] = "application/x-www-form-urlencoded"
		}
		requests = append(requests, req)
	}
	return requests, nil
}

func stringInSlice(v string, list []string) bool {
	for _, s := range list {
		if s == v {
			return true
		}
	}
	return false
}

func exceptStrings(all, exclude []string) []string {
	var out []string
	for _, s := range all {
		if !stringInSlice(s, exclude) {
			out = append(out, s)
		}
	}
	return out
}

// --- download request generation -------------------------------------------

// addTemplateVariablesFromUri ports AddTemplateVariablesFromUri
// (CardigannIndexer.cs 2009-2026). Port follows Go's empty Port; the scheme
// default (80/443) is substituted like .NET's Uri.Port.
func addTemplateVariablesFromURI(vars TemplateVars, u *url.URL, prefix string) {
	vars[prefix+".AbsoluteUri"] = u.String()
	vars[prefix+".AbsolutePath"] = u.Path
	vars[prefix+".Scheme"] = u.Scheme
	vars[prefix+".Host"] = u.Hostname()
	port := u.Port()
	if port == "" {
		switch u.Scheme {
		case "https":
			port = "443"
		case "http":
			port = "80"
		}
	}
	vars[prefix+".Port"] = port
	vars[prefix+".PathAndQuery"] = u.RequestURI()
	vars[prefix+".Query"] = ""
	if u.RawQuery != "" {
		vars[prefix+".Query"] = "?" + u.RawQuery
	}
	query, _ := url.ParseQuery(u.RawQuery)
	for key := range query {
		// supplied multiple times → take the first (upstream)
		vars[prefix+".Query."+key] = query.Get(key)
	}
}

// magnetTrackers mirrors MagnetUtil._Trackers (ngosang/trackerslist).
var magnetTrackers = []string{
	"udp://tracker.opentrackr.org:1337/announce",
	"udp://open.demonii.com:1337/announce",
	"udp://open.stealth.si:80/announce",
	"udp://tracker.torrent.eu.org:451/announce",
	"udp://vito-tracker.space:6969/announce",
	"udp://vito-tracker.duckdns.org:6969/announce",
	"udp://tracker.theoks.net:6969/announce",
	"udp://tracker.srv00.com:6969/announce",
	"udp://tracker.qu.ax:6969/announce",
	"udp://tracker.corpscorp.online:80/announce",
}

// infoHashToPublicMagnet ports MagnetUtil.InfoHashToPublicMagnet (28-33);
// empty input yields "" (upstream null → caller error).
func infoHashToPublicMagnet(infoHash, title string) string {
	if strings.TrimSpace(infoHash) == "" || strings.TrimSpace(title) == "" {
		return ""
	}
	parts := []string{
		"magnet:?xt=urn:btih:" + infoHash,
		"dn=" + netUrlEncode(title, unicode.UTF8),
	}
	for _, tr := range magnetTrackers {
		parts = append(parts, "tr="+netUrlEncode(tr, unicode.UTF8))
	}
	return strings.Join(parts, "&")
}

// handleRedirectableRequestAsync ports HandleRedirectableRequestAsync
// (2009-2023 area, 2053-2062): the Go client auto-follows (max 5); this only
// keeps following when a redirect chain does not settle.
func (x *Cardigann) handleRedirectableRequest(ctx context.Context, target string, headers map[string]string) (*httpclient.Result, error) {
	response, err := x.requestWithCookies(ctx, target, x.CookieHeader(), http.MethodGet, "", nil, "", headers)
	if err != nil {
		return nil, err
	}
	for i := 0; i < 5; i++ {
		if response.StatusCode < 300 || response.StatusCode >= 400 {
			break
		}
		location := response.Headers.Get("Location")
		if location == "" {
			break
		}
		response, err = x.requestWithCookies(ctx, location, x.CookieHeader(), http.MethodGet, "", nil, "", headers)
		if err != nil {
			return nil, err
		}
	}
	return response, nil
}

// handleRequestAsync ports HandleRequestAsync (CardigannIndexer.cs
// 1968-2007): templated path (no url-encoding), GET inputs become the query
// string with the request's queryseparator, POST inputs become the form body.
func (x *Cardigann) handleRequestAsync(ctx context.Context, req *RequestBlock, vars TemplateVars, referer string, headers map[string]string) (*httpclient.Result, error) {
	requestLinkStr, err := x.resolvePath(ApplyTemplate(req.Path, vars, nil), nil)
	if err != nil {
		return nil, err
	}

	var pairs []queryPair
	var queryCollection []queryPair
	method := http.MethodGet
	if strings.EqualFold(req.Method, "post") {
		method = http.MethodPost
	}

	for _, input := range req.Inputs {
		value := ApplyTemplate(input.Value, vars, nil)
		if method == http.MethodGet {
			queryCollection = append(queryCollection, queryPair{input.Key, value})
		} else {
			pairs = append(pairs, queryPair{input.Key, value})
		}
	}

	if len(queryCollection) > 0 {
		if !strings.Contains(requestLinkStr, "?") {
			requestLinkStr += "?"
		}
		requestLinkStr += queryString(queryCollection, x.encoder(), req.Queryseparator)
	}

	x.log().Debug(fmt.Sprintf("CardigannIndexer (%s): handleRequest() requestLinkStr= %s", x.ID(), requestLinkStr))

	var res *httpclient.Result
	if method == http.MethodPost {
		h := map[string]string{"Content-Type": "application/x-www-form-urlencoded"}
		for k, v := range headers {
			h[k] = v
		}
		res, err = x.requestWithCookies(ctx, requestLinkStr, x.CookieHeader(), method, referer, nil, formBody(pairs, x.encoder()), h)
	} else {
		res, err = x.requestWithCookies(ctx, requestLinkStr, x.CookieHeader(), method, referer, nil, "", headers)
	}
	if err != nil {
		return nil, err
	}
	x.log().Debug(fmt.Sprintf("CardigannIndexer (%s): handleRequest() remote server returned %d", x.ID(), res.StatusCode))
	return res, nil
}

// matchSelector ports MatchSelector (CardigannIndexer.cs 2028-2064): "" when
// the selector matches nothing, error when a requested attribute is absent.
func (x *Cardigann) matchSelector(res *httpclient.Result, sel *SelectorField, vars TemplateVars) (string, error) {
	selectorText := ApplyTemplate(sel.Selector, vars, nil)
	doc, err := parseHTML(string(res.Body))
	if err != nil {
		return "", err
	}
	element := querySelector(doc.Selection, selectorText)
	if element.Length() == 0 {
		x.log().Debug(fmt.Sprintf("CardigannIndexer (%s): Selector %s could not match any elements.", x.ID(), selectorText))
		return "", nil
	}

	var val string
	if sel.Attribute != "" {
		attr, ok := element.Attr(sel.Attribute)
		if !ok {
			return "", fmt.Errorf("Attribute %q is not set for element %s", sel.Attribute, goqueryOuterHTML(element))
		}
		val = attr
	} else {
		val = element.Text()
	}
	return x.applyFilters(val, sel.Filters, vars), nil
}

// Download ports CardigannIndexer.Download (2083-2146): definition-level
// before blocks, infohash magnets, download selectors and the test-link
// torrent check, with upstream error fall-through semantics preserved.
func (x *Cardigann) Download(ctx context.Context, link *url.URL) ([]byte, error) {
	method := http.MethodGet
	dl := x.Definition.Download
	var headers map[string]string

	if dl != nil {
		variables := x.GetBaseTemplateVariables()
		addTemplateVariablesFromURI(variables, link, ".DownloadUri")

		headers = x.parseCustomHeaders(firstNonNil(dl.Headers, x.Definition.Search.Headers), variables)

		var response *httpclient.Result
		var err error
		if dl.Before != nil {
			before := dl.Before
			if before.PathSelector != nil {
				response, err = x.handleRedirectableRequest(ctx, link.String(), headers)
				if err != nil {
					return nil, err
				}
				path, err := x.matchSelector(response, before.PathSelector, variables)
				if err != nil {
					return nil, err
				}
				before.RequestBlock.Path = path
			}
			response, err = x.handleRequestAsync(ctx, &before.RequestBlock, variables, link.String(), headers)
			if err != nil {
				return nil, err
			}
		}

		if dl.Method == "post" {
			method = http.MethodPost
		}

		if dl.Infohash != nil {
			data, done := x.downloadViaInfohash(ctx, link, method, headers, variables, dl, response)
			if done {
				return data, nil
			}
			// upstream: exception logged, falls through to the plain
			// base download with the original link
		} else if len(dl.Selectors) > 0 {
			return x.downloadViaSelectors(ctx, link, method, headers, variables, dl, response)
		}
	}
	headers = x.parseCustomHeaders(firstNonNil(dlHeaders(dl), x.Definition.Search.Headers), x.GetBaseTemplateVariables())
	return x.Base.DownloadWith(ctx, link, method, link.String(), headers)
}

func dlHeaders(dl *DownloadBlock) StringListMap {
	if dl == nil {
		return nil
	}
	return dl.Headers
}

// downloadViaInfohash mirrors the Infohash try/catch block; done is true only
// when the upstream code path returned (base download succeeded).
func (x *Cardigann) downloadViaInfohash(ctx context.Context, link *url.URL, method string, headers map[string]string, variables TemplateVars, dl *DownloadBlock, response *httpclient.Result) (data []byte, done bool) {
	fail := func(err error) ([]byte, bool) {
		x.log().Error(fmt.Sprintf("CardigannIndexer (%s): An exception occurred while trying Infohash block with hashSelector %s and titleSelector %s: %v",
			x.ID(), dl.Infohash.Hash.Selector, dl.Infohash.Title.Selector, err))
		return nil, false
	}

	headers = x.parseCustomHeaders(firstNonNil(dl.Headers, x.Definition.Search.Headers), variables)

	var err error
	if !dl.Infohash.Usebeforeresponse || dl.Before == nil || response == nil {
		response, err = x.handleRedirectableRequest(ctx, link.String(), headers)
		if err != nil {
			return fail(err)
		}
	}
	hash, err := x.matchSelector(response, dl.Infohash.Hash, variables)
	if err != nil {
		return fail(err)
	}
	if hash == "" {
		return fail(errors.New("InfoHash selectors didn't match hash."))
	}
	title, err := x.matchSelector(response, dl.Infohash.Title, variables)
	if err != nil {
		return fail(err)
	}
	if title == "" {
		return fail(errors.New("InfoHash selectors didn't match title."))
	}
	magnet := infoHashToPublicMagnet(hash, title)
	if magnet == "" {
		return fail(errors.New("InfoHashToPublicMagnet returned null"))
	}
	torrentLink, err := x.resolvePath(magnet, link)
	if err != nil {
		return fail(err)
	}
	// upstream passes no headers here (preserved)
	data, err = x.Base.DownloadWith(ctx, mustParse(torrentLink), method, torrentLink, nil)
	if err != nil {
		return fail(err)
	}
	return data, true
}

// downloadViaSelectors mirrors the Download.Selectors loop with per-selector
// try/catch (a failing base download moves on to the next selector).
func (x *Cardigann) downloadViaSelectors(ctx context.Context, link *url.URL, method string, headers map[string]string, variables TemplateVars, dl *DownloadBlock, response *httpclient.Result) ([]byte, error) {
	headers = x.parseCustomHeaders(firstNonNil(dl.Headers, x.Definition.Search.Headers), variables)

	var lastResponse *httpclient.Result
	for _, selector := range dl.Selectors {
		querySelector := ApplyTemplate(selector.Selector, variables, nil)
		fail := func(err error) {
			x.log().Error(fmt.Sprintf("CardigannIndexer (%s): An exception occurred while trying selector %s, retrying with next available selector: %v", x.ID(), querySelector, err))
		}

		var err error
		if !selector.Usebeforeresponse || dl.Before == nil || response == nil {
			response, err = x.handleRedirectableRequest(ctx, link.String(), headers)
			if err != nil {
				fail(err)
				continue
			}
		}
		lastResponse = response
		href, err := x.matchSelector(response, selector, variables)
		if err != nil {
			fail(err)
			continue
		}
		if href == "" {
			continue
		}
		torrentLink, err := x.resolvePath(href, link)
		if err != nil {
			fail(err)
			continue
		}
		if mustParse(torrentLink).Scheme != "magnet" && x.Definition.Testlinktorrent {
			response, err = x.handleRedirectableRequest(ctx, torrentLink, headers)
			if err != nil {
				fail(err)
				continue
			}
			if len(response.Body) >= 1 && response.Body[0] != 'd' {
				x.log().Debug(fmt.Sprintf("CardigannIndexer (%s): Download selector %s's torrent file is invalid, retrying with next available selector", x.ID(), querySelector))
				continue
			}
		}
		data, err := x.Base.DownloadWith(ctx, mustParse(torrentLink), method, torrentLink, headers)
		if err != nil {
			fail(err)
			continue
		}
		return data, nil
	}

	body := ""
	if lastResponse != nil {
		body = string(lastResponse.Body)
	}
	x.log().Error(fmt.Sprintf("CardigannIndexer (%s): Download selectors didn't match:\n%s", x.ID(), body))
	return nil, errors.New("Download selectors didn't match")
}
