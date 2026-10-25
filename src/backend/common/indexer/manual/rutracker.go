// Package manual holds the hand-written indexers (one file per site).
package manual

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
	"golang.org/x/text/encoding/htmlindex"

	"vestt/common/httpclient"
	"vestt/common/indexer"
	"vestt/common/indexer/configdata"
	"vestt/common/indexer/shared"
	"vestt/common/models"
	"vestt/common/services"
	"vestt/common/utils/parseutil"
)

// RuTracker ports Jackett.Common/Indexers/Definitions/RuTracker.cs.
//
// mechanism: html-login (username/password POST with optional captcha;
// session cookie is verified on every search via the logged-in-username
// marker and transparently refreshed by re-login)
//
// Ported from: Definitions/RuTracker.cs.
type RuTracker struct {
	indexer.Base
	cfg *rutrackerConfig

	// captcha state fetched by ConfigurationForSetup and consumed by
	// ApplyConfiguration (upstream _capSid/_capCodeField fields).
	capSid       string
	capCodeField string
}

// rutrackerConfig ports upstream ConfigurationDataRutracker
// (ConfigurationDataCaptchaLogin + SearchByUploader/UseMagnetLinks/title
// options). Field order matches the upstream JsonPropertyOrder so the
// setup form renders identically.
type rutrackerConfig struct {
	configdata.ConfigData
	Username          *configdata.StringItem
	Password          *configdata.StringItem
	CaptchaImage      *configdata.ImageItem
	CaptchaText       *configdata.StringItem
	CaptchaCookie     *configdata.HiddenItem
	Instructions      *configdata.InfoItem
	SearchByUploader  *configdata.StringItem
	SearchByUploaderI *configdata.InfoItem
	UseMagnetLinks    *configdata.BoolItem
	StripRussian      *configdata.BoolItem
	AddRussianToTitle *configdata.BoolItem
	MoveTagsInfo      *configdata.InfoItem
	MoveFirstTags     *configdata.BoolItem
	MoveAllTags       *configdata.BoolItem
	CaptchaWarning    *configdata.InfoItem
}

const (
	rtSearchByUploaderInfo = "<b>About searching by Uploader (Author):</b> You can search by Uploader (Author) by entering an Author username, or leave empty to get all results."
	rtMoveTagsInfo         = "<b>About moving tags:</b> We define a tag as a part of the release title between round or square brackets. If the release title contains tags then these options will move those tags and their brackets to the end of the release title. Moving only the first tags will try to detect where the actual title of the release begins, and move only the tags that are found before that point. Enabling both options will enable moving of all tags."
	rtCaptchaInfo          = "<b>About Captcha:</b> If the Captcha Image is missing then leave the Captcha Text empty."
	rtAddRussianToTitle    = "Add RUS to end of all titles to improve language detection by Sonarr and Radarr. Will cause English-only results to be misidentified."
)

func newRutrackerConfig() *rutrackerConfig {
	return &rutrackerConfig{
		ConfigData:        *configdata.NewConfigData(),
		Username:          configdata.NewStringItem("Username"),
		Password:          configdata.NewStringItem("Password"),
		CaptchaImage:      configdata.NewImageItem("Captcha Image"),
		CaptchaText:       configdata.NewStringItem("Captcha Text"),
		CaptchaCookie:     configdata.NewHiddenItem("Captcha Cookie"),
		Instructions:      configdata.NewInfoItem("", ""),
		SearchByUploader:  configdata.NewStringItem("Search By Uploader"),
		SearchByUploaderI: configdata.NewInfoItem("Search By Uploader Info", rtSearchByUploaderInfo),
		UseMagnetLinks:    configdata.NewBoolItem("Use Magnet Links"),
		StripRussian:      configdata.NewBoolItem("Strip Russian Letters"),
		AddRussianToTitle: configdata.NewBoolItem(rtAddRussianToTitle),
		MoveTagsInfo:      configdata.NewInfoItem("Move Tags Info", rtMoveTagsInfo),
		MoveFirstTags:     configdata.NewBoolItem("Move first tags to end of release title"),
		MoveAllTags:       configdata.NewBoolItem("Move all tags to end of release title"),
		CaptchaWarning:    configdata.NewInfoItem("Captcha Info", rtCaptchaInfo),
	}
}

// NewRuTracker builds the indexer (services.Factory shape).
func NewRuTracker(deps services.Deps) (indexer.Indexer, error) {
	cfg := newRutrackerConfig()
	// upstream defaults: Strip Russian Letters = true, everything else false.
	cfg.StripRussian.Value = true
	rt := &RuTracker{cfg: cfg}
	rt.Base = indexer.Base{
		IDValue:          "rutracker",
		NameValue:        "RuTracker.org",
		DescriptionValue: "RuTracker.org is a RUSSIAN Semi-Private site with a thriving file-sharing community",
		SiteLinkValue:    "https://rutracker.org/",
		DefaultSiteLink:  "https://rutracker.org/",
		AltSiteLinks:     []string{"https://rutracker.org/", "https://rutracker.net/"},
		LegacySiteLinks:  []string{"https://rutracker.nl/"},
		LangValue:        "ru-RU",
		TypeValue:        "semi-private",
		EncodingName:     "windows-1251",
		Caps:             rutrackerCaps(),
		Config:           cfg,
		Client:           deps.Client,
		Saver:            deps.Saver,
		Cache:            deps.Cache,
	}
	return rt, nil
}

func rutrackerCaps() *models.Capabilities {
	caps := models.NewCapabilities()
	caps.TVSearchParams = []string{models.TVParamQ, models.TVParamSeason, models.TVParamEp}
	caps.MovieSearchParams = []string{models.MovieParamQ}
	caps.MusicSearchParams = []string{models.MusicParamQ}
	caps.BookSearchParams = []string{models.BookParamQ}
	caps.SupportsRawSearch = true
	c := caps.Categories
	for _, m := range rutrackerCategoryMappings {
		c.AddCategoryMapping(strconv.Itoa(m.Tracker), m.Torznab, m.Desc)
	}
	return caps
}

// rutrackerCategoryMapping is one AddCategoryMapping row
// (forum id → Torznab category + forum description).
type rutrackerCategoryMapping struct {
	Tracker int
	Torznab *models.Category
	Desc    string
}

