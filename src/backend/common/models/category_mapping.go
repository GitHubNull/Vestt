package models

import (
	"crypto/sha1"
	"encoding/binary"
	"fmt"
	"strconv"
	"strings"
)

// CategoryMapping is one tracker-category → torznab-category binding
// (upstream CategoryMapping in TorznabCapabilitiesCategories.cs).
type CategoryMapping struct {
	TrackerCategory     string
	TrackerCategoryDesc string
	NewzNabCategory     int
}

// GetTrackerCategories mirrors upstream: distinct tracker categories of all
// non-custom mappings.
func (c *CapabilitiesCategories) GetTrackerCategories() []string {
	seen := map[string]bool{}
	var out []string
	for _, m := range c.mappings {
		if m.NewzNabCategory >= 100000 || seen[m.TrackerCategory] {
			continue
		}
		seen[m.TrackerCategory] = true
		out = append(out, m.TrackerCategory)
	}
	return out
}

// AddCategoryMapping mirrors upstream: binds the tracker category to the
// torznab category and extends the tree; when a tracker-category description
// is given a custom 1:1 category (id = trackerCategoryInt + 100000, or a
// SHA1-derived id for non-integer tracker categories) is created.
func (c *CapabilitiesCategories) AddCategoryMapping(trackerCategory string, torznabCategory *Category, trackerCategoryDesc string) {
	if torznabCategory == nil {
		// upstream: TorznabCat can be null for desc-only custom cats; the
		// mapping still needs a NewzNabCategory — upstream would NRE here,
		// so callers must pass a valid cat unless desc is set. We mirror the
		// observed upstream usage (null cat only with desc → custom cat).
		if trackerCategoryDesc == "" {
			return
		}
		torznabCategory = &Category{}
	}
	c.mappings = append(c.mappings, CategoryMapping{
		TrackerCategory:     trackerCategory,
		TrackerCategoryDesc: trackerCategoryDesc,
		NewzNabCategory:     torznabCategory.ID,
	})
	c.AddCategoryTree(torznabCategory)

	if trackerCategoryDesc == "" {
		return
	}
	trackerCategoryInt, err := strconv.Atoi(trackerCategory)
	if err != nil {
		sum := sha1.Sum([]byte(trackerCategory))
		trackerCategoryInt = int(binary.LittleEndian.Uint16(sum[:2]))
	}
	customCat := &Category{ID: trackerCategoryInt + 100000, Name: trackerCategoryDesc}
	c.mappings = append(c.mappings, CategoryMapping{
		TrackerCategory:     trackerCategory,
		TrackerCategoryDesc: trackerCategoryDesc,
		NewzNabCategory:     customCat.ID,
	})
	c.AddCategoryTree(customCat)
}

// MapTorznabCapsToTrackers mirrors upstream: expand the query categories
// (parents include children) then collect distinct tracker categories.
func (c *CapabilitiesCategories) MapTorznabCapsToTrackers(q *TorznabQuery, mapChildrenCatsToParent bool) []string {
	expanded := c.ExpandTorznabQueryCategories(q, mapChildrenCatsToParent)
	seen := map[string]bool{}
	var out []string
	for _, m := range c.mappings {
		if !containsInt(expanded, m.NewzNabCategory) || seen[m.TrackerCategory] {
			continue
		}
		seen[m.TrackerCategory] = true
		out = append(out, m.TrackerCategory)
	}
	return out
}

// MapTrackerCatToNewznab mirrors upstream (case-insensitive tracker category).
func (c *CapabilitiesCategories) MapTrackerCatToNewznab(trackerCategory string) []int {
	if strings.TrimSpace(trackerCategory) == "" {
		return []int{}
	}
	var out []int
	for _, m := range c.mappings {
		if strings.TrimSpace(m.TrackerCategory) != "" && strings.EqualFold(m.TrackerCategory, trackerCategory) {
			out = append(out, m.NewzNabCategory)
		}
	}
	return out
}

// MapTrackerCatDescToNewznab mirrors upstream (case-insensitive desc).
func (c *CapabilitiesCategories) MapTrackerCatDescToNewznab(trackerCategoryDesc string) []int {
	if strings.TrimSpace(trackerCategoryDesc) == "" {
		return []int{}
	}
	var out []int
	for _, m := range c.mappings {
		if strings.TrimSpace(m.TrackerCategoryDesc) != "" && strings.EqualFold(m.TrackerCategoryDesc, trackerCategoryDesc) {
			out = append(out, m.NewzNabCategory)
		}
	}
	return out
}

