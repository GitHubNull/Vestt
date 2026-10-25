package manual

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	"golang.org/x/net/html"

	"vestt/common/indexer"
	"vestt/common/indexer/shared"
	"vestt/common/models"
	"vestt/common/services"
)

// SecretCinema ports Jackett.Common/Indexers/Definitions/SecretCinema.cs.
//
// mechanism: gazelle-api (Secret Cinema ajax.php JSON API, ratioless tracker,
// media-based movie category refinement, releaseName/year title override)
//
// Ported from: Definitions/SecretCinema.cs.
//
// 澄清记录:
//   - upstream category 3 (E-Books) is commented out upstream
//     (SecretCinema.cs:59-60, "required a refactor in Gazelle abstract");
//     not ported.
//   - upstream re-parses torrent["time"] with a +0200 offset after the base
//     FillReleaseInfoFromJson already parsed it as +0000 (SecretCinema.cs:126-131);
//     the hook re-parses with the same layout and converts to UTC
//     (AdjustToUniversal). A malformed time leaves the base's +0000 parse
//     (upstream would throw → OnParseError; not observable for well-formed
//     tracker responses).
//   - upstream List<int>.Remove removes the first occurrence only
//     (SecretCinema.cs:80-96); scReplaceCategory mirrors that.
//   - upstream PerformQuery filters the base results with
//     query.MatchQueryStringAND(release.Title) (SecretCinema.cs:65-71);
//     ported in performQuery.
type SecretCinema struct {
	shared.Gazelle
}

// scYearRegex mirrors upstream _YearRegex (SecretCinema.cs:30).
var scYearRegex = regexp.MustCompile(`(\b|[-._ ])((?:19|20)\d{2})(\b|[-._ ])`)

var (
	scBDMVRe = regexp.MustCompile(`(?i)\bBDMV\b`)
	scSDRe   = regexp.MustCompile(`(?i)\bSD\b`)
)

// NewSecretCinema builds the indexer (services.Factory shape).
func NewSecretCinema(deps services.Deps) (indexer.Indexer, error) {
	s := &SecretCinema{}
	s.Base = indexer.Base{
		IDValue:          "secretcinema",
		NameValue:        "Secret Cinema",
		DescriptionValue: "Secret Cinema is a Private ratioless site for rare MOVIES.",
		SiteLinkValue:    "https://secret-cinema.pw/",
		DefaultSiteLink:  "https://secret-cinema.pw/",
		LangValue:        "en-US",
		TypeValue:        "private",
		Caps:             secretCinemaCaps(),
		Client:           deps.Client,
		Saver:            deps.Saver,
		Cache:            deps.Cache,
	}
	// upstream: GazelleTracker(supportsFreeleechTokens: false) — ratioless.
	cfg := shared.NewGazelleConfig(shared.GazelleOptions{
		SupportsFreeleechTokens: false,
	})
	s.Bind(cfg)
	s.ReleaseInfoPostParse = s.releaseInfoPostParse
	return s, nil
}

func secretCinemaCaps() *models.Capabilities {
	caps := models.NewCapabilities()
	caps.MovieSearchParams = []string{models.MovieParamQ, models.MovieParamImdbID}
	caps.MusicSearchParams = []string{
		models.MusicParamQ, models.MusicParamAlbum, models.MusicParamArtist,
		models.MusicParamLabel, models.MusicParamYear,
	}
	c := caps.Categories
	c.AddCategoryMapping("1", models.CatMovies, "Movies")
	c.AddCategoryMapping("2", models.CatAudio, "Music")
	// upstream: cat=3 exists but it's required a refactor in Gazelle abstract
	// to make it work — left out upstream (SecretCinema.cs:59-60).
	return caps
}

// ResultsForQuery implements indexer.Indexer.
func (s *SecretCinema) ResultsForQuery(ctx context.Context, q *models.TorznabQuery, isMeta bool) (*indexer.IndexerResult, error) {
	return s.ResultsForQueryWith(ctx, q, isMeta, s.performQuery)
}

// Test implements indexer.Indexer.
func (s *SecretCinema) Test(ctx context.Context) error {
	return s.Base.Test(ctx, s.performQuery)
}

