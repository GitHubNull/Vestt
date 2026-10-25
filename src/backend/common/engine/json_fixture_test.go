// Port of upstream Jackett.Test/Common/Indexers/CardigannIndexerJsonTests.cs
// TestCardigannJsonAsync: the upstream fixture definition
// (testdata/pages/json-definition1.yml, Jackett.Test/Resources) runs against
// the recorded upstream response (json-response1.json) and must yield the
// same 78 releases with the same key fields. The definition's rows selector
// chains nested :has/:not/:contains filters, which pins the upstream
// _JsonSelectorRegex lookahead semantics (CardigannIndexer.cs:73,2491-2580).
package engine

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"vestt/common/models"
)

func loadFixtureDefinition(t *testing.T, name string) *IndexerDefinition {
	t.Helper()
	defs := map[string]*IndexerDefinition{}
	stats := &LoadStats{}
	loadOne(defs, stats, discardLogger(), func() ([]byte, error) {
		return os.ReadFile(filepath.Join("testdata", "pages", name))
	}, name)
	if len(stats.Skipped) > 0 {
		t.Fatalf("load %s: %v", name, stats.Skipped)
	}
	def := defs["jsondefinition1"]
	if def == nil {
		t.Fatalf("definition jsondefinition1 not loaded")
	}
	return def
}

func TestCardigannJsonDefinition1(t *testing.T) {
	fixture, err := os.ReadFile(filepath.Join("testdata", "pages", "json-response1.json"))
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, string(fixture))
	}))
	t.Cleanup(srv.Close)

	def := loadFixtureDefinition(t, "json-definition1.yml")
	def.Links = []string{srv.URL + "/"}
	x, err := NewCardigann(def, discardLogger())
	if err != nil {
		t.Fatalf("NewCardigann: %v", err)
	}
	x.Client = mustTestClient(t)

	res, err := x.ResultsForQuery(context.Background(), &models.TorznabQuery{
		QueryType:  "search",
		SearchTerm: "1080p",
	}, false)
	if err != nil {
		t.Fatalf("ResultsForQuery: %v", err)
	}
	rels := res.Releases
	if len(rels) != 78 {
		t.Fatalf("got %d releases, want 78", len(rels))
	}

	// per-release expectations mirror upstream's firstRelease asserts
	r0 := rels[0]
	if len(r0.Category) != 2 || r0.Category[0] != 2000 || r0.Category[1] != 100001 {
		t.Errorf("category = %v, want [2000 100001]", r0.Category)
	}
	if r0.Title != "The Eyes of Tammy Faye (2021)  BDRip 1080p AVC ES DD+ 5.1 EN DTSSS 5.1 Subs] HDO" {
		t.Errorf("title = %q", r0.Title)
	}
	if r0.Details == nil || r0.Details.String() != "https://jsondefinition1.com/torrents/24804" {
		t.Errorf("details = %v", r0.Details)
	}
	if r0.Link == nil || r0.Link.String() != "https://jsondefinition1.com/torrent/download/24804.01c887e14d0845f195bc12b31ea27d38" {
		t.Errorf("link = %v", r0.Link)
	}
	if r0.Guid == nil || r0.Guid.String() != r0.Link.String() {
		t.Errorf("guid = %v, want link %v", r0.Guid, r0.Link)
	}
	if r0.MagnetUri != nil {
		t.Errorf("magnet = %v, want nil", r0.MagnetUri)
	}
	if r0.InfoHash != "" {
		t.Errorf("infohash = %q, want empty", r0.InfoHash)
	}
	if r0.Poster == nil || r0.Poster.String() != "https://image.tmdb.org/t/p/w92/iBjkm6oxTPrvNkzr63cmnrpsQPR.jpg" {
		t.Errorf("poster = %v", r0.Poster)
	}
	if r0.PublishDate.Year() != 2021 {
		t.Errorf("year = %d, want 2021", r0.PublishDate.Year())
	}
	if r0.Size == nil || *r0.Size != 17964744495 {
		t.Errorf("size = %v, want 17964744495", r0.Size)
	}
	if r0.Seeders == nil || *r0.Seeders != 27 {
		t.Errorf("seeders = %v, want 27", r0.Seeders)
	}
	if r0.Peers == nil || *r0.Peers != 30 {
		t.Errorf("peers = %v, want 30", r0.Peers)
	}
	if r0.Files == nil || *r0.Files != 1 {
		t.Errorf("files = %v, want 1", r0.Files)
	}
	if r0.Grabs == nil || *r0.Grabs != 29 {
		t.Errorf("grabs = %v, want 29", r0.Grabs)
	}
	if r0.DownloadVolumeFactor == nil || *r0.DownloadVolumeFactor != 1 {
		t.Errorf("dvf = %v, want 1", r0.DownloadVolumeFactor)
	}
	if r0.UploadVolumeFactor == nil || *r0.UploadVolumeFactor != 1 {
		t.Errorf("uvf = %v, want 1", r0.UploadVolumeFactor)
	}
	if r0.MinimumRatio != nil {
		t.Errorf("minratio = %v, want nil", r0.MinimumRatio)
	}
	if r0.MinimumSeedTime == nil || *r0.MinimumSeedTime != 345600 {
		t.Errorf("minseedtime = %v, want 345600", r0.MinimumSeedTime)
	}
	if g := r0.Gain(); g == nil || *g != 451.7362465756014 {
		t.Errorf("gain = %v, want 451.7362465756014", g)
	}
	if r0.Imdb == nil || *r0.Imdb != 9115530 {
		t.Errorf("imdb = %v, want 9115530", r0.Imdb)
	}
	if r0.RageID != nil {
		t.Errorf("rageid = %v, want nil", r0.RageID)
	}
	if r0.TMDb == nil || *r0.TMDb != 601470 {
		t.Errorf("tmdbid = %v, want 601470", r0.TMDb)
	}
	if r0.TVDBId == nil || *r0.TVDBId != 0 {
		t.Errorf("tvdbid = %v, want 0", r0.TVDBId)
	}
}
