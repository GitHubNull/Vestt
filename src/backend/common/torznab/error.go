package torznab

import (
	"io"
	"strconv"
	"strings"
)

// ErrorXML renders the upstream CreateErrorXML document:
//
//	<?xml version="1.0" encoding="UTF-8"?>
//	<error code="100" description="Invalid API Key" />
//
// Ported from: Jackett.Server/Controllers/ResultsController.cs.
func ErrorXML(w io.Writer, code int, description string) {
	var b strings.Builder
	b.Grow(256)
	b.WriteString(xmlHeader)
	b.WriteString(`<error code="`)
	b.WriteString(strconv.Itoa(code))
	b.WriteString(`" description="`)
	writeAttr(&b, description)
	b.WriteString(`" />`)
	_, _ = io.WriteString(w, b.String())
}

// ErrorXMLString is the string form of ErrorXML (convenience for handlers).
func ErrorXMLString(code int, description string) string {
	var b strings.Builder
	ErrorXML(&b, code, description)
	return b.String()
}
