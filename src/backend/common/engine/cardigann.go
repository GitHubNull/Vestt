// Cardigann YAML-definition engine indexer (SPEC-05).
//
// Ported from: Jackett.Common/Indexers/Definitions/CardigannIndexer.cs
// (configuration form, login, caps; request generation in requests.go).
package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"

	"vestt/common/httpclient"
	"vestt/common/indexer"
	"vestt/common/indexer/configdata"
	"vestt/common/models"
)

// Cardigann is one YAML-driven indexer instance.
type Cardigann struct {
	indexer.Base
	Definition *IndexerDefinition

	// DefaultCategories collects categorymappings entries flagged default
	// (upstream DefaultCategories).
	DefaultCategories []string

	config        *cardigannConfig
	landingResult *httpclient.Result
	landingDoc    *goquery.Document
	logger        *slog.Logger
}

// cardigannConfig is the whole configuration for a cardigann indexer: only
// the base ConfigData plus dynamics (definition settings are dynamic items).
type cardigannConfig struct {
	configdata.ConfigData
}

const (
	cookieInfoText = "<ol><li>Login to this tracker with your browser</li><li>If present in the login page, ensure you have the <b>Remember me</b> ticked and the <b>Log Me Out if IP Changes</b> unticked when you login</li><li>Navigate to the web site's torrent search page to view the list of available torrents for download</li><li>Open the <b>DevTools</b> panel by pressing <b>F12</b></li><li>Select the <b>Network</b> tab</li><li>Click on the <b>Doc</b> button (Chrome Browser) or <b>HTML</b> button (FireFox)</li><li>Refresh the page by pressing <b>F5</b></li><li>Click on the first row entry</li><li>Select the <b>Headers</b> tab on the Right panel</li><li>Find <b>'cookie:'</b> in the <b>Request Headers</b> section</li><li><b>Select</b> and <b>Copy</b> the whole cookie string <i>(everything after 'cookie: ')</i> and <b>Paste</b> here.</li></ol>"
	userAgentInfo  = "<ol><li>From the same place you fetched the cookie,</li><li>Find <b>'user-agent:'</b> in the <b>Request Headers</b> section</li><li><b>Select</b> and <b>Copy</b> the whole user-agent string <i>(everything after 'user-agent: ')</i> and <b>Paste</b> here.</li></ol>"
)

// NewCardigann mirrors the CardigannIndexer constructor
// (CardigannIndexer.cs 75-238).
func NewCardigann(def *IndexerDefinition, logger *slog.Logger) (*Cardigann, error) {
	if logger == nil {
		logger = slog.Default()
	}

	// default settings: username/password
	if def.Settings == nil {
		def.Settings = []*SettingsField{
			{Name: "username", Label: "Username", Type: "text"},
			{Name: "password", Label: "Password", Type: "password"},
		}
	}
	if def.Encoding == "" {
		def.Encoding = "utf-8"
	}
	if def.Login != nil && def.Login.Method == "" {
		def.Login.Method = "form"
	}
	if def.Search.Paths == nil {
		def.Search.Paths = []*SearchPathBlock{}
	}
	// convert a single search Path into a Paths entry
	if def.Search.Path != "" {
		def.Search.Paths = append(def.Search.Paths, &SearchPathBlock{
			RequestBlock:  RequestBlock{Path: def.Search.Path},
			Inheritinputs: true,
		})
	}
	if len(def.Links) == 0 {
		return nil, fmt.Errorf("cardigann definition %s has no links", def.ID)
	}

	x := &Cardigann{Definition: def, logger: logger}
	cfg := &cardigannConfig{ConfigData: *configdata.NewConfigData()}
	x.config = cfg

	defaultSiteLink := def.Links[0]
	if len(def.Links) > 1 {
		x.AltSiteLinks = append([]string(nil), def.Links[1:]...)
	}
	if !strings.HasSuffix(defaultSiteLink, "/") {
		defaultSiteLink += "/"
	}

	caps := models.NewCapabilities()
	modes := make([]models.SearchMode, 0, len(def.Caps.Modes))
	for _, kv := range def.Caps.Modes {
		modes = append(modes, models.SearchMode{Key: kv.Key, Params: kv.Values})
	}
	if err := caps.ParseCardigannSearchModes(modes); err != nil {
		return nil, fmt.Errorf("cardigann definition %s: %v", def.ID, err)
	}
	caps.SupportsRawSearch = def.Caps.Allowrawsearch
	caps.TVSearchImdbAvailable = def.Caps.Allowtvsearchimdb

	// caps.categories
	for _, kv := range def.Caps.Categories {
		cat := models.GetCatByName(kv.Value)
		if cat == nil {
			logger.Error(fmt.Sprintf("CardigannIndexer (%s): invalid Torznab category for id %s: %s", def.ID, kv.Key, kv.Value))
			continue
		}
		caps.Categories.AddCategoryMapping(kv.Key, cat, "")
	}
	// caps.categorymappings
	for _, cm := range def.Caps.Categorymappings {
		var torznabCat *models.Category
		if cm.Cat != "" {
			torznabCat = models.GetCatByName(cm.Cat)
			if torznabCat == nil {
				logger.Error(fmt.Sprintf("CardigannIndexer (%s): invalid Torznab category for id %s: %s", def.ID, cm.ID, cm.Cat))
				continue
			}
		}
		caps.Categories.AddCategoryMapping(cm.ID, torznabCat, cm.Desc)
		if cm.Default {
			x.DefaultCategories = append(x.DefaultCategories, cm.ID)
		}
	}

	x.Base = indexer.Base{
		IDValue:          def.ID,
		NameValue:        def.Name,
		DescriptionValue: def.Description,
		DefaultSiteLink:  defaultSiteLink,
		AltSiteLinks:     x.AltSiteLinks,
		LegacySiteLinks:  def.Legacylinks,
		LangValue:        def.Language,
		TypeValue:        def.Type,
		EncodingName:     def.Encoding,
		Caps:             caps,
		Config:           cfg,
		Logger:           logger,
	}
	x.SiteLinkValue = defaultSiteLink

	// definition settings → dynamic config items (in definition order)
	for _, setting := range def.Settings {
		item, err := x.settingItem(setting)
		if err != nil {
			return nil, fmt.Errorf("cardigann definition %s: %v", def.ID, err)
		}
		x.config.AddDynamic(setting.Name, item)
	}
	return x, nil
}

