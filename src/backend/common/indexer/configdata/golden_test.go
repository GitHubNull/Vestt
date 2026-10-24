package configdata

import (
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"testing"
)

var update = flag.Bool("update", false, "rewrite golden files")

// TestConfigDTOGolden byte-compares the display (backend-api.md §2.2) and
// saved serialization against frozen goldens covering all item types.
func TestConfigDTOGolden(t *testing.T) {
	cfg := newFullConfig()
	cfg.SiteLink.Value = "https://example.com/"
	cfg.Username.Value = "demo"
	cfg.Password.Value = "hunter2"
	cfg.AuthMethod.Value = "form"
	cfg.Categories.Values = []string{"2000"}
	cfg.FreeLeech.Value = true
	cfg.Captcha.Value = []byte("JPEGDATA")
	cfg.CookieHeader.Value = "uid=1; pass=abc"
	cfg.LastError.Value = ""
	cfg.Tags.Values["movies"] = struct{}{}

	display, err := ItemsJSON(cfg)
	if err != nil {
		t.Fatal(err)
	}
	saved, err := MarshalItems(cfg)
	if err != nil {
		t.Fatal(err)
	}

	// normalize: re-encode through encoding/json for stable comparison
	pretty := func(raw []byte) string {
		var v any
		if err := json.Unmarshal(raw, &v); err != nil {
			t.Fatal(err)
		}
		out, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		return string(out)
	}
	displayRaw, err := json.Marshal(display)
	if err != nil {
		t.Fatal(err)
	}

	for name, got := range map[string]string{
		"display.json": pretty(displayRaw),
		"saved.json":   pretty(saved),
	} {
		path := filepath.Join("testdata", name)
		if *update {
			if err := os.MkdirAll("testdata", 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(got+"\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			continue
		}
		want, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read golden %s: %v (run with -update to create)", path, err)
		}
		if got != string(want[:len(want)-1]) {
			t.Fatalf("golden mismatch %s:\n got: %s\nwant: %s", path, got, want)
		}
	}
}
