package models

import (
	"strconv"
	"strings"
)

// Category is one Torznab category node.
//
// Ported from: Jackett.Common/Models/TorznabCategory.cs and
// TorznabCatType.cs (table transcribed verbatim, including IDs and names).
type Category struct {
	ID            int
	Name          string
	SubCategories []*Category
}

// cat is a compact constructor for the static table.
func cat(id int, name string) *Category { return &Category{ID: id, Name: name} }

// Static category table — one variable per upstream TorznabCatType member.
var (
	CatConsole           = cat(1000, "Console")
	CatConsoleNDS        = cat(1010, "Console/NDS")
	CatConsolePSP        = cat(1020, "Console/PSP")
	CatConsoleWii        = cat(1030, "Console/Wii")
	CatConsoleXBox       = cat(1040, "Console/XBox")
	CatConsoleXBox360    = cat(1050, "Console/XBox 360")
	CatConsoleWiiware    = cat(1060, "Console/Wiiware")
	CatConsoleXBox360DLC = cat(1070, "Console/XBox 360 DLC")
	CatConsolePS3        = cat(1080, "Console/PS3")
	CatConsoleOther      = cat(1090, "Console/Other")
	CatConsole3DS        = cat(1110, "Console/3DS")
	CatConsolePSVita     = cat(1120, "Console/PS Vita")
	CatConsoleWiiU       = cat(1130, "Console/WiiU")
	CatConsoleXBoxOne    = cat(1140, "Console/XBox One")
	CatConsolePS4        = cat(1180, "Console/PS4")

	CatMovies        = cat(2000, "Movies")
	CatMoviesForeign = cat(2010, "Movies/Foreign")
	CatMoviesOther   = cat(2020, "Movies/Other")
	CatMoviesSD      = cat(2030, "Movies/SD")
	CatMoviesHD      = cat(2040, "Movies/HD")
	CatMoviesUHD     = cat(2045, "Movies/UHD")
	CatMoviesBluRay  = cat(2050, "Movies/BluRay")
	CatMovies3D      = cat(2060, "Movies/3D")
	CatMoviesDVD     = cat(2070, "Movies/DVD")
	CatMoviesWEBDL   = cat(2080, "Movies/WEB-DL")

	CatAudio          = cat(3000, "Audio")
	CatAudioMP3       = cat(3010, "Audio/MP3")
	CatAudioVideo     = cat(3020, "Audio/Video")
	CatAudioAudiobook = cat(3030, "Audio/Audiobook")
	CatAudioLossless  = cat(3040, "Audio/Lossless")
	CatAudioOther     = cat(3050, "Audio/Other")
	CatAudioForeign   = cat(3060, "Audio/Foreign")

	CatPC              = cat(4000, "PC")
	CatPC0day          = cat(4010, "PC/0day")
	CatPCISO           = cat(4020, "PC/ISO")
	CatPCMac           = cat(4030, "PC/Mac")
	CatPCMobileOther   = cat(4040, "PC/Mobile-Other")
	CatPCGames         = cat(4050, "PC/Games")
	CatPCMobileiOS     = cat(4060, "PC/Mobile-iOS")
	CatPCMobileAndroid = cat(4070, "PC/Mobile-Android")

	CatTV            = cat(5000, "TV")
	CatTVWEBDL       = cat(5010, "TV/WEB-DL")
	CatTVForeign     = cat(5020, "TV/Foreign")
	CatTVSD          = cat(5030, "TV/SD")
	CatTVHD          = cat(5040, "TV/HD")
	CatTVUHD         = cat(5045, "TV/UHD")
	CatTVOther       = cat(5050, "TV/Other")
	CatTVSport       = cat(5060, "TV/Sport")
	CatTVAnime       = cat(5070, "TV/Anime")
	CatTVDocumentary = cat(5080, "TV/Documentary")

	CatXXX         = cat(6000, "XXX")
	CatXXXDVD      = cat(6010, "XXX/DVD")
	CatXXXWMV      = cat(6020, "XXX/WMV")
	CatXXXXviD     = cat(6030, "XXX/XviD")
	CatXXXx264     = cat(6040, "XXX/x264")
	CatXXXUHD      = cat(6045, "XXX/UHD")
	CatXXXPack     = cat(6050, "XXX/Pack")
	CatXXXImageSet = cat(6060, "XXX/ImageSet")
	CatXXXOther    = cat(6070, "XXX/Other")
	CatXXXSD       = cat(6080, "XXX/SD")
	CatXXXWEBDL    = cat(6090, "XXX/WEB-DL")

	CatBooks          = cat(7000, "Books")
	CatBooksMags      = cat(7010, "Books/Mags")
	CatBooksEBook     = cat(7020, "Books/EBook")
	CatBooksComics    = cat(7030, "Books/Comics")
	CatBooksTechnical = cat(7040, "Books/Technical")
	CatBooksOther     = cat(7050, "Books/Other")
	CatBooksForeign   = cat(7060, "Books/Foreign")

	CatOther       = cat(8000, "Other")
	CatOtherMisc   = cat(8010, "Other/Misc")
	CatOtherHashed = cat(8020, "Other/Hashed")
)

// ParentCats are the eight top-level categories (upstream ParentCats).
var ParentCats = []*Category{
	CatConsole, CatMovies, CatAudio, CatPC, CatTV, CatXXX, CatBooks, CatOther,
}

