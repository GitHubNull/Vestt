// mechanism: gazelle (cookie / username-password / API key)
//
// Ported from: Abstract/GazelleTracker.cs
//
// Gazelle is the shared base for Gazelle-API trackers (ajax.php?action=browse
// JSON API). Concrete indexers embed it, set identity/Caps via indexer.Base
// and bind a NewGazelleConfig. Hook fields mirror the upstream virtual
// methods; nil falls back to the upstream default behaviour.
//
// 澄清记录:
//   - upstream keeps cached `cookie`/`useTokens` fields refreshed in
//     LoadValuesFromJson; the Go port reads the config items directly, which
//     is observationally equivalent.
//   - upstream detects a login redirect via response.IsRedirect; the Go
//     http stack follows redirects transparently, so the re-login trigger is
//     "HTML body where a JSON response was expected" (a redirected browse
//     request lands on the login page), gated by the same conditions
//     (!useApiKey && no manually supplied cookie).
//   - upstream Download uses base.Download(uri, GET, headers) for the
//     token-flip retry; the Go port routes it through Base.DownloadWith
//     which adds the upstream ()' URL escaping.
package shared

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"vestt/common/httpclient"
	"vestt/common/indexer"
	"vestt/common/indexer/configdata"
	"vestt/common/models"
	"vestt/common/utils/dateutil"
	"vestt/common/utils/parseutil"
)

// DefaultAPIKeyLength mirrors upstream GazelleTracker.ApiKeyLength.
const DefaultAPIKeyLength = 41

// gazelleCookieHint is upstream ConfigurationDataGazelleTracker's 2FA cookie
// instructions (verbatim).
const gazelleCookieHint = `Use the Cookie field only if 2FA is enabled for your account, or the tracker requests your email at login; leave it empty otherwise.
<ol><li>Login to this tracker with your browser
<li>Open the <b>DevTools</b> panel by pressing <b>F12</b>
<li>Select the <b>Network</b> tab
<li>Click on the <b>Doc</b> button
<li>Refresh the page by pressing <b>F5</b>
<li>Select the <b>Headers</b> tab
<li>Find 'cookie:' in the <b>Request Headers</b> section
<li>Copy & paste the whole cookie string to here.</ol>`

// GazelleOptions mirrors the upstream GazelleTracker constructor flags; each
// flag controls which ConfigurationDataGazelleTracker items exist.
type GazelleOptions struct {
	SupportsFreeleechTokens bool
	SupportsFreeleechOnly   bool
	SupportsFreeloadOnly    bool
	ImdbInTags              bool
	Has2FA                  bool
	UseAPIKey               bool
	UsePassKey              bool
	UseAuthKey              bool
	InstructionMessage      string
}

// GazelleConfig ports upstream ConfigurationDataGazelleTracker
// (Models/IndexerConfig/Bespoke/ConfigurationDataGazelleTracker.cs). Items
// stay nil when the corresponding option is disabled, exactly like the
// upstream conditional construction.
type GazelleConfig struct {
	configdata.ConfigData
	Username      *configdata.StringItem
	Password      *configdata.PasswordItem
	APIKey        *configdata.StringItem
	Passkey       *configdata.StringItem
	Authkey       *configdata.StringItem
	CookieHint    *configdata.InfoItem
	Cookie        *configdata.StringItem
	UseToken      *configdata.BoolItem
	FreeleechOnly *configdata.BoolItem
	FreeloadOnly  *configdata.BoolItem
	Instructions  *configdata.InfoItem
}

// NewGazelleConfig mirrors the upstream constructor: items are created in
// declaration order and only when the matching flag is set.
func NewGazelleConfig(opts GazelleOptions) *GazelleConfig {
	c := &GazelleConfig{ConfigData: *configdata.NewConfigData()}
	if opts.UseAPIKey {
		c.APIKey = configdata.NewStringItem("APIKey")
	} else {
		c.Username = configdata.NewStringItem("Username")
		c.Password = configdata.NewPasswordItem("Password")
	}
	if opts.Has2FA {
		c.CookieHint = configdata.NewInfoItem("CookieHint", gazelleCookieHint)
		c.Cookie = configdata.NewStringItem("Cookie")
	}
	if opts.UsePassKey {
		c.Passkey = configdata.NewStringItem("Passkey")
	}
	if opts.UseAuthKey {
		c.Authkey = configdata.NewStringItem("Authkey")
	}
	if opts.SupportsFreeleechTokens {
		c.UseToken = configdata.NewBoolItem("Use Freeleech Tokens when Available")
	}
	if opts.SupportsFreeleechOnly {
		c.FreeleechOnly = configdata.NewBoolItem("Search freeleech only")
	}
	if opts.SupportsFreeloadOnly {
		c.FreeloadOnly = configdata.NewBoolItem("Search freeload only")
	}
	c.Instructions = configdata.NewInfoItem("", opts.InstructionMessage)
	return c
}

