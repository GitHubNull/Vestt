package manual

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"vestt/common/httpclient"
	"vestt/common/indexer"
	"vestt/common/indexer/configdata"
	"vestt/common/indexer/shared"
	"vestt/common/models"
	"vestt/common/services"
	"vestt/common/utils/parseutil"
)

// GazelleGamesAPI ports Jackett.Common/Indexers/Definitions/GazelleGamesAPI.cs.
//
// mechanism: gazelle-api (GGn api.php?request=search JSON API; PerformQuery
// is fully overridden upstream — group-keyed response, artistcheck category
// params, freeleech token/passkey/authkey download URL)
//
// Ported from: Definitions/GazelleGamesAPI.cs.
//
// 澄清记录:
//   - upstream self-test (GazelleTracker.ApplyConfiguration) runs the
//     virtual PerformQuery, i.e. api.php?request=search; the Go port's
//     embedded shared.Gazelle.ApplyConfiguration runs the shared browse
//     (ajax.php?action=browse) instead — same deviation pattern as
//     AlphaRatio (SPEC-08), not observable in the self-test's checks.
//   - upstream iterates the response/torrent JObjects in document order;
//     Go maps are unordered, so both are iterated in ascending numeric id
//     order (deterministic; the final order is PublishDate desc either way).
//   - upstream NREs when a group has no "Artists" array; the port treats a
//     missing/null Artists as empty (categories then fall back to
//     CategoryID per torrent, which is the only upstream-observable
//     difference on malformed responses).
type GazelleGamesAPI struct {
	shared.Gazelle
}

const ggnInstructionMessage = "<ol><li>Go to GGn's site and open your account settings.</li><li>Under <b>Access Settings</b> click on 'Create a new token'</li><li>Give it a name you like and click <b>Generate</b>.</li><li>Copy the generated API Key and paste it in the above text field.</li></ol>"

// NewGazelleGamesAPI builds the indexer (services.Factory shape).
func NewGazelleGamesAPI(deps services.Deps) (indexer.Indexer, error) {
	g := &GazelleGamesAPI{}
	g.Base = indexer.Base{
		IDValue:          "gazellegamesapi",
		NameValue:        "GazelleGames (API)",
		DescriptionValue: "GazelleGames is a Private gaming tracker",
		SiteLinkValue:    "https://gazellegames.net/",
		DefaultSiteLink:  "https://gazellegames.net/",
		LangValue:        "en-US",
		TypeValue:        "private",
		Caps:             ggnCaps(),
		Client:           deps.Client,
		Saver:            deps.Saver,
		Cache:            deps.Cache,
	}
	cfg := shared.NewGazelleConfig(shared.GazelleOptions{
		SupportsFreeleechTokens: true,
		SupportsFreeleechOnly:   true,
		UseAPIKey:               true,
		UsePassKey:              true,
		UseAuthKey:              true,
		InstructionMessage:      ggnInstructionMessage,
	})
	// upstream constructor: configData.AddDynamic("searchgroupnames", ...)
	cfg.AddDynamic("searchgroupnames", configdata.NewBoolItem("Search Group Names Only"))
	g.Bind(cfg)
	g.UseAPIKey = true
	g.UsePassKey = true
	g.UseAuthKey = true
	g.APIKeyLength = 64
	g.AuthorizationName = "X-API-Key"
	g.FlipOptionalTokenString = func(requestLink string) string {
		return strings.ReplaceAll(requestLink, "&usetoken=1", "")
	}
	// Upstream inherits the base GetInfoUrl/GetDownloadUrl; the hooks are set
	// explicitly so this indexer's PerformQuery and the shared Download path
	// use the same construction.
	g.GetInfoURL = func(torrentID int, groupID *int) string {
		s := fmt.Sprintf("%storrents.php?torrentid=%d", g.SiteLink(), torrentID)
		if groupID != nil && *groupID > 0 {
			s += fmt.Sprintf("&id=%d", *groupID)
		}
		return s
	}
	g.GetDownloadURL = func(torrentID int, canUseToken bool) string {
		var b strings.Builder
		b.WriteString(g.SiteLink() + "torrents.php?action=download")
		if g.Config.UseToken != nil && g.Config.UseToken.Value && canUseToken {
			b.WriteString("&usetoken=1")
		}
		if g.Config.Passkey != nil {
			b.WriteString("&torrent_pass=" + g.Config.Passkey.Value)
		}
		if g.Config.Authkey != nil {
			b.WriteString("&authkey=" + g.Config.Authkey.Value)
		}
		fmt.Fprintf(&b, "&id=%d", torrentID)
		return b.String()
	}
	return g, nil
}