// settingItem maps one definition setting to its config item
// (CardigannIndexer.cs 138-201).
func (x *Cardigann) settingItem(setting *SettingsField) (configdata.Item, error) {
	itemName := setting.Label
	if itemName == "" {
		itemName = setting.Name
	}

	switch setting.Type {
	case "":
		it := configdata.NewStringItem(itemName)
		it.Value = setting.Default
		return it, nil
	case "checkbox":
		it := configdata.NewBoolItem(itemName)
		it.Value = setting.Default == "true"
		return it, nil
	case "password", "text":
		it := configdata.NewStringItem(itemName)
		it.Value = setting.Default
		return it, nil
	case "multi-select":
		if setting.Options == nil {
			return nil, errors.New("Options must be given for the 'multi-select' type")
		}
		it := configdata.NewMultiSelectItem(itemName, toOptions(setting.Options))
		it.Values = append([]string(nil), setting.Defaults...)
		return it, nil
	case "select":
		if setting.Options == nil {
			return nil, errors.New("Options must be given for the 'select' type")
		}
		it := configdata.NewSelectItem(itemName, toOptions(setting.Options))
		it.Value = setting.Default
		return it, nil
	case "info":
		return configdata.NewInfoItem(itemName, setting.Default), nil
	case "info_category_8000":
		return configdata.NewInfoItem(
			fmt.Sprintf("About %s Categories", x.Definition.Name),
			fmt.Sprintf("%s does not return categories in its search results.</br>To add to your Apps' Torznab indexer, replace all categories with 8000(Other).", x.Definition.Name)), nil
	case "info_cookie":
		return configdata.NewInfoItem("How to get the Cookie", cookieInfoText), nil
	case "info_flaresolverr":
		return configdata.NewInfoItem("FlareSolverr", `This site may use Cloudflare DDoS Protection, therefore Jackett requires <a href="https://github.com/Jackett/Jackett#configuring-flaresolverr" target="_blank">FlareSolverr</a> to access it.`), nil
	case "info_useragent":
		return configdata.NewInfoItem("How to get the User-Agent", userAgentInfo), nil
	default:
		return nil, fmt.Errorf("Invalid setting type '%s' specified", setting.Type)
	}
}

func toOptions(m StringMap) []configdata.Option {
	out := make([]configdata.Option, 0, len(m))
	for _, kv := range m {
		out = append(out, configdata.Option{Value: kv.Key, Label: kv.Value})
	}
	return out
}

