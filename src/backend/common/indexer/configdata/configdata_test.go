package configdata

import (
	"encoding/json"
	"reflect"
	"testing"
)

type fullConfig struct {
	ConfigData
	Username    *StringItem
	Password    *PasswordItem
	Cookie      *StringItem
	AuthMethod  *SelectItem
	Categories  *MultiSelectItem
	FreeLeech   *BoolItem
	Notice      *InfoItem
	Captcha     *ImageItem
	ExtraHidden *HiddenItem
}

func newFullConfig() *fullConfig {
	return &fullConfig{
		ConfigData:  *NewConfigData(),
		Username:    NewStringItem("Username"),
		Password:    NewPasswordItem("Password"),
		Cookie:      NewStringItem("Cookie"),
		AuthMethod:  NewSelectItem("Authentication method", []Option{{"form", "Form"}, {"cookie", "Cookie"}}),
		Categories:  NewMultiSelectItem("Categories", []Option{{"2000", "Movies"}, {"3000", "Audio"}}),
		FreeLeech:   NewBoolItem("Freeleech"),
		Notice:      NewInfoItem("FlareSolverr", "This site uses Cloudflare..."),
		Captcha:     NewImageItem("Captcha"),
		ExtraHidden: NewHiddenItem("InternalState"),
	}
}

func TestCollectOrdering(t *testing.T) {
	cfg := newFullConfig()
	items := Collect(cfg)
	if len(items) == 0 {
		t.Fatal("no items")
	}
	if items[0].ID() != "sitelink" {
		t.Fatalf("Site Link must be first, got %s", items[0].ID())
	}
	last := items[len(items)-1]
	if last.ID() != "tags" {
		t.Fatalf("Tags must be last, got %s", last.ID())
	}
	// declaration order preserved between the anchors
	ids := make([]string, 0, len(items))
	for _, it := range items {
		ids = append(ids, it.ID())
	}
	// embedded ConfigData fields ride in declaration order (hidden items
	// included); Site Link is hoisted first and Tags pushed last.
	wantOrder := []string{"sitelink", "cookieheader", "lasterror", "username", "password", "cookie", "authenticationmethod", "categories", "freeleech", "flaresolverr", "captcha", "internalstate", "tags"}
	if !reflect.DeepEqual(ids, wantOrder) {
		t.Fatalf("ordering mismatch:\n got: %v\nwant: %v", ids, wantOrder)
	}
}

func TestHiddenItemsExcludedFromDisplay(t *testing.T) {
	cfg := newFullConfig()
	display, err := ItemsJSON(cfg)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range display {
		id := m["id"].(string)
		if id == "cookieheader" || id == "lasterror" || id == "internalstate" {
			t.Fatalf("hidden item %s must not appear in the display form", id)
		}
	}
	saved, err := MarshalItems(cfg)
	if err != nil {
		t.Fatal(err)
	}
	var arr []map[string]any
	if err := json.Unmarshal(saved, &arr); err != nil {
		t.Fatal(err)
	}
	found := map[string]bool{}
	for _, m := range arr {
		found[m["id"].(string)] = true
	}
	for _, id := range []string{"cookieheader", "lasterror", "internalstate", "sitelink", "tags"} {
		if !found[id] {
			t.Fatalf("saved config must contain %s", id)
		}
	}
	// displayinfo / displayimage are never saved
	if found["flaresolverr"] || found["captcha"] {
		t.Fatal("displayinfo/displayimage must not be saved")
	}
}

func TestPasswordMasking(t *testing.T) {
	p := NewPasswordItem("Password")
	p.Value = "secret"

	display := p.ToJSON(true)
	if display["value"] != PasswordReplacement {
		t.Fatalf("display must mask the password: %v", display["value"])
	}
	saved := p.ToJSON(false)
	if saved["value"] != "secret" {
		t.Fatalf("saved form must keep the password: %v", saved["value"])
	}

	// submitting the sentinel back must not overwrite the value
	p.FromJSON(map[string]any{"value": PasswordReplacement})
	if p.Value != "secret" {
		t.Fatalf("sentinel round trip must preserve the password: %q", p.Value)
	}
	p.FromJSON(map[string]any{"value": "new"})
	if p.Value != "new" {
		t.Fatalf("new password must be applied: %q", p.Value)
	}
}

func TestLegacyMaybePassword(t *testing.T) {
	s := NewStringItem("Password")
	s.Value = "legacy"
	if got := s.ToJSON(true)["value"]; got != PasswordReplacement {
		t.Fatalf("legacy string item named password must mask on display: %v", got)
	}
	s.FromJSON(map[string]any{"value": PasswordReplacement})
	if s.Value != "legacy" {
		t.Fatal("sentinel must be ignored")
	}
}

