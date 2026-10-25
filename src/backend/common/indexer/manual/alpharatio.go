package manual

import (
	"context"

	"vestt/common/indexer"
	"vestt/common/indexer/configdata"
	"vestt/common/indexer/shared"
	"vestt/common/models"
	"vestt/common/services"
)

// AlphaRatio ports Jackett.Common/Indexers/Definitions/AlphaRatio.cs.
//
// mechanism: gazelle
//
// Ported from: Definitions/AlphaRatio.cs.
type AlphaRatio struct {
	shared.Gazelle
}

// alphaRatioAccountInactivity mirrors the upstream AddDynamic DisplayInfo
// item ("Account Inactivity").
const alphaRatioAccountInactivity = "Users must login to the site at least once every 90 days. Failure to do so results in the user account being suspended and automatically demoted to Exiled user class."

var (
	alphaRatioMinRatio    = 1.0
	alphaRatioMinSeedTime = int64(259200)
)

// NewAlphaRatio builds the indexer (services.Factory shape).
func NewAlphaRatio(deps services.Deps) (indexer.Indexer, error) {
	a := &AlphaRatio{}
	a.Base = indexer.Base{
		IDValue:          "alpharatio",
		NameValue:        "AlphaRatio",
		DescriptionValue: "AlphaRatio (AR) is a Private Torrent Tracker for 0DAY / GENERAL",
		SiteLinkValue:    "https://alpharatio.cc/",
		DefaultSiteLink:  "https://alpharatio.cc/",
		LangValue:        "en-US",
		TypeValue:        "private",
		Caps:             alphaRatioCaps(),
		Client:           deps.Client,
		Saver:            deps.Saver,
		Cache:            deps.Cache,
	}
	cfg := shared.NewGazelleConfig(shared.GazelleOptions{
		SupportsFreeleechTokens: true,
		SupportsFreeleechOnly:   true,
		ImdbInTags:              true,
	})
	// upstream constructor: configData.AddDynamic("Account Inactivity", ...)
	cfg.AddDynamic("accountinactivity", configdata.NewInfoItem("Account Inactivity", alphaRatioAccountInactivity))
	a.Bind(cfg)
	a.ImdbInTags = true
	return a, nil
}

func alphaRatioCaps() *models.Capabilities {
	caps := models.NewCapabilities()
	caps.TVSearchParams = []string{models.TVParamQ, models.TVParamSeason, models.TVParamEp}
	caps.MovieSearchParams = []string{models.MovieParamQ, models.MovieParamImdbID}
	c := caps.Categories
	c.AddCategoryMapping("1", models.CatTVSD, "TvSD")
	c.AddCategoryMapping("2", models.CatTVHD, "TvHD")
	c.AddCategoryMapping("3", models.CatTVUHD, "TvUHD")
	c.AddCategoryMapping("4", models.CatTVSD, "TvDVDRip")
	c.AddCategoryMapping("5", models.CatTVSD, "TvPackSD")
	c.AddCategoryMapping("6", models.CatTVHD, "TvPackHD")
	c.AddCategoryMapping("7", models.CatTVUHD, "TvPackUHD")
	c.AddCategoryMapping("8", models.CatMoviesSD, "MovieSD")
	c.AddCategoryMapping("9", models.CatMoviesHD, "MovieHD")
	c.AddCategoryMapping("10", models.CatMoviesUHD, "MovieUHD")
	c.AddCategoryMapping("11", models.CatMoviesSD, "MoviePackSD")
	c.AddCategoryMapping("12", models.CatMoviesHD, "MoviePackHD")
	c.AddCategoryMapping("13", models.CatMoviesUHD, "MoviePackUHD")
	c.AddCategoryMapping("14", models.CatXXX, "MovieXXX")
	c.AddCategoryMapping("15", models.CatMoviesBluRay, "Bluray")
	c.AddCategoryMapping("16", models.CatTVAnime, "AnimeSD")
	c.AddCategoryMapping("17", models.CatTVAnime, "AnimeHD")
	c.AddCategoryMapping("18", models.CatPCGames, "GamesPC")
	c.AddCategoryMapping("19", models.CatConsoleXBox, "GamesxBox")
	c.AddCategoryMapping("20", models.CatConsolePS4, "GamesPS")
	c.AddCategoryMapping("21", models.CatConsoleWii, "GamesNin")
	c.AddCategoryMapping("22", models.CatPC0day, "AppsWindows")
	c.AddCategoryMapping("23", models.CatPCMac, "AppsMAC")
	c.AddCategoryMapping("24", models.CatPC0day, "AppsLinux")
	c.AddCategoryMapping("25", models.CatPCMobileOther, "AppsMobile")
	c.AddCategoryMapping("26", models.CatXXX, "0dayXXX")
	c.AddCategoryMapping("27", models.CatBooks, "eBook")
	c.AddCategoryMapping("28", models.CatAudioAudiobook, "AudioBook")
	c.AddCategoryMapping("29", models.CatAudioOther, "Music")
	c.AddCategoryMapping("30", models.CatOther, "Misc")
	return caps
}

func (a *AlphaRatio) ResultsForQuery(ctx context.Context, q *models.TorznabQuery, isMeta bool) (*indexer.IndexerResult, error) {
	return a.ResultsForQueryWith(ctx, q, isMeta, a.performQuery)
}

func (a *AlphaRatio) Test(ctx context.Context) error {
	return a.Base.Test(ctx, a.performQuery)
}

// performQuery ports upstream AlphaRatio.PerformQuery: base query plus the
// post-parse MinimumRatio/MinimumSeedTime stamps.
//
// 澄清记录:
//   - upstream ApplyConfiguration (GazelleTracker base) calls the virtual
//     PerformQuery, so the config self-test also stamps MinimumRatio/
//     MinimumSeedTime; the Go port routes the self-test through the embedded
//     shared.Gazelle.PerformQuery without the stamps. This is not observable
//     in the result count/validity checks the self-test performs.
func (a *AlphaRatio) performQuery(ctx context.Context, query *models.TorznabQuery) ([]*models.ReleaseInfo, error) {
	releases, err := a.PerformQuery(ctx, query)
	if err != nil {
		return nil, err
	}
	for _, release := range releases {
		release.MinimumRatio = &alphaRatioMinRatio
		release.MinimumSeedTime = &alphaRatioMinSeedTime
	}
	return releases, nil
}
