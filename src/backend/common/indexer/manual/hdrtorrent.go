// Package manual holds the hand-written indexers (one file per site).
package manual

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
	"golang.org/x/net/html"

	"vestt/common/httpclient"
	"vestt/common/indexer"
	"vestt/common/indexer/configdata"
	"vestt/common/indexer/shared"
	"vestt/common/models"
	"vestt/common/services"
)

// HDRTorrent ports Jackett.Common/Indexers/Definitions/HDRTorrent.cs.
//
// mechanism: html (public Brazilian tracker; "index.php?busca=" search, one
// details page fetch per row, file info from div.infos, one release per
// magnet link)
//
// Ported from: Definitions/HDRTorrent.cs (+ Abstract/PublicBrazilianIndexerBase.cs
// SimpleRequestGenerator/FileInfo/RowParsingExtensions).
//
// 澄清记录:
//   - upstream `new Uri(detailUrlStr)` requires absolute detail hrefs; the
//     port resolves relative hrefs against the site link instead of crashing
//     (same precedent as BitHDTV).
//   - upstream ExtractFileInfo splits InnerHtml on the literal "<br>"; the
//     Go HTML parser normalizes <br> to "<br/>" in Html(), so the port walks
//     the child nodes of div.infos p and treats every <br> element as a line
//     break (observationally identical for the real site's <br> markup).
//   - upstream ExtractFileInfo applies the value replacements ("Dual Áudio"
//     → "Dual", "Full HD" → "1080p", ...) via a switch with when-clauses:
//     only the FIRST matching clause runs. The port replicates that
//     (sequential if/else chain), including that "SD"/"WEB" match inside
//     larger words.
//   - magnetLink.ExtractCategory(release.Title) walks descendants of the
//     magnet anchor looking for "div.title > a" (never present), so the
//     category always falls out of the title text alone — the port calls
//     shared.ExtractCategory("", title) directly.
//   - upstream HDRTorrentParser.GetTitleElementOrNull is dead code here (the
//     overridden ParseResponse never calls ExtractTitleOrDefault); not ported.
//   - upstream fetches details pages with a bare WebRequest (no cookies, no
//     error check); a fetch failure throws and kills the whole parse via
//     OnParseError. The port keeps that: details-fetch errors abort with a
//     parse error.
type HDRTorrent struct {
	shared.Brazilian
	cfg *hdrConfig
}

type hdrConfig struct {
	configdata.ConfigData
}

// NewHDRTorrent builds the indexer (services.Factory shape).
func NewHDRTorrent(deps services.Deps) (indexer.Indexer, error) {
	h := &HDRTorrent{cfg: &hdrConfig{ConfigData: *configdata.NewConfigData()}}
	h.Base = indexer.Base{
		IDValue:          "hdrtorrent",
		NameValue:        "HDRTorrent",
		DescriptionValue: shared.BrazilianDescription("HDRTorrent"),
		SiteLinkValue:    "https://hdrtorrents.net/",
		DefaultSiteLink:  "https://hdrtorrents.net/",
		LegacySiteLinks:  []string{"https://hdrtorrent.com/"},
		LangValue:        "pt-BR",
		TypeValue:        "public",
		Caps:             shared.NewBrazilianCaps(),
		Config:           h.cfg,
		Client:           deps.Client,
		Saver:            deps.Saver,
		Cache:            deps.Cache,
	}
	h.SearchQueryParamsKey = "index.php?busca="
	h.WireDelay() // upstream webclient.requestDelay = .5
	return h, nil
}

func (h *HDRTorrent) ResultsForQuery(ctx context.Context, q *models.TorznabQuery, isMeta bool) (*indexer.IndexerResult, error) {
	return h.ResultsForQueryWith(ctx, q, isMeta, h.perform)
}

func (h *HDRTorrent) Test(ctx context.Context) error {
	return h.Base.Test(ctx, h.perform)
}

func (h *HDRTorrent) perform(ctx context.Context, query *models.TorznabQuery) ([]*models.ReleaseInfo, error) {
	if h.Client == nil {
		return nil, errors.New("http client not configured")
	}
	searchURL := h.SearchURL(query)
	res, err := h.Client.Do(ctx, h.ID(), &httpclient.Request{URL: searchURL})
	if err != nil {
		return nil, err
	}
	return h.parseResponse(ctx, string(res.Body))
}