func ggnCaps() *models.Capabilities {
	caps := models.NewCapabilities()
	c := caps.Categories

	// Apple
	c.AddCategoryMapping("Mac", models.CatConsoleOther, "Mac")
	c.AddCategoryMapping("iOS", models.CatPCMobileiOS, "iOS")
	c.AddCategoryMapping("Apple Bandai Pippin", models.CatConsoleOther, "Apple Bandai Pippin")
	c.AddCategoryMapping("Apple II", models.CatConsoleOther, "Apple II")

	// Google
	c.AddCategoryMapping("Android", models.CatPCMobileAndroid, "Android")

	// Microsoft
	c.AddCategoryMapping("DOS", models.CatPCGames, "DOS")
	c.AddCategoryMapping("Windows", models.CatPCGames, "Windows")
	c.AddCategoryMapping("Xbox", models.CatConsoleXBox, "Xbox")
	c.AddCategoryMapping("Xbox 360", models.CatConsoleXBox360, "Xbox 360")

	// Nintendo
	c.AddCategoryMapping("Game Boy", models.CatConsoleOther, "Game Boy")
	c.AddCategoryMapping("Game Boy Advance", models.CatConsoleOther, "Game Boy Advance")
	c.AddCategoryMapping("Game Boy Color", models.CatConsoleOther, "Game Boy Color")
	c.AddCategoryMapping("NES", models.CatConsoleOther, "NES")
	c.AddCategoryMapping("Nintendo 64", models.CatConsoleOther, "Nintendo 64")
	c.AddCategoryMapping("Nintendo 3DS", models.CatConsoleOther, "Nintendo 3DS")
	c.AddCategoryMapping("New Nintendo 3DS", models.CatConsoleOther, "New Nintendo 3DS")
	c.AddCategoryMapping("Nintendo DS", models.CatConsoleNDS, "Nintendo DS")
	c.AddCategoryMapping("Nintendo GameCube", models.CatConsoleOther, "Nintendo GameCube")
	c.AddCategoryMapping("Pokemon Mini", models.CatConsoleOther, "Pokemon Mini")
	c.AddCategoryMapping("SNES", models.CatConsoleOther, "SNES")
	c.AddCategoryMapping("Switch", models.CatConsoleOther, "Switch")
	c.AddCategoryMapping("Virtual Boy", models.CatConsoleOther, "Virtual Boy")
	c.AddCategoryMapping("Wii", models.CatConsoleWii, "Wii")
	c.AddCategoryMapping("Wii U", models.CatConsoleWiiU, "Wii U")

	// Sony
	c.AddCategoryMapping("PlayStation 1", models.CatConsoleOther, "PlayStation 1")
	c.AddCategoryMapping("PlayStation 2", models.CatConsoleOther, "PlayStation 2")
	c.AddCategoryMapping("PlayStation 3", models.CatConsolePS3, "PlayStation 3")
	c.AddCategoryMapping("PlayStation 4", models.CatConsolePS4, "PlayStation 4")
	c.AddCategoryMapping("PlayStation Portable", models.CatConsolePSP, "PlayStation Portable")
	c.AddCategoryMapping("PlayStation Vita", models.CatConsolePSVita, "PlayStation Vita")

	// Sega
	c.AddCategoryMapping("Dreamcast", models.CatConsoleOther, "Dreamcast")
	c.AddCategoryMapping("Game Gear", models.CatConsoleOther, "Game Gear")
	c.AddCategoryMapping("Master System", models.CatConsoleOther, "Master System")
	c.AddCategoryMapping("Mega Drive", models.CatConsoleOther, "Mega Drive")
	c.AddCategoryMapping("Pico", models.CatConsoleOther, "Pico")
	c.AddCategoryMapping("Saturn", models.CatConsoleOther, "Saturn")
	c.AddCategoryMapping("SG-1000", models.CatConsoleOther, "SG-1000")

	// Atari
	c.AddCategoryMapping("Atari 2600", models.CatConsoleOther, "Atari 2600")
	c.AddCategoryMapping("Atari 5200", models.CatConsoleOther, "Atari 5200")
	c.AddCategoryMapping("Atari 7800", models.CatConsoleOther, "Atari 7800")
	c.AddCategoryMapping("Atari Jaguar", models.CatConsoleOther, "Atari Jaguar")
	c.AddCategoryMapping("Atari Lynx", models.CatConsoleOther, "Atari Lynx")
	c.AddCategoryMapping("Atari ST", models.CatConsoleOther, "Atari ST")

	// Amstrad
	c.AddCategoryMapping("Amstrad CPC", models.CatConsoleOther, "Amstrad CPC")

	// Bandai
	c.AddCategoryMapping("Bandai WonderSwan", models.CatConsoleOther, "Bandai WonderSwan")
	c.AddCategoryMapping("Bandai WonderSwan Color", models.CatConsoleOther, "Bandai WonderSwan Color")
	// c.AddCategoryMapping("Apple Bandai Pippin", models.CatConsoleOther, "Apple Bandai Pippin")  // repeated

	// Commodore
	c.AddCategoryMapping("Commodore 64", models.CatConsoleOther, "Commodore 64")
	c.AddCategoryMapping("Commodore 128", models.CatConsoleOther, "Commodore 128")
	c.AddCategoryMapping("Commodore Amiga", models.CatConsoleOther, "Commodore Amiga")
	c.AddCategoryMapping("Amiga CD32", models.CatConsoleOther, "Amiga CD32")
	c.AddCategoryMapping("Commodore Plus-4", models.CatConsoleOther, "Commodore Plus-4")
	c.AddCategoryMapping("Commodore VIC-20", models.CatConsoleOther, "Commodore VIC-20")

	// NEC
	c.AddCategoryMapping("NEC PC-98", models.CatConsoleOther, "NEC PC-98")
	c.AddCategoryMapping("NEC PC-FX", models.CatConsoleOther, "NEC PC-FX")
	c.AddCategoryMapping("NEC SuperGrafx", models.CatConsoleOther, "NEC SuperGrafx")
	c.AddCategoryMapping("NEC TurboGrafx-16", models.CatConsoleOther, "NEC TurboGrafx-16")

	// Sinclair
	c.AddCategoryMapping("ZX Spectrum", models.CatConsoleOther, "ZX Spectrum")

	// Spectravideo
	c.AddCategoryMapping("MSX", models.CatConsoleOther, "MSX")
	c.AddCategoryMapping("MSX 2", models.CatConsoleOther, "MSX 2")

	// Tiger
	c.AddCategoryMapping("Game.com", models.CatConsoleOther, "Game.com")
	c.AddCategoryMapping("Gizmondo", models.CatConsoleOther, "Gizmondo")

	// VTech
	c.AddCategoryMapping("V.Smile", models.CatConsoleOther, "V.Smile")
	c.AddCategoryMapping("CreatiVision", models.CatConsoleOther, "CreatiVision")

	// Tabletop Games
	c.AddCategoryMapping("Board Game", models.CatConsoleOther, "Board Game")
	c.AddCategoryMapping("Card Game", models.CatConsoleOther, "Card Game")
	c.AddCategoryMapping("Miniature Wargames", models.CatConsoleOther, "Miniature Wargames")
	c.AddCategoryMapping("Pen and Paper RPG", models.CatConsoleOther, "Pen and Paper RPG")

	// Other
	c.AddCategoryMapping("3DO", models.CatConsoleOther, "3DO")
	c.AddCategoryMapping("Casio Loopy", models.CatConsoleOther, "Casio Loopy")
	c.AddCategoryMapping("Casio PV-1000", models.CatConsoleOther, "Casio PV-1000")
	c.AddCategoryMapping("Colecovision", models.CatConsoleOther, "Colecovision")
	c.AddCategoryMapping("Emerson Arcadia 2001", models.CatConsoleOther, "Emerson Arcadia 2001")
	c.AddCategoryMapping("Entex Adventure Vision", models.CatConsoleOther, "Entex Adventure Vision")
	c.AddCategoryMapping("Epoch Super Casette Vision", models.CatConsoleOther, "Epoch Super Casette Vision")
	c.AddCategoryMapping("Fairchild Channel F", models.CatConsoleOther, "Fairchild Channel F")
	c.AddCategoryMapping("Funtech Super Acan", models.CatConsoleOther, "Funtech Super Acan")
	c.AddCategoryMapping("GamePark GP32", models.CatConsoleOther, "GamePark GP32")
	c.AddCategoryMapping("General Computer Vectrex", models.CatConsoleOther, "General Computer Vectrex")
	c.AddCategoryMapping("Interactive DVD", models.CatConsoleOther, "Interactive DVD")
	c.AddCategoryMapping("Linux", models.CatConsoleOther, "Linux")
	c.AddCategoryMapping("Hartung Game Master", models.CatConsoleOther, "Hartung Game Master")
	c.AddCategoryMapping("Magnavox-Phillips Odyssey", models.CatConsoleOther, "Magnavox-Phillips Odyssey")
	c.AddCategoryMapping("Mattel Intellivision", models.CatConsoleOther, "Mattel Intellivision")
	c.AddCategoryMapping("Memotech MTX", models.CatConsoleOther, "Memotech MTX")
	c.AddCategoryMapping("Miles Gordon Sam Coupe", models.CatConsoleOther, "Miles Gordon Sam Coupe")
	c.AddCategoryMapping("Nokia N-Gage", models.CatConsoleOther, "Nokia N-Gage")
	c.AddCategoryMapping("Oculus Quest", models.CatConsoleOther, "Oculus Quest")
	c.AddCategoryMapping("Ouya", models.CatConsoleOther, "Ouya")
	c.AddCategoryMapping("Philips Videopac+", models.CatConsoleOther, "Philips Videopac+")
	c.AddCategoryMapping("Philips CD-i", models.CatConsoleOther, "Philips CD-i")
	c.AddCategoryMapping("Phone/PDA", models.CatConsoleOther, "Phone/PDA")
	c.AddCategoryMapping("RCA Studio II", models.CatConsoleOther, "RCA Studio II")
	c.AddCategoryMapping("Sharp X1", models.CatConsoleOther, "Sharp X1")
	c.AddCategoryMapping("Sharp X68000", models.CatConsoleOther, "Sharp X68000")
	c.AddCategoryMapping("SNK Neo Geo", models.CatConsoleOther, "SNK Neo Geo")
	c.AddCategoryMapping("SNK Neo Geo Pocket", models.CatConsoleOther, "SNK Neo Geo Pocket")
	c.AddCategoryMapping("Taito Type X", models.CatConsoleOther, "Taito Type X")
	c.AddCategoryMapping("Tandy Color Computer", models.CatConsoleOther, "Tandy Color Computer")
	c.AddCategoryMapping("Tangerine Oric", models.CatConsoleOther, "Tangerine Oric")
	c.AddCategoryMapping("Thomson MO5", models.CatConsoleOther, "Thomson MO5")
	c.AddCategoryMapping("Watara Supervision", models.CatConsoleOther, "Watara Supervision")
	c.AddCategoryMapping("Retro - Other", models.CatConsoleOther, "Retro - Other")

	// special categories (real categories/not platforms)
	c.AddCategoryMapping("1", models.CatPCGames, "Games")
	c.AddCategoryMapping("2", models.CatPC0day, "Applications")
	c.AddCategoryMapping("3", models.CatBooksEBook, "E-Books")
	c.AddCategoryMapping("4", models.CatAudioOther, "OST")

	return caps
}

