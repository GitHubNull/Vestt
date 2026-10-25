package manual

import (
	"context"
	"encoding/base64"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"vestt/common/httpclient"
	"vestt/common/indexer"
	"vestt/common/models"
	"vestt/common/services"
	"vestt/common/utils/parseutil"
)

type audiobookbaySite struct {
	srv   *httptest.Server
	mu    sync.Mutex
	hits  []string
	serve map[string]string
}

func newAudiobookbaySite(t *testing.T, serve map[string]string) *audiobookbaySite {
	t.Helper()
	s := &audiobookbaySite{serve: serve}
	s.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		s.hits = append(s.hits, r.URL.RequestURI())
		page, ok := s.serve[r.URL.Path]
		s.mu.Unlock()
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(page))
	}))
	t.Cleanup(s.srv.Close)
	return s
}

func (s *audiobookbaySite) paths() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.hits...)
}

func newTestAudiobookbay(t *testing.T, site *audiobookbaySite) *AudioBookBay {
	t.Helper()
	client, err := httpclient.New(httpclient.Config{}, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("httpclient.New: %v", err)
	}
	ix, err := NewAudioBookBay(services.Deps{Client: client})
	if err != nil {
		t.Fatalf("NewAudioBookBay: %v", err)
	}
	a := ix.(*AudioBookBay)
	a.SiteLinkValue = site.srv.URL + "/"
	// tests must not pay the upstream 6.1s delay
	client.SetRequestDelay(a.ID(), 0)
	return a
}

const audiobookbayPage1 = `<html><body>
<div class="post">
  <div class="postTitle"><h2><a href="/audio-books/some-book/">Some Book Title</a></h2></div>
  <div class="postContent">
Format: MP3 /
Bitrate: 128kbps
Posted: 2 Jan 2024
File Size: 650 MB
  </div>
  <div class="postInfo">Category: Fantasy&#160;Adventure</div>
  <img src="covers/1.jpg"/>
</div>
<div class="post">
  <div class="postTitle"><h2><a href="/audio-books/second-book">Second Book</a></h2></div>
  <div class="postContent">
Format: MP3 / Bitrate: 64kbpsFile Size: 1.5 GB
  </div>
  <div class="postInfo">Category: Sci-Fi</div>
</div>
</body></html>`

// The re-ab post body decodes to a normal post div content.
var audiobookbayHiddenPost = base64.StdEncoding.EncodeToString([]byte(
	`<div class="postTitle"><h2><a href="/audio-books/hidden-book/">Hidden Book</a></h2></div>` +
		`<div class="postContent">Posted: 3 Feb 2024&#10;File Size: 100 MB</div>` +
		`<div class="postInfo">Category: Mystery</div>`))

const audiobookbayPage2 = `<html><body>
<div class="post re-ab">` + "`HIDDEN`" + `</div>
</body></html>`

const audiobookbayDetailsPage = `<html><body>
<div class="postTitle"><h1>Some Book Title</h1></div>
<table><tr><td>Info Hash:</td><td>ABCDEF0123456789ABCDEF0123456789ABCDEF01</td></tr></table>
</body></html>`

func TestAudiobookbayCaps(t *testing.T) {
	a, err := NewAudioBookBay(services.Deps{})
	if err != nil {
		t.Fatalf("NewAudioBookBay: %v", err)
	}
	caps := a.Capabilities()
	if strings.Join(caps.BookSearchParams, ",") != "q" {
		t.Fatalf("BookSearchParams: %v", caps.BookSearchParams)
	}
	got := caps.Categories.MapTrackerCatToNewznab("1")
	if len(got) != 1 || got[0] != 3030 {
		t.Fatalf("MapTrackerCatToNewznab(1) = %v", got)
	}
}

func TestAudiobookbayIdentity(t *testing.T) {
	a, err := NewAudioBookBay(services.Deps{})
	if err != nil {
		t.Fatalf("NewAudioBookBay: %v", err)
	}
	if a.ID() != "audiobookbay" || a.Name() != "AudioBook Bay" || a.Language() != "en-US" || a.Type() != "public" {
		t.Fatalf("identity: %s %s %s %s", a.ID(), a.Name(), a.Language(), a.Type())
	}
	alts := a.AlternativeSiteLinks()
	if len(alts) != 6 || alts[0] != "https://audiobookbay.lu/" {
		t.Fatalf("alt links: %v", alts)
	}
}

func TestAudiobookbayConfigForm(t *testing.T) {
	a, err := NewAudioBookBay(services.Deps{})
	if err != nil {
		t.Fatalf("NewAudioBookBay: %v", err)
	}
	items, err := a.ConfigurationForSetup(context.Background())
	if err != nil {
		t.Fatalf("ConfigurationForSetup: %v", err)
	}
	var ids []string
	for _, it := range items {
		ids = append(ids, it["id"].(string))
	}
	// plain ConfigurationData: sitelink + tags only (hidden items excluded).
	if strings.Join(ids, ",") != "sitelink,tags" {
		t.Fatalf("config ids: %v", ids)
	}
}