func (x *Cardigann) log() *slog.Logger { return x.logger }

// --- template variables --------------------------------------------------

// GetBaseTemplateVariables ports CardigannIndexer.GetBaseTemplateVariables
// (252-321).
func (x *Cardigann) GetBaseTemplateVariables() TemplateVars {
	now := time.Now()
	year := now.Year()
	if now.Month() > 1 {
		// upstream quirk: DateTime.Today.Month > 1 ? Year : Year-1
	} else {
		year--
	}
	vars := TemplateVars{
		".Config.sitelink": x.SiteLink(),
		".True":            "True",
		".False":           nil,
		".Today.Year":      strconv.Itoa(year),
	}
	cd := x.Base.ConfigData()
	for _, setting := range x.Definition.Settings {
		if cd == nil {
			break
		}
		item := cd.GetDynamic(setting.Name)
		if item == nil {
			continue
		}
		key := ".Config." + setting.Name
		switch it := item.(type) {
		case *configdata.BoolItem:
			if it.Value {
				vars[key] = vars[".True"]
			} else {
				vars[key] = vars[".False"]
			}
		case *configdata.StringItem:
			vars[key] = it.Value
		case *configdata.PasswordItem:
			vars[key] = it.Value
		case *configdata.SelectItem:
			vars[key] = it.Value
		case *configdata.MultiSelectItem:
			vars[key] = it.Values
		case *configdata.ImageItem:
			vars[key] = it.Value
		case *configdata.InfoItem:
			vars[key] = it.Value
		case *configdata.HiddenItem:
			vars[key] = it.Value
		}
	}
	return vars
}

// --- request helpers ------------------------------------------------------

func (x *Cardigann) siteURL() *url.URL {
	u, err := url.Parse(x.SiteLink())
	if err != nil {
		return &url.URL{}
	}
	return u
}

// resolvePath ports CardigannIndexer.resolvePath: joins a (possibly
// relative/absolute) path against the site link.
func (x *Cardigann) resolvePath(path string, base *url.URL) (string, error) {
	if strings.HasPrefix(path, "magnet:") {
		return path, nil
	}
	if base == nil {
		base = x.siteURL()
	}
	ref, err := url.Parse(path)
	if err != nil {
		return "", err
	}
	return base.ResolveReference(ref).String(), nil
}

// parseCustomHeaders ports CardigannIndexer.ParseCustomHeaders: each header
// value list contributes its first entry, template-expanded.
func (x *Cardigann) parseCustomHeaders(customHeaders StringListMap, vars TemplateVars) map[string]string {
	headers := map[string]string{}
	for _, kv := range customHeaders {
		if len(kv.Values) == 0 {
			continue
		}
		headers[kv.Key] = ApplyTemplate(kv.Values[0], vars, nil)
	}
	return headers
}

// requestWithCookies performs one request through the client stack,
// overriding the cookie jar with the given raw Cookie header when non-empty
// (upstream RequestWithCookiesAsync semantics).
func (x *Cardigann) requestWithCookies(ctx context.Context, target, cookies, method, referer string, body url.Values, rawBody string, headers map[string]string) (*httpclient.Result, error) {
	if x.Client == nil {
		return nil, errors.New("http client not configured")
	}
	req := &httpclient.Request{
		Method:  method,
		URL:     target,
		Cookies: cookies,
		Headers: headers,
		Body:    body,
		RawBody: rawBody,
		Referer: referer,
	}
	return x.Client.Do(ctx, x.ID(), req)
}

// storeResponseCookies mirrors "configData.CookieHeader.Value =
// loginResult.Cookies": the full jar state for the site URL after a login
// response (upstream WebResult.Cookies).
func (x *Cardigann) storeResponseCookies(u *url.URL) {
	if x.Client == nil {
		return
	}
	x.SetCookieHeader(x.Client.CookieHeader(x.ID(), u))
}

// --- configuration ---------------------------------------------------------

// ConfigurationForSetup implements indexer.Indexer: for form-login
// definitions the landing page is fetched (capturing captcha) before
// rendering the config form. Upstream GetConfigurationForSetup swallows
// errors and returns the plain configData.
func (x *Cardigann) ConfigurationForSetup(ctx context.Context) ([]map[string]any, error) {
	if _, err := x.getConfigurationForSetup(ctx, false, ""); err != nil {
		x.log().Error(fmt.Sprintf("Exception in GetConfigurationForSetup (%s): %v", x.ID(), err))
	}
	return configdata.ItemsJSON(x.config)
}