// ResultsForQuery implements indexer.Indexer.
func (g *GazelleGamesAPI) ResultsForQuery(ctx context.Context, q *models.TorznabQuery, isMeta bool) (*indexer.IndexerResult, error) {
	return g.ResultsForQueryWith(ctx, q, isMeta, g.performQuery)
}

// Test implements indexer.Indexer.
func (g *GazelleGamesAPI) Test(ctx context.Context) error {
	return g.Base.Test(ctx, g.performQuery)
}

var ggnYearRegex = regexp.MustCompile(`\b(?:19|20|21)\d{2}\b`)

// searchURL mirrors upstream GazelleGamesAPI.PerformQuery request
// construction (note: unlike the base browse, the "." removal happens on
// the search string BEFORE encoding).
func (g *GazelleGamesAPI) searchURL(query *models.TorznabQuery) string {
	searchString := query.GetQueryString()

	var qc shared.QueryParams
	qc.Add("request", "search")
	qc.Add("search_type", "torrents")
	// group_results=0 omitted upstream: results won't include all information
	qc.Add("order_by", "time")
	qc.Add("order_way", "desc")

	if strings.TrimSpace(searchString) != "" {
		searchGroupNames := false
		if item, ok := g.Config.GetDynamic("searchgroupnames").(*configdata.BoolItem); ok {
			searchGroupNames = item.Value
		}
		if searchGroupNames {
			qc.Add("groupname", strings.ReplaceAll(searchString, ".", " "))
		} else {
			qc.Add("searchstr", strings.ReplaceAll(searchString, ".", " "))
		}
	}

	categoryMappings := ggnDistinctStrings(g.Caps.Categories.MapTorznabCapsToTrackers(query, false))
	i := 0
	for _, cat := range categoryMappings {
		if isAllDigitsString(cat) {
			continue
		}
		qc.Add(fmt.Sprintf("artistcheck[%d]", i), cat)
		i++
	}

	if g.Config.FreeleechOnly != nil && g.Config.FreeleechOnly.Value {
		qc.Set("freetorrent", "1")
	}

	return g.SiteLink() + "api.php?" + qc.GetQueryString()
}