// rutrackerCategoryMappings is the upstream SetCapabilities() table
// transcribed verbatim from Definitions/RuTracker.cs (RuTracker.cs lines
// 88–1412), preserving declaration order, IDs and descriptions.
var rutrackerCategoryMappings = []rutrackerCategoryMapping{
	{22, models.CatMovies, "Наше кино"},
	{941, models.CatMovies, "|- Кино СССР"},
	{1666, models.CatMovies, "|- Детские отечественные фильмы"},
	{376, models.CatMovies, "|- Авторские дебюты"},
	{106, models.CatMovies, "|- Фильмы России и СССР на национальных языках [без перевода]"},
	{7, models.CatMoviesForeign, "Зарубежное кино"},
	{187, models.CatMoviesForeign, "|- Классика мирового кинематографа"},
	{2090, models.CatMoviesForeign, "|- Фильмы до 1990 года"},
	{2221, models.CatMoviesForeign, "|- Фильмы 1991-2000"},
	{2091, models.CatMoviesForeign, "|- Фильмы 2001-2005"},
	{2092, models.CatMoviesForeign, "|- Фильмы 2006-2010"},
	{2093, models.CatMoviesForeign, "|- Фильмы 2011-2015"},
	{2200, models.CatMoviesForeign, "|- Фильмы 2016-2020"},
	{1950, models.CatMoviesForeign, "|- Фильмы 2021-2025"},
	{252, models.CatMoviesForeign, "|- Фильмы 2026"},
	{2540, models.CatMoviesForeign, "|- Фильмы ближнего зарубежья"},
	{934, models.CatMoviesForeign, "|- Азиатские фильмы"},
	{505, models.CatMoviesForeign, "|- Индийское кино"},
	{212, models.CatMoviesForeign, "|- Сборники фильмов"},
	{2459, models.CatMoviesForeign, "|- Короткий метр"},
	{1235, models.CatMoviesForeign, "|- Грайндхаус"},
	{166, models.CatMoviesForeign, "|- Зарубежные фильмы без перевода"},
	{185, models.CatAudio, "|- Звуковые дорожки и Переводы"},
	{124, models.CatMoviesOther, "Арт-хаус и авторское кино"},
	{1543, models.CatMoviesOther, "|- Короткий метр (Арт-хаус и авторское кино)"},
	{709, models.CatMoviesOther, "|- Документальные фильмы (Арт-хаус и авторское кино)"},
	{1577, models.CatMoviesOther, "|- Анимация (Арт-хаус и авторское кино)"},
	{511, models.CatTVOther, "Театр"},
	{1493, models.CatTVOther, "|- Спектакли без перевода"},
	{93, models.CatMoviesDVD, "DVD Video"},
	{905, models.CatMoviesDVD, "|- Классика мирового кинематографа (DVD Video)"},
	{101, models.CatMoviesDVD, "|- Зарубежное кино (DVD Video)"},
	{100, models.CatMoviesDVD, "|- Наше кино (DVD Video)"},
	{877, models.CatMoviesDVD, "|- Фильмы Ближнего Зарубежья (DVD Video)"},
	{1576, models.CatMoviesDVD, "|- Азиатские фильмы (DVD Video)"},
	{572, models.CatMoviesDVD, "|- Арт-хаус и авторское кино (DVD Video)"},
	{2220, models.CatMoviesDVD, "|- Индийское кино (DVD Video)"},
	{1670, models.CatMoviesDVD, "|- Грайндхаус (DVD Video)"},
	{2198, models.CatMoviesHD, "HD Video"},
	{2199, models.CatMoviesHD, "|- Классика мирового кинематографа (HD Video)"},
	{313, models.CatMoviesHD, "|- Зарубежное кино (HD Video)"},
	{312, models.CatMoviesHD, "|- Наше кино (HD Video)"},
	{1247, models.CatMoviesHD, "|- Фильмы Ближнего Зарубежья (HD Video)"},
	{2201, models.CatMoviesHD, "|- Азиатские фильмы (HD Video)"},
	{2339, models.CatMoviesHD, "|- Арт-хаус и авторское кино (HD Video)"},
	{140, models.CatMoviesHD, "|- Индийское кино (HD Video)"},
	{194, models.CatMoviesHD, "|- Грайндхаус (HD Video)"},
	{718, models.CatMoviesUHD, "UHD Video"},
	{775, models.CatMoviesUHD, "|- Классика мирового кинематографа (UHD Video)"},
	{1457, models.CatMoviesUHD, "|- Зарубежное кино (UHD Video)"},
	{1940, models.CatMoviesUHD, "|- Наше кино (UHD Video)"},
	{272, models.CatMoviesUHD, "|- Азиатские фильмы (UHD Video)"},
	{271, models.CatMoviesUHD, "|- Арт-хаус и авторское кино (UHD Video)"},
	{352, models.CatMovies3D, "3D/Стерео Кино, Видео, TV и Спорт"},
	{549, models.CatMovies3D, "|- 3D Кинофильмы"},
	{1213, models.CatMovies3D, "|- 3D Мультфильмы"},
	{2109, models.CatMovies3D, "|- 3D Документальные фильмы"},
	{514, models.CatMovies3D, "|- 3D Спорт"},
	{2097, models.CatMovies3D, "|- 3D Ролики, Музыкальное видео, Трейлеры к фильмам"},
	{4, models.CatMovies, "Мультфильмы"},
	{84, models.CatMoviesUHD, "|- Мультфильмы (UHD Video)"},
	{2343, models.CatMoviesHD, "|- Отечественные мультфильмы (HD Video)"},
	{930, models.CatMoviesHD, "|- Иностранные мультфильмы (HD Video)"},
	{2365, models.CatMoviesHD, "|- Иностранные короткометражные мультфильмы (HD Video)"},
	{1900, models.CatMoviesDVD, "|- Отечественные мультфильмы (DVD)"},
	{2258, models.CatMoviesDVD, "|- Иностранные короткометражные мультфильмы (DVD)"},
	{521, models.CatMoviesDVD, "|- Иностранные мультфильмы (DVD)"},
	{208, models.CatMovies, "|- Отечественные мультфильмы"},
	{539, models.CatMovies, "|- Отечественные полнометражные мультфильмы"},
	{2183, models.CatMoviesForeign, "|- Мультфильмы Ближнего Зарубежья"},
	{209, models.CatMoviesForeign, "|- Иностранные мультфильмы"},
	{484, models.CatMoviesForeign, "|- Иностранные короткометражные мультфильмы"},
	{822, models.CatMovies, "|- Сборники мультфильмов"},
	{181, models.CatMovies, "|- Мультфильмы без перевода"},
	{921, models.CatTV, "Мультсериалы"},
	{815, models.CatTVSD, "|- Мультсериалы (SD Video)"},
	{816, models.CatTVHD, "|- Мультсериалы (DVD Video)"},
	{1460, models.CatTVHD, "|- Мультсериалы (HD Video)"},
	{498, models.CatTVUHD, "|- Мультсериалы (UHD Video)"},
	{33, models.CatTVAnime, "Аниме"},
	{1106, models.CatTVAnime, "|- Онгоинги (HD Video)"},
	{1105, models.CatTVAnime, "|- Аниме (HD Video)"},
	{599, models.CatTVAnime, "|- Аниме (DVD Video)"},
	{1389, models.CatTVAnime, "|- Аниме (SD Video)"},
	{1391, models.CatTVAnime, "|- Аниме (плеерный подраздел)"},
	{2491, models.CatTVAnime, "|- Аниме (QC подраздел)"},
	{2544, models.CatTVAnime, "|- Ван-Пис"},
	{1642, models.CatTVAnime, "|- Гандам"},
	{1390, models.CatTVAnime, "|- Наруто"},
	{404, models.CatTVAnime, "|- Покемоны"},
	{1277, models.CatTVAnime, "|- Дунхуа и Эни"},
	{809, models.CatAudio, "|- Звуковые дорожки (Аниме)"},
	{2484, models.CatTVAnime, "|- Артбуки и журналы (Аниме)"},
	{1386, models.CatTVAnime, "|- Обои, сканы, аватары, арт"},
	{1387, models.CatTVAnime, "|- AMV и другие ролики"},
	{9, models.CatTV, "Русские сериалы"},
	{812, models.CatTVUHD, "|- Русские сериалы (UHD Video)"},
	{81, models.CatTVHD, "|- Русские сериалы (HD Video)"},
	{920, models.CatTVSD, "|- Русские сериалы (DVD Video)"},
	{80, models.CatTV, "|- Сельский детектив"},
	{1535, models.CatTV, "|- По законам военного времени"},
	{188, models.CatTV, "|- Московские тайны"},
	{91, models.CatTV, "|- Я знаю твои секреты"},
	{990, models.CatTV, "|- Универ / Универ. Новая общага / СашаТаня"},
	{1408, models.CatTV, "|- Женская версия"},
	{175, models.CatTV, "|- След"},
	{79, models.CatTV, "|- Некрасивая подружка"},
	{104, models.CatTV, "|- Психология преступления"},
	{189, models.CatTVForeign, "Зарубежные сериалы"},
	{842, models.CatTVForeign, "|- Новинки и сериалы в стадии показа"},
	{235, models.CatTVForeign, "|- Сериалы США и Канады"},
	{242, models.CatTVForeign, "|- Сериалы Великобритании и Ирландии"},
	{819, models.CatTVForeign, "|- Скандинавские сериалы"},
	{1531, models.CatTVForeign, "|- Испанские сериалы"},
	{721, models.CatTVForeign, "|- Итальянские сериалы"},
	{1102, models.CatTVForeign, "|- Европейские сериалы"},
	{1120, models.CatTVForeign, "|- Сериалы стран Африки, Ближнего и Среднего Востока"},
	{1214, models.CatTVForeign, "|- Сериалы Австралии и Новой Зеландии"},
	{489, models.CatTVForeign, "|- Сериалы Ближнего Зарубежья"},
	{387, models.CatTVForeign, "|- Сериалы совместного производства нескольких стран"},
	{1359, models.CatTVForeign, "|- Веб-сериалы, Вебизоды к сериалам и Пилотные серии сериалов"},
	{184, models.CatTVForeign, "|- Бесстыжие / Shameless (US)"},
	{1417, models.CatTVForeign, "|- Во все тяжкие / Breaking Bad"},
	{1449, models.CatTVForeign, "|- Игра престолов / Game of Thrones"},
	{504, models.CatTVForeign, "|- Клан Сопрано / The Sopranos"},
	{372, models.CatTVForeign, "|- Сверхъестественное / Supernatural"},
	{110, models.CatTVForeign, "|- Секретные материалы / The X-Files"},
	{121, models.CatTVForeign, "|- Твин пикс / Twin Peaks"},
	{507, models.CatTVForeign, "|- Теория большого взрыва + Детство Шелдона"},
	{536, models.CatTVForeign, "|- Форс-мажоры / Костюмы в законе / Suits"},
	{1144, models.CatTVForeign, "|- Ходячие мертвецы + Бойтесь ходячих мертвецов"},
	{195, models.CatTVForeign, "|- Для некондиционных раздач"},
	{2366, models.CatTVHD, "Зарубежные сериалы (HD Video)"},
	{1803, models.CatTVHD, "|- Новинки и сериалы в стадии показа (HD Video)"},
	{266, models.CatTVHD, "|- Сериалы США и Канады (HD Video)"},
	{193, models.CatTVHD, "|- Сериалы Великобритании и Ирландии (HD Video)"},
	{1690, models.CatTVHD, "|- Скандинавские сериалы (HD Video)"},
	{1459, models.CatTVHD, "|- Европейские сериалы (HD Video)"},
	{1463, models.CatTVHD, "|- Сериалы стран Африки, Ближнего и Среднего Востока (HD Video)"},
	{825, models.CatTVHD, "|- Сериалы Австралии и Новой Зеландии (HD Video)"},
	{1248, models.CatTVHD, "|- Сериалы Ближнего Зарубежья (HD Video)"},
	{1288, models.CatTVHD, "|- Сериалы совместного производства нескольких стран (HD Video)"},
	{265, models.CatTVHD, "|- Игра престолов / Game of Thrones (HD Video)"},
	{2404, models.CatTVHD, "|- Сверхъестественное / Supernatural (HD Video)"},
	{2405, models.CatTVHD, "|- Секретные материалы / The X-Files (HD Video)"},
	{2370, models.CatTVHD, "|- Твин пикс / Twin Peaks (HD Video)"},
	{2396, models.CatTVHD, "|- Теория Большого Взрыва / The Big Bang Theory (HD Video)"},
	{2398, models.CatTVHD, "|- Ходячие мертвецы + Бойтесь ходячих мертвецов (HD Video)"},
	{1498, models.CatTVHD, "|- Для некондиционных раздач (HD Video)"},
	{119, models.CatTVUHD, "Зарубежные сериалы (UHD Video)"},
	{1171, models.CatTVUHD, "З|- Новинки и сериалы в стадии показа (UHD Video)"},
	{1669, models.CatTVUHD, "|- Сериалы США и Канады (UHD Video)"},
	{2393, models.CatTVUHD, "|- Сериалы Великобритании и Ирландии (UHD Video)"},
	{625, models.CatTVUHD, "|- Европейские сериалы (UHD Video)"},
	{1949, models.CatTVUHD, "|- Сериалы Австралии и Новой Зеландии (UHD Video)"},
	{173, models.CatTVUHD, "|- Сериалы совместного производства нескольких стран (UHD Video)"},
	{273, models.CatTVUHD, "|- Для некондиционных раздач (UHD Video)"},
	{911, models.CatTVForeign, "Сериалы Латинской Америки, Турции и Индии"},
	{325, models.CatTVForeign, "|- Сериалы Аргентины"},
	{534, models.CatTVForeign, "|- Сериалы Бразилии"},
	{594, models.CatTVForeign, "|- Сериалы Венесуэлы"},
	{1301, models.CatTVForeign, "|- Сериалы Индии"},
	{607, models.CatTVForeign, "|- Сериалы Колумбии"},
	{1574, models.CatTVForeign, "|- Сериалы Латинской Америки с озвучкой (раздачи папками)"},
	{1539, models.CatTVForeign, "|- Сериалы Латинской Америки с субтитрами"},
	{694, models.CatTVForeign, "|- Сериалы Мексики"},
	{781, models.CatTVForeign, "|- Сериалы совместного производства"},
	{704, models.CatTVForeign, "|- Сериалы Турции"},
	{1537, models.CatTVForeign, "|- Для некондиционных раздач"},
	{2100, models.CatTVForeign, "Азиатские сериалы"},
	{820, models.CatTVForeign, "|- Азиатские сериалы (UHD Video)"},
	{915, models.CatTVForeign, "|- Корейские сериалы"},
	{1242, models.CatTVForeign, "|- Корейские сериалы (HD Video)"},
	{717, models.CatTVForeign, "|- Китайские сериалы"},
	{1939, models.CatTVForeign, "|- Японские сериалы"},
	{2412, models.CatTVForeign, "|- Сериалы Таиланда, Индонезии, Сингапура"},
	{2102, models.CatTVForeign, "|- VMV и др. ролики"},
	{19, models.CatTVDocumentary, "СМИ"},
	{670, models.CatTVDocumentary, "Вера и религия"},
	{1475, models.CatTVDocumentary, "|- [Видео Религия] Христианство"},
	{2107, models.CatTVDocumentary, "|- [Видео Религия] Ислам"},
	{1453, models.CatTVDocumentary, "|- [Видео Религия] Культы и новые религиозные движения"},
	{294, models.CatTVDocumentary, "|- [Видео Религия] Религии Индии, Тибета и Восточной Азии"},
	{46, models.CatTVDocumentary, "Документальные фильмы и телепередачи"},
	{103, models.CatTVDocumentary, "|- Документальные (DVD)"},
	{671, models.CatTVDocumentary, "|- [Док] Биографии. Личности и кумиры"},
	{2177, models.CatTVDocumentary, "|- [Док] Кинематограф и мультипликация"},
	{656, models.CatTVDocumentary, "|- [Док] Мастера искусств Театра и Кино"},
	{2538, models.CatTVDocumentary, "|- [Док] Искусство, история искусств"},
	{2159, models.CatTVDocumentary, "|- [Док] Музыка"},
	{251, models.CatTVDocumentary, "|- [Док] Криминальная документалистика"},
	{98, models.CatTVDocumentary, "|- [Док] Тайны века / Спецслужбы / Теории Заговоров"},
	{97, models.CatTVDocumentary, "|- [Док] Военное дело"},
	{851, models.CatTVDocumentary, "|- [Док] Вторая мировая война"},
	{2178, models.CatTVDocumentary, "|- [Док] Аварии / Катастрофы / Катаклизмы"},
	{821, models.CatTVDocumentary, "|- [Док] Авиация"},
	{2076, models.CatTVDocumentary, "|- [Док] Космос"},
	{56, models.CatTVDocumentary, "|- [Док] Научно-популярные фильмы"},
	{2123, models.CatTVDocumentary, "|- [Док] Флора и фауна"},
	{876, models.CatTVDocumentary, "|- [Док] Путешествия и туризм"},
	{2139, models.CatTVDocumentary, "|- [Док] Медицина"},
	{2380, models.CatTVDocumentary, "|- [Док] Социальные ток-шоу"},
	{1467, models.CatTVDocumentary, "|- [Док] Информационно-аналитические и общественно-политические передачи"},
	{1469, models.CatTVDocumentary, "|- [Док] Архитектура и строительство"},
	{672, models.CatTVDocumentary, "|- [Док] Всё о доме, быте и дизайне"},
	{249, models.CatTVDocumentary, "|- [Док] BBC"},
	{552, models.CatTVDocumentary, "|- [Док] Discovery"},
	{500, models.CatTVDocumentary, "|- [Док] National Geographic"},
	{2112, models.CatTVDocumentary, "|- [Док] История: Древний мир / Античность / Средневековье"},
	{1327, models.CatTVDocumentary, "|- [Док] История: Новое и Новейшее время"},
	{1468, models.CatTVDocumentary, "|- [Док] Эпоха СССР"},
	{1280, models.CatTVDocumentary, "|- [Док] Битва экстрасенсов / Теория невероятности / Искатели / Галилео"},
	{752, models.CatTVDocumentary, "|- [Док] Русские сенсации / Программа Максимум / Профессия репортёр"},
	{1114, models.CatTVDocumentary, "|- [Док] Паранормальные явления"},
	{2168, models.CatTVDocumentary, "|- [Док] Альтернативная история и наука"},
	{2160, models.CatTVDocumentary, "|- [Док] Внежанровая документалистика"},
	{2176, models.CatTVDocumentary, "|- [Док] Разное / некондиция"},
	{314, models.CatTVDocumentary, "Документальные (HD Video)"},
	{2323, models.CatTVDocumentary, "|- Информационно-аналитические и общественно-политические (HD Video)"},
	{1278, models.CatTVDocumentary, "|- Биографии. Личности и кумиры (HD Video)"},
	{1281, models.CatTVDocumentary, "|- Военное дело (HD Video)"},
	{2110, models.CatTVDocumentary, "|- Естествознание, наука и техника (HD Video)"},
	{979, models.CatTVDocumentary, "|- Путешествия и туризм (HD Video)"},
	{2169, models.CatTVDocumentary, "|- Флора и фауна (HD Video)"},
	{2166, models.CatTVDocumentary, "|- История (HD Video)"},
	{2164, models.CatTVDocumentary, "|- BBC, Discovery, National Geographic, History Channel, Netflix (HD Video)"},
	{2163, models.CatTVDocumentary, "|- Криминальная документалистика (HD Video)"},
	{85, models.CatTVDocumentary, "|- Некондиционное видео - Документальные (HD Video)"},
	{24, models.CatTVDocumentary, "Развлекательные телепередачи и шоу, приколы и юмор"},
	{1959, models.CatTVOther, "|- [Видео Юмор] Интеллектуальные игры и викторины"},
	{939, models.CatTVOther, "|- [Видео Юмор] Реалити и ток-шоу / номинации / показы"},
	{1481, models.CatTVOther, "|- [Видео Юмор] Детские телешоу"},
	{113, models.CatTVOther, "|- [Видео Юмор] КВН"},
	{115, models.CatTVOther, "|- [Видео Юмор] Пост КВН"},
	{882, models.CatTVOther, "|- [Видео Юмор] Кривое Зеркало / Городок / В Городке"},
	{1482, models.CatTVOther, "|- [Видео Юмор] Ледовые шоу"},
	{393, models.CatTVOther, "|- [Видео Юмор] Музыкальные шоу"},
	{1569, models.CatTVOther, "|- [Видео Юмор] Званый ужин"},
	{373, models.CatTVOther, "|- [Видео Юмор] Хорошие Шутки"},
	{1186, models.CatTVOther, "|- [Видео Юмор] Вечерний Квартал"},
	{137, models.CatTVOther, "|- [Видео Юмор] Фильмы со смешным переводом (пародии)"},
	{2537, models.CatTVOther, "|- [Видео Юмор] Stand-up comedy"},
	{532, models.CatTVOther, "|- [Видео Юмор] Украинские Шоу"},
	{827, models.CatTVOther, "|- [Видео Юмор] Танцевальные шоу, концерты, выступления"},
	{1484, models.CatTVOther, "|- [Видео Юмор] Цирк"},
	{1485, models.CatTVOther, "|- [Видео Юмор] Школа злословия"},
	{114, models.CatTVOther, "|- [Видео Юмор] Сатирики и юмористы"},
	{1332, models.CatTVOther, "|- Юмористические аудиопередачи"},
	{1495, models.CatTVOther, "|- Аудио и видео ролики (Приколы и юмор)"},
	{926, models.CatTVOther, "XXV Зимние Олимпийские игры 2026"},
	{1311, models.CatTVOther, "|- Биатлон"},
	{2171, models.CatTVOther, "|- Лыжные гонки"},
	{2455, models.CatTVOther, "|- Горные лыжи. Сноубординг. Фристайл. Прыжки на лыжах с трамплина.Лы.."},
	{1434, models.CatTVOther, "|- Конькобежный спорт. Шорт-трек. Бобслей. Санный спорт. Скелетон "},
	{1339, models.CatTVOther, "|- Фигурное катание "},
	{922, models.CatTVOther, "|- Хоккей"},
	{918, models.CatTVOther, "|- Керлинг"},
	{1315, models.CatTVOther, "XXIV Зимние Олимпийские игры 2022"},
	{1336, models.CatTVOther, "|- Биатлон.Лыжные гонки.Прыжки с трамп.Двоеборье.Горные лыжи.Сноуборд.."},
	{2350, models.CatTVOther, "|- Конькобежный спорт. Шорт-трек. Бобслей. Санный спорт. Скелетон"},
	{1472, models.CatTVOther, "|- Фигурное катание "},
	{2068, models.CatTVOther, "|- Хоккей"},
	{2016, models.CatTVOther, "|- Керлинг"},
	{1346, models.CatTVOther, "XXXIII Летние Олимпийские игры 2024"},
	{2493, models.CatTVSport, "|- Легкая атлетика. Плавание. Прыжки в воду. Синхронное плавание. Гимнастика"},
	{2103, models.CatTVSport, "|- Велоспорт. Академическая гребля. Гребля на байдарках и каноэ"},
	{2485, models.CatTVSport, "|- Футбол. Баскетбол. Волейбол. Гандбол. Водное поло. Регби. Хоккей на траве"},
	{2479, models.CatTVSport, "|- Теннис. Настольный теннис. Бадминтон"},
	{2089, models.CatTVSport, "|- Фехтование. Стрельба. Стрельба из лука. Современное пятиборье"},
	{2338, models.CatTVSport, "|- Бокс. Борьба Вольная и Греко-римская. Дзюдо. Карате. Тхэквондо"},
	{927, models.CatTVSport, "|- Другие виды спорта"},
	{1392, models.CatTVSport, "XXXII Летние Олимпийские игры 2020"},
	{2475, models.CatTVSport, "|- Легкая атлетика. Плавание. Прыжки в воду. Синхронное плавание"},
	{2113, models.CatTVSport, "|- Гимнастика. Прыжки на батуте. Фехтование. Стрельба. Современное пятиборье"},
	{2482, models.CatTVSport, "|- Велоспорт. Академическая гребля. Гребля на байдарках и каноэ"},
	{2522, models.CatTVSport, "|- Бокс. Борьба Вольная и Греко-римская. Дзюдо. Карате. Тхэквондо"},
	{2486, models.CatTVSport, "|- Футбол. Баскетбол. Волейбол. Гандбол. Водное поло. Регби. Хоккей на траве"},
	{1794, models.CatTVSport, "|- Другие виды спорта"},
	{255, models.CatTVSport, "Спортивные турниры, фильмы и передачи"},
	{256, models.CatTVSport, "|- Автоспорт"},
	{1986, models.CatTVSport, "|- Мотоспорт"},
	{660, models.CatTVSport, "|- Формула-1 (2026)"},
	{1551, models.CatTVSport, "|- Формула-1 (2012-2025)"},
	{626, models.CatTVSport, "|- Формула 1 (до 2011 вкл.)"},
	{262, models.CatTVSport, "|- Велоспорт"},
	{1326, models.CatTVSport, "|- Волейбол/Гандбол"},
	{978, models.CatTVSport, "|- Бильярд"},
	{1287, models.CatTVSport, "|- Покер"},
	{1188, models.CatTVSport, "|- Бодибилдинг/Силовые виды спорта"},
	{1667, models.CatTVSport, "|- Бокс"},
	{1675, models.CatTVSport, "|- Классические единоборства"},
	{257, models.CatTVSport, "|- Смешанные единоборства и K-1"},
	{875, models.CatTVSport, "|- Американский футбол"},
	{263, models.CatTVSport, "|- Регби"},
	{2073, models.CatTVSport, "|- Бейсбол"},
	{550, models.CatTVSport, "|- Теннис"},
	{2124, models.CatTVSport, "|- Бадминтон/Настольный теннис"},
	{1470, models.CatTVSport, "|- Гимнастика/Соревнования по танцам"},
	{528, models.CatTVSport, "|- Лёгкая атлетика/Водные виды спорта"},
	{486, models.CatTVSport, "|- Зимние виды спорта"},
	{854, models.CatTVSport, "|- Фигурное катание"},
	{2079, models.CatTVSport, "|- Биатлон"},
	{260, models.CatTVSport, "|- Экстрим"},
	{1319, models.CatTVSport, "|- Спорт (видео)"},
	{1608, models.CatTVSport, "⚽ Футбол"},
	{2294, models.CatTVSport, "|- UHDTV"},
	{2545, models.CatTVSport, "|- Клубный Чемпионат Мира 2025"},
	{2147, models.CatTVSport, "|- Чемпионат Мира 2026 (финальный турнир)"},
	{1693, models.CatTVSport, "|- Чемпионат Мира 2026 (отбор)"},
	{136, models.CatTVSport, "|- Чемпионат Европы 2024 (финальный турнир)"},
	{2532, models.CatTVSport, "|- Чемпионат Европы 2020 [2021] (финальный турнир)"},
	{592, models.CatTVSport, "|- Лига Наций"},
	{1229, models.CatTVSport, "|- Чемпионат Мира 2022"},
	{2533, models.CatTVSport, "|- Чемпионат Мира 2018 (игры)"},
	{1952, models.CatTVSport, "|- Чемпионат Мира 2018 (обзорные передачи, документалистика)"},
	{1621, models.CatTVSport, "|- Чемпионаты Мира"},
	{1668, models.CatTVSport, "|- Россия 2025-2026"},
	{2075, models.CatTVSport, "|- Россия 2024-2025"},
	{1613, models.CatTVSport, "|- Россия/СССР"},
	{1614, models.CatTVSport, "|- Англия"},
	{1623, models.CatTVSport, "|- Испания"},
	{1615, models.CatTVSport, "|- Италия"},
	{1630, models.CatTVSport, "|- Германия"},
	{2425, models.CatTVSport, "|- Франция"},
	{2514, models.CatTVSport, "|- Украина"},
	{1616, models.CatTVSport, "|- Другие национальные чемпионаты и кубки"},
	{2014, models.CatTVSport, "|- Международные турниры"},
	{1491, models.CatTVSport, "|- Еврокубки 2025-2026"},
	{1442, models.CatTVSport, "|- Еврокубки 2024-2025"},
	{1987, models.CatTVSport, "|- Еврокубки 2011-2024"},
	{1617, models.CatTVSport, "|- Еврокубки"},
	{1620, models.CatTVSport, "|- Чемпионаты Европы"},
	{1998, models.CatTVSport, "|- Товарищеские турниры и матчи"},
	{1343, models.CatTVSport, "|- Обзорные и аналитические передачи 2018-2025"},
	{751, models.CatTVSport, "|- Обзорные и аналитические передачи"},
	{497, models.CatTVSport, "|- Документальные фильмы (футбол)"},
	{1697, models.CatTVSport, "|- Мини-футбол/Пляжный футбол"},
	{2004, models.CatTVSport, "🏀 Баскетбол"},
	{2001, models.CatTVSport, "|- Международные соревнования"},
	{2002, models.CatTVSport, "|- NBA / NCAA (до 2000 г.)"},
	{283, models.CatTVSport, "|- NBA / NCAA (2000-2010 гг.)"},
	{1997, models.CatTVSport, "|- NBA / NCAA (2010-2026 гг.)"},
	{2003, models.CatTVSport, "|- Европейский клубный баскетбол"},
	{2009, models.CatTVSport, "🏒 Хоккей"},
	{2010, models.CatTVSport, "|- Хоккей с мячом / Бенди"},
	{2006, models.CatTVSport, "|- Международные турниры"},
	{2007, models.CatTVSport, "|- КХЛ"},
	{2005, models.CatTVSport, "|- НХЛ (до 2011/12)"},
	{259, models.CatTVSport, "|- НХЛ (с 2013)"},
	{2008, models.CatTVSport, "|- СССР - Канада"},
	{126, models.CatTVSport, "|- Документальные фильмы и аналитика"},
	{845, models.CatTVSport, "Рестлинг"},
	{343, models.CatTVSport, "|- Professional Wrestling"},
	{2111, models.CatTVSport, "|- Independent Wrestling"},
	{1527, models.CatTVSport, "|- International Wrestling"},
	{2069, models.CatTVSport, "|- Oldschool Wrestling"},
	{1323, models.CatTVSport, "|- Documentary Wrestling"},
	{1411, models.CatBooks, "|- Сканирование, обработка сканов"},
	{21, models.CatBooks, "Книги и журналы (общий раздел)"},
	{2157, models.CatBooks, "|- Кино, театр, ТВ, мультипликация, цирк"},
	{765, models.CatBooks, "|- Рисунок, графический дизайн"},
	{2019, models.CatBooks, "|- Фото и видеосъемка"},
	{31, models.CatBooksMags, "|- Журналы и газеты (общий раздел)"},
	{1427, models.CatBooks, "|- Эзотерика, гадания, магия, фен-шуй"},
	{2422, models.CatBooks, "|- Астрология"},
	{2195, models.CatBooks, "|- Красота. Уход. Домоводство"},
	{2521, models.CatBooks, "|- Мода. Стиль. Этикет"},
	{2223, models.CatBooks, "|- Путешествия и туризм"},
	{2447, models.CatBooks, "|- Знаменитости и кумиры"},
	{39, models.CatBooks, "|- Разное (книги)"},
	{2086, models.CatBooks, "|- Самиздат, статьи из журналов, фрагменты книг, любительские переводы"},
	{1101, models.CatBooks, "Для детей, родителей и учителей"},
	{745, models.CatBooks, "|- Учебная литература для детского сада и начальной школы (до 4 класса)"},
	{1689, models.CatBooks, "|- Учебная литература для старших классов (5-11 класс)"},
	{2336, models.CatBooks, "|- Учителям и педагогам"},
	{2337, models.CatBooks, "|- Научно-популярная и познавательная литература (для детей)"},
	{1353, models.CatBooks, "|- Досуг и творчество"},
	{1400, models.CatBooks, "|- Воспитание и развитие"},
	{1415, models.CatBooks, "|- Худ. лит-ра для дошкольников и младших классов"},
	{2046, models.CatBooks, "|- Худ. лит-ра для средних и старших классов"},
	{1802, models.CatBooks, "Спорт, физическая культура, боевые искусства"},
	{2189, models.CatBooks, "|- Футбол (книги и журналы)"},
	{2190, models.CatBooks, "|- Хоккей (книги и журналы)"},
	{2443, models.CatBooks, "|- Игровые виды спорта"},
	{1477, models.CatBooks, "|- Легкая атлетика. Плавание. Гимнастика. Тяжелая атлетика. Гребля"},
	{669, models.CatBooks, "|- Автоспорт. Мотоспорт. Велоспорт"},
	{2196, models.CatBooks, "|- Шахматы. Шашки"},
	{2056, models.CatBooks, "|- Боевые искусства, единоборства"},
	{1436, models.CatBooks, "|- Экстрим (книги)"},
	{2191, models.CatBooks, "|- Физкультура, фитнес, бодибилдинг"},
	{2477, models.CatBooks, "|- Спортивная пресса"},
	{1680, models.CatBooks, "Гуманитарные науки"},
	{1684, models.CatBooks, "|- Искусствоведение. Культурология"},
	{2446, models.CatBooks, "|- Фольклор. Эпос. Мифология"},
	{2524, models.CatBooks, "|- Литературоведение"},
	{2525, models.CatBooks, "|- Лингвистика"},
	{995, models.CatBooks, "|- Философия"},
	{2022, models.CatBooks, "|- Политология"},
	{2471, models.CatBooks, "|- Социология"},
	{2375, models.CatBooks, "|- Публицистика, журналистика"},
	{764, models.CatBooks, "|- Бизнес, менеджмент"},
	{1685, models.CatBooks, "|- Маркетинг"},
	{1688, models.CatBooks, "|- Экономика"},
	{2472, models.CatBooks, "|- Финансы"},
	{1687, models.CatBooks, "|- Юридические науки. Право. Криминалистика"},
	{2020, models.CatBooks, "Исторические науки"},
	{1349, models.CatBooks, "|- Методология и философия исторической науки"},
	{1967, models.CatBooks, "|- Исторические источники (книги, периодика)"},
	{1341, models.CatBooks, "|- Исторические источники (документы)"},
	{2049, models.CatBooks, "|- Исторические персоны"},
	{1681, models.CatBooks, "|- Альтернативные исторические теории"},
	{2319, models.CatBooks, "|- Археология"},
	{2434, models.CatBooks, "|- Древний мир. Античность"},
	{1683, models.CatBooks, "|- Средние века"},
	{2444, models.CatBooks, "|- История Нового и Новейшего времени"},
	{2427, models.CatBooks, "|- История Европы"},
	{2452, models.CatBooks, "|- История Азии и Африки"},
	{2445, models.CatBooks, "|- История Америки, Австралии, Океании"},
	{2435, models.CatBooks, "|- История России"},
	{667, models.CatBooks, "|- История России до 1917 года"},
	{2436, models.CatBooks, "|- Эпоха СССР"},
	{1335, models.CatBooks, "|- История России после 1991 года"},
	{2453, models.CatBooks, "|- История стран бывшего СССР"},
	{2320, models.CatBooks, "|- Этнография, антропология"},
	{1801, models.CatBooks, "|- Международные отношения. Дипломатия"},
	{2023, models.CatBooksTechnical, "Точные, естественные и инженерные науки"},
	{2024, models.CatBooksTechnical, "|- Авиация / Космонавтика"},
	{2026, models.CatBooksTechnical, "|- Физика"},
	{2192, models.CatBooksTechnical, "|- Астрономия"},
	{2027, models.CatBooksTechnical, "|- Биология / Экология"},
	{295, models.CatBooksTechnical, "|- Химия / Биохимия"},
	{2028, models.CatBooksTechnical, "|- Математика"},
	{2029, models.CatBooksTechnical, "|- География / Геология / Геодезия"},
	{1325, models.CatBooksTechnical, "|- Электроника / Радио"},
	{2386, models.CatBooksTechnical, "|- Схемы и сервис-мануалы (оригинальная документация)"},
	{2031, models.CatBooksTechnical, "|- Архитектура / Строительство / Инженерные сети / Ландшафтный дизайн"},
	{2030, models.CatBooksTechnical, "|- Машиностроение"},
	{2526, models.CatBooksTechnical, "|- Сварка / Пайка / Неразрушающий контроль"},
	{2527, models.CatBooksTechnical, "|- Автоматизация / Робототехника"},
	{2254, models.CatBooksTechnical, "|- Металлургия / Материаловедение"},
	{2376, models.CatBooksTechnical, "|- Механика, сопротивление материалов"},
	{2054, models.CatBooksTechnical, "|- Энергетика / электротехника"},
	{770, models.CatBooksTechnical, "|- Нефтяная, газовая и химическая промышленность"},
	{2476, models.CatBooksTechnical, "|- Сельское хозяйство и пищевая промышленность"},
	{2494, models.CatBooksTechnical, "|- Железнодорожное дело"},
	{1528, models.CatBooksTechnical, "|- Нормативная документация"},
	{2032, models.CatBooksTechnical, "|- Журналы: научные, научно-популярные, радио и др."},
	{919, models.CatBooks, "Ноты и Музыкальная литература"},
	{944, models.CatBooks, "|- Академическая музыка (Ноты и Media CD)"},
	{980, models.CatBooks, "|- Другие направления (Ноты, табулатуры)"},
	{946, models.CatBooks, "|- Самоучители и Школы"},
	{977, models.CatBooks, "|- Песенники (Songbooks)"},
	{2074, models.CatBooks, "|- Музыкальная литература и Теория"},
	{2349, models.CatBooks, "|- Музыкальные журналы"},
	{768, models.CatBooks, "Военное дело"},
	{2099, models.CatBooks, "|- Милитария"},
	{2021, models.CatBooks, "|- Военная история"},
	{2437, models.CatBooks, "|- История Второй мировой войны"},
	{1337, models.CatBooks, "|- Биографии и мемуары военных деятелей"},
	{1447, models.CatBooks, "|- Военная техника"},
	{2468, models.CatBooks, "|- Стрелковое оружие"},
	{2469, models.CatBooks, "|- Учебно-методическая литература"},
	{2470, models.CatBooks, "|- Спецслужбы мира"},
	{1686, models.CatBooks, "Вера и религия"},
	{2215, models.CatBooks, "|- Христианство"},
	{2216, models.CatBooks, "|- Ислам"},
	{2546, models.CatBooks, "|- Иудаизм"},
	{2217, models.CatBooks, "|- Религии Индии, Тибета и Восточной Азии"},
	{2218, models.CatBooks, "|- Нетрадиционные религиозные, духовные и мистические учения"},
	{2252, models.CatBooks, "|- Религиоведение. История Религии"},
	{2543, models.CatBooks, "|- Атеизм. Научный атеизм"},
	{767, models.CatBooks, "Психология"},
	{2515, models.CatBooks, "|- Общая и прикладная психология"},
	{2516, models.CatBooks, "|- Психотерапия и консультирование"},
	{2517, models.CatBooks, "|- Психодиагностика и психокоррекция"},
	{2518, models.CatBooks, "|- Социальная психология и психология отношений"},
	{2519, models.CatBooks, "|- Тренинг и коучинг"},
	{2520, models.CatBooks, "|- Саморазвитие и самосовершенствование"},
	{1696, models.CatBooks, "|- Популярная психология"},
	{2253, models.CatBooks, "|- Сексология. Взаимоотношения полов (18+)"},
	{2033, models.CatBooks, "Коллекционирование, увлечения и хобби"},
	{1412, models.CatBooks, "|- Коллекционирование и вспомогательные ист. дисциплины"},
	{1446, models.CatBooks, "|- Вышивание"},
	{753, models.CatBooks, "|- Вязание"},
	{2037, models.CatBooks, "|- Шитье, пэчворк"},
	{2224, models.CatBooks, "|- Кружевоплетение"},
	{2194, models.CatBooks, "|- Бисероплетение. Ювелирика. Украшения из проволоки."},
	{2418, models.CatBooks, "|- Бумажный арт"},
	{1410, models.CatBooks, "|- Другие виды декоративно-прикладного искусства"},
	{2034, models.CatBooks, "|- Домашние питомцы и аквариумистика"},
	{2433, models.CatBooks, "|- Охота и рыбалка"},
	{1961, models.CatBooks, "|- Кулинария (книги)"},
	{2432, models.CatBooks, "|- Кулинария (газеты и журналы)"},
	{565, models.CatBooks, "|- Моделизм"},
	{1523, models.CatBooks, "|- Приусадебное хозяйство / Цветоводство"},
	{1575, models.CatBooks, "|- Ремонт, частное строительство, дизайн интерьеров"},
	{1520, models.CatBooks, "|- Деревообработка"},
	{2424, models.CatBooks, "|- Настольные игры"},
	{769, models.CatBooks, "|- Прочие хобби и игры"},
	{2038, models.CatBooks, "Художественная литература"},
	{2043, models.CatBooks, "|- Русская литература"},
	{2042, models.CatBooks, "|- Зарубежная литература (до 1900 г.)"},
	{2041, models.CatBooks, "|- Зарубежная литература (XX и XXI век)"},
	{2044, models.CatBooks, "|- Детектив, боевик"},
	{2039, models.CatBooks, "|- Женский роман"},
	{2045, models.CatBooks, "|- Отечественная фантастика / фэнтези / мистика"},
	{2080, models.CatBooks, "|- Зарубежная фантастика / фэнтези / мистика"},
	{2047, models.CatBooks, "|- Приключения"},
	{2193, models.CatBooks, "|- Литературные журналы"},
	{1037, models.CatBooks, "|- Самиздат и книги, изданные за счет авторов"},
	{1418, models.CatBooksTechnical, "Компьютерная литература"},
	{1422, models.CatBooksTechnical, "|- Программы от Microsoft"},
	{1423, models.CatBooksTechnical, "|- Другие программы"},
	{1424, models.CatBooksTechnical, "|- Mac OS; Linux, FreeBSD и прочие *NIX"},
	{1445, models.CatBooksTechnical, "|- СУБД"},
	{1425, models.CatBooksTechnical, "|- Веб-дизайн и программирование"},
	{1426, models.CatBooksTechnical, "|- Программирование (книги)"},
	{1428, models.CatBooksTechnical, "|- Графика, обработка видео"},
	{1429, models.CatBooksTechnical, "|- Сети / VoIP"},
	{1430, models.CatBooksTechnical, "|- Хакинг и безопасность"},
	{1431, models.CatBooksTechnical, "|- Железо (книги о ПК)"},
	{1433, models.CatBooksTechnical, "|- Инженерные и научные программы (книги)"},
	{1432, models.CatBooksTechnical, "|- Компьютерные журналы и приложения к ним"},
	{2202, models.CatBooksTechnical, "|- Дисковые приложения к игровым журналам"},
	{862, models.CatBooksComics, "Комиксы, манга, ранобэ"},
	{2461, models.CatBooksComics, "|- Комиксы на русском языке"},
	{2462, models.CatBooksComics, "|- Комиксы издательства Marvel"},
	{2463, models.CatBooksComics, "|- Комиксы издательства DC"},
	{2464, models.CatBooksComics, "|- Комиксы других издательств"},
	{2473, models.CatBooksComics, "|- Комиксы на других языках"},
	{281, models.CatBooksComics, "|- Манга (на русском языке)"},
	{2465, models.CatBooksComics, "|- Манга (на иностранных языках)"},
	{2458, models.CatBooksComics, "|- Ранобэ"},
	{2048, models.CatBooksOther, "Коллекции книг и библиотеки"},
	{1238, models.CatBooksOther, "|- Библиотеки (зеркала сетевых библиотек/коллекций)"},
	{2055, models.CatBooksOther, "|- Тематические коллекции (подборки)"},
	{754, models.CatBooksOther, "|- Многопредметные коллекции (подборки)"},
	{2114, models.CatBooksEBook, "Мультимедийные и интерактивные издания"},
	{2438, models.CatBooksEBook, "|- Мультимедийные энциклопедии"},
	{2439, models.CatBooksEBook, "|- Интерактивные обучающие и развивающие материалы"},
	{2440, models.CatBooksEBook, "|- Обучающие издания для детей"},
	{2441, models.CatBooksEBook, "|- Кулинария. Цветоводство. Домоводство"},
	{2442, models.CatBooksEBook, "|- Культура. Искусство. История"},
	{2125, models.CatBooks, "Медицина и здоровье"},
	{2133, models.CatBooks, "|- Клиническая медицина до 1980 года"},
	{2130, models.CatBooks, "|- Клиническая медицина с 1980 по 2000 год"},
	{2313, models.CatBooks, "|- Клиническая медицина после 2000 года"},
	{2528, models.CatBooks, "|- Научная медицинская периодика (газеты и журналы)"},
	{2129, models.CatBooks, "|- Медико-биологические науки"},
	{2141, models.CatBooks, "|- Фармация и фармакология"},
	{2314, models.CatBooks, "|- Популярная медицинская периодика (газеты и журналы)"},
	{2132, models.CatBooks, "|- Нетрадиционная, народная медицина и популярные книги о здоровье"},
	{2131, models.CatBooks, "|- Ветеринария, разное"},
	{2362, models.CatBooksEBook, "Иностранные языки для взрослых"},
	{1265, models.CatBooksEBook, "|- Английский язык (для взрослых)"},
	{1266, models.CatBooksEBook, "|- Немецкий язык"},
	{1267, models.CatBooksEBook, "|- Французский язык"},
	{1358, models.CatBooksEBook, "|- Испанский язык"},
	{2363, models.CatBooksEBook, "|- Итальянский язык"},
	{734, models.CatBooksEBook, "|- Финский язык"},
	{1268, models.CatBooksEBook, "|- Другие европейские языки"},
	{1673, models.CatBooksEBook, "|- Арабский язык"},
	{1269, models.CatBooksEBook, "|- Китайский язык"},
	{1270, models.CatBooksEBook, "|- Японский язык"},
	{1275, models.CatBooksEBook, "|- Другие восточные языки"},
	{2364, models.CatBooksEBook, "|- Русский язык как иностранный"},
	{1276, models.CatBooksEBook, "|- Мультиязычные сборники и курсы"},
	{2094, models.CatBooksEBook, "|- LIM-курсы"},
	{1274, models.CatBooksEBook, "|- Разное (иностранные языки)"},
	{1264, models.CatBooksEBook, "Иностранные языки для детей"},
	{2358, models.CatBooksEBook, "|- Английский язык (для детей)"},
	{2359, models.CatBooksEBook, "|- Другие европейские языки (для детей)"},
	{2360, models.CatBooksEBook, "|- Восточные языки (для детей)"},
	{2361, models.CatBooksEBook, "|- Школьные учебники, ЕГЭ, ОГЭ"},
	{2057, models.CatBooksEBook, "Художественная литература (ин.языки)"},
	{2355, models.CatBooksEBook, "|- Художественная литература на английском языке"},
	{2474, models.CatBooksEBook, "|- Художественная литература на французском языке"},
	{2356, models.CatBooksEBook, "|- Художественная литература на других европейских языках"},
	{2357, models.CatBooksEBook, "|- Художественная литература на восточных языках"},
	{2413, models.CatAudioAudiobook, "Аудиокниги на иностранных языках"},
	{1501, models.CatAudioAudiobook, "|- Аудиокниги на английском языке"},
	{1580, models.CatAudioAudiobook, "|- Аудиокниги на немецком языке"},
	{525, models.CatAudioAudiobook, "|- Аудиокниги на других иностранных языках"},
	{610, models.CatBooksOther, "Видеоуроки и обучающие интерактивные DVD"},
	{1568, models.CatBooksOther, "|- Кулинария"},
	{1542, models.CatBooksOther, "|- Спорт"},
	{2335, models.CatBooksOther, "|- Фитнес - Кардио-Силовые Тренировки"},
	{1544, models.CatBooksOther, "|- Фитнес - Разум и Тело"},
	{1546, models.CatBooksOther, "|- Бодибилдинг"},
	{1549, models.CatBooksOther, "|- Оздоровительные практики"},
	{1597, models.CatBooksOther, "|- Йога"},
	{1552, models.CatBooksOther, "|- Видео- и фотосъёмка"},
	{1550, models.CatBooksOther, "|- Уход за собой"},
	{1553, models.CatBooksOther, "|- Рисование"},
	{1554, models.CatBooksOther, "|- Игра на гитаре"},
	{617, models.CatBooksOther, "|- Ударные инструменты"},
	{1555, models.CatBooksOther, "|- Другие музыкальные инструменты"},
	{2017, models.CatBooksOther, "|- Игра на бас-гитаре"},
	{1257, models.CatBooksOther, "|- Бальные танцы"},
	{1258, models.CatBooksOther, "|- Танец живота"},
	{2208, models.CatBooksOther, "|- Уличные и клубные танцы"},
	{677, models.CatBooksOther, "|- Танцы, разное"},
	{1255, models.CatBooksOther, "|- Охота"},
	{1479, models.CatBooksOther, "|- Рыболовство и подводная охота"},
	{1261, models.CatBooksOther, "|- Фокусы и трюки"},
	{614, models.CatBooksOther, "|- Образование"},
	{1583, models.CatBooksOther, "|- Финансы"},
	{1259, models.CatBooksOther, "|- Продажи, бизнес"},
	{2065, models.CatBooksOther, "|- Беременность, роды, материнство"},
	{1254, models.CatBooksOther, "|- Учебные видео для детей"},
	{1260, models.CatBooksOther, "|- Психология"},
	{2209, models.CatBooksOther, "|- Эзотерика, саморазвитие"},
	{2210, models.CatBooksOther, "|- Пикап, знакомства"},
	{1547, models.CatBooksOther, "|- Строительство, ремонт и дизайн"},
	{1548, models.CatBooksOther, "|- Дерево- и металлообработка"},
	{2211, models.CatBooksOther, "|- Растения и животные"},
	{1596, models.CatBooksOther, "|- Хобби и рукоделие"},
	{2135, models.CatBooksOther, "|- Медицина и стоматология"},
	{2140, models.CatBooksOther, "|- Психотерапия и клиническая психология"},
	{2136, models.CatBooksOther, "|- Массаж"},
	{2138, models.CatBooksOther, "|- Здоровье"},
	{615, models.CatBooksOther, "|- Разное"},
	{1581, models.CatBooksOther, "Боевые искусства (Видеоуроки)"},
	{1590, models.CatBooksOther, "|- Айкидо и айки-дзюцу"},
	{1587, models.CatBooksOther, "|- Вин чун"},
	{1594, models.CatBooksOther, "|- Джиу-джитсу"},
	{1591, models.CatBooksOther, "|- Дзюдо и самбо"},
	{1588, models.CatBooksOther, "|- Каратэ"},
	{1585, models.CatBooksOther, "|- Работа с оружием"},
	{1586, models.CatBooksOther, "|- Русский стиль"},
	{2078, models.CatBooksOther, "|- Рукопашный бой"},
	{1929, models.CatBooksOther, "|- Смешанные стили"},
	{1593, models.CatBooksOther, "|- Ударные стили"},
	{1592, models.CatBooksOther, "|- Ушу"},
	{1595, models.CatBooksOther, "|- Разное"},
	{1556, models.CatBooksTechnical, "Компьютерные видеоуроки и обучающие интерактивные DVD"},
	{2539, models.CatBooksTechnical, "|- Machine/Deep Learning, Neural Networks"},
	{1560, models.CatBooksTechnical, "|- Компьютерные сети и безопасность"},
	{1991, models.CatBooksTechnical, "|- Devops"},
	{1561, models.CatBooksTechnical, "|- ОС и серверные программы Microsoft"},
	{1653, models.CatBooksTechnical, "|- Офисные программы Microsoft"},
	{1570, models.CatBooksTechnical, "|- ОС и программы семейства UNIX"},
	{1654, models.CatBooksTechnical, "|- Adobe Photoshop"},
	{1655, models.CatBooksTechnical, "|- Autodesk Maya"},
	{1656, models.CatBooksTechnical, "|- Autodesk 3ds Max"},
	{1930, models.CatBooksTechnical, "|- Autodesk Softimage (XSI)"},
	{1931, models.CatBooksTechnical, "|- ZBrush"},
	{1932, models.CatBooksTechnical, "|- Flash, Flex и ActionScript"},
	{1562, models.CatBooksTechnical, "|- 2D-графика"},
	{1563, models.CatBooksTechnical, "|- 3D-графика"},
	{1626, models.CatBooksTechnical, "|- Инженерные и научные программы (видеоуроки)"},
	{1564, models.CatBooksTechnical, "|- Web-дизайн"},
	{1545, models.CatBooksTechnical, "|- WEB, SMM, SEO, интернет-маркетинг"},
	{1565, models.CatBooksTechnical, "|- Программирование (видеоуроки)"},
	{1559, models.CatBooksTechnical, "|- Программы для Mac OS"},
	{1566, models.CatBooksTechnical, "|- Работа с видео"},
	{1573, models.CatBooksTechnical, "|- Работа со звуком"},
	{1567, models.CatBooksTechnical, "|- Разное (Компьютерные видеоуроки)"},
	{2326, models.CatAudioAudiobook, "Радиоспектакли, история, мемуары"},
	{574, models.CatAudioAudiobook, "|- [Аудио] Радиоспектакли и литературные чтения"},
	{1036, models.CatAudioAudiobook, "|- [Аудио] Биографии и мемуары"},
	{400, models.CatAudioAudiobook, "|- [Аудио] История, культурология, философия"},
	{2389, models.CatAudioAudiobook, "Фантастика, фэнтези, мистика, ужасы, фанфики"},
	{2388, models.CatAudioAudiobook, "|- [Аудио] Зарубежная фантастика, фэнтези, мистика, ужасы, фанфики"},
	{2387, models.CatAudioAudiobook, "|- [Аудио] Российская фантастика, фэнтези, мистика, ужасы, фанфики"},
	{661, models.CatAudioAudiobook, "|- [Аудио] Любовно-фантастический роман"},
	{2348, models.CatAudioAudiobook, "|- [Аудио] Сборники/разное Фантастика, фэнтези, мистика, ужасы, фанфики"},
	{2327, models.CatAudioAudiobook, "Художественная литература"},
	{695, models.CatAudioAudiobook, "|- [Аудио] Поэзия"},
	{399, models.CatAudioAudiobook, "|- [Аудио] Зарубежная литература"},
	{402, models.CatAudioAudiobook, "|- [Аудио] Русская литература"},
	{467, models.CatAudioAudiobook, "|- [Аудио] Современные любовные романы"},
	{490, models.CatAudioAudiobook, "|- [Аудио] Детская литература"},
	{499, models.CatAudioAudiobook, "|- [Аудио] Зарубежные детективы, приключения, триллеры, боевики"},
	{2137, models.CatAudioAudiobook, "|- [Аудио] Российские детективы, приключения, триллеры, боевики"},
	{2127, models.CatAudioAudiobook, "|- [Аудио] Азиатская подростковая литература, ранобэ, веб-новеллы"},
	{2324, models.CatAudioAudiobook, "Религии"},
	{2325, models.CatAudioAudiobook, "|- [Аудио] Православие"},
	{2342, models.CatAudioAudiobook, "|- [Аудио] Ислам"},
	{530, models.CatAudioAudiobook, "|- [Аудио] Другие традиционные религии"},
	{2152, models.CatAudioAudiobook, "|- [Аудио] Нетрадиционные религиозно-философские учения"},
	{2328, models.CatAudioAudiobook, "Прочая литература"},
	{1350, models.CatAudioAudiobook, "|- [Аудио] Книги по медицине"},
	{403, models.CatAudioAudiobook, "|- [Аудио] Учебная и научно-популярная литература"},
	{1279, models.CatAudioAudiobook, "|- [Аудио] lossless-аудиокниги"},
	{716, models.CatAudioAudiobook, "|- [Аудио] Бизнес"},
	{2165, models.CatAudioAudiobook, "|- [Аудио] Разное"},
	{401, models.CatAudioAudiobook, "|- [Аудио] Некондиционные раздачи"},
	{1964, models.CatBooks, "Ремонт и эксплуатация транспортных средств"},
	{1973, models.CatBooks, "|- Оригинальные каталоги по подбору запчастей"},
	{1974, models.CatBooks, "|- Неоригинальные каталоги по подбору запчастей"},
	{1975, models.CatBooks, "|- Программы по диагностике и ремонту"},
	{1976, models.CatBooks, "|- Тюнинг, чиптюнинг, настройка"},
	{1977, models.CatBooks, "|- Книги по ремонту/обслуживанию/эксплуатации ТС"},
	{1203, models.CatBooks, "|- Мультимедийки по ремонту/обслуживанию/эксплуатации ТС"},
	{1978, models.CatBooks, "|- Учет, утилиты и прочее"},
	{1979, models.CatBooks, "|- Виртуальная автошкола"},
	{1980, models.CatBooks, "|- Видеоуроки по вождению транспортных средств"},
	{1981, models.CatBooks, "|- Видеоуроки по ремонту транспортных средств"},
	{1970, models.CatBooks, "|- Журналы по авто/мото"},
	{334, models.CatBooks, "|- Водный транспорт"},
	{1202, models.CatTVDocumentary, "Фильмы и передачи по авто/мото"},
	{1985, models.CatTVDocumentary, "|- Документальные/познавательные фильмы"},
	{1982, models.CatTVOther, "|- Развлекательные передачи"},
	{2151, models.CatTVDocumentary, "|- Top Gear/Топ Гир"},
	{1983, models.CatTVDocumentary, "|- Тест драйв/Обзоры/Автосалоны"},
	{1984, models.CatTVDocumentary, "|- Тюнинг/форсаж"},
	{409, models.CatAudio, "Классическая и современная академическая музыка"},
	{560, models.CatAudioLossless, "|- Полные собрания сочинений и многодисковые издания (lossless)"},
	{794, models.CatAudioLossless, "|- Опера (lossless)"},
	{556, models.CatAudioLossless, "|- Вокальная музыка (lossless)"},
	{2307, models.CatAudioLossless, "|- Хоровая музыка (lossless)"},
	{557, models.CatAudioLossless, "|- Оркестровая музыка (lossless)"},
	{2308, models.CatAudioLossless, "|- Концерт для инструмента с оркестром (lossless)"},
	{558, models.CatAudioLossless, "|- Камерная инструментальная музыка (lossless)"},
	{793, models.CatAudioLossless, "|- Сольная инструментальная музыка (lossless)"},
	{1395, models.CatAudioLossless, "|- Духовные песнопения и музыка (lossless)"},
	{436, models.CatAudioMP3, "|- Полные собрания сочинений и многодисковые издания (lossy)"},
	{2309, models.CatAudioMP3, "|- Вокальная и хоровая музыка (lossy)"},
	{2310, models.CatAudioMP3, "|- Оркестровая музыка (lossy)"},
	{2311, models.CatAudioMP3, "|- Камерная и сольная инструментальная музыка (lossy)"},
	{969, models.CatAudio, "|- Классика в современной обработке, Classical Crossover (lossy и lossless)"},
	{1125, models.CatAudio, "Фольклор, Народная и Этническая музыка"},
	{1130, models.CatAudioMP3, "|- Восточноевропейский фолк (lossy)"},
	{1131, models.CatAudioLossless, "|- Восточноевропейский фолк (lossless)"},
	{1132, models.CatAudioMP3, "|- Западноевропейский фолк (lossy)"},
	{1133, models.CatAudioLossless, "|- Западноевропейский фолк (lossless)"},
	{2084, models.CatAudio, "|- Klezmer и Еврейский фольклор (lossy и lossless)"},
	{1128, models.CatAudioMP3, "|- Этническая музыка Сибири, Средней и Восточной Азии (lossy)"},
	{1129, models.CatAudioLossless, "|- Этническая музыка Сибири, Средней и Восточной Азии (lossless)"},
	{1856, models.CatAudioMP3, "|- Этническая музыка Индии (lossy)"},
	{2430, models.CatAudioLossless, "|- Этническая музыка Индии (lossless)"},
	{1283, models.CatAudioMP3, "|- Этническая музыка Африки и Ближнего Востока (lossy)"},
	{2085, models.CatAudioLossless, "|- Этническая музыка Африки и Ближнего Востока (lossless)"},
	{1282, models.CatAudio, "|- Фольклорная, Народная, Эстрадная музыка Кавказа и Закавказья (lossy и lossless)"},
	{1284, models.CatAudioMP3, "|- Этническая музыка Северной и Южной Америки (lossy)"},
	{1285, models.CatAudioLossless, "|- Этническая музыка Северной и Южной Америки (lossless)"},
	{1138, models.CatAudio, "|- Этническая музыка Австралии, Тихого и Индийского океанов (lossy и lossless)"},
	{1136, models.CatAudioMP3, "|- Country, Bluegrass (lossy)"},
	{1137, models.CatAudioLossless, "|- Country, Bluegrass (lossless)"},
	{1849, models.CatAudio, "New Age, Relax, Meditative & Flamenco"},
	{1126, models.CatAudioMP3, "|- New Age & Meditative (lossy)"},
	{1127, models.CatAudioLossless, "|- New Age & Meditative (lossless)"},
	{1134, models.CatAudioMP3, "|- Фламенко и акустическая гитара (lossy)"},
	{1135, models.CatAudioLossless, "|- Фламенко и акустическая гитара (lossless)"},
	{2018, models.CatAudio, "|- Музыка для бальных танцев (lossy и lossless)"},
	{855, models.CatAudio, "|- Звуки природы"},
	{408, models.CatAudio, "Рэп, Хип-Хоп, R'n'B"},
	{441, models.CatAudioMP3, "|- Отечественный Рэп, Хип-Хоп (lossy)"},
	{1173, models.CatAudioMP3, "|- Отечественный R'n'B (lossy)"},
	{1486, models.CatAudioLossless, "|- Отечественный Рэп, Хип-Хоп, R'n'B (lossless)"},
	{1172, models.CatAudioMP3, "|- Зарубежный R'n'B (lossy)"},
	{446, models.CatAudioMP3, "|- Зарубежный Рэп, Хип-Хоп (lossy)"},
	{909, models.CatAudioLossless, "|- Зарубежный Рэп, Хип-Хоп (lossless)"},
	{1665, models.CatAudioLossless, "|- Зарубежный R'n'B (lossless)"},
	{1760, models.CatAudio, "Reggae, Ska, Dub"},
	{1764, models.CatAudio, "|- Rocksteady, Early Reggae, Ska-Jazz, Trad.Ska (lossy и lossless)"},
	{1767, models.CatAudioMP3, "|- 3rd Wave Ska (lossy)"},
	{1769, models.CatAudioMP3, "|- Ska-Punk, Ska-Core (lossy)"},
	{1765, models.CatAudioMP3, "|- Reggae (lossy)"},
	{1771, models.CatAudioMP3, "|- Dub (lossy)"},
	{1770, models.CatAudioMP3, "|- Dancehall, Raggamuffin (lossy)"},
	{1768, models.CatAudioLossless, "|- Reggae, Dancehall, Dub (lossless)"},
	{1774, models.CatAudioLossless, "|- Ska, Ska-Punk, Ska-Jazz (lossless)"},
	{1772, models.CatAudio, "|- Отечественный Reggae, Ska, Dub (lossy и lossless)"},
	{2233, models.CatAudio, "|- Reggae, Ska, Dub (компиляции) (lossy и lossless)"},
	{416, models.CatAudio, "Саундтреки, караоке и мюзиклы"},
	{2377, models.CatAudioVideo, "|- Караоке"},
	{468, models.CatAudio, "|- Минусовки (lossy и lossless)"},
	{691, models.CatAudioLossless, "|- Саундтреки к отечественным фильмам (lossless)"},
	{469, models.CatAudioMP3, "|- Саундтреки к отечественным фильмам (lossy)"},
	{786, models.CatAudioLossless, "|- Саундтреки к зарубежным фильмам (lossless)"},
	{785, models.CatAudioMP3, "|- Саундтреки к зарубежным фильмам (lossy)"},
	{1631, models.CatAudioLossless, "|- Саундтреки к сериалам (lossless)"},
	{1499, models.CatAudioMP3, "|- Саундтреки к сериалам (lossy)"},
	{715, models.CatAudio, "|- Саундтреки к мультфильмам (lossy и lossless)"},
	{1388, models.CatAudioLossless, "|- Саундтреки к аниме (lossless)"},
	{282, models.CatAudioMP3, "|- Саундтреки к аниме (lossy)"},
	{796, models.CatAudioMP3, "|- Неофициальные саундтреки к фильмам и сериалам (lossy)"},
	{784, models.CatAudioLossless, "|- Саундтреки к играм (lossless)"},
	{783, models.CatAudioMP3, "|- Саундтреки к играм (lossy)"},
	{2331, models.CatAudioMP3, "|- Неофициальные саундтреки к играм (lossy)"},
	{2431, models.CatAudio, "|- Аранжировки музыки из игр (lossy и lossless)"},
	{880, models.CatAudio, "|- Мюзикл (lossy и lossless)"},
	{1215, models.CatAudio, "Шансон, Авторская и Военная песня"},
	{1220, models.CatAudioLossless, "|- Отечественный шансон (lossless)"},
	{1221, models.CatAudioMP3, "|- Отечественный шансон (lossy)"},
	{1334, models.CatAudioMP3, "|- Сборники отечественного шансона (lossy)"},
	{1216, models.CatAudioLossless, "|- Военная песня, марши (lossless)"},
	{1223, models.CatAudioMP3, "|- Военная песня, марши (lossy)"},
	{1224, models.CatAudioLossless, "|- Авторская песня (lossless)"},
	{1225, models.CatAudioMP3, "|- Авторская песня (lossy)"},
	{1226, models.CatAudio, "|- Менестрели и ролевики (lossy и lossless)"},
	{782, models.CatAudio, "Лейбл- и сцен-паки. Неофициальные сборники и ремастеринги. AI-музыка"},
	{577, models.CatAudio, "|- AI-Music - музыка ИИ, нейросетей (lossy и lossless)"},
	{1842, models.CatAudioLossless, "|- Лейбл-паки (lossless)"},
	{1648, models.CatAudioMP3, "|- Лейбл-паки, Сцен-паки (lossy)"},
	{134, models.CatAudioLossless, "|- Неофициальные сборники и ремастеринги (lossless)"},
	{965, models.CatAudioMP3, "|- Неофициальные сборники (lossy)"},
	{2495, models.CatAudioMP3, "Отечественная поп-музыка"},
	{424, models.CatAudioMP3, "|- Популярная музыка России и стран бывшего СССР (lossy)"},
	{1361, models.CatAudioMP3, "|- Популярная музыка России и стран бывшего СССР (сборники) (lossy)"},
	{425, models.CatAudioLossless, "|- Популярная музыка России и стран бывшего СССР (lossless)"},
	{1635, models.CatAudioMP3, "|- Советская эстрада, ретро, романсы (lossy)"},
	{1634, models.CatAudioLossless, "|- Советская эстрада, ретро, романсы (lossless)"},
	{2497, models.CatAudio, "Зарубежная поп-музыка"},
	{428, models.CatAudioMP3, "|- Зарубежная поп-музыка (lossy)"},
	{1362, models.CatAudioMP3, "|- Зарубежная поп-музыка (сборники) (lossy)"},
	{429, models.CatAudioLossless, "|- Зарубежная поп-музыка (lossless)"},
	{735, models.CatAudioMP3, "|- Итальянская поп-музыка (lossy)"},
	{1753, models.CatAudioLossless, "|- Итальянская поп-музыка (lossless)"},
	{2232, models.CatAudioMP3, "|- Латиноамериканская поп-музыка (lossy)"},
	{714, models.CatAudioLossless, "|- Латиноамериканская поп-музыка (lossless)"},
	{1331, models.CatAudioMP3, "|- Восточноазиатская поп-музыка (lossy)"},
	{1330, models.CatAudioLossless, "|- Восточноазиатская поп-музыка (lossless)"},
	{1219, models.CatAudioMP3, "|- Зарубежный шансон (lossy)"},
	{1452, models.CatAudioLossless, "|- Зарубежный шансон (lossless)"},
	{2275, models.CatAudioMP3, "|- Easy Listening, Instrumental Pop (lossy)"},
	{2270, models.CatAudioLossless, "|- Easy Listening, Instrumental Pop (lossless)"},
	{1351, models.CatAudio, "|- Сборники песен для детей (lossy и lossless)"},
	{2499, models.CatAudio, "Eurodance, Disco, Hi-NRG"},
	{2503, models.CatAudioMP3, "|- Eurodance, Euro-House, Technopop (lossy)"},
	{2504, models.CatAudioMP3, "|- Eurodance, Euro-House, Technopop (сборники) (lossy)"},
	{2502, models.CatAudioLossless, "|- Eurodance, Euro-House, Technopop (lossless)"},
	{2501, models.CatAudioMP3, "|- Disco, Italo-Disco, Euro-Disco, Hi-NRG (lossy)"},
	{2505, models.CatAudioMP3, "|- Disco, Italo-Disco, Euro-Disco, Hi-NRG (сборники) (lossy)"},
	{2500, models.CatAudioLossless, "|- Disco, Italo-Disco, Euro-Disco, Hi-NRG (lossless)"},
	{2267, models.CatAudio, "Зарубежный джаз"},
	{2277, models.CatAudioLossless, "|- Early Jazz, Swing, Gypsy (lossless)"},
	{2278, models.CatAudioLossless, "|- Bop (lossless)"},
	{2279, models.CatAudioLossless, "|- Mainstream Jazz, Cool (lossless)"},
	{2280, models.CatAudioLossless, "|- Jazz Fusion (lossless)"},
	{2281, models.CatAudioLossless, "|- World Fusion, Ethnic Jazz (lossless)"},
	{2282, models.CatAudioLossless, "|- Avant-Garde Jazz, Free Improvisation (lossless)"},
	{2353, models.CatAudioLossless, "|- Modern Creative, Third Stream (lossless)"},
	{2284, models.CatAudioLossless, "|- Smooth, Jazz-Pop (lossless)"},
	{2285, models.CatAudioLossless, "|- Vocal Jazz (lossless)"},
	{2283, models.CatAudioLossless, "|- Funk, Soul, R&B (lossless)"},
	{2286, models.CatAudioLossless, "|- Сборники зарубежного джаза (lossless)"},
	{2287, models.CatAudioMP3, "|- Зарубежный джаз (lossy)"},
	{2268, models.CatAudio, "Зарубежный блюз"},
	{2293, models.CatAudioLossless, "|- Blues (Texas, Chicago, Modern and Others) (lossless)"},
	{2292, models.CatAudioLossless, "|- Blues-rock (lossless)"},
	{2290, models.CatAudioLossless, "|- Roots, Pre-War Blues, Early R&B, Gospel (lossless)"},
	{2289, models.CatAudioLossless, "|- Зарубежный блюз (сборники; Tribute VA) (lossless)"},
	{2288, models.CatAudioMP3, "|- Зарубежный блюз (lossy)"},
	{2269, models.CatAudio, "Отечественный джаз и блюз"},
	{2297, models.CatAudioLossless, "|- Отечественный джаз (lossless)"},
	{2295, models.CatAudioMP3, "|- Отечественный джаз (lossy)"},
	{2296, models.CatAudioLossless, "|- Отечественный блюз (lossless)"},
	{2298, models.CatAudioMP3, "|- Отечественный блюз (lossy)"},
	{1698, models.CatAudio, "Зарубежный Rock"},
	{1702, models.CatAudioLossless, "|- Classic Rock & Hard Rock (lossless)"},
	{1703, models.CatAudioMP3, "|- Classic Rock & Hard Rock (lossy)"},
	{1704, models.CatAudioLossless, "|- Progressive & Art-Rock (lossless)"},
	{1705, models.CatAudioMP3, "|- Progressive & Art-Rock (lossy)"},
	{1706, models.CatAudioLossless, "|- Folk-Rock (lossless)"},
	{1707, models.CatAudioMP3, "|- Folk-Rock (lossy)"},
	{2329, models.CatAudioLossless, "|- AOR (Melodic Hard Rock, Arena rock) (lossless)"},
	{2330, models.CatAudioMP3, "|- AOR (Melodic Hard Rock, Arena rock) (lossy)"},
	{1708, models.CatAudioLossless, "|- Pop-Rock & Soft Rock (lossless)"},
	{1709, models.CatAudioMP3, "|- Pop-Rock & Soft Rock (lossy)"},
	{1710, models.CatAudioLossless, "|- Instrumental Guitar Rock (lossless)"},
	{1711, models.CatAudioMP3, "|- Instrumental Guitar Rock (lossy)"},
	{1712, models.CatAudioLossless, "|- Rockabilly, Psychobilly, Rock'n'Roll (lossless)"},
	{1713, models.CatAudioMP3, "|- Rockabilly, Psychobilly, Rock'n'Roll (lossy)"},
	{731, models.CatAudioLossless, "|- Сборники зарубежного рока (lossless)"},
	{1799, models.CatAudioMP3, "|- Сборники зарубежного рока (lossy)"},
	{1714, models.CatAudioLossless, "|- Восточноазиатский рок (lossless)"},
	{1715, models.CatAudioMP3, "|- Восточноазиатский рок (lossy)"},
	{1716, models.CatAudio, "Зарубежный Metal"},
	{1796, models.CatAudioLossless, "|- Avant-garde, Experimental Metal (lossless)"},
	{1797, models.CatAudioMP3, "|- Avant-garde, Experimental Metal (lossy)"},
	{1719, models.CatAudioLossless, "|- Black (lossless)"},
	{1778, models.CatAudioMP3, "|- Black (lossy)"},
	{1779, models.CatAudioLossless, "|- Death, Doom (lossless)"},
	{1780, models.CatAudioMP3, "|- Death, Doom (lossy)"},
	{1720, models.CatAudioLossless, "|- Folk, Pagan, Viking (lossless)"},
	{798, models.CatAudioMP3, "|- Folk, Pagan, Viking (lossy)"},
	{1724, models.CatAudioLossless, "|- Gothic Metal (lossless)"},
	{1725, models.CatAudioMP3, "|- Gothic Metal (lossy)"},
	{1730, models.CatAudioLossless, "|- Grind, Brutal Death (lossless)"},
	{1731, models.CatAudioMP3, "|- Grind, Brutal Death (lossy)"},
	{1726, models.CatAudioLossless, "|- Heavy, Power, Progressive (lossless)"},
	{1727, models.CatAudioMP3, "|- Heavy, Power, Progressive (lossy)"},
	{1815, models.CatAudioLossless, "|- Sludge, Stoner, Post-Metal (lossless)"},
	{1816, models.CatAudioMP3, "|- Sludge, Stoner, Post-Metal (lossy)"},
	{1728, models.CatAudioLossless, "|- Thrash, Speed (lossless)"},
	{1729, models.CatAudioMP3, "|- Thrash, Speed (lossy)"},
	{2230, models.CatAudioLossless, "|- Сборники (lossless)"},
	{2231, models.CatAudioMP3, "|- Сборники (lossy)"},
	{1732, models.CatAudio, "Зарубежные Alternative, Punk, Independent"},
	{1736, models.CatAudioLossless, "|- Alternative & Nu-metal (lossless)"},
	{1737, models.CatAudioMP3, "|- Alternative & Nu-metal (lossy)"},
	{1738, models.CatAudioLossless, "|- Punk (lossless)"},
	{1739, models.CatAudioMP3, "|- Punk (lossy)"},
	{1740, models.CatAudioLossless, "|- Hardcore (lossless)"},
	{1741, models.CatAudioMP3, "|- Hardcore (lossy)"},
	{1773, models.CatAudioLossless, "|- Indie Rock, Indie Pop, Dream Pop, Brit-Pop (lossless)"},
	{202, models.CatAudioMP3, "|- Indie Rock, Indie Pop, Dream Pop, Brit-Pop (lossy)"},
	{172, models.CatAudioLossless, "|- Post-Punk, Shoegaze, Garage Rock, Noise Rock (lossless)"},
	{236, models.CatAudioMP3, "|- Post-Punk, Shoegaze, Garage Rock, Noise Rock (lossy)"},
	{1742, models.CatAudioLossless, "|- Post-Rock (lossless)"},
	{1743, models.CatAudioMP3, "|- Post-Rock (lossy)"},
	{1744, models.CatAudioLossless, "|- Industrial & Post-industrial (lossless)"},
	{1745, models.CatAudioMP3, "|- Industrial & Post-industrial (lossy)"},
	{1746, models.CatAudioLossless, "|- Emocore, Post-hardcore, Metalcore (lossless)"},
	{1747, models.CatAudioMP3, "|- Emocore, Post-hardcore, Metalcore (lossy)"},
	{1748, models.CatAudioLossless, "|- Gothic Rock & Dark Folk (lossless)"},
	{1749, models.CatAudioMP3, "|- Gothic Rock & Dark Folk (lossy)"},
	{2175, models.CatAudioLossless, "|- Avant-garde, Experimental Rock (lossless)"},
	{2174, models.CatAudioMP3, "|- Avant-garde, Experimental Rock (lossy)"},
	{722, models.CatAudio, "Отечественный Rock, Metal"},
	{737, models.CatAudioLossless, "|- Rock (lossless)"},
	{738, models.CatAudioMP3, "|- Rock (lossy)"},
	{464, models.CatAudioLossless, "|- Alternative, Punk, Independent (lossless)"},
	{463, models.CatAudioMP3, "|- Alternative, Punk, Independent (lossy)"},
	{739, models.CatAudioLossless, "|- Metal (lossless)"},
	{740, models.CatAudioMP3, "|- Metal (lossy)"},
	{951, models.CatAudioLossless, "|- Rock на языках народов xUSSR (lossless)"},
	{952, models.CatAudioMP3, "|- Rock на языках народов xUSSR (lossy)"},
	{1821, models.CatAudio, "Trance, Goa Trance, Psy-Trance, PsyChill, Ambient, Dub"},
	{1844, models.CatAudioLossless, "|- Goa Trance, Psy-Trance (lossless)"},
	{1822, models.CatAudioMP3, "|- Goa Trance, Psy-Trance (lossy)"},
	{1894, models.CatAudioLossless, "|- PsyChill, Ambient, Dub (lossless)"},
	{1895, models.CatAudioMP3, "|- PsyChill, Ambient, Dub (lossy)"},
	{460, models.CatAudioMP3, "|- Goa Trance, Psy-Trance, PsyChill, Ambient, Dub (Live Sets, Mixes) (lossy)"},
	{1818, models.CatAudioLossless, "|- Trance (lossless)"},
	{1819, models.CatAudioMP3, "|- Trance (lossy)"},
	{1847, models.CatAudioMP3, "|- Trance (Singles, EPs) (lossy)"},
	{1824, models.CatAudioMP3, "|- Trance (Radioshows, Podcasts, Live Sets, Mixes) (lossy)"},
	{1807, models.CatAudio, "House, Techno, Hardcore, Hardstyle, Jumpstyle"},
	{1829, models.CatAudioLossless, "|- Hardcore, Hardstyle, Jumpstyle (lossless)"},
	{1830, models.CatAudioMP3, "|- Hardcore, Hardstyle, Jumpstyle (lossy)"},
	{1831, models.CatAudioMP3, "|- Hardcore, Hardstyle, Jumpstyle (vinyl, web)"},
	{1857, models.CatAudioLossless, "|- House (lossless)"},
	{1859, models.CatAudioMP3, "|- House (Radioshow, Podcast, Liveset, Mixes)"},
	{1858, models.CatAudioMP3, "|- House (lossy)"},
	{840, models.CatAudioMP3, "|- House (Проморелизы, сборники) (lossy)"},
	{1860, models.CatAudioMP3, "|- House (Singles, EPs) (lossy)"},
	{1825, models.CatAudioLossless, "|- Techno (lossless)"},
	{1826, models.CatAudioMP3, "|- Techno (lossy)"},
	{1827, models.CatAudioMP3, "|- Techno (Radioshows, Podcasts, Livesets, Mixes)"},
	{1828, models.CatAudioMP3, "|- Techno (Singles, EPs) (lossy)"},
	{1808, models.CatAudio, "Drum & Bass, Jungle, Breakbeat, Dubstep, IDM, Electro"},
	{797, models.CatAudioLossless, "|- Electro, Electro-Freestyle, Nu Electro (lossless)"},
	{1805, models.CatAudioMP3, "|- Electro, Electro-Freestyle, Nu Electro (lossy)"},
	{1832, models.CatAudioLossless, "|- Drum & Bass, Jungle (lossless)"},
	{1833, models.CatAudioMP3, "|- Drum & Bass, Jungle (lossy)"},
	{1834, models.CatAudioMP3, "|- Drum & Bass, Jungle (Radioshows, Podcasts, Livesets, Mixes)"},
	{1836, models.CatAudioLossless, "|- Breakbeat (lossless)"},
	{1837, models.CatAudioMP3, "|- Breakbeat (lossy)"},
	{1839, models.CatAudioLossless, "|- Dubstep (lossless)"},
	{454, models.CatAudioMP3, "|- Dubstep (lossy)"},
	{1838, models.CatAudioMP3, "|- Breakbeat, Dubstep (Radioshows, Podcasts, Livesets, Mixes)"},
	{1840, models.CatAudioLossless, "|- IDM (lossless)"},
	{1841, models.CatAudioMP3, "|- IDM (lossy)"},
	{2229, models.CatAudioMP3, "|- IDM Discography & Collections (lossy)"},
	{1809, models.CatAudio, "Chillout, Lounge, Downtempo, Trip-Hop"},
	{1861, models.CatAudioLossless, "|- Chillout, Lounge, Downtempo (lossless)"},
	{1862, models.CatAudioMP3, "|- Chillout, Lounge, Downtempo (lossy)"},
	{1947, models.CatAudioLossless, "|- Nu Jazz, Acid Jazz, Future Jazz (lossless)"},
	{1946, models.CatAudioMP3, "|- Nu Jazz, Acid Jazz, Future Jazz (lossy)"},
	{1945, models.CatAudioLossless, "|- Trip Hop, Abstract Hip-Hop (lossless)"},
	{1944, models.CatAudioMP3, "|- Trip Hop, Abstract Hip-Hop (lossy)"},
	{1810, models.CatAudio, "Traditional Electronic, Ambient, Modern Classical, Electroacoustic, Experimental"},
	{1864, models.CatAudioLossless, "|- Traditional Electronic, Ambient (lossless)"},
	{1865, models.CatAudioMP3, "|- Traditional Electronic, Ambient (lossy)"},
	{1871, models.CatAudioLossless, "|- Modern Classical, Electroacoustic (lossless)"},
	{1867, models.CatAudioMP3, "|- Modern Classical, Electroacoustic (lossy)"},
	{1869, models.CatAudioLossless, "|- Experimental (lossless)"},
	{1873, models.CatAudioMP3, "|- Experimental (lossy)"},
	{1811, models.CatAudio, "Industrial, Noise, EBM, Dark Electro, Aggrotech, Cyberpunk, Synthpop, New Wave"},
	{1868, models.CatAudioLossless, "|- EBM, Dark Electro, Aggrotech (lossless)"},
	{1875, models.CatAudioMP3, "|- EBM, Dark Electro, Aggrotech (lossy)"},
	{1877, models.CatAudioLossless, "|- Industrial, Noise (lossless)"},
	{1878, models.CatAudioMP3, "|- Industrial, Noise (lossy)"},
	{1907, models.CatAudio, "|- Cyberpunk, 8-bit, Chiptune (lossy & lossless)"},
	{1880, models.CatAudioLossless, "|- Synthpop, Futurepop, New Wave, Electropop (lossless)"},
	{1881, models.CatAudioMP3, "|- Synthpop, Futurepop, New Wave, Electropop (lossy)"},
	{466, models.CatAudioLossless, "|- Synthwave, Spacesynth, Dreamwave, Retrowave, Outrun (lossless)"},
	{465, models.CatAudioMP3, "|- Synthwave, Spacesynth, Dreamwave, Retrowave, Outrun (lossy)"},
	{1866, models.CatAudioLossless, "|- Darkwave, Neoclassical, Ethereal, Dungeon Synth (lossless)"},
	{406, models.CatAudioMP3, "|- Darkwave, Neoclassical, Ethereal, Dungeon Synth (lossy)"},
	{1299, models.CatAudio, "Hi-Res stereo и многоканальная музыка"},
	{1163, models.CatAudio, "|- Многоканальная музыка в формате Dolby Atmos"},
	{1164, models.CatAudio, "|- Классическая вокальная и хоровая музыка, Classical Crossover (Hi-R.."},
	{1884, models.CatAudio, "|- Классическая инструментальная музыка, Classical Crossover (Hi-Res)"},
	{1885, models.CatAudio, "|- Поп-музыка (Hi-Res)"},
	{2513, models.CatAudio, "|- New Age, Relax, Meditative & Flamenco (Hi-Res stereo и многоканальная музыка)"},
	{1755, models.CatAudio, "|- Rock (Hi-Res)"},
	{1757, models.CatAudio, "|- Prog Rock & Art Rock (Hi-Res)"},
	{1890, models.CatAudio, "|- Metal (Hi-Res)"},
	{1396, models.CatAudio, "|- Alternative, Punk, Independent (Hi-Res)"},
	{2302, models.CatAudio, "|- Jazz, Bop, Cool, Fusion, Avant-Garde Jazz (Hi-Res)"},
	{2346, models.CatAudio, "|- Bop (Hi-Res)"},
	{2303, models.CatAudio, "|- Vocal Jazz, Funk, Soul, R&B, Jazz-Pop (Hi-Res)"},
	{2345, models.CatAudio, "|- Blues (Hi-Res)"},
	{1893, models.CatAudio, "|- Электронная музыка (Hi-Res)"},
	{1397, models.CatAudio, "|- Саундтреки (Hi-Res)"},
	{2512, models.CatAudio, "|- Музыка других жанров (Hi-Res)"},
	{2219, models.CatAudio, "Оцифровки с аналоговых носителей"},
	{1660, models.CatAudio, "|- Классика и классика в современной обработке (оцифровки)"},
	{506, models.CatAudio, "|- Фольклор, народная и этническая музыка (оцифровки)"},
	{1835, models.CatAudio, "|- Rap, Hip-Hop, R'n'B, Reggae, Ska, Dub (оцифровки)"},
	{1625, models.CatAudio, "|- Саундтреки и мюзиклы (оцифровки)"},
	{1217, models.CatAudio, "|- Шансон, авторские, военные песни и марши (оцифровки)"},
	{974, models.CatAudio, "|- Музыка других жанров (оцифровки)"},
	{1444, models.CatAudio, "|- Зарубежная поп-музыка (оцифровки)"},
	{2401, models.CatAudio, "|- Советская эстрада, ретро, романсы (оцифровки)"},
	{239, models.CatAudio, "|- Отечественная поп-музыка (оцифровки)"},
	{450, models.CatAudio, "|- Инструментальная поп-музыка (оцифровки)"},
	{2301, models.CatAudio, "|- Джаз и блюз (оцифровки)"},
	{123, models.CatAudio, "|- Alternative, Punk, Independent (оцифровки)"},
	{1756, models.CatAudio, "|- Зарубежная рок-музыка (оцифровки)"},
	{1758, models.CatAudio, "|- Отечественная рок-музыка (оцифровки)"},
	{1766, models.CatAudio, "|- Зарубежный и Отечественный Metal (оцифровки)"},
	{1754, models.CatAudio, "|- Электронная музыка (оцифровки)"},
	{860, models.CatAudio, "Неофициальные конверсии цифровых форматов"},
	{453, models.CatAudio, "|- Конверсии Quadraphonic"},
	{1170, models.CatAudio, "|- Конверсии SACD"},
	{1759, models.CatAudio, "|- Конверсии Blu-Ray, ADVD и DVD-Audio"},
	{1852, models.CatAudio, "|- Апмиксы-Upmixes"},
	{413, models.CatAudioVideo, "Музыкальное SD видео"},
	{445, models.CatAudioVideo, "|- Классическая и современная академическая музыка (Видео)"},
	{702, models.CatAudioVideo, "|- Опера, Оперетта и Мюзикл (Видео)"},
	{1990, models.CatAudioVideo, "|- Балет и современная хореография (Видео)"},
	{1793, models.CatAudioVideo, "|- Классика в современной обработке, Classical Crossover (Видео)"},
	{1141, models.CatAudioVideo, "|- Фольклор, Народная и Этническая музыка и фламенко (Видео)"},
	{1775, models.CatAudioVideo, "|- New Age, Relax, Meditative, Рэп, Хип-Хоп, R'n'B, Reggae, Ska, Dub (Видео)"},
	{1227, models.CatAudioVideo, "|- Зарубежный и Отечественный Шансон, Авторская и Военная песня (Видео)"},
	{475, models.CatAudioVideo, "|- Музыка других жанров, Советская эстрада, ретро, романсы (Видео)"},
	{1121, models.CatAudioVideo, "|- Отечественная поп-музыка (Видео)"},
	{431, models.CatAudioVideo, "|- Зарубежная Поп-музыка, Eurodance, Disco (Видео)"},
	{2378, models.CatAudioVideo, "|- Восточноазиатская поп-музыка (Видео)"},
	{1039, models.CatAudioVideo, "|- Восточноазиатский Rock (Видео)"},
	{2383, models.CatAudioVideo, "|- Разножанровые сборные концерты и сборники видеоклипов (Видео)"},
	{2305, models.CatAudioVideo, "|- Джаз и Блюз (Видео)"},
	{1782, models.CatAudioVideo, "|- Rock (Видео)"},
	{1787, models.CatAudioVideo, "|- Metal (Видео)"},
	{1789, models.CatAudioVideo, "|- Зарубежный Alternative, Punk, Independent (Видео)"},
	{1791, models.CatAudioVideo, "|- Отечественный Рок, Панк, Альтернатива (Видео)"},
	{1912, models.CatAudioVideo, "|- Электронная музыка (Видео)"},
	{1189, models.CatAudioVideo, "|- Документальные фильмы о музыке и музыкантах (Видео)"},
	{2403, models.CatAudioVideo, "Музыкальное DVD видео"},
	{984, models.CatAudioVideo, "|- Классическая и современная академическая музыка (DVD Видео)"},
	{983, models.CatAudioVideo, "|- Опера, Оперетта и Мюзикл (DVD Видео)"},
	{2352, models.CatAudioVideo, "|- Балет и современная хореография (DVD Видео)"},
	{2384, models.CatAudioVideo, "|- Классика в современной обработке, Classical Crossover (DVD Видео)"},
	{1142, models.CatAudioVideo, "|- Фольклор, Народная и Этническая музыка и Flamenco (DVD Видео)"},
	{1107, models.CatAudioVideo, "|- New Age, Relax, Meditative, Рэп, Хип-Хоп, R'n'B, Reggae, Ska, Dub (DVD Видео)"},
	{1228, models.CatAudioVideo, "|- Зарубежный и Отечественный Шансон, Авторская и Военная песня (DVD Видео)"},
	{988, models.CatAudioVideo, "|- Музыка других жанров, Советская эстрада, ретро, романсы (DVD Видео)"},
	{1122, models.CatAudioVideo, "|- Отечественная поп-музыка (DVD Видео)"},
	{986, models.CatAudioVideo, "|- Зарубежная Поп-музыка, Eurodance, Disco (DVD Видео)"},
	{2379, models.CatAudioVideo, "|- Восточноазиатская поп-музыка (DVD Видео)"},
	{1038, models.CatAudioVideo, "|- Восточноазиатский Rock (DVD Видео)"},
	{2088, models.CatAudioVideo, "|- Разножанровые сборные концерты и сборники видеоклипов (DVD Видео)"},
	{2304, models.CatAudioVideo, "|- Джаз и Блюз (DVD Видео)"},
	{1783, models.CatAudioVideo, "|- Зарубежный Rock (DVD Видео)"},
	{1788, models.CatAudioVideo, "|- Зарубежный Metal (DVD Видео)"},
	{1790, models.CatAudioVideo, "|- Зарубежный Alternative, Punk, Independent (DVD Видео)"},
	{1792, models.CatAudioVideo, "|- Отечественный Рок, Метал, Панк, Альтернатива (DVD Видео)"},
	{1886, models.CatAudioVideo, "|- Электронная музыка (DVD Видео)"},
	{2509, models.CatAudioVideo, "|- Документальные фильмы о музыке и музыкантах (DVD Видео)"},
	{2507, models.CatAudioVideo, "Неофициальные DVD видео"},
	{2263, models.CatAudioVideo, "|- Классическая музыка, Опера, Балет, Мюзикл (Неофициальные DVD Видео)"},
	{2511, models.CatAudioVideo, "|- Шансон, Авторская песня, Сборные концерты, МДЖ (Неофициальные DVD Видео)"},
	{2264, models.CatAudioVideo, "|- Зарубежная и Отечественная Поп-музыка (Неофициальные DVD Видео)"},
	{2262, models.CatAudioVideo, "|- Джаз и Блюз (Неофициальные DVD Видео)"},
	{2261, models.CatAudioVideo, "|- Зарубежная и Отечественная Рок-музыка (Неофициальные DVD Видео)"},
	{1887, models.CatAudioVideo, "|- Электронная музыка (Неофициальные DVD Видео)"},
	{2531, models.CatAudioVideo, "|- Прочие жанры (Неофициальные DVD Видео)"},
	{2400, models.CatAudioVideo, "Музыкальное HD видео"},
	{1812, models.CatAudioVideo, "|- Классическая и современная академическая музыка (HD Видео)"},
	{655, models.CatAudioVideo, "|- Опера, Оперетта и Мюзикл (HD Видео)"},
	{1777, models.CatAudioVideo, "|- Балет и современная хореография (HD Видео)"},
	{2530, models.CatAudioVideo, "|- Фольклор, Народная, Этническая музыка и Flamenco (HD Видео)"},
	{2529, models.CatAudioVideo, "|- New Age, Relax, Meditative, Рэп, Хип-Хоп, R'n'B, Reggae, Ska, Dub (HD Видео)"},
	{1781, models.CatAudioVideo, "|- Музыка других жанров, Разножанровые сборные концерты (HD видео)"},
	{2508, models.CatAudioVideo, "|- Зарубежная поп-музыка (HD Видео)"},
	{2426, models.CatAudioVideo, "|- Отечественная поп-музыка (HD Видео)"},
	{2351, models.CatAudioVideo, "|- Восточноазиатская Поп-музыка (HD Видео)"},
	{1340, models.CatAudioVideo, "|- Восточноазиатский Rock (HD Видео)"},
	{2306, models.CatAudioVideo, "|- Джаз и Блюз (HD Видео)"},
	{1795, models.CatAudioVideo, "|- Зарубежный рок (HD Видео)"},
	{2271, models.CatAudioVideo, "|- Отечественный рок (HD видео)"},
	{1913, models.CatAudioVideo, "|- Электронная музыка (HD Видео)"},
	{1784, models.CatAudioVideo, "|- UHD музыкальное видео"},
	{1892, models.CatAudioVideo, "|- Документальные фильмы о музыке и музыкантах (HD Видео)"},
	{2266, models.CatAudioVideo, "|- Официальные апскейлы (Blu-ray, HDTV, WEB-DL)"},
	{518, models.CatAudioVideo, "Некондиционное музыкальное видео (Видео, DVD видео, HD видео)"},
	{5, models.CatPCGames, "Игры для Windows"},
	{635, models.CatPCGames, "|- Горячие Новинки"},
	{127, models.CatPCGames, "|- Аркады"},
	{2203, models.CatPCGames, "|- Файтинги"},
	{647, models.CatPCGames, "|- Экшены от первого лица"},
	{646, models.CatPCGames, "|- Экшены от третьего лица"},
	{50, models.CatPCGames, "|- Хорроры"},
	{53, models.CatPCGames, "|- Приключения и квесты"},
	{1008, models.CatPCGames, "|- Квесты в стиле \"Поиск предметов\""},
	{900, models.CatPCGames, "|- Визуальные новеллы"},
	{128, models.CatPCGames, "|- Для самых маленьких"},
	{2204, models.CatPCGames, "|- Логические игры"},
	{278, models.CatPCGames, "|- Шахматы"},
	{52, models.CatPCGames, "|- Ролевые игры"},
	{54, models.CatPCGames, "|- Симуляторы"},
	{51, models.CatPCGames, "|- Стратегии в реальном времени"},
	{2226, models.CatPCGames, "|- Пошаговые стратегии"},
	{2118, models.CatPCGames, "|- Антологии и сборники игр"},
	{1310, models.CatPCGames, "|- Старые игры (Экшены)"},
	{2410, models.CatPCGames, "|- Старые игры (Ролевые игры)"},
	{2205, models.CatPCGames, "|- Старые игры (Стратегии)"},
	{2225, models.CatPCGames, "|- Старые игры (Приключения и квесты)"},
	{2206, models.CatPCGames, "|- Старые игры (Симуляторы)"},
	{1007, models.CatPCGames, "|- Старые игры (Аркады)"},
	{2228, models.CatPCGames, "|- IBM-PC-несовместимые компьютеры"},
	{139, models.CatPCGames, "Прочее для Windows-игр"},
	{2478, models.CatPCGames, "|- Официальные патчи, моды, плагины, дополнения"},
	{2480, models.CatPCGames, "|- Неофициальные модификации, плагины, дополнения"},
	{2481, models.CatPCGames, "|- Русификаторы"},
	{2142, models.CatPCGames, "Прочее для Microsoft Flight Simulator, Prepar3D, X-Plane"},
	{2060, models.CatPCGames, "|- Сценарии, меши и аэропорты для FS2004, FSX, P3D"},
	{2145, models.CatPCGames, "|- Самолёты и вертолёты для FS2004, FSX, P3D"},
	{2146, models.CatPCGames, "|- Миссии, трафик, звуки, паки и утилиты для FS2004, FSX, P3D"},
	{2143, models.CatPCGames, "|- Сценарии, миссии, трафик, звуки, паки и утилиты для X-Plane"},
	{2012, models.CatPCGames, "|- Самолёты и вертолёты для X-Plane"},
	{960, models.CatPCMac, "Игры для Apple Macintosh"},
	{537, models.CatPCMac, "|- Нативные игры для Mac"},
	{637, models.CatPCMac, "|- Игры для Mac с Wineskin, DOSBox, Cider и другими"},
	{899, models.CatPCGames, "Игры для Linux"},
	{1992, models.CatPCGames, "|- Нативные игры для Linux"},
	{2059, models.CatPCGames, "|- Игры для Linux с Wine, DOSBox и другими"},
	{548, models.CatConsole, "Игры для консолей"},
	{908, models.CatConsole, "|- PS"},
	{357, models.CatConsoleOther, "|- PS2"},
	{886, models.CatConsolePS3, "|- PS3"},
	{973, models.CatConsolePS4, "|- PS4"},
	{546, models.CatConsoleOther, "|- PS5"},
	{1352, models.CatConsolePSP, "|- PSP"},
	{1116, models.CatConsolePSP, "|- Игры PS1 для PSP"},
	{595, models.CatConsolePSVita, "|- PS Vita"},
	{887, models.CatConsoleXBox, "|- Original Xbox"},
	{510, models.CatConsoleXBox360, "|- Xbox 360"},
	{773, models.CatConsoleWii, "|- Wii U/Wii/GameCube"},
	{774, models.CatConsoleNDS, "|- 3DS/DS"},
	{1605, models.CatConsole, "|- Switch"},
	{968, models.CatConsole, "|- Dreamcast"},
	{129, models.CatConsole, "|- Остальные платформы"},
	{2185, models.CatConsoleOther, "Видео для консолей"},
	{2487, models.CatConsoleOther, "|- Видео для PS Vita"},
	{2182, models.CatConsoleOther, "|- Фильмы для PSP"},
	{2181, models.CatConsoleOther, "|- Сериалы для PSP"},
	{2180, models.CatConsoleOther, "|- Мультфильмы для PSP"},
	{2179, models.CatConsoleOther, "|- Дорамы для PSP"},
	{2186, models.CatConsoleOther, "|- Аниме для PSP"},
	{700, models.CatConsoleOther, "|- Видео для PSP"},
	{1926, models.CatConsoleOther, "|- Видео для PS3 и других консолей"},
	{650, models.CatPCMobileOther, "Игры для мобильных устройств"},
	{2149, models.CatPCMobileAndroid, "|- Игры для Android"},
	{2420, models.CatConsoleOther, "|- Игры для Oculus Quest"},
	{1004, models.CatPCMobileOther, "|- Игры для Symbian"},
	{1002, models.CatPCMobileOther, "|- Игры для Windows Mobile"},
	{240, models.CatOtherMisc, "Игровое видео"},
	{2415, models.CatOtherMisc, "|- Видеопрохождения игр"},
	{1012, models.CatPC, "Операционные системы от Microsoft"},
	{2489, models.CatPC, "|- Оригинальные образы Windows"},
	{2523, models.CatPC, "|- Сборки Windows 8 и далее"},
	{2153, models.CatPC, "|- Сборки Windows XP - Windows 7"},
	{1019, models.CatPC, "|- Операционные системы выпущенные до Windows XP"},
	{1021, models.CatPC, "|- Серверные ОС (оригинальные + сборки)"},
	{1025, models.CatPC, "|- Разное (сборки All-in-One, пакеты обновлений, утилиты, и прочее)"},
	{1376, models.CatPC, "Linux, Unix и другие ОС"},
	{1379, models.CatPC, "|- Операционные системы (Linux, Unix)"},
	{1381, models.CatPC, "|- Программное обеспечение (Linux, Unix)"},
	{1473, models.CatPC, "|- Другие ОС и ПО под них"},
	{1013, models.CatPC, "Системные программы"},
	{1028, models.CatPC, "|- Работа с жёстким диском"},
	{1029, models.CatPC, "|- Резервное копирование"},
	{1030, models.CatPC, "|- Архиваторы и файловые менеджеры"},
	{1031, models.CatPC, "|- Программы для настройки и оптимизации ОС"},
	{1032, models.CatPC, "|- Сервисное обслуживание компьютера"},
	{1033, models.CatPC, "|- Работа с носителями информации"},
	{1034, models.CatPC, "|- Информация и диагностика"},
	{1066, models.CatPC, "|- Программы для интернет и сетей"},
	{1035, models.CatPC, "|- ПО для защиты компьютера (Антивирусное ПО, Фаерволлы)"},
	{1536, models.CatPC, "|- Драйверы и прошивки"},
	{1051, models.CatPC, "|- Оригинальные диски к компьютерам и комплектующим"},
	{1040, models.CatPC, "|- Серверное ПО для Windows"},
	{1041, models.CatPC, "|- Изменение интерфейса ОС Windows"},
	{1636, models.CatPC, "|- Скринсейверы"},
	{1042, models.CatPC, "|- Разное (Системные программы под Windows)"},
	{1014, models.CatPC, "Системы для бизнеса, офиса, научной и проектной работы"},
	{2134, models.CatPC, "|- Медицина - интерактивный софт"},
	{1060, models.CatPC, "|- Всё для дома: кройка, шитьё, кулинария"},
	{1061, models.CatPC, "|- Офисные системы"},
	{1062, models.CatPC, "|- Системы для бизнеса"},
	{1067, models.CatPC, "|- Распознавание текста, звука и синтез речи"},
	{1086, models.CatPC, "|- Работа с PDF и DjVu"},
	{1068, models.CatPC, "|- Словари, переводчики"},
	{1063, models.CatPC, "|- Системы для научной работы"},
	{1087, models.CatPC, "|- САПР (общие и машиностроительные)"},
	{1192, models.CatPC, "|- САПР (электроника, автоматика, ГАП)"},
	{1088, models.CatPC, "|- Программы для архитекторов и строителей"},
	{1193, models.CatPC, "|- Библиотеки и проекты для архитекторов и дизайнеров интерьеров"},
	{1071, models.CatPC, "|- Прочие справочные системы"},
	{1073, models.CatPC, "|- Разное (Системы для бизнеса, офиса, научной и проектной работы)"},
	{1052, models.CatPC, "Веб-разработка и Программирование"},
	{1053, models.CatPC, "|- WYSIWYG Редакторы для веб-диза"},
	{1054, models.CatPC, "|- Текстовые редакторы с подсветкой"},
	{1055, models.CatPC, "|- Среды программирования, компиляторы и вспомогательные программы"},
	{1056, models.CatPC, "|- Компоненты для сред программирования"},
	{2077, models.CatPC, "|- Системы управления базами данных"},
	{1057, models.CatPC, "|- Скрипты и движки сайтов, CMS а также расширения к ним"},
	{1018, models.CatPC, "|- Шаблоны для сайтов и CMS"},
	{1058, models.CatPC, "|- Разное (Веб-разработка и программирование)"},
	{1016, models.CatPC, "Программы для работы с мультимедиа и 3D"},
	{1195, models.CatPC, "|- Тестовые диски для настройки аудио/видео аппаратуры"},
	{1079, models.CatPC, "|- Программные комплекты"},
	{1080, models.CatPC, "|- Плагины для программ компании Adobe"},
	{1081, models.CatPC, "|- Графические редакторы"},
	{1082, models.CatPC, "|- Программы для верстки, печати и работы со шрифтами"},
	{1083, models.CatPC, "|- 3D моделирование, рендеринг и плагины для них"},
	{1084, models.CatPC, "|- Анимация"},
	{1085, models.CatPC, "|- Создание BD/HD/DVD-видео"},
	{1089, models.CatPC, "|- Редакторы видео"},
	{1090, models.CatPC, "|- Видео- Аудио- конверторы"},
	{1065, models.CatPC, "|- Аудио- и видео-, CD- проигрыватели и каталогизаторы"},
	{1064, models.CatPC, "|- Каталогизаторы и просмотрщики графики"},
	{1092, models.CatPC, "|- Разное (Программы для работы с мультимедиа и 3D)"},
	{1204, models.CatPC, "|- Виртуальные студии, секвенсоры и аудиоредакторы"},
	{1027, models.CatPC, "|- Виртуальные инструменты и синтезаторы"},
	{1199, models.CatPC, "|- Плагины для обработки звука"},
	{1091, models.CatPC, "|- Разное (Программы для работы со звуком)"},
	{828, models.CatOtherMisc, "Материалы для мультимедиа и дизайна"},
	{1357, models.CatOtherMisc, "|- Авторские работы"},
	{890, models.CatOtherMisc, "|- Официальные сборники векторных клипартов"},
	{830, models.CatOtherMisc, "|- Прочие векторные клипарты"},
	{1290, models.CatOtherMisc, "|- Photostocks"},
	{1962, models.CatOtherMisc, "|- Дополнения для программ компоузинга и постобработки"},
	{831, models.CatOtherMisc, "|- Рамки, шаблоны, текстуры и фоны"},
	{829, models.CatOtherMisc, "|- Прочие растровые клипарты"},
	{633, models.CatOtherMisc, "|- 3D модели, сцены и материалы"},
	{1009, models.CatOtherMisc, "|- Футажи"},
	{1963, models.CatOtherMisc, "|- Прочие сборники футажей"},
	{1954, models.CatOtherMisc, "|- Музыкальные библиотеки"},
	{1010, models.CatOtherMisc, "|- Звуковые эффекты"},
	{1674, models.CatOtherMisc, "|- Библиотеки сэмплов"},
	{2421, models.CatOtherMisc, "|- Библиотеки и саундбанки для сэмплеров, пресеты для синтезаторов"},
	{2492, models.CatOtherMisc, "|- Multitracks"},
	{839, models.CatOtherMisc, "|- Материалы для создания меню и обложек DVD"},
	{1679, models.CatOtherMisc, "|- Дополнения, стили, кисти, формы, узоры для программ Adobe"},
	{1011, models.CatOtherMisc, "|- Шрифты"},
	{835, models.CatOtherMisc, "|- Разное (Материалы для мультимедиа и дизайна)"},
	{1503, models.CatOtherMisc, "ГИС, системы навигации и карты"},
	{1507, models.CatOtherMisc, "|- ГИС (Геоинформационные системы)"},
	{1526, models.CatOtherMisc, "|- Карты, снабженные программной оболочкой"},
	{1508, models.CatOtherMisc, "|- Атласы и карты современные (после 1950 г.)"},
	{1509, models.CatOtherMisc, "|- Атласы и карты старинные (до 1950 г.)"},
	{1510, models.CatOtherMisc, "|- Карты прочие (астрономические, исторические, тематические)"},
	{1511, models.CatOtherMisc, "|- Встроенная автомобильная навигация"},
	{1512, models.CatOtherMisc, "|- Garmin"},
	{1513, models.CatOtherMisc, "|- Ozi"},
	{1514, models.CatOtherMisc, "|- TomTom"},
	{1515, models.CatOtherMisc, "|- Navigon / Navitel"},
	{1516, models.CatOtherMisc, "|- Igo"},
	{1517, models.CatOtherMisc, "|- Разное - системы навигации и карты"},
	{285, models.CatPCMobileOther, "Приложения для мобильных устройств"},
	{2154, models.CatPCMobileAndroid, "|- Приложения для Android"},
	{1005, models.CatPCMobileOther, "|- Приложения для Java"},
	{289, models.CatPCMobileOther, "|- Приложения для Symbian"},
	{290, models.CatPCMobileOther, "|- Приложения для Windows Mobile"},
	{288, models.CatPCMobileOther, "|- Софт для работы с телефоном"},
	{292, models.CatPCMobileOther, "|- Прошивки для телефонов"},
	{291, models.CatPCMobileOther, "|- Обои и темы"},
	{957, models.CatPCMobileOther, "Видео для мобильных устройств"},
	{287, models.CatPCMobileOther, "|- Видео для смартфонов и КПК"},
	{286, models.CatPCMobileOther, "|- Видео в формате 3GP для мобильных"},
	{1366, models.CatPCMac, "Apple Macintosh"},
	{1368, models.CatPCMac, "|- Mac OS (для Macintosh)"},
	{1383, models.CatPCMac, "|- Mac OS (для РС-Хакинтош)"},
	{1394, models.CatPCMac, "|- Программы для просмотра и обработки видео (Mac OS)"},
	{1370, models.CatPCMac, "|- Программы для создания и обработки графики (Mac OS)"},
	{2237, models.CatPCMac, "|- Плагины для программ компании Adobe (Mac OS)"},
	{1372, models.CatPCMac, "|- Аудио редакторы и конвертеры (Mac OS)"},
	{1373, models.CatPCMac, "|- Системные программы (Mac OS)"},
	{1375, models.CatPCMac, "|- Офисные программы (Mac OS)"},
	{1371, models.CatPCMac, "|- Программы для интернета и сетей (Mac OS)"},
	{1374, models.CatPCMac, "|- Другие программы (Mac OS)"},
	{1933, models.CatPCMobileiOS, "iOS"},
	{1935, models.CatPCMobileiOS, "|- Программы для iOS"},
	{1003, models.CatPCMobileiOS, "|- Игры для iOS"},
	{1937, models.CatPCMobileiOS, "|- Разное для iOS"},
	{2235, models.CatPCMobileiOS, "Видео"},
	{1908, models.CatPCMobileiOS, "|- Фильмы для iPod, iPhone, iPad"},
	{864, models.CatPCMobileiOS, "|- Сериалы для iPod, iPhone, iPad"},
	{863, models.CatPCMobileiOS, "|- Мультфильмы для iPod, iPhone, iPad"},
	{2535, models.CatPCMobileiOS, "|- Аниме для iPod, iPhone, iPad"},
	{2534, models.CatPCMobileiOS, "|- Музыкальное видео для iPod, iPhone, iPad"},
	{2238, models.CatPCMac, "Видео HD"},
	{1936, models.CatPCMac, "|- Фильмы HD для Apple TV"},
	{315, models.CatPCMac, "|- Сериалы HD для Apple TV"},
	{1363, models.CatPCMac, "|- Мультфильмы HD для Apple TV"},
	{2082, models.CatPCMac, "|- Документальное видео HD для Apple TV"},
	{2241, models.CatPCMac, "|- Музыкальное видео HD для Apple TV"},
	{2236, models.CatAudio, "Аудио"},
	{1909, models.CatAudioAudiobook, "|- Аудиокниги (AAC, ALAC)"},
	{1927, models.CatAudioLossless, "|- Музыка lossless (ALAC)"},
	{2240, models.CatAudio, "|- Музыка Lossy (AAC-iTunes)"},
	{2248, models.CatAudio, "|- Музыка Lossy (AAC)"},
	{2244, models.CatAudio, "|- Музыка Lossy (AAC) (Singles, EPs)"},
	{10, models.CatOtherMisc, "Разное (раздачи)"},
	{865, models.CatOtherMisc, "|- Психоактивные аудиопрограммы"},
	{1100, models.CatOtherMisc, "|- Аватары, Иконки, Смайлы"},
	{1643, models.CatOtherMisc, "|- Живопись, Графика, Скульптура, Digital Art"},
	{848, models.CatOtherMisc, "|- Картинки"},
	{808, models.CatOtherMisc, "|- Любительские фотографии"},
	{630, models.CatOtherMisc, "|- Обои"},
	{1664, models.CatOtherMisc, "|- Фото знаменитостей"},
	{148, models.CatAudio, "|- Аудио"},
	{807, models.CatTVOther, "|- Видео"},
	{147, models.CatBooks, "|- Публикации и учебные материалы (тексты)"},
	{847, models.CatMoviesOther, "|- Трейлеры и дополнительные материалы к фильмам"},
	{1167, models.CatTVOther, "|- Любительские видеоклипы"},
	{321, models.CatOther, "|- Отчеты о встречах"},
}

