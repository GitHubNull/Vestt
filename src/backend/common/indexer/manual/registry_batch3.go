package manual

import "vestt/common/services"

// registry_batch3.go registers the SPEC-10 batch 3 manual indexers (21).
// Picked up via the package's blank import in cmd/vestt/main.go.
func init() {
	services.Register("privatehd", NewPrivateHD)
	services.Register("redacted", NewRedacted)
	services.Register("retroflix", NewRetroFlix)
	services.Register("revopeers", NewRevoPeers)
	services.Register("rutracker", NewRuTracker)
	services.Register("scenehd", NewSceneHD)
	services.Register("secretcinema", NewSecretCinema)
	services.Register("shazbat", NewShazbat)
	services.Register("simurg", NewSimurg)
	services.Register("speedapp", NewSpeedApp)
	services.Register("speedcd", NewSpeedCD)
	services.Register("subsplease", NewSubsPlease)
	services.Register("toloka", NewToloka)
	services.Register("torrentbytes", NewTorrentBytes)
	services.Register("torrentday", NewTorrentDay)
	services.Register("torrentnetwork", NewTorrentNetwork)
	services.Register("torrentscsv", NewTorrentsCSV)
	services.Register("torrentsyndikat", NewTorrentSyndikat)
	services.Register("tvstore", NewTVStore)
	services.Register("uniotaku", NewUniotaku)
	services.Register("xspeeds", NewXSpeeds)
}