// performQuery ports upstream GazelleGamesAPI.PerformQuery (API request,
// failure error surfacing and the group-keyed response parsing).
func (g *GazelleGamesAPI) performQuery(ctx context.Context, query *models.TorznabQuery) ([]*models.ReleaseInfo, error) {
	if g.Client == nil {
		return nil, errors.New("http client not configured")
	}
	searchURL := g.searchURL(query)

	// upstream: headers when the ApiKey item exists (value possibly empty)
	var headers map[string]string
	if g.Config.APIKey != nil {
		headers = map[string]string{"X-API-Key": g.Config.APIKey.Value}
	}

	res, err := g.Client.Do(ctx, g.ID(), &httpclient.Request{
		URL:     searchURL,
		Headers: headers,
		Cookies: g.CookieHeader(),
	})
	if err != nil {
		return nil, err
	}
	body := res.Body

	// we get a redirect in html pages and an error message in json response (api)
	// (useApiKey is always true for this indexer, so the upstream re-login
	// branch never triggers)

	if strings.Contains(string(body), "failure") && g.UseAPIKey {
		// reason for failure should be explained.
		if jsonError, err := ggnDecodeObject(body); err == nil {
			if reason, ok := jsonError["error"]; ok && strings.TrimSpace(ggnStr(reason)) != "" {
				return nil, fmt.Errorf("Unexpected response from indexer request: %q", ggnStr(reason)) //nolint:staticcheck // upstream GazelleTracker.cs error text, kept 1:1
			}
		}
	}

	if res.StatusCode >= 400 {
		return nil, fmt.Errorf("Invalid status code %d (%s) received from indexer", res.StatusCode, shared.StatusName(res.StatusCode)) //nolint:staticcheck // upstream GazelleTracker.cs error text, kept 1:1
	}

	releases, err := g.parseSearch(body)
	if err != nil {
		return nil, g.parseFailure(string(body), err)
	}

	// order by date
	sort.SliceStable(releases, func(i, j int) bool {
		return releases[i].PublishDate.After(releases[j].PublishDate)
	})
	return releases, nil
}

