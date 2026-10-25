package manual

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"log/slog"
	"net/http"
	"net/url"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"vestt/common/httpclient"
	"vestt/common/indexer"
	"vestt/common/indexer/configdata"
	"vestt/common/indexer/shared"
	"vestt/common/models"
	"vestt/common/services"
)

// AnimeBytes ports Jackett.Common/Indexers/Definitions/AnimeBytes.cs.
//
// mechanism: user-passkey json-api (scrape.php)
//
// Correction to the SPEC-08 batch-1 table ("html-login (含 2FA)"): upstream
// 0.24.2900 AnimeBytes has NO HTML login and NO twoFactorCode. Configuration
// is Username + Passkey (Models/IndexerConfig/ConfigurationDataUserPasskey.cs
// + Bespoke/ConfigurationDataAnimeBytes.cs) and queries go through the
// scrape.php JSON API. The 2FA/login annotation stems from an older upstream
// revision; the reference tree is authoritative here.
//
// 澄清记录:
//   - BaseCachingWebIndexer's per-URL query cache is not ported (perf-only;
//     no observable behaviour change).
//   - Upstream wraps the whole parse/mapping block in try/catch →
//     OnParseError, so even `throw new Exception($"Unexpected response from
//     indexer request: {Error}")` surfaces as "Parse error". Replicated.
//   - StringUtil.Hash = MD5 over ASCII bytes (non-ASCII replaced with '?').
//
// Ported from: Definitions/AnimeBytes.cs.
type AnimeBytes struct {
	indexer.Base
	cfg *animeBytesConfig
}

type animeBytesConfig struct {
	configdata.ConfigData
	Username             *configdata.StringItem
	Passkey              *configdata.StringItem
	Instructions         *configdata.InfoItem
	FreeleechOnly        *configdata.BoolItem
	ExcludeHentai        *configdata.BoolItem
	IncludeRaw           *configdata.BoolItem
	SearchByYear         *configdata.BoolItem
	PadEpisode           *configdata.BoolItem
	AddJapaneseTitle     *configdata.BoolItem
	AddRomajiTitle       *configdata.BoolItem
	AddAlternativeTitles *configdata.BoolItem
	AddFileNameTitles    *configdata.BoolItem
	FilterSeasonEpisode  *configdata.BoolItem
}

func newAnimeBytesConfig() *animeBytesConfig {
	return &animeBytesConfig{
		ConfigData:           *configdata.NewConfigData(),
		Username:             configdata.NewStringItem("Username"),
		Passkey:              configdata.NewStringItem("Passkey"),
		Instructions:         configdata.NewInfoItem("", "Note: Go to AnimeBytes site and open your account settings. Go to 'Account' tab, move cursor over black part near 'Passkey' and copy its value. Your username is case sensitive."),
		FreeleechOnly:        configdata.NewBoolItem("Search freeleech only"),
		ExcludeHentai:        configdata.NewBoolItem("Exclude Hentai from results"),
		IncludeRaw:           configdata.NewBoolItem("Include RAW in results"),
		SearchByYear:         configdata.NewBoolItem("Search by year as a different argument in the request"),
		PadEpisode:           configdata.NewBoolItem("Pad episode number for Sonarr compatibility"),
		AddJapaneseTitle:     configdata.NewBoolItem("Add releases for Japanese Title"),
		AddRomajiTitle:       configdata.NewBoolItem("Add releases for Romaji Title"),
		AddAlternativeTitles: configdata.NewBoolItem("Add releases for Alternative Title(s)"),
		AddFileNameTitles:    configdata.NewBoolItem("Add releases based on single filename"),
		FilterSeasonEpisode:  configdata.NewBoolItem("Filter results by season/episode"),
	}
}

// NewAnimeBytes builds the indexer (services.Factory shape).
func NewAnimeBytes(deps services.Deps) (indexer.Indexer, error) {
	a := &AnimeBytes{cfg: newAnimeBytesConfig()}
	a.Base = indexer.Base{
		IDValue:          "animebytes",
		NameValue:        "AnimeBytes",
		DescriptionValue: "AnimeBytes is a Private site. Powered by Tentacles",
		SiteLinkValue:    "https://animebytes.tv/",
		DefaultSiteLink:  "https://animebytes.tv/",
		LangValue:        "en-US",
		TypeValue:        "private",
		Caps:             animeBytesCaps(),
		Config:           a.cfg,
		Client:           deps.Client,
		Saver:            deps.Saver,
		Cache:            deps.Cache,
	}
	// upstream: webclient.requestDelay = 3.1 (API limit: 1 request / 3 s).
	if deps.Client != nil {
		deps.Client.SetRequestDelay(a.IDValue, 3100*time.Millisecond)
	}
	return a, nil
}