// getConfigurationForSetup ports GetConfigurationForSetup(automaticLogin,
// cookies) (CardigannIndexer.cs 1013-1100); gotCaptcha reports the upstream
// "return null" path.
func (x *Cardigann) getConfigurationForSetup(ctx context.Context, automaticLogin bool, cookies string) (gotCaptcha bool, err error) {
	login := x.Definition.Login
	if login == nil || login.Method != "form" {
		return false, nil
	}

	vars := x.GetBaseTemplateVariables()
	headers := x.parseCustomHeaders(firstNonNil(login.Headers, x.Definition.Search.Headers), vars)

	loginURL, err := x.resolvePath(ApplyTemplate(login.Path, vars, nil), nil)
	if err != nil {
		return false, err
	}

	x.SetCookieHeader("")
	if login.Cookies != nil {
		x.SetCookieHeader(strings.Join(login.Cookies, "; "))
	}

	landing, err := x.requestWithCookies(ctx, loginURL, cookies, http.MethodGet, x.SiteLink(), nil, "", headers)
	if err != nil {
		return false, err
	}
	x.landingResult = landing

	doc, derr := goquery.NewDocumentFromReader(strings.NewReader(string(landing.Body)))
	if derr != nil {
		return false, derr
	}
	x.landingDoc = doc

	hasCaptcha := false
	if login.Captcha != nil {
		captcha := login.Captcha
		switch captcha.Type {
		case "image":
			captchaElement := doc.Find(captcha.Selector).First()
			if captchaElement.Length() > 0 {
				hasCaptcha = true
				src, _ := captchaElement.Attr("src")
				captchaURL, err := x.resolvePath(src, mustParse(loginURL))
				if err != nil {
					return false, err
				}
				imageData, err := x.requestWithCookies(ctx, captchaURL, landingCookies(landing), http.MethodGet, loginURL, nil, "", headers)
				if err != nil {
					return false, err
				}
				captchaImage := configdata.NewImageItem("Captcha Image")
				captchaImage.Value = imageData.Body
				x.config.AddDynamic("CaptchaImage", captchaImage)
				x.config.AddDynamic("CaptchaText", configdata.NewStringItem("Captcha Text"))
			} else {
				x.log().Debug(fmt.Sprintf("CardigannIndexer (%s): No captcha image found", x.ID()))
			}
		case "text":
			captchaElement := doc.Find(captcha.Selector).First()
			if captchaElement.Length() > 0 {
				hasCaptcha = true
				x.config.AddDynamic("CaptchaChallenge", configdata.NewInfoItem("Captcha Challenge", captchaElement.Text()))
				x.config.AddDynamic("CaptchaAnswer", configdata.NewStringItem("Captcha Answer"))
			} else {
				x.log().Debug(fmt.Sprintf("CardigannIndexer (%s): No captcha image found", x.ID()))
			}
		default:
			return false, fmt.Errorf("Captcha type %q is not implemented", captcha.Type)
		}
	}

	if hasCaptcha && automaticLogin {
		x.setLastError("Got captcha during automatic login, please reconfigure manually")
		x.log().Error(fmt.Sprintf("CardigannIndexer (%s): Found captcha during automatic login, aborting", x.ID()))
		x.landingDoc = nil
		return true, nil
	}
	return false, nil
}

func (x *Cardigann) setLastError(msg string) {
	if cd := x.Base.ConfigData(); cd != nil && cd.LastError != nil {
		cd.LastError.Value = msg
	}
}

func mustParse(raw string) *url.URL {
	u, err := url.Parse(raw)
	if err != nil {
		return &url.URL{}
	}
	return u
}

func landingCookies(res *httpclient.Result) string {
	if res == nil {
		return ""
	}
	var parts []string
	for _, sc := range res.Headers.Values("Set-Cookie") {
		if c, err := http.ParseSetCookie(sc); err == nil {
			parts = append(parts, c.Name+"="+c.Value)
		}
	}
	return strings.Join(parts, "; ")
}

func firstNonNil(a, b StringListMap) StringListMap {
	if len(a) > 0 {
		return a
	}
	return b
}