func (rt *RuTracker) loginURL() string  { return rt.SiteLink() + "forum/login.php" }
func (rt *RuTracker) searchURL() string { return rt.SiteLink() + "forum/tracker.php" }

// ConfigurationForSetup ports upstream GetConfigurationForSetup: reset the
// stored cookie, fetch the login page and extract the captcha image/fields
// when present. Any failure is logged and swallowed upstream
// ("Error loading configuration: …"), the form is rendered regardless.
func (rt *RuTracker) ConfigurationForSetup(ctx context.Context) ([]map[string]any, error) {
	if cd := rt.ConfigData(); cd != nil && cd.CookieHeader != nil {
		cd.CookieHeader.Value = ""
	}
	if rt.Client != nil {
		rt.loadCaptchaConfig(ctx)
	}
	return rt.Base.ConfigurationForSetup(ctx)
}

func (rt *RuTracker) loadCaptchaConfig(ctx context.Context) {
	res, err := rt.Client.Do(ctx, rt.ID(), &httpclient.Request{
		URL:     rt.loginURL(),
		Cookies: rt.CookieHeader(),
	})
	if err != nil {
		rt.log().Error("Error loading configuration: " + err.Error())
		return
	}
	content, err := rtDecode(res.Body)
	if err != nil {
		rt.log().Error("Error loading configuration: " + err.Error())
		return
	}
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(content))
	if err != nil {
		rt.log().Error("Error loading configuration: " + err.Error())
		return
	}
	captchaImg := doc.Find(`img[src^="https://static.rutracker.cc/captcha/"]`).First()
	if captchaImg.Length() == 0 {
		rt.cfg.CaptchaImage.Value = nil
		return
	}
	if src, ok := captchaImg.Attr("src"); ok {
		captchaRes, err := rt.Client.Do(ctx, rt.ID(), &httpclient.Request{
			URL:     src,
			Cookies: rt.CookieHeader(),
		})
		if err != nil {
			rt.log().Error("Error loading configuration: " + err.Error())
			return
		}
		rt.cfg.CaptchaImage.Value = captchaRes.Body
	}
	rt.capCodeField, _ = doc.Find(`input[name^="cap_code_"]`).First().Attr("name")
	rt.capSid, _ = doc.Find(`input[name="cap_sid"]`).First().Attr("value")
}

