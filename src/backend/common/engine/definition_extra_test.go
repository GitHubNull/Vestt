// Definition decode unit tests (SPEC-05): the hand-written yaml.Node decoder
// must map upstream YamlDotNet semantics — case-insensitive keys, ordered
// maps, scalar-tolerant header lists and typed filter args.
package engine

import (
	"testing"

	"gopkg.in/yaml.v3"
)

func TestStringMapGet(t *testing.T) {
	m := StringMap{{Key: "a", Value: "1"}, {Key: "b", Value: "2"}}
	if v, ok := m.Get("b"); !ok || v != "2" {
		t.Fatalf("Get(b) = %q, %v", v, ok)
	}
	if _, ok := m.Get("c"); ok {
		t.Fatal("Get(c) must miss")
	}
	if v := m.MustGet("a"); v != "1" {
		t.Fatalf("MustGet(a) = %q", v)
	}
	if v := m.MustGet("zz"); v != "" {
		t.Fatalf("MustGet(zz) = %q, want empty", v)
	}
}

func TestStringListMapGet(t *testing.T) {
	m := StringListMap{{Key: "X-Custom", Values: []string{"one", "two"}}}
	if v, ok := m.Get("X-Custom"); !ok || len(v) != 2 {
		t.Fatalf("Get = %v, %v", v, ok)
	}
	if _, ok := m.Get("Other"); ok {
		t.Fatal("Get(Other) must miss")
	}
}

const fullDefYAML = `
id: decodefull
name: Decode Full
description: decoder exercise
type: private
language: en-US
encoding: windows-1252
requestDelay: 2.5
replaces: [old-def]
links:
  - https://example.test/
legacylinks:
  - https://legacy.example.test/
followredirect: true
testlinktorrent: false
certificates:
  - example.test
settings:
  - name: username
    type: text
    label: Username
  - name: multiselect
    type: multi-select
    label: Multi
    defaults: [a, b]
    options:
      a: Alpha
      b: Beta
  - name: sel
    type: select
    label: Select
    default: b
    options:
      a: Alpha
      b: Beta
caps:
  categories:
    1: Movies
  categorymappings:
    - id: 7
      cat: TV
      desc: TV Shows
      default: true
  modes:
    search: [q]
    tv-search: [q, season, ep]
  allowrawsearch: true
  allowtvsearchimdb: true
login:
  method: form
  path: /login
  submitpath: /do-login
  cookies:
    - a=1
    - b=2
  form: form#login
  selectors: true
  inputs:
    username: u
    password: p
  selectorinputs:
    csrf:
      selector: "#csrf"
      attribute: value
      optional: true
      default: none
      remove: span
      filters:
        - name: trim
      case:
        x: y
  getselectorinputs:
    token:
      selector: meta[name=token]
      attribute: content
  error:
    - path: /
      selector: .err
      message: plain message
    - selector: .err2
      message:
        selector: .msg
        filters:
          - name: prepend
            args: "E: "
  test:
    path: /torrents
    selector: "#content"
  captcha:
    type: image
    selector: "#captcha"
    input: captcha
  headers:
    X-Login: [one, two]
    X-Scalar: single
ratio:
  path: /ratio
  selector: ".ratio"
search:
  path: /old
  headers:
    X-Search: [hdr]
  keywordsfilters:
    - name: re_replace
      args: ["\\s+", " "]
  allowEmptyInputs: true
  inputs:
    q: "{{ .Keywords }}"
  preprocessingfilters:
    - name: replace
      args: ["&nbsp;", " "]
  error:
    - selector: .serr
  paths:
    - path: /tv
      categories: [5000]
      inheritinputs: false
      followredirect: true
      method: post
      queryseparator: ";"
      inputs:
        season: "{{ .Query.Season }}"
      response:
        type: json
        noResultsMessage: nothing here
  rows:
    selector: tr.row
    after: 1
    multiple: true
    missingAttributeEqualsNoResults: true
    dateheaders:
      selector: tr.date
      optional: true
    count:
      selector: "#count"
  fields:
    title:
      selector: td.title
    size:
      text: "1 GB"
    genre:
      selector: td.g
      optional: true
download:
  method: post
  headers:
    X-Dl: [dl]
  before:
    path: /before
    method: get
    inputs:
      id: "1"
    pathselector:
      selector: a.link
      attribute: href
      usebeforeresponse: true
      filters:
        - name: querystring
          args: u
  infohash:
    hash:
      selector: "#hash"
    title:
      selector: "#title"
    usebeforeresponse: true
  selectors:
    - selector: a.one
    - selector: a.two
      attribute: href
`