// ApplyConfiguration ports CardigannIndexer.ApplyConfiguration
// (CardigannIndexer.cs 1102-1112).
func (x *Cardigann) ApplyConfiguration(ctx context.Context, items []indexer.ConfigItem) (indexer.ConfigStatus, error) {
	if err := x.ApplyItems(items); err != nil {
		return indexer.ConfigCompleted, err
	}
	// upstream LoadValuesFromJson: apply site-link normalization after values
	if err := x.LoadValuesFromJSON(nil, false); err != nil {
		return indexer.ConfigCompleted, err
	}
	// the upstream return value (captcha-during-auto-login) is discarded by
	// ApplyConfiguration — replicate faithfully
	if _, err := x.doLogin(ctx, ""); err != nil {
		return indexer.ConfigCompleted, err
	}
	if _, err := x.testLogin(ctx); err != nil {
		return indexer.ConfigCompleted, err
	}
	x.SetConfigured(true)
	x.SaveConfig(ctx)
	return indexer.ConfigCompleted, nil
}

// --- login -----------------------------------------------------------------

// checkForError ports CardigannIndexer.checkForError (538-562).
func (x *Cardigann) checkForError(res *httpclient.Result, errorBlocks []ErrorBlock) error {
	if res.StatusCode == http.StatusUnauthorized { // e.g. YGGtorrent
		return errors.New("401 Unauthorized, check your credentials")
	}
	if errorBlocks == nil {
		return nil
	}
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(string(res.Body)))
	if err != nil {
		return err
	}
	for _, eb := range errorBlocks {
		selection := querySelector(doc.Selection, eb.Selector)
		if selection.Length() == 0 {
			continue
		}
		errorMessage := selection.Text()
		if eb.Message != nil {
			msg, herr := x.handleSelector(eb.Message, doc.Selection, x.GetBaseTemplateVariables(), true)
			if herr != nil {
				return herr
			}
			errorMessage = msg
		}
		return fmt.Errorf("Error: %s", strings.TrimSpace(errorMessage))
	}
	return nil
}

