package manual

import "vestt/common/services"

// registry_batch2.go registers the SPEC-09 batch 2 manual indexers (20).
// Picked up via the package's blank import in cmd/vestt/main.go.
func init() {
	services.Register("filelist", NewFileList)
	services.Register("gazellegamesapi", NewGazelleGamesAPI)
	services.Register("greatposterwall", NewGreatPosterWall)
	services.Register("hdbitsapi", NewHDBitsApi)
	services.Register("hdrtorrent", NewHDRTorrent)
	services.Register("immortalseed", NewImmortalSeed)
	services.Register("iptorrents", NewIPTorrents)
	services.Register("knaben", NewKnaben)
	services.Register("libble", NewLibble)
	services.Register("lostfilm", NewLostFilm)
	services.Register("magnetico", NewMagnetico)
	services.Register("mejortorrent", NewMejorTorrent)
	services.Register("mteamtp", NewMTeamTp)
	services.Register("myanonamouse", NewMyAnonamouse)
	services.Register("nebulanceapi", NewNebulanceAPI)
	services.Register("norbits", NewNorBits)
	services.Register("orpheus", NewOrpheus)
	services.Register("passthepopcorn", NewPassThePopcorn)
	services.Register("pixelhd", NewPixelHD)
	services.Register("pretome", NewPreToMe)
}
