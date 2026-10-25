package manual

import (
	"context"
	"encoding/json"
	"html"
	"strings"
	"time"

	"vestt/common/indexer"
	"vestt/common/indexer/configdata"
	"vestt/common/indexer/shared"
	"vestt/common/models"
	"vestt/common/services"
	"vestt/common/utils/parseutil"
)

// GreatPosterWall ports Jackett.Common/Indexers/Definitions/GreatPosterWall.cs.
//
// mechanism: gazelle (username/password + 2FA cookie; ajax.php browse JSON;
// freeType-based download-volume factors; douban/imdb ids; Chinese-time
// publish dates converted to UTC)
//
// Ported from: Definitions/GreatPosterWall.cs.
//
// 澄清记录:
//   - upstream replaces the "seals" id (Replaces => new[] { "seals" }); the
//     Go Base has no Replaces field (same precedent as AnimeZ), so nothing is
//     carried over — there is no legacy site link for GPW either.
//   - upstream `ReleaseInfoPostParse` assigns `release.Description` only when
//     groupSubName is non-empty (otherwise it stays null); the port only sets
//     the field in that case ("" is the Go zero value either way).
//   - upstream reads the imdb query into SearchTerm and nulls ImdbID so the
//     base class cannot use cataloguenumber/taglist; the port clones the
//     query before mutating it.
type GreatPosterWall struct {
	shared.Gazelle
}

// gpwAccountInactivity mirrors the upstream AddDynamic DisplayInfo item.
const gpwAccountInactivity = "To keep your account active, you should log in and browse the site at least once every 120 days. Simply seeding is not currently considered an active feature of your account, so to keep your account active you can only log in and browse the site. Some scripts or automated tools may help keep your account active, but it's best to log in via your browser from time to time to ensure your account isn't marked as inactive."

// NewGreatPosterWall builds the indexer (services.Factory shape).
func NewGreatPosterWall(deps services.Deps) (indexer.Indexer, error) {
	g := &GreatPosterWall{}
	g.Base = indexer.Base{
		IDValue:          "greatposterwall",
		NameValue:        "GreatPosterWall",
		DescriptionValue: "GreatPosterWall (GPW) is a CHINESE Private site for MOVIES",
		SiteLinkValue:    "https://greatposterwall.com/",
		DefaultSiteLink:  "https://greatposterwall.com/",
		LangValue:        "zh-CN",
		TypeValue:        "private",
		Caps:             gpwCaps(),
		Client:           deps.Client,
		Saver:            deps.Saver,
		Cache:            deps.Cache,
	}
	cfg := shared.NewGazelleConfig(shared.GazelleOptions{
		SupportsFreeleechTokens: true,
		SupportsFreeleechOnly:   true,
		ImdbInTags:              false,
		Has2FA:                  true,
	})
	showFilename := configdata.NewBoolItem("Use the first torrent filename as the title")
	showFilename.Value = false
	cfg.AddDynamic("showFilename", showFilename)
	cfg.AddDynamic("accountinactivity", configdata.NewInfoItem("Account Inactivity", gpwAccountInactivity))
	g.Bind(cfg)
	g.ImdbInTags = false

	g.ReleaseInfoPostParse = func(release *models.ReleaseInfo, torrent, result map[string]any) bool {
		return gpwReleaseInfoPostParse(release, torrent, result, showFilename.Value)
	}
	return g, nil
}

func gpwCaps() *models.Capabilities {
	caps := models.NewCapabilities()
	caps.MovieSearchParams = []string{models.MovieParamQ, models.MovieParamImdbID, models.MovieParamGenre}
	c := caps.Categories
	c.AddCategoryMapping("1", models.CatMovies, "Movies 电影")
	return caps
}

func (g *GreatPosterWall) ResultsForQuery(ctx context.Context, q *models.TorznabQuery, isMeta bool) (*indexer.IndexerResult, error) {
	return g.ResultsForQueryWith(ctx, q, isMeta, g.performQuery)
}

