package manual

import (
	"context"
	"fmt"
	"strings"

	"vestt/common/indexer"
	"vestt/common/indexer/configdata"
	"vestt/common/indexer/shared"
	"vestt/common/models"
	"vestt/common/services"
)

// Orpheus ports Jackett.Common/Indexers/Definitions/Orpheus.cs.
//
// mechanism: gazelle-api (OPSnet ajax.php JSON API, "token {0}" API key
// authorization, ajax.php download endpoint, proportional-ratio stamp)
//
// Ported from: Definitions/Orpheus.cs.
//
// 澄清记录:
//   - upstream self-test (GazelleTracker.ApplyConfiguration) runs the
//     virtual PerformQuery (which stamps MinimumRatio = 0.6); the Go port's
//     embedded shared.Gazelle.ApplyConfiguration runs the shared browse
//     without the stamp — same deviation pattern as AlphaRatio (SPEC-08),
//     not observable in the self-test's checks.
//   - upstream FillReleaseInfoFromJson computes GetDownloadUrl(torrentId,
//     release.DownloadVolumeFactor != 0) BEFORE the volume factors are
//     assigned, so canUseToken is always true on this path and every
//     download link carries "&usetoken=1" (freeleech releases included);
//     replicated verbatim (shared.gazelle fillReleaseInfoFromJSON keeps the
//     same ordering).
type Orpheus struct {
	shared.Gazelle
}

const orpheusInstructionMessage = "<ol><li>Go to Orpheus's site and open your account settings.</li><li>Under <b>Access Settings</b> click on 'Create a new token'</li><li>Give it a name you like and click <b>Generate</b>.</li><li>Copy the generated API Key and paste it in the above text field.</li></ol>"

// orpheusAccountInactivity mirrors the upstream AddDynamic DisplayInfo item.
const orpheusAccountInactivity = "To keep your account active, sign in and browse the site at least once every 120 days. Seeding torrents does not count as account activity, so in order to remain active you need to sign in and browse the site. Power Users (and above) are immune to the inactivity timer, but logging in regularly is recommended to learn about special events and new releases. Donors are exempt from automatic account disabling due to inactivity. If you wish to always maintain an active account consider donating."

var orpheusMinimumRatio = 0.6

// NewOrpheus builds the indexer (services.Factory shape).
func NewOrpheus(deps services.Deps) (indexer.Indexer, error) {
	o := &Orpheus{}
	o.Base = indexer.Base{
		IDValue:          "orpheus",
		NameValue:        "Orpheus",
		DescriptionValue: "Orpheus is a Private MUSIC tracker",
		SiteLinkValue:    "https://orpheus.network/",
		DefaultSiteLink:  "https://orpheus.network/",
		LangValue:        "en-US",
		TypeValue:        "private",
		Caps:             orpheusCaps(),
		Client:           deps.Client,
		Saver:            deps.Saver,
		Cache:            deps.Cache,
	}
	cfg := shared.NewGazelleConfig(shared.GazelleOptions{
		SupportsFreeleechTokens: true,
		UseAPIKey:               true,
		InstructionMessage:      orpheusInstructionMessage,
	})
	// upstream constructor: configData.AddDynamic("Account Inactivity", ...)
	cfg.AddDynamic("accountinactivity", configdata.NewInfoItem("Account Inactivity", orpheusAccountInactivity))
	o.Bind(cfg)
	o.UseAPIKey = true
	o.APIKeyLength = 116
	o.APIKeyLengthLegacy = 118
	o.AuthorizationFormat = "token {0}"
	o.FlipOptionalTokenString = func(requestLink string) string {
		return strings.ReplaceAll(requestLink, "&usetoken=1", "")
	}
	// Upstream overrides GetDownloadUrl to use the ajax.php endpoint (the
	// shared default builds torrents.php and appends passkey/authkey, which
	// Orpheus does not use).
	o.GetDownloadURL = func(torrentID int, canUseToken bool) string {
		s := fmt.Sprintf("%sajax.php?action=download", o.SiteLink())
		if o.Config.UseToken != nil && o.Config.UseToken.Value && canUseToken {
			s += "&usetoken=1"
		}
		return fmt.Sprintf("%s&id=%d", s, torrentID)
	}
	return o, nil
}

func orpheusCaps() *models.Capabilities {
	caps := models.NewCapabilities()
	caps.MusicSearchParams = []string{
		models.MusicParamQ, models.MusicParamAlbum, models.MusicParamArtist,
		models.MusicParamLabel, models.MusicParamYear, models.MusicParamGenre,
	}
	caps.BookSearchParams = []string{models.BookParamQ, models.BookParamGenre}
	c := caps.Categories
	c.AddCategoryMapping("1", models.CatAudio, "Music")
	c.AddCategoryMapping("2", models.CatPC, "Applications")
	c.AddCategoryMapping("3", models.CatBooksEBook, "E-Books")
	c.AddCategoryMapping("4", models.CatAudioAudiobook, "Audiobooks")
	c.AddCategoryMapping("5", models.CatOther, "E-Learning Videos")
	c.AddCategoryMapping("6", models.CatOther, "Comedy")
	c.AddCategoryMapping("7", models.CatBooksComics, "Comics")
	return caps
}

// ResultsForQuery implements indexer.Indexer.
func (o *Orpheus) ResultsForQuery(ctx context.Context, q *models.TorznabQuery, isMeta bool) (*indexer.IndexerResult, error) {
	return o.ResultsForQueryWith(ctx, q, isMeta, o.performQuery)
}

// Test implements indexer.Indexer.
func (o *Orpheus) Test(ctx context.Context) error {
	return o.Base.Test(ctx, o.performQuery)
}

// performQuery ports upstream Orpheus.PerformQuery: the base Gazelle query
// plus the proportional-ratio MinimumRatio stamp on every release.
func (o *Orpheus) performQuery(ctx context.Context, query *models.TorznabQuery) ([]*models.ReleaseInfo, error) {
	releases, err := o.PerformQuery(ctx, query)
	if err != nil {
		return nil, err
	}
	for _, release := range releases {
		// the site has a proportional ratio system calculated using (1) the
		// total amount of data you've downloaded and (2) the total number of
		// torrents you're seeding.
		// So we are going to default the MR to the maximim ratio required to
		// cover the whole range as we cannot calculate this for each user.
		release.MinimumRatio = &orpheusMinimumRatio
	}
	return releases, nil
}
