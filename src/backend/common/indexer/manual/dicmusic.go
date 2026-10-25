package manual

import (
	"context"

	"vestt/common/indexer"
	"vestt/common/indexer/shared"
	"vestt/common/models"
	"vestt/common/services"
)

// DICMusic ports Jackett.Common/Indexers/Definitions/DICMusic.cs.
//
// mechanism: gazelle
//
// Ported from: Definitions/DICMusic.cs.
type DICMusic struct {
	shared.Gazelle
}

// NewDICMusic builds the indexer (services.Factory shape).
func NewDICMusic(deps services.Deps) (indexer.Indexer, error) {
	d := &DICMusic{}
	d.Base = indexer.Base{
		IDValue:          "dicmusic",
		NameValue:        "DICMusic",
		DescriptionValue: "DICMusic is a CHINESE Private Torrent Tracker for MUSIC",
		SiteLinkValue:    "https://dicmusic.com/",
		DefaultSiteLink:  "https://dicmusic.com/",
		LegacySiteLinks:  []string{"https://dicmusic.club/"},
		LangValue:        "zh-CN",
		TypeValue:        "private",
		Caps:             dicmusicCaps(),
		Client:           deps.Client,
		Saver:            deps.Saver,
		Cache:            deps.Cache,
	}
	// upstream: Gazelle(supportsFreeleechTokens: true, supportsFreeleechOnly:
	// true, has2Fa: true)
	d.Bind(shared.NewGazelleConfig(shared.GazelleOptions{
		SupportsFreeleechTokens: true,
		SupportsFreeleechOnly:   true,
		Has2FA:                  true,
	}))
	return d, nil
}

func dicmusicCaps() *models.Capabilities {
	caps := models.NewCapabilities()
	caps.MusicSearchParams = []string{
		models.MusicParamQ, models.MusicParamAlbum, models.MusicParamArtist,
		models.MusicParamLabel, models.MusicParamYear,
	}
	c := caps.Categories
	c.AddCategoryMapping("1", models.CatAudio, "Music")
	c.AddCategoryMapping("2", models.CatPC, "Applications")
	return caps
}

func (d *DICMusic) ResultsForQuery(ctx context.Context, q *models.TorznabQuery, isMeta bool) (*indexer.IndexerResult, error) {
	return d.ResultsForQueryWith(ctx, q, isMeta, d.performQuery)
}

func (d *DICMusic) Test(ctx context.Context) error {
	return d.Base.Test(ctx, d.performQuery)
}

// performQuery ports upstream DICMusic.PerformQuery: base query, then keep
// only releases whose title matches the query string (AND semantics).
func (d *DICMusic) performQuery(ctx context.Context, query *models.TorznabQuery) ([]*models.ReleaseInfo, error) {
	results, err := d.PerformQuery(ctx, query)
	if err != nil {
		return nil, err
	}
	// results must contain search terms
	var filtered []*models.ReleaseInfo
	for _, release := range results {
		if query.MatchQueryStringAND(release.Title, 0, "") {
			filtered = append(filtered, release)
		}
	}
	return filtered, nil
}