// ApplyConfiguration ports upstream ApplyConfiguration: load values, POST
// the login form and verify the welcome marker
// `id="logged-in-username"` in the response. Success → RequiresTesting.
func (rt *RuTracker) ApplyConfiguration(ctx context.Context, items []indexer.ConfigItem) (indexer.ConfigStatus, error) {
	if err := rt.ApplyItems(items); err != nil {
		return indexer.ConfigCompleted, err
	}
	if err := rt.LoadValuesFromJSON(nil, false); err != nil {
		return indexer.ConfigCompleted, err
	}
	if err := rt.doLogin(ctx); err != nil {
		return indexer.ConfigCompleted, err
	}
	return indexer.ConfigRequiresTesting, nil
}

// doLogin ports the upstream login POST (RequestLoginAndFollowRedirect with
// referer = login page) plus ConfigureIfOK semantics.
func (rt *RuTracker) doLogin(ctx context.Context) error {
	if rt.Client == nil {
		return errors.New("http client not configured")
	}
	pairs := rtFormPairs{
		{"login_username", rt.cfg.Username.Value},
		{"login_password", rt.cfg.Password.Value},
		{"login", "Login"},
	}
	if strings.TrimSpace(rt.capSid) != "" {
		pairs = append(pairs,
			[2]string{"cap_sid", rt.capSid},
			[2]string{rt.capCodeField, rt.cfg.CaptchaText.Value},
		)
		// upstream clears the captcha state after use.
		rt.capSid = ""
		rt.capCodeField = ""
	}

	res, err := rt.Client.Do(ctx, rt.ID(), &httpclient.Request{
		Method:  http.MethodPost,
		URL:     rt.loginURL(),
		RawBody: pairs.encode(),
		Headers: rtFormContentType,
		Cookies: rt.CookieHeader(),
		Referer: rt.loginURL(),
	})
	if err != nil {
		return err
	}
	content, err := rtDecode(res.Body)
	if err != nil {
		return err
	}

	// upstream ConfigureIfOK: welcome marker → persist cookies, configured.
	if strings.Contains(content, `id="logged-in-username"`) {
		if u, err := url.Parse(rt.SiteLink()); err == nil {
			rt.SetCookieHeader(rt.Client.CookieHeader(rt.ID(), u))
		}
		rt.SetConfigured(true)
		rt.SaveConfig(ctx)
		return nil
	}

	// failure: surface the site error message, upstream falls back to
	// "RuTracker authentication failed" only when no error node exists.
	errorMessage := "RuTracker authentication failed"
	if doc, docErr := goquery.NewDocumentFromReader(strings.NewReader(content)); docErr == nil {
		if sel := doc.Find("h4.warnColor1.tCenter.mrg_16, div.msg-main").First(); sel.Length() > 0 {
			errorMessage = strings.TrimSpace(sel.Text())
		}
	}
	return &indexer.IndexerError{IndexerID: rt.ID(), Kind: indexer.KindConfig, Message: errorMessage}
}

