package models

import (
	"strings"
)

// xmlHeader is the declaration emitted by every Torznab document
// (upstream XDocument.ToString prefixes the declaration).
const xmlHeader = `<?xml version="1.0" encoding="UTF-8"?>` + "\n"

// writeAttr appends s escaped for an XML attribute value, equivalent to
// XAttribute escaping: & < > and " (apostrophes pass through).
func writeAttr(b *strings.Builder, s string) {
	for _, r := range s {
		switch r {
		case '&':
			b.WriteString("&amp;")
		case '<':
			b.WriteString("&lt;")
		case '>':
			b.WriteString("&gt;")
		case '"':
			b.WriteString("&quot;")
		default:
			b.WriteRune(r)
		}
	}
}

// writeText appends s escaped for XML character data, equivalent to
// XElement escaping: & < > only.
func writeText(b *strings.Builder, s string) {
	for _, r := range s {
		switch r {
		case '&':
			b.WriteString("&amp;")
		case '<':
			b.WriteString("&lt;")
		case '>':
			b.WriteString("&gt;")
		default:
			b.WriteRune(r)
		}
	}
}
