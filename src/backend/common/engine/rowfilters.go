// Row-level filters (SPEC-06 §3.2).
//
// Ported from: Jackett.Common/Indexers/Definitions/CardigannIndexer.cs
// ParseRowFilters (2435-2489). Upstream 0.24.2900 supports only "andmatch"
// and "strdump"; any other filter name is logged as an error.
package engine

import (
	"fmt"
	"strconv"

	"vestt/common/models"
)

// parseRowFilters ports ParseRowFilters: returns true when the row must be
// skipped. rowDebug is the row's pretty rendering (strdump only).
func (x *Cardigann) parseRowFilters(filters []FilterBlock, release *models.ReleaseInfo, q *models.TorznabQuery, vars TemplateVars, rowDebug string) bool {
	skip := false
	caps := x.Caps
	movieImdb := stringSliceContains(caps.MovieSearchParams, models.TVParamImdbID)
	tvImdb := caps.TVSearchImdbAvailable
	movieTmdb := stringSliceContains(caps.MovieSearchParams, models.TVParamTmdbID)
	tvTmdb := stringSliceContains(caps.TVSearchParams, models.TVParamTmdbID)
	tvTvdb := stringSliceContains(caps.TVSearchParams, models.TVParamTvdbID)

	for _, filter := range filters {
		switch filter.Name {
		case "andmatch":
			characterLimit := -1
			if filter.Args != nil {
				if s, ok := filter.Args.(string); ok && s != "" {
					if n, err := strconv.Atoi(s); err == nil {
						characterLimit = n
					}
				}
			}
			// skip andmatch for id searches the indexer supports natively
			if q.ImdbID != "" && (movieImdb || tvImdb) {
				break
			}
			if q.TmdbID != nil && (movieTmdb || tvTmdb) {
				break
			}
			if q.TvdbID != nil && tvTvdb {
				break
			}
			if q.DoubanID != nil && (movieImdb || tvImdb) {
				break
			}
			if q.TraktID != nil && (movieImdb || tvImdb) {
				break
			}
			if q.TvmazeID != nil && tvImdb {
				break
			}
			if q.RageID != nil && tvImdb {
				break
			}

			queryKeywords, _ := vars[".Keywords"].(string)
			if !q.MatchQueryStringAND(release.Title, characterLimit, queryKeywords) {
				x.log().Debug(fmt.Sprintf("CardigannIndexer (%s): skipping %s (andmatch filter)", x.ID(), release.Title))
				skip = true
			}
		case "strdump":
			// for debugging
			x.log().Debug(fmt.Sprintf("CardigannIndexer (%s): row strdump: %s", x.ID(), rowDebug))
		default:
			x.log().Error(fmt.Sprintf("CardigannIndexer (%s): Unsupported rows filter: %s", x.ID(), filter.Name))
		}
	}
	return skip
}

func stringSliceContains(list []string, v string) bool {
	for _, s := range list {
		if s == v {
			return true
		}
	}
	return false
}