func (rt *RuTracker) ResultsForQuery(ctx context.Context, q *models.TorznabQuery, isMeta bool) (*indexer.IndexerResult, error) {
	return rt.ResultsForQueryWith(ctx, q, isMeta, rt.perform)
}

func (rt *RuTracker) Test(ctx context.Context) error {
	return rt.Base.Test(ctx, rt.perform)
}

// perform ports upstream PerformQuery: for every search URL, request it
// with the session cookie; when the response is missing the logged-in
// marker, re-login and repeat the request (upstream re-request the SAME
// url). Results are returned ordered by descending publish date.
func (rt *RuTracker) perform(ctx context.Context, query *models.TorznabQuery) ([]*models.ReleaseInfo, error) {
	if rt.Client == nil {
		return nil, errors.New("http client not configured")
	}
	var releases []*models.ReleaseInfo
	for _, searchURL := range rt.createSearchURLs(query) {
		content, err := rt.getPage(ctx, searchURL)
		if err != nil {
			return nil, err
		}
		if !strings.Contains(content, `id="logged-in-username"`) {
			// upstream: await ApplyConfiguration(null) — re-login with the
			// stored credentials, then re-issue the same search request.
			if _, err := rt.ApplyConfiguration(ctx, nil); err != nil {
				return nil, err
			}
			content, err = rt.getPage(ctx, searchURL)
			if err != nil {
				return nil, err
			}
		}

		doc, err := goquery.NewDocumentFromReader(strings.NewReader(content))
		if err != nil {
			return nil, rt.parseError(content, err)
		}
		rows := doc.Find("table#tor-tbl > tbody > tr")
		rows.Each(func(_ int, row *goquery.Selection) {
			release, err := rt.parseReleaseRow(row)
			if err != nil {
				// upstream logs the row dump and skips the row.
				rowHTML, _ := goquery.OuterHtml(row)
				rt.log().Error(fmt.Sprintf("%s: Error while parsing row '%s':\n\n%v", rt.ID(), rowHTML, err))
				return
			}
			if release != nil {
				releases = append(releases, release)
			}
		})
	}
	// upstream: releases.OrderByDescending(o => o.PublishDate)
	sort.SliceStable(releases, func(i, j int) bool {
		return releases[i].PublishDate.After(releases[j].PublishDate)
	})
	return releases, nil
}

