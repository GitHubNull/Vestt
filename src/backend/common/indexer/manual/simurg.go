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

// Simurg ports Jackett.Common/Indexers/Definitions/Simurg.cs.
//
// mechanism: gazelle-api (Simurg ajax.php JSON API, 116-char API key,
// "token {0}" authorization, ajax.php download endpoint, format-fallback
// categories, proportional-ratio stamp)
//
// Ported from: Definitions/Simurg.cs.
//
// 澄清记录:
//   - upstream ApplyConfiguration (GazelleTracker base) runs the virtual
//     PerformQuery for the API-key self-test, which stamps MinimumRatio /
//     MinimumSeedTime; the Go port's embedded shared.Gazelle
//     ApplyConfiguration runs the shared browse without the stamps — same
//     deviation pattern as DesiGaane (SPEC-08), not observable in the
//     self-test's checks.
//   - upstream ReleaseInfoPostParse reads torrent["category"] first and falls
//     back to result["category"] (Simurg.cs:64); older grouped responses omit
//     the torrent-level category, so the format fallback below stays.
//   - upstream GetDownloadUrl builds
//     "{SiteLink}ajax.php?action=download{&usetoken=1}&id={torrentId}"
//     (Simurg.cs:55-58); ported via the GetDownloadURL hook.
type Simurg struct {
	shared.Gazelle
}

// simurgAccountInactivity mirrors the upstream AddDynamic DisplayInfo item
// (Simurg.cs:40).
const simurgAccountInactivity = "To keep your account active, sign in and browse the site at least once every 90 days. Seeding torrents does not count as account activity."

var (
	simurgMinimumRatio    = 0.6
	simurgMinimumSeedTime = int64(259200)
)

// NewSimurg builds the indexer (services.Factory shape).
func NewSimurg(deps services.Deps) (indexer.Indexer, error) {
	si := &Simurg{}
	si.Base = indexer.Base{
		IDValue:          "simurg",
		NameValue:        "Simurg",
		DescriptionValue: "Simurg is a Private Torrent Tracker for EBOOKS and AUDIOBOOKS",
		SiteLinkValue:    "https://simurg.world/",
		DefaultSiteLink:  "https://simurg.world/",
		LangValue:        "en-US",
		TypeValue:        "private",
		Caps:             simurgCaps(),
		Client:           deps.Client,
		Saver:            deps.Saver,
		Cache:            deps.Cache,
	}
	// upstream: GazelleTracker(supportsFreeleechTokens: true, useApiKey: true)
	cfg := shared.NewGazelleConfig(shared.GazelleOptions{
		SupportsFreeleechTokens: true,
		UseAPIKey:               true,
	})
	// upstream constructor: configData.AddDynamic("Account Inactivity", ...)
	cfg.AddDynamic("accountinactivity", configdata.NewInfoItem("Account Inactivity", simurgAccountInactivity))
	si.Bind(cfg)
	si.UseAPIKey = true
	si.APIKeyLength = 116 // upstream: protected override int ApiKeyLength => 116
	si.AuthorizationFormat = "token {0}"

	// upstream GetDownloadUrl override (Simurg.cs:55-58)
	si.GetDownloadURL = func(torrentID int, canUseToken bool) string {
		link := si.SiteLink() + "ajax.php?action=download"
		if si.Config.UseToken != nil && si.Config.UseToken.Value && canUseToken {
			link += "&usetoken=1"
		}
		return fmt.Sprintf("%s&id=%d", link, torrentID)
	}
	si.ReleaseInfoPostParse = si.releaseInfoPostParse
	return si, nil
}

func simurgCaps() *models.Capabilities {
	caps := models.NewCapabilities()
	c := caps.Categories
	c.AddCategoryMapping("3", models.CatBooksEBook, "E-Books")
	c.AddCategoryMapping("4", models.CatAudioAudiobook, "Audiobooks")
	c.AddCategoryMapping("7", models.CatBooksComics, "Comics")
	c.AddCategoryMapping("8", models.CatBooksMags, "Magazines")
	return caps
}

// ResultsForQuery implements indexer.Indexer.
func (si *Simurg) ResultsForQuery(ctx context.Context, q *models.TorznabQuery, isMeta bool) (*indexer.IndexerResult, error) {
	return si.ResultsForQueryWith(ctx, q, isMeta, si.performQuery)
}

// Test implements indexer.Indexer.
func (si *Simurg) Test(ctx context.Context) error {
	return si.Base.Test(ctx, si.performQuery)
}

// performQuery ports upstream Simurg.PerformQuery: the base Gazelle query
// plus the proportional-ratio stamps on every release (Simurg.cs:98-109).
func (si *Simurg) performQuery(ctx context.Context, query *models.TorznabQuery) ([]*models.ReleaseInfo, error) {
	releases, err := si.PerformQuery(ctx, query)
	if err != nil {
		return nil, err
	}
	for _, release := range releases {
		// the site has a proportional ratio system calculated using (1) the
		// total amount of data you've downloaded and (2) the total number of
		// torrents you're seeding.
		// So we are going to default the MR to the maximim ratio required to
		// cover the whole range as we cannot calculate this for each user.
		release.MinimumRatio = &simurgMinimumRatio
		release.MinimumSeedTime = &simurgMinimumSeedTime
	}
	return releases, nil
}

// releaseInfoPostParse ports upstream Simurg.ReleaseInfoPostParse
// (Simurg.cs:60-96).
func (si *Simurg) releaseInfoPostParse(release *models.ReleaseInfo, torrent, result map[string]any) bool {
	// Content categories are authoritative: a PDF can be a book, comic, or
	// magazine. Older grouped responses omit them, so retain the format
	// fallback below.
	category := simurgStr(torrent, "category")
	if strings.TrimSpace(category) == "" {
		category = simurgStr(result, "category")
	}
	if strings.TrimSpace(category) != "" {
		mappedCategories := si.Caps.Categories.MapTrackerCatDescToNewznab(category)
		if len(mappedCategories) > 0 {
			release.Category = mappedCategories
			return true
		}
	}

	switch simurgStr(torrent, "format") {
	case "PDF", "MOBI", "EPUB", "AZW3":
		release.Category = []int{models.CatBooksEBook.ID}
	case "CBR", "CBZ":
		release.Category = []int{models.CatBooksComics.ID}
	case "MP3", "M4B":
		release.Category = []int{models.CatAudioAudiobook.ID}
	}

	return true
}

// simurgStr extracts a string value from a decoded JSON object.
func simurgStr(m map[string]any, key string) string {
	if m == nil {
		return ""
	}
	if v, ok := m[key].(string); ok {
		return v
	}
	return ""
}
