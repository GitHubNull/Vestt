package torznab

import (
	"io"
	"strings"

	"vestt/common/models"
)

// IndexerEntry is one indexer's identity + capabilities for the t=indexers
// document (SPEC-07 assembles these from configured indexers).
type IndexerEntry struct {
	ID          string
	Configured  bool
	Title       string
	Description string
	Link        string
	Language    string
	Type        string
	Caps        *models.Capabilities
}

// WriteIndexers renders the t=indexers document:
//
//	<?xml version="1.0" encoding="UTF-8"?>
//	<indexers>
//	  <indexer id="..." configured="true">
//	    <title>...</title> ... <caps>...</caps>
//	  </indexer>
//	</indexers>
func WriteIndexers(w io.Writer, indexers []IndexerEntry) {
	var b strings.Builder
	b.Grow(4096)
	b.WriteString(xmlHeader)
	b.WriteString("<indexers>")
	for _, idx := range indexers {
		b.WriteString(`<indexer id="`)
		writeAttr(&b, idx.ID)
		b.WriteString(`" configured="`)
		if idx.Configured {
			b.WriteString("true")
		} else {
			b.WriteString("false")
		}
		b.WriteString(`">`)
		writeTextEl(&b, "title", idx.Title)
		writeTextEl(&b, "description", idx.Description)
		writeTextEl(&b, "link", idx.Link)
		writeTextEl(&b, "language", idx.Language)
		writeTextEl(&b, "type", idx.Type)
		if idx.Caps != nil {
			// embed the full caps document without its XML declaration
			caps := idx.Caps.XML()
			b.WriteString(strings.TrimPrefix(caps, xmlHeader))
		}
		b.WriteString("</indexer>")
	}
	b.WriteString("</indexers>")
	_, _ = io.WriteString(w, b.String())
}