// createSearchURLs ports upstream CreateSearchUrlsForQuery: the query term
// is wildcard-sanitized ([^a-zA-Zа-яА-ЯёЁ0-9]+ → "%"), season/episode are
// appended for tv-search ("ТВ | Сезон: N" / "Серии: E"), categories are
// mapped to forum ids and chunked by 200 into repeated "f" params.
func (rt *RuTracker) createSearchURLs(query *models.TorznabQuery) []string {
	var qc shared.QueryParams

	searchString := query.SearchTerm
	//  replace any space, special char, etc. with % (wildcard)
	if strings.TrimSpace(searchString) != "" {
		searchString = rtSearchTermSanitizeRe.ReplaceAllString(searchString, "%")
	}

	if strings.TrimSpace(rt.cfg.SearchByUploader.Value) != "" {
		qc.Set("pn", rt.cfg.SearchByUploader.Value)
	}

	// if the search string is empty use the getnew view
	if strings.TrimSpace(searchString) == "" {
		qc.Set("nm", searchString)
	} else {
		// use the normal search
		// upstream quirk (RuTracker.cs:1563): no "-" can survive the
		// wildcard pass above, so this Replace is effectively dead code.
		searchString = strings.ReplaceAll(searchString, "-", " ")

		if query.Season != nil && *query.Season > 0 {
			searchString += " ТВ | Сезон: " + strconv.Itoa(*query.Season)
		}
		if strings.TrimSpace(query.Episode) != "" {
			searchString += " Серии: " + query.Episode
		}
		qc.Set("nm", searchString)
	}

	var urls []string
	if query.HasSpecifiedCategories() {
		trackerCategories := rtDistinctStrings(rt.Caps.Categories.MapTorznabCapsToTrackers(query, true))
		for _, chunk := range rtChunkStrings(trackerCategories, 200) {
			qc.Set("f", strings.Join(chunk, ","))
			urls = append(urls, rt.searchURL()+"?"+qc.GetQueryString())
		}
	} else {
		urls = append(urls, rt.searchURL()+"?"+qc.GetQueryString())
	}
	return urls
}

