// mechanism: json-api (email/password JSON login against api/login, Bearer
// token on api/torrent searches — the shared SpeedAppTracker mechanism)
//
// Ported from: Definitions/RetroFlix.cs (+ Abstract/SpeedAppTracker.cs).
//
// 澄清记录:
//   - upstream RetroFlix.cs:41 adds a dynamic "Account Inactivity"
//     DisplayInfo on top of the base ConfigurationDataSpeedAppTracker's
//     equally-named item, so the upstream form shows the info twice; the
//     dynamic entry carries RetroFlix's 150-day message (replicated via
//     AddDynamic, same deviation note as PrivateHD).
//   - upstream overrides MinimumSeedTime to 432000 (120h, RetroFlix.cs:29);
//     passed to the shared base here.
package manual

import (
	"vestt/common/indexer"
	"vestt/common/indexer/configdata"
	"vestt/common/models"
	"vestt/common/services"
)

// RetroFlix ports Jackett.Common/Indexers/Definitions/RetroFlix.cs.
type RetroFlix struct {
	speedAppTracker
}

// retroflixAccountInactivity mirrors the upstream AddDynamic DisplayInfo
// item (RetroFlix.cs:41).
const retroflixAccountInactivity = "Any account not logged into after 150 days will be deleted. Parked accounts will be deleted after 150 days of inactivity."

// NewRetroFlix builds the indexer (services.Factory shape).
func NewRetroFlix(deps services.Deps) (indexer.Indexer, error) {
	r := &RetroFlix{}
	cfg := newSpeedAppConfig()
	// upstream constructor: configData.AddDynamic("Account Inactivity", ...)
	cfg.AddDynamic("accountinactivity", configdata.NewInfoItem("Account Inactivity", retroflixAccountInactivity))
	r.buildSpeedAppTracker(deps, speedAppIdentity{
		id:          "retroflix",
		name:        "RetroFlix",
		description: "RetroFlix is a Private site for Classic Movies / TV / General Releases",
		siteLink:    "https://retroflix.net/",
		altLinks:    []string{"https://retroflix.net/", "https://retroflix.club/"},
		lang:        "en-US",
	}, retroflixCaps(), 432000, cfg) // upstream MinimumSeedTime override (120h)
	return r, nil
}

func retroflixCaps() *models.Capabilities {
	caps := models.NewCapabilities()
	caps.TVSearchParams = []string{models.TVParamQ, models.TVParamSeason, models.TVParamEp, models.TVParamImdbID}
	caps.MovieSearchParams = []string{models.MovieParamQ, models.MovieParamImdbID}
	caps.MusicSearchParams = []string{models.MusicParamQ}
	caps.BookSearchParams = []string{models.BookParamQ}

	c := caps.Categories
	c.AddCategoryMapping("401", models.CatMovies, "Movies")
	c.AddCategoryMapping("402", models.CatTV, "TV Series")
	c.AddCategoryMapping("406", models.CatAudioVideo, "Music Videos")
	c.AddCategoryMapping("407", models.CatTVSport, "Sports")
	c.AddCategoryMapping("409", models.CatBooks, "Books")
	c.AddCategoryMapping("408", models.CatAudio, "HQ Audio")
	return caps
}
