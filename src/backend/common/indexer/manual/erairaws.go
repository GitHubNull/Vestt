// Package manual holds the hand-written indexers (one file per site).
package manual

import (
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"log/slog"
	"net/mail"
	"net/url"
	"regexp"
	"strings"
	"time"

	"vestt/common/httpclient"
	"vestt/common/indexer"
	"vestt/common/indexer/configdata"
	"vestt/common/models"
	"vestt/common/services"
	"vestt/common/utils/parseutil"
)

// EraiRaws ports Jackett.Common/Indexers/Definitions/EraiRaws.cs.
//
// mechanism: rss-feed (token-authenticated RSS, client-side filtering)
//
// Ported from: Definitions/EraiRaws.cs.
//
// 澄清记录:
//   - Upstream registers every site-specific item via AddDynamic (including
//     four DisplayInfo items whose upstream Name is ""); configdata.Collect
//     dedups declared fields by item ID, which would silently drop three of
//     those help items. AddDynamic (insertion-ordered, no dedup) reproduces
//     the upstream configuration form exactly, so it is used here in
//     deliberate deviation from the usual declared-field pattern.
//   - The upstream torrent branch of ConvertEraiRawsInfoToJackettInfo builds
//     the guid from DetailsLink and would NRE when the description carries
//     no href; the Go port guards the nil details link instead.
type EraiRaws struct {
	indexer.Base
	cfg *eraiRawsConfig
}

type eraiRawsConfig struct {
	configdata.ConfigData
}

const (
	eraiRawsRSSFeed      = "feed/?"
	eraiRawsDefaultSize  = "512MB"
	eraiRaws403Marker    = "<status>403</status>"
	eraiRaws403Error     = "The RSSkey may need to be replaced as EraiRaws returned 403 Forbidden."
	eraiRawsNoResultsErr = "Could not find releases from this URL"
)

func newEraiRawsConfig() *eraiRawsConfig {
	cfg := &eraiRawsConfig{ConfigData: *configdata.NewConfigData()}

	cfg.AddDynamic("rssKey", configdata.NewStringItem("RSSKey"))

	cfg.AddDynamic("rssKeyHelp", configdata.NewInfoItem("",
		"Find the RSS Key by accessing <a href=\"https://www.erai-raws.info/rss-page/\" target =_blank>Erai-Raws RSS page</a> while you're logged in. Copy the <i>All RSS</i> URL, the RSS Key is the last part. Example: for the URL <b>.../feed/?type=torrent&0879fd62733b8db8535eb1be2333</b> the RSS Key is <b>0879fd62733b8db8535eb1be2333</b>"))

	cfg.AddDynamic("rssCategories", configdata.NewSelectItem("Select a Category", []configdata.Option{
		{Value: "none", Label: "-- Categories --"},
		{Value: "episodes/", Label: "Airing"},
		{Value: "batches/", Label: "Batches"},
		{Value: "specials/", Label: "Movies or Special Episodes"},
		{Value: "encodes/", Label: "Encodings"},
		{Value: "raws/", Label: "Raws"},
	}))
	cfg.GetDynamic("rssCategories").(*configdata.SelectItem).Value = "none"

	cfg.AddDynamic("rssResolution", configdata.NewSelectItem("Select a Resolution", []configdata.Option{
		{Value: "none", Label: "-- Resolution --"},
		{Value: "res=1080p&", Label: "1080p"},
		{Value: "res=720p&", Label: "720p"},
		{Value: "res=SD&", Label: "SD"},
	}))
	cfg.GetDynamic("rssResolution").(*configdata.SelectItem).Value = "none"

	cfg.AddDynamic("rssLinkType", configdata.NewSelectItem("Select a Link Type", []configdata.Option{
		{Value: "type=torrent&", Label: "Torrent"},
		{Value: "type=magnet&", Label: "Magnet"},
	}))
	cfg.GetDynamic("rssLinkType").(*configdata.SelectItem).Value = "type=magnet&"

	cfg.AddDynamic("rssSubtitles", configdata.NewMultiSelectItem("Select one or more Subtitles (None ticked = ALL)", eraiRawsSubtitleOptions()))
	cfg.GetDynamic("rssSubtitles").(*configdata.MultiSelectItem).Values = []string{""}

	cfg.AddDynamic("DDoS-Guard", configdata.NewInfoItem("",
		"This site may use DDoS-Guard Protection, therefore Jackett requires <a href='https://github.com/Jackett/Jackett#configuring-flaresolverr' target='_blank'>FlareSolverr</a> to access it."))

	cfg.AddDynamic("download-stats-unavailable", configdata.NewInfoItem("",
		"<p>Please note that the following stats are not available for this indexer. Default values are used instead. </p><ul><li>Files</li><li>Seeders</li><li>Leechers</li></ul>"))

	cfg.AddDynamic("include-subs", configdata.NewBoolItem("Enable appending SubTitles to the Title"))
	cfg.AddDynamic("title-detail-parsing", configdata.NewBoolItem("Enable Title Detail Parsing"))
	cfg.AddDynamic("title-detail-parsing-help", configdata.NewInfoItem("",
		"Title Detail Parsing will attempt to determine the season and episode number from the release names and reformat them as a suffix in the format S1E1. If successful, this should provide better matching in applications such as Sonarr."))

	return cfg
}