func animeBytesCaps() *models.Capabilities {
	caps := models.NewCapabilities()
	caps.TVSearchParams = []string{models.TVParamQ, models.TVParamSeason, models.TVParamEp}
	caps.MovieSearchParams = []string{models.MovieParamQ}
	caps.MusicSearchParams = []string{models.MusicParamQ}
	caps.BookSearchParams = []string{models.BookParamQ}
	caps.SupportsRawSearch = true
	c := caps.Categories
	c.AddCategoryMapping("anime[tv_series]", models.CatTVAnime, "TV Series")
	c.AddCategoryMapping("anime[tv_special]", models.CatTVAnime, "TV Special")
	c.AddCategoryMapping("anime[ova]", models.CatTVAnime, "OVA")
	c.AddCategoryMapping("anime[ona]", models.CatTVAnime, "ONA")
	c.AddCategoryMapping("anime[dvd_special]", models.CatTVAnime, "DVD Special")
	c.AddCategoryMapping("anime[bd_special]", models.CatTVAnime, "BD Special")
	c.AddCategoryMapping("anime[movie]", models.CatMovies, "Movie")
	c.AddCategoryMapping("audio", models.CatAudio, "Music")
	c.AddCategoryMapping("gamec[game]", models.CatPCGames, "Game")
	c.AddCategoryMapping("gamec[visual_novel]", models.CatPCGames, "Game Visual Novel")
	c.AddCategoryMapping("printedtype[manga]", models.CatBooksComics, "Manga")
	c.AddCategoryMapping("printedtype[oneshot]", models.CatBooksComics, "Oneshot")
	c.AddCategoryMapping("printedtype[anthology]", models.CatBooksComics, "Anthology")
	c.AddCategoryMapping("printedtype[manhwa]", models.CatBooksComics, "Manhwa")
	c.AddCategoryMapping("printedtype[light_novel]", models.CatBooksComics, "Light Novel")
	c.AddCategoryMapping("printedtype[artbook]", models.CatBooksComics, "Artbook")
	return caps
}

// ApplyConfiguration ports upstream: load values, validate the passkey
// length, run a test query, mark configured and save.
func (a *AnimeBytes) ApplyConfiguration(ctx context.Context, items []indexer.ConfigItem) (indexer.ConfigStatus, error) {
	if err := a.ApplyItems(items); err != nil {
		return indexer.ConfigCompleted, err
	}
	if err := a.LoadValuesFromJSON(nil, false); err != nil {
		return indexer.ConfigCompleted, err
	}
	if l := len(a.cfg.Passkey.Value); l != 32 && l != 48 && l != 56 {
		return indexer.ConfigCompleted, fmt.Errorf("invalid passkey configured: expected length: 32, 48, or 56, got %d", l)
	}
	results, err := a.perform(ctx, models.NewTorznabQuery())
	if err != nil {
		return indexer.ConfigCompleted, err
	}
	if len(results) == 0 {
		return indexer.ConfigCompleted, errors.New("no results found, please report this bug")
	}
	a.SetConfigured(true)
	a.SaveConfig(ctx)
	return indexer.ConfigCompleted, nil
}

func (a *AnimeBytes) ResultsForQuery(ctx context.Context, q *models.TorznabQuery, isMeta bool) (*indexer.IndexerResult, error) {
	return a.ResultsForQueryWith(ctx, q, isMeta, a.perform)
}

func (a *AnimeBytes) Test(ctx context.Context) error {
	return a.Base.Test(ctx, a.perform)
}

func (a *AnimeBytes) perform(ctx context.Context, query *models.TorznabQuery) ([]*models.ReleaseInfo, error) {
	releases, err := a.getResults(ctx, query, "anime", abCleanSearchTerm(strings.TrimSpace(query.SanitizedSearchTerm())))
	if err != nil {
		return nil, err
	}
	if abContainsMusicCategories(query.Categories) {
		music, err := a.getResults(ctx, query, "music", strings.TrimSpace(query.SanitizedSearchTerm()))
		if err != nil {
			return nil, err
		}
		releases = append(releases, music...)
	}
	sort.SliceStable(releases, func(i, j int) bool { return releases[i].PublishDate.After(releases[j].PublishDate) })
	return releases, nil
}

