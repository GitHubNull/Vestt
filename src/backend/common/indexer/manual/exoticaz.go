// mechanism: json-api
//
// Ported from: Definitions/ExoticaZ.cs
//
// ExoticaZ is the 3X member of the AvistaZ family (replaces the "yourexotic"
// id upstream; the Go Base has no Replaces field, so the legacy site link is
// the only migration hint carried over).
//
// 澄清记录:
//   - upstream iterates row.Category with SelectMany over the JSON object's
//     keys (.NET dictionaries preserve insertion order in practice); Go maps
//     are unordered, so the keys are sorted for determinism — the resulting
//     category multiset is identical either way.
package manual

import (
	"fmt"
	"sort"
	"strings"

	"vestt/common/indexer"
	"vestt/common/indexer/shared"
	"vestt/common/models"
	"vestt/common/services"
)

// ExoticaZ ports Jackett.Common/Indexers/Definitions/ExoticaZ.cs.
type ExoticaZ struct {
	shared.Avistaz
}

// NewExoticaZ builds the indexer (services.Factory shape).
func NewExoticaZ(deps services.Deps) (indexer.Indexer, error) {
	e := &ExoticaZ{}
	cfg := shared.NewAvistazConfig("")
	e.Base = indexer.Base{
		IDValue:          "exoticaz",
		NameValue:        "ExoticaZ",
		DescriptionValue: "ExoticaZ (YourExotic) is a Private Torrent Tracker for 3X",
		SiteLinkValue:    "https://exoticaz.to/",
		DefaultSiteLink:  "https://exoticaz.to/",
		LegacySiteLinks:  []string{"https://torrents.yourexotic.com/"},
		LangValue:        "en-US",
		TypeValue:        "private",
		Caps:             exoticazCaps(),
		Config:           cfg,
		Client:           deps.Client,
		Saver:            deps.Saver,
		Cache:            deps.Cache,
	}
	e.Bind(cfg)
	e.WireDelay()

	// GetSearchTerm is not overridden upstream; setting the hook to the base
	// behaviour lets the overridden GetSearchQueryParameters call it (the
	// shared base's term helpers are unexported).
	e.GetSearchTerm = func(q *models.TorznabQuery) string {
		return strings.TrimSpace(q.SearchTerm + " " + strings.TrimSpace(q.GetEpisodeSearchString()))
	}
	e.GetSearchQueryParameters = func(q *models.TorznabQuery) shared.QueryParams {
		categoryMapping := avzDistinctStrings(e.Caps.Categories.MapTorznabCapsToTrackers(q, false))
		var qc shared.QueryParams // NameValueCollection don't support cat[]=19&cat[]=6
		qc.Add("in", "1")
		qc.Add("category", avzFirstIfSingleOrDefault(categoryMapping, "0"))
		qc.Add("limit", "50")
		qc.Add("search", strings.TrimSpace(e.GetSearchTerm(q)))

		if q.Limit > 0 && q.Offset > 0 {
			page := q.Offset/q.Limit + 1
			qc.Add("page", fmt.Sprintf("%d", page))
		}

		if e.Config.Freeleech.Value {
			qc.Add("discount[]", "1")
		}

		return qc
	}
	e.ParseCategories = func(q *models.TorznabQuery, row *shared.AvistazRelease) ([]int, error) {
		keys := make([]string, 0, len(row.Category))
		for k := range row.Category {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		var cats []int
		for _, k := range keys {
			cats = append(cats, e.Caps.Categories.MapTrackerCatToNewznab(k)...)
		}
		return cats, nil
	}
	return e, nil
}

func exoticazCaps() *models.Capabilities {
	caps := models.NewCapabilities()
	def, max := 50, 50
	caps.LimitsDefault = &def
	caps.LimitsMax = &max

	c := caps.Categories
	c.AddCategoryMapping("1", models.CatXXXx264, "Video Clip")
	c.AddCategoryMapping("2", models.CatXXXPack, "Video Pack")
	c.AddCategoryMapping("3", models.CatXXXPack, "Siterip Pack")
	c.AddCategoryMapping("4", models.CatXXXPack, "Pornstar Pack")
	c.AddCategoryMapping("5", models.CatXXXDVD, "DVD")
	c.AddCategoryMapping("6", models.CatXXXx264, "BluRay")
	c.AddCategoryMapping("7", models.CatXXXImageSet, "Photo Pack")
	c.AddCategoryMapping("8", models.CatXXXImageSet, "Books & Magazines")
	return caps
}