func eraiRawsSubtitleOptions() []configdata.Option {
	return []configdata.Option{
		{Value: "subs[]=us&", Label: "English"},
		{Value: "subs[]=br&", Label: "Portuguese(Brazil)"},
		{Value: "subs[]=mx&", Label: "Spanish(Latin_America)"},
		{Value: "subs[]=es&", Label: "Spanish"},
		{Value: "subs[]=sa&", Label: "Arabic"},
		{Value: "subs[]=fr&", Label: "French"},
		{Value: "subs[]=de&", Label: "German"},
		{Value: "subs[]=it&", Label: "Italian"},
		{Value: "subs[]=ru&", Label: "Russian"},
		{Value: "subs[]=jp&", Label: "Japanese"},
		{Value: "subs[]=pt&", Label: "Portuguese"},
		{Value: "subs[]=pl&", Label: "Polish"},
		{Value: "subs[]=nl&", Label: "Dutch"},
		{Value: "subs[]=no&", Label: "Norwegian"},
		{Value: "subs[]=fi&", Label: "Finnish"},
		{Value: "subs[]=tr&", Label: "Turkish"},
		{Value: "subs[]=se&", Label: "Swedish"},
		{Value: "subs[]=gr&", Label: "Greek"},
		{Value: "subs[]=il&", Label: "Hebrew"},
		{Value: "subs[]=ro&", Label: "Romanian"},
		{Value: "subs[]=id&", Label: "Indonesian"},
		{Value: "subs[]=th&", Label: "Thai"},
		{Value: "subs[]=kr&", Label: "Korean"},
		{Value: "subs[]=dk&", Label: "Danish"},
		{Value: "subs[]=cn&", Label: "Chinese(Simplified&Traditional)"},
		{Value: "subs[]=bg&", Label: "Bulgarian"},
		{Value: "subs[]=vn&", Label: "Vietnamese"},
		{Value: "subs[]=in&", Label: "Hindi"},
		{Value: "subs[]=lk&", Label: "Tamil"},
		{Value: "subs[]=ua&", Label: "Ukrainian"},
		{Value: "subs[]=hu&", Label: "Hungarian"},
		{Value: "subs[]=cz&", Label: "Czech"},
		{Value: "subs[]=hr&", Label: "Croatian"},
		{Value: "subs[]=my&", Label: "Malaysian"},
		{Value: "subs[]=sk&", Label: "Slovakian"},
		{Value: "subs[]=ph&", Label: "Filipino"},
	}
}

