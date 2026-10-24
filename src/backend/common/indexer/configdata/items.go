// Package configdata implements the dynamic configuration form model,
// byte-compatible with upstream Jackett ConfigurationData
// (src/Jackett.Common/Models/IndexerConfig/ConfigurationData.cs).
package configdata

import (
	"encoding/base64"
	"fmt"
	"sort"
	"strings"
)

// PasswordReplacement is the sentinel a password item displays instead of
// the real value (upstream PASSWORD_REPLACEMENT); submitting it back leaves
// the stored value untouched.
const PasswordReplacement = "|||%%PREVJACKPASSWD%%|||"

// Item is one configuration form item (upstream ConfigurationItem).
// ToJSON returns the display/save object; it returns nil for items that
// must not appear in the requested serialization.
type Item interface {
	ID() string
	Name() string
	ItemType() string
	CanBeShownToUser() bool
	CanBeSavedToFile() bool
	ToJSON(forDisplay bool) map[string]any
	FromJSON(data map[string]any)
	SetValue(v string)
}

// itemBase carries the upstream base-class fields.
type itemBase struct {
	name             string
	itemType         string
	canBeShownToUser bool
	canBeSavedToFile bool
}

func newItemBase(name, itemType string, shown, saved bool) itemBase {
	return itemBase{name: name, itemType: itemType, canBeShownToUser: shown, canBeSavedToFile: saved}
}

// idOf mirrors upstream ConfigurationItem.ID:
// Name.Replace(" ", "").ToLower().
func idOf(name string) string { return strings.ReplaceAll(strings.ToLower(name), " ", "") }

func (b *itemBase) ID() string             { return idOf(b.name) }
func (b *itemBase) Name() string           { return b.name }
func (b *itemBase) ItemType() string       { return b.itemType }
func (b *itemBase) CanBeShownToUser() bool { return b.canBeShownToUser }
func (b *itemBase) CanBeSavedToFile() bool { return b.canBeSavedToFile }

func (b *itemBase) createJObject() map[string]any {
	return map[string]any{"id": b.ID(), "type": strings.ToLower(b.itemType), "name": b.name}
}

func toJSONOrNil(b *itemBase, forDisplay bool, m map[string]any) map[string]any {
	if forDisplay && !b.canBeShownToUser {
		return nil
	}
	if !forDisplay && !b.canBeSavedToFile {
		return nil
	}
	return m
}

// StringItem is upstream StringConfigurationItem (inputstring). The legacy
// maybe-password behaviour (name == "password" masks the value) is kept for
// indexers that have not migrated to PasswordItem.
type StringItem struct {
	itemBase
	Value string
}

func NewStringItem(name string) *StringItem {
	return &StringItem{itemBase: newItemBase(name, "inputstring", true, true)}
}

func (it *StringItem) ToJSON(forDisplay bool) map[string]any {
	j := it.createJObject()
	value := it.Value
	// upstream ConfigurationItemMaybePassword.ToJson
	if strings.EqualFold(it.name, "password") && value != "" {
		value = PasswordReplacement
	}
	j["value"] = value
	return toJSONOrNil(&it.itemBase, forDisplay, j)
}

func (it *StringItem) FromJSON(data map[string]any) {
	v, _ := data["value"].(string)
	if strings.EqualFold(it.name, "password") && v == PasswordReplacement {
		return
	}
	it.Value = v
}

func (it *StringItem) SetValue(v string) { it.Value = v }

// PasswordItem is upstream PasswordConfigurationItem.
type PasswordItem struct {
	itemBase
	Value string
}

func NewPasswordItem(name string) *PasswordItem {
	return &PasswordItem{itemBase: newItemBase(name, "password", true, true)}
}

func (it *PasswordItem) ToJSON(forDisplay bool) map[string]any {
	j := it.createJObject()
	password := it.Value
	if password == "" {
		password = ""
	} else if forDisplay {
		password = PasswordReplacement
	}
	j["value"] = password
	return toJSONOrNil(&it.itemBase, forDisplay, j)
}

func (it *PasswordItem) FromJSON(data map[string]any) {
	v, _ := data["value"].(string)
	if v == PasswordReplacement {
		return
	}
	it.Value = v
}

func (it *PasswordItem) SetValue(v string) { it.Value = v }

// HiddenItem is upstream HiddenStringConfigurationItem (hiddendata): saved
// but never shown in the setup form.
type HiddenItem struct {
	itemBase
	Value string
}

