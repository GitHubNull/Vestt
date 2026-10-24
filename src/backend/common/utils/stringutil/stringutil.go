// Package stringutil ports the upstream StringUtil helpers needed by the
// proxy endpoints (SPEC-07).
//
// Ported from: Jackett.Common/Utils/StringUtil.cs.
package stringutil

import "strings"

// invalidFileNameChars mirrors Path.GetInvalidFileNameChars() on .NET Unix
// (Vestt's reference platform): NUL and '/'. Windows builds of upstream get
// a larger set; the proxy endpoints sanitise again for defence in depth.
var invalidFileNameChars = []rune{'\x00', '/'}

// MakeValidFileName mirrors upstream StringUtil.MakeValidFileName: replaces
// characters not allowed in file names. When fancy is true, quotes and
// slashes map to the Unicode substitutes used upstream; the proxy endpoints
// pass fancy=false. An empty result becomes "_". The input string is
// returned unchanged when no replacement was needed.
func MakeValidFileName(text string, replacement rune, fancy bool) string {
	var sb strings.Builder
	sb.Grow(len(text))
	changed := false
	for _, c := range text {
		invalid := false
		for _, iv := range invalidFileNameChars {
			if c == iv {
				invalid = true
				break
			}
		}
		if !invalid {
			sb.WriteRune(c)
			continue
		}
		changed = true
		repl := replacement
		if fancy {
			switch c {
			case '"':
				repl = '”' // U+201D right double quotation mark
			case '\'':
				repl = '’' // U+2019 right single quotation mark
			case '/':
				repl = '⁄' // U+2044 fraction slash
			}
		}
		if repl != 0 {
			sb.WriteRune(repl)
		}
	}
	if sb.Len() == 0 {
		return "_"
	}
	if !changed {
		return text
	}
	return sb.String()
}