// abCleanSearchTerm ports AnimeBytes.CleanSearchTerm: Tracer does not
// support searching with an episode number so strip it.
func abCleanSearchTerm(term string) string {
	term = abCleanEpRe1.ReplaceAllString(term, "")
	term = abCleanEpRe2.ReplaceAllString(term, "")
	term = abCleanEpRe3.ReplaceAllString(term, "")
	term = abCleanMovieRe.ReplaceAllString(strings.TrimSpace(term), "")
	return strings.TrimSpace(term)
}

// abContainsMusicCategories ports upstream (empty categories also count).
func abContainsMusicCategories(categories []int) bool {
	music := []int{
		models.CatAudio.ID, models.CatAudioMP3.ID, models.CatAudioLossless.ID,
		models.CatAudioOther.ID, models.CatAudioForeign.ID,
	}
	if len(categories) == 0 {
		return true
	}
	for _, c := range categories {
		for _, m := range music {
			if c == m {
				return true
			}
		}
	}
	return false
}

func (a *AnimeBytes) getResults(ctx context.Context, query *models.TorznabQuery, searchType, searchTerm string) ([]*models.ReleaseInfo, error) {
	params := shared.QueryParams{}
	params.Add("username", a.cfg.Username.Value)
	params.Add("torrent_pass", a.cfg.Passkey.Value)
	params.Add("sort", "grouptime")
	params.Add("way", "desc")
	params.Add("type", searchType)
	if strings.TrimSpace(searchTerm) != "" {
		params.Add("limit", "50")
	} else {
		params.Add("limit", "15")
	}
	params.Add("searchstr", searchTerm)

	if a.cfg.SearchByYear.Value && searchType == "anime" {
		if year, ok := abParseYearFromSearchTerm(strings.TrimSpace(query.SanitizedSearchTerm())); ok && year > 0 {
			params.Set("year", strconv.FormatInt(year, 10))
		}
	}

	queryCats := abDistinctStrings(a.Caps.Categories.MapTorznabCapsToTrackers(query, false))
	if len(queryCats) > 0 && query.IsTVSearch() && query.Season != nil && *query.Season > 0 {
		// Avoid searching for specials if it's a non-zero season search
		queryCats = abRemoveStrings(queryCats, "anime[tv_special]", "anime[ova]", "anime[dvd_special]", "anime[bd_special]")
	}
	for _, cat := range queryCats {
		params.Set(cat, "1")
	}
	if a.cfg.FreeleechOnly.Value {
		params.Set("freeleech", "1")
	}
	if a.cfg.ExcludeHentai.Value && searchType == "anime" {
		params.Set("hentai", "0")
	}

	searchURL := a.SiteLink() + "scrape.php?" + params.GetQueryString()

	// NOTE: upstream BaseCachingWebIndexer caches by searchUrl here; the
	// cache is a perf-only optimisation and is not ported.

	res, err := a.Client.Do(ctx, a.ID(), &httpclient.Request{
		URL:            searchURL,
		Cookies:        a.CookieHeader(),
		EmulateBrowser: httpclient.Bool(false), // upstream: EmulateBrowser = false (issue #1535)
	})
	if err != nil {
		return nil, err
	}

	if !strings.HasPrefix(string(res.Body), "{") { // not JSON => error
		if res.StatusCode == http.StatusUnauthorized || res.StatusCode == http.StatusNotFound {
			return nil, errors.New("Invalid response, please check if your credentials are valid.") //nolint:staticcheck // upstream AnimeBytes.cs error text, kept 1:1
		}
		return nil, errors.New("Unexpected response (not JSON)") //nolint:staticcheck // upstream AnimeBytes.cs error text, kept 1:1
	}

	var parsed abResponse
	if err := json.Unmarshal(res.Body, &parsed); err != nil {
		return nil, a.parseError(string(res.Body), err)
	}
	// Upstream quirk: the Error throw below happens inside the try block, so
	// the catch converts it into OnParseError ("Parse error") — replicated.
	if strings.TrimSpace(parsed.Error) != "" {
		return nil, a.parseError(string(res.Body), fmt.Errorf("Unexpected response from indexer request: %s", parsed.Error)) //nolint:staticcheck // upstream AnimeBytes.cs error text, kept 1:1
	}
	if parsed.Matches == 0 {
		return nil, nil
	}

	releases, err := a.mapGroups(parsed.Groups, query, searchType)
	if err != nil {
		return nil, a.parseError(string(res.Body), err)
	}

	sort.SliceStable(releases, func(i, j int) bool { return releases[i].PublishDate.After(releases[j].PublishDate) })

	if query.IsRssSearch() {
		cutoff := time.Now().Add(-24 * time.Hour)
		filtered := make([]*models.ReleaseInfo, 0, len(releases))
		for i, r := range releases {
			if r.PublishDate.After(cutoff) || i < 20 {
				filtered = append(filtered, r)
			}
		}
		releases = filtered
	}

	return releases, nil
}