// allCats is the flat table in upstream declaration order (AllCats).
var allCats = []*Category{
	CatConsole, CatConsoleNDS, CatConsolePSP, CatConsoleWii, CatConsoleXBox,
	CatConsoleXBox360, CatConsoleWiiware, CatConsoleXBox360DLC, CatConsolePS3,
	CatConsoleOther, CatConsole3DS, CatConsolePSVita, CatConsoleWiiU,
	CatConsoleXBoxOne, CatConsolePS4,
	CatMovies, CatMoviesForeign, CatMoviesOther, CatMoviesSD, CatMoviesHD,
	CatMoviesUHD, CatMoviesBluRay, CatMovies3D, CatMoviesDVD, CatMoviesWEBDL,
	CatAudio, CatAudioMP3, CatAudioVideo, CatAudioAudiobook, CatAudioLossless,
	CatAudioOther, CatAudioForeign,
	CatPC, CatPC0day, CatPCISO, CatPCMac, CatPCMobileOther, CatPCGames,
	CatPCMobileiOS, CatPCMobileAndroid,
	CatTV, CatTVWEBDL, CatTVForeign, CatTVSD, CatTVHD, CatTVUHD, CatTVOther,
	CatTVSport, CatTVAnime, CatTVDocumentary,
	CatXXX, CatXXXDVD, CatXXXWMV, CatXXXXviD, CatXXXx264, CatXXXUHD, CatXXXPack,
	CatXXXImageSet, CatXXXOther, CatXXXSD, CatXXXWEBDL,
	CatBooks, CatBooksMags, CatBooksEBook, CatBooksComics, CatBooksTechnical,
	CatBooksOther, CatBooksForeign,
	CatOther, CatOtherMisc, CatOtherHashed,
}

func init() {
	// Sub-category tree, mirroring the upstream static constructor.
	CatConsole.SubCategories = []*Category{
		CatConsoleNDS, CatConsolePSP, CatConsoleWii, CatConsoleXBox,
		CatConsoleXBox360, CatConsoleWiiware, CatConsoleXBox360DLC, CatConsolePS3,
		CatConsoleOther, CatConsole3DS, CatConsolePSVita, CatConsoleWiiU,
		CatConsoleXBoxOne, CatConsolePS4,
	}
	CatMovies.SubCategories = []*Category{
		CatMoviesForeign, CatMoviesOther, CatMoviesSD, CatMoviesHD, CatMoviesUHD,
		CatMoviesBluRay, CatMovies3D, CatMoviesDVD, CatMoviesWEBDL,
	}
	CatAudio.SubCategories = []*Category{
		CatAudioMP3, CatAudioVideo, CatAudioAudiobook, CatAudioLossless,
		CatAudioOther, CatAudioForeign,
	}
	CatPC.SubCategories = []*Category{
		CatPC0day, CatPCISO, CatPCMac, CatPCMobileOther, CatPCGames,
		CatPCMobileiOS, CatPCMobileAndroid,
	}
	CatTV.SubCategories = []*Category{
		CatTVWEBDL, CatTVForeign, CatTVSD, CatTVHD, CatTVUHD, CatTVOther,
		CatTVSport, CatTVAnime, CatTVDocumentary,
	}
	CatXXX.SubCategories = []*Category{
		CatXXXDVD, CatXXXWMV, CatXXXXviD, CatXXXx264, CatXXXUHD, CatXXXPack,
		CatXXXImageSet, CatXXXOther, CatXXXSD, CatXXXWEBDL,
	}
	CatBooks.SubCategories = []*Category{
		CatBooksMags, CatBooksEBook, CatBooksComics, CatBooksTechnical,
		CatBooksOther, CatBooksForeign,
	}
	CatOther.SubCategories = []*Category{CatOtherMisc, CatOtherHashed}
}

// AllCategories returns the flat category table (upstream AllCats).
func AllCategories() []*Category { return allCats }

// GetCatDesc mirrors TorznabCatType.GetCatDesc.
func GetCatDesc(id int) string {
	for _, c := range allCats {
		if c.ID == id {
			return c.Name
		}
	}
	return ""
}

// GetCatByName mirrors TorznabCatType.GetCatByName.
func GetCatByName(name string) *Category {
	for _, c := range allCats {
		if c.Name == name {
			return c
		}
	}
	return nil
}

// Contains reports whether c is cat or one of its direct sub-categories
// (upstream TorznabCategory.Contains).
func (c *Category) Contains(cat *Category) bool {
	if c.ID == cat.ID {
		return true
	}
	for _, sc := range c.SubCategories {
		if sc.ID == cat.ID {
			return true
		}
	}
	return false
}

// ParseTorznabCategories parses a comma-separated category list, skipping
// empty parts (upstream `cat.Split(',')...int.Parse` semantics). Invalid
// entries are skipped (upstream TryParse tolerance, SPEC-03 §3.1.1).
func ParseTorznabCategories(csv string) []int {
	if csv == "" {
		return []int{}
	}
	var out []int
	for _, part := range strings.Split(csv, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if id, err := strconv.Atoi(part); err == nil {
			out = append(out, id)
		}
	}
	if out == nil {
		out = []int{}
	}
	return out
}
