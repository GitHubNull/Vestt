// Package engine implements the YAML definition engine (Cardigann
// equivalent). This file holds the definition document model.
//
// Ported from: Jackett.Common/Models/IndexerDefinition.cs.
//
// Definitions are decoded manually from yaml.Node (not struct tags) because
// upstream YamlDotNet matches keys case-insensitively and the 582 shipped
// definitions use mixed casings (e.g. both `noResultsMessage` and
// `noresultsmessage` appear). Mapping order is preserved where upstream
// relies on it (inputs, fields, options, headers).
package engine

import (
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

// StringKV is one ordered key/value pair.
type StringKV struct {
	Key   string
	Value string
}

// StringMap is an insertion-ordered string→string map (upstream
// Dictionary<string,string> enumeration order).
type StringMap []StringKV

// Get returns the value for key (exact match).
func (m StringMap) Get(key string) (string, bool) {
	for _, kv := range m {
		if kv.Key == key {
			return kv.Value, true
		}
	}
	return "", false
}

// MustGet returns the value or "".
func (m StringMap) MustGet(key string) string {
	v, _ := m.Get(key)
	return v
}

// StringListKV is one ordered key→string-list pair (headers).
type StringListKV struct {
	Key    string
	Values []string
}

// StringListMap is an insertion-ordered string→[]string map.
type StringListMap []StringListKV

// Get returns the values for key.
func (m StringListMap) Get(key string) ([]string, bool) {
	for _, kv := range m {
		if kv.Key == key {
			return kv.Values, true
		}
	}
	return nil, false
}

// FilterBlock is upstream filterBlock (dynamic Args).
type FilterBlock struct {
	Name string
	Args any // string | int64 | float64 | []any | nil
}

// SelectorBlock is upstream selectorBlock, embedded in rows/ratio/etc.
type SelectorBlock struct {
	Selector  string
	Optional  bool
	Default   string
	Text      string
	Attribute string
	Remove    string
	Filters   []FilterBlock
	Case      StringMap
}

// SettingsField is upstream settingsField.
type SettingsField struct {
	Name     string
	Type     string
	Label    string
	Default  string
	Defaults []string
	Options  StringMap
}

// CategoryMappingBlock is upstream CategorymappingBlock.
type CategoryMappingBlock struct {
	ID      string
	Cat     string
	Desc    string
	Default bool
}

// CaptchaBlock is upstream captchaBlock.
type CaptchaBlock struct {
	Type     string
	Selector string
	Input    string
}

// ErrorBlock is upstream errorBlock.
type ErrorBlock struct {
	Path     string
	Selector string
	Message  *SelectorBlock
}

// PageTestBlock is upstream pageTestBlock.
type PageTestBlock struct {
	Path     string
	Selector string
}

// SelectorInputKV is one selectorinput entry: the posted field name plus
// the selectorBlock used to extract its value from the landing page.
type SelectorInputKV struct {
	Key   string
	Value *SelectorBlock
}

// SelectorInputMap is upstream Dictionary<string, selectorBlock>.
type SelectorInputMap []SelectorInputKV

// LoginBlock is upstream loginBlock.
type LoginBlock struct {
	Path              string
	Submitpath        string
	Cookies           []string
	Method            string
	Form              string
	Selectors         bool
	Inputs            StringMap
	SelectorInputs    SelectorInputMap
	GetSelectorInputs SelectorInputMap
	Error             []ErrorBlock
	Test              *PageTestBlock
	Captcha           *CaptchaBlock
	Headers           StringListMap
}

// RatioBlock is upstream ratioBlock.
type RatioBlock struct {
	SelectorBlock `yaml:"-"`
	Path          string
}

// RequestBlock is upstream requestBlock.
type RequestBlock struct {
	Path           string
	Method         string
	Inputs         StringMap
	Queryseparator string
}

// BeforeBlock is upstream beforeBlock.
type BeforeBlock struct {
	RequestBlock `yaml:"-"`
	PathSelector *SelectorField
}

// SelectorField is upstream selectorField.
type SelectorField struct {
	Selector          string
	Attribute         string
	Usebeforeresponse bool
	Filters           []FilterBlock
}

// InfohashBlock is upstream infohashBlock.
type InfohashBlock struct {
	Hash              *SelectorField
	Title             *SelectorField
	Usebeforeresponse bool
}

// DownloadBlock is upstream downloadBlock.
type DownloadBlock struct {
	Selectors []*SelectorField
	Method    string
	Before    *BeforeBlock
	Infohash  *InfohashBlock
	Headers   StringListMap
}

// ResponseBlock is upstream responseBlock.
type ResponseBlock struct {
	Type             string
	NoResultsMessage string
}

// SearchPathBlock is upstream searchPathBlock.
type SearchPathBlock struct {
	RequestBlock   `yaml:"-"`
	Categories     []string
	Inheritinputs  bool
	Followredirect bool
	Response       *ResponseBlock
}

// RowsBlock is upstream rowsBlock.
type RowsBlock struct {
	SelectorBlock                   `yaml:"-"`
	After                           int
	Dateheaders                     *SelectorBlock
	Count                           *SelectorBlock
	Multiple                        bool
	MissingAttributeEqualsNoResults bool
}

// FieldBlock is one search.fields entry (upstream KeyValuePairList item);
// the key may carry "|modifiers".
type FieldBlock struct {
	Name     string
	Selector *SelectorBlock
}

// SearchBlock is upstream searchBlock.
type SearchBlock struct {
	Path                 string
	Paths                []*SearchPathBlock
	Headers              StringListMap
	Keywordsfilters      []FilterBlock
	AllowEmptyInputs     bool
	Inputs               StringMap
	Error                []ErrorBlock
	Preprocessingfilters []FilterBlock
	Rows                 RowsBlock
	Fields               []FieldBlock
}

// CapsBlock is upstream capabilitiesBlock.
type CapsBlock struct {
	Categories        StringMap // tracker id → Torznab category name
	Categorymappings  []CategoryMappingBlock
	Modes             StringListMap // search mode → supported params
	Allowrawsearch    bool
	Allowtvsearchimdb bool
}

// IndexerDefinition is upstream IndexerDefinition.
type IndexerDefinition struct {
	ID              string
	Replaces        []string
	Settings        []*SettingsField
	Name            string
	Description     string
	Type            string
	Language        string
	Encoding        string
	RequestDelay    *float64
	Links           []string
	Legacylinks     []string
	Followredirect  bool
	Testlinktorrent bool
	Certificates    []string
	Caps            *CapsBlock
	Login           *LoginBlock
	Ratio           *RatioBlock
	Search          *SearchBlock
	Download        *DownloadBlock
}

// --- yaml.Node decoding helpers -------------------------------------------

// mappingKeys returns the keys of a mapping node in document order.
func keyLookup(n *yaml.Node, name string) *yaml.Node {
	if n == nil || n.Kind != yaml.MappingNode {
		return nil
	}
	lname := strings.ToLower(name)
	for i := 0; i+1 < len(n.Content); i += 2 {
		if strings.ToLower(n.Content[i].Value) == lname {
			return n.Content[i+1]
		}
	}
	return nil
}

func hasKey(n *yaml.Node, name string) bool { return keyLookup(n, name) != nil }

func decodeString(n *yaml.Node) string {
	if n == nil {
		return ""
	}
	var s string
	if err := n.Decode(&s); err != nil {
		return ""
	}
	return s
}

func decodeStringList(n *yaml.Node) []string {
	if n == nil {
		return nil
	}
	var out []string
	if err := n.Decode(&out); err != nil {
		return nil
	}
	return out
}

func decodeStringMap(n *yaml.Node) StringMap {
	if n == nil || n.Kind != yaml.MappingNode {
		return nil
	}
	var out StringMap
	for i := 0; i+1 < len(n.Content); i += 2 {
		out = append(out, StringKV{Key: n.Content[i].Value, Value: decodeString(n.Content[i+1])})
	}
	return out
}

func decodeStringListMap(n *yaml.Node) StringListMap {
	if n == nil || n.Kind != yaml.MappingNode {
		return nil
	}
	var out StringListMap
	for i := 0; i+1 < len(n.Content); i += 2 {
		k := n.Content[i].Value
		v := n.Content[i+1]
		var vals []string
		switch v.Kind {
		case yaml.SequenceNode:
			if err := v.Decode(&vals); err != nil {
				vals = nil
			}
		default:
			// tolerate a scalar header value (upstream YamlDotNet would not,
			// but be liberal on read; the schema validation is authoritative)
			vals = []string{decodeString(v)}
		}
		out = append(out, StringListKV{Key: k, Values: vals})
	}
	return out
}

func decodeFilters(n *yaml.Node) []FilterBlock {
	if n == nil || n.Kind != yaml.SequenceNode {
		return nil
	}
	var out []FilterBlock
	for _, item := range n.Content {
		if item.Kind != yaml.MappingNode {
			continue
		}
		f := FilterBlock{Name: decodeString(keyLookup(item, "name"))}
		f.Args = nodeToValue(keyLookup(item, "args"))
		out = append(out, f)
	}
	return out
}

func nodeToValue(n *yaml.Node) any {
	if n == nil {
		return nil
	}
	switch n.Kind {
	case yaml.ScalarNode:
		switch n.Tag {
		case "!!null":
			return nil
		case "!!bool":
			return n.Value == "true"
		case "!!int":
			var i int64
			_, _ = fmt.Sscanf(n.Value, "%d", &i) // parse failure falls through to the zero value
			return i
		case "!!float":
			var f float64
			_, _ = fmt.Sscanf(n.Value, "%g", &f) // parse failure falls through to the zero value
			return f
		default:
			return n.Value
		}
	case yaml.SequenceNode:
		out := make([]any, 0, len(n.Content))
		for _, c := range n.Content {
			out = append(out, nodeToValue(c))
		}
		return out
	case yaml.MappingNode:
		m := map[string]any{}
		for i := 0; i+1 < len(n.Content); i += 2 {
			m[n.Content[i].Value] = nodeToValue(n.Content[i+1])
		}
		return m
	}
	return nil
}

func decodeSelectorBlock(n *yaml.Node, out *SelectorBlock) {
	if n == nil {
		return
	}
	out.Selector = decodeString(keyLookup(n, "selector"))
	out.Optional = decodeBool(keyLookup(n, "optional"))
	out.Default = decodeString(keyLookup(n, "default"))
	out.Text = decodeString(keyLookup(n, "text"))
	out.Attribute = decodeString(keyLookup(n, "attribute"))
	out.Remove = decodeString(keyLookup(n, "remove"))
	out.Filters = decodeFilters(keyLookup(n, "filters"))
	out.Case = decodeStringMap(keyLookup(n, "case"))
}

func decodeBool(n *yaml.Node) bool {
	if n == nil {
		return false
	}
	var b bool
	if err := n.Decode(&b); err != nil {
		return false
	}
	return b
}

func decodeInt(n *yaml.Node) int {
	if n == nil {
		return 0
	}
	var i int
	if err := n.Decode(&i); err != nil {
		return 0
	}
	return i
}

// UnmarshalYAML decodes a settings field.
func (f *SettingsField) UnmarshalYAML(node *yaml.Node) error {
	f.Name = decodeString(keyLookup(node, "name"))
	f.Type = decodeString(keyLookup(node, "type"))
	f.Label = decodeString(keyLookup(node, "label"))
	f.Default = decodeString(keyLookup(node, "default"))
	f.Defaults = decodeStringList(keyLookup(node, "defaults"))
	f.Options = decodeStringMap(keyLookup(node, "options"))
	return nil
}

func decodeCategoryMappingBlock(n *yaml.Node) CategoryMappingBlock {
	var b CategoryMappingBlock
	b.ID = decodeString(keyLookup(n, "id"))
	b.Cat = decodeString(keyLookup(n, "cat"))
	b.Desc = decodeString(keyLookup(n, "desc"))
	b.Default = decodeBool(keyLookup(n, "default"))
	return b
}

func decodeErrorBlock(n *yaml.Node) ErrorBlock {
	var b ErrorBlock
	b.Path = decodeString(keyLookup(n, "path"))
	b.Selector = decodeString(keyLookup(n, "selector"))
	if msg := keyLookup(n, "message"); msg != nil {
		sb := &SelectorBlock{}
		if msg.Kind == yaml.MappingNode {
			decodeSelectorBlock(msg, sb)
		} else {
			sb.Text = decodeString(msg)
		}
		b.Message = sb
	}
	return b
}

func decodeCaptchaBlock(n *yaml.Node) *CaptchaBlock {
	if n == nil {
		return nil
	}
	return &CaptchaBlock{
		Type:     decodeString(keyLookup(n, "type")),
		Selector: decodeString(keyLookup(n, "selector")),
		Input:    decodeString(keyLookup(n, "input")),
	}
}

func decodePageTestBlock(n *yaml.Node) *PageTestBlock {
	if n == nil {
		return nil
	}
	return &PageTestBlock{
		Path:     decodeString(keyLookup(n, "path")),
		Selector: decodeString(keyLookup(n, "selector")),
	}
}

func decodeSelectorField(n *yaml.Node) *SelectorField {
	if n == nil {
		return nil
	}
	f := &SelectorField{
		Selector:          decodeString(keyLookup(n, "selector")),
		Attribute:         decodeString(keyLookup(n, "attribute")),
		Usebeforeresponse: decodeBool(keyLookup(n, "usebeforeresponse")),
		Filters:           decodeFilters(keyLookup(n, "filters")),
	}
	return f
}

func decodeRequestBlock(n *yaml.Node, out *RequestBlock) {
	if n == nil {
		return
	}
	out.Path = decodeString(keyLookup(n, "path"))
	out.Method = decodeString(keyLookup(n, "method"))
	out.Inputs = decodeStringMap(keyLookup(n, "inputs"))
	out.Queryseparator = decodeString(keyLookup(n, "queryseparator"))
}

func decodeBeforeBlock(n *yaml.Node) *BeforeBlock {
	if n == nil {
		return nil
	}
	b := &BeforeBlock{}
	decodeRequestBlock(n, &b.RequestBlock)
	b.PathSelector = decodeSelectorField(keyLookup(n, "pathselector"))
	return b
}

func decodeInfohashBlock(n *yaml.Node) *InfohashBlock {
	if n == nil {
		return nil
	}
	return &InfohashBlock{
		Hash:              decodeSelectorField(keyLookup(n, "hash")),
		Title:             decodeSelectorField(keyLookup(n, "title")),
		Usebeforeresponse: decodeBool(keyLookup(n, "usebeforeresponse")),
	}
}

func decodeSelectorInputMap(n *yaml.Node) SelectorInputMap {
	if n == nil || n.Kind != yaml.MappingNode {
		return nil
	}
	var out SelectorInputMap
	for i := 0; i+1 < len(n.Content); i += 2 {
		sb := &SelectorBlock{}
		decodeSelectorBlock(n.Content[i+1], sb)
		out = append(out, SelectorInputKV{Key: n.Content[i].Value, Value: sb})
	}
	return out
}

func decodeSearchPathBlock(n *yaml.Node) *SearchPathBlock {
	if n == nil {
		return nil
	}
	b := &SearchPathBlock{
		Categories:     decodeStringList(keyLookup(n, "categories")),
		Inheritinputs:  true,
		Followredirect: decodeBool(keyLookup(n, "followredirect")),
	}
	if hasKey(n, "inheritinputs") {
		b.Inheritinputs = decodeBool(keyLookup(n, "inheritinputs"))
	}
	decodeRequestBlock(n, &b.RequestBlock)
	if resp := keyLookup(n, "response"); resp != nil {
		b.Response = &ResponseBlock{
			Type:             decodeString(keyLookup(resp, "type")),
			NoResultsMessage: decodeString(keyLookup(resp, "noResultsMessage")),
		}
	}
	return b
}

func decodeRowsBlock(n *yaml.Node) RowsBlock {
	var b RowsBlock
	if n == nil {
		return b
	}
	decodeSelectorBlock(n, &b.SelectorBlock)
	b.After = decodeInt(keyLookup(n, "after"))
	if dh := keyLookup(n, "dateheaders"); dh != nil {
		sb := &SelectorBlock{}
		decodeSelectorBlock(dh, sb)
		b.Dateheaders = sb
	}
	if c := keyLookup(n, "count"); c != nil {
		sb := &SelectorBlock{}
		decodeSelectorBlock(c, sb)
		b.Count = sb
	}
	b.Multiple = decodeBool(keyLookup(n, "multiple"))
	b.MissingAttributeEqualsNoResults = decodeBool(keyLookup(n, "missingAttributeEqualsNoResults"))
	return b
}

// UnmarshalYAML decodes the definition document.
func (d *IndexerDefinition) UnmarshalYAML(node *yaml.Node) error {
	d.ID = decodeString(keyLookup(node, "id"))
	d.Replaces = decodeStringList(keyLookup(node, "replaces"))
	if settings := keyLookup(node, "settings"); settings != nil && settings.Kind == yaml.SequenceNode {
		for _, item := range settings.Content {
			f := &SettingsField{}
			if err := item.Decode(f); err != nil {
				return fmt.Errorf("settings: %w", err)
			}
			d.Settings = append(d.Settings, f)
		}
	}
	d.Name = decodeString(keyLookup(node, "name"))
	d.Description = decodeString(keyLookup(node, "description"))
	d.Type = decodeString(keyLookup(node, "type"))
	d.Language = decodeString(keyLookup(node, "language"))
	d.Encoding = decodeString(keyLookup(node, "encoding"))
	if rd := keyLookup(node, "requestDelay"); rd != nil {
		var f float64
		if err := rd.Decode(&f); err == nil {
			d.RequestDelay = &f
		}
	}
	d.Links = decodeStringList(keyLookup(node, "links"))
	d.Legacylinks = decodeStringList(keyLookup(node, "legacylinks"))
	d.Followredirect = decodeBool(keyLookup(node, "followredirect"))
	if hasKey(node, "testlinktorrent") {
		d.Testlinktorrent = decodeBool(keyLookup(node, "testlinktorrent"))
	} else {
		d.Testlinktorrent = true // upstream property default
	}
	d.Certificates = decodeStringList(keyLookup(node, "certificates"))

	if caps := keyLookup(node, "caps"); caps != nil {
		c := &CapsBlock{
			Categories:        decodeStringMap(keyLookup(caps, "categories")),
			Allowrawsearch:    decodeBool(keyLookup(caps, "allowrawsearch")),
			Allowtvsearchimdb: decodeBool(keyLookup(caps, "allowtvsearchimdb")),
		}
		if cm := keyLookup(caps, "categorymappings"); cm != nil && cm.Kind == yaml.SequenceNode {
			for _, item := range cm.Content {
				c.Categorymappings = append(c.Categorymappings, decodeCategoryMappingBlock(item))
			}
		}
		if modes := keyLookup(caps, "modes"); modes != nil && modes.Kind == yaml.MappingNode {
			for i := 0; i+1 < len(modes.Content); i += 2 {
				c.Modes = append(c.Modes, StringListKV{
					Key:    modes.Content[i].Value,
					Values: decodeStringList(modes.Content[i+1]),
				})
			}
		}
		d.Caps = c
	}

	if login := keyLookup(node, "login"); login != nil {
		l := &LoginBlock{
			Path:              decodeString(keyLookup(login, "path")),
			Submitpath:        decodeString(keyLookup(login, "submitpath")),
			Cookies:           decodeStringList(keyLookup(login, "cookies")),
			Method:            decodeString(keyLookup(login, "method")),
			Form:              decodeString(keyLookup(login, "form")),
			Selectors:         decodeBool(keyLookup(login, "selectors")),
			Inputs:            decodeStringMap(keyLookup(login, "inputs")),
			SelectorInputs:    decodeSelectorInputMap(keyLookup(login, "selectorinputs")),
			GetSelectorInputs: decodeSelectorInputMap(keyLookup(login, "getselectorinputs")),
			Captcha:           decodeCaptchaBlock(keyLookup(login, "captcha")),
			Test:              decodePageTestBlock(keyLookup(login, "test")),
			Headers:           decodeStringListMap(keyLookup(login, "headers")),
		}
		if eb := keyLookup(login, "error"); eb != nil && eb.Kind == yaml.SequenceNode {
			for _, item := range eb.Content {
				l.Error = append(l.Error, decodeErrorBlock(item))
			}
		}
		d.Login = l
	}

	if ratio := keyLookup(node, "ratio"); ratio != nil {
		r := &RatioBlock{Path: decodeString(keyLookup(ratio, "path"))}
		decodeSelectorBlock(ratio, &r.SelectorBlock)
		d.Ratio = r
	}

	if search := keyLookup(node, "search"); search != nil {
		s := &SearchBlock{
			Path:                 decodeString(keyLookup(search, "path")),
			Headers:              decodeStringListMap(keyLookup(search, "headers")),
			Keywordsfilters:      decodeFilters(keyLookup(search, "keywordsfilters")),
			AllowEmptyInputs:     decodeBool(keyLookup(search, "allowEmptyInputs")),
			Inputs:               decodeStringMap(keyLookup(search, "inputs")),
			Preprocessingfilters: decodeFilters(keyLookup(search, "preprocessingfilters")),
			Rows:                 decodeRowsBlock(keyLookup(search, "rows")),
		}
		if eb := keyLookup(search, "error"); eb != nil && eb.Kind == yaml.SequenceNode {
			for _, item := range eb.Content {
				s.Error = append(s.Error, decodeErrorBlock(item))
			}
		}
		if paths := keyLookup(search, "paths"); paths != nil && paths.Kind == yaml.SequenceNode {
			for _, item := range paths.Content {
				s.Paths = append(s.Paths, decodeSearchPathBlock(item))
			}
		}
		if fields := keyLookup(search, "fields"); fields != nil && fields.Kind == yaml.MappingNode {
			for i := 0; i+1 < len(fields.Content); i += 2 {
				name := fields.Content[i].Value
				sb := &SelectorBlock{}
				decodeSelectorBlock(fields.Content[i+1], sb)
				s.Fields = append(s.Fields, FieldBlock{Name: name, Selector: sb})
			}
		}
		d.Search = s
	}

	if dl := keyLookup(node, "download"); dl != nil {
		down := &DownloadBlock{
			Method:   decodeString(keyLookup(dl, "method")),
			Before:   decodeBeforeBlock(keyLookup(dl, "before")),
			Infohash: decodeInfohashBlock(keyLookup(dl, "infohash")),
			Headers:  decodeStringListMap(keyLookup(dl, "headers")),
		}
		if sels := keyLookup(dl, "selectors"); sels != nil && sels.Kind == yaml.SequenceNode {
			for _, item := range sels.Content {
				down.Selectors = append(down.Selectors, decodeSelectorField(item))
			}
		}
		d.Download = down
	}
	return nil
}
