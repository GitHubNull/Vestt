// Field → ReleaseInfo mapping (SPEC-06 §3.4).
//
// Ported from: Jackett.Common/Indexers/Definitions/CardigannIndexer.cs
// ParseFields (2214-2433) and OnParseError-adjacent semantics.
package engine

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"vestt/common/models"
	"vestt/common/utils/dateutil"
	"vestt/common/utils/parseutil"
)

// optionalFields mirrors CardigannIndexer.OptionalFields (line 52).
var optionalFields = []string{"imdb", "imdbid", "tmdbid", "rageid", "tvdbid", "tvmazeid", "traktid", "doubanid", "poster", "genre", "description"}

func isOptionalField(key string) bool {
	for _, f := range optionalFields {
		if f == key {
			return true
		}
	}
	return false
}

// parseFields ports ParseFields: applies one parsed field value to the
// release and returns the value stored in the ".Result.<name>" variable.
// The date case propagates FromUnknown failures: upstream has no try/catch
// here, so the HTML per-row catch drops the row and the JSON branch (no
// catch) fails the whole query.
func (x *Cardigann) parseFields(value, fieldName string, release *models.ReleaseInfo, modifiers []string, searchURL string) (string, error) {
	hasModifier := func(m string) bool {
		for _, mod := range modifiers {
			if mod == m {
				return true
			}
		}
		return false
	}
	resolve := func(v string) string {
		out, err := x.resolvePath(v, mustParse(searchURL))
		if err != nil {
			return v
		}
		return out
	}

	switch fieldName {
	case "download":
		if value == "" {
			release.Link = nil
			return "", nil
		}
		if strings.HasPrefix(value, "magnet:") {
			if u, err := url.Parse(value); err == nil {
				release.MagnetUri = u
				value = release.MagnetUri.String()
			}
		} else {
			release.Link = mustParse(resolve(value))
			value = release.Link.String()
		}
	case "magnet":
		if u, err := url.Parse(value); err == nil {
			release.MagnetUri = u
			value = release.MagnetUri.String()
		}
	case "infohash":
		release.InfoHash = value
	case "details":
		release.Details = mustParse(resolve(value))
		value = release.Details.String()
	case "title":
		if hasModifier("append") {
			release.Title += value
		} else {
			release.Title = value
		}
		value = release.Title
	case "description":
		if hasModifier("append") {
			release.Description += value
		} else {
			release.Description = value
		}
		value = release.Description
	case "category":
		if hasModifier("noappend") {
			x.log().Warn(fmt.Sprintf("CardigannIndexer (%s): The \"noappend\" modifier is deprecated. Please switch to \"default\".", x.ID()))
		}
		cats := x.Caps.Categories.MapTrackerCatToNewznab(value)
		if len(cats) > 0 {
			if release.Category == nil || hasModifier("noappend") {
				release.Category = cats
			} else {
				release.Category = unionInts(release.Category, cats)
			}
		}
		if strings.TrimSpace(value) != "" && len(release.Category) == 0 {
			x.log().Warn(fmt.Sprintf("[%s] Invalid category for value: '%s'", x.ID(), value))
		} else {
			value = joinInts(release.Category)
		}
	case "categorydesc":
		if hasModifier("noappend") {
			x.log().Warn(fmt.Sprintf("CardigannIndexer (%s): The \"noappend\" modifier is deprecated. Please switch to \"default\".", x.ID()))
		}
		catsDesc := x.Caps.Categories.MapTrackerCatDescToNewznab(value)
		if len(catsDesc) > 0 {
			if release.Category == nil || hasModifier("noappend") {
				release.Category = catsDesc
			} else {
				release.Category = unionInts(release.Category, catsDesc)
			}
		}
		if strings.TrimSpace(value) != "" && len(release.Category) == 0 {
			x.log().Warn(fmt.Sprintf("[%s] Invalid category for value: '%s'", x.ID(), value))
		} else {
			value = joinInts(release.Category)
		}
	case "size":
		n := parseutil.GetBytes(value)
		release.Size = &n
		value = strconv.FormatInt(n, 10)
	case "leechers":
		leechers := parseutil.MustCoerceLong(value)
		if leechers >= 5000000 {
			leechers = 0 // to fix #6558
		}
		if release.Peers == nil {
			peers := leechers
			release.Peers = &peers
		} else {
			*release.Peers += leechers
		}
		value = strconv.FormatInt(leechers, 10)
	case "seeders":
		seeders := parseutil.MustCoerceLong(value)
		if seeders >= 5000000 {
			seeders = 0 // to fix #6558
		}
		release.Seeders = &seeders
		if release.Peers == nil {
			peers := seeders
			release.Peers = &peers
		} else {
			*release.Peers += seeders
		}
		value = strconv.FormatInt(seeders, 10)
	case "date":
		t, err := dateutil.FromUnknown(value, "")
		if err != nil {
			return "", fmt.Errorf("CardigannIndexer (%s): Error parsing date \"%s\": %w", x.ID(), value, err)
		}
		release.PublishDate = t
		value = dateutil.FormatRFC1123Z(t)
	case "files":
		n := parseutil.MustCoerceLong(value)
		release.Files = &n
		value = strconv.FormatInt(n, 10)
	case "grabs":
		n := parseutil.MustCoerceLong(value)
		release.Grabs = &n
		value = strconv.FormatInt(n, 10)
	case "downloadvolumefactor":
		f := parseutil.MustCoerceDouble(value)
		release.DownloadVolumeFactor = &f
		value = strconv.FormatFloat(f, 'G', -1, 64)
	case "uploadvolumefactor":
		f := parseutil.MustCoerceDouble(value)
		release.UploadVolumeFactor = &f
		value = strconv.FormatFloat(f, 'G', -1, 64)
	case "minimumratio":
		f := parseutil.MustCoerceDouble(value)
		release.MinimumRatio = &f
		value = strconv.FormatFloat(f, 'G', -1, 64)
	case "minimumseedtime":
		n := parseutil.MustCoerceLong(value)
		release.MinimumSeedTime = &n
		value = strconv.FormatInt(n, 10)
	case "imdb", "imdbid":
		n, _ := parseutil.GetLongFromString(value)
		release.Imdb = &n
		value = strconv.FormatInt(n, 10)
	case "tmdbid":
		n, _ := parseutil.GetLongFromString(value)
		release.TMDb = &n
		value = strconv.FormatInt(n, 10)
	case "rageid":
		n, _ := parseutil.GetLongFromString(value)
		release.RageID = &n
		value = strconv.FormatInt(n, 10)
	case "tvdbid":
		n, _ := parseutil.GetLongFromString(value)
		release.TVDBId = &n
		value = strconv.FormatInt(n, 10)
	case "tvmazeid":
		n, _ := parseutil.GetLongFromString(value)
		release.TVMazeId = &n
		value = strconv.FormatInt(n, 10)
	case "traktid":
		n, _ := parseutil.GetLongFromString(value)
		release.TraktId = &n
		value = strconv.FormatInt(n, 10)
	case "doubanid":
		n, _ := parseutil.GetLongFromString(value)
		release.DoubanId = &n
		value = strconv.FormatInt(n, 10)
	case "genre":
		delimiters := ", /)(.;[]\"|:"
		var parts []string
		for _, piece := range strings.FieldsFunc(value, func(r rune) bool {
			return strings.ContainsRune(delimiters, r)
		}) {
			if piece != "" {
				parts = append(parts, piece)
			}
		}
		release.Genres = unionStrings(release.Genres, parts)
		for i, g := range release.Genres {
			release.Genres[i] = strings.ReplaceAll(g, "_", " ")
		}
		value = strings.Join(release.Genres, ",")
	case "year":
		n := parseutil.MustCoerceLong(value)
		release.Year = &n
		value = strconv.FormatInt(n, 10)
	case "author":
		release.Author = value
	case "booktitle":
		release.BookTitle = value
	case "publisher":
		release.Publisher = value
	case "artist":
		release.Artist = value
	case "album":
		release.Album = value
	case "label":
		release.Label = value
	case "track":
		release.Track = value
	case "poster":
		if strings.TrimSpace(value) != "" {
			release.Poster = mustParse(resolve(value))
		}
		value = ""
		if release.Poster != nil {
			value = release.Poster.String()
		}
	default:
		// unknown fields are kept as-is (upstream default: break)
	}
	return value, nil
}

func unionInts(a, b []int) []int {
	out := append([]int(nil), a...)
	seen := map[int]bool{}
	for _, v := range a {
		seen[v] = true
	}
	for _, v := range b {
		if !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	return out
}

func unionStrings(a, b []string) []string {
	out := append([]string(nil), a...)
	seen := map[string]bool{}
	for _, v := range a {
		seen[v] = true
	}
	for _, v := range b {
		if !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	return out
}

func joinInts(vs []int) string {
	parts := make([]string, 0, len(vs))
	for _, v := range vs {
		parts = append(parts, strconv.Itoa(v))
	}
	return strings.Join(parts, ",")
}

// zeroTime mirrors DateTime.MinValue comparisons.
var zeroTime = time.Time{}
