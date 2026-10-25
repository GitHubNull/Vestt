package manual

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"vestt/common/httpclient"
	"vestt/common/indexer"
	"vestt/common/indexer/configdata"
	"vestt/common/models"
	"vestt/common/services"
)

type eraiSite struct {
	srv   *httptest.Server
	mu    sync.Mutex
	urls  []string
	serve map[string]string
}

func newEraiSite(t *testing.T, serve map[string]string) *eraiSite {
	t.Helper()
	s := &eraiSite{serve: serve}
	s.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		s.urls = append(s.urls, r.URL.RequestURI())
		payload, ok := s.serve[r.URL.Path]
		s.mu.Unlock()
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/rss+xml; charset=utf-8")
		_, _ = w.Write([]byte(payload))
	}))
	t.Cleanup(s.srv.Close)
	return s
}

func newTestEraiRaws(t *testing.T, site *eraiSite) *EraiRaws {
	t.Helper()
	client, err := httpclient.New(httpclient.Config{}, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("httpclient.New: %v", err)
	}
	ix, err := NewEraiRaws(services.Deps{Client: client})
	if err != nil {
		t.Fatalf("NewEraiRaws: %v", err)
	}
	e := ix.(*EraiRaws)
	e.SiteLinkValue = site.srv.URL + "/"
	client.SetRequestDelay(e.ID(), 0)
	return e
}

const eraiRSSFixture = `<?xml version="1.0" encoding="UTF-8"?>
<rss version="2.0" xmlns:erai="https://www.erai-raws.info/rss-page/">
<channel>
<title>Erai-Raws Feed</title>
<item>
  <title>[Erai-Raws] Some Anime - 01 [1080p].mkv</title>
  <link>magnet:?xt=urn:btih:abcdef0123456789abcdef0123456789abcdef01&amp;dn=Some+Anime</link>
  <pubDate>Mon, 15 Jan 2024 10:30:00 +0000</pubDate>
  <description>&lt;a href="https://www.erai-raws.info/episodes/some-anime/"&gt;Some Anime&lt;/a&gt;</description>
  <erai:infohash>abcdef0123456789abcdef0123456789abcdef01</erai:infohash>
  <erai:size>1.5GB</erai:size>
  <erai:resolution>1080p</erai:resolution>
  <erai:subtitles>[us][br]</erai:subtitles>
</item>
<item>
  <title>[Erai-Raws] Other Show - 02 [720p].mkv</title>
  <link>https://ddl.erai-raws.info/torrents/Other.Show.torrent</link>
  <pubDate>Tue, 16 Jan 2024 12:00:00 +0000</pubDate>
  <description>&lt;a href="https://www.erai-raws.info/episodes/other-show/"&gt;Other Show&lt;/a&gt;</description>
  <erai:infohash>1111111111111111111111111111111111111111</erai:infohash>
  <erai:size>512MB</erai:size>
  <erai:resolution>720p</erai:resolution>
</item>
<item>
  <title>[Erai-Raws] Broken Item - 03 [1080p]</title>
  <pubDate>Wed, 17 Jan 2024 12:00:00 +0000</pubDate>
  <erai:resolution>1080p</erai:resolution>
  <erai:size>100MB</erai:size>
</item>
</channel>
</rss>`

func TestEraiRawsCaps(t *testing.T) {
	ix, err := NewEraiRaws(services.Deps{})
	if err != nil {
		t.Fatalf("NewEraiRaws: %v", err)
	}
	caps := ix.Capabilities()
	if strings.Join(caps.TVSearchParams, ",") != models.TVParamQ {
		t.Fatalf("TVSearchParams: %v", caps.TVSearchParams)
	}
	got := caps.Categories.MapTrackerCatToNewznab("1")
	if !containsInt(got, 5070) {
		t.Fatalf("MapTrackerCatToNewznab(1) = %v", got)
	}
}

