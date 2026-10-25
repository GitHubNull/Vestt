// mechanism: json-api
//
// Ported from: Definitions/AnimeZ.cs
//
// AnimeZ is the ex-AnimeTorrents member of the AvistaZ family (replaces the
// "animetorrents" id upstream; the Go Base has no Replaces field, so the
// legacy site link is the only migration hint carried over).
//
// 澄清记录:
//   - upstream throws $"Unknown error in download: {response.ContentBytes}"
//     where ContentBytes is a byte[], so the upstream message literally ends
//     with "System.Byte[]"; the port replicates that verbatim.
package manual

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"vestt/common/httpclient"
	"vestt/common/indexer"
	"vestt/common/indexer/shared"
	"vestt/common/models"
	"vestt/common/services"
)

// AnimeZ ports Jackett.Common/Indexers/Definitions/AnimeZ.cs.
type AnimeZ struct {
	shared.Avistaz
}

// NewAnimeZ builds the indexer (services.Factory shape).
func NewAnimeZ(deps services.Deps) (indexer.Indexer, error) {
	a := &AnimeZ{}
	cfg := shared.NewAvistazConfig("")
	a.Base = indexer.Base{
		IDValue:          "animez",
		NameValue:        "AnimeZ",
		DescriptionValue: "AnimeZ (ex-AnimeTorrents) is a Private Torrent Tracker for ANIME / MANGA",
		SiteLinkValue:    "https://animez.to/",
		DefaultSiteLink:  "https://animez.to/",
		LegacySiteLinks:  []string{"https://animetorrents.me/"},
		LangValue:        "en-US",
		TypeValue:        "private",
		Caps:             animeZCaps(),
		Config:           cfg,
		Client:           deps.Client,
		Saver:            deps.Saver,
		Cache:            deps.Cache,
	}
	a.Bind(cfg)
	a.WireDelay()

	// GetSearchTerm is not overridden upstream; setting the hook to the base
	// behaviour lets the overridden GetSearchQueryParameters call it (the
	// shared base's term helpers are unexported).
	a.GetSearchTerm = func(q *models.TorznabQuery) string {
		return strings.TrimSpace(q.SearchTerm + " " + strings.TrimSpace(q.GetEpisodeSearchString()))
	}
	a.GetSearchQueryParameters = func(q *models.TorznabQuery) shared.QueryParams {
		var qc shared.QueryParams
		qc.Add("limit", "50")

		categoryMappings := avzDistinctStrings(a.Caps.Categories.MapTorznabCapsToTrackers(q, false))
		if len(categoryMappings) > 0 {
			for _, category := range categoryMappings {
				qc.Add("format[]", category)
			}
		}

		qc.Add("search", strings.TrimSpace(a.GetSearchTerm(q)))

		if q.Limit > 0 && q.Offset > 0 {
			page := q.Offset/q.Limit + 1
			qc.Add("page", fmt.Sprintf("%d", page))
		}

		if a.Config.Freeleech.Value {
			qc.Add("freeleech", "1")
		}

		return qc
	}
	a.ParseCategories = func(q *models.TorznabQuery, row *shared.AvistazRelease) ([]int, error) {
		return a.Caps.Categories.MapTrackerCatToNewznab(row.Format), nil
	}
	a.ParseTitle = func(row *shared.AvistazRelease) string {
		if strings.TrimSpace(row.ReleaseTitle) != "" {
			return row.ReleaseTitle
		}
		return row.FileName
	}
	return a, nil
}

func animeZCaps() *models.Capabilities {
	caps := models.NewCapabilities()
	def, max := 50, 50
	caps.LimitsDefault = &def
	caps.LimitsMax = &max
	caps.TVSearchParams = []string{models.TVParamQ, models.TVParamSeason, models.TVParamEp}
	caps.MovieSearchParams = []string{models.MovieParamQ}
	caps.BookSearchParams = []string{models.BookParamQ}

	c := caps.Categories
	c.AddCategoryMapping("TV", models.CatTVAnime, "Anime > TV")
	c.AddCategoryMapping("TV_SHORT", models.CatTVAnime, "Anime > TV Short")
	c.AddCategoryMapping("MOVIE", models.CatMovies, "Anime > Movie")
	c.AddCategoryMapping("SPECIAL", models.CatTVAnime, "Anime > Special")
	c.AddCategoryMapping("OVA", models.CatTVAnime, "Anime > OVA")
	c.AddCategoryMapping("ONA", models.CatTVAnime, "Anime > ONA")
	c.AddCategoryMapping("MUSIC", models.CatTVAnime, "Anime > Music")
	c.AddCategoryMapping("MANGA", models.CatBooksComics, "Manga > Manga")
	c.AddCategoryMapping("NOVEL", models.CatBooksForeign, "Manga > Novel")
	c.AddCategoryMapping("ONE_SHOT", models.CatBooksForeign, "Manga > One-Shot")
	return caps
}

// Download ports upstream AnimeZ.Download: bearer-authorized download with
// token renewal + one retry on 401.
func (a *AnimeZ) Download(ctx context.Context, link *url.URL) ([]byte, error) {
	if strings.TrimSpace(a.AuthorizationToken) == "" {
		if err := a.RenewalToken(ctx); err != nil {
			return nil, err
		}
	}

	res, err := a.Client.Do(ctx, a.ID(), &httpclient.Request{
		URL:     link.String(),
		Headers: a.downloadHeaders(),
		Cookies: a.CookieHeader(),
	})
	if err != nil {
		return nil, err
	}

	if res.StatusCode == http.StatusUnauthorized {
		if err := a.RenewalToken(ctx); err != nil {
			return nil, err
		}
		res, err = a.Client.Do(ctx, a.ID(), &httpclient.Request{
			URL:     link.String(),
			Headers: a.downloadHeaders(),
			Cookies: a.CookieHeader(),
		})
		if err != nil {
			return nil, err
		}
	}

	if res.StatusCode != http.StatusOK {
		// upstream interpolates response.ContentBytes (a byte[]) — the
		// message literally ends with "System.Byte[]".
		return nil, errors.New("Unknown error in download: System.Byte[]") //nolint:staticcheck // upstream AnimeZ.cs error text (incl. C# quirk), kept 1:1
	}

	return res.Body, nil
}

// downloadHeaders mirrors upstream AnimeZ.GetDownloadHeaders.
func (a *AnimeZ) downloadHeaders() map[string]string {
	return map[string]string{
		"Accept":        "application/json",
		"Authorization": "Bearer " + a.AuthorizationToken,
	}
}