func (a *AnimeBytes) mapGroups(groups []abGroup, query *models.TorznabQuery, searchType string) ([]*models.ReleaseInfo, error) {
	var releases []*models.ReleaseInfo
	for _, group := range groups {
		categoryName := group.CategoryName
		description := group.Description
		var year *int64
		if group.Year != nil {
			y := int64(*group.Year)
			year = &y
		}
		var poster *url.URL
		if strings.TrimSpace(group.Image) != "" {
			u, err := url.Parse(strings.TrimSpace(group.Image))
			if err != nil {
				return nil, err
			}
			poster = u
		}
		groupName := group.GroupName
		mainTitle := html.UnescapeString(group.FullName)
		if strings.TrimSpace(group.SeriesName) != "" {
			mainTitle = group.SeriesName
		}

		synonyms := newABSynonyms()
		synonyms.add(mainTitle)
		if len(group.Synonymns) > 0 {
			if a.cfg.AddJapaneseTitle.Value {
				if t, ok := group.Synonymns["Japanese"]; ok && strings.TrimSpace(t) != "" {
					synonyms.add(strings.TrimSpace(t))
				}
			}
			if a.cfg.AddRomajiTitle.Value {
				if t, ok := group.Synonymns["Romaji"]; ok && strings.TrimSpace(t) != "" {
					synonyms.add(strings.TrimSpace(t))
				}
			}
			if a.cfg.AddAlternativeTitles.Value {
				if t, ok := group.Synonymns["Alternative"]; ok && strings.TrimSpace(t) != "" {
					for _, x := range strings.Split(t, ",") {
						if s := strings.TrimSpace(x); s != "" {
							synonyms.add(s)
						}
					}
				}
			}
		}

		for _, torrent := range group.Torrents {
			// Skip non-freeleech results when freeleech only is set
			if a.cfg.FreeleechOnly.Value && torrent.RawDownMultiplier != 0 {
				continue
			}

			publishDate, err := time.ParseInLocation("2006-01-02 15:04:05", torrent.UploadTime, time.UTC)
			if err != nil {
				return nil, err
			}
			details, err := url.Parse(a.SiteLink() + "torrent/" + strconv.FormatInt(torrent.ID, 10) + "/group")
			if err != nil {
				return nil, err
			}
			link, err := url.Parse(torrent.Link)
			if err != nil {
				return nil, err
			}
			size := torrent.Size
			snatched := torrent.Snatched
			seeders := torrent.Seeders
			leechers := torrent.Leechers
			peers := seeders + leechers
			fileCount := torrent.FileCount
			rawDownMultiplier := torrent.RawDownMultiplier
			rawUpMultiplier := torrent.RawUpMultiplier

			// MST with additional 5 hours per GB
			minimumSeedTime := int64(259200) + size/(1<<30)*18000

			properties := newABSet()
			for _, p := range strings.Split(html.UnescapeString(torrent.Property), "|") {
				p = strings.TrimSpace(p)
				if p == "" {
					continue
				}
				excluded := false
				for _, ex := range abExcludedProperties {
					if strings.Contains(strings.ToLower(p), strings.ToLower(ex)) {
						excluded = true
						break
					}
				}
				if !excluded {
					properties.add(p)
				}
			}

			if properties.any(func(p string) bool { return strings.HasPrefix(p, "M2TS") }) {
				properties.add("BR-DISK")
			}

			isBluRayDisk := properties.any(func(p string) bool {
				return p == "RAW" || strings.HasPrefix(p, "M2TS") || strings.HasPrefix(p, "ISO")
			})

			if !a.cfg.IncludeRaw.Value && categoryName == "Anime" && isBluRayDisk {
				continue
			}

			hasRemuxFile := false
			for _, f := range torrent.Files {
				if strings.Contains(strings.ToLower(f.FileName), "remux") {
					hasRemuxFile = true
					break
				}
			}
			properties.mapInPlace(func(p string) string {
				if isBluRayDisk {
					p = abH265Re.ReplaceAllString(p, "HEVC")
					p = abH264Re.ReplaceAllString(p, "AVC")
				}
				if hasRemuxFile && abRemuxResolutions[strings.ToLower(p)] {
					p += " Remux"
				}
				return p
			})

			var season, episode *int64
			releaseInfo := ""
			if categoryName == "Anime" {
				releaseInfo = "S01"
			}
			if torrent.EditionData != nil && strings.TrimSpace(torrent.EditionData.EditionTitle) != "" {
				releaseInfo = html.UnescapeString(torrent.EditionData.EditionTitle)
				if m := abEditionSeasonRe.FindStringSubmatch(releaseInfo); m != nil {
					n, err := strconv.ParseInt(m[1], 10, 64)
					if err != nil {
						return nil, err
					}
					season = &n
				}
				if m := abEditionEpisodeRe.FindStringSubmatch(releaseInfo); m != nil {
					n, err := strconv.ParseInt(m[1], 10, 64)
					if err != nil {
						return nil, err
					}
					episode = &n
				}
			}

			if categoryName == "Anime" && season == nil {
				if n, ok := abParseSeasonFromTitles(synonyms.list); ok {
					season = &n
				}
			}

			if a.cfg.PadEpisode.Value && episode != nil && *episode > 0 && season == nil {
				releaseInfo = fmt.Sprintf("- %02d", *episode)
			} else if season != nil && *season > 0 {
				releaseInfo = fmt.Sprintf("S%02d", *season)
				if episode != nil && *episode > 0 {
					releaseInfo += fmt.Sprintf("E%02d - %02d", *episode, *episode)
				}
			}

			if a.cfg.FilterSeasonEpisode.Value {
				if query.Season != nil && *query.Season > 0 && season != nil && int64(*query.Season) != *season {
					continue // skip if season doesn't match
				}
				if strings.TrimSpace(query.Episode) != "" && episode != nil {
					ep, err := strconv.Atoi(query.Episode)
					if err != nil {
						return nil, err // upstream int.Parse throws → OnParseError
					}
					if int64(ep) != *episode {
						continue // skip if episode doesn't match
					}
				}
			}

			var category []int
			if searchType == "anime" {
				// Ignore these categories as they'll cause hell with the matcher
				// TV Special, DVD Special, BD Special
				switch groupName {
				case "TV Special", "DVD Special", "BD Special":
					continue
				}
				switch groupName {
				case "TV Series", "OVA", "ONA":
					category = []int{models.CatTVAnime.ID}
				case "Movie", "Live Action Movie":
					category = []int{models.CatMovies.ID}
				}
				switch categoryName {
				case "Manga", "Oneshot", "Anthology", "Manhwa", "Manhua", "Light Novel", "Novel", "Artbook":
					category = []int{models.CatBooksComics.ID}
				}
				if categoryName == "Game" || categoryName == "Visual Novel" {
					// upstream uses sequential ifs: the LAST match wins
					if properties.contains("PSP") {
						category = []int{models.CatConsolePSP.ID}
					}
					if properties.contains("PS3") {
						category = []int{models.CatConsolePS3.ID}
					}
					if properties.contains("PS Vita") {
						category = []int{models.CatConsolePSVita.ID}
					}
					if properties.contains("3DS") {
						category = []int{models.CatConsole3DS.ID}
					}
					if properties.contains("NDS") {
						category = []int{models.CatConsoleNDS.ID}
					}
					if properties.contains("PSX") || properties.contains("PS2") || properties.contains("SNES") ||
						properties.contains("NES") || properties.contains("GBA") || properties.contains("Switch") {
						category = []int{models.CatConsoleOther.ID}
					}
					if properties.contains("PC") {
						category = []int{models.CatPCGames.ID}
					}
				}
			} else if searchType == "music" {
				switch categoryName {
				case "Single", "EP", "Album", "Compilation", "Soundtrack", "Remix CD", "PV",
					"Live Album", "Image CD", "Drama CD", "Vocal CD":
					switch {
					case properties.any(func(p string) bool { return strings.Contains(p, "Lossless") }):
						category = []int{models.CatAudioLossless.ID}
					case properties.any(func(p string) bool { return strings.Contains(p, "MP3") }):
						category = []int{models.CatAudioMP3.ID}
					default:
						category = []int{models.CatAudioOther.ID}
					}
				}
			}

			// We don't actually have a release name >.> so try to create one
			releaseGroup := ""
			for i := len(properties.list) - 1; i >= 0; i-- {
				p := properties.list[i]
				if !abIsCommonReleaseGroup(p) || !strings.Contains(p, "(") || !strings.Contains(p, ")") {
					continue
				}
				start := strings.Index(p, "(")
				end := strings.Index(p, ")")
				if end < start+1 {
					// upstream Substring would throw here → OnParseError
					return nil, fmt.Errorf("release group %q has unbalanced parentheses", p)
				}
				releaseGroup = "[" + p[start+1:end] + "] "
				break
			}

			infoString := ""
			for _, p := range properties.list {
				infoString += "[" + p + "]"
			}

			useYearInTitle := false
			if year != nil && *year > 0 {
				ys := strconv.FormatInt(*year, 10)
				for _, f := range torrent.Files {
					if strings.Contains(f.FileName, ys) {
						useYearInTitle = true
						break
					}
				}
			}

			minimumRatio := 1.0
			isMovie := groupName == "Movie" || groupName == "Live Action Movie"
			for _, title := range synonyms.list {
				var releaseTitle string
				if isMovie {
					yearStr := ""
					if year != nil {
						yearStr = strconv.FormatInt(*year, 10)
					}
					releaseTitle = fmt.Sprintf("%s%s %s %s", releaseGroup, title, yearStr, infoString)
				} else {
					yearPart := ""
					if useYearInTitle && year != nil {
						yearPart = " " + strconv.FormatInt(*year, 10)
					}
					releaseTitle = fmt.Sprintf("%s%s%s %s %s", releaseGroup, title, yearPart, releaseInfo, infoString)
				}

				guid, err := url.Parse(details.String() + "&nh=" + abHashMD5(title))
				if err != nil {
					return nil, err
				}

				releases = append(releases, &models.ReleaseInfo{
					MinimumRatio:         &minimumRatio,
					MinimumSeedTime:      &minimumSeedTime,
					Title:                releaseTitle,
					Year:                 year,
					Details:              details,
					Guid:                 guid,
					Link:                 link,
					Poster:               poster,
					PublishDate:          publishDate,
					Category:             category,
					Description:          description,
					Size:                 &size,
					Seeders:              &seeders,
					Peers:                &peers,
					Grabs:                &snatched,
					Files:                &fileCount,
					DownloadVolumeFactor: &rawDownMultiplier,
					UploadVolumeFactor:   &rawUpMultiplier,
				})
			}

			if a.cfg.AddFileNameTitles.Value {
				files := torrent.Files
				if len(files) > 1 {
					filtered := make([]abFile, 0, len(files))
					for _, f := range files {
						if abExcludedFileExtensions[strings.ToLower(filepath.Ext(f.FileName))] {
							continue
						}
						filtered = append(filtered, f)
					}
					files = filtered
				}
				if len(files) != 1 {
					continue
				}

				releaseTitle := files[0].FileName
				guid, err := url.Parse(details.String() + "&nh=" + abHashMD5(releaseTitle))
				if err != nil {
					return nil, err
				}
				releases = append(releases, &models.ReleaseInfo{
					MinimumRatio:         &minimumRatio,
					MinimumSeedTime:      &minimumSeedTime,
					Title:                releaseTitle,
					Year:                 year,
					Details:              details,
					Guid:                 guid,
					Link:                 link,
					Poster:               poster,
					PublishDate:          publishDate,
					Category:             category,
					Description:          description,
					Size:                 &size,
					Seeders:              &seeders,
					Peers:                &peers,
					Grabs:                &snatched,
					Files:                &fileCount,
					DownloadVolumeFactor: &rawDownMultiplier,
					UploadVolumeFactor:   &rawUpMultiplier,
				})
			}
		}
	}
	return releases, nil
}

