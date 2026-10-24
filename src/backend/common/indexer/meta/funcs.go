package meta

import (
	"strings"

	"vestt/common/indexer"
)

// ParseFilter ports the single-component subset of upstream
// FilterFunc.TryParse used for dynamic filter indexers: "type:<value>",
// "tag:<value>" and "lang:<prefix>". The predefined type filters
// ("public"/"private"/"semi-private") are shorthand for the corresponding
// type: components. The full upstream expression grammar (test:/status:
// components, boolean combinators) is outside SPEC-07 scope — see the
// SPEC-07 澄清记录.
//
// Ported from: Jackett.Common/Utils/FilterFunc.cs (Tag/Language/Type).
func ParseFilter(source string) (FilterFunc, bool) {
	name, args, found := strings.Cut(source, ":")
	if !found {
		// predefined shorthand
		switch strings.ToLower(source) {
		case "public", "private", "semi-private":
			return typeFilter(source), true
		}
		return nil, false
	}
	args = strings.ToLower(args)
	switch strings.ToLower(name) {
	case "tag":
		return func(idx indexer.Indexer) bool {
			for _, t := range idx.Tags() {
				if t == args {
					return true
				}
			}
			return false
		}, true
	case "lang":
		return func(idx indexer.Indexer) bool {
			return strings.HasPrefix(strings.ToLower(idx.Language()), args)
		}, true
	case "type":
		return typeFilter(args), true
	}
	return nil, false
}

// typeFilter mirrors FilterFunc.Type: case-insensitive indexer.Type match,
// restricted to configured indexers (FilterFunc.IsValid).
func typeFilter(value string) FilterFunc {
	return func(idx indexer.Indexer) bool {
		return strings.EqualFold(idx.Type(), value)
	}
}