// Gazelle ports upstream GazelleTracker. Embed it, then set the Base
// identity fields, Caps and Bind a config created with NewGazelleConfig
// using the same options.
type Gazelle struct {
	indexer.Base

	// Config is the typed configuration; also wired into Base via Bind.
	Config *GazelleConfig

	// ImdbInTags / UseAPIKey / UsePassKey / UseAuthKey mirror the upstream
	// constructor flags (they drive request/parse behaviour).
	ImdbInTags bool
	UseAPIKey  bool
	UsePassKey bool
	UseAuthKey bool

	// APIKeyLength / APIKeyLengthLegacy mirror the virtual ApiKeyLength
	// properties (0 → 41 / disabled).
	APIKeyLength       int
	APIKeyLengthLegacy int

	// LoginURL/APIURL/PosterURL default to SiteLink+"login.php",
	// SiteLink+"ajax.php" and SiteLink; set to override (upstream virtuals).
	LoginURL  string
	APIURL    string
	PosterURL string

	// AuthorizationName/AuthorizationFormat default to "Authorization"/
	// "{0}" (upstream virtuals).
	AuthorizationName   string
	AuthorizationFormat string

	// Hooks mirror upstream virtual methods; nil = default behaviour.
	GetSearchTerm           func(q *models.TorznabQuery) string
	GetInfoURL              func(torrentID int, groupID *int) string
	GetDownloadURL          func(torrentID int, canUseToken bool) string
	ReleaseInfoPostParse    func(release *models.ReleaseInfo, torrent, result map[string]any) bool
	ShouldSkipRelease       func(torrent map[string]any) bool
	FlipOptionalTokenString func(requestLink string) string
}

// Bind wires the typed config into Base (Base.Config must see the same
// pointer for configdata.Find/ItemsJSON to work).
func (g *Gazelle) Bind(c *GazelleConfig) {
	g.Config = c
	g.Base.Config = c
}

func (g *Gazelle) loginURL() string {
	if g.LoginURL != "" {
		return g.LoginURL
	}
	return g.SiteLinkValue + "login.php"
}

func (g *Gazelle) apiURL() string {
	if g.APIURL != "" {
		return g.APIURL
	}
	return g.SiteLinkValue + "ajax.php"
}

func (g *Gazelle) posterURL() string {
	if g.PosterURL != "" {
		return g.PosterURL
	}
	return g.SiteLinkValue
}

func (g *Gazelle) authorizationName() string {
	if g.AuthorizationName != "" {
		return g.AuthorizationName
	}
	return "Authorization"
}

func (g *Gazelle) authorizationFormat() string {
	if g.AuthorizationFormat != "" {
		return g.AuthorizationFormat
	}
	return "{0}"
}

func (g *Gazelle) apiKeyLength() int {
	if g.APIKeyLength > 0 {
		return g.APIKeyLength
	}
	return DefaultAPIKeyLength
}

// cookieValue is the manually supplied cookie (upstream `cookie` field).
func (g *Gazelle) cookieValue() string {
	if g.Config != nil && g.Config.Cookie != nil {
		return g.Config.Cookie.Value
	}
	return ""
}

// useTokens is upstream `useTokens` (supportsFreeleechTokens config flag &&
// the UseToken item).
func (g *Gazelle) useTokens() bool {
	return g.Config != nil && g.Config.UseToken != nil && g.Config.UseToken.Value
}

// freeleechOnly / freeloadOnly read the optional bool items.
func (g *Gazelle) freeleechOnly() bool {
	return g.Config != nil && g.Config.FreeleechOnly != nil && g.Config.FreeleechOnly.Value
}

func (g *Gazelle) freeloadOnly() bool {
	return g.Config != nil && g.Config.FreeloadOnly != nil && g.Config.FreeloadOnly.Value
}

// authHeaders mirrors the Authorization header construction (null headers
// when no API key is configured).
func (g *Gazelle) authHeaders() map[string]string {
	if g.Config == nil || g.Config.APIKey == nil {
		return nil
	}
	return map[string]string{
		g.authorizationName(): strings.Replace(g.authorizationFormat(), "{0}", g.Config.APIKey.Value, 1),
	}
}

// --- search URL ------------------------------------------------------------

// getSearchTerm mirrors the upstream virtual GetSearchTerm.
func (g *Gazelle) getSearchTerm(q *models.TorznabQuery) string {
	if g.GetSearchTerm != nil {
		return g.GetSearchTerm(q)
	}
	return q.GetQueryString()
}