// log is a nil-safe logger for the manual package (Base.Logger may be nil).
func (a *AnimeBytes) log() *slog.Logger {
	if a.Logger != nil {
		return a.Logger
	}
	return slog.Default()
}

// parseError mirrors upstream OnParseError: log the response dump and
// surface Exception("Parse error", ex).
func (a *AnimeBytes) parseError(dump string, err error) error {
	a.log().Error(fmt.Sprintf("Error while parsing result: %v\n%s", err, dump))
	return &indexer.IndexerError{IndexerID: a.ID(), Kind: indexer.KindParse, Message: "Parse error"}
}

// abParseYearFromSearchTerm ports upstream ParseYearFromSearchTerm.
func abParseYearFromSearchTerm(term string) (int64, bool) {
	if strings.TrimSpace(term) == "" {
		return 0, false
	}
	m := abYearRe.FindStringSubmatch(term)
	if m == nil {
		return 0, false
	}
	n, err := strconv.ParseInt(m[1], 10, 64)
	if err != nil {
		return 0, false
	}
	return n, true
}

// abParseSeasonFromTitles ports upstream ParseSeasonFromTitles (static).
func abParseSeasonFromTitles(titles []string) (int64, bool) {
	for _, title := range titles {
		if m := abAdvSeasonRe.FindStringSubmatch(title); m != nil {
			if m[1] != "" {
				if n, err := strconv.ParseInt(m[1], 10, 64); err == nil {
					return n, true
				}
			}
			if m[2] != "" {
				if n, err := strconv.ParseInt(m[2], 10, 64); err == nil {
					return n, true
				}
			}
		}
		if m := abSeasonCharsRe.FindStringSubmatch(title); m != nil {
			return int64(len(m[1])), true
		}
		if n, ok := abSeasonFromTail(title); ok {
			return n, true
		}
	}
	return 0, false
}

