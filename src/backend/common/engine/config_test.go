package engine

import (
	"strings"
	"testing"

	"vestt/common/indexer/configdata"
)

// configFormDef builds a definition exercising every setting type.
func configFormDef() *IndexerDefinition {
	return &IndexerDefinition{
		ID:       "cfgform",
		Name:     "Config Form",
		Type:     "private",
		Language: "en-US",
		Links:    []string{"https://example.invalid/"},
		Caps: &CapsBlock{
			Categories: StringMap{{Key: "1", Value: "Movies"}},
			Modes:      StringListMap{{Key: "search", Values: []string{"q"}}},
		},
		Search: &SearchBlock{Path: "/search"},
		Settings: []*SettingsField{
			{Name: "username", Label: "Username", Type: "text"},
			{Name: "passkey", Label: "Passkey", Type: "password"},
			{Name: "freeleech", Label: "Freeleech", Type: "checkbox", Default: "true"},
			{Name: "sort", Label: "Sort", Type: "select", Default: "seeders", Options: StringMap{
				{Key: "seeders", Value: "Seeders"},
				{Key: "leechers", Value: "Leechers"},
			}},
			{Name: "tags", Label: "Tags", Type: "multi-select", Defaults: []string{"a"}, Options: StringMap{
				{Key: "a", Value: "A"},
				{Key: "b", Value: "B"},
			}},
			{Name: "info", Label: "Info", Type: "info", Default: "Some instructions"},
			{Name: "cookieinfo", Label: "Cookie Howto", Type: "info_cookie"},
		},
	}
}

// TestSettingItemsMapping verifies §3.3: every setting type maps to the
// upstream item kind with the right id/type/default, in definition order.
func TestSettingItemsMapping(t *testing.T) {
	x, err := NewCardigann(configFormDef(), discardLogger())
	if err != nil {
		t.Fatalf("NewCardigann: %v", err)
	}
	items, err := configdata.ItemsJSON(x.config)
	if err != nil {
		t.Fatalf("ItemsJSON: %v", err)
	}
	// display items exclude hidden base items; sitelink first, tags last
	type want struct {
		id    string
		itype string
		value any
	}
	wants := []want{
		{"sitelink", "inputstring", nil},
		{"username", "inputstring", ""},
		{"passkey", "inputstring", ""},  // upstream maps password settings to StringConfigurationItem
		{"freeleech", "inputbool", nil}, // default "true" asserted below
		{"sort", "inputselect", "seeders"},
		{"tags", "inputcheckbox", nil},
		{"info", "displayinfo", "Some instructions"},
		{"howtogetthecookie", "displayinfo", nil}, // fixed "How to get the Cookie" text
		{"tags", "inputtags", nil},                // base Tags item is also named Tags
	}
	if len(items) != len(wants) {
		t.Fatalf("got %d display items, want %d: %v", len(items), len(wants), items)
	}
	for i, w := range wants {
		it := items[i]
		if it["id"] != w.id || it["type"] != w.itype {
			t.Errorf("item %d = (%v,%v), want (%v,%v)", i, it["id"], it["type"], w.id, w.itype)
		}
		if w.value != nil && it["value"] != w.value {
			t.Errorf("item %d value = %v, want %v", i, it["value"], w.value)
		}
	}
	// checkbox default "true" → Value true
	for _, it := range items {
		if it["id"] == "freeleech" && it["value"] != true {
			t.Errorf("freeleech default = %v, want true", it["value"])
		}
		if it["id"] == "howtogetthecookie" {
			text, _ := it["value"].(string)
			if !strings.Contains(text, "DevTools") {
				t.Errorf("info_cookie fixed text missing: %v", it["value"])
			}
		}
	}
	// select options preserve definition order
	for _, it := range items {
		if it["id"] == "sort" {
			opts, ok := it["options"].(map[string]any)
			if !ok || opts["seeders"] != "Seeders" || opts["leechers"] != "Leechers" {
				t.Errorf("sort options = %v", it["options"])
			}
		}
	}
}

// TestGetBaseTemplateVariables verifies §3.2 variable derivation.
func TestGetBaseTemplateVariables(t *testing.T) {
	def := configFormDef()
	x, err := NewCardigann(def, discardLogger())
	if err != nil {
		t.Fatalf("NewCardigann: %v", err)
	}
	cd := x.ConfigData()
	cd.GetDynamic("username").(*configdata.StringItem).Value = "alice"
	cd.GetDynamic("freeleech").(*configdata.BoolItem).Value = true
	cd.GetDynamic("sort").(*configdata.SelectItem).Value = "leechers"
	cd.GetDynamic("tags").(*configdata.MultiSelectItem).Values = []string{"a", "b"}

	vars := x.GetBaseTemplateVariables()
	if vars[".Config.username"] != "alice" {
		t.Errorf("username = %v", vars[".Config.username"])
	}
	if vars[".Config.freeleech"] != "True" {
		t.Errorf("freeleech bool → %v, want True", vars[".Config.freeleech"])
	}
	if vars[".Config.sort"] != "leechers" {
		t.Errorf("sort = %v", vars[".Config.sort"])
	}
	if v, ok := vars[".Config.tags"].([]string); !ok || len(v) != 2 {
		t.Errorf("tags = %v", vars[".Config.tags"])
	}
	if v := vars[".False"]; v != nil {
		t.Errorf(".False = %v, want nil", v)
	}
	if vars[".True"] != "True" {
		t.Errorf(".True = %v", vars[".True"])
	}
	if !strings.HasSuffix(vars[".Config.sitelink"].(string), "/") {
		t.Errorf("sitelink = %v", vars[".Config.sitelink"])
	}
}

// TestSettingDefaultsWhenNoSettings: definitions without settings get the
// upstream default username/password pair.
func TestSettingDefaultsWhenNoSettings(t *testing.T) {
	def := configFormDef()
	def.Settings = nil
	x, err := NewCardigann(def, discardLogger())
	if err != nil {
		t.Fatalf("NewCardigann: %v", err)
	}
	if x.config.GetDynamic("username") == nil || x.config.GetDynamic("password") == nil {
		t.Errorf("default username/password settings missing")
	}
}

// TestInvalidSettingType: unknown setting types fail construction
// (upstream throws in the constructor).
func TestInvalidSettingType(t *testing.T) {
	def := configFormDef()
	def.Settings = []*SettingsField{{Name: "x", Label: "X", Type: "bogus"}}
	if _, err := NewCardigann(def, discardLogger()); err == nil {
		t.Errorf("expected error for invalid setting type")
	}
}