// SearchURL builds the browse API URL exactly like upstream PerformQuery
// (including the trailing GetQueryString().Replace(".", " ")).
func (g *Gazelle) SearchURL(query *models.TorznabQuery) string {
	searchString := g.getSearchTerm(query)

	var qc QueryParams
	qc.Add("action", "browse")
	// group_results=0 omitted upstream: results won't include all information
	qc.Add("order_by", "time")
	qc.Add("order_way", "desc")

	if strings.TrimSpace(query.Genre) != "" {
		qc.Set("taglist", query.Genre)
	}

	if strings.TrimSpace(query.ImdbID) != "" {
		if g.ImdbInTags {
			qc.Set("taglist", query.ImdbID)
		} else {
			qc.Set("cataloguenumber", query.ImdbID)
		}
	} else if searchString != "" {
		qc.Set("searchstr", searchString)
	}

	if strings.TrimSpace(query.Artist) != "" && query.Artist != "VA" {
		qc.Set("artistname", query.Artist)
	}

	if strings.TrimSpace(query.Label) != "" {
		qc.Set("recordlabel", query.Label)
	}

	if query.Year != nil {
		qc.Set("year", fmt.Sprintf("%d", *query.Year))
	}

	if strings.TrimSpace(query.Album) != "" {
		qc.Set("groupname", query.Album)
	}

	for _, cat := range g.Caps.Categories.MapTorznabCapsToTrackers(query, false) {
		qc.Set("filter_cat["+cat+"]", "1")
	}

	if g.freeleechOnly() {
		qc.Set("freetorrent", "1")
	} else if g.freeloadOnly() {
		qc.Set("freetorrent", "4")
	}

	// remove . as not used in titles
	return g.apiURL() + "?" + strings.ReplaceAll(qc.GetQueryString(), ".", " ")
}

// --- configuration ---------------------------------------------------------

// ApplyConfiguration mirrors upstream GazelleTracker.ApplyConfiguration:
// API-key and cookie paths self-test and complete immediately; the
// username/password path posts the login form and returns
// RequiresTesting (upstream IndexerConfigurationStatus.RequiresTesting).
func (g *Gazelle) ApplyConfiguration(ctx context.Context, items []indexer.ConfigItem) (indexer.ConfigStatus, error) {
	if err := applyPostedItems(&g.Base, items); err != nil {
		return indexer.ConfigCompleted, err
	}

	if g.UseAPIKey {
		if g.Config == nil || g.Config.APIKey == nil {
			return indexer.ConfigCompleted, errors.New("Invalid API Key configured") //nolint:staticcheck // upstream GazelleTracker.cs error text, kept 1:1
		}
		apiKey := g.Config.APIKey.Value
		length := g.apiKeyLength()
		if g.APIKeyLengthLegacy == 0 && len(apiKey) != length {
			return indexer.ConfigCompleted, fmt.Errorf("Invalid API Key configured: expected length: %d, got %d", length, len(apiKey)) //nolint:staticcheck // upstream GazelleTracker.cs error text, kept 1:1
		}
		if g.APIKeyLengthLegacy != 0 && len(apiKey) != length && len(apiKey) != g.APIKeyLengthLegacy {
			return indexer.ConfigCompleted, fmt.Errorf("Invalid API Key configured: expected length: %d or %d, got %d", length, g.APIKeyLengthLegacy, len(apiKey)) //nolint:staticcheck // upstream GazelleTracker.cs error text, kept 1:1
		}

		releases, err := g.PerformQuery(ctx, models.NewTorznabQuery())
		if err != nil || len(releases) == 0 {
			g.SetConfigured(false)
			if err == nil {
				err = errors.New("Found 0 results in the tracker") //nolint:staticcheck // upstream GazelleTracker.cs error text, kept 1:1
			}
			return indexer.ConfigCompleted, fmt.Errorf("Your API Key did not work: %v", err) //nolint:staticcheck // upstream GazelleTracker.cs error text, kept 1:1
		}
		g.SetConfigured(true)
		g.SaveConfig(ctx)
		return indexer.ConfigCompleted, nil
	}

	if cookie := g.cookieValue(); strings.TrimSpace(cookie) != "" {
		// Cookie was manually supplied
		g.SetCookieHeader(cookie)
		releases, err := g.PerformQuery(ctx, models.NewTorznabQuery())
		if err != nil || len(releases) == 0 {
			g.SetConfigured(false)
			if err == nil {
				err = errors.New("Found 0 results in the tracker") //nolint:staticcheck // upstream GazelleTracker.cs error text, kept 1:1
			}
			return indexer.ConfigCompleted, fmt.Errorf("Your cookie did not work: %v", err) //nolint:staticcheck // upstream GazelleTracker.cs error text, kept 1:1
		}
		g.SetConfigured(true)
		g.SaveConfig(ctx)
		return indexer.ConfigCompleted, nil
	}

	if err := g.login(ctx); err != nil {
		return indexer.ConfigCompleted, err
	}
	return indexer.ConfigRequiresTesting, nil
}

