package manual

import "vestt/common/services"

// registry_batch1.go registers the SPEC-08 batch 1 manual indexers (21).
// Imported for side effects from cmd/vestt/main.go.
func init() {
	services.Register("alpharatio", NewAlphaRatio)
	services.Register("anidub", NewAniDUB)
	services.Register("anilibria", NewAnilibria)
	services.Register("animebytes", NewAnimeBytes)
	services.Register("animez", NewAnimeZ)
	services.Register("audiobookbay", NewAudioBookBay)
	services.Register("avistaz", NewAvistaZ)
	services.Register("bakabt", NewBakaBT)
	services.Register("beyond-hd-api", NewBeyondHDAPI)
	services.Register("bithdtv", NewBitHDTV)
	services.Register("brasiltracker", NewBrasilTracker)
	services.Register("broadcasthenet", NewBroadcastheNet)
	services.Register("brokenstones", NewBrokenStones)
	services.Register("cinemaz", NewCinemaZ)
	services.Register("desigaane", NewDesiGaane)
	services.Register("dicmusic", NewDICMusic)
	services.Register("divxtotal", NewDivxTotal)
	services.Register("dontorrent", NewDonTorrent)
	services.Register("epublibre", NewEpubLibre)
	services.Register("erai-raws", NewEraiRaws)
	services.Register("exoticaz", NewExoticaZ)
}
