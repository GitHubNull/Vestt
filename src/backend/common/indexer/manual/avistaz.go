// mechanism: json-api
//
// Ported from: Definitions/AvistaZ.cs
//
// AvistaZ (AsiaTorrents) extends the AvistaZ-family base with the audio /
// subtitle language multi-selects (upstream ConfigurationDataAvistaZ) and
// the season-less episode search term.
package manual

import (
	"strings"

	"vestt/common/indexer"
	"vestt/common/indexer/configdata"
	"vestt/common/indexer/shared"
	"vestt/common/models"
	"vestt/common/services"
)

// AvistaZ ports Jackett.Common/Indexers/Definitions/AvistaZ.cs.
type AvistaZ struct {
	shared.Avistaz
	cfg *avistaZConfig
}

// avistaZConfig ports upstream ConfigurationDataAvistaZ (base tracker config
// plus the audio/subtitle language multi-selects). The embedded
// shared.AvistazConfig keeps the upstream item order (base items first).
type avistaZConfig struct {
	shared.AvistazConfig
	SearchAudioLanguages    *configdata.MultiSelectItem
	SearchSubtitleLanguages *configdata.MultiSelectItem
	InfoSearchLanguages     *configdata.InfoItem
}

func newAvistaZConfig() *avistaZConfig {
	return &avistaZConfig{
		AvistazConfig:           *shared.NewAvistazConfig(""),
		SearchAudioLanguages:    configdata.NewMultiSelectItem("Search Audio Languages", avistaZAudioLanguageOptions()),
		SearchSubtitleLanguages: configdata.NewMultiSelectItem("Search Subtitle Languages", avistaZSubtitleLanguageOptions()),
		InfoSearchLanguages:     configdata.NewInfoItem("Info Search Languages", "The audio and subtitle language settings are AND searches, e.g. selecting English audio with Basque and Catalan subtitles, will only return results with all three of these."),
	}
}

// NewAvistaZ builds the indexer (services.Factory shape).
func NewAvistaZ(deps services.Deps) (indexer.Indexer, error) {
	a := &AvistaZ{cfg: newAvistaZConfig()}
	a.Config = &a.cfg.AvistazConfig
	a.Base = indexer.Base{
		IDValue:          "avistaz",
		NameValue:        "AvistaZ",
		DescriptionValue: "AvistaZ (AsiaTorrents) is a Private Torrent Tracker for ASIAN MOVIES / TV / GENERAL",
		SiteLinkValue:    "https://avistaz.to/",
		DefaultSiteLink:  "https://avistaz.to/",
		LangValue:        "en-US",
		TypeValue:        "private",
		Caps:             avistaZCaps(),
		// Base.Config points at the full bespoke config so the posted
		// language values survive the ApplyConfiguration round-trip.
		Config: a.cfg,
		Client: deps.Client,
		Saver:  deps.Saver,
		Cache:  deps.Cache,
	}
	a.WireDelay()

	// upstream: append language[] / subtitle[] to the base parameters.
	a.GetSearchQueryParameters = func(q *models.TorznabQuery) shared.QueryParams {
		qc := a.DefaultSearchQueryParameters(q)

		for _, languageID := range avzDistinctStrings(a.cfg.SearchAudioLanguages.Values) {
			qc.Add("language[]", languageID)
		}

		for _, languageID := range avzDistinctStrings(a.cfg.SearchSubtitleLanguages.Values) {
			qc.Add("subtitle[]", languageID)
		}

		return qc
	}

	// upstream: Avistaz has episodes without season. eg Running Man E323
	a.GetEpisodeSearchTerm = func(q *models.TorznabQuery) string {
		if (q.Season == nil || *q.Season == 0) && strings.TrimSpace(q.Episode) != "" {
			return "E" + q.Episode
		}
		return q.GetEpisodeSearchString()
	}
	return a, nil
}

func avistaZCaps() *models.Capabilities {
	caps := models.NewCapabilities()
	def, max := 50, 50
	caps.LimitsDefault = &def
	caps.LimitsMax = &max
	caps.TVSearchParams = []string{
		models.TVParamQ, models.TVParamSeason, models.TVParamEp,
		models.TVParamImdbID, models.TVParamTvdbID, models.TVParamGenre,
	}
	caps.MovieSearchParams = []string{
		models.MovieParamQ, models.MovieParamImdbID, models.MovieParamTmdbID, models.MovieParamGenre,
	}
	caps.SupportsRawSearch = true
	caps.TVSearchImdbAvailable = true

	c := caps.Categories
	c.AddCategoryMapping("1", models.CatMovies, "")
	c.AddCategoryMapping("1", models.CatMoviesUHD, "")
	c.AddCategoryMapping("1", models.CatMoviesHD, "")
	c.AddCategoryMapping("1", models.CatMoviesSD, "")
	c.AddCategoryMapping("2", models.CatTV, "")
	c.AddCategoryMapping("2", models.CatTVUHD, "")
	c.AddCategoryMapping("2", models.CatTVHD, "")
	c.AddCategoryMapping("2", models.CatTVSD, "")
	c.AddCategoryMapping("3", models.CatAudio, "")
	return caps
}