func TestEraiRawsConfigForm(t *testing.T) {
	ix, err := NewEraiRaws(services.Deps{})
	if err != nil {
		t.Fatalf("NewEraiRaws: %v", err)
	}
	items, err := ix.ConfigurationForSetup(context.Background())
	if err != nil {
		t.Fatalf("ConfigurationForSetup: %v", err)
	}
	var ids []string
	for _, it := range items {
		ids = append(ids, it["id"].(string))
	}
	// 13 items: sitelink, rsskey, 4 displayinfo items with upstream Name ""
	// (ids ""), 3 selects, 1 multiselect, 2 bools, tags.
	wantLen := 13
	if len(ids) != wantLen {
		t.Fatalf("config ids (%d): %v", len(ids), ids)
	}
	if ids[0] != "sitelink" || ids[1] != "rsskey" || ids[len(ids)-1] != "tags" {
		t.Fatalf("config ids: %v", ids)
	}
	joined := strings.Join(ids, "|")
	for _, want := range []string{
		"selectacategory", "selectaresolution", "selectalinktype",
		"selectoneormoresubtitles(noneticked=all)",
		"enableappendingsubtitlestothetitle", "enabletitledetailparsing",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("config ids missing %q: %v", want, ids)
		}
	}
}

func TestEraiRawsFeedRequestAndParse(t *testing.T) {
	site := newEraiSite(t, map[string]string{"/feed/": eraiRSSFixture})
	e := newTestEraiRaws(t, site)

	res, err := e.ResultsForQuery(context.Background(), &models.TorznabQuery{QueryType: "search"}, false)
	if err != nil {
		t.Fatalf("ResultsForQuery: %v", err)
	}
	// the invalid item (no link and no infohash) is skipped
	if len(res.Releases) != 2 {
		t.Fatalf("releases: %d", len(res.Releases))
	}

	site.mu.Lock()
	if len(site.urls) != 1 || site.urls[0] != "/feed/?type=magnet&token=" {
		t.Fatalf("urls: %v", site.urls)
	}
	site.mu.Unlock()

	r := res.Releases[0]
	// upstream StripTitle only removes the bracketed suffix, the trailing
	// ".mkv" stays attached to the episode number.
	if r.Title != "Some Anime - 01.mkv - 1080p" {
		t.Fatalf("title: %q", r.Title)
	}
	if r.MagnetUri == nil || !strings.HasPrefix(r.MagnetUri.String(), "magnet:?xt=urn:btih:abcdef0123456789abcdef0123456789abcdef01") {
		t.Fatalf("magnet: %v", r.MagnetUri)
	}
	if r.Guid == nil || r.Guid.String() != r.MagnetUri.String() {
		t.Fatalf("guid: %v", r.Guid)
	}
	if r.InfoHash != "abcdef0123456789abcdef0123456789abcdef01" {
		t.Fatalf("infohash: %q", r.InfoHash)
	}
	if r.Details == nil || r.Details.String() != "https://www.erai-raws.info/episodes/some-anime/" {
		t.Fatalf("details: %v", r.Details)
	}
	if r.Size == nil || *r.Size != 1610612736 { // 1.5GB
		t.Fatalf("size: %v", r.Size)
	}
	if r.Files == nil || *r.Files != 1 {
		t.Fatalf("files: %v", r.Files)
	}
	if r.Seeders == nil || *r.Seeders != 1 {
		t.Fatalf("seeders: %v", r.Seeders)
	}
	if r.Peers == nil || *r.Peers != 2 {
		t.Fatalf("peers: %v", r.Peers)
	}
	if !containsInt(r.Category, 5070) {
		t.Fatalf("category: %v", r.Category)
	}
	if r.DownloadVolumeFactor == nil || *r.DownloadVolumeFactor != 0 {
		t.Fatalf("dvf: %v", r.DownloadVolumeFactor)
	}
	if r.UploadVolumeFactor == nil || *r.UploadVolumeFactor != 1 {
		t.Fatalf("uvf: %v", r.UploadVolumeFactor)
	}
	wantDate := time.Date(2024, 1, 15, 10, 30, 0, 0, time.UTC).Local()
	if !r.PublishDate.Equal(wantDate) {
		t.Fatalf("publish date: %v want %v", r.PublishDate, wantDate)
	}

	// second item: upstream treats any absolute link as the magnet URI
	r2 := res.Releases[1]
	if r2.Title != "Other Show - 02.mkv - 720p" {
		t.Fatalf("title2: %q", r2.Title)
	}
	if r2.MagnetUri == nil || r2.MagnetUri.String() != "https://ddl.erai-raws.info/torrents/Other.Show.torrent" {
		t.Fatalf("magnet2: %v", r2.MagnetUri)
	}
	if r2.Size == nil || *r2.Size != 536870912 { // 512MB
		t.Fatalf("size2: %v", r2.Size)
	}
}

