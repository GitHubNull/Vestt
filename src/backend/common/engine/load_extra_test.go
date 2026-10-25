// Definition loading unit tests (SPEC-05): overlay failures, secondary
// validation and the YAML→JSON value conversion.
package engine

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"gopkg.in/yaml.v3"
)

func quietLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func writeOverlay(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// overlayCapsBlock is the caps block spliced into overlayTpl; kept separate
// so tests can omit it entirely.
const overlayCapsBlock = `caps:
  categories:
    1: Movies
  modes:
    search: [q]
`

// overlayTpl builds a schema-valid minimal definition (every SchemaRoot
// required property present, FieldsBlock with seeders/size/title/category/
// download per the schema oneOf/anyOf constraints).
const overlayTpl = `id: %q
name: %s
description: overlay fixture
type: public
language: en-US
encoding: utf-8
links: [%s]
%[4]ssearch:
  path: /search
  rows:
    selector: tr.row
  fields:
    title:
      selector: td.t
    download:
      selector: a.dl
    category:
      selector: td.c
    size:
      selector: td.s
    seeders:
      selector: td.sd
`

func TestLoadAllOverlayFailures(t *testing.T) {
	dir := t.TempDir()
	writeOverlay(t, dir, "badyml.yml", "id: [unclosed\n")
	// id: "" passes the schema (plain string) but fails the id check in loadOne
	writeOverlay(t, dir, "noid.yml", fmt.Sprintf(overlayTpl, "", "No ID", "https://a.test/", overlayCapsBlock))
	// ":: bad ::" passes the schema (format uri is annotation-only) but
	// fails the secondary url.Parse check in LoadAll
	writeOverlay(t, dir, "badlink.yml", fmt.Sprintf(overlayTpl, "badlink", "Bad Link", "':: bad ::'", overlayCapsBlock))
	// caps is required by the schema → rejected during schema validation
	writeOverlay(t, dir, "nocaps.yml", fmt.Sprintf(overlayTpl, "nocaps", "No Caps", "https://c.test/", ""))

	defs, stats, err := LoadAll(quietLogger(), dir)
	if err != nil {
		t.Fatalf("LoadAll: %v", err)
	}
	if len(defs) == 0 {
		t.Fatal("embedded definitions must still load")
	}
	for _, want := range []string{
		"badyml.yml:",
		"noid.yml: missing id",
		"badlink: invalid link",
		"nocaps.yml: schema:",
	} {
		found := false
		for _, s := range stats.Skipped {
			if len(s) >= len(want) && s[:len(want)] == want {
				found = true
			}
		}
		if !found {
			t.Errorf("skipped list missing %q; got %v", want, stats.Skipped)
		}
	}
	if stats.Loaded != len(defs) {
		t.Fatalf("stats.Loaded = %d, len(defs) = %d", stats.Loaded, len(defs))
	}
	// failed overlays must not appear
	for _, id := range []string{"badyml", "noid", "badlink", "nocaps"} {
		if _, ok := defs[id]; ok {
			t.Errorf("invalid definition %s loaded", id)
		}
	}
}

func TestLoadAllOverlayShadowing(t *testing.T) {
	dir := t.TempDir()
	// shadow an embedded definition: overlay wins, warning recorded
	writeOverlay(t, dir, "1337x.yml", fmt.Sprintf(overlayTpl, "1337x", "Local 1337x Overlay", "https://overlay.test/", overlayCapsBlock))
	defs, _, err := LoadAll(quietLogger(), dir)
	if err != nil {
		t.Fatalf("LoadAll: %v", err)
	}
	def, ok := defs["1337x"]
	if !ok {
		t.Fatal("1337x missing")
	}
	if def.Name != "Local 1337x Overlay" || def.Links[0] != "https://overlay.test/" {
		t.Fatalf("overlay did not shadow: %+v", def)
	}
}

func TestValidateDefinition(t *testing.T) {
	ok := &IndexerDefinition{
		Links: []string{"https://a.test/", "https://b.test/x"},
		Caps:  &CapsBlock{},
	}
	if err := validateDefinition(ok); err != nil {
		t.Fatalf("valid def: %v", err)
	}
	if err := validateDefinition(&IndexerDefinition{Caps: &CapsBlock{}}); err == nil || err.Error() != "no links defined" {
		t.Fatalf("no links: %v", err)
	}
	if err := validateDefinition(&IndexerDefinition{Links: []string{"https://good.test/", "not a url at all"}, Caps: &CapsBlock{}}); err == nil {
		t.Fatal("relative link must fail")
	}
	if err := validateDefinition(&IndexerDefinition{Links: []string{"https://good.test/", "ftp://"}, Caps: &CapsBlock{}}); err == nil || err.Error() != `invalid link "ftp://"` {
		t.Fatalf("scheme-less host: %v", err)
	}
	if err := validateDefinition(&IndexerDefinition{Links: []string{"https://a.test/"}}); err == nil || err.Error() != "no caps block" {
		t.Fatalf("no caps: %v", err)
	}
}

func TestToJSONValue(t *testing.T) {
	var node yaml.Node
	yamlDoc := `
int: 42
float: 2.5
bool: true
false-bool: false
null: null
string: text
seq: [1, two]
map:
  nested: deep
alias: &base
  x: 1
ref: *base
`
	if err := yaml.Unmarshal([]byte(yamlDoc), &node); err != nil {
		t.Fatal(err)
	}
	v, err := toJSONValue(&node)
	if err != nil {
		t.Fatalf("toJSONValue: %v", err)
	}
	m := v.(map[string]any)
	if m["int"] != int64(42) || m["float"] != 2.5 || m["bool"] != true || m["false-bool"] != false {
		t.Fatalf("scalars: %#v", m)
	}
	if m["null"] != nil || m["string"] != "text" {
		t.Fatalf("null/string: %#v", m)
	}
	if seq, ok := m["seq"].([]any); !ok || len(seq) != 2 || seq[0] != int64(1) || seq[1] != "two" {
		t.Fatalf("seq: %#v", m["seq"])
	}
	if mp, ok := m["map"].(map[string]any); !ok || mp["nested"] != "deep" {
		t.Fatalf("map: %#v", m["map"])
	}
	// alias resolves to the anchored mapping
	if ref, ok := m["ref"].(map[string]any); !ok || ref["x"] != int64(1) {
		t.Fatalf("alias: %#v", m["ref"])
	}

	// nil node → nil
	if v, err := toJSONValue(nil); v != nil || err != nil {
		t.Fatalf("nil node = %v %v", v, err)
	}
	// empty document → error (unsupported kind)
	var empty yaml.Node
	if err := yaml.Unmarshal([]byte(""), &empty); err != nil {
		t.Fatal(err)
	}
	if _, err := toJSONValue(&empty); err == nil {
		t.Fatal("empty document must error")
	}
}

func TestFixYAML11Escapes(t *testing.T) {
	// "\/" inside double quotes → "\x2F" (both resolve to "/"); "\\d" is a
	// valid YAML-1.2 escaped backslash and must be left alone.
	raw := []byte(`args: "^(\\d+) \/ rest"`)
	fixed := fixYAML11Escapes(raw)
	if fixed == nil {
		t.Fatal("escape not rewritten")
	}
	if string(fixed) != `args: "^(\\d+) \x2F rest"` {
		t.Fatalf("fixed = %q", fixed)
	}
	// the original must fail to parse (YAML 1.2 rejects "\/")
	var a, b yaml.Node
	if err := yaml.Unmarshal(raw, &b); err == nil {
		t.Skip("original already parses; upstream-only case")
	}
	if err := yaml.Unmarshal(fixed, &a); err != nil {
		t.Fatalf("fixed form does not parse: %v", err)
	}
	if got := a.Content[0].Content[1].Value; got != `^(\d+) / rest` {
		t.Fatalf("decoded scalar = %q", got)
	}
	// no escape → nil (no rewrite needed)
	if got := fixYAML11Escapes([]byte(`a: "plain / slash"`)); got != nil {
		t.Fatalf("plain slash = %q, want nil", got)
	}
	// escaped backslash before slash is not rewritten
	if got := fixYAML11Escapes([]byte(`a: "x\\\\/y"`)); got != nil {
		t.Fatalf("double backslash = %q, want nil", got)
	}
	// slash outside double quotes is untouched
	if got := fixYAML11Escapes([]byte("a: plain/url")); got != nil {
		t.Fatalf("outside quotes = %q, want nil", got)
	}
}

func TestLoadOneReadError(t *testing.T) {
	defs := map[string]*IndexerDefinition{}
	stats := &LoadStats{}
	loadOne(defs, stats, quietLogger(), func() ([]byte, error) {
		return nil, os.ErrPermission
	}, "boom.yml")
	if len(defs) != 0 {
		t.Fatal("failed load must not register a definition")
	}
	if len(stats.Skipped) != 1 || stats.Skipped[0] != "boom.yml: permission denied" {
		t.Fatalf("skipped = %v", stats.Skipped)
	}
}