// performQuery ports upstream SecretCinema.PerformQuery: the base Gazelle
// query plus the MatchQueryStringAND title filter.
func (s *SecretCinema) performQuery(ctx context.Context, query *models.TorznabQuery) ([]*models.ReleaseInfo, error) {
	releases, err := s.PerformQuery(ctx, query)
	if err != nil {
		return nil, err
	}
	// results must contain search terms (upstream SecretCinema.cs:69-71)
	var filtered []*models.ReleaseInfo
	for _, release := range releases {
		if query.MatchQueryStringAND(release.Title, 0, "") {
			filtered = append(filtered, release)
		}
	}
	return filtered, nil
}

// releaseInfoPostParse ports upstream SecretCinema.ReleaseInfoPostParse.
func (s *SecretCinema) releaseInfoPostParse(release *models.ReleaseInfo, torrent, result map[string]any) bool {
	media := scStr(torrent, "media")

	switch media {
	case "SD":
		release.Category = scReplaceCategory(release.Category, models.CatMovies.ID, models.CatMoviesSD.ID)
	case "720p", "1080p", "2160p", "4k": // not verified (upstream comment)
		release.Category = scReplaceCategory(release.Category, models.CatMovies.ID, models.CatMoviesHD.ID)
	case "DVD-R":
		release.Category = scReplaceCategory(release.Category, models.CatMovies.ID, models.CatMoviesDVD.ID)
	case "BDMV":
		release.Category = scReplaceCategory(release.Category, models.CatMovies.ID, models.CatMoviesBluRay.ID)
	}

	if scIsAnyMovieCategory(release.Category) {
		releaseName := strings.TrimSpace(scStr(torrent, "releaseName"))

		if releaseName != "" && scYearRegex.MatchString(releaseName) {
			release.Title = html.UnescapeString(releaseName)
		} else {
			title := html.UnescapeString(scStr(result, "groupName"))
			remasterTitle := strings.TrimSpace(scStr(torrent, "remasterTitle"))

			release.Title = strings.TrimSpace(fmt.Sprintf("%s (%s) %s", title, scStr(result, "groupYear"), media))

			if remasterTitle != "" {
				release.Title += " / " + html.UnescapeString(remasterTitle)
			}

			// Replace media formats with standards
			// (upstream SecretCinema.cs:120-122)
			release.Title = scBDMVRe.ReplaceAllString(release.Title, "COMPLETE BLURAY")
			release.Title = scSDRe.ReplaceAllString(release.Title, "DVDRip")
		}
	}

	if t := scStr(torrent, "time"); strings.TrimSpace(t) != "" {
		// upstream: DateTime.ParseExact(time + " +0200",
		// "yyyy-MM-dd HH:mm:ss zzz", InvariantCulture, AdjustToUniversal)
		if parsed, err := time.Parse("2006-01-02 15:04:05 -0700", t+" +0200"); err == nil {
			release.PublishDate = parsed.UTC()
		}
	}

	return true
}

// scReplaceCategory mirrors upstream release.Category.Remove(id).Add(newId):
// the first occurrence of remove is dropped, newId is appended.
func scReplaceCategory(cats []int, remove, add int) []int {
	out := make([]int, 0, len(cats)+1)
	removed := false
	for _, c := range cats {
		if c == remove && !removed {
			removed = true
			continue
		}
		out = append(out, c)
	}
	return append(out, add)
}

// scIsAnyMovieCategory ports upstream IsAnyMovieCategory
// (SecretCinema.cs:136-139).
func scIsAnyMovieCategory(category []int) bool {
	if scContainsInt(category, models.CatMovies.ID) {
		return true
	}
	for _, sub := range models.CatMovies.SubCategories {
		if scContainsInt(category, sub.ID) {
			return true
		}
	}
	return false
}

func scContainsInt(list []int, v int) bool {
	for _, n := range list {
		if n == v {
			return true
		}
	}
	return false
}

// scStr extracts a string value from a decoded JSON object
// (Newtonsoft JObject.GetValue(...)?.Value<string>()).
func scStr(m map[string]any, key string) string {
	if m == nil {
		return ""
	}
	if v, ok := m[key].(string); ok {
		return v
	}
	return ""
}
