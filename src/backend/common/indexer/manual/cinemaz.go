// mechanism: json-api
//
// Ported from: Definitions/CinemaZ.cs
//
// CinemaZ is a pure base-class assembly: no hooks, no overrides.
package manual

import (
	"vestt/common/indexer"
	"vestt/common/indexer/shared"
	"vestt/common/models"
	"vestt/common/services"
)

// CinemaZ ports Jackett.Common/Indexers/Definitions/CinemaZ.cs.
type CinemaZ struct {
	shared.Avistaz
}

// NewCinemaZ builds the indexer (services.Factory shape).
func NewCinemaZ(deps services.Deps) (indexer.Indexer, error) {
	c := &CinemaZ{}
	cfg := shared.NewAvistazConfig("")
	c.Base = indexer.Base{
		IDValue:          "cinemaz",
		NameValue:        "CinemaZ",
		DescriptionValue: "CinemaZ (EuTorrents) is a Private Torrent Tracker for FOREIGN NON-ASIAN MOVIES / TV",
		SiteLinkValue:    "https://cinemaz.to/",
		DefaultSiteLink:  "https://cinemaz.to/",
		LangValue:        "en-US",
		TypeValue:        "private",
		Caps:             cinemazCaps(),
		Config:           cfg,
		Client:           deps.Client,
		Saver:            deps.Saver,
		Cache:            deps.Cache,
	}
	c.Bind(cfg)
	c.WireDelay()
	return c, nil
}

func cinemazCaps() *models.Capabilities {
	caps := models.NewCapabilities()
	def, max := 50, 50
	caps.LimitsDefault = &def
	caps.LimitsMax = &max
	caps.TVSearchParams = []string{
		models.TVParamQ, models.TVParamSeason, models.TVParamEp,
		models.TVParamImdbID, models.TVParamTvdbID, models.TVParamGenre,
	}
	caps.MovieSearchParams = []string{
		models.MovieParamQ, models.MovieParamImdbID, models.MovieParamTmdbID, models.MovieParamGenre,
	}
	caps.TVSearchImdbAvailable = true

	c := caps.Categories
	c.AddCategoryMapping("1", models.CatMovies, "")
	c.AddCategoryMapping("1", models.CatMoviesUHD, "")
	c.AddCategoryMapping("1", models.CatMoviesHD, "")
	c.AddCategoryMapping("1", models.CatMoviesSD, "")
	c.AddCategoryMapping("2", models.CatTV, "")
	c.AddCategoryMapping("2", models.CatTVUHD, "")
	c.AddCategoryMapping("2", models.CatTVHD, "")
	c.AddCategoryMapping("2", models.CatTVSD, "")
	return caps
}
