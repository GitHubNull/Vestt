package models

import (
	"encoding/json"
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

// trackerCacheResultJSON is the wire shape of TrackerCacheResult
// (backend-api.md §2.6): PascalCase keys, URL fields as strings
// (upstream serialises System.Uri values via ToString(); null stays null).
type trackerCacheResultJSON struct {
	Title                string    `json:"Title"`
	Guid                 *string   `json:"Guid"`
	Link                 *string   `json:"Link"`
	Details              *string   `json:"Details"`
	PublishDate          time.Time `json:"PublishDate"`
	Category             []int     `json:"Category"`
	Size                 *int64    `json:"Size"`
	Files                *int64    `json:"Files"`
	Grabs                *int64    `json:"Grabs"`
	Description          string    `json:"Description"`
	RageID               *int64    `json:"RageID"`
	TVDBId               *int64    `json:"TVDBId"`
	Imdb                 *int64    `json:"Imdb"`
	TMDb                 *int64    `json:"TMDb"`
	TVMazeId             *int64    `json:"TVMazeId"`
	TraktId              *int64    `json:"TraktId"`
	DoubanId             *int64    `json:"DoubanId"`
	Genres               []string  `json:"Genres"`
	Languages            []string  `json:"Languages"`
	Subs                 []string  `json:"Subs"`
	Year                 *int64    `json:"Year"`
	Author               string    `json:"Author"`
	BookTitle            string    `json:"BookTitle"`
	Publisher            string    `json:"Publisher"`
	Artist               string    `json:"Artist"`
	Album                string    `json:"Album"`
	Label                string    `json:"Label"`
	Track                string    `json:"Track"`
	Seeders              *int64    `json:"Seeders"`
	Peers                *int64    `json:"Peers"`
	Poster               *string   `json:"Poster"`
	InfoHash             string    `json:"InfoHash"`
	MagnetUri            *string   `json:"MagnetUri"`
	MinimumRatio         *float64  `json:"MinimumRatio"`
	MinimumSeedTime      *int64    `json:"MinimumSeedTime"`
	DownloadVolumeFactor *float64  `json:"DownloadVolumeFactor"`
	UploadVolumeFactor   *float64  `json:"UploadVolumeFactor"`
	FirstSeen            time.Time `json:"FirstSeen"`
	Tracker              string    `json:"Tracker"`
	TrackerId            string    `json:"TrackerId"`
	TrackerType          string    `json:"TrackerType"`
	CategoryDesc         string    `json:"CategoryDesc"`
	BlackholeLink        *string   `json:"BlackholeLink"`
}

func urlString(u *url.URL) *string {
	if u == nil {
		return nil
	}
	s := u.String()
	return &s
}

// MarshalJSON emits the documented §2.6 shape (net/url fields would
// otherwise serialise as structs).
func (t TrackerCacheResult) MarshalJSON() ([]byte, error) {
	r := t.ReleaseInfo
	return json.Marshal(trackerCacheResultJSON{
		Title:                r.Title,
		Guid:                 urlString(r.Guid),
		Link:                 urlString(r.Link),
		Details:              urlString(r.Details),
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
		Languages:            r.Languages,
		Subs:                 r.Subs,
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
		Poster:               urlString(r.Poster),
		InfoHash:             r.InfoHash,
		MagnetUri:            urlString(r.MagnetUri),
		MinimumRatio:         r.MinimumRatio,
		MinimumSeedTime:      r.MinimumSeedTime,
		DownloadVolumeFactor: r.DownloadVolumeFactor,
		UploadVolumeFactor:   r.UploadVolumeFactor,
		FirstSeen:            t.FirstSeen,
		Tracker:              t.Tracker,
		TrackerId:            t.TrackerId,
		TrackerType:          t.TrackerType,
		CategoryDesc:         t.CategoryDesc,
		BlackholeLink:        urlString(t.BlackholeLink),
	})
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