func NewHiddenItem(name string) *HiddenItem {
	return &HiddenItem{itemBase: newItemBase(name, "hiddendata", false, true)}
}

func (it *HiddenItem) ToJSON(forDisplay bool) map[string]any {
	j := it.createJObject()
	j["value"] = it.Value
	return toJSONOrNil(&it.itemBase, forDisplay, j)
}

func (it *HiddenItem) FromJSON(data map[string]any) {
	v, _ := data["value"].(string)
	it.Value = v
}

func (it *HiddenItem) SetValue(v string) { it.Value = v }

// BoolItem is upstream BoolConfigurationItem (inputbool).
type BoolItem struct {
	itemBase
	Value bool
}

func NewBoolItem(name string) *BoolItem {
	return &BoolItem{itemBase: newItemBase(name, "inputbool", true, true)}
}

func (it *BoolItem) ToJSON(forDisplay bool) map[string]any {
	j := it.createJObject()
	j["value"] = it.Value
	return toJSONOrNil(&it.itemBase, forDisplay, j)
}

func (it *BoolItem) FromJSON(data map[string]any) {
	switch v := data["value"].(type) {
	case bool:
		it.Value = v
	case string:
		// posted ConfigItem values are strings; Newtonsoft Value<bool>()
		// converts them upstream
		it.Value = v == "true" || v == "1"
	}
}

func (it *BoolItem) SetValue(v string) { it.Value = v == "true" || v == "1" }

// InfoItem is upstream DisplayInfoConfigurationItem (displayinfo): shown
// but never saved.
type InfoItem struct {
	itemBase
	Value string
}

func NewInfoItem(name, value string) *InfoItem {
	return &InfoItem{itemBase: newItemBase(name, "displayinfo", true, false), Value: value}
}

func (it *InfoItem) ToJSON(forDisplay bool) map[string]any {
	j := it.createJObject()
	j["value"] = it.Value
	return toJSONOrNil(&it.itemBase, forDisplay, j)
}

func (it *InfoItem) FromJSON(_ map[string]any) {}

func (it *InfoItem) SetValue(v string) { it.Value = v }

// ImageItem is upstream DisplayImageConfigurationItem (displayimage):
// captcha images; serialized as a JPEG data URI, never saved.
type ImageItem struct {
	itemBase
	Value []byte
}

func NewImageItem(name string) *ImageItem {
	return &ImageItem{itemBase: newItemBase(name, "displayimage", true, false)}
}

func (it *ImageItem) ToJSON(forDisplay bool) map[string]any {
	j := it.createJObject()
	j["value"] = "data:image/jpeg;base64," + base64.StdEncoding.EncodeToString(it.Value)
	return toJSONOrNil(&it.itemBase, forDisplay, j)
}

func (it *ImageItem) FromJSON(_ map[string]any) {}

func (it *ImageItem) SetValue(_ string) {}

// Option is one select option, preserving upstream Dictionary insertion
// order in the serialized JSON object.
type Option struct {
	Value string
	Label string
}

// optionsObject renders options as a JSON object in declaration order.
func optionsObject(options []Option) map[string]any {
	m := make(map[string]any, len(options))
	for _, o := range options {
		m[o.Value] = o.Label
	}
	return m
}

// SelectItem is upstream SingleSelectConfigurationItem (inputselect).
type SelectItem struct {
	itemBase
	Value   string
	Options []Option
}

func NewSelectItem(name string, options []Option) *SelectItem {
	return &SelectItem{itemBase: newItemBase(name, "inputselect", true, true), Options: options}
}

func (it *SelectItem) ToJSON(forDisplay bool) map[string]any {
	j := it.createJObject()
	j["value"] = it.Value
	j["options"] = optionsObject(it.Options)
	return toJSONOrNil(&it.itemBase, forDisplay, j)
}

func (it *SelectItem) FromJSON(data map[string]any) {
	it.Value, _ = data["value"].(string)
}

func (it *SelectItem) SetValue(v string) { it.Value = v }

// MultiSelectItem is upstream MultiSelectConfigurationItem (inputcheckbox).
type MultiSelectItem struct {
	itemBase
	Values  []string
	Options []Option
}

func NewMultiSelectItem(name string, options []Option) *MultiSelectItem {
	return &MultiSelectItem{itemBase: newItemBase(name, "inputcheckbox", true, true), Values: []string{}, Options: options}
}