func TestEraiRawsFeedURLOptions(t *testing.T) {
	site := newEraiSite(t, map[string]string{"/episodes/feed/": eraiRSSFixture})
	e := newTestEraiRaws(t, site)
	e.cfg.GetDynamic("rssCategories").(*configdata.SelectItem).Value = "episodes/"
	e.cfg.GetDynamic("rssResolution").(*configdata.SelectItem).Value = "res=1080p&"
	e.cfg.GetDynamic("rssSubtitles").(*configdata.MultiSelectItem).Values = []string{"subs[]=us&", "subs[]=br&"}
	e.cfg.GetDynamic("rssLinkType").(*configdata.SelectItem).Value = "type=torrent&"
	e.cfg.GetDynamic("rssKey").(*configdata.StringItem).Value = "myrsskey"

	if _, err := e.ResultsForQuery(context.Background(), &models.TorznabQuery{QueryType: "search"}, false); err != nil {
		t.Fatalf("ResultsForQuery: %v", err)
	}
	site.mu.Lock()
	defer site.mu.Unlock()
	if len(site.urls) != 1 {
		t.Fatalf("urls: %v", site.urls)
	}
	u, err := url.Parse(site.urls[0])
	if err != nil {
		t.Fatalf("parse url: %v", err)
	}
	if u.Path != "/episodes/feed/" {
		t.Fatalf("path: %q", u.Path)
	}
	if u.RawQuery != "res=1080p&subs[]=us&subs[]=br&type=torrent&token=myrsskey" {
		t.Fatalf("query: %q", u.RawQuery)
	}
}

func TestEraiRawsQueryFilter(t *testing.T) {
	site := newEraiSite(t, map[string]string{"/feed/": eraiRSSFixture})
	e := newTestEraiRaws(t, site)

	q := models.NewTorznabQuery()
	q.QueryType = "search"
	q.SearchTerm = "Some Anime"
	res, err := e.ResultsForQuery(context.Background(), q, false)
	if err != nil {
		t.Fatalf("ResultsForQuery: %v", err)
	}
	if len(res.Releases) != 1 || res.Releases[0].Title != "Some Anime - 01.mkv - 1080p" {
		t.Fatalf("releases: %+v", res.Releases)
	}
}

func TestEraiRawsSubsInTitle(t *testing.T) {
	site := newEraiSite(t, map[string]string{"/feed/": eraiRSSFixture})
	e := newTestEraiRaws(t, site)
	e.cfg.GetDynamic("include-subs").(*configdata.BoolItem).Value = true

	res, err := e.ResultsForQuery(context.Background(), &models.TorznabQuery{QueryType: "search"}, false)
	if err != nil {
		t.Fatalf("ResultsForQuery: %v", err)
	}
	want := "Some Anime - 01.mkv - 1080p JAPANESE [Subs:  US  BR ]"
	if res.Releases[0].Title != want {
		t.Fatalf("title: %q want %q", res.Releases[0].Title, want)
	}
}

func TestEraiRaws403Error(t *testing.T) {
	site := newEraiSite(t, map[string]string{
		"/feed/": `<html><body><status>403</status></body></html>`,
	})
	e := newTestEraiRaws(t, site)
	_, err := e.ResultsForQuery(context.Background(), &models.TorznabQuery{QueryType: "search"}, false)
	if err == nil || !strings.Contains(err.Error(), "The RSSkey may need to be replaced as EraiRaws returned 403 Forbidden.") {
		t.Fatalf("err: %v", err)
	}
}