// doLogin ports CardigannIndexer.DoLogin (564-852). The bool reports the
// upstream "got captcha during automatic login" return value.
func (x *Cardigann) doLogin(ctx context.Context, cookies string) (bool, error) {
	login := x.Definition.Login
	if login == nil {
		return false, nil
	}

	vars := x.GetBaseTemplateVariables()
	headers := x.parseCustomHeaders(firstNonNil(login.Headers, x.Definition.Search.Headers), vars)

	switch login.Method {
	case "post":
		pairs := url.Values{}
		if len(login.Inputs) > 0 {
			for _, input := range login.Inputs {
				pairs.Add(input.Key, ApplyTemplate(input.Value, vars, nil))
			}
		}
		loginURL, err := x.resolvePath(ApplyTemplate(login.Path, vars, nil), nil)
		if err != nil {
			return false, err
		}
		x.SetCookieHeader("")
		if login.Cookies != nil {
			x.SetCookieHeader(strings.Join(login.Cookies, "; "))
		}
		loginResult, err := x.requestWithCookies(ctx, loginURL, x.CookieHeader(), http.MethodPost, x.SiteLink(), pairs, "", headers)
		if err != nil {
			return false, err
		}
		x.storeResponseCookies(mustParse(loginURL))
		if err := x.checkForError(loginResult, login.Error); err != nil {
			return false, err
		}

	case "form":
		loginURL, err := x.resolvePath(ApplyTemplate(login.Path, vars, nil), nil)
		if err != nil {
			return false, err
		}
		queryCollection := url.Values{}
		pairs := url.Values{}

		formSelector := login.Form
		if formSelector == "" {
			formSelector = "form"
		}

		// landingResultDocument might not be initiated if the login is caused
		// by a re-login during a query
		if x.landingDoc == nil {
			gotCaptcha, err := x.getConfigurationForSetup(ctx, true, cookies)
			if err != nil {
				return false, err
			}
			if gotCaptcha {
				return false, nil
			}
		}

		form := x.landingDoc.Find(formSelector).First()
		if form.Length() == 0 {
			return false, fmt.Errorf("Login failed: No form found on %s using form selector %s", loginURL, formSelector)
		}

		inputs := form.Find("input")
		if inputs.Length() == 0 {
			return false, fmt.Errorf("Login failed: No inputs found on %s using form selector %s", loginURL, formSelector)
		}

		submitURLstr, _ := form.Attr("action")
		if login.Submitpath != "" {
			submitURLstr = login.Submitpath
		}

		var pairsOrder []string
		inputs.Each(func(_ int, s *goquery.Selection) {
			name, ok := s.Attr("name")
			if !ok || name == "" || isDisabled(s) {
				return
			}
			inputType, _ := s.Attr("type")
			if (strings.EqualFold(inputType, "checkbox") || strings.EqualFold(inputType, "radio")) && !isChecked(s) {
				return
			}
			value, _ := s.Attr("value")
			if !valueExists(pairsOrder, name) {
				pairsOrder = append(pairsOrder, name)
			}
			pairs.Set(name, value)
		})

		if len(login.Inputs) > 0 {
			for _, input := range login.Inputs {
				value := ApplyTemplate(input.Value, vars, nil)
				inputName := input.Key
				if login.Selectors {
					inputElement := x.landingDoc.Find(input.Key).First()
					if inputElement.Length() == 0 {
						return false, fmt.Errorf("Login failed: No input found using selector %s", input.Key)
					}
					inputName, _ = inputElement.Attr("name")
				}
				if !valueExists(pairsOrder, inputName) {
					pairsOrder = append(pairsOrder, inputName)
				}
				pairs.Set(inputName, value)
			}
		}

		// selector inputs
		if len(login.SelectorInputs) > 0 {
			for _, kv := range login.SelectorInputs {
				value, err := x.handleSelector(kv.Value, x.landingDoc.Selection, vars, !kv.Value.Optional)
				if err != nil {
					return false, fmt.Errorf("Error while parsing selector input=%s, selector=%s: %v", kv.Key, kv.Value.Selector, err)
				}
				if kv.Value.Optional && value == "" {
					continue
				}
				if !valueExists(pairsOrder, kv.Key) {
					pairsOrder = append(pairsOrder, kv.Key)
				}
				pairs.Set(kv.Key, value)
			}
		}

		// getselector inputs
		if len(login.GetSelectorInputs) > 0 {
			for _, kv := range login.GetSelectorInputs {
				value, err := x.handleSelector(kv.Value, x.landingDoc.Selection, vars, !kv.Value.Optional)
				if err != nil {
					return false, fmt.Errorf("Error while parsing get selector input=%s, selector=%s: %v", kv.Key, kv.Value.Selector, err)
				}
				if kv.Value.Optional && value == "" {
					continue
				}
				queryCollection.Set(kv.Key, value)
			}
		}
		if len(queryCollection) > 0 {
			sep := "?"
			if strings.Contains(submitURLstr, "?") {
				sep = "&"
			}
			submitURLstr += sep + queryCollection.Encode()
		}
		submitURL, err := x.resolvePath(submitURLstr, mustParse(loginURL))
		if err != nil {
			return false, err
		}

		// automatically solve simpleCaptchas, if used
		if x.landingDoc.Find(`script[src*="simpleCaptcha"]`).Length() > 0 {
			captchaURL, err := x.resolvePath("simpleCaptcha.php?numImages=1", nil)
			if err != nil {
				return false, err
			}
			simpleCaptchaResult, err := x.requestWithCookies(ctx, captchaURL, x.CookieHeader(), http.MethodGet, loginURL, nil, "", headers)
			if err != nil {
				return false, err
			}
			var parsed struct {
				Images []struct {
					Hash string `json:"hash"`
				} `json:"images"`
			}
			if err := json.Unmarshal(simpleCaptchaResult.Body, &parsed); err == nil && len(parsed.Images) > 0 {
				pairs.Set("captchaSelection", parsed.Images[0].Hash)
				pairs.Set("submitme", "X")
			}
		}

		if login.Captcha != nil {
			captcha := login.Captcha
			if captcha.Type == "image" {
				if item := x.config.GetDynamic("CaptchaText"); item != nil {
					input := captcha.Input
					if login.Selectors {
						inputElement := x.landingDoc.Find(captcha.Input).First()
						if inputElement.Length() == 0 {
							return false, fmt.Errorf("Login failed: No captcha input found using %s", captcha.Input)
						}
						input, _ = inputElement.Attr("name")
					}
					if si, ok := item.(*configdata.StringItem); ok {
						pairs.Set(input, si.Value)
					}
				}
			}
			if captcha.Type == "text" {
				if item := x.config.GetDynamic("CaptchaAnswer"); item != nil {
					input := captcha.Input
					if login.Selectors {
						inputElement := x.landingDoc.Find(captcha.Input).First()
						if inputElement.Length() == 0 {
							return false, fmt.Errorf("Login failed: No captcha input found using %s", captcha.Input)
						}
						input, _ = inputElement.Attr("name")
					}
					if si, ok := item.(*configdata.StringItem); ok {
						pairs.Set(input, si.Value)
					}
				}
			}
		}

		// clear landing results (avoid stale re-login state)
		x.landingResult = nil
		x.landingDoc = nil

		var loginResult *httpclient.Result
		enctype, _ := form.Attr("enctype")
		if enctype == "multipart/form-data" {
			boundary := "---------------------------" + strconv.FormatInt(time.Now().Unix(), 10)
			var bodyParts []string
			for _, name := range pairsOrder {
				bodyParts = append(bodyParts,
					"--"+boundary+"\r\n"+
						"Content-Disposition: form-data; name=\""+name+"\"\r\n"+
						"\r\n"+
						pairs.Get(name))
			}
			bodyParts = append(bodyParts, "--"+boundary+"--")
			if headers == nil {
				headers = map[string]string{}
			}
			headers["Content-Type"] = "multipart/form-data; boundary=" + boundary
			loginResult, err = x.requestWithCookies(ctx, submitURL, x.CookieHeader(), http.MethodPost, x.SiteLink(), nil, strings.Join(bodyParts, "\r\n"), headers)
		} else {
			loginResult, err = x.requestWithCookies(ctx, submitURL, x.CookieHeader(), http.MethodPost, loginURL, pairs, "", headers)
		}
		if err != nil {
			return false, err
		}
		x.storeResponseCookies(mustParse(loginURL))
		if err := x.checkForError(loginResult, login.Error); err != nil {
			return false, err
		}

	case "cookie":
		if item := x.config.GetDynamic("cookie"); item != nil {
			if si, ok := item.(*configdata.StringItem); ok {
				x.SetCookieHeader(si.Value)
			}
		}

	case "get":
		queryCollection := url.Values{}
		if len(login.Inputs) > 0 {
			for _, input := range login.Inputs {
				queryCollection.Add(input.Key, ApplyTemplate(input.Value, vars, nil))
			}
		}
		loginURL, err := x.resolvePath(ApplyTemplate(login.Path, vars, nil)+"?"+queryCollection.Encode(), nil)
		if err != nil {
			return false, err
		}
		x.SetCookieHeader("")
		loginResult, err := x.requestWithCookies(ctx, loginURL, "", http.MethodGet, x.SiteLink(), nil, "", headers)
		if err != nil {
			return false, err
		}
		x.storeResponseCookies(mustParse(loginURL))
		if err := x.checkForError(loginResult, login.Error); err != nil {
			return false, err
		}

	case "oneurl":
		oneURL := ""
		if v, ok := login.Inputs.Get("oneurl"); ok {
			oneURL = ApplyTemplate(v, vars, nil)
		}
		loginURL, err := x.resolvePath(ApplyTemplate(login.Path, vars, nil)+oneURL, nil)
		if err != nil {
			return false, err
		}
		x.SetCookieHeader("")
		loginResult, err := x.requestWithCookies(ctx, loginURL, "", http.MethodGet, x.SiteLink(), nil, "", headers)
		if err != nil {
			return false, err
		}
		x.storeResponseCookies(mustParse(loginURL))
		if err := x.checkForError(loginResult, login.Error); err != nil {
			return false, err
		}

	default:
		return false, fmt.Errorf("Login method %s not implemented", login.Method)
	}

	x.log().Debug(fmt.Sprintf("CardigannIndexer (%s): Cookies after login: %s", x.ID(), x.CookieHeader()))
	return false, nil
}

