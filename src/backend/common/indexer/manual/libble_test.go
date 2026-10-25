package manual

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	"vestt/common/httpclient"
	"vestt/common/indexer"
	"vestt/common/models"
	"vestt/common/services"
)

type libbleSite struct {
	srv   *httptest.Server
	mu    sync.Mutex
	hits  []string
	serve map[string]string
}

func newLibbleSite(t *testing.T, serve map[string]string) *libbleSite {
	t.Helper()
	s := &libbleSite{serve: serve}
	s.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		s.hits = append(s.hits, r.Method+" "+r.URL.RequestURI())
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

func (s *libbleSite) paths() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.hits...)
}

func newTestLibble(t *testing.T, site *libbleSite) *Libble {
	t.Helper()
	client, err := httpclient.New(httpclient.Config{}, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("httpclient.New: %v", err)
	}
	ix, err := NewLibble(services.Deps{Client: client})
	if err != nil {
		t.Fatalf("NewLibble: %v", err)
	}
	l := ix.(*Libble)
	l.SiteLinkValue = site.srv.URL + "/"
	client.SetRequestDelay(l.ID(), 0)
	return l
}

const libbleLoginOK = `<html><body><a href="logout.php">Logout</a></body></html>`

const libbleLoginFailed = `<html><body>
<form id="loginform"><div class="warning">Invalid username or password.</div></form>
</body></html>`

// Gazelle-style torrent table: one group row + two torrent rows.
const libbleSearchPage = `<html><body>
<table id="torrent_table"><tbody>
<tr class="group">
  <td class="cats_col"><div class="cats_music"></div></td>
  <td>
    <strong><a href="torrents.php?id=51694">In Love and Death</a> <a href="artist.php?id=101">The Used</a> [2004]</strong>
    <div class="tags">rock, alternative</div>
    <div class="thumbnail" title="https://libble.me/covers/51694.jpg"></div>
  </td>
</tr>
<tr class="group_torrent groupid_51694">
  <td><span>▶</span><span>MP3 / 320 / Freeleech!</span></td>
  <td>12</td>
  <td><span title="Aug 31 2020, 15:50">3 years ago</span></td>
  <td>105.2 MB</td>
  <td>42</td>
  <td>5</td>
  <td>2</td>
  <td><a href="torrents.php?id=51694&amp;torrentid=89758">info</a></td>
  <td><a href="torrents.php?action=download&amp;id=89758">DL</a></td>
</tr>
<tr class="group_torrent groupid_51694">
  <td><span>▶</span><span>FLAC / Lossless / Neutral!</span></td>
  <td>14</td>
  <td><span title="Sep 01 2020, 09:15">3 years ago</span></td>
  <td>410 MB</td>
  <td>7</td>
  <td>1</td>
  <td>0</td>
  <td><a href="torrents.php?id=51694&amp;torrentid=89759">info</a></td>
  <td><a href="torrents.php?action=download&amp;id=89759">DL</a></td>
</tr>
</tbody></table>
</body></html>`

func TestLibbleCaps(t *testing.T) {
	ix, err := NewLibble(services.Deps{})
	if err != nil {
		t.Fatalf("NewLibble: %v", err)
	}
	caps := ix.Capabilities()
	wantParams := []string{
		models.MusicParamQ, models.MusicParamAlbum, models.MusicParamArtist,
		models.MusicParamLabel, models.MusicParamYear, models.MusicParamGenre,
	}
	if strings.Join(caps.MusicSearchParams, ",") != strings.Join(wantParams, ",") {
		t.Fatalf("MusicSearchParams: %v", caps.MusicSearchParams)
	}
	for _, tc := range []struct {
		tracker string
		want    int
	}{
		{"1", 3000}, {"2", 3000}, {"7", 3020},
	} {
		got := caps.Categories.MapTrackerCatToNewznab(tc.tracker)
		found := false
		for _, id := range got {
			if id == tc.want {
				found = true
			}
		}
		if !found {
			t.Fatalf("MapTrackerCatToNewznab(%q) = %v, want it to contain %d", tc.tracker, got, tc.want)
		}
	}
}

func TestLibbleConfigForm(t *testing.T) {
	ix, err := NewLibble(services.Deps{})
	if err != nil {
		t.Fatalf("NewLibble: %v", err)
	}
	items, err := ix.ConfigurationForSetup(context.Background())
	if err != nil {
		t.Fatalf("ConfigurationForSetup: %v", err)
	}
	var ids []string
	for _, it := range items {
		ids = append(ids, it["id"].(string))
	}
	// sitelink, username, password, two-factorauth, instructions(""),
	// searchfreeleechonly, accountinactivity, tags (hidden items excluded).
	want := []string{"sitelink", "username", "password", "two-factorauth", "", "searchfreeleechonly", "accountinactivity", "tags"}
	if strings.Join(ids, "|") != strings.Join(want, "|") {
		t.Fatalf("config ids: %v", ids)
	}
}