// parseResponse ports upstream HDRTorrentParser.ParseResponse.
func (h *HDRTorrent) parseResponse(ctx context.Context, content string) ([]*models.ReleaseInfo, error) {
	var releases []*models.ReleaseInfo
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(content))
	if err != nil {
		return nil, h.parseError(content, err)
	}

	var rowErr error
	doc.Find("div.col").EachWithBreak(func(_ int, row *goquery.Selection) bool {
		h2Anchor := row.Find("a.media-card-link").First()
		if h2Anchor.Length() == 0 {
			return true
		}

		title := strings.TrimSpace(h2Anchor.Text())
		detailURLStr := strings.TrimSpace(h2Anchor.AttrOr("href", ""))
		if detailURLStr == "" {
			return true
		}
		detailURL, err := hdrResolve(h.SiteLink(), detailURLStr)
		if err != nil {
			rowErr = fmt.Errorf("invalid details url: %w", err)
			return false
		}

		releaseCommonInfo := &models.ReleaseInfo{
			Title:   shared.CleanTitle(title),
			Details: detailURL,
			Guid:    detailURL,
			Seeders: hdrI64(1),
		}

		detailsRes, err := h.Client.Do(ctx, h.ID(), &httpclient.Request{URL: detailURL.String()})
		if err != nil {
			rowErr = err
			return false
		}
		detailsDoc, err := goquery.NewDocumentFromReader(strings.NewReader(string(detailsRes.Body)))
		if err != nil {
			rowErr = fmt.Errorf("parsing details page: %w", err)
			return false
		}

		fileInfoDict := hdrExtractFileInfo(detailsDoc)
		fileInfo := hdrFileInfoFromDict(fileInfoDict)

		publishedMeta, _ := detailsDoc.Find("meta[property='article:published_time']").First().Attr("content")
		if publishedMeta != "" {
			if parsedDate, ok := hdrTryParse(publishedMeta); ok {
				releaseCommonInfo.PublishDate = parsedDate
			} else {
				releaseCommonInfo.PublishDate = hdrToday()
			}
		} else {
			releaseCommonInfo.PublishDate = hdrToday()
		}

		var parseErr error
		detailsDoc.Find("a[href^='magnet:?']").EachWithBreak(func(_ int, magnetLink *goquery.Selection) bool {
			magnet := magnetLink.AttrOr("href", "")
			if magnet == "" {
				return true
			}
			magnetURI, err := url.Parse(magnet)
			if err != nil {
				parseErr = err
				return false
			}

			release := releaseCommonInfo.Clone()
			release.Guid = magnetURI
			release.MagnetUri = magnetURI

			parentText := ""
			if parent := magnetLink.Parent(); parent.Length() > 0 {
				parentText = strings.TrimSpace(parent.Text())
			}
			if parentText != "" {
				parentText = hdrDownloadTorrentRe.ReplaceAllString(parentText, "")
				for _, rep := range [][2]string{
					{"DUAL ÁUDIO", "Dual"},
					{"DUAL AUDIO", "Dual"},
					{"ÁUDIO", "Audio"},
					{"AUDIO", "Audio"},
					{"DUBLADO", "Dubbed"},
					{"LEGENDADO", "Subbed"},
					{"MKV", ""},
					{"MP4", ""},
					{"MAGNET", ""},
					{"TORRENT", ""},
					{"LINK", ""},
				} {
					parentText = strings.ReplaceAll(parentText, rep[0], rep[1])
				}
				parentText = strings.TrimSpace(parentText)

				if parentText != "" {
					release.Title = strings.TrimSpace(release.Title + " " + parentText)
				}
			}

			resolution := fileInfo.Quality
			if resolution == "" {
				resolution = fileInfo.VideoQuality
			}
			if resolution != "" {
				release.Title = strings.TrimSpace(release.Title + " " + resolution)
			}

			release.Category = shared.ExtractCategory("", release.Title)
			size := shared.GetBytes(fileInfo.Size)
			if size > 0 {
				release.Size = &size
			} else {
				byResolution := shared.ExtractSizeByResolution(release.Title)
				release.Size = &byResolution
			}

			release.DownloadVolumeFactor = hdrF64(0)
			release.UploadVolumeFactor = hdrF64(1)

			if fileInfo.Audio != nil {
				release.Languages = append([]string(nil), fileInfo.Audio...)
			}
			if fileInfo.Genres != nil {
				release.Genres = append([]string(nil), fileInfo.Genres...)
			}
			if fileInfo.Subtitle != "" {
				release.Subs = []string{fileInfo.Subtitle}
			}

			if strings.TrimSpace(release.Title) != "" {
				releases = append(releases, release)
			}
			return true
		})
		if parseErr != nil {
			rowErr = parseErr
			return false
		}
		return true
	})
	if rowErr != nil {
		return nil, h.parseError(content, rowErr)
	}
	return releases, nil
}

// hdrDownloadTorrentRe mirrors upstream Regex.Replace(parentText,
// "DOWNLOAD TORRENT", "", IgnoreCase).
var hdrDownloadTorrentRe = regexp.MustCompile(`(?i)DOWNLOAD TORRENT`)

