package manual

import (
	"testing"

	"vestt/common/services"
)

// TestAllManualIndexersRegistered locks the SPEC-10 收口断言: the three
// batches register exactly 62 static manual factories with no duplicate ids.
// Together with the cardigann engine's 582 dynamic definitions (asserted in
// common/engine/load_test.go) and the two meta indexers (aggregate +
// filters, created by IndexerManagerService itself, never through the
// registry), the registry matches the upstream 0.24.2900 indexer list 1:1.
func TestAllManualIndexersRegistered(t *testing.T) {
	factories := services.RegisteredFactories()
	if len(factories) != 62 {
		t.Fatalf("registered %d manual factories, want 62", len(factories))
	}
	ids := make(map[string]bool, len(factories))
	for _, f := range factories {
		if ids[f.ID] {
			t.Fatalf("duplicate factory id %q", f.ID)
		}
		ids[f.ID] = true
	}
	for _, want := range []string{
		// SPEC-08 batch 1 (21)
		"alpharatio", "anidub", "anilibria", "animebytes", "animez",
		"audiobookbay", "avistaz", "bakabt", "beyond-hd-api", "bithdtv",
		"brasiltracker", "broadcasthenet", "brokenstones", "cinemaz",
		"desigaane", "dicmusic", "divxtotal", "dontorrent", "epublibre",
		"erai-raws", "exoticaz",
		// SPEC-09 batch 2 (20)
		"filelist", "gazellegamesapi", "greatposterwall", "hdbitsapi",
		"hdrtorrent", "immortalseed", "iptorrents", "knaben", "libble",
		"lostfilm", "magnetico", "mejortorrent", "mteamtp", "myanonamouse",
		"nebulanceapi", "norbits", "orpheus", "passthepopcorn", "pixelhd",
		"pretome",
		// SPEC-10 batch 3 (21)
		"privatehd", "redacted", "retroflix", "revopeers", "rutracker",
		"scenehd", "secretcinema", "shazbat", "simurg", "speedapp",
		"speedcd", "subsplease", "toloka", "torrentbytes", "torrentday",
		"torrentnetwork", "torrentscsv", "torrentsyndikat", "tvstore",
		"uniotaku", "xspeeds",
	} {
		if !ids[want] {
			t.Fatalf("missing factory %q", want)
		}
	}
	if services.CardigannFactory() != nil {
		t.Fatal("cardigann factory unexpectedly registered in the manual package test binary")
	}
}