// isDisabled mirrors AngleSharp IElement.IsDisabled for input elements
// (disabled attribute or a disabled fieldset ancestor).
func isDisabled(s *goquery.Selection) bool {
	if s.AttrOr("disabled", "") != "" || s.Is("[disabled]") {
		return true
	}
	return s.ParentsFiltered("fieldset[disabled]").Length() > 0
}

func isChecked(s *goquery.Selection) bool { return s.Is(":checked") }

func valueExists(list []string, v string) bool {
	for _, s := range list {
		if s == v {
			return true
		}
	}
	return false
}

// testLogin ports CardigannIndexer.TestLogin (892-949). The bool mirrors the
// upstream "was a test performed" return value.
func (x *Cardigann) testLogin(ctx context.Context) (bool, error) {
	login := x.Definition.Login
	if login == nil || login.Test == nil {
		return false, nil
	}

	loginTestURL, err := x.resolvePath(login.Test.Path, nil)
	if err != nil {
		return false, err
	}
	vars := x.GetBaseTemplateVariables()
	headers := x.parseCustomHeaders(firstNonNil(login.Headers, x.Definition.Search.Headers), vars)
	testResult, err := x.requestWithCookies(ctx, loginTestURL, "", http.MethodGet, "", nil, "", headers)
	if err != nil {
		return false, err
	}

	// The Go http.Client follows redirects automatically (max 5), which
	// subsumes the upstream follow-once-then-error flow; the cross-domain
	// hint below only fires when the redirect chain does not settle.
	if testResult.StatusCode >= 300 && testResult.StatusCode < 400 {
		location := testResult.Headers.Get("Location")
		errMsg := fmt.Sprintf("Login Failed, got redirected to: %s", location)
		if hint := x.redirectDomainHint(loginTestURL, location); hint != "" {
			errMsg += " Try changing the indexer URL to " + hint + "."
			if x.Definition.Followredirect {
				if cd := x.Base.ConfigData(); cd != nil && cd.SiteLink != nil {
					cd.SiteLink.Value = hint
					x.SiteLinkValue = hint
					x.SaveConfig(ctx)
				}
				errMsg += " Updated site link, please try again."
			}
		}
		return true, errors.New(errMsg)
	}

	if login.Test.Selector != "" {
		doc, err := goquery.NewDocumentFromReader(strings.NewReader(string(testResult.Body)))
		if err != nil {
			return true, err
		}
		if querySelectorAll(doc.Selection, login.Test.Selector).Length() == 0 {
			return true, fmt.Errorf("Login failed: Selector %q didn't match", login.Test.Selector)
		}
	}
	return true, nil
}

