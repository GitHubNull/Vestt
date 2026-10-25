// Package manual holds the hand-written indexers (one file per site).
package manual

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"vestt/common/httpclient"
	"vestt/common/indexer"
	"vestt/common/indexer/configdata"
	"vestt/common/indexer/shared"
	"vestt/common/models"
	"vestt/common/services"
)

// SubsPlease ports Jackett.Common/Indexers/Definitions/SubsPlease.cs.
//
// mechanism: json-api (public; GET api/?f=latest|search with JSON results)
//
// 澄清记录:
//   - the API returns releases as a JSON object keyed by show page; upstream
//     iterates Dictionary.Values in JSON insertion order, the port sorts the
//     keys so results are deterministic (Go maps have no order).
//   - upstream mutates query.Season (sets it to 0 when season > 1 is folded
//     into the search term, SubsPlease.cs:148-152); the port keeps that
//     mutation so GetEpisodeSearchString() no longer appends Sxx afterwards
//     (load-bearing for the MatchQueryStringAND post-filter), while the
//     ResultsForQuery caller's query stays untouched because the base works
//     on a clone (BaseIndexer.cs:355-357).
//   - the search term is processed on query.SearchTerm (NOT
//     GetQueryString()): season/episode are never appended by the base
//     query string, only the explicit " S{season}" for season > 1.
//   - resolution stripping uses string.Replace semantics = ALL occurrences
//     removed (SubsPlease.cs:144).
//   - GetReleaseSize falls back to resolution estimates
//     (SubsPlease.cs:259-266): 1080 → 1.3 GB (int64(1.3*2^30) = 1395864371,
//     the C# (long) cast truncates), 720 → 700 MB, 480 → 350 MB, else 1 GB.
//   - Seeders=1 / Peers=2 / Files=1 / DVF=0 are upstream constants
//     (SubsPlease.cs:212-218).
//
// Ported from: Definitions/SubsPlease.cs.
type SubsPlease struct {
	indexer.Base
}

// NewSubsPlease builds the indexer (services.Factory shape).
func NewSubsPlease(deps services.Deps) (indexer.Indexer, error) {
	sp := &SubsPlease{}
	sp.Base = indexer.Base{
		IDValue:          "subsplease",
		NameValue:        "SubsPlease",
		DescriptionValue: "SubsPlease is a Public site. A better HorribleSubs/Erai replacement",
		SiteLinkValue:    "https://subsplease.org/",
		DefaultSiteLink:  "https://subsplease.org/",
		LegacySiteLinks: []string{
			"https://subsplease.mrunblock.bond/",
			"https://subsplease.nocensor.cloud/",
		},
		LangValue: "en-US",
		TypeValue: "public",
		Caps:      subsPleaseCaps(),
		Config:    newSubsPleaseConfig(),
		Client:    deps.Client,
		Saver:     deps.Saver,
		Cache:     deps.Cache,
	}
	return sp, nil
}

type subsPleaseConfig struct {
	configdata.ConfigData
}

func newSubsPleaseConfig() *subsPleaseConfig {
	return &subsPleaseConfig{ConfigData: *configdata.NewConfigData()}
}

func subsPleaseCaps() *models.Capabilities {
	caps := models.NewCapabilities()
	caps.TVSearchParams = []string{models.TVParamQ, models.TVParamSeason, models.TVParamEp}
	caps.MovieSearchParams = []string{models.MovieParamQ}
	c := caps.Categories
	c.AddCategoryMapping("1", models.CatTVAnime, "")
	c.AddCategoryMapping("2", models.CatMoviesOther, "")
	return caps
}

// ApplyConfiguration ports upstream SubsPlease.ApplyConfiguration
// (SubsPlease.cs:78-87): an empty result set is an error
// ("Could not find releases from this URL").
func (sp *SubsPlease) ApplyConfiguration(ctx context.Context, items []indexer.ConfigItem) (indexer.ConfigStatus, error) {
	if err := sp.ApplyItems(items); err != nil {
		return indexer.ConfigCompleted, err
	}
	if err := sp.LoadValuesFromJSON(nil, false); err != nil {
		return indexer.ConfigCompleted, err
	}
	releases, err := sp.perform(ctx, models.NewTorznabQuery())
	if err != nil {
		sp.SetConfigured(false)
		return indexer.ConfigCompleted, err
	}
	if len(releases) == 0 {
		sp.SetConfigured(false)
		return indexer.ConfigCompleted, errors.New("Could not find releases from this URL") //nolint:staticcheck // upstream SubsPlease.cs error text, kept 1:1
	}
	sp.SetConfigured(true)
	sp.SaveConfig(ctx)
	return indexer.ConfigCompleted, nil
}