// parseReleaseRow ports upstream ParseReleaseRow. A row without a download
// link is "under moderation" and skipped (nil, nil); any parse failure
// aborts the row (upstream logs and returns null).
func (rt *RuTracker) parseReleaseRow(row *goquery.Selection) (*models.ReleaseInfo, error) {
	qDownloadLink := row.Find("td.tor-size > a.tr-dl").First()
	if qDownloadLink.Length() == 0 {
		// Expects moderation
		return nil, nil
	}
	dlHref, ok := qDownloadLink.Attr("href")
	if !ok {
		return nil, errors.New("download link has no href")
	}
	link, err := url.Parse(rt.SiteLink() + "forum/" + dlHref)
	if err != nil {
		return nil, err
	}

	qDetailsLink := row.Find("td.t-title-col > div.t-title > a.tLink").First()
	if qDetailsLink.Length() == 0 {
		return nil, errors.New("row has no details link")
	}
	detailsHref, ok := qDetailsLink.Attr("href")
	if !ok {
		return nil, errors.New("details link has no href")
	}
	details, err := url.Parse(rt.SiteLink() + "forum/" + detailsHref)
	if err != nil {
		return nil, err
	}

	title := strings.TrimSpace(qDetailsLink.Text())
	category := rt.getCategoryOfRelease(row)

	size, err := rt.getSizeOfRelease(row)
	if err != nil {
		return nil, err
	}
	seeders := rt.getSeedersOfRelease(row)
	leechers := parseutil.MustCoerceLong(strings.TrimSpace(row.Find("td:nth-child(8)").Text()))
	grabs := parseutil.MustCoerceLong(strings.TrimSpace(row.Find("td:nth-child(9)").Text()))
	peers := leechers + seeders

	publishDate, err := rt.getPublishDateOfRelease(row)
	if err != nil {
		return nil, err
	}

	authorSel := row.Find("td.u-name-col").First()
	if authorSel.Length() == 0 {
		return nil, errors.New("row has no author cell")
	}
	author := authorSel.Text()
	description := "Uploader: " + author + "<br>" + title

	minimumRatio := 1.0
	var minimumSeedTime int64
	releaseLink := link
	if rt.cfg.UseMagnetLinks.Value {
		releaseLink = details
	}
	return &models.ReleaseInfo{
		MinimumRatio:    &minimumRatio,
		MinimumSeedTime: &minimumSeedTime,
		Title: rtTitleParser{}.Parse(
			title,
			category,
			rt.cfg.StripRussian.Value,
			rt.cfg.MoveAllTags.Value,
			rt.cfg.MoveFirstTags.Value,
			rt.cfg.AddRussianToTitle.Value,
		),
		Description:          description,
		Details:              details,
		Link:                 releaseLink,
		Guid:                 details,
		Size:                 &size,
		Seeders:              &seeders,
		Peers:                &peers,
		Grabs:                &grabs,
		PublishDate:          publishDate,
		Category:             category,
		DownloadVolumeFactor: &minimumRatio,
		UploadVolumeFactor:   &minimumRatio,
	}, nil
}

// getSeedersOfRelease ports upstream GetSeedersOfRelease: rows pending
// moderation show a "N дн" age in the seeders cell and count as 0.
func (rt *RuTracker) getSeedersOfRelease(row *goquery.Selection) int64 {
	qSeeders := row.Find("td:nth-child(7)").First()
	if qSeeders.Length() == 0 || strings.Contains(qSeeders.Text(), "дн") {
		return 0
	}
	var seeders int64
	if b := qSeeders.Find("b").First(); b.Length() > 0 {
		if s := strings.TrimSpace(b.Text()); s != "" {
			seeders = parseutil.MustCoerceLong(s)
		}
	}
	return seeders
}

// getCategoryOfRelease ports upstream GetCategoryOfRelease: the forum id
// comes from the f= argument of the forum link.
func (rt *RuTracker) getCategoryOfRelease(row *goquery.Selection) []int {
	forum, _ := row.Find("td.f-name-col > div.f-name > a").First().Attr("href")
	cat := parseutil.GetArgumentFromQueryString(forum, "f")
	return rt.Caps.Categories.MapTrackerCatToNewznab(cat)
}

// getSizeOfRelease ports upstream GetSizeOfRelease
// (ParseUtil.GetBytes on the data-ts_text attribute).
func (rt *RuTracker) getSizeOfRelease(row *goquery.Selection) (int64, error) {
	ts, ok := row.Find("td.tor-size").First().Attr("data-ts_text")
	if !ok {
		return 0, errors.New("size cell has no data-ts_text attribute")
	}
	return parseutil.GetBytes(ts), nil
}

// getPublishDateOfRelease ports upstream GetPublishDateOfRelease
// (UnixTimestampToDateTime on the data-ts_text attribute).
func (rt *RuTracker) getPublishDateOfRelease(row *goquery.Selection) (time.Time, error) {
	ts, ok := row.Find("td:nth-child(10)").First().Attr("data-ts_text")
	if !ok {
		return time.Time{}, errors.New("date cell has no data-ts_text attribute")
	}
	seconds, err := strconv.ParseInt(strings.TrimSpace(ts), 10, 64)
	if err != nil {
		return time.Time{}, err
	}
	return time.Unix(seconds, 0), nil
}