func TestAudiobookbayLatestRequestAndParse(t *testing.T) {
	page2 := strings.Replace(audiobookbayPage2, "`HIDDEN`", audiobookbayHiddenPost, 1)
	site := newAudiobookbaySite(t, map[string]string{
		"/":        audiobookbayPage1,
		"/page/2/": page2,
	})
	a := newTestAudiobookbay(t, site)

	res, err := a.ResultsForQuery(context.Background(), &models.TorznabQuery{QueryType: "search", IsTest: true}, false)
	if err != nil {
		t.Fatalf("ResultsForQuery: %v", err)
	}
	if len(res.Releases) != 3 {
		t.Fatalf("releases: %d", len(res.Releases))
	}
	r := res.Releases[0]
	if r.Title != "Some Book Title [MP3]" {
		t.Fatalf("title: %q", r.Title)
	}
	if r.Size == nil || *r.Size != parseutil.GetBytes("650 MB") {
		t.Fatalf("size: %v", r.Size)
	}
	if r.PublishDate.Format("2006-01-02") != "2024-01-02" {
		t.Fatalf("publish date: %v", r.PublishDate)
	}
	if len(r.Category) != 1 || r.Category[0] != 3030 {
		t.Fatalf("category: %v", r.Category)
	}
	if r.Seeders == nil || *r.Seeders != 1 || r.Peers == nil || *r.Peers != 1 {
		t.Fatalf("seeders/peers: %v %v", r.Seeders, r.Peers)
	}
	if r.Details == nil || r.Details.String() != site.srv.URL+"/audio-books/some-book/" {
		t.Fatalf("details: %v", r.Details)
	}
	if r.Link != r.Details || r.Guid != r.Details {
		t.Fatal("link/guid must equal details")
	}
	if r.Poster == nil || r.Poster.String() != site.srv.URL+"/covers/1.jpg" {
		t.Fatalf("poster: %v", r.Poster)
	}
	if strings.Join(r.Genres, ",") != "Fantasy,Adventure" {
		t.Fatalf("genres: %v", r.Genres)
	}
	if r.DownloadVolumeFactor == nil || *r.DownloadVolumeFactor != 0 || r.UploadVolumeFactor == nil || *r.UploadVolumeFactor != 1 {
		t.Fatalf("dvf/uvf: %v %v", r.DownloadVolumeFactor, r.UploadVolumeFactor)
	}

	// second post: bitrate shares the line with "File" so it is appended
	r2 := res.Releases[1]
	if r2.Title != "Second Book [MP3] [64kbps]" {
		t.Fatalf("second title: %q", r2.Title)
	}
	if r2.Size == nil || *r2.Size != parseutil.GetBytes("1.5 GB") {
		t.Fatalf("second size: %v", r2.Size)
	}

	// third post comes from the base64 re-ab replacement on page 2
	r3 := res.Releases[2]
	if r3.Title != "Hidden Book" {
		t.Fatalf("hidden title: %q", r3.Title)
	}
	if r3.Size == nil || *r3.Size != parseutil.GetBytes("100 MB") {
		t.Fatalf("hidden size: %v", r3.Size)
	}
	if strings.Join(r3.Genres, ",") != "Mystery" {
		t.Fatalf("hidden genres: %v", r3.Genres)
	}

	paths := site.paths()
	if len(paths) != 2 || paths[0] != "/" || paths[1] != "/page/2/" {
		t.Fatalf("paths: %v", paths)
	}
}

func TestAudiobookbaySearchRequest(t *testing.T) {
	site := newAudiobookbaySite(t, map[string]string{
		"/":        audiobookbayPage1,
		"/page/2/": audiobookbayPage1,
	})
	a := newTestAudiobookbay(t, site)

	q := models.NewTorznabQuery()
	q.QueryType = "search"
	q.SearchTerm = "Some, Book!"
	res, err := a.ResultsForQuery(context.Background(), q, false)
	if err != nil {
		t.Fatalf("ResultsForQuery: %v", err)
	}
	_ = res
	paths := site.paths()
	if len(paths) != 2 || paths[0] != "/?s=some+book&tt=1" || paths[1] != "/page/2/?s=some+book&tt=1" {
		t.Fatalf("paths: %v", paths)
	}
}

func TestAudiobookbayDownloadBuildsMagnet(t *testing.T) {
	site := newAudiobookbaySite(t, map[string]string{
		"/audio-books/some-book/": audiobookbayDetailsPage,
	})
	a := newTestAudiobookbay(t, site)

	data, err := a.Download(context.Background(), anidubMustParseURL(t, site.srv.URL+"/audio-books/some-book/"))
	if err != nil {
		t.Fatalf("Download: %v", err)
	}
	magnet := string(data)
	if !strings.HasPrefix(magnet, "magnet:?xt=urn:btih:ABCDEF0123456789ABCDEF0123456789ABCDEF01&dn=Some+Book+Title&tr=") {
		t.Fatalf("magnet: %q", magnet)
	}
}

func TestAudiobookbayApplyConfigurationEmptyIsError(t *testing.T) {
	site := newAudiobookbaySite(t, map[string]string{
		"/":        `<html><body></body></html>`,
		"/page/2/": `<html><body></body></html>`,
	})
	a := newTestAudiobookbay(t, site)
	_, err := a.ApplyConfiguration(context.Background(), []indexer.ConfigItem{
		{ID: "sitelink", Value: site.srv.URL + "/"},
	})
	if err == nil || err.Error() != "Could not find releases from this URL" {
		t.Fatalf("err: %v", err)
	}
	if a.IsConfigured() {
		t.Fatal("must not be configured after failure")
	}
	if a.SiteLink() != site.srv.URL+"/" {
		t.Fatalf("site link rewritten to default: %q", a.SiteLink())
	}
}

func TestAudiobookbayCleanTitleStripsNonASCII(t *testing.T) {
	// upstream removes \x{0100}-\x{FFFF} only; Latin-1 é (U+00E9) survives.
	if got := audiobookbayCleanTitle("café au\x00lait \n\nBook"); got != "café aulait Book" {
		t.Fatalf("clean title: %q", got)
	}
}