func containsInt(list []int, v int) bool {
	for _, n := range list {
		if n == v {
			return true
		}
	}
	return false
}

// tvSearchParamNames maps the wire-name params of tv-search modes
// (upstream TvSearchParam enum, Enum.TryParse case-insensitive).
var tvSearchParamNames = map[string]string{
	"q": TVParamQ, "season": TVParamSeason, "ep": TVParamEp,
	"imdbid": TVParamImdbID, "tvdbid": TVParamTvdbID, "rid": TVParamRID,
	"tmdbid": TVParamTmdbID, "tvmazeid": TVParamTvmazeID,
	"traktid": TVParamTraktID, "doubanid": TVParamDoubanID,
	"year": TVParamYear, "genre": TVParamGenre,
}

var movieSearchParamNames = map[string]string{
	"q": MovieParamQ, "imdbid": MovieParamImdbID, "tmdbid": MovieParamTmdbID,
	"traktid": MovieParamTraktID, "doubanid": MovieParamDoubanID,
	"year": MovieParamYear, "genre": MovieParamGenre,
}

var musicSearchParamNames = map[string]string{
	"q": MusicParamQ, "album": MusicParamAlbum, "artist": MusicParamArtist,
	"label": MusicParamLabel, "track": MusicParamTrack,
	"year": MusicParamYear, "genre": MusicParamGenre,
}

var bookSearchParamNames = map[string]string{
	"q": BookParamQ, "title": BookParamTitle, "author": BookParamAuthor,
	"publisher": BookParamPublisher, "year": BookParamYear, "genre": BookParamGenre,
}

// SearchMode is one caps.modes entry (ordered).
type SearchMode struct {
	Key    string
	Params []string
}

// ParseCardigannSearchModes mirrors upstream TorznabCapabilities
// .ParseCardigannSearchModes including its validation errors.
func (c *Capabilities) ParseCardigannSearchModes(modes []SearchMode) error {
	if len(modes) == 0 {
		return fmt.Errorf("At least one search mode is required")
	}
	hasSearch := false
	for _, m := range modes {
		if m.Key == "search" {
			hasSearch = true
			break
		}
	}
	if !hasSearch {
		return fmt.Errorf("The search mode 'search' is mandatory")
	}
	for _, m := range modes {
		switch m.Key {
		case "search":
			if len(m.Params) != 1 || m.Params[0] != "q" {
				return fmt.Errorf("In search mode 'search' only 'q' parameter is supported and it's mandatory")
			}
		case "tv-search":
			for _, p := range m.Params {
				name, ok := tvSearchParamNames[strings.ToLower(p)]
				if !ok {
					return fmt.Errorf("Not supported tv-search param: %s", p)
				}
				if contains(c.TVSearchParams, name) {
					return fmt.Errorf("Duplicate tv-search param: %s", p)
				}
				c.TVSearchParams = append(c.TVSearchParams, name)
			}
		case "movie-search":
			for _, p := range m.Params {
				name, ok := movieSearchParamNames[strings.ToLower(p)]
				if !ok {
					return fmt.Errorf("Not supported movie-search param: %s", p)
				}
				if contains(c.MovieSearchParams, name) {
					return fmt.Errorf("Duplicate movie-search param: %s", p)
				}
				c.MovieSearchParams = append(c.MovieSearchParams, name)
			}
		case "music-search":
			for _, p := range m.Params {
				name, ok := musicSearchParamNames[strings.ToLower(p)]
				if !ok {
					return fmt.Errorf("Not supported music-search param: %s", p)
				}
				if contains(c.MusicSearchParams, name) {
					return fmt.Errorf("Duplicate music-search param: %s", p)
				}
				c.MusicSearchParams = append(c.MusicSearchParams, name)
			}
		case "book-search":
			for _, p := range m.Params {
				name, ok := bookSearchParamNames[strings.ToLower(p)]
				if !ok {
					return fmt.Errorf("Not supported book-search param: %s", p)
				}
				if contains(c.BookSearchParams, name) {
					return fmt.Errorf("Duplicate book-search param: %s", p)
				}
				c.BookSearchParams = append(c.BookSearchParams, name)
			}
		default:
			return fmt.Errorf("Unsupported search mode: %s", m.Key)
		}
	}
	return nil
}