// login mirrors the upstream username/password login branch of
// ApplyConfiguration (RequestLoginAndFollowRedirect + login-form error
// extraction). On success the response cookies become the CookieHeader and
// the config is saved, like upstream ConfigureIfOK.
func (g *Gazelle) login(ctx context.Context) error {
	if g.Config.Username == nil || g.Config.Password == nil {
		return errors.New("Username/password configuration is required") //nolint:staticcheck // port-authored guard message, kept stable
	}
	pairs := url.Values{}
	pairs.Set("username", g.Config.Username.Value)
	pairs.Set("password", g.Config.Password.Value)
	pairs.Set("keeplogged", "1")

	res, err := g.Client.Do(ctx, g.ID(), &httpclient.Request{
		Method:  http.MethodPost,
		URL:     g.loginURL(),
		Body:    pairs,
		Referer: g.SiteLinkValue,
		Cookies: g.CookieHeader(),
	})
	if err != nil {
		return err
	}
	content := string(res.Body)

	if strings.Contains(content, "logout.php") {
		// ConfigureIfOK(response.Cookies, true, ...)
		//
		// 澄清记录: upstream reads cookies from the first (redirected) login
		// response; Vestt's http stack follows redirects transparently and
		// only exposes the final response plus the per-indexer jar, so the
		// capture is the union of both (same approach as the Cardigann
		// engine's storeResponseCookies).
		cookies := setCookieHeaderValue(res)
		if site, err := url.Parse(g.SiteLinkValue); err == nil {
			cookies = mergeCookieHeaders(cookies, g.Client.CookieHeader(g.ID(), site))
		}
		g.SetCookieHeader(cookies)
		g.SetConfigured(true)
		g.SaveConfig(ctx)
		return nil
	}

	if msg, ok := loginFormError(content); ok {
		return errors.New(msg)
	}
	logError(&g.Base, "Unexpected response during login:\n"+content)
	return errors.New("Unexpected response during login, see log for HTML response.") //nolint:staticcheck // upstream GazelleTracker.cs error text, kept 1:1
}

// setCookieHeaderValue renders a response's Set-Cookie headers in
// "name=value; name2=value2" Cookie-header form (upstream WebResult.Cookies).
func setCookieHeaderValue(res *httpclient.Result) string {
	var parts []string
	for _, line := range res.Headers.Values("Set-Cookie") {
		if i := strings.IndexByte(line, ';'); i >= 0 {
			line = line[:i]
		}
		if line != "" {
			parts = append(parts, line)
		}
	}
	return strings.Join(parts, "; ")
}

// mergeCookieHeaders unions two Cookie-header strings (first occurrence
// wins), mirroring upstream ResolveCookies accumulation.
func mergeCookieHeaders(a, b string) string {
	seen := map[string]bool{}
	var parts []string
	for _, s := range append(strings.Split(a, "; "), strings.Split(b, "; ")...) {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		key := s
		if i := strings.IndexByte(s, '='); i >= 0 {
			key = s[:i]
		}
		if seen[key] {
			continue
		}
		seen[key] = true
		parts = append(parts, s)
	}
	return strings.Join(parts, "; ")
}

// loginFormError extracts the upstream login error message: the element with
// id="loginform", its first <table> removed, rendered as text with "\n\t"
// replaced by spaces.
func loginFormError(content string) (string, bool) {
	const marker = `id="loginform"`
	idx := strings.Index(content, marker)
	if idx < 0 {
		return "", false
	}
	start := strings.LastIndex(content[:idx], "<")
	if start < 0 {
		return "", false
	}
	// tag name
	rest := content[start+1:]
	name := rest
	if i := strings.IndexAny(name, " \t\r\n>"); i >= 0 {
		name = name[:i]
	}
	inner, ok := elementInnerHTML(content, start, strings.ToLower(name))
	if !ok {
		return "", false
	}
	// loginform.QuerySelector("table").Remove() — first table only
	inner = removeFirstElement(inner, "table")
	text := strings.ReplaceAll(stripTags(inner), "\n\t", " ")
	return strings.TrimSpace(text), true
}

