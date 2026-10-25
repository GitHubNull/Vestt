package manual

import (
	"testing"

	"vestt/common/services"
)

// TestBatch1Identity is SPEC-08 acceptance #2: every batch 1 indexer is
// registered with id/name/type/language identical to the upstream
// Jackett 0.24.2900 definitions (tmp/ref/.../Definitions/<Name>.cs).
func TestBatch1Identity(t *testing.T) {
	want := map[string][3]string{
		// id: {name, type, language}
		"alpharatio":     {"AlphaRatio", "private", "en-US"},
		"anidub":         {"AniDUB", "semi-private", "ru-RU"},
		"anilibria":      {"Anilibria", "public", "ru-RU"},
		"animebytes":     {"AnimeBytes", "private", "en-US"},
		"animez":         {"AnimeZ", "private", "en-US"},
		"audiobookbay":   {"AudioBook Bay", "public", "en-US"},
		"avistaz":        {"AvistaZ", "private", "en-US"},
		"bakabt":         {"BakaBT", "private", "en-US"},
		"beyond-hd-api":  {"Beyond-HD (API)", "private", "en-US"},
		"bithdtv":        {"BIT-HDTV", "private", "en-US"},
		"brasiltracker":  {"BrasilTracker", "private", "pt-BR"},
		"broadcasthenet": {"BroadcasTheNet", "private", "en-US"},
		"brokenstones":   {"BrokenStones", "private", "en-US"},
		"cinemaz":        {"CinemaZ", "private", "en-US"},
		"desigaane":      {"DesiGaane", "private", "en-US"},
		"dicmusic":       {"DICMusic", "private", "zh-CN"},
		"divxtotal":      {"DivxTotal", "public", "es-ES"},
		"dontorrent":     {"DonTorrent", "public", "es-ES"},
		"epublibre":      {"EpubLibre", "public", "es-ES"},
		"erai-raws":      {"Erai-Raws", "semi-private", "en-US"},
		"exoticaz":       {"ExoticaZ", "private", "en-US"},
		// SPEC-09 batch 2
		"filelist":        {"FileList", "private", "ro-RO"},
		"gazellegamesapi": {"GazelleGames (API)", "private", "en-US"},
		"greatposterwall": {"GreatPosterWall", "private", "zh-CN"},
		"hdbitsapi":       {"HDBits (API)", "private", "en-US"},
		"hdrtorrent":      {"HDRTorrent", "public", "pt-BR"},
		"immortalseed":    {"ImmortalSeed", "private", "en-US"},
		"iptorrents":      {"IPTorrents", "private", "en-US"},
		"knaben":          {"Knaben", "public", "en-US"},
		"libble":          {"Libble", "private", "en-US"},
		"lostfilm":        {"LostFilm.tv", "semi-private", "ru-RU"},
		"magnetico":       {"Magnetico (Local DHT)", "semi-private", "en-US"},
		"mejortorrent":    {"MejorTorrent", "public", "es-ES"},
		"mteamtp":         {"M-Team - TP", "private", "zh-CN"},
		"myanonamouse":    {"MyAnonamouse", "private", "en-US"},
		"nebulanceapi":    {"NebulanceAPI", "private", "en-US"},
		"norbits":         {"NorBits", "private", "nb-NO"},
		"orpheus":         {"Orpheus", "private", "en-US"},
		"passthepopcorn":  {"PassThePopcorn", "private", "en-US"},
		"pixelhd":         {"PiXELHD", "private", "en-US"},
		"pretome":         {"PreToMe", "private", "en-US"},
		// SPEC-10 batch 3
		"privatehd":       {"PrivateHD", "private", "en-US"},
		"redacted":        {"Redacted", "private", "en-US"},
		"retroflix":       {"RetroFlix", "private", "en-US"},
		"revopeers":       {"RevoPeers", "private", "en-US"},
		"rutracker":       {"RuTracker.org", "semi-private", "ru-RU"},
		"scenehd":         {"SceneHD", "private", "en-US"},
		"secretcinema":    {"Secret Cinema", "private", "en-US"},
		"shazbat":         {"Shazbat", "private", "en-US"},
		"simurg":          {"Simurg", "private", "en-US"},
		"speedapp":        {"SpeedApp", "private", "ro-RO"},
		"speedcd":         {"Speed.cd", "private", "en-US"},
		"subsplease":      {"SubsPlease", "public", "en-US"},
		"toloka":          {"Toloka.to", "semi-private", "uk-UA"},
		"torrentbytes":    {"TorrentBytes", "private", "en-US"},
		"torrentday":      {"TorrentDay", "private", "en-US"},
		"torrentnetwork":  {"Torrent Network", "private", "de-DE"},
		"torrentscsv":     {"Torrents.csv", "public", "en-US"},
		"torrentsyndikat": {"Torrent-Syndikat", "private", "de-DE"},
		"tvstore":         {"TV Store", "private", "hu-HU"},
		"uniotaku":        {"UniOtaku", "semi-private", "pt-BR"},
		"xspeeds":         {"XSpeeds", "private", "en-US"},
	}
	factories := services.RegisteredFactories()
	if len(factories) != len(want) {
		t.Fatalf("registered %d factories, want %d", len(factories), len(want))
	}
	for _, f := range factories {
		w, ok := want[f.ID]
		if !ok {
			t.Fatalf("unexpected factory id %q", f.ID)
		}
		ix, err := f.Factory(services.Deps{})
		if err != nil {
			t.Fatalf("%s: factory error: %v", f.ID, err)
		}
		if ix.Name() != w[0] {
			t.Errorf("%s: name = %q, want %q", f.ID, ix.Name(), w[0])
		}
		if ix.Type() != w[1] {
			t.Errorf("%s: type = %q, want %q", f.ID, ix.Type(), w[1])
		}
		if ix.Language() != w[2] {
			t.Errorf("%s: language = %q, want %q", f.ID, ix.Language(), w[2])
		}
	}
}