func (it *MultiSelectItem) ToJSON(forDisplay bool) map[string]any {
	j := it.createJObject()
	values := it.Values
	if values == nil {
		values = []string{}
	}
	j["values"] = values
	j["options"] = optionsObject(it.Options)
	return toJSONOrNil(&it.itemBase, forDisplay, j)
}

func (it *MultiSelectItem) FromJSON(data map[string]any) {
	raw, ok := data["values"].([]any)
	if !ok {
		return
	}
	it.Values = make([]string, 0, len(raw))
	for _, v := range raw {
		if s, ok := v.(string); ok {
			it.Values = append(it.Values, s)
		}
	}
}

func (it *MultiSelectItem) SetValue(v string) { it.Values = []string{v} }

// TagsItem is upstream TagsConfigurationItem (inputtags). Values are a set;
// the wire value is the separator-joined lower-case list.
type TagsItem struct {
	itemBase
	Values     map[string]struct{}
	Pattern    string
	Delimiters string
	Separator  rune
	Whitelist  map[string]struct{}
	Blacklist  map[string]struct{}
}

// NewTagsItem mirrors the upstream constructor: a non-empty charSet derives
// Pattern = ^[charSet]+$ and Delimiters = [^charSet]+.
func NewTagsItem(name, charSet string) *TagsItem {
	it := &TagsItem{
		itemBase:  newItemBase(name, "inputtags", true, true),
		Values:    map[string]struct{}{},
		Separator: ',',
		Whitelist: map[string]struct{}{},
		Blacklist: map[string]struct{}{},
	}
	if strings.TrimSpace(charSet) != "" {
		it.Pattern = fmt.Sprintf("^[%s]+$", charSet)
		it.Delimiters = fmt.Sprintf("[^%s]+", charSet)
	}
	return it
}

// SortedValues returns the tag set in sorted order.
func (it *TagsItem) SortedValues() []string {
	out := make([]string, 0, len(it.Values))
	for v := range it.Values {
		out = append(out, v)
	}
	sort.Strings(out)
	return out
}

func (it *TagsItem) ToJSON(forDisplay bool) map[string]any {
	j := it.createJObject()
	sep := string(it.Separator)
	j["value"] = strings.Join(it.SortedValues(), sep)
	if forDisplay {
		j["separator"] = sep
		if it.Delimiters != "" {
			j["delimiters"] = it.Delimiters
		}
		if it.Pattern != "" {
			j["pattern"] = it.Pattern
		}
		if len(it.Whitelist) > 0 {
			j["whitelist"] = strings.Join(sortedSet(it.Whitelist), sep)
		}
		if len(it.Blacklist) > 0 {
			j["blacklist"] = strings.Join(sortedSet(it.Blacklist), sep)
		}
	}
	return toJSONOrNil(&it.itemBase, forDisplay, j)
}

func sortedSet(s map[string]struct{}) []string {
	out := make([]string, 0, len(s))
	for v := range s {
		out = append(out, v)
	}
	sort.Strings(out)
	return out
}

// FromJSON mirrors upstream: split by Delimiters (or Separator+), trim,
// lowercase, then apply pattern/whitelist/blacklist filtering.
func (it *TagsItem) FromJSON(data map[string]any) {
	value, _ := data["value"].(string)
	if value == "" {
		return
	}
	pattern := compilePattern(it.Pattern)
	var parts []string
	if it.Delimiters != "" {
		parts = splitByPattern(value, it.Delimiters)
	} else {
		parts = strings.FieldsFunc(value, func(r rune) bool { return r == it.Separator })
	}
	for _, p := range parts {
		tag := strings.ToLower(strings.TrimSpace(p))
		if tag == "" {
			continue
		}
		if pattern != nil && !pattern.MatchString(tag) {
			if _, ok := it.Whitelist[tag]; !ok {
				continue
			}
		}
		if _, blocked := it.Blacklist[tag]; blocked {
			continue
		}
		it.Values[tag] = struct{}{}
	}
}

func (it *TagsItem) SetValue(v string) {
	it.Values = map[string]struct{}{}
	it.FromJSON(map[string]any{"value": v})
}

// compilePattern compiles an upstream-style character-class pattern
// ("^[A-Za-z0-9\\-\\._~]+$") via Go regexp (RE2-compatible subset).
func compilePattern(pattern string) interface{ MatchString(string) bool } {
	if pattern == "" {
		return nil
	}
	re, err := regexpCompile(pattern)
	if err != nil {
		return nil
	}
	return re
}