// NewEraiRaws builds the indexer (services.Factory shape).
func NewEraiRaws(deps services.Deps) (indexer.Indexer, error) {
	e := &EraiRaws{cfg: newEraiRawsConfig()}
	e.Base = indexer.Base{
		IDValue:          "erai-raws",
		NameValue:        "Erai-Raws",
		DescriptionValue: "Erai-Raws is a Semi-Private team release site for Anime subtitles.",
		SiteLinkValue:    "https://www.erai-raws.info/",
		DefaultSiteLink:  "https://www.erai-raws.info/",
		LegacySiteLinks: []string{
			"https://erairaws.mrunblock.bond/",
			"https://erairaws.nocensor.cloud/",
			"https://beta.erai-raws.info/",
		},
		LangValue: "en-US",
		TypeValue: "semi-private",
		Caps:      eraiRawsCaps(),
		Config:    e.cfg,
		Client:    deps.Client,
		Saver:     deps.Saver,
		Cache:     deps.Cache,
	}
	return e, nil
}

func eraiRawsCaps() *models.Capabilities {
	caps := models.NewCapabilities()
	caps.TVSearchParams = []string{models.TVParamQ}
	caps.Categories.AddCategoryMapping("1", models.CatTVAnime, "Anime - Sub")
	return caps
}

// dynamic item accessors (upstream GetDynamic casts).
func (e *EraiRaws) rssKey() string {
	return e.cfg.GetDynamic("rssKey").(*configdata.StringItem).Value
}
func (e *EraiRaws) rssCategories() string {
	return e.cfg.GetDynamic("rssCategories").(*configdata.SelectItem).Value
}
func (e *EraiRaws) rssResolution() string {
	return e.cfg.GetDynamic("rssResolution").(*configdata.SelectItem).Value
}
func (e *EraiRaws) rssLinkType() string {
	return e.cfg.GetDynamic("rssLinkType").(*configdata.SelectItem).Value
}
func (e *EraiRaws) getRSSSubtitles() string {
	return strings.Join(e.cfg.GetDynamic("rssSubtitles").(*configdata.MultiSelectItem).Values, "")
}
func (e *EraiRaws) isTitleDetailParsingEnabled() bool {
	return e.cfg.GetDynamic("title-detail-parsing").(*configdata.BoolItem).Value
}
func (e *EraiRaws) isSubsEnabled() bool {
	return e.cfg.GetDynamic("include-subs").(*configdata.BoolItem).Value
}

// ApplyConfiguration ports upstream: test-query the feed, mark configured
// and save on success.
func (e *EraiRaws) ApplyConfiguration(ctx context.Context, items []indexer.ConfigItem) (indexer.ConfigStatus, error) {
	if err := e.ApplyItems(items); err != nil {
		return indexer.ConfigCompleted, err
	}
	if err := e.LoadValuesFromJSON(nil, false); err != nil {
		return indexer.ConfigCompleted, err
	}
	releases, err := e.perform(ctx, models.NewTorznabQuery())
	if err != nil {
		return indexer.ConfigCompleted, err
	}
	if len(releases) == 0 {
		return indexer.ConfigCompleted, errors.New(eraiRawsNoResultsErr) //nolint:staticcheck // upstream EraiRaws.cs error text, kept 1:1
	}
	e.SetConfigured(true)
	e.SaveConfig(ctx)
	return indexer.ConfigCompleted, nil
}

func (e *EraiRaws) ResultsForQuery(ctx context.Context, q *models.TorznabQuery, isMeta bool) (*indexer.IndexerResult, error) {
	return e.ResultsForQueryWith(ctx, q, isMeta, e.perform)
}

func (e *EraiRaws) Test(ctx context.Context) error {
	return e.Base.Test(ctx, e.perform)
}

// perform ports upstream PerformQuery: fetch feed → convert → filter → map.
func (e *EraiRaws) perform(ctx context.Context, query *models.TorznabQuery) ([]*models.ReleaseInfo, error) {
	feedItems, err := e.getItemsFromFeed(ctx)
	if err != nil {
		return nil, err
	}
	releaseInfos := e.convertFeedItems(convertFeedItemsToReleaseInfo(feedItems))

	// Perform basic filter within Jackett
	var filtered []*eraiRawsReleaseInfo
	for _, fi := range releaseInfos {
		if !query.MatchQueryStringAND(fi.Title, 0, "") {
			continue
		}
		filtered = append(filtered, fi)
	}

	return e.convertToJackettInfo(filtered), nil
}