// parseSearch ports the upstream JObject.Parse loop over the group-keyed
// api.php response.
func (g *GazelleGamesAPI) parseSearch(body []byte) ([]*models.ReleaseInfo, error) {
	jsonResponse, err := ggnDecodeObject(body)
	if err != nil {
		return nil, err
	}
	results, ok := jsonResponse["response"].(map[string]any)
	if !ok {
		return nil, nil
	}

	var releases []*models.ReleaseInfo
	for _, groupIDKey := range ggnSortedKeys(results) {
		groupID, err := strconv.Atoi(groupIDKey)
		if err != nil {
			return nil, err
		}
		group, ok := results[groupIDKey].(map[string]any)
		if !ok {
			continue
		}
		groupTorrents, ok := group["Torrents"].(map[string]any)
		if !ok {
			continue
		}

		artists := ggnArtists(group)

		var categories []int
		seenCat := map[int]bool{}
		for _, name := range artists {
			for _, c := range g.Caps.Categories.MapTrackerCatDescToNewznab(name) {
				if !seenCat[c] {
					seenCat[c] = true
					categories = append(categories, c)
				}
			}
		}

		for _, torrentIDKey := range ggnSortedKeys(groupTorrents) {
			torrent, ok := groupTorrents[torrentIDKey].(map[string]any)
			if !ok {
				continue
			}

			var freeTorrent int64
			if ft := strings.TrimSpace(ggnStr(torrent["FreeTorrent"])); ft != "" {
				if v, err := strconv.ParseInt(ft, 10, 64); err == nil {
					freeTorrent = v
				}
			}

			downloadVolumeFactor := 1.0
			if freeTorrent >= 1 {
				downloadVolumeFactor = 0
			}

			// Skip non-freeleech results when freeleech only is set
			if g.Config.FreeleechOnly != nil && g.Config.FreeleechOnly.Value && downloadVolumeFactor != 0.0 {
				continue
			}

			torrentID, ok := ggnInt(torrent["ID"])
			if !ok {
				return nil, fmt.Errorf("missing torrent ID")
			}

			if len(categories) == 0 {
				categories = g.Caps.Categories.MapTrackerCatToNewznab(ggnStr(torrent["CategoryID"]))
			}

			details := g.GetInfoURL(int(torrentID), &groupID)
			detailsURL, err := url.Parse(details)
			if err != nil {
				return nil, err
			}
			link := g.GetDownloadURL(int(torrentID), false)
			linkURL, err := url.Parse(link)
			if err != nil {
				return nil, err
			}

			title := html.UnescapeString(ggnStr(torrent["ReleaseTitle"]))
			groupYear, groupYearOK := ggnInt(group["year"])
			if groupYearOK && groupYear > 0 && strings.TrimSpace(title) != "" && !ggnYearRegex.MatchString(title) {
				title += fmt.Sprintf(" (%d)", groupYear)
			}

			if remasterTitle := ggnStr(torrent["RemasterTitle"]); strings.TrimSpace(remasterTitle) != "" {
				remasterYear, _ := ggnInt(torrent["RemasterYear"])
				title += fmt.Sprintf(" [%s]", strings.TrimSpace(html.UnescapeString(remasterTitle)+" "+strconv.FormatInt(remasterYear, 10)))
			}

			var tags []string
			if len(artists) > 0 {
				tags = append(tags, strings.Join(artists, ", "))
			}
			for _, tag := range []string{"Format", "Encoding", "Region", "Language", "Scene", "Miscellaneous", "GameDOXType", "GameDOXVers"} {
				var tagValue string
				if tag == "Scene" {
					if ggnStr(torrent[tag]) == "1" {
						tagValue = "Scene"
					}
				} else {
					tagValue = ggnStr(torrent[tag])
				}
				if tagValue != "" {
					tags = append(tags, tagValue)
				}
			}
			if dupable, _ := ggnInt(torrent["Dupable"]); dupable == 1 {
				tags = append(tags, "Trumpable")
			}
			if len(tags) > 0 {
				title += fmt.Sprintf(" [%s]", strings.Join(tags, ", "))
			}

			publishDate, err := time.Parse("2006-01-02 15:04:05", ggnStr(torrent["Time"]))
			if err != nil {
				return nil, err
			}

			size := parseutil.MustCoerceLong(ggnStr(torrent["Size"]))
			grabs, _ := ggnInt(torrent["Snatched"])
			seeders, _ := ggnInt(torrent["Seeders"])
			leechers, _ := ggnInt(torrent["Leechers"])
			peers := seeders + leechers
			files, _ := ggnInt(torrent["FileCount"])

			uploadVolumeFactor := 1.0
			if freeTorrent >= 2 {
				uploadVolumeFactor = 0
			}
			minimumRatio := 1.0
			minimumSeedTime := int64(288000) // 80 hours

			releases = append(releases, &models.ReleaseInfo{
				Guid:                 linkURL,
				Details:              detailsURL,
				Link:                 linkURL,
				Title:                title,
				Category:             categories,
				PublishDate:          publishDate,
				Size:                 &size,
				Grabs:                &grabs,
				Seeders:              &seeders,
				Peers:                &peers,
				Files:                &files,
				DownloadVolumeFactor: &downloadVolumeFactor,
				UploadVolumeFactor:   &uploadVolumeFactor,
				MinimumRatio:         &minimumRatio,
				MinimumSeedTime:      &minimumSeedTime,
			})
		}
	}
	return releases, nil
}

