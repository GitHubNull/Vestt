package engine

import (
	"testing"
)

// testCardigann builds a minimal engine instance for selector-helper tests
// (no network).
func testCardigann(t *testing.T) *Cardigann {
	t.Helper()
	x, err := NewCardigann(&IndexerDefinition{
		ID:    "unittest",
		Name:  "Unit Test",
		Links: []string{"https://example.test/"},
		Caps: &CapsBlock{
			Categories: StringMap{{Key: "1", Value: "Movies"}},
			Modes:      StringListMap{{Key: "search", Values: []string{"q"}}},
		},
		Search: &SearchBlock{Path: "/search"},
	}, discardLogger())
	if err != nil {
		t.Fatalf("NewCardigann: %v", err)
	}
	return x
}