// getItemsFromFeed ports upstream GetItemsFromFeed (the http stack follows
// redirects itself, matching upstream FollowIfRedirect).
func (e *EraiRaws) getItemsFromFeed(ctx context.Context) ([]*eraiRawsFeedItem, error) {
	feedURI := e.SiteLink() +
		strings.ReplaceAll(e.rssCategories(), "none", "") +
		eraiRawsRSSFeed +
		strings.ReplaceAll(e.rssResolution(), "none", "") +
		e.getRSSSubtitles() +
		e.rssLinkType() +
		"token=" +
		e.rssKey()

	if e.Client == nil {
		return nil, errors.New("http client not configured")
	}
	result, err := e.Client.Do(ctx, e.ID(), &httpclient.Request{
		URL:     feedURI,
		Cookies: e.CookieHeader(),
	})
	if err != nil {
		return nil, err
	}
	if strings.Contains(string(result.Body), eraiRaws403Marker) {
		e.log().Error("[EraiRaws] 403 Forbidden")
		return nil, errors.New(eraiRaws403Error) //nolint:staticcheck // upstream EraiRaws.cs error text, kept 1:1
	}

	var rss struct {
		Channel struct {
			Items []eraiRawsFeedItem `xml:"item"`
		} `xml:"channel"`
	}
	if err := xml.Unmarshal(result.Body, &rss); err != nil {
		return nil, err
	}

	var feedItems []*eraiRawsFeedItem
	for i := range rss.Channel.Items {
		item := &rss.Channel.Items[i]
		if !item.isValid() {
			e.log().Warn(fmt.Sprintf("Could not parse %s RSS item '%v'", e.Name(), item))
			continue
		}
		feedItems = append(feedItems, item)
	}
	return feedItems, nil
}

// convertFeedItems ports ConvertFeedItemsToEraiRawsReleaseInfo validation.
func (e *EraiRaws) convertFeedItems(releaseInfos []*eraiRawsReleaseInfo) []*eraiRawsReleaseInfo {
	var out []*eraiRawsReleaseInfo
	for _, ri := range releaseInfos {
		// Validate the release
		if ri.PublishDate == nil {
			e.log().Warn(fmt.Sprintf("Failed to parse %s RSS feed item '%s' due to malformed publish date.", e.Name(), ri.Title))
			continue
		}
		if ri.MagnetLink == nil && strings.TrimSpace(ri.InfoHash) == "" {
			e.log().Warn(fmt.Sprintf("Failed to parse %s RSS feed item '%s' due to malformed link URI and no infohash available.", e.Name(), ri.Title))
			continue
		}

		// If enabled, perform detailed title parsing
		if e.isTitleDetailParsingEnabled() {
			ri.Title = eraiTitleParser{}.Parse(ri.Title)
		}

		out = append(out, ri)
	}
	return out
}

// convertFeedItemsToReleaseInfo ports the EraiRawsReleaseInfo ctor mapping.
func convertFeedItemsToReleaseInfo(feedItems []*eraiRawsFeedItem) []*eraiRawsReleaseInfo {
	var out []*eraiRawsReleaseInfo
	for _, fi := range feedItems {
		out = append(out, newEraiRawsReleaseInfo(fi))
	}
	return out
}

// convertToJackettInfo ports ConvertEraiRawsInfoToJackettInfo.
func (e *EraiRaws) convertToJackettInfo(feedItems []*eraiRawsReleaseInfo) []*models.ReleaseInfo {
	var releases []*models.ReleaseInfo
	for _, fi := range feedItems {
		var guid *url.URL
		if fi.MagnetLink != nil {
			guid = fi.MagnetLink
		} else if fi.DetailsLink != nil {
			// Magnet link is not available so generate something unique
			// (upstream builds on DetailsLink and would NRE when nil; guarded
			// here — see 澄清记录).
			u := *fi.DetailsLink
			if u.RawQuery != "" {
				u.RawQuery += "&"
			}
			u.RawQuery += "infoHash=" + fi.InfoHash
			guid = &u
		}

		subs := ""
		if e.isSubsEnabled() {
			subs = " JAPANESE [Subs: " + fi.SubTitles + "]"
		}

		size := fi.Size
		files := int64(1)
		seeders := int64(1)
		peers := int64(2)
		dvf := 0.0
		uvf := 1.0

		releases = append(releases, &models.ReleaseInfo{
			Title:                fi.Title + " - " + fi.Quality + subs,
			Guid:                 guid,
			MagnetUri:            fi.MagnetLink,
			InfoHash:             fi.InfoHash,
			Details:              fi.DetailsLink,
			PublishDate:          fi.PublishDate.Local(), // upstream ToLocalTime()
			Category:             e.Caps.Categories.MapTrackerCatToNewznab("1"),
			Size:                 &size,
			Files:                &files,
			Seeders:              &seeders,
			Peers:                &peers,
			DownloadVolumeFactor: &dvf,
			UploadVolumeFactor:   &uvf,
		})
	}
	return releases
}