func (g *GazelleGamesAPI) parseFailure(dump string, err error) error {
	if g.Logger != nil {
		g.Logger.Error(fmt.Sprintf("Error while parsing result: %v\n%s", err, dump))
	}
	return &indexer.IndexerError{IndexerID: g.ID(), Kind: indexer.KindParse, Message: "Parse error"}
}

// ggnArtists mirrors upstream group.Value<JArray>("Artists") (nil-safe).
func ggnArtists(group map[string]any) []string {
	rawArtists, ok := group["Artists"].([]any)
	if !ok {
		return nil
	}
	var names []string
	for _, rawArtist := range rawArtists {
		artist, ok := rawArtist.(map[string]any)
		if !ok {
			continue
		}
		names = append(names, ggnStr(artist["name"]))
	}
	return names
}

// ggnSortedKeys returns the map keys in ascending numeric order (falling
// back to lexicographic order for non-numeric keys).
func ggnSortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		a, aErr := strconv.Atoi(keys[i])
		b, bErr := strconv.Atoi(keys[j])
		if aErr != nil || bErr != nil {
			return keys[i] < keys[j]
		}
		return a < b
	})
	return keys
}

// ggnDecodeObject decodes one JSON object loosely (numbers as json.Number),
// mirroring the unexported shared helper.
func ggnDecodeObject(data []byte) (map[string]any, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	var m map[string]any
	if err := dec.Decode(&m); err != nil {
		return nil, err
	}
	return m, nil
}

// ggnStr mirrors (string)JToken for JValue scalars (shared.jStr).
func ggnStr(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return t
	case json.Number:
		return t.String()
	case bool:
		if t {
			return "True"
		}
		return "False"
	default:
		return fmt.Sprintf("%v", t)
	}
}

// ggnInt mirrors the upstream numeric casts (shared.jInt).
func ggnInt(v any) (int64, bool) {
	switch t := v.(type) {
	case json.Number:
		if n, err := t.Int64(); err == nil {
			return n, true
		}
		if f, err := t.Float64(); err == nil {
			return int64(f), true
		}
	case float64:
		return int64(t), true
	case string:
		if n, err := strconv.ParseInt(strings.TrimSpace(t), 10, 64); err == nil {
			return n, true
		}
	}
	return 0, false
}

func ggnDistinctStrings(list []string) []string {
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

func isAllDigitsString(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}