// elementInnerHTML returns the inner HTML of the element whose open tag
// starts at openStart in content (case-insensitive tag matching, nesting
// aware). ok is false when no matching close tag exists.
func elementInnerHTML(content string, openStart int, tag string) (string, bool) {
	lowerContent := strings.ToLower(content)
	lowerTag := strings.ToLower(tag)
	openEnd := strings.IndexByte(content[openStart:], '>')
	if openEnd < 0 {
		return "", false
	}
	openEnd += openStart
	if strings.HasSuffix(strings.TrimRight(content[openStart:openEnd+1], " \t\r\n"), "/>") {
		// self-closed: no inner content
		return "", true
	}
	depth := 1
	pos := openEnd + 1
	for pos < len(content) {
		openIdx := indexTagOpen(lowerContent, lowerTag, pos)
		closeIdx := strings.Index(lowerContent[pos:], "</"+lowerTag+">")
		if closeIdx < 0 {
			return "", false
		}
		closeIdx += pos
		if openIdx >= 0 && openIdx < closeIdx {
			depth++
			pos = openIdx + 1
			continue
		}
		depth--
		if depth == 0 {
			return content[openEnd+1 : closeIdx], true
		}
		pos = closeIdx + len(lowerTag) + 3
	}
	return "", false
}

// indexTagOpen finds the next "<tag" open tag at a word boundary (no match
// inside "<tags" or "</tag"); returns -1 when absent.
func indexTagOpen(lowerContent, lowerTag string, from int) int {
	for {
		i := strings.Index(lowerContent[from:], "<"+lowerTag)
		if i < 0 {
			return -1
		}
		i += from
		if rest := i + 1 + len(lowerTag); rest < len(lowerContent) {
			switch lowerContent[rest] {
			case '>', '/', ' ', '\t', '\r', '\n':
				return i
			}
		}
		from = i + 1
	}
}

// removeFirstElement strips the first occurrence of element `tag` (with its
// inner HTML) from the fragment.
func removeFirstElement(fragment, tag string) string {
	lower := strings.ToLower(fragment)
	idx := indexTagOpen(lower, strings.ToLower(tag), 0)
	if idx < 0 {
		return fragment
	}
	inner, ok := elementInnerHTML(fragment, idx, tag)
	if !ok {
		return fragment
	}
	openEnd := strings.IndexByte(fragment[idx:], '>')
	openEnd += idx
	// self-closed element: drop just the open tag
	if strings.HasSuffix(strings.TrimRight(fragment[idx:openEnd+1], " \t\r\n"), "/>") {
		return fragment[:idx] + fragment[openEnd+1:]
	}
	closeTag := "</" + tag + ">"
	closeIdx := openEnd + 1 + len(inner)
	if !strings.HasPrefix(fragment[closeIdx:], closeTag) {
		return fragment
	}
	return fragment[:idx] + fragment[closeIdx+len(closeTag):]
}

// --- query handling --------------------------------------------------------

// ResultsForQuery implements indexer.Indexer.
func (g *Gazelle) ResultsForQuery(ctx context.Context, q *models.TorznabQuery, isMeta bool) (*indexer.IndexerResult, error) {
	return g.ResultsForQueryWith(ctx, q, isMeta, g.PerformQuery)
}

// Test implements indexer.Indexer (upstream TestIndexer).
func (g *Gazelle) Test(ctx context.Context) error {
	return g.Base.Test(ctx, g.PerformQuery)
}

// PerformQuery mirrors upstream GazelleTracker.PerformQuery.
func (g *Gazelle) PerformQuery(ctx context.Context, query *models.TorznabQuery) ([]*models.ReleaseInfo, error) {
	searchURL := g.SearchURL(query)
	headers := g.authHeaders()

	res, err := g.Client.Do(ctx, g.ID(), &httpclient.Request{
		URL:     searchURL,
		Headers: headers,
		Cookies: g.CookieHeader(),
	})
	if err != nil {
		return nil, err
	}
	body := res.Body

	// we get a redirect in html pages and an error message in json response (api)
	if !g.UseAPIKey && strings.TrimSpace(g.cookieValue()) == "" && looksLikeHTML(body) {
		// re-login only if API key is not in use
		if err := g.login(ctx); err != nil {
			return nil, err
		}
		res, err = g.Client.Do(ctx, g.ID(), &httpclient.Request{
			URL:     searchURL,
			Headers: headers,
			Cookies: g.CookieHeader(),
		})
		if err != nil {
			return nil, err
		}
		body = res.Body
	}

	if strings.Contains(string(body), "failure") && g.UseAPIKey {
		// reason for failure should be explained
		if jsonError, err := decodeObject(body); err == nil {
			if reason, ok := jsonError["error"]; ok && strings.TrimSpace(jStr(reason)) != "" {
				return nil, fmt.Errorf("Unexpected response from indexer request: %q", jStr(reason)) //nolint:staticcheck // upstream GazelleTracker.cs error text, kept 1:1
			}
		}
	}

	if res.StatusCode >= 400 {
		return nil, fmt.Errorf("Invalid status code %d (%s) received from indexer", res.StatusCode, StatusName(res.StatusCode)) //nolint:staticcheck // upstream GazelleTracker.cs error text, kept 1:1
	}

	releases, err := g.parseBrowse(body)
	if err != nil {
		return nil, parseFailure(&g.Base, string(body), err)
	}

	// OrderByDescending(o => o.PublishDate)
	sort.SliceStable(releases, func(i, j int) bool {
		return releases[i].PublishDate.After(releases[j].PublishDate)
	})

	if query.IsRssSearch() && len(releases) > 50 {
		releases = releases[:50]
	}

	return releases, nil
}