// log is a nil-safe logger for the manual package (Base.Logger may be nil).
func (e *EraiRaws) log() *slog.Logger {
	if e.Logger != nil {
		return e.Logger
	}
	return slog.Default()
}

// --- upstream DTOs ----------------------------------------------------------

// eraiRawsFeedItem mirrors upstream RssFeedItem + TryParse (the namespace
// prefix is fixed by the site; encoding/xml matches the local names).
type eraiRawsFeedItem struct {
	Title       string `xml:"title"`
	Link        string `xml:"link"`
	PublishDate string `xml:"pubDate"`
	InfoHash    string `xml:"infohash"`
	Size        string `xml:"size"`
	Description string `xml:"description"`
	Quality     string `xml:"resolution"`
	SubTitles   string `xml:"subtitles"`
}

// isValid mirrors upstream RssFeedItem.IsValid (Title/Size/Quality are
// defaulted before validation upstream, so they never fail here).
func (fi *eraiRawsFeedItem) isValid() bool {
	missingBothHashAndLink := strings.TrimSpace(fi.Link) == "" && strings.TrimSpace(fi.InfoHash) == ""
	return !missingBothHashAndLink &&
		strings.TrimSpace(fi.Title) != "" &&
		strings.TrimSpace(fi.PublishDate) != "" &&
		strings.TrimSpace(fi.Quality) != ""
}

// eraiRawsReleaseInfo mirrors upstream EraiRawsReleaseInfo.
type eraiRawsReleaseInfo struct {
	Quality     string
	SubTitles   string
	Title       string
	MagnetLink  *url.URL
	InfoHash    string
	DetailsLink *url.URL
	PublishDate *time.Time
	Size        int64
}

var (
	eraiTitlePrefixRe = regexp.MustCompile(`^\[.+?\]`)
	eraiTitleSuffixRe = regexp.MustCompile(` \[.+\]`)
	eraiDetailsHrefRe = regexp.MustCompile(`href="(.+?)"`)
)

// newEraiRawsReleaseInfo ports the upstream EraiRawsReleaseInfo ctor.
func newEraiRawsReleaseInfo(fi *eraiRawsFeedItem) *eraiRawsReleaseInfo {
	size := fi.Size
	if strings.TrimSpace(size) == "" {
		size = eraiRawsDefaultSize
	}
	subs := fi.SubTitles
	if subs == "" {
		subs = "[]"
	}

	ri := &eraiRawsReleaseInfo{
		Title:     stripEraiTitle(fi.Title),
		Quality:   fi.Quality,
		Size:      parseutil.GetBytes(size),
		InfoHash:  fi.InfoHash,
		SubTitles: strings.ToUpper(strings.ReplaceAll(strings.ReplaceAll(subs, "[", " "), "]", " ")),
	}
	if m := eraiDetailsHrefRe.FindStringSubmatch(fi.Description); m != nil {
		if details, err := url.Parse(m[1]); err == nil && details.IsAbs() {
			ri.DetailsLink = details
		}
	}
	if magnet, err := url.Parse(fi.Link); err == nil && magnet.IsAbs() {
		ri.MagnetLink = magnet
	}
	if t, ok := eraiParsePublishDate(fi.PublishDate); ok {
		ri.PublishDate = &t
	}
	return ri
}

