package indexer

// ConfigItem is the wire form of one configuration entry
// (backend-api.md §2.3, upstream Jackett.Common/Models/DTO/Config.cs).
//
// Ported from: Jackett.Common/Models/DTO/Config.cs.
type ConfigItem struct {
	ID     string   `json:"id"`
	Value  string   `json:"value,omitempty"`
	Values []string `json:"values,omitempty"` // array data (checkboxes)
	Cookie string   `json:"cookie,omitempty"` // cookie alternative login channel
}
