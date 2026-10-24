package models

import (
	"net/url"
	"strings"
	"time"
)

// TrackerCacheResult is ReleaseInfo plus cache-view metadata (upstream
// TrackerCacheResult.cs). It is the JSON shape of the manual-search results,
// the Cache view and the potato/torznab UI consumers (backend-api.md §2.6/§3.1).
//
// Ported from: Jackett.Common/Models/TrackerCacheResult.cs.
type TrackerCacheResult struct {
	ReleaseInfo
	FirstSeen    time.Time  `json:"FirstSeen"`
	Tracker      string     `json:"Tracker"`
	TrackerId    string     `json:"TrackerId"`
	TrackerType  string     `json:"TrackerType"`
	CategoryDesc string     `json:"CategoryDesc"`
	BlackholeLink *url.URL  `json:"BlackholeLink"`
}

// NewTrackerCacheResult mirrors the upstream copy constructor: every field
// is copied EXCEPT Languages/Subs (the upstream constructor omits them, so
// the DTO always serialises empty lists) and Peers is reduced by Seeders
// ("use peers as leechers").
func NewTrackerCacheResult(r *ReleaseInfo) *TrackerCacheResult {
	t := &TrackerCacheResult{
		ReleaseInfo: ReleaseInfo{
			Title:                r.Title,
			Guid:                 r.Guid,
			Link:                 r.Link,
			Details:              r.Details,
			PublishDate:          r.PublishDate,
			Category:             r.Category,
			Size:                 r.Size,
			Files:                r.Files,
			Grabs:                r.Grabs,
			Description:          r.Description,
			RageID:               r.RageID,
			TVDBId:               r.TVDBId,
			Imdb:                 r.Imdb,
			TMDb:                 r.TMDb,
			TVMazeId:             r.TVMazeId,
			TraktId:              r.TraktId,
			DoubanId:             r.DoubanId,
			Genres:               r.Genres,
			Languages:            []string{},
			Subs:                 []string{},
			Year:                 r.Year,
			Author:               r.Author,
			BookTitle:            r.BookTitle,
			Publisher:            r.Publisher,
			Artist:               r.Artist,
			Album:                r.Album,
			Label:                r.Label,
			Track:                r.Track,
			Seeders:              r.Seeders,
			Peers:                r.Peers,
			Poster:               r.Poster,
			InfoHash:             r.InfoHash,
			MagnetUri:            r.MagnetUri,
			MinimumRatio:         r.MinimumRatio,
			MinimumSeedTime:      r.MinimumSeedTime,
			DownloadVolumeFactor: r.DownloadVolumeFactor,
			UploadVolumeFactor:   r.UploadVolumeFactor,
			Origin:               r.Origin,
		},
	}
	if t.Category != nil {
		descs := make([]string, 0, len(t.Category))
		for _, c := range t.Category {
			if d := GetCatDesc(c); d != "" {
				descs = append(descs, d)
			}
		}
		t.CategoryDesc = strings.Join(descs, ", ")
	}
	if t.Peers != nil && t.Seeders != nil {
		leechers := *t.Peers - *t.Seeders
		t.Peers = &leechers
	}
	return t
}