// stripEraiTitle ports upstream StripTitle.
func stripEraiTitle(rawTitle string) string {
	prefixStripped := eraiTitlePrefixRe.ReplaceAllString(rawTitle, "")
	suffixStripped := eraiTitleSuffixRe.ReplaceAllString(prefixStripped, "")
	return strings.TrimSpace(suffixStripped)
}

// eraiParsePublishDate mirrors DateTimeOffset.TryParse on the pubDate text
// (RSS RFC1123 variants first, then the common fallbacks).
func eraiParsePublishDate(s string) (time.Time, bool) {
	if t, err := mail.ParseDate(s); err == nil {
		return t, true
	}
	for _, layout := range []string{time.RFC1123, time.RFC1123Z, time.RFC3339, "2006-01-02 15:04:05", "2006-01-02"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

// --- upstream TitleParser ----------------------------------------------------

var (
	eraiDetailSearchEpisode = []eraiSearchReplace{
		{re: regexp.MustCompile(`(?i) - (?P<detail>[0-9]+)$`), replace: " - "}, // "<title> - <episode>" <end_of_title>
		{re: regexp.MustCompile(`(?i) - (?P<detail>[0-9]+) `), replace: " - "}, // "<title> - <episode> ..."
	}
	eraiDetailSearchSeason = []eraiSearchReplace{
		{re: regexp.MustCompile(`(?i) Season (?P<detail>[0-9]+)`), replace: ""},              // "Season 2"
		{re: regexp.MustCompile(`(?i) (?P<detail>[0-9]+)(st|nd|rd|th) Season`), replace: ""}, // "2nd Season"
		{re: regexp.MustCompile(`(?i) Part (?P<detail>[0-9]+) - `), replace: " - "},          // "<title> Part 2 - <episode>"
		{re: regexp.MustCompile(`(?i) (?P<detail>[0-9]+) - `), replace: " - "},               // "<title> 2 - <episode>"
	}
)

type eraiSearchReplace struct {
	re      *regexp.Regexp
	replace string
}

// eraiTitleParser ports upstream EraiRaws.TitleParser.
type eraiTitleParser struct{}

func (eraiTitleParser) Parse(title string) string {
	// upstream dictionary order: episode searches first, then season
	strippedEpisode, episode := eraiSearchTitleForDetail(title, eraiDetailSearchEpisode)
	stripped, season := eraiSearchTitleForDetail(strippedEpisode, eraiDetailSearchSeason)

	seasonEpisodeIdentifier := eraiPrefixOrDefault("S", season) + eraiPrefixOrDefault("E", episode)

	// If title still contains the hyphen, insert the identifier after it.
	// Otherwise put it at the end.
	strangeHyphenPosition := strings.LastIndex(stripped, "-")
	if strangeHyphenPosition > -1 {
		return strings.TrimSpace(strings.TrimSpace(stripped[:strangeHyphenPosition]) +
			" - " + seasonEpisodeIdentifier + " " +
			strings.TrimSpace(stripped[strangeHyphenPosition+1:]))
	}

	return strings.TrimSpace(strings.TrimSpace(stripped) + " " + seasonEpisodeIdentifier)
}

// eraiSearchTitleForDetail ports upstream SearchTitleForDetail: the first
// matching pattern wins; the detail is group "detail", the title is the
// case-insensitive replace-all of the whole pattern.
func eraiSearchTitleForDetail(title string, patterns []eraiSearchReplace) (string, string) {
	for _, srp := range patterns {
		m := srp.re.FindStringSubmatch(title)
		if m != nil {
			detail := ""
			for i, name := range srp.re.SubexpNames() {
				if name == "detail" && i < len(m) {
					detail = m[i]
					break
				}
			}
			strippedTitle := srp.re.ReplaceAllString(title, srp.replace)
			return strippedTitle, detail
		}
	}
	// Nothing found so return null
	return title, ""
}

// eraiPrefixOrDefault ports upstream PrefixOrDefault.
func eraiPrefixOrDefault(prefix, value string) string {
	if strings.TrimSpace(value) == "" {
		return ""
	}
	return prefix + value
}
