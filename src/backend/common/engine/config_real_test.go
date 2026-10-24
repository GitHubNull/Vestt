package engine

import (
	"testing"

	"vestt/common/indexer/configdata"
)

// SPEC-05 acceptance #7: 抽样 5 个真实 yml 走 Configure → GET config：
// 字段顺序/类型/默认值与上游 UI 展示一致（upstream CardigannIndexer
// settings → ConfigurationItem 映射）。
func TestConfigureConfigShapeRealDefs(t *testing.T) {
	cases := []struct {
		id   string
		want []map[string]any // expected items AFTER sitelink, in order
	}{
		{"1337x", []map[string]any{
			{"id": "filterbyuploader", "type": "inputstring"},
			{"id": "aboutfilteringbyuploader", "type": "displayinfo"},
			{"id": "flaresolverr", "type": "displayinfo"},
			{"id": "primarydownloadlink", "type": "inputselect", "value": "magnet:"},
			{"id": "fallbackdownloadlink", "type": "inputselect", "value": "//itorrents."},
			{"id": "aboutthedownloadlinks", "type": "displayinfo"},
			{"id": "disablesorting-1337xpreventssortingsearchesduringhighserverload,whichbreakstheindexerwhenperformingakeywordsearch-disableifyougetzeroresults", "type": "inputbool", "value": false},
		}},
		{"0dayfiles-api", []map[string]any{
			{"id": "apikey", "type": "inputstring"},
			{"id": "aboutyourapikey", "type": "displayinfo"},
			{"id": "searchfreeleechonly", "type": "inputbool", "value": false},
		}},
		{"bjshare", []map[string]any{
			{"id": "cookie", "type": "inputstring"},
			{"id": "howtogetthecookie", "type": "displayinfo"},
			{"id": "torrentgrouping", "type": "displayinfo"},
			{"id": "searchfreeleechonly", "type": "inputbool", "value": false},
		}},
		{"yts", nil},
		{"acgrip", nil},
	}

	for _, tc := range cases {
		t.Run(tc.id, func(t *testing.T) {
			def := loadDefinition(t, tc.id)
			x, err := NewCardigann(def, discardLogger())
			if err != nil {
				t.Fatalf("NewCardigann: %v", err)
			}
			items, err := configdata.ItemsJSON(x.config)
			if err != nil {
				t.Fatal(err)
			}
			if len(items) < 3 {
				t.Fatalf("too few items: %v", items)
			}
			// first: sitelink; last: tags (upstream configData layout)
			if items[0]["id"] != "sitelink" || items[0]["type"] != "inputstring" {
				t.Errorf("first item: %v", items[0])
			}
			if items[len(items)-1]["id"] != "tags" {
				t.Errorf("last item: %v", items[len(items)-1])
			}
			body := items[1 : len(items)-1]
			if len(body) != len(def.Settings) {
				t.Fatalf("settings count %d != definition %d", len(body), len(def.Settings))
			}
			// settings appear in definition order with upstream-mapped types
			for i, want := range tc.want {
				got := body[i]
				for k, v := range want {
					if got[k] != v {
						t.Errorf("item %d (%v): %s = %v, want %v", i, got["id"], k, got[k], v)
					}
				}
			}
			// select options (serialized as an insertion-ordered JSON object)
			if tc.id == "1337x" {
				var sel map[string]any
				for _, it := range body {
					if it["id"] == "primarydownloadlink" {
						sel = it
					}
				}
				opts, ok := sel["options"].(map[string]any)
				if !ok || len(opts) != 2 || opts["//itorrents."] != "iTorrents" || opts["magnet:"] != "magnet" {
					t.Errorf("select options: %v", sel["options"])
				}
			}
		})
	}
}