func (sp *SubsPlease) ResultsForQuery(ctx context.Context, q *models.TorznabQuery, isMeta bool) (*indexer.IndexerResult, error) {
	return sp.ResultsForQueryWith(ctx, q, isMeta, sp.perform)
}

func (sp *SubsPlease) Test(ctx context.Context) error {
	return sp.Base.Test(ctx, sp.perform)
}

var (
	spResolutionRe   = regexp.MustCompile(`(?i)\d{3,4}p`)
	spSubsPleaseName = regexp.MustCompile(`(?i)\[?subsplease\]?\s*`)
	spSizeRe         = regexp.MustCompile(`(?i)&xl=(\d+)`)
)

// perform ports upstream SubsPleaseRequestGenerator.GetSearchRequests +
// SubsPleaseParser.ParseResponse + the derived-class post-filter
// (SubsPlease.cs:89-107, 121-177, 191-243).
func (sp *SubsPlease) perform(ctx context.Context, query *models.TorznabQuery) ([]*models.ReleaseInfo, error) {
	queryParameters := shared.QueryParams{}
	queryParameters.Add("tz", "UTC")

	if strings.TrimSpace(query.SearchTerm) == "" {
		queryParameters.Set("f", "latest")
	} else {
		// If the search terms contain [SubsPlease] or SubsPlease, remove them
		searchTerm := spSubsPleaseName.ReplaceAllString(query.SearchTerm, "")
		searchTerm = strings.TrimSpace(searchTerm)

		// If the search terms contain a resolution, remove it
		if resolutionMatch := spResolutionRe.FindStringSubmatch(searchTerm); resolutionMatch != nil {
			searchTerm = strings.ReplaceAll(searchTerm, resolutionMatch[0], "")
		}

		// Only include season > 1 in searchTerm, format as S2 rather than S02
		if query.Season != nil && *query.Season > 1 {
			searchTerm += " S" + strconv.Itoa(*query.Season)
			s := 0
			query.Season = &s // upstream: query.Season = 0
		}

		queryParameters.Set("f", "search")
		queryParameters.Set("s", searchTerm)
	}

	searchURL := sp.SiteLink() + "api/?" + queryParameters.GetQueryString()

	if sp.Client == nil {
		return nil, errors.New("http client not configured")
	}
	res, err := sp.Client.Do(ctx, sp.ID(), &httpclient.Request{
		URL:     searchURL,
		Headers: map[string]string{"Accept": "application/json"},
	})
	if err != nil {
		return nil, err
	}
	releases, err := sp.parseResponse(res.Body)
	if err != nil {
		return nil, err
	}

	// Derived-class post-filter (SubsPlease.cs:93-104).
	if strings.TrimSpace(query.SearchTerm) != "" {
		filtered := releases[:0]
		for _, release := range releases {
			if !query.MatchQueryStringAND(release.Title, 0, "") {
				continue
			}
			// If we detected a resolution in the search terms earlier, filter by it
			if resolutionMatch := spResolutionRe.FindString(query.SearchTerm); resolutionMatch != "" {
				if !strings.Contains(strings.ToLower(release.Title), strings.ToLower(resolutionMatch)) {
					continue
				}
			}
			filtered = append(filtered, release)
		}
		releases = filtered
	}

	return releases, nil
}