// abSeasonFromTail ports the upstream seasonNumberRegex
// "\b(?<!(Part|No\.)[- ._])(?<!\d[/])(?<!\#)(?:S)?(?<season>[2-9])$".
// RE2 has no look-behind, so the assertions are checked manually.
func abSeasonFromTail(title string) (int64, bool) {
	for i := 0; i < len(title); i++ {
		m := abSeasonNumTailAnchoredRe.FindStringSubmatch(title[i:])
		if m == nil {
			continue
		}
		// \b before the optional S / digit: previous char must be non-word
		if i > 0 {
			r, _ := utf8.DecodeLastRuneInString(title[:i])
			if r == utf8.RuneError || unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' {
				continue
			}
		}
		// (?<!\#)
		if i > 0 && title[i-1] == '#' {
			continue
		}
		// (?<!\d[/])
		if i >= 2 && title[i-2] >= '0' && title[i-2] <= '9' && title[i-1] == '/' {
			continue
		}
		// (?<!(Part|No\.)[- ._])
		if i >= 5 && isABSepByte(title[i-1]) &&
			(strings.HasSuffix(title[:i-1], "Part") || strings.HasSuffix(title[:i-1], "No.")) {
			continue
		}
		n, err := strconv.ParseInt(m[1], 10, 64)
		if err != nil {
			continue
		}
		return n, true
	}
	return 0, false
}