func decodeFullDef(t *testing.T) *IndexerDefinition {
	t.Helper()
	var node yaml.Node
	if err := yaml.Unmarshal([]byte(fullDefYAML), &node); err != nil {
		t.Fatalf("yaml unmarshal: %v", err)
	}
	var def IndexerDefinition
	if err := node.Decode(&def); err != nil {
		t.Fatalf("definition decode: %v", err)
	}
	return &def
}

func TestUnmarshalFullDefinition(t *testing.T) {
	def := decodeFullDef(t)

	if def.ID != "decodefull" || def.Name != "Decode Full" || def.Type != "private" ||
		def.Language != "en-US" || def.Encoding != "windows-1252" {
		t.Fatalf("scalars: %+v", def)
	}
	// float pointer (decodeStringPtr path)
	if def.RequestDelay == nil || *def.RequestDelay != 2.5 {
		t.Fatalf("requestDelay = %v", def.RequestDelay)
	}
	if len(def.Replaces) != 1 || def.Replaces[0] != "old-def" ||
		len(def.Links) != 1 || def.Links[0] != "https://example.test/" ||
		len(def.Legacylinks) != 1 || len(def.Certificates) != 1 {
		t.Fatalf("lists: %+v", def)
	}
	if !def.Followredirect || def.Testlinktorrent {
		t.Fatalf("bools: followredirect=%v testlinktorrent=%v", def.Followredirect, def.Testlinktorrent)
	}

	// settings (ordered, defaults + options)
	if len(def.Settings) != 3 {
		t.Fatalf("settings = %d", len(def.Settings))
	}
	ms := def.Settings[1]
	if ms.Type != "multi-select" || len(ms.Defaults) != 2 || len(ms.Options) != 2 ||
		ms.Options[0].Key != "a" || ms.Options[0].Value != "Alpha" {
		t.Fatalf("multi-select: %+v", ms)
	}
	if def.Settings[2].Default != "b" {
		t.Fatalf("select default: %+v", def.Settings[2])
	}

	// caps
	if def.Caps == nil || len(def.Caps.Categories) != 1 || def.Caps.Categories[0].Key != "1" ||
		!def.Caps.Allowrawsearch || !def.Caps.Allowtvsearchimdb {
		t.Fatalf("caps: %+v", def.Caps)
	}
	if len(def.Caps.Categorymappings) != 1 || def.Caps.Categorymappings[0].ID != "7" ||
		!def.Caps.Categorymappings[0].Default || def.Caps.Categorymappings[0].Desc != "TV Shows" {
		t.Fatalf("categorymappings: %+v", def.Caps.Categorymappings)
	}
	if len(def.Caps.Modes) != 2 || def.Caps.Modes[0].Values[0] != "q" {
		t.Fatalf("modes: %+v", def.Caps.Modes)
	}

	// login
	l := def.Login
	if l == nil || l.Method != "form" || l.Submitpath != "/do-login" || !l.Selectors ||
		len(l.Cookies) != 2 || l.Form != "form#login" {
		t.Fatalf("login scalars: %+v", l)
	}
	if len(l.Inputs) != 2 || l.Inputs[0].Key != "username" {
		t.Fatalf("login inputs: %+v", l.Inputs)
	}
	si := l.SelectorInputs
	if len(si) != 1 || si[0].Key != "csrf" {
		t.Fatalf("selectorinputs: %+v", si)
	}
	sb := si[0].Value
	if sb.Selector != "#csrf" || sb.Attribute != "value" || !sb.Optional ||
		sb.Default != "none" || sb.Remove != "span" || len(sb.Filters) != 1 ||
		len(sb.Case) != 1 || sb.Case[0].Key != "x" {
		t.Fatalf("selectorblock: %+v", sb)
	}
	if len(l.GetSelectorInputs) != 1 || l.GetSelectorInputs[0].Value.Attribute != "content" {
		t.Fatalf("getselectorinputs: %+v", l.GetSelectorInputs)
	}
	// error blocks: scalar message vs mapping message
	if len(l.Error) != 2 {
		t.Fatalf("errors = %d", len(l.Error))
	}
	if l.Error[0].Message == nil || l.Error[0].Message.Text != "plain message" || l.Error[0].Path != "/" {
		t.Fatalf("error0: %+v", l.Error[0])
	}
	e1 := l.Error[1].Message
	if e1 == nil || e1.Selector != ".msg" || len(e1.Filters) != 1 || e1.Filters[0].Args != "E: " {
		t.Fatalf("error1 message: %+v", e1)
	}
	if l.Test == nil || l.Test.Path != "/torrents" || l.Captcha == nil || l.Captcha.Type != "image" {
		t.Fatalf("test/captcha: %+v %+v", l.Test, l.Captcha)
	}
	// headers: sequence value and scalar value both tolerated
	if v, ok := l.Headers.Get("X-Login"); !ok || len(v) != 2 {
		t.Fatalf("X-Login: %v %v", v, ok)
	}
	if v, ok := l.Headers.Get("X-Scalar"); !ok || len(v) != 1 || v[0] != "single" {
		t.Fatalf("X-Scalar: %v %v", v, ok)
	}

	// ratio
	if def.Ratio == nil || def.Ratio.Path != "/ratio" || def.Ratio.Selector != ".ratio" {
		t.Fatalf("ratio: %+v", def.Ratio)
	}

	// search
	s := def.Search
	if s == nil || s.Path != "/old" || !s.AllowEmptyInputs {
		t.Fatalf("search scalars: %+v", s)
	}
	if len(s.Keywordsfilters) != 1 {
		t.Fatalf("keywordsfilters: %+v", s.Keywordsfilters)
	}
	if args, ok := s.Keywordsfilters[0].Args.([]any); !ok || len(args) != 2 || args[0] != "\\s+" || args[1] != " " {
		t.Fatalf("keywordsfilters args: %#v", s.Keywordsfilters[0].Args)
	}
	if len(s.Preprocessingfilters) != 1 {
		t.Fatalf("preprocessingfilters: %+v", s.Preprocessingfilters)
	}
	if len(s.Error) != 1 {
		t.Fatalf("search error: %+v", s.Error)
	}
	if len(s.Paths) != 1 {
		t.Fatalf("paths = %d", len(s.Paths))
	}
	p := s.Paths[0]
	if p.Path != "/tv" || p.Method != "post" || p.Queryseparator != ";" ||
		len(p.Categories) != 1 || p.Categories[0] != "5000" || p.Inheritinputs ||
		!p.Followredirect || len(p.Inputs) != 1 {
		t.Fatalf("path block: %+v", p)
	}
	if p.Response == nil || p.Response.Type != "json" || p.Response.NoResultsMessage != "nothing here" {
		t.Fatalf("response: %+v", p.Response)
	}
	r := s.Rows
	if r.Selector != "tr.row" || r.After != 1 || !r.Multiple ||
		!r.MissingAttributeEqualsNoResults {
		t.Fatalf("rows: %+v", r)
	}
	if r.Dateheaders == nil || !r.Dateheaders.Optional || r.Count == nil || r.Count.Selector != "#count" {
		t.Fatalf("rows sub-blocks: %+v %+v", r.Dateheaders, r.Count)
	}
	if len(s.Fields) != 3 || s.Fields[0].Name != "title" || s.Fields[0].Selector.Selector != "td.title" ||
		s.Fields[1].Selector.Text != "1 GB" || !s.Fields[2].Selector.Optional {
		t.Fatalf("fields: %+v", s.Fields)
	}

	// download
	d := def.Download
	if d == nil || d.Method != "post" {
		t.Fatalf("download: %+v", d)
	}
	if d.Before == nil || d.Before.Path != "/before" || d.Before.Method != "get" ||
		len(d.Before.Inputs) != 1 {
		t.Fatalf("before: %+v", d.Before)
	}
	ps := d.Before.PathSelector
	if ps == nil || ps.Selector != "a.link" || ps.Attribute != "href" || !ps.Usebeforeresponse ||
		len(ps.Filters) != 1 {
		t.Fatalf("pathselector: %+v", ps)
	}
	if d.Infohash == nil || d.Infohash.Hash.Selector != "#hash" ||
		d.Infohash.Title.Selector != "#title" || !d.Infohash.Usebeforeresponse {
		t.Fatalf("infohash: %+v", d.Infohash)
	}
	if len(d.Selectors) != 2 || d.Selectors[0].Selector != "a.one" || d.Selectors[1].Attribute != "href" {
		t.Fatalf("selectors: %+v", d.Selectors)
	}
	if v, ok := d.Headers.Get("X-Dl"); !ok || len(v) != 1 {
		t.Fatalf("dl headers: %v %v", v, ok)
	}
}

func TestUnmarshalTestlinktorrentDefault(t *testing.T) {
	var node yaml.Node
	if err := yaml.Unmarshal([]byte("id: mini\nlinks: [https://a.test/]\n"), &node); err != nil {
		t.Fatal(err)
	}
	var def IndexerDefinition
	if err := node.Decode(&def); err != nil {
		t.Fatal(err)
	}
	if !def.Testlinktorrent {
		t.Fatal("testlinktorrent must default to true (upstream property default)")
	}
	if def.Caps != nil || def.Login != nil || def.Search != nil || def.Download != nil || def.Ratio != nil {
		t.Fatal("absent blocks must stay nil")
	}
}
