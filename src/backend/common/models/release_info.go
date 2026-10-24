package models

import (
	"net/url"
	"time"
)

// ReleaseInfo is one search result entry.
//
// Ported from: Jackett.Common/Models/ReleaseInfo.cs — field set matches
// backend-api.md §2.6 (TrackerCacheResult JSON shape).
type ReleaseInfo struct {
	Title                string
	Guid                 *url.URL
	Link                 *url.URL
	Details              *url.URL
	PublishDate          time.Time
	Category             []int
	Size                 *int64
	Files                *int64
	Grabs                *int64
	Description          string
	RageID               *int64
	TVDBId               *int64
	Imdb                 *int64
	TMDb                 *int64
	TVMazeId             *int64
	TraktId              *int64
	DoubanId             *int64
	Genres               []string
	Languages            []string
	Subs                 []string
	Year                 *int64
	Author               string
	BookTitle            string
	Publisher            string
	Artist               string
	Album                string
	Label                string
	Track                string
	Seeders              *int64
	Peers                *int64
	Poster               *url.URL
	InfoHash             string
	MagnetUri            *url.URL
	MinimumRatio         *float64
	MinimumSeedTime      *int64
	DownloadVolumeFactor *float64
	UploadVolumeFactor   *float64

	// Origin identifies the producing indexer (upstream [JsonIgnore] —
	// never serialised into TrackerCacheResult JSON). Filled by the indexer
	// layer in SPEC-04.
	Origin ReleaseOrigin `json:"-"`
}

// ReleaseOrigin carries the indexer identity attached to a release
// (upstream ReleaseInfo.Origin, IIndexer reduced to the fields needed for
// RSS output).
type ReleaseOrigin struct {
	ID   string
	Name string
	Type string
}

// GigabytesFromBytes mirrors the upstream helper.
func GigabytesFromBytes(size *int64) *float64 {
	if size == nil {
		return nil
	}
	g := float64(*size) / 1024.0 / 1024.0 / 1024.0
	return &g
}

// Gain mirrors upstream: Seeders * GigabytesFromBytes(Size).
func (r *ReleaseInfo) Gain() *float64 {
	if r.Seeders == nil {
		return nil
	}
	gb := GigabytesFromBytes(r.Size)
	if gb == nil {
		return nil
	}
	g := float64(*r.Seeders) * *gb
	return &g
}

// Clone deep-copies the release (upstream ICloneable.Clone; note the
// upstream copy constructor shares collection references for
// Category/Languages/Subs — the SPEC requires deep copies).
func (r *ReleaseInfo) Clone() *ReleaseInfo {
	ret := *r
	ret.Category = cloneInts(r.Category)
	ret.Genres = cloneStrings(r.Genres)
	ret.Languages = cloneStrings(r.Languages)
	ret.Subs = cloneStrings(r.Subs)
	return &ret
}

func cloneInts(in []int) []int {
	if in == nil {
		return nil
	}
	out := make([]int, len(in))
	copy(out, in)
	return out
}

func cloneStrings(in []string) []string {
	if in == nil {
		return nil
	}
	out := make([]string, len(in))
	copy(out, in)
	return out
}