const eraiEmptyFeed = `<?xml version="1.0" encoding="UTF-8"?>
<rss version="2.0" xmlns:erai="https://www.erai-raws.info/rss-page/"><channel></channel></rss>`

func TestEraiRawsApplyConfiguration(t *testing.T) {
	site := newEraiSite(t, map[string]string{"/feed/": eraiRSSFixture})
	e := newTestEraiRaws(t, site)
	status, err := e.ApplyConfiguration(context.Background(), []indexer.ConfigItem{
		{ID: "sitelink", Value: site.srv.URL + "/"},
		{ID: "rsskey", Value: "myrsskey"},
	})
	if err != nil {
		t.Fatalf("ApplyConfiguration: %v", err)
	}
	if status != indexer.ConfigCompleted || !e.IsConfigured() {
		t.Fatalf("status: %v configured: %v", status, e.IsConfigured())
	}

	siteEmpty := newEraiSite(t, map[string]string{"/feed/": eraiEmptyFeed})
	e2 := newTestEraiRaws(t, siteEmpty)
	_, err = e2.ApplyConfiguration(context.Background(), []indexer.ConfigItem{
		{ID: "sitelink", Value: siteEmpty.srv.URL + "/"},
		{ID: "rsskey", Value: "myrsskey"},
	})
	if err == nil || err.Error() != "Could not find releases from this URL" {
		t.Fatalf("err: %v", err)
	}
	if e2.IsConfigured() {
		t.Fatal("must not be configured after failure")
	}
}

func TestEraiRawsTitleDetailParsing(t *testing.T) {
	site := newEraiSite(t, map[string]string{"/feed/": eraiRSSFixture})
	e := newTestEraiRaws(t, site)
	e.cfg.GetDynamic("title-detail-parsing").(*configdata.BoolItem).Value = true

	res, err := e.ResultsForQuery(context.Background(), &models.TorznabQuery{QueryType: "search"}, false)
	if err != nil {
		t.Fatalf("ResultsForQuery: %v", err)
	}
	// "[Erai-Raws] Some Anime - 01 [1080p].mkv" → stripped "Some Anime - 01.mkv";
	// the episode regex needs a trailing space after the digits, so the
	// ".mkv" suffix prevents detail detection (upstream-faithful): the empty
	// identifier is spliced in at the last hyphen.
	if got := res.Releases[0].Title; got != "Some Anime -  01.mkv - 1080p" {
		t.Fatalf("title: %q", got)
	}
}

func TestEraiTitleParser(t *testing.T) {
	cases := []struct {
		title string
		want  string
	}{
		{"Some Anime 2nd Season - 05", "Some Anime - S2E05"},
		{"Some Anime Season 2 - 05", "Some Anime - S2E05"},
		{"Some Anime Part 2 - 07", "Some Anime - S2E07"},
		{"Some Anime - 13", "Some Anime - E13"},
		// upstream quirk: both " - " separators survive the stripping, so the
		// identifier lands between a doubled hyphen
		{"Some Anime 2 - 13 - 1080p", "Some Anime - - S2E13 1080p"},
		{"No Details Here", "No Details Here"},
	}
	for _, tc := range cases {
		if got := (eraiTitleParser{}).Parse(tc.title); got != tc.want {
			t.Fatalf("Parse(%q) = %q want %q", tc.title, got, tc.want)
		}
	}
}

func TestStripEraiTitle(t *testing.T) {
	cases := map[string]string{
		// upstream only strips the bracketed suffix; ".mkv" stays
		"[Erai-Raws] Some Anime - 01 [1080p].mkv": "Some Anime - 01.mkv",
		"[Group] Title [720p]":                    "Title",
		"Plain Title":                             "Plain Title",
	}
	for in, want := range cases {
		if got := stripEraiTitle(in); got != want {
			t.Fatalf("stripEraiTitle(%q) = %q want %q", in, got, want)
		}
	}
}