// looksLikeHTML approximates the upstream IsRedirect check: a browsed API
// request that got redirected to the login page returns HTML, not JSON.
func looksLikeHTML(body []byte) bool {
	trimmed := strings.TrimSpace(string(body))
	return len(trimmed) > 0 && trimmed[0] == '<'
}

// parseBrowse parses the ajax.php browse JSON response (the upstream
// JObject.Parse loop in PerformQuery).
func (g *Gazelle) parseBrowse(body []byte) ([]*models.ReleaseInfo, error) {
	jsonResponse, err := decodeObject(body)
	if err != nil {
		return nil, err
	}
	response, ok := jsonResponse["response"].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("missing response object")
	}
	results, ok := response["results"].([]any)
	if !ok {
		return nil, fmt.Errorf("missing results array")
	}

	var releases []*models.ReleaseInfo
	for _, rawResult := range results {
		r, ok := rawResult.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("result is not an object")
		}

		// groupTime may be a unixTime or a datetime string
		var groupTime time.Time
		groupTimeStr := jStr(r["groupTime"])
		if n, errIsNumber := jInt(r["groupTime"]); errIsNumber && groupTimeStr != "" && isAllDigits(groupTimeStr) {
			groupTime = dateutil.UnixTimestampToDateTime(n)
		} else if groupTimeStr != "" {
			groupTime, _ = dateutil.FromFuzzyTime(groupTimeStr, "")
		}

		groupName := jStr(r["groupName"])
		artist := jStr(r["artist"])
		cover := jStr(r["cover"])
		var tags []string
		if rawTags, ok := r["tags"].([]any); ok {
			for _, t := range rawTags {
				tags = append(tags, jStr(t))
			}
		}
		groupYear := jStr(r["groupYear"])
		releaseType := jStr(r["releaseType"])

		var title strings.Builder
		if artist != "" {
			title.WriteString(artist + " - ")
		}
		title.WriteString(groupName)
		if groupYear != "" && groupYear != "0" {
			title.WriteString(" [" + groupYear + "]")
		}
		if releaseType != "" && releaseType != "Unknown" {
			title.WriteString(" [" + releaseType + "]")
		}

		description := ""
		genre := ""
		if len(tags) > 0 && tags[0] != "" {
			description = "Tags: " + strings.Join(tags, ", ") + "\n"
			genre = strings.Join(tags, ",")
		}

		var poster *url.URL
		if cover != "" {
			if strings.HasPrefix(cover, "http") {
				poster, _ = url.Parse(cover)
			} else {
				poster, _ = url.Parse(g.posterURL() + cover)
			}
		}

		release := &models.ReleaseInfo{
			PublishDate: groupTime,
			Title:       title.String(),
			Description: description,
			Poster:      poster,
		}

		if genre != "" {
			release.Genres = unionStrings(release.Genres, strings.Split(genre, ","))
		}

		if g.ImdbInTags {
			var imdbIDs []int64
			for _, tag := range tags {
				if id, ok := parseutil.GetImdbID(tag); ok {
					imdbIDs = append(imdbIDs, id)
				}
			}
			if len(imdbIDs) == 1 {
				imdb := imdbIDs[0]
				release.Imdb = &imdb
			}
		}

		if torrents, ok := r["torrents"].([]any); ok {
			for _, rawTorrent := range torrents {
				torrent, ok := rawTorrent.(map[string]any)
				if !ok {
					return nil, fmt.Errorf("torrent is not an object")
				}
				if g.shouldSkipRelease(torrent) {
					continue
				}
				release2 := release.Clone()
				if err := g.fillReleaseInfoFromJSON(release2, torrent); err != nil {
					return nil, err
				}
				if g.releaseInfoPostParse(release2, torrent, r) {
					releases = append(releases, release2)
				}
			}
		} else {
			if g.shouldSkipRelease(r) {
				continue
			}
			if err := g.fillReleaseInfoFromJSON(release, r); err != nil {
				return nil, err
			}
			if g.releaseInfoPostParse(release, r, r) {
				releases = append(releases, release)
			}
		}
	}
	return releases, nil
}

func isAllDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

func unionStrings(a, b []string) []string {
	out := append([]string(nil), a...)
	seen := map[string]bool{}
	for _, s := range out {
		seen[s] = true
	}
	for _, s := range b {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

// releaseInfoPostParse mirrors the upstream virtual (hook; return false to
// exclude the torrent from the results).
func (g *Gazelle) releaseInfoPostParse(release *models.ReleaseInfo, torrent, result map[string]any) bool {
	if g.ReleaseInfoPostParse != nil {
		return g.ReleaseInfoPostParse(release, torrent, result)
	}
	return true
}

// shouldSkipRelease mirrors the upstream virtual: skip non-freeleech results
// when freeleech-only is set.
func (g *Gazelle) shouldSkipRelease(torrent map[string]any) bool {
	if g.ShouldSkipRelease != nil {
		return g.ShouldSkipRelease(torrent)
	}
	isFreeleech, ok := jBoolLoose(torrent["isFreeleech"])
	return g.freeleechOnly() && (!ok || !isFreeleech)
}

// fillReleaseInfoFromJSON ports upstream FillReleaseInfoFromJson.
func (g *Gazelle) fillReleaseInfoFromJSON(release *models.ReleaseInfo, torrent map[string]any) error {
	torrentID, ok := jInt(torrent["torrentId"])
	if !ok {
		return fmt.Errorf("missing torrentId")
	}

	if t := jStr(torrent["time"]); t != "" {
		// upstream: DateTime.ParseExact(time + " +0000", "yyyy-MM-dd HH:mm:ss zzz")
		if parsed, err := time.ParseInLocation("2006-01-02 15:04:05 -0700", t+" +0000", time.Local); err == nil {
			release.PublishDate = parsed
		}
	}

	var flags []string
	if format := jStr(torrent["format"]); format != "" {
		flags = append(flags, format)
	}
	if encoding := jStr(torrent["encoding"]); encoding != "" {
		flags = append(flags, encoding)
	}
	if hasLog, ok := jBoolLoose(torrent["hasLog"]); ok && hasLog {
		flags = append(flags, "Log ("+jStr(torrent["logScore"])+"%)")
	}
	if hasCue, ok := jBoolLoose(torrent["hasCue"]); ok && hasCue {
		flags = append(flags, "Cue")
	}
	// tehconnection.me specific?
	if lang := jStr(torrent["lang"]); lang != "" && lang != "---" {
		flags = append(flags, lang)
	}
	if media := jStr(torrent["media"]); media != "" {
		flags = append(flags, media)
	}
	// tehconnection.me specific?
	if resolution := jStr(torrent["resolution"]); resolution != "" {
		flags = append(flags, resolution)
	}
	if container := jStr(torrent["container"]); container != "" {
		flags = append(flags, container)
	}
	if codec := jStr(torrent["codec"]); codec != "" {
		flags = append(flags, codec)
	}
	if audio := jStr(torrent["audio"]); audio != "" {
		flags = append(flags, audio)
	}
	if subbing := jStr(torrent["subbing"]); subbing != "" && subbing != "---" {
		flags = append(flags, subbing)
	}
	if remastered, ok := jBoolLoose(torrent["remastered"]); ok && remastered {
		remasterYear := jStr(torrent["remasterYear"])
		if remasterYear == "0" {
			remasterYear = ""
		}
		remasteredStr := strings.TrimSpace(remasterYear + " " + jStr(torrent["remasterTitle"]))
		if remasteredStr != "" {
			flags = append(flags, remasteredStr)
		}
	}

	if len(flags) > 0 {
		release.Title += " " + strings.Join(flags, " / ")
	}

	if size, ok := jInt(torrent["size"]); ok {
		release.Size = &size
	}
	seeders, _ := jInt(torrent["seeders"])
	leechers, _ := jInt(torrent["leechers"])
	release.Seeders = &seeders
	peers := leechers + seeders
	release.Peers = &peers

	details := g.getInfoURL(int(torrentID), nil)
	detailsURL, err := url.Parse(details)
	if err != nil {
		return err
	}
	release.Details = detailsURL
	release.Guid = release.Details
	downloadURL, err := url.Parse(g.getDownloadURL(int(torrentID), release.DownloadVolumeFactor == nil || *release.DownloadVolumeFactor != 0))
	if err != nil {
		return err
	}
	release.Link = downloadURL

	category := jStr(torrent["category"])
	if category == "" || strings.Contains(category, "Select Category") {
		release.Category = g.Caps.Categories.MapTrackerCatToNewznab("1")
	} else {
		release.Category = g.Caps.Categories.MapTrackerCatDescToNewznab(category)
	}

	files, _ := jInt(torrent["fileCount"])
	release.Files = &files
	grabs, _ := jInt(torrent["snatches"])
	release.Grabs = &grabs

	dvf := 1.0
	uvf := 1.0
	release.DownloadVolumeFactor = &dvf
	release.UploadVolumeFactor = &uvf
	if isFreeleech, ok := jBoolLoose(torrent["isFreeleech"]); ok && isFreeleech {
		release.DownloadVolumeFactor = &zeroFloat
	}

	if isPersonalFreeleech, ok := jBoolLoose(torrent["isPersonalFreeleech"]); ok && isPersonalFreeleech {
		release.DownloadVolumeFactor = &zeroFloat
	}

	isFreeload, _ := jBoolLoose(torrent["isFreeload"])
	if isNeutralLeech, ok := jBoolLoose(torrent["isNeutralLeech"]); (ok && isNeutralLeech) || isFreeload {
		release.DownloadVolumeFactor = &zeroFloat
		release.UploadVolumeFactor = &zeroFloat
	}
	return nil
}

var zeroFloat = 0.0

// getInfoURL mirrors the upstream virtual GetInfoUrl.
func (g *Gazelle) getInfoURL(torrentID int, groupID *int) string {
	if g.GetInfoURL != nil {
		return g.GetInfoURL(torrentID, groupID)
	}
	s := fmt.Sprintf("%storrents.php?torrentid=%d", g.SiteLinkValue, torrentID)
	if groupID != nil && *groupID > 0 {
		s += fmt.Sprintf("&id=%d", *groupID)
	}
	return s
}

// getDownloadURL mirrors the upstream virtual GetDownloadUrl.
func (g *Gazelle) getDownloadURL(torrentID int, canUseToken bool) string {
	if g.GetDownloadURL != nil {
		return g.GetDownloadURL(torrentID, canUseToken)
	}
	var b strings.Builder
	b.WriteString(g.SiteLinkValue)
	b.WriteString("torrents.php?action=download")
	if g.useTokens() && canUseToken {
		b.WriteString("&usetoken=1")
	}
	if g.UsePassKey && g.Config.Passkey != nil {
		b.WriteString("&torrent_pass=" + g.Config.Passkey.Value)
	}
	if g.UseAuthKey && g.Config.Authkey != nil {
		b.WriteString("&authkey=" + g.Config.Authkey.Value)
	}
	fmt.Fprintf(&b, "&id=%d", torrentID)
	return b.String()
}

// Download mirrors upstream GazelleTracker.Download: fetch the torrent with
// the API-key headers; when a freeleech-token download failed with a known
// HTML error, retry without "&usetoken=1".
func (g *Gazelle) Download(ctx context.Context, link *url.URL) ([]byte, error) {
	headers := g.authHeaders()
	res, err := g.Client.Do(ctx, g.ID(), &httpclient.Request{
		Method:  http.MethodGet,
		URL:     link.String(),
		Headers: headers,
		Cookies: g.CookieHeader(),
	})
	if err != nil {
		return nil, err
	}
	content := res.Body

	// Check if we're out of FL tokens/torrent is to large
	requestLink := link.String()
	if len(content) >= 1 && content[0] != 'd' && strings.Contains(requestLink, "usetoken=1") {
		htmlText := string(content)
		if strings.Contains(htmlText, "You do not have any freeleech tokens left.") ||
			strings.Contains(htmlText, "You do not have enough freeleech tokens") ||
			strings.Contains(htmlText, "This torrent is too large.") ||
			strings.Contains(htmlText, "You cannot use tokens here") {
			// download again without usetoken=1
			requestLinkNew := g.flipOptionalTokenString(requestLink)
			newURL, err := url.Parse(requestLinkNew)
			if err != nil {
				return nil, err
			}
			return g.DownloadWith(ctx, newURL, http.MethodGet, "", headers)
		}
	}
	return content, nil
}

// flipOptionalTokenString mirrors the upstream virtual.
func (g *Gazelle) flipOptionalTokenString(requestLink string) string {
	if g.FlipOptionalTokenString != nil {
		return g.FlipOptionalTokenString(requestLink)
	}
	return strings.ReplaceAll(requestLink, "&usetoken=1", "")
}
