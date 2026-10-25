package manual

import (
	"context"
	"fmt"

	"vestt/common/indexer"
	"vestt/common/indexer/configdata"
	"vestt/common/indexer/shared"
	"vestt/common/models"
	"vestt/common/services"
)

// DesiGaane ports Jackett.Common/Indexers/Definitions/DesiGaane.cs.
//
// mechanism: gazelle
//
// Ported from: Definitions/DesiGaane.cs.
type DesiGaane struct {
	shared.Gazelle
}

// desiGaaneAccountInactivity mirrors the upstream AddDynamic DisplayInfo
// item ("Account Inactivity").
const desiGaaneAccountInactivity = "To keep your account active, sign in and browse the site at least once every 90 days. Seeding torrents does not count as account activity."

var (
	desiGaaneMinRatio    = 0.6
	desiGaaneMinSeedTime = int64(259200)
)

// NewDesiGaane builds the indexer (services.Factory shape).
func NewDesiGaane(deps services.Deps) (indexer.Indexer, error) {
	d := &DesiGaane{}
	d.Base = indexer.Base{
		IDValue:          "desigaane",
		NameValue:        "DesiGaane",
		DescriptionValue: "DesiGaane is a Private Torrent Tracker for DESI MUSIC",
		SiteLinkValue:    "https://desigaane.rocks/",
		DefaultSiteLink:  "https://desigaane.rocks/",
		LangValue:        "en-US",
		TypeValue:        "private",
		Caps:             desigaaneCaps(),
		Client:           deps.Client,
		Saver:            deps.Saver,
		Cache:            deps.Cache,
	}
	// upstream: Gazelle(supportsFreeleechTokens: true, useApiKey: true)
	cfg := shared.NewGazelleConfig(shared.GazelleOptions{
		SupportsFreeleechTokens: true,
		UseAPIKey:               true,
	})
	// upstream constructor: configData.AddDynamic("Account Inactivity", ...)
	cfg.AddDynamic("accountinactivity", configdata.NewInfoItem("Account Inactivity", desiGaaneAccountInactivity))
	d.Bind(cfg)
	d.UseAPIKey = true
	d.APIKeyLength = 116 // upstream: protected override int ApiKeyLength => 116

	// upstream GetDownloadUrl override:
	// {SiteLink}ajax.php?action=download{&usetoken=1 if useTokens&&canUseToken}&id={torrentId}
	d.GetDownloadURL = func(torrentID int, canUseToken bool) string {
		s := d.SiteLink() + "ajax.php?action=download"
		if d.Config.UseToken != nil && d.Config.UseToken.Value && canUseToken {
			s += "&usetoken=1"
		}
		return fmt.Sprintf("%s&id=%d", s, torrentID)
	}
	return d, nil
}

func desigaaneCaps() *models.Capabilities {
	caps := models.NewCapabilities()
	caps.Categories.AddCategoryMapping("1", models.CatAudio, "Music")
	return caps
}

func (d *DesiGaane) ResultsForQuery(ctx context.Context, q *models.TorznabQuery, isMeta bool) (*indexer.IndexerResult, error) {
	return d.ResultsForQueryWith(ctx, q, isMeta, d.performQuery)
}

func (d *DesiGaane) Test(ctx context.Context) error {
	return d.Base.Test(ctx, d.performQuery)
}

// performQuery ports upstream DesiGaane.PerformQuery: base query plus the
// post-parse MinimumRatio/MinimumSeedTime stamps.
//
// 澄清记录:
//   - upstream ApplyConfiguration (GazelleTracker base) calls the virtual
//     PerformQuery, so the API-key self-test also stamps MinimumRatio/
//     MinimumSeedTime; the Go port routes the self-test through the embedded
//     shared.Gazelle.PerformQuery without the stamps. This is not observable
//     in the result count/validity checks the self-test performs.
//   - the upstream GetDownloadUrl override reads the protected `useTokens`
//     field (supportsFreeleechTokens && UseToken item); the hook replicates
//     it via the UseToken item, SupportsFreeleechTokens being true here.
func (d *DesiGaane) performQuery(ctx context.Context, query *models.TorznabQuery) ([]*models.ReleaseInfo, error) {
	releases, err := d.PerformQuery(ctx, query)
	if err != nil {
		return nil, err
	}
	for _, release := range releases {
		// the site has a proportional ratio system calculated using (1) the
		// total amount of data you've downloaded and (2) the total number of
		// torrents you're seeding.
		// So we are going to default the MR to the maximim ratio required to
		// cover the whole range as we cannot calculate this for each user.
		release.MinimumRatio = &desiGaaneMinRatio
		release.MinimumSeedTime = &desiGaaneMinSeedTime
	}
	return releases, nil
}