func TestItemRoundTrips(t *testing.T) {
	t.Run("string", func(t *testing.T) {
		it := NewStringItem("Username")
		it.FromJSON(map[string]any{"value": "alice"})
		if it.Value != "alice" || it.ToJSON(true)["value"] != "alice" {
			t.Fatalf("string round trip failed: %+v", it)
		}
	})
	t.Run("bool", func(t *testing.T) {
		it := NewBoolItem("Freeleech")
		it.FromJSON(map[string]any{"value": true})
		if !it.Value || it.ToJSON(true)["value"] != true {
			t.Fatalf("bool round trip failed: %+v", it)
		}
	})
	t.Run("select", func(t *testing.T) {
		it := NewSelectItem("Auth", []Option{{"a", "A"}, {"b", "B"}})
		it.FromJSON(map[string]any{"value": "b"})
		j := it.ToJSON(true)
		if j["value"] != "b" {
			t.Fatalf("select value: %v", j)
		}
		opts, ok := j["options"].(map[string]any)
		if !ok || opts["a"] != "A" || opts["b"] != "B" {
			t.Fatalf("select options: %v", j["options"])
		}
	})
	t.Run("multiselect", func(t *testing.T) {
		it := NewMultiSelectItem("Cats", []Option{{"1", "One"}})
		it.FromJSON(map[string]any{"values": []any{"1", "2"}})
		if !reflect.DeepEqual(it.Values, []string{"1", "2"}) {
			t.Fatalf("multiselect values: %v", it.Values)
		}
		j := it.ToJSON(true)
		if !reflect.DeepEqual(j["values"], []string{"1", "2"}) {
			t.Fatalf("multiselect json values: %v", j["values"])
		}
	})
	t.Run("info", func(t *testing.T) {
		it := NewInfoItem("Notice", "hello")
		if it.ToJSON(true)["value"] != "hello" {
			t.Fatal("info value")
		}
		if it.ToJSON(false) != nil {
			t.Fatal("info must not be saved")
		}
	})
	t.Run("image", func(t *testing.T) {
		it := NewImageItem("Captcha")
		it.Value = []byte("JPEGDATA")
		j := it.ToJSON(true)
		if j["value"] != "data:image/jpeg;base64,SlBFR0RBVEE=" {
			t.Fatalf("image data uri: %v", j["value"])
		}
		if it.ToJSON(false) != nil {
			t.Fatal("image must not be saved")
		}
	})
	t.Run("hidden", func(t *testing.T) {
		it := NewHiddenItem("Secret")
		it.Value = "x"
		if it.ToJSON(true) != nil {
			t.Fatal("hidden must not be displayed")
		}
		if it.ToJSON(false)["value"] != "x" {
			t.Fatal("hidden must be saved")
		}
	})
}

func TestTagsParsing(t *testing.T) {
	tags := NewTagsItem("Tags", `A-Za-z0-9\-\._~`)
	if tags.ItemType() != "inputtags" {
		t.Fatalf("item type: %s", tags.ItemType())
	}
	tags.FromJSON(map[string]any{"value": "Movies, tv-Shows;BAD TAG!docs.4k"})
	got := tags.SortedValues()
	// '.' is in the char set, so "docs.4k" stays one token
	want := []string{"bad", "docs.4k", "movies", "tag", "tv-shows"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("tags parsed: got %v want %v", got, want)
	}
	j := tags.ToJSON(true)
	if j["pattern"] != `^[A-Za-z0-9\-\._~]+$` {
		t.Fatalf("pattern: %v", j["pattern"])
	}
	if j["delimiters"] != `[^A-Za-z0-9\-\._~]+` {
		t.Fatalf("delimiters: %v", j["delimiters"])
	}
	if j["separator"] != "," {
		t.Fatalf("separator: %v", j["separator"])
	}
	// all chars survive the pattern so value round-trips sorted
	if j["value"] != "bad,docs.4k,movies,tag,tv-shows" {
		t.Fatalf("tags value: %v", j["value"])
	}
}

func TestUnmarshalMissingIDsSkipped(t *testing.T) {
	cfg := newFullConfig()
	raw := []byte(`[{"id":"username","value":"bob"},{"id":"nonexistent","value":"zzz"}]`)
	if err := UnmarshalItems(cfg, raw); err != nil {
		t.Fatal(err)
	}
	if cfg.Username.Value != "bob" {
		t.Fatalf("username not loaded: %q", cfg.Username.Value)
	}
}

func TestConfigDataDynamics(t *testing.T) {
	cfg := newFullConfig()
	cfg.AddDynamic("retryAttempts", NewSelectItem("Number of retries", []Option{
		{"0", "No retries (fail fast)"}, {"2", "2 retries (1s delay)"},
	}))
	if cfg.GetDynamic("retryAttempts") == nil {
		t.Fatal("dynamic lookup failed")
	}
	items := Collect(cfg)
	// dynamics sit between declared fields and tags
	last := items[len(items)-1]
	if last.ID() != "tags" {
		t.Fatalf("tags must stay last after dynamics: %s", last.ID())
	}
	found := false
	for _, it := range items {
		if it.ID() == "numberofretries" {
			found = true
		}
	}
	if !found {
		t.Fatal("dynamic item missing from collected set")
	}
}