func TestLibbleApplyConfigurationOK(t *testing.T) {
	site := newLibbleSite(t, map[string]string{
		"/login.php":    libbleLoginOK,
		"/torrents.php": libbleSearchPage,
	})
	l := newTestLibble(t, site)

	status, err := l.ApplyConfiguration(context.Background(), []indexer.ConfigItem{
		{ID: "sitelink", Value: site.srv.URL + "/"},
		{ID: "username", Value: "user"},
		{ID: "password", Value: "pass"},
		{ID: "twofactorauth", Value: "123456"},
	})
	if err != nil {
		t.Fatalf("ApplyConfiguration: %v", err)
	}
	if status != indexer.ConfigRequiresTesting {
		t.Fatalf("status: %v, want ConfigRequiresTesting", status)
	}
	if !l.IsConfigured() {
		t.Fatal("must be configured after successful login")
	}
	if l.SiteLink() != site.srv.URL+"/" {
		t.Fatalf("site link: %q", l.SiteLink())
	}
	// the login POST carries the upstream field set
	if len(site.paths()) == 0 || !strings.HasPrefix(site.paths()[0], "POST /login.php") {
		t.Fatalf("hits: %v", site.paths())
	}
}

func TestLibbleApplyConfigurationLoginFailed(t *testing.T) {
	site := newLibbleSite(t, map[string]string{
		"/login.php": libbleLoginFailed,
	})
	l := newTestLibble(t, site)

	_, err := l.ApplyConfiguration(context.Background(), []indexer.ConfigItem{
		{ID: "sitelink", Value: site.srv.URL + "/"},
		{ID: "username", Value: "user"},
		{ID: "password", Value: "wrong"},
		{ID: "twofactorauth", Value: "000000"},
	})
	if err == nil || !strings.Contains(err.Error(), "Invalid username or password.") {
		t.Fatalf("err: %v", err)
	}
	if l.IsConfigured() {
		t.Fatal("must not be configured after failed login")
	}
}

func TestLibbleSearchRequestAndParse(t *testing.T) {
	site := newLibbleSite(t, map[string]string{
		"/torrents.php": libbleSearchPage,
	})
	l := newTestLibble(t, site)
	l.cfg.Freeleech.Value = true

	year := 2004
	q := models.NewTorznabQuery()
	q.QueryType = "search"
	q.SearchTerm = "the used"
	q.Artist = "The Used"
	q.Year = &year
	q.Genre = "rock"
	q.Categories = []int{3000}
	q.Limit = 50
	q.Offset = 100 // upstream pagination: page = offset/limit + 1 = 3

	res, err := l.ResultsForQuery(context.Background(), q, false)
	if err != nil {
		t.Fatalf("ResultsForQuery: %v", err)
	}
	if len(res.Releases) != 2 {
		t.Fatalf("releases: %d", len(res.Releases))
	}

	paths := site.paths()
	if len(paths) != 1 {
		t.Fatalf("hits: %v", paths)
	}
	req := paths[0]
	// query category 3000 (Audio) expands to its children: tracker cats
	// 1+2 (Audio) and 7 (AudioVideo) all filter.
	for _, fragment := range []string{
		"GET /torrents.php?",
		"order_by=time", "order_way=desc", "freetorrent=1",
		"searchstr=the+used", "artistname=The+Used", "year=2004",
		"taglist=rock", "tags_type=0",
		"filter_cat[1]=1", "filter_cat[2]=1", "filter_cat[7]=1", "page=3",
	} {
		if !strings.Contains(req, fragment) {
			t.Fatalf("request %q missing %q", req, fragment)
		}
	}

	r := res.Releases[0]
	if r.Title != "The Used - In Love and Death [2004] MP3 / 320" {
		t.Fatalf("title: %q", r.Title)
	}
	if r.Link == nil || !strings.HasSuffix(r.Link.Path, "torrents.php") ||
		r.Link.Query().Get("action") != "download" || r.Link.Query().Get("id") != "89758" {
		t.Fatalf("link: %v", r.Link)
	}
	if r.Guid == nil || r.Guid.String() != r.Link.String() {
		t.Fatalf("guid: %v (link %v)", r.Guid, r.Link)
	}
	if r.Details == nil || !strings.Contains(r.Details.RawQuery, "id=51694") {
		t.Fatalf("details: %v", r.Details)
	}
	if r.PublishDate.Format("2006-01-02 15:04") != "2020-08-31 15:50" {
		t.Fatalf("publish date: %v", r.PublishDate)
	}
	if r.Files == nil || *r.Files != 12 {
		t.Fatalf("files: %v", r.Files)
	}
	if r.Grabs == nil || *r.Grabs != 42 {
		t.Fatalf("grabs: %v", r.Grabs)
	}
	if r.Seeders == nil || *r.Seeders != 5 {
		t.Fatalf("seeders: %v", r.Seeders)
	}
	if r.Peers == nil || *r.Peers != 7 {
		t.Fatalf("peers: %v", r.Peers)
	}
	if r.Size == nil || *r.Size != 105*1024*1024+2*1024*1024/10 {
		t.Fatalf("size: %v", r.Size)
	}
	found := false
	for _, id := range r.Category {
		if id == 3000 {
			found = true
		}
	}
	if !found {
		t.Fatalf("category: %v, want it to contain 3000", r.Category)
	}
	if r.DownloadVolumeFactor == nil || *r.DownloadVolumeFactor != 0 {
		t.Fatalf("dvf (Freeleech! tag): %v", r.DownloadVolumeFactor)
	}
	if r.UploadVolumeFactor == nil || *r.UploadVolumeFactor != 1 {
		t.Fatalf("uvf (Freeleech! tag): %v", r.UploadVolumeFactor)
	}
	if r.Description != "rock,alternative" {
		t.Fatalf("description: %q", r.Description)
	}
	if len(r.Genres) != 2 || r.Genres[0] != "rock" || r.Genres[1] != "alternative" {
		t.Fatalf("genres: %v", r.Genres)
	}
	if r.Poster == nil || r.Poster.String() != "https://libble.me/covers/51694.jpg" {
		t.Fatalf("poster: %v", r.Poster)
	}
	if r.MinimumSeedTime == nil || *r.MinimumSeedTime != 259200 {
		t.Fatalf("minimum seed time: %v", r.MinimumSeedTime)
	}

	// second row carries the Neutral! tag (dvf 0 / uvf 0)
	r2 := res.Releases[1]
	if r2.Title != "The Used - In Love and Death [2004] FLAC / Lossless" {
		t.Fatalf("title 2: %q", r2.Title)
	}
	if r2.DownloadVolumeFactor == nil || *r2.DownloadVolumeFactor != 0 ||
		r2.UploadVolumeFactor == nil || *r2.UploadVolumeFactor != 0 {
		t.Fatalf("volume factors 2: %v/%v", r2.DownloadVolumeFactor, r2.UploadVolumeFactor)
	}
	if r2.Peers == nil || *r2.Peers != 1 {
		t.Fatalf("peers 2: %v", r2.Peers)
	}
}