// parseResponse ports SubsPleaseParser.ParseResponse (SubsPlease.cs:191-243).
func (sp *SubsPlease) parseResponse(body []byte) ([]*models.ReleaseInfo, error) {
	var releases []*models.ReleaseInfo

	// When there are no results, the API returns an empty array or empty
	// response instead of an object.
	content := string(body)
	if strings.TrimSpace(content) == "" || strings.TrimSpace(content) == "[]" {
		return releases, nil
	}

	// Decode loosely to keep the JSON object order handling explicit: the
	// port sorts the show keys (see file header 澄清记录).
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, err
	}
	keys := make([]string, 0, len(raw))
	for k := range raw {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	for _, k := range keys {
		var r subsPleaseRelease
		if err := json.Unmarshal(raw[k], &r); err != nil {
			return nil, err
		}
		for _, d := range r.Downloads {
			files := int64(1)
			seeders := int64(1)
			peers := int64(2)
			minimumRatio := 1.0
			minimumSeedTime := int64(172800) // 48 hours
			dvf := 0.0
			uvf := 1.0

			details, err := url.Parse(sp.SiteLink() + "shows/" + r.Page + "/")
			if err != nil {
				return nil, err
			}
			publishDate, err := spParseReleaseDate(r.ReleaseDate)
			if err != nil {
				return nil, err
			}

			category := []int{models.CatTVAnime.ID}
			if strings.ToLower(r.Episode) == "movie" {
				category = append(category, models.CatMoviesOther.ID)
			}

			var poster *url.URL
			if strings.TrimSpace(r.ImageURL) != "" {
				poster, err = url.Parse(sp.SiteLink() + strings.TrimLeft(r.ImageURL, "/"))
				if err != nil {
					return nil, err
				}
			}

			// Ex: [SubsPlease] Shingeki no Kyojin (The Final Season) - 64 (1080p)
			magnet, err := url.Parse(d.Magnet)
			if err != nil {
				return nil, err
			}
			size := spReleaseSize(d)

			releases = append(releases, &models.ReleaseInfo{
				Title:                "[SubsPlease] " + r.Show + " - " + r.Episode + " (" + d.Resolution + "p)",
				Details:              details,
				PublishDate:          publishDate,
				Files:                &files,
				Category:             category,
				Seeders:              &seeders,
				Peers:                &peers,
				MinimumRatio:         &minimumRatio,
				MinimumSeedTime:      &minimumSeedTime,
				DownloadVolumeFactor: &dvf,
				UploadVolumeFactor:   &uvf,
				Poster:               poster,
				MagnetUri:            magnet,
				Link:                 nil,
				Guid:                 magnet,
				Size:                 &size,
			})
		}
	}

	return releases, nil
}

// spReleaseSize ports SubsPleaseParser.GetReleaseSize (SubsPlease.cs:245-267).
func spReleaseSize(info subsPleaseDownloadInfo) int64 {
	if strings.TrimSpace(info.Magnet) != "" {
		if m := spSizeRe.FindStringSubmatch(info.Magnet); m != nil {
			if releaseSize, err := strconv.ParseInt(m[1], 10, 64); err == nil && releaseSize > 0 {
				return releaseSize
			}
		}
	}

	// The API doesn't tell us file size, so give an estimate based on resolution
	switch info.Resolution {
	case "1080":
		// 1.3.Gigabytes() with the C# (long) truncation: 1395864371
		gb := float64(int64(1) << 30)
		return int64(1.3 * gb)
	case "720":
		return 700 * (1 << 20)
	case "480":
		return 350 * (1 << 20)
	default:
		return 1 << 30
	}
}

// spParseReleaseDate parses the DateTimeOffset release_date and converts it
// to local time (upstream ReleaseDate.LocalDateTime).
func spParseReleaseDate(value string) (time.Time, error) {
	if t, err := time.Parse(time.RFC3339, value); err == nil {
		return t.Local(), nil
	}
	if t, err := time.Parse("2006-01-02 15:04:05", value); err == nil {
		return t.Local(), nil
	}
	return time.Time{}, fmt.Errorf("could not parse release_date %q", value)
}

// --- upstream DTOs ----------------------------------------------------------

type subsPleaseRelease struct {
	Time        string                   `json:"time"`
	ReleaseDate string                   `json:"release_date"`
	Show        string                   `json:"show"`
	Episode     string                   `json:"episode"`
	Downloads   []subsPleaseDownloadInfo `json:"downloads"`
	Xdcc        string                   `json:"xdcc"`
	ImageURL    string                   `json:"image_url"`
	Page        string                   `json:"page"`
}

type subsPleaseDownloadInfo struct {
	Resolution string `json:"res"`
	Magnet     string `json:"magnet"`
}