// hdrExtractFileInfo ports upstream HDRTorrentParser.ExtractFileInfo.
func hdrExtractFileInfo(detailsDoc *goquery.Document) map[string]string {
	fileInfo := map[string]string{}
	infoSection := detailsDoc.Find("div.infos p").First()
	if infoSection.Length() == 0 {
		return fileInfo
	}

	// Upstream splits InnerHtml on the literal "<br>". The Go HTML parser
	// serializes <br> as "<br/>" in Html(), so the port walks the child
	// nodes and starts a new "line" at every <br> element instead —
	// observationally identical for the real site's <br> markup.
	var lines []string
	var cur strings.Builder
	flush := func() {
		if cur.Len() > 0 {
			lines = append(lines, cur.String())
			cur.Reset()
		}
	}
	for n := infoSection.Nodes[0].FirstChild; n != nil; n = n.NextSibling {
		if n.Type == html.ElementNode && n.Data == "br" {
			flush()
			continue
		}
		if n.Type == html.TextNode {
			cur.WriteString(n.Data)
			continue
		}
		var buf bytes.Buffer
		if err := html.Render(&buf, n); err == nil {
			cur.WriteString(buf.String())
		}
	}
	flush()

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if !strings.Contains(line, "<b>") || !strings.Contains(line, ":") {
			continue
		}
		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(strings.ReplaceAll(strings.ReplaceAll(parts[0], "<b>", ""), "</b>", ""))
		value := parts[1]
		for _, tag := range []string{"<b>", "</b>", "<strong>", "</strong>"} {
			value = strings.ReplaceAll(value, tag, "")
		}
		value = strings.TrimSpace(value)

		if strings.Contains(value, "<") {
			value = strings.TrimSpace(shared.TextOf(value))
		}

		// upstream switch with when-clauses: first match wins.
		switch {
		case strings.Contains(value, "Dual Áudio"):
			value = strings.ReplaceAll(value, "Dual Áudio", "Dual")
		case strings.Contains(value, "Dual Audio"):
			value = strings.ReplaceAll(value, "Dual Audio", "Dual")
		case strings.Contains(value, "Full HD"):
			value = strings.ReplaceAll(value, "Full HD", "1080p")
		case strings.Contains(value, "4K"):
			value = strings.ReplaceAll(value, "4K", "2160p")
		case strings.Contains(value, "SD"):
			value = strings.ReplaceAll(value, "SD", "480p")
		case strings.Contains(value, "WEB"):
			value = strings.ReplaceAll(value, "WEB", "WEB-DL")
		}

		if key != "" && value != "" {
			fileInfo[key] = value
		}
	}
	return fileInfo
}

// hdrFileInfo ports upstream PublicBrazilianIndexerBase.FileInfo +
// FileInfo.FromDictionary.
type hdrFileInfo struct {
	Genres       []string
	Audio        []string
	Subtitle     string
	Quality      string
	Size         string
	VideoQuality string
}

func hdrFileInfoFromDict(dict map[string]string) hdrFileInfo {
	var fi hdrFileInfo
	if genres, ok := dict["Gênero"]; ok && genres != "" {
		for _, g := range strings.Split(genres, ",") {
			fi.Genres = append(fi.Genres, strings.TrimSpace(g))
		}
	}
	if audio, ok := dict["Áudio"]; ok && audio != "" {
		for _, a := range strings.Split(audio, ",") {
			fi.Audio = append(fi.Audio, strings.TrimSpace(a))
		}
	} else if lang, ok := dict["Idioma"]; ok {
		fi.Audio = []string{lang}
	}
	if subtitle, ok := dict["Legenda"]; ok {
		fi.Subtitle = subtitle
	}
	if quality, ok := dict["Qualidade"]; ok {
		fi.Quality = quality
	}
	if size, ok := dict["Tamanho"]; ok {
		fi.Size = size
	}
	if videoQuality, ok := dict["Qualidade de Vídeo"]; ok {
		fi.VideoQuality = videoQuality
	}
	return fi
}

// hdrTryParse mirrors DateTime.TryParse for the meta article:published_time
// content (RFC3339 / common tracker formats).
func hdrTryParse(s string) (time.Time, bool) {
	s = strings.TrimSpace(s)
	for _, layout := range []string{time.RFC3339, "2006-01-02 15:04:05", "2006-01-02"} {
		if t, err := time.ParseInLocation(layout, s, time.Local); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

// hdrToday mirrors DateTime.Today (local midnight).
func hdrToday() time.Time {
	now := time.Now()
	return time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
}

// log is a nil-safe logger for the manual package (Base.Logger may be nil).
func (h *HDRTorrent) log() *slog.Logger {
	if h.Logger != nil {
		return h.Logger
	}
	return slog.Default()
}

// parseError mirrors upstream OnParseError.
func (h *HDRTorrent) parseError(dump string, err error) error {
	h.log().Error(fmt.Sprintf("Error while parsing result: %v\n%s", err, dump))
	return &indexer.IndexerError{IndexerID: h.ID(), Kind: indexer.KindParse, Message: "Parse error"}
}

func hdrF64(v float64) *float64 { return &v }

func hdrI64(v int64) *int64 { return &v }

// hdrResolve parses href and resolves relative references against the site
// link (upstream `new Uri(...)` requires absolute hrefs; see file header).
func hdrResolve(siteLink, href string) (*url.URL, error) {
	u, err := url.Parse(href)
	if err != nil {
		return nil, err
	}
	if u.IsAbs() {
		return u, nil
	}
	base, err := url.Parse(siteLink)
	if err != nil {
		return nil, err
	}
	return base.ResolveReference(u), nil
}
