// Package torznab implements Torznab XML serialization (caps / rss / error).
//
// Ported from: Jackett.Common/Models/ResultPage.cs (ToXml),
// ChannelInfo.cs, Jackett.Server/Controllers/ResultsController.cs
// (CreateErrorXML).
package torznab

import (
	"io"
	"net/url"
	"strconv"
	"strings"
	"time"

	"vestt/common/models"
)

const (
	// AtomNs is the Atom namespace declared on the rss root (upstream).
	AtomNs = "http://www.w3.org/2005/Atom"
	// TorznabNs is the torznab namespace for result attributes.
	TorznabNs = "http://torznab.com/schemas/2015/feed"
)

const xmlHeader = `<?xml version="1.0" encoding="UTF-8"?>` + "\n"

// removeInvalidXMLChars mirrors upstream RemoveInvalidXMLChars: drops
// characters that cannot appear in XML 1.0 (control chars, BOM, non-chars,
// lone surrogates). The upstream regex uses look-around, which Go's RE2
// lacks; an explicit rune scan implements the same filter. Valid UTF-8
// cannot encode surrogates, so those never survive Go string decoding.
func removeInvalidXMLChars(text string) string {
	if strings.IndexFunc(text, isInvalidXMLRune) < 0 {
		return text
	}
	var b strings.Builder
	b.Grow(len(text))
	for _, r := range text {
		if isInvalidXMLRune(r) {
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

func isInvalidXMLRune(r rune) bool {
	switch {
	case r >= 0x00 && r <= 0x08,
		r == 0x0B, r == 0x0C,
		r >= 0x0E && r <= 0x1F,
		r >= 0x7F && r <= 0x9F,
		r == 0xFEFF, r == 0xFFFE, r == 0xFFFF:
		return true
	}
	return false
}

// ChannelInfo mirrors upstream ChannelInfo (defaults: en-US / search).
type ChannelInfo struct {
	Title       string
	Description string
	Link        *url.URL
	Language    string
	Category    string
}

// NewChannelInfo applies upstream constructor defaults.
func NewChannelInfo() ChannelInfo {
	return ChannelInfo{Language: "en-US", Category: "search"}
}

// xmlDateFormat mirrors upstream: "ddd, dd MMM yyyy HH:mm:ss zzz" with the
// colon removed from the offset (e.g. "+0000").
func xmlDateFormat(t time.Time) string {
	return t.Format("Mon, 02 Jan 2006 15:04:05 -0700")
}

// WriteRSS renders the full RSS document for a result page. selfAtom is the
// feed URL embedded in the atom:self link (upstream ToXml(selfAtom)).
//
// Element order follows upstream ResultPage.ToXml exactly.
func WriteRSS(w io.Writer, ch ChannelInfo, releases []*models.ReleaseInfo, selfAtom *url.URL) {
	var b strings.Builder
	b.Grow(4096)
	b.WriteString(xmlHeader)
	b.WriteString(`<rss version="2.0" xmlns:atom="`)
	b.WriteString(AtomNs)
	b.WriteString(`" xmlns:torznab="`)
	b.WriteString(TorznabNs)
	b.WriteString(`">`)
	b.WriteString("<channel>")
	// atom:self link
	b.WriteString(`<atom:link href="`)
	writeAttr(&b, selfAtom.String())
	b.WriteString(`" rel="self" type="application/rss+xml" />`)
	writeTextEl(&b, "title", ch.Title)
	writeTextEl(&b, "description", ch.Description)
	b.WriteString("<link>")
	if ch.Link != nil {
		writeText(&b, ch.Link.String())
	}
	b.WriteString("</link>")
	writeTextEl(&b, "language", ch.Language)
	writeTextEl(&b, "category", ch.Category)

	for _, r := range releases {
		writeItem(&b, r)
	}

	b.WriteString("</channel></rss>")
	_, _ = io.WriteString(w, b.String())
}

func writeTextEl(b *strings.Builder, tag, text string) {
	b.WriteString("<")
	b.WriteString(tag)
	b.WriteString(">")
	writeText(b, text)
	b.WriteString("</")
	b.WriteString(tag)
	b.WriteString(">")
}

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

// linkOrMagnet mirrors `r.Link?.AbsoluteUri ?? r.MagnetUri?.AbsoluteUri ?? ""`.
func linkOrMagnet(r *models.ReleaseInfo) string {
	if r.Link != nil {
		return r.Link.String()
	}
	if r.MagnetUri != nil {
		return r.MagnetUri.String()
	}
	return ""
}

func writeItem(b *strings.Builder, r *models.ReleaseInfo) {
	b.WriteString("<item>")
	writeTextEl(b, "title", removeInvalidXMLChars(r.Title))
	b.WriteString("<guid>")
	if r.Guid != nil {
		writeText(b, r.Guid.String())
	}
	b.WriteString("</guid>")
	// jackettindexer + type (origin identity)
	b.WriteString(`<jackettindexer id="`)
	writeAttr(b, r.Origin.ID)
	b.WriteString(`">`)
	writeText(b, r.Origin.Name)
	b.WriteString("</jackettindexer>")
	writeTextEl(b, "type", r.Origin.Type)
	if r.Details != nil {
		b.WriteString("<comments>")
		writeText(b, r.Details.String())
		b.WriteString("</comments>")
	}
	if r.PublishDate.IsZero() {
		writeTextEl(b, "pubDate", xmlDateFormat(time.Now()))
	} else {
		writeTextEl(b, "pubDate", xmlDateFormat(r.PublishDate))
	}
	if r.Size != nil {
		writeTextEl(b, "size", strconv.FormatInt(*r.Size, 10))
	}
	if r.Files != nil {
		writeTextEl(b, "files", strconv.FormatInt(*r.Files, 10))
	}
	if r.Grabs != nil {
		writeTextEl(b, "grabs", strconv.FormatInt(*r.Grabs, 10))
	}
	writeTextEl(b, "description", removeInvalidXMLChars(r.Description))
	writeTextEl(b, "link", linkOrMagnet(r))
	if r.Category != nil {
		for _, c := range r.Category {
			writeTextEl(b, "category", strconv.Itoa(c))
		}
	}
	// enclosure (always present, url = link|magnet)
	b.WriteString(`<enclosure url="`)
	writeAttr(b, linkOrMagnet(r))
	b.WriteString(`"`)
	if r.Size != nil {
		b.WriteString(` length="`)
		b.WriteString(strconv.FormatInt(*r.Size, 10))
		b.WriteString(`"`)
	}
	b.WriteString(` type="application/x-bittorrent" />`)

	// torznab:attr entries (order follows upstream ToXml)
	if r.Category != nil {
		for _, c := range r.Category {
			writeTorznabAttr(b, "category", strconv.Itoa(c))
		}
	}
	writeTorznabAttrPtrInt64(b, "rageid", r.RageID)
	writeTorznabAttrPtrInt64(b, "tvdbid", r.TVDBId)
	if r.Imdb != nil {
		writeTorznabAttr(b, "imdb", imdbPad7(*r.Imdb))
		writeTorznabAttr(b, "imdbid", "tt"+imdbPad7(*r.Imdb))
	}
	writeTorznabAttrPtrInt64(b, "tmdbid", r.TMDb)
	writeTorznabAttrPtrInt64(b, "tvmazeid", r.TVMazeId)
	writeTorznabAttrPtrInt64(b, "traktid", r.TraktId)
	writeTorznabAttrPtrInt64(b, "doubanid", r.DoubanId)
	if r.Genres != nil {
		writeTorznabAttr(b, "genre", strings.Join(r.Genres, ", "))
	}
	if r.Languages != nil {
		for _, l := range r.Languages {
			writeTorznabAttr(b, "language", l)
		}
	}
	if r.Subs != nil {
		for _, s := range r.Subs {
			writeTorznabAttr(b, "subs", s)
		}
	}
	writeTorznabAttrPtrInt64(b, "year", r.Year)
	writeTorznabAttr(b, "author", removeInvalidXMLChars(r.Author))
	writeTorznabAttr(b, "booktitle", removeInvalidXMLChars(r.BookTitle))
	writeTorznabAttr(b, "publisher", removeInvalidXMLChars(r.Publisher))
	writeTorznabAttr(b, "artist", removeInvalidXMLChars(r.Artist))
	writeTorznabAttr(b, "album", removeInvalidXMLChars(r.Album))
	writeTorznabAttr(b, "label", removeInvalidXMLChars(r.Label))
	writeTorznabAttr(b, "track", removeInvalidXMLChars(r.Track))
	writeTorznabAttrPtrInt64(b, "seeders", r.Seeders)
	writeTorznabAttrPtrInt64(b, "peers", r.Peers)
	if r.Poster != nil {
		writeTorznabAttr(b, "coverurl", r.Poster.String())
	}
	writeTorznabAttr(b, "infohash", removeInvalidXMLChars(r.InfoHash))
	if r.MagnetUri != nil {
		writeTorznabAttr(b, "magneturl", r.MagnetUri.String())
	}
	writeTorznabAttrPtrFloat(b, "minimumratio", r.MinimumRatio)
	writeTorznabAttrPtrInt64(b, "minimumseedtime", r.MinimumSeedTime)
	writeTorznabAttrPtrFloat(b, "downloadvolumefactor", r.DownloadVolumeFactor)
	writeTorznabAttrPtrFloat(b, "uploadvolumefactor", r.UploadVolumeFactor)

	b.WriteString("</item>")
}

// imdbPad7 mirrors Imdb.ToString("D7").
func imdbPad7(v int64) string {
	s := strconv.FormatInt(v, 10)
	for len(s) < 7 {
		s = "0" + s
	}
	return s
}

func writeTorznabAttr(b *strings.Builder, name, value string) {
	b.WriteString(`<torznab:attr name="`)
	b.WriteString(name)
	b.WriteString(`" value="`)
	writeAttr(b, value)
	b.WriteString(`" />`)
}

func writeTorznabAttrPtrInt64(b *strings.Builder, name string, v *int64) {
	if v == nil {
		return
	}
	writeTorznabAttr(b, name, strconv.FormatInt(*v, 10))
}

func writeTorznabAttrPtrFloat(b *strings.Builder, name string, v *float64) {
	if v == nil {
		return
	}
	// C# double.ToString() general format
	writeTorznabAttr(b, name, strconv.FormatFloat(*v, 'g', -1, 64))
}