// redirectDomainHint ports GetRedirectDomainHint (854-870).
func (x *Cardigann) redirectDomainHint(requestURL, redirectURL string) string {
	if strings.TrimSpace(redirectURL) == "" || !strings.HasPrefix(requestURL, x.SiteLink()) {
		return ""
	}
	redirectURI, err := url.Parse(redirectURL)
	if err != nil {
		return ""
	}
	siteURI := x.siteURL()
	if !strings.EqualFold(redirectURI.Host, siteURI.Host) {
		return redirectURI.Scheme + "://" + redirectURI.Host + "/"
	}
	return ""
}

// --- query handling ---------------------------------------------------------

// ResultsForQuery implements indexer.Indexer. Response parsing lands in
// SPEC-06; until then the request generation pipeline returns an empty set.
func (x *Cardigann) ResultsForQuery(ctx context.Context, q *models.TorznabQuery, isMeta bool) (*indexer.IndexerResult, error) {
	return x.Base.ResultsForQueryWith(ctx, q, isMeta, func(ctx context.Context, q *models.TorznabQuery) ([]*models.ReleaseInfo, error) {
		return x.performQuery(ctx, q)
	})
}

// performQuery executes the search paths and parses the responses. The
// template variable map is shared between request generation and response
// parsing, mirroring upstream PerformQuery.
func (x *Cardigann) performQuery(ctx context.Context, q *models.TorznabQuery) ([]*models.ReleaseInfo, error) {
	variables := x.queryVariables(q)
	requests, err := x.searchRequests(ctx, q, variables)
	if err != nil {
		return nil, err
	}
	var releases []*models.ReleaseInfo
	for _, req := range requests {
		// upstream PerformQuery passes no referer for search requests
		res, err := x.requestWithCookies(ctx, req.URL, x.CookieHeader(), req.Method, "", nil, req.RawBody, req.Headers)
		if err != nil {
			return nil, err
		}
		parsed, err := x.parseSearchResponse(ctx, res, req, variables, q)
		if err != nil {
			return nil, err
		}
		releases = append(releases, parsed...)
	}
	return releases, nil
}

// Test implements indexer.Indexer (upstream TestIndexer).
func (x *Cardigann) Test(ctx context.Context) error {
	return x.Base.Test(ctx, x.performQuery)
}

// Download is defined in requests.go (definition-level download handling).

// compile-time interface check
var _ indexer.Indexer = (*Cardigann)(nil)

// silence unused warnings for helpers referenced from future SPEC steps
var _ = httputil.DumpResponse
