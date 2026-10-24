package torznab

import (
	"io"

	"vestt/common/models"
)

// WriteCaps renders the <caps> document for capabilities (thin wrapper over
// models.Capabilities.XML, SPEC-03 §3.2.2).
func WriteCaps(w io.Writer, caps *models.Capabilities) {
	_, _ = io.WriteString(w, caps.XML())
}

// CapsXML is the string form of WriteCaps.
func CapsXML(caps *models.Capabilities) string {
	capsValue := caps
	if capsValue == nil {
		capsValue = models.NewCapabilities()
	}
	return capsValue.XML()
}
