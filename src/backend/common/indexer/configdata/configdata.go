package configdata

import (
	"encoding/json"
	"reflect"
	"strings"
)

// dynamicEntry preserves insertion order like upstream
// List<KeyValuePair<string, ConfigurationItem>>.
type dynamicEntry struct {
	id   string
	item Item
}

// ConfigData is the base configuration every indexer embeds
// (upstream ConfigurationData base class): hidden cookie/last-error items,
// the Site Link field (always displayed first) and Tags (always last),
// plus per-indexer dynamic items.
type ConfigData struct {
	CookieHeader *HiddenItem
	LastError    *HiddenItem
	SiteLink     *StringItem
	Tags         *TagsItem

	dynamics []dynamicEntry
}

// NewConfigData mirrors the upstream base-class property initialization.
func NewConfigData() *ConfigData {
	return &ConfigData{
		CookieHeader: NewHiddenItem("CookieHeader"),
		LastError:    NewHiddenItem("LastError"),
		SiteLink:     NewStringItem("Site Link"),
		Tags:         NewTagsItem("Tags", `A-Za-z0-9\-\._~`),
	}
}

// AddDynamic mirrors upstream AddDynamic (appends; duplicates allowed).
func (c *ConfigData) AddDynamic(id string, item Item) {
	c.dynamics = append(c.dynamics, dynamicEntry{id: id, item: item})
}

// GetDynamic mirrors upstream GetDynamic (first match by id).
func (c *ConfigData) GetDynamic(id string) Item {
	for _, e := range c.dynamics {
		if e.id == id {
			return e.item
		}
	}
	return nil
}

// GetDynamicByName mirrors upstream GetDynamicByName (first match, case-insensitive).
func (c *ConfigData) GetDynamicByName(name string) Item {
	for _, e := range c.dynamics {
		if e.item != nil && strings.EqualFold(e.item.Name(), name) {
			return e.item
		}
	}
	return nil
}

// itemType is the reflect type of the Item interface.
var itemType = reflect.TypeOf((*Item)(nil)).Elem()

// Collect gathers every Item reachable from cfg, mirroring upstream
// GetAllConfigurationItems ordering:
//  1. Site Link first
//  2. declared fields in declaration order (embedded structs recursed)
//  3. dynamic items (sorted by id for determinism)
//  4. Tags last
//
// cfg is a pointer to a struct embedding ConfigData (value or pointer) plus
// indexer-specific item fields, e.g.:
//
//	type myConfig struct {
//	    configdata.ConfigData
//	    Username *configdata.StringItem
//	    Password *configdata.PasswordItem
//	}
func Collect(cfg any) []Item {
	var all []Item
	collectInto(reflect.ValueOf(cfg), &all)

	byID := make(map[string]Item, len(all))
	for _, item := range all {
		if item == nil {
			continue
		}
		byID[item.ID()] = item
	}
	siteLink, tags := byID["sitelink"], byID["tags"]

	out := make([]Item, 0, len(all)+2)
	if siteLink != nil {
		out = append(out, siteLink)
	}
	// declared fields keep struct declaration order; hidden base fields
	// (cookieheader/lasterror) ride along and are filtered at serialization.
	seen := map[string]bool{}
	if siteLink != nil {
		seen["sitelink"] = true
	}
	if tags != nil {
		seen["tags"] = true
	}
	for _, item := range all {
		if item == nil || seen[item.ID()] {
			continue
		}
		seen[item.ID()] = true
		out = append(out, item)
	}

	// dynamics keep insertion order (upstream List<KeyValuePair> semantics;
	// cardigann settings display in definition order).
	if cfgCD := findConfigData(reflect.ValueOf(cfg)); cfgCD != nil {
		for _, e := range cfgCD.dynamics {
			out = append(out, e.item)
		}
	}
	if tags != nil {
		out = append(out, tags)
	}
	return out
}

// collectInto walks struct fields in declaration order, appending items.
func collectInto(v reflect.Value, out *[]Item) {
	if !v.IsValid() {
		return
	}
	if v.Kind() == reflect.Pointer {
		if v.IsNil() {
			return
		}
		v = v.Elem()
	}
	if v.Kind() != reflect.Struct {
		return
	}
	t := v.Type()
	for i := 0; i < v.NumField(); i++ {
		field := v.Field(i)
		ft := t.Field(i)
		if ft.Anonymous {
			collectInto(field, out)
			continue
		}
		if ft.Type == itemType || (ft.Type.Kind() == reflect.Pointer && ft.Type.Implements(itemType)) {
			if field.Kind() == reflect.Pointer && field.IsNil() {
				continue
			}
			if item, ok := field.Interface().(Item); ok {
				*out = append(*out, item)
			}
		}
	}
}

// findConfigData locates an embedded ConfigData (value or pointer).
func findConfigData(v reflect.Value) *ConfigData {
	if !v.IsValid() {
		return nil
	}
	if v.Kind() == reflect.Pointer {
		if v.IsNil() {
			return nil
		}
		v = v.Elem()
	}
	if v.Kind() != reflect.Struct {
		return nil
	}
	cdType := reflect.TypeOf(ConfigData{})
	cdPtrType := reflect.TypeOf(&ConfigData{})
	t := v.Type()
	for i := 0; i < v.NumField(); i++ {
		ft := t.Field(i)
		if !ft.Anonymous {
			continue
		}
		if ft.Type == cdType {
			return v.Field(i).Addr().Interface().(*ConfigData)
		}
		if ft.Type == cdPtrType {
			if p := v.Field(i); !p.IsNil() {
				return p.Interface().(*ConfigData)
			}
		}
	}
	return nil
}

// Find returns the embedded ConfigData of cfg (value or pointer embed),
// or nil when cfg does not embed one.
func Find(cfg any) *ConfigData { return findConfigData(reflect.ValueOf(cfg)) }

// ItemsJSON renders the display form (backend-api.md §2.2): a JSON array of
// item objects, hidden items excluded.
func ItemsJSON(cfg any) ([]map[string]any, error) {
	items := Collect(cfg)
	out := make([]map[string]any, 0, len(items))
	for _, item := range items {
		if m := item.ToJSON(true); m != nil {
			out = append(out, m)
		}
	}
	return out, nil
}

// MarshalItems serializes items (forDisplay=false shape) to JSON bytes —
// the upstream SaveConfig payload (indexer_config.config_json).
func MarshalItems(cfg any) ([]byte, error) {
	items := Collect(cfg)
	arr := make([]map[string]any, 0, len(items))
	for _, item := range items {
		if m := item.ToJSON(false); m != nil {
			arr = append(arr, m)
		}
	}
	return json.Marshal(arr)
}

// UnmarshalItems loads a saved configuration JSON array (forDisplay=false
// shape) into the matching items, mirroring upstream
// LoadConfigDataValuesFromJson (values matched by item id; missing ids are
// skipped; items that cannot be saved to file are ignored).
func UnmarshalItems(cfg any, raw []byte) error {
	if len(raw) == 0 {
		return nil
	}
	var arr []map[string]any
	if err := json.Unmarshal(raw, &arr); err != nil {
		return err
	}
	items := Collect(cfg)
	for _, item := range items {
		if !item.CanBeSavedToFile() {
			continue
		}
		for _, data := range arr {
			id, _ := data["id"].(string)
			if id == item.ID() {
				item.FromJSON(data)
				break
			}
		}
	}
	return nil
}
