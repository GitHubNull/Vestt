package manual

import (
	"vestt/common/indexer"
	"vestt/common/indexer/shared"
	"vestt/common/models"
	"vestt/common/services"
)

// BrokenStones ports Jackett.Common/Indexers/Definitions/BrokenStones.cs.
//
// mechanism: gazelle
//
// Ported from: Definitions/BrokenStones.cs.
type BrokenStones struct {
	shared.Gazelle
}

// NewBrokenStones builds the indexer (services.Factory shape).
func NewBrokenStones(deps services.Deps) (indexer.Indexer, error) {
	b := &BrokenStones{}
	b.Base = indexer.Base{
		IDValue:          "brokenstones",
		NameValue:        "BrokenStones",
		DescriptionValue: "Broken Stones is a Private site for MacOS and iOS APPS / GAMES",
		SiteLinkValue:    "https://brokenstones.is/",
		DefaultSiteLink:  "https://brokenstones.is/",
		LegacySiteLinks: []string{
			"https://brokenstones.club/",
			"https://broken-stones.club/",
		},
		LangValue: "en-US",
		TypeValue: "private",
		Caps:      brokenStonesCaps(),
		Client:    deps.Client,
		Saver:     deps.Saver,
		Cache:     deps.Cache,
	}
	// upstream: Gazelle(supportsFreeleechTokens: true, has2Fa: true)
	b.Bind(shared.NewGazelleConfig(shared.GazelleOptions{
		SupportsFreeleechTokens: true,
		Has2FA:                  true,
	}))
	return b, nil
}

func brokenStonesCaps() *models.Capabilities {
	caps := models.NewCapabilities()
	c := caps.Categories
	c.AddCategoryMapping("1", models.CatPCMac, "MacOS Apps")
	c.AddCategoryMapping("2", models.CatPCMac, "MacOS Games")
	c.AddCategoryMapping("3", models.CatPCMobileiOS, "iOS Apps")
	c.AddCategoryMapping("4", models.CatPCMobileiOS, "iOS Games")
	c.AddCategoryMapping("5", models.CatOther, "Graphics")
	c.AddCategoryMapping("6", models.CatAudio, "Audio")
	c.AddCategoryMapping("7", models.CatOther, "Tutorials")
	c.AddCategoryMapping("8", models.CatOther, "Other")
	return caps
}