func isABSepByte(b byte) bool {
	return b == '-' || b == ' ' || b == '.' || b == '_'
}

// abIsCommonReleaseGroup ports the upstream release-group predicate:
// starts with any of Softsubs/Hardsubs/RAW/Translated (case-insensitive)
// and contains parentheses.
func abIsCommonReleaseGroup(p string) bool {
	for _, g := range abCommonReleaseGroups {
		if len(p) >= len(g) && strings.EqualFold(p[:len(g)], g) {
			return true
		}
	}
	return false
}

// abHashMD5 ports StringUtil.Hash: MD5 over ASCII bytes (non-ASCII → '?').
func abHashMD5(s string) string {
	b := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c > 127 {
			c = '?'
		}
		b = append(b, c)
	}
	sum := md5.Sum(b)
	return hex.EncodeToString(sum[:])
}

// abDistinctStrings mirrors upstream .Distinct().
func abDistinctStrings(list []string) []string {
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

// abRemoveStrings mirrors upstream List.RemoveAll(cat is ...).
func abRemoveStrings(list []string, drop ...string) []string {
	out := make([]string, 0, len(list))
	for _, s := range list {
		found := false
		for _, d := range drop {
			if s == d {
				found = true
				break
			}
		}
		if !found {
			out = append(out, s)
		}
	}
	return out
}

// --- small ordered-set helpers ----------------------------------------------

// abSet preserves insertion order (upstream HashSet enumeration order after
// ToHashSet of a de-duplicated list; no removals happen after construction).
type abSet struct {
	list []string
	idx  map[string]bool
}

func newABSet() *abSet { return &abSet{idx: map[string]bool{}} }

func (s *abSet) add(v string) {
	if s.idx[v] {
		return
	}
	s.idx[v] = true
	s.list = append(s.list, v)
}

func (s *abSet) contains(v string) bool { return s.idx[v] }

func (s *abSet) any(f func(string) bool) bool {
	for _, v := range s.list {
		if f(v) {
			return true
		}
	}
	return false
}

func (s *abSet) mapInPlace(f func(string) string) {
	for i, v := range s.list {
		nv := f(v)
		if nv != v {
			delete(s.idx, v)
			s.idx[nv] = true
		}
		s.list[i] = nv
	}
}

// abSynonyms is the upstream case-insensitive (OrdinalIgnoreCase) HashSet.
type abSynonyms struct {
	list []string
	seen map[string]bool
}

func newABSynonyms() *abSynonyms { return &abSynonyms{seen: map[string]bool{}} }

func (s *abSynonyms) add(v string) {
	k := strings.ToLower(v)
	if s.seen[k] {
		return
	}
	s.seen[k] = true
	s.list = append(s.list, v)
}

// --- upstream regexes / tables ----------------------------------------------

var (
	abCleanEpRe1 = regexp.MustCompile(`\W(\dx)?\d?\d$`)
	abCleanEpRe2 = regexp.MustCompile(`\W(S\d\d?E)?\d?\d$`)
	abCleanEpRe3 = regexp.MustCompile(`\W\d+$`)
	// upstream: RegexOptions.IgnoreCase
	abCleanMovieRe = regexp.MustCompile(`(?i)\bThe Movie$`)
	abYearRe       = regexp.MustCompile(`\b((?:19|20)\d{2})$`)

	// edition-title regexes are case-SENSITIVE upstream (no options).
	abEditionSeasonRe  = regexp.MustCompile(`\bSeason (\d+)\b`)
	abEditionEpisodeRe = regexp.MustCompile(`\bEpisode (\d+)\b`)

	// ParseSeasonFromTitles regexes.
	abAdvSeasonRe   = regexp.MustCompile(`(?i)\b(?:(\d+)(?:st|nd|rd|th) Season|Season (\d+))\b`)
	abSeasonCharsRe = regexp.MustCompile(`(I{2,})$`)
	// tail fragment of seasonNumberRegex (look-behinds checked manually).
	abSeasonNumTailAnchoredRe = regexp.MustCompile(`^S?([2-9])$`)

	abH265Re = regexp.MustCompile(`(?i)\b(H\.?265)\b`)
	abH264Re = regexp.MustCompile(`(?i)\b(H\.?264)\b`)
)

var abExcludedProperties = []string{"Freeleech"}

var abRemuxResolutions = map[string]bool{
	"1080i": true, "1080p": true, "2160p": true, "4k": true,
}

var abCommonReleaseGroups = []string{"Softsubs", "Hardsubs", "RAW", "Translated"}

var abExcludedFileExtensions = map[string]bool{
	".mka": true, ".mds": true, ".md5": true, ".nfo": true, ".sfv": true,
	".ass": true, ".mks": true, ".srt": true, ".ssa": true, ".sup": true,
	".jpeg": true, ".jpg": true, ".png": true, ".otf": true, ".ttf": true,
}

// --- upstream DTOs -----------------------------------------------------------

type abResponse struct {
	Matches int64     `json:"Matches"`
	Groups  []abGroup `json:"Groups"`
	Error   string    `json:"Error"`
}

type abGroup struct {
	ID           int64             `json:"ID"`
	CategoryName string            `json:"CategoryName"`
	FullName     string            `json:"FullName"`
	GroupName    string            `json:"GroupName"`
	SeriesName   string            `json:"SeriesName"`
	Year         *abFlexInt        `json:"Year"`
	Image        string            `json:"Image"`
	Synonymns    map[string]string `json:"SynonymnsV2"`
	Description  string            `json:"Description"`
	Tags         []string          `json:"Tags"`
	Torrents     []abTorrent       `json:"Torrents"`
}

type abTorrent struct {
	ID                int64      `json:"ID"`
	EditionData       *abEdition `json:"EditionData"`
	RawDownMultiplier float64    `json:"RawDownMultiplier"`
	RawUpMultiplier   float64    `json:"RawUpMultiplier"`
	Link              string     `json:"Link"`
	Property          string     `json:"Property"`
	Snatched          int64      `json:"Snatched"`
	Seeders           int64      `json:"Seeders"`
	Leechers          int64      `json:"Leechers"`
	Size              int64      `json:"Size"`
	FileCount         int64      `json:"FileCount"`
	Files             []abFile   `json:"FileList"`
	UploadTime        string     `json:"UploadTime"`
}

type abEdition struct {
	EditionTitle string `json:"EditionTitle"`
}

type abFile struct {
	FileName string `json:"filename"`
	FileSize int64  `json:"size"`
}

// abFlexInt mirrors upstream JsonNumberHandling.AllowReadingFromString.
type abFlexInt int64

func (v *abFlexInt) UnmarshalJSON(data []byte) error {
	s := strings.TrimSpace(string(data))
	if s == "" || s == "null" {
		return nil
	}
	if strings.HasPrefix(s, "\"") {
		var str string
		if err := json.Unmarshal(data, &str); err != nil {
			return err
		}
		s = strings.TrimSpace(str)
		if s == "" {
			return nil
		}
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return err
	}
	*v = abFlexInt(n)
	return nil
}