// Download ports upstream Download: with UseMagnetLinks enabled, torrent
// links pointing at viewtopic.php are fetched and the magnet link from the
// attachments table replaces the download target.
func (rt *RuTracker) Download(ctx context.Context, link *url.URL) ([]byte, error) {
	if rt.cfg.UseMagnetLinks.Value && strings.Contains(link.Path+"?"+link.RawQuery, "viewtopic.php?t=") {
		res, err := rt.Client.Do(ctx, rt.ID(), &httpclient.Request{
			URL:     link.String(),
			Cookies: rt.CookieHeader(),
		})
		if err != nil {
			return nil, err
		}
		content, err := rtDecode(res.Body)
		if err != nil {
			return nil, err
		}
		doc, err := goquery.NewDocumentFromReader(strings.NewReader(content))
		if err != nil {
			return nil, err
		}
		magnetLink, _ := doc.Find(`table.attach a.magnet-link[href^="magnet:?"]`).First().Attr("href")
		if magnetLink == "" {
			return nil, fmt.Errorf("Failed to fetch magnet link from %s", link) //nolint:staticcheck // upstream RuTracker.cs error text, kept 1:1
		}
		magnetURL, err := url.Parse(magnetLink)
		if err != nil {
			return nil, err
		}
		link = magnetURL
	}
	return rt.Base.Download(ctx, link)
}

// getPage issues one upstream RequestWithCookiesAsync (GET with the session
// cookie) and decodes the windows-1251 body.
func (rt *RuTracker) getPage(ctx context.Context, target string) (string, error) {
	res, err := rt.Client.Do(ctx, rt.ID(), &httpclient.Request{
		URL:     target,
		Cookies: rt.CookieHeader(),
	})
	if err != nil {
		return "", err
	}
	return rtDecode(res.Body)
}

// log is a nil-safe logger (Base.Logger may be nil; logs are discarded).
func (rt *RuTracker) log() *slog.Logger {
	if rt.Logger != nil {
		return rt.Logger
	}
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// parseError mirrors upstream OnParseError: log the response dump and
// surface Exception("Parse error", ex).
func (rt *RuTracker) parseError(dump string, err error) error {
	rt.log().Error(fmt.Sprintf("Error while parsing result: %v\n%s", err, dump))
	return &indexer.IndexerError{IndexerID: rt.ID(), Kind: indexer.KindParse, Message: "Parse error"}
}

// rtDecode decodes a windows-1251 response body (upstream
// `Encoding.GetEncoding("windows-1251")`).
func rtDecode(body []byte) (string, error) {
	enc, err := htmlindex.Get("windows-1251")
	if err != nil {
		return "", err
	}
	decoded, err := enc.NewDecoder().Bytes(body)
	if err != nil {
		return "", err
	}
	return string(decoded), nil
}

// rtFormPairs is an ordered list of form fields (upstream
// Dictionary<string,string> insertion order; the Go client stack's
// url.Values would serialize alphabetically, so login posts go through
// RawBody — WebUtility.UrlEncode-compatible encoding).
type rtFormPairs [][2]string

func (p rtFormPairs) encode() string {
	parts := make([]string, len(p))
	for i, kv := range p {
		parts[i] = shared.Encode(kv[0]) + "=" + shared.Encode(kv[1])
	}
	return strings.Join(parts, "&")
}

// rtFormContentType is the header upstream WebClient sets for form posts.
var rtFormContentType = map[string]string{"Content-Type": "application/x-www-form-urlencoded"}

// rtSearchTermSanitizeRe ports the upstream wildcard sanitizer
// (RuTracker.cs:1548): any run of chars outside [a-zA-Zа-яА-ЯёЁ0-9]
// becomes a single "%".
var rtSearchTermSanitizeRe = regexp.MustCompile(`[^a-zA-Zа-яА-ЯёЁ0-9]+`)

// rtDistinctStrings mirrors upstream IEnumerable.Distinct() (ordinal,
// order-preserving).
func rtDistinctStrings(list []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range list {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

// rtChunkStrings mirrors upstream IEnumerableExtensions.ChunkBy(200).
func rtChunkStrings(list []string, size int) [][]string {
	var chunks [][]string
	for start := 0; start < len(list); start += size {
		end := start + size
		if end > len(list) {
			end = len(list)
		}
		chunks = append(chunks, list[start:end])
	}
	return chunks
}

// --- TitleParser (upstream nested RuTracker.TitleParser) -------------------

// NOTE on .NET regex classes: upstream \w / \W are Unicode-aware. Go's \w
// is ASCII-only, so the ports below spell the classes out with \p{L}
// (Unicode letters) / \p{N} (Unicode numbers) / "_" to stay 1:1 for all
// realistic release titles (residual gap: .NET word chars also include
// connector punctuation \p{Pc} and modifier letters \p{Lm}, which never
// occur in RuTracker titles).

// rtNonWordClass approximates the upstream class [\p{IsCyrillic}\W]
// (Cyrillic letters plus Unicode non-word characters) as a Go character
// class: punctuation, symbols, separators, control and mark runes plus the
// Cyrillic script (\p{Pc} connectors are word chars in .NET but are
// included here — they never occur in RuTracker titles).
const rtNonWordClass = `\p{Cyrillic}\p{P}\p{S}\p{Z}\p{C}\p{M}`

var (
	// upstream _stripCyrillicRegex (RuTracker.cs:1702):
	// (\([\p{IsCyrillic}\W]+)|(^[\p{IsCyrillic}\W\d]+/ )|
	// ([\p{IsCyrillic} -]+,+)|([\p{IsCyrillic}]+)
	rtStripCyrillicRe = regexp.MustCompile(`(\([` + rtNonWordClass + `]+\))|(^[` + rtNonWordClass + `\p{N}]+/ )|([\p{Cyrillic} -]+,+)|([\p{Cyrillic}]+)`)

	rtTvTitleCommaRe            = regexp.MustCompile(`\s(\d+),(\d+)`)
	rtTvTitleCyrillicXRe        = regexp.MustCompile(`(?i)([\s-])Х+([\s\)\]])`)
	rtTvTitleRusSeasonEpisodeOf = regexp.MustCompile(`(?i)Сезон\s*[:]*\s+(\d+).+(?:Серии|Эпизод|Выпуски)+\s*[:]*\s+(\d+(?:-\d+)?)\s*из\s*([\w?])`)
	rtTvTitleRusSeasonEpisode   = regexp.MustCompile(`(?i)Сезон\s*[:]*\s+(\d+).+(?:Серии|Эпизод|Выпуски)+\s*[:]*\s+(\d+(?:-\d+)?)`)
	rtTvTitleRusSeason          = regexp.MustCompile(`(?i)Сезон\s*[:]*\s+(\d+(?:-\d+)?)`)
	rtTvTitleRusSeasonAnime     = regexp.MustCompile(`(?i)ТВ[-]*(?:(\d+))`)
	rtTvTitleRusEpisodeOf       = regexp.MustCompile(`(?i)(?:Серии|Эпизод|Выпуски)+\s*[:]*\s+(\d+(?:-\d+)?)\s*из\s*([\w?])`)
	rtTvTitleRusEpisode         = regexp.MustCompile(`(?i)(?:Серии|Эпизод|Выпуски)+\s*[:]*\s+(\d+(?:-\d+)?)`)
	rtTvTitleRusEpisodeAnimeOf  = regexp.MustCompile(`(?i)\[(\d+(\+\d+)?)\s+из\s+(\d+(\+\d+)?)\]`)

	// upstream movie director strip (RuTracker.cs:1747).
	rtMovieDirectorRe = regexp.MustCompile(`(?i)(\([` + rtNonWordClass + `]+)(?:\s/\s(.+?))?\)`)

	rtDashPunctRe     = regexp.MustCompile(`\p{Pd}`)
	rtDouble4KRe      = regexp.MustCompile(`(?i)\b(2160p), 4K\b`)
	rtRusWordRe       = regexp.MustCompile(`(?i)\bRUS\b`)
	rtRipRe           = regexp.MustCompile(`(?i)\b-Rip\b`)
	rtHdtvRipRe       = regexp.MustCompile(`(?i)\bHDTVRip\b`)
	rtWebDlRip1Re     = regexp.MustCompile(`(?i)\bWEB-DLRip\b`)
	rtWebDlRip2Re     = regexp.MustCompile(`(?i)\bWEBDLRip\b`)
	rtWebDlRe         = regexp.MustCompile(`(?i)\bWEBDL\b`)
	rtKurazhRe        = regexp.MustCompile(`(?i)\bКураж-Бамбей\b`)
	rtOpenParenSlash  = regexp.MustCompile(`\(\s*\/\s*`)
	rtCloseParenSlash = regexp.MustCompile(`\s*\/\s*\)`)
	rtEmptyBracketsRe = regexp.MustCompile(`[\[\(]\s*[\)\]]`)
	rtMultiSpaceRe    = regexp.MustCompile(`\s+`)

	rtFourDigitsRe    = regexp.MustCompile(`\d{4}`)
	rtYearSlashYear   = regexp.MustCompile(`(\d{4}) *\/ *(\d{4})`)
	rtTitleTrimCutset = " &,.'!?+-_|/\\:"
)

// rtTitleParser ports upstream RuTracker.TitleParser (nested class).
type rtTitleParser struct{}

// Parse ports upstream TitleParser.Parse. The category slice is the mapped
// newznab category list of the release.
func (rtTitleParser) Parse(title string,
	category []int,
	stripCyrillicLetters bool,
	moveAllTagsToEndOfReleaseTitle bool,
	moveFirstTagsToEndOfReleaseTitle bool,
	addRussianToTitle bool) string {
	// https://www.fileformat.info/info/unicode/category/Pd/list.htm
	title = rtDashPunctRe.ReplaceAllString(title, "-")

	// replace double 4K quality in title
	title = rtDouble4KRe.ReplaceAllString(title, "${1}")

	if rtIsAnyTvCategory(category) {
		title = rtTvTitleCommaRe.ReplaceAllString(title, " ${1}-${2}")
		title = rtTvTitleCyrillicXRe.ReplaceAllString(title, "${1}XX${2}")

		title = rtTvTitleRusSeasonEpisodeOf.ReplaceAllString(title, "S${1}E${2} of ${3}")
		title = rtTvTitleRusSeasonEpisode.ReplaceAllString(title, "S${1}E${2}")
		title = rtTvTitleRusSeason.ReplaceAllString(title, "S${1}")
		title = rtTvTitleRusSeasonAnime.ReplaceAllString(title, "S${1}")
		title = rtTvTitleRusEpisodeOf.ReplaceAllString(title, "E${1} of ${2}")
		title = rtTvTitleRusEpisode.ReplaceAllString(title, "E${1}")
		title = rtTvTitleRusEpisodeAnimeOf.ReplaceAllString(title, "[E${1} of ${3}]")
	} else if rtIsAnyMovieCategory(category) {
		// remove director's name from title
		// rutracker movies titles look like: russian name / english name (russian director / english director) other stuff
		// Ирландец / The Irishman (Мартин Скорсезе / Martin Scorsese) [2019, США, криминал, драма, биография, WEB-DL 1080p] Dub (Пифагор) + MVO (Jaskier) + AVO (Юрий Сербин) + Sub Rus, Eng + Original Eng
		// this part should be removed: (Мартин Скорсезе / Martin Scorsese)
		title = rtMovieDirectorRe.ReplaceAllString(title, "")

		// Bluray quality fix: radarr parse Blu-ray Disc as Bluray-1080p but should be BR-DISK
		title = regexp.MustCompile(`(?i)\bBlu-ray Disc\b`).ReplaceAllString(title, "BR-DISK")
	}

	// language fix: all rutracker releases contains russian track
	if addRussianToTitle && (rtIsAnyTvCategory(category) || rtIsAnyMovieCategory(category)) && !rtRusWordRe.MatchString(title) {
		title += " RUS"
	}

	if stripCyrillicLetters {
		title = strings.Trim(rtStripCyrillicRe.ReplaceAllString(title, ""), " -")
	}

	if moveAllTagsToEndOfReleaseTitle {
		title = rtMoveAllTagsToEndOfReleaseTitle(title)
	} else if moveFirstTagsToEndOfReleaseTitle {
		title = rtMoveFirstTagsToEndOfReleaseTitle(title)
	}

	if rtIsAnyAudioCategory(category) {
		title = rtDetectRereleaseInReleaseTitle(title)
	}

	title = rtRipRe.ReplaceAllString(title, "Rip")
	title = rtHdtvRipRe.ReplaceAllString(title, "HDTV")
	title = rtWebDlRip1Re.ReplaceAllString(title, "WEB-DL")
	title = rtWebDlRip2Re.ReplaceAllString(title, "WEB-DL")
	title = rtWebDlRe.ReplaceAllString(title, "WEB-DL")
	title = rtKurazhRe.ReplaceAllString(title, "kurazh")

	title = rtOpenParenSlash.ReplaceAllString(title, "(")
	title = rtCloseParenSlash.ReplaceAllString(title, ")")

	title = rtEmptyBracketsRe.ReplaceAllString(title, "")

	title = strings.Trim(title, rtTitleTrimCutset)

	// replace multiple spaces with a single space
	title = rtMultiSpaceRe.ReplaceAllString(title, " ")

	return strings.TrimSpace(title)
}

func rtIsAnyTvCategory(category []int) bool {
	return rtCategoryOrSubContains(category, models.CatTV)
}

func rtIsAnyMovieCategory(category []int) bool {
	return rtCategoryOrSubContains(category, models.CatMovies)
}

func rtIsAnyAudioCategory(category []int) bool {
	return rtCategoryOrSubContains(category, models.CatAudio)
}

// rtCategoryOrSubContains mirrors upstream
// `category.Contains(cat.ID) || cat.SubCategories.Any(sub => category.Contains(sub.ID))`.
func rtCategoryOrSubContains(category []int, cat *models.Category) bool {
	for _, id := range category {
		if id == cat.ID {
			return true
		}
	}
	for _, sub := range cat.SubCategories {
		for _, id := range category {
			if id == sub.ID {
				return true
			}
		}
	}
	return false
}

// rtMoveAllTagsToEndOfReleaseTitle ports upstream MoveAllTagsToEndOfReleaseTitle.
// The balanced-group scans use the ORIGINAL input (matches are computed on
// `input`, mutations apply to `output`) — upstream quirk kept.
func rtMoveAllTagsToEndOfReleaseTitle(input string) string {
	output := input
	for _, braces := range [][2]byte{{'(', ')'}, {'[', ']'}} {
		for _, m := range rtFindBalancedTags(input, braces[0], braces[1]) {
			tag := input[m[0]:m[1]]
			output = strings.TrimSpace(strings.ReplaceAll(output, tag, "") + " " + tag)
		}
	}
	return strings.TrimSpace(output)
}

// rtMoveFirstTagsToEndOfReleaseTitle ports upstream MoveFirstTagsToEndOfReleaseTitle.
// Upstream iterates match objects captured before the loop while mutating
// the string, so the recorded indexes go stale; the bounds guards below
// mirror the upstream behaviour of aborting via exception (which the
// per-row catch upstream turns into a skipped row).
func rtMoveFirstTagsToEndOfReleaseTitle(input string) string {
	output := input
	for _, braces := range [][2]byte{{'(', ')'}, {'[', ']'}} {
		expectedIndex := 0
		matches := rtFindBalancedTags(output, braces[0], braces[1])
		for _, m := range matches {
			if m[0] > expectedIndex {
				if m[0] > len(output) {
					break
				}
				substring := output[expectedIndex:m[0]]
				if strings.TrimSpace(substring) == "" {
					expectedIndex = m[0]
				} else {
					break
				}
			}
			if m[1] > len(output) {
				break
			}
			tag := output[m[0]:m[1]]
			output = strings.TrimSpace(strings.Replace(output, tag, "", 1) + " " + tag)
			expectedIndex += len(tag)
		}
	}
	return strings.TrimSpace(output)
}

// rtDetectRereleaseInReleaseTitle ports upstream DetectRereleaseInReleaseTitle:
// a "year1/year2" pattern outside of any bracket tag marks a re-release.
func rtDetectRereleaseInReleaseTitle(input string) string {
	fullTitle := input

	squareBracketTags := rtFindSubstringsBetween(input, '[', ']', true)
	input = rtRemoveSubstrings(input, squareBracketTags)

	roundBracketTags := rtFindSubstringsBetween(input, '(', ')', true)
	input = rtRemoveSubstrings(input, roundBracketTags)

	yearsInTitle := rtFourDigitsRe.FindAllStringIndex(input, -1)
	if len(yearsInTitle) < 2 {
		// Can only be a re-release if there's at least 2 years in the title.
		return fullTitle
	}

	match := rtYearSlashYear.FindStringSubmatch(input)
	if match == nil {
		// Not in the expected format. Return the unaltered title.
		return fullTitle
	}

	originalYear := match[1]
	fullTitle = strings.ReplaceAll(fullTitle, match[0], originalYear)

	return fullTitle + "(Re-release)"
}

// rtFindBalancedTags returns the non-overlapping balanced (…) / […] spans
// of s, mirroring the upstream balanced-group regexes
// (`\((?>\((?<c>)|[^()]+|\)(?<-c>))*(?(c)(?!))\)` etc., RuTracker.cs:1698-1699):
// a span starts at every opening brace; unbalanced openings are skipped one
// position at a time.
func rtFindBalancedTags(s string, open, close byte) [][2]int {
	var out [][2]int
	for i := 0; i < len(s); i++ {
		if s[i] != open {
			continue
		}
		depth := 0
		for j := i; j < len(s); j++ {
			switch s[j] {
			case open:
				depth++
			case close:
				depth--
			}
			if depth == 0 {
				out = append(out, [2]int{i, j + 1})
				i = j
				break
			}
		}
	}
	return out
}

// rtFindSubstringsBetween ports StringUtil.FindSubstringsBetween
// (StringUtil.cs:222): pair every closing char (left to right) with the
// nearest unused opening char before it; nested pairs yield both the parent
// and the child substring.
func rtFindSubstringsBetween(source string, opening, closing byte, includeOpeningAndClosing bool) []string {
	var openingIndexes []int
	for i := len(source) - 1; i >= 0; i-- {
		if source[i] == opening {
			openingIndexes = append(openingIndexes, i)
		}
	}
	var closingIndexes []int
	for i := 0; i < len(source); i++ {
		if source[i] == closing {
			closingIndexes = append(closingIndexes, i)
		}
	}

	var out []string
	for _, closingIndex := range closingIndexes {
		idx := -1
		for i, o := range openingIndexes {
			if o < closingIndex {
				idx = i
				break
			}
		}
		if idx == -1 {
			continue
		}
		openingIndex := openingIndexes[idx]
		openingIndexes = append(openingIndexes[:idx], openingIndexes[idx+1:]...)

		substringIndex := openingIndex + 1
		substringLength := closingIndex - substringIndex
		if includeOpeningAndClosing {
			substringIndex--
			substringLength += 2
		}
		out = append(out, source[substringIndex:substringIndex+substringLength])
	}
	return out
}

// rtRemoveSubstrings ports StringUtil.RemoveSubstrings (longest first).
func rtRemoveSubstrings(source string, substrings []string) string {
	result := source
	sorted := append([]string(nil), substrings...)
	sort.SliceStable(sorted, func(i, j int) bool { return len(sorted[i]) > len(sorted[j]) })
	for _, substring := range sorted {
		result = strings.ReplaceAll(result, substring, "")
	}
	return result
}