// --- local replicas of unexported shared helpers ---------------------------

// avzDistinctStrings mirrors upstream .Distinct() (order preserved); shared's
// copy is unexported.
func avzDistinctStrings(list []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(list))
	for _, s := range list {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

// avzFirstIfSingleOrDefault mirrors upstream FirstIfSingleOrDefault; used by
// the ExoticaZ port (shared's copy is unexported).
func avzFirstIfSingleOrDefault(list []string, def string) string {
	if len(list) == 1 {
		return list[0]
	}
	return def
}

// --- language option lists (upstream ConfigurationDataAvistaZ) --------------

func avistaZAudioLanguageOptions() []configdata.Option {
	return []configdata.Option{
		{Value: "7", Label: "Arabic"}, {Value: "10", Label: "Assamese"}, {Value: "17", Label: "Basque"}, {Value: "19", Label: "Bengali"},
		{Value: "23", Label: "Bosnian"}, {Value: "189", Label: "Brazilian Portuguese"}, {Value: "26", Label: "Burmese"},
		{Value: "27", Label: "Cantonese"}, {Value: "28", Label: "Catalan"}, {Value: "29", Label: "Central Khmer"},
		{Value: "30", Label: "Chamorro"}, {Value: "33", Label: "Chinese"}, {Value: "39", Label: "Croatian"}, {Value: "40", Label: "Czech"},
		{Value: "41", Label: "Danish"}, {Value: "43", Label: "Dutch"}, {Value: "44", Label: "Dzongkha"}, {Value: "45", Label: "English"},
		{Value: "46", Label: "Esperanto"}, {Value: "188", Label: "Filipino"}, {Value: "51", Label: "Finnish"}, {Value: "52", Label: "French"},
		{Value: "58", Label: "German"}, {Value: "59", Label: "Greek"}, {Value: "61", Label: "Gujarati"}, {Value: "63", Label: "Hausa"},
		{Value: "64", Label: "Hebrew"}, {Value: "66", Label: "Hindi"}, {Value: "68", Label: "Hungarian"}, {Value: "72", Label: "Indonesian"},
		{Value: "75", Label: "Inuktitut"}, {Value: "78", Label: "Italian"}, {Value: "79", Label: "Japanese"}, {Value: "80", Label: "Javanese"},
		{Value: "82", Label: "Kannada"}, {Value: "84", Label: "Kashmiri"}, {Value: "85", Label: "Kazakh"}, {Value: "88", Label: "Kirghiz"},
		{Value: "91", Label: "Korean"}, {Value: "94", Label: "Lao"}, {Value: "104", Label: "Malay"}, {Value: "105", Label: "Malayalam"},
		{Value: "107", Label: "Mandarin"}, {Value: "110", Label: "Marathi"}, {Value: "112", Label: "Mongolian"}, {Value: "118", Label: "Nepali"},
		{Value: "123", Label: "Ojibwa"}, {Value: "124", Label: "Oriya"}, {Value: "127", Label: "Pali"}, {Value: "128", Label: "Panjabi"},
		{Value: "129", Label: "Persian"}, {Value: "130", Label: "Polish"}, {Value: "131", Label: "Portuguese"}, {Value: "133", Label: "Quechua"},
		{Value: "134", Label: "Romanian"}, {Value: "137", Label: "Russian"}, {Value: "140", Label: "Sanskrit"}, {Value: "146", Label: "Sinhala"},
		{Value: "147", Label: "Slovak"}, {Value: "151", Label: "Spanish"}, {Value: "155", Label: "Swedish"}, {Value: "156", Label: "Tagalog"},
		{Value: "159", Label: "Tamil"}, {Value: "161", Label: "Telugu"}, {Value: "162", Label: "Thai"}, {Value: "163", Label: "Tibetan"},
		{Value: "168", Label: "Turkish"}, {Value: "171", Label: "Uighur"}, {Value: "172", Label: "Ukrainian"}, {Value: "173", Label: "Urdu"},
		{Value: "176", Label: "Vietnamese"}, {Value: "182", Label: "Xhosa"},
	}
}

func avistaZSubtitleLanguageOptions() []configdata.Option {
	return []configdata.Option{
		{Value: "1", Label: "Abkhazian"}, {Value: "2", Label: "Afar"}, {Value: "3", Label: "Afrikaans"}, {Value: "5", Label: "Albanian"},
		{Value: "6", Label: "Amharic"}, {Value: "7", Label: "Arabic"}, {Value: "17", Label: "Basque"}, {Value: "18", Label: "Belarusian"},
		{Value: "19", Label: "Bengali"}, {Value: "23", Label: "Bosnian"}, {Value: "189", Label: "Brazilian Portuguese"},
		{Value: "25", Label: "Bulgarian"}, {Value: "26", Label: "Burmese"}, {Value: "27", Label: "Cantonese"}, {Value: "28", Label: "Catalan"},
		{Value: "29", Label: "Central Khmer"}, {Value: "32", Label: "Chichewa"}, {Value: "33", Label: "Chinese"}, {Value: "37", Label: "Corsican"},
		{Value: "39", Label: "Croatian"}, {Value: "40", Label: "Czech"}, {Value: "41", Label: "Danish"}, {Value: "43", Label: "Dutch"},
		{Value: "45", Label: "English"}, {Value: "46", Label: "Esperanto"}, {Value: "47", Label: "Estonian"}, {Value: "50", Label: "Fijian"},
		{Value: "188", Label: "Filipino"}, {Value: "51", Label: "Finnish"}, {Value: "52", Label: "French"}, {Value: "55", Label: "Galician"},
		{Value: "57", Label: "Georgian"}, {Value: "58", Label: "German"}, {Value: "59", Label: "Greek"}, {Value: "61", Label: "Gujarati"},
		{Value: "62", Label: "Haitian"}, {Value: "64", Label: "Hebrew"}, {Value: "66", Label: "Hindi"}, {Value: "68", Label: "Hungarian"},
		{Value: "69", Label: "Icelandic"}, {Value: "72", Label: "Indonesian"}, {Value: "77", Label: "Irish"}, {Value: "78", Label: "Italian"},
		{Value: "79", Label: "Japanese"}, {Value: "80", Label: "Javanese"}, {Value: "82", Label: "Kannada"}, {Value: "85", Label: "Kazakh"},
		{Value: "91", Label: "Korean"}, {Value: "93", Label: "Kurdish"}, {Value: "94", Label: "Lao"}, {Value: "95", Label: "Latin"},
		{Value: "96", Label: "Latvian"}, {Value: "99", Label: "Lithuanian"}, {Value: "102", Label: "Macedonian"}, {Value: "103", Label: "Malagasy"},
		{Value: "104", Label: "Malay"}, {Value: "105", Label: "Malayalam"}, {Value: "107", Label: "Mandarin"}, {Value: "109", Label: "Maori"},
		{Value: "110", Label: "Marathi"}, {Value: "112", Label: "Mongolian"}, {Value: "118", Label: "Nepali"}, {Value: "120", Label: "Norwegian"},
		{Value: "22", Label: "Norwegian Bokmål"}, {Value: "124", Label: "Oriya"}, {Value: "128", Label: "Panjabi"}, {Value: "129", Label: "Persian"},
		{Value: "130", Label: "Polish"}, {Value: "131", Label: "Portuguese"}, {Value: "134", Label: "Romanian"}, {Value: "135", Label: "Romansh"},
		{Value: "137", Label: "Russian"}, {Value: "141", Label: "Sardinian"}, {Value: "142", Label: "Serbian"}, {Value: "146", Label: "Sinhala"},
		{Value: "147", Label: "Slovak"}, {Value: "148", Label: "Slovenian"}, {Value: "149", Label: "Somali"}, {Value: "151", Label: "Spanish"},
		{Value: "152", Label: "Sundanese"}, {Value: "153", Label: "Swahili"}, {Value: "155", Label: "Swedish"}, {Value: "156", Label: "Tagalog"},
		{Value: "159", Label: "Tamil"}, {Value: "161", Label: "Telugu"}, {Value: "162", Label: "Thai"}, {Value: "168", Label: "Turkish"},
		{Value: "170", Label: "Twi"}, {Value: "172", Label: "Ukrainian"}, {Value: "173", Label: "Urdu"}, {Value: "175", Label: "Venda"},
		{Value: "176", Label: "Vietnamese"}, {Value: "177", Label: "Volapük"}, {Value: "179", Label: "Welsh"}, {Value: "181", Label: "Wolof"},
	}
}