func TestLibbleVariousArtistsTitle(t *testing.T) {
	page := strings.Replace(libbleSearchPage,
		`<a href="artist.php?id=101">The Used</a> [2004]`, `[2004]`, 1)
	site := newLibbleSite(t, map[string]string{
		"/torrents.php": page,
	})
	l := newTestLibble(t, site)

	q := models.NewTorznabQuery()
	q.QueryType = "search"
	res, err := l.ResultsForQuery(context.Background(), q, false)
	if err != nil {
		t.Fatalf("ResultsForQuery: %v", err)
	}
	if len(res.Releases) != 2 {
		t.Fatalf("releases: %d", len(res.Releases))
	}
	if !strings.HasPrefix(res.Releases[0].Title, "Various Artists - In Love and Death [2004]") {
		t.Fatalf("title: %q", res.Releases[0].Title)
	}
}

func TestLibbleParseError(t *testing.T) {
	// group row without the .cats_col category node → upstream NRE path
	site := newLibbleSite(t, map[string]string{
		"/torrents.php": `<html><body><table id="torrent_table"><tbody>
			<tr class="group"><td><strong><a href="torrents.php?id=1">X</a></strong></td></tr>
			<tr class="group_torrent groupid_1"><td><span></span><span>MP3</span></td>
			<td>1</td><td><span title="Aug 31 2020, 15:50">x</span></td><td>1 MB</td>
			<td>1</td><td>1</td><td>1</td><td><a href="torrents.php?id=1&amp;torrentid=2">i</a></td>
			<td><a href="torrents.php?action=download&amp;id=2">DL</a></td></tr>
		</tbody></table></body></html>`,
	})
	l := newTestLibble(t, site)

	q := models.NewTorznabQuery()
	q.QueryType = "search"
	_, err := l.ResultsForQuery(context.Background(), q, false)
	if err == nil {
		t.Fatal("expected parse error")
	}
	var ie *indexer.IndexerError
	if !errors.As(err, &ie) || ie.Kind != indexer.KindParse {
		t.Fatalf("err: %v", err)
	}
}

func TestLibbleReleaseURLResolution(t *testing.T) {
	// absolute vs relative hrefs resolve against the site link
	site := newLibbleSite(t, map[string]string{
		"/torrents.php": libbleSearchPage,
	})
	l := newTestLibble(t, site)
	q := models.NewTorznabQuery()
	q.QueryType = "search"
	res, err := l.ResultsForQuery(context.Background(), q, false)
	if err != nil {
		t.Fatalf("ResultsForQuery: %v", err)
	}
	base, _ := url.Parse(site.srv.URL + "/")
	if !strings.HasPrefix(res.Releases[0].Link.String(), base.String()) {
		t.Fatalf("link not resolved against site link: %v", res.Releases[0].Link)
	}
}