func (g *GreatPosterWall) Test(ctx context.Context) error {
	return g.Base.Test(ctx, g.performQuery)
}

// performQuery ports upstream GreatPosterWall.PerformQuery: GPW uses the
// imdbid in the searchstr so prevent cataloguenumber or taglist search.
func (g *GreatPosterWall) performQuery(ctx context.Context, query *models.TorznabQuery) ([]*models.ReleaseInfo, error) {
	q := query.Clone()
	if q.IsImdbQuery() {
		q.SearchTerm = q.ImdbID
		q.ImdbID = ""
	}
	return g.PerformQuery(ctx, q)
}

// gpwReleaseInfoPostParse ports upstream ReleaseInfoPostParse (override
// release title, douban/imdb ids, freeType download factors, personal
// freeleech, Movie category, +08:00 publish dates).
func gpwReleaseInfoPostParse(release *models.ReleaseInfo, torrent, result map[string]any, showFilename bool) bool {
	groupName := html.UnescapeString(gpwStr(result["groupName"]))
	groupSubName := html.UnescapeString(gpwStr(result["groupSubName"]))
	groupYear := gpwStr(result["groupYear"])

	var title strings.Builder
	title.WriteString(groupName)
	if groupYear != "" && groupYear != "0" {
		title.WriteString(" [" + groupYear + "]")
	}

	var flags []string
	for _, key := range []string{"codec", "source", "resolution", "container", "processing"} {
		if v := gpwStr(torrent[key]); v != "" {
			flags = append(flags, v)
		}
	}
	if len(flags) > 0 {
		title.WriteString(" " + strings.Join(flags, " / "))
	}
	release.Title = title.String()

	// option to overwrite the title with the first torrent filename #13646
	if showFilename {
		release.Title = html.UnescapeString(gpwStr(torrent["fileName"]))
	}

	if douban, ok := parseutil.GetLongFromString(gpwStr(result["doubanId"])); ok {
		release.DoubanId = &douban
	}

	switch gpwStr(torrent["freeType"]) {
	case "11":
		release.DownloadVolumeFactor = gpwF64(0.75)
	case "12":
		release.DownloadVolumeFactor = gpwF64(0.5)
	case "13":
		release.DownloadVolumeFactor = gpwF64(0.25)
	case "1":
		release.DownloadVolumeFactor = gpwF64(0)
	case "2":
		release.DownloadVolumeFactor = gpwF64(0)
		release.UploadVolumeFactor = gpwF64(0)
	}

	if isPersonalFreeleech, ok := torrent["isPersonalFreeleech"].(bool); ok && isPersonalFreeleech {
		release.DownloadVolumeFactor = gpwF64(0)
	}

	if imdbID := gpwStr(result["imdbId"]); imdbID != "" {
		if imdb, ok := parseutil.GetImdbID(imdbID); ok {
			release.Imdb = &imdb
		}
	}

	release.MinimumRatio = gpwF64(1)
	release.MinimumSeedTime = gpwI64(172800) // 48 hours
	// tag each results with Movie cats.
	release.Category = []int{models.CatMovies.ID}
	if groupSubName != "" {
		release.Description = groupSubName
	}

	if t := gpwStr(torrent["time"]); strings.TrimSpace(t) != "" {
		// Time is Chinese Time, add 8 hours difference from UTC
		// (upstream: ParseExact with AdjustToUniversal → UTC instant).
		if parsed, err := time.Parse("2006-01-02 15:04:05 -07:00", t+" +08:00"); err == nil {
			release.PublishDate = parsed.UTC()
		}
	}
	return true
}

// gpwStr mirrors the loose Newtonsoft (string)JToken cast for the GPW
// ajax.php response (strings and numbers both appear).
func gpwStr(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return t
	case json.Number:
		return t.String()
	default:
		return ""
	}
}

func gpwF64(v float64) *float64 { return &v }

func gpwI64(v int64) *int64 { return &v }
