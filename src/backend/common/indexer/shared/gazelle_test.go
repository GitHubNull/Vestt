package shared

import (
	"context"
	"fmt"
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
	"vestt/common/indexer/configdata"
	"vestt/common/models"
)

func testClient(t *testing.T) *httpclient.Client {
	t.Helper()
	c, err := httpclient.New(httpclient.Config{}, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("httpclient.New: %v", err)
	}
	return c
}

func newGazelle(site string, opts GazelleOptions, caps *models.Capabilities) *Gazelle {
	g := &Gazelle{
		ImdbInTags: opts.ImdbInTags,
		UseAPIKey:  opts.UseAPIKey,
		UsePassKey: opts.UsePassKey,
		UseAuthKey: opts.UseAuthKey,
	}
	g.IDValue = "gazelletest"
	g.NameValue = "Gazelle Test"
	g.SiteLinkValue = site
	g.DefaultSiteLink = site
	g.LangValue = "en-US"
	g.TypeValue = "private"
	g.Caps = caps
	g.Bind(NewGazelleConfig(opts))
	return g
}

func gazelleTestCaps() *models.Capabilities {
	caps := models.NewCapabilities()
	caps.MovieSearchParams = []string{models.MovieParamQ, models.MovieParamGenre}
	caps.MusicSearchParams = []string{
		models.MusicParamQ, models.MusicParamAlbum, models.MusicParamArtist,
		models.MusicParamLabel, models.MusicParamYear, models.MusicParamGenre,
	}
	caps.Categories.AddCategoryMapping("1", models.CatAudio, "Music")
	caps.Categories.AddCategoryMapping("2", models.CatPC, "Applications")
	caps.Categories.AddCategoryMapping("3", models.CatBooks, "E-Books")
	caps.Categories.AddCategoryMapping("4", models.CatAudioAudiobook, "Audiobooks")
	return caps
}

const gazelleBrowseFixture = `{
  "status": "success",
  "response": {
    "results": [
      {
        "groupId": 100,
        "groupName": "Test Album",
        "artist": "Test Artist",
        "cover": "static/common/covers/100.jpg",
        "tags": ["rock", "2020s"],
        "groupTime": "1700000000",
        "groupYear": "2020",
        "releaseType": "Album",
        "torrents": [
          {
            "torrentId": 1001,
            "time": "2023-11-14 22:13:20",
            "size": 314572800,
            "seeders": 5,
            "leechers": 2,
            "snatches": 10,
            "fileCount": 8,
            "format": "FLAC",
            "encoding": "Lossless",
            "hasLog": false,
            "hasCue": true,
            "media": "CD",
            "category": "Music",
            "isFreeleech": false,
            "isPersonalFreeleech": false,
            "isNeutralLeech": false,
            "isFreeload": ""
          },
          {
            "torrentId": 1002,
            "time": "2023-11-15 08:00:00",
            "size": 104857600,
            "seeders": 1,
            "leechers": 0,
            "snatches": 3,
            "fileCount": 4,
            "format": "MP3",
            "encoding": "320",
            "media": "WEB",
            "category": "Music",
            "isFreeleech": true,
            "isPersonalFreeleech": false,
            "isNeutralLeech": false
          }
        ]
      },
      {
        "groupId": 200,
        "groupName": "Lone Release",
        "artist": "",
        "cover": "https://img.example/cover.png",
        "tags": [],
        "groupTime": "2023-05-01 10:00:00",
        "groupYear": "0",
        "releaseType": "Unknown",
        "torrentId": 2001,
        "time": "2023-05-01 10:00:00",
        "size": 2048,
        "seeders": 0,
        "leechers": 0,
        "snatches": 0,
        "fileCount": 1,
        "category": "Select Category",
        "isFreeleech": false,
        "isPersonalFreeleech": false,
        "isNeutralLeech": false
      }
    ]
  }
}`

// gazelleSite is a fake Gazelle tracker site.
type gazelleSite struct {
	*httptest.Server

	mu             sync.Mutex
	loginPosts     int
	searchRequests int
	lastAuth       string
	seq            int
	sessions       map[string]bool // valid session cookie values
}

// ExpireSession invalidates all issued sessions (models expired
// server-side sessions).
func (s *gazelleSite) ExpireSession() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sessions = map[string]bool{}
}

func (s *gazelleSite) hasValidSession(cookieHeader string) bool {
	for sess := range s.sessions {
		if strings.Contains(cookieHeader, sess) {
			return true
		}
	}
	return false
}

func newGazelleSite(t *testing.T, loginOK bool) *gazelleSite {
	t.Helper()
	s := &gazelleSite{sessions: map[string]bool{"session=abc123": true}}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()
		switch r.URL.Path {
		case "/login.php":
			s.loginPosts++
			if r.Method != http.MethodPost {
				w.WriteHeader(http.StatusMethodNotAllowed)
				return
			}
			if err := r.ParseForm(); err != nil {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			if loginOK && r.PostForm.Get("username") == "okuser" && r.PostForm.Get("password") == "okpass" && r.PostForm.Get("keeplogged") == "1" {
				s.seq++
				sess := fmt.Sprintf("session=s%d", s.seq)
				s.sessions[sess] = true
				w.Header().Add("Set-Cookie", sess+"; path=/")
				http.Redirect(w, r, "/", http.StatusFound)
				return
			}
			w.Header().Set("Content-Type", "text/html")
			_, _ = fmt.Fprint(w, `<html><body><div id="loginform"><table><tr><td>form fields</td></tr></table>
	Invalid username or password.</div></body></html>`)
		case "/ajax.php":
			s.searchRequests++
			s.lastAuth = r.Header.Get("Authorization")
			// JSON for a valid session cookie or any API-key Authorization
			if !s.hasValidSession(r.Header.Get("Cookie")) && r.Header.Get("Authorization") == "" {
				// redirect to the login page renders HTML
				w.Header().Set("Content-Type", "text/html")
				_, _ = fmt.Fprint(w, `<html><body>login required</body></html>`)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprint(w, gazelleBrowseFixture)
		case "/":
			// the Go http stack cannot surface Set-Cookie headers of
			// intermediate redirect responses; the landing page refreshes the
			// newest session cookie (as real Gazelle index pages do)
			if s.seq > 0 {
				w.Header().Add("Set-Cookie", fmt.Sprintf("session=s%d; path=/", s.seq))
			}
			w.Header().Set("Content-Type", "text/html")
			_, _ = fmt.Fprint(w, `<html><body><a href="/logout.php">Log out</a></body></html>`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(s.Close)
	return s
}

func TestGazelleConfigJSON(t *testing.T) {
	cfg := NewGazelleConfig(GazelleOptions{
		Has2FA:                  true,
		UsePassKey:              true,
		SupportsFreeleechTokens: true,
		SupportsFreeleechOnly:   true,
		InstructionMessage:      "do things",
	})
	items, err := configdata.ItemsJSON(cfg)
	if err != nil {
		t.Fatalf("ItemsJSON: %v", err)
	}
	type item struct{ id, typ string }
	got := make([]item, 0, len(items))
	for _, m := range items {
		got = append(got, item{m["id"].(string), m["type"].(string)})
	}
	want := []item{
		{"sitelink", "inputstring"},
		{"username", "inputstring"},
		{"password", "password"},
		{"passkey", "inputstring"},
		{"cookiehint", "displayinfo"},
		{"cookie", "inputstring"},
		{"usefreeleechtokenswhenavailable", "inputbool"},
		{"searchfreeleechonly", "inputbool"},
		{"", "displayinfo"},
		{"tags", "inputtags"},
	}
	if fmt.Sprintf("%v", got) != fmt.Sprintf("%v", want) {
		t.Fatalf("config items = %v, want %v", got, want)
	}
	// API-key flavour replaces username/password with the APIKey item
	cfgKey := NewGazelleConfig(GazelleOptions{UseAPIKey: true})
	itemsKey, _ := configdata.ItemsJSON(cfgKey)
	if itemsKey[1]["id"] != "apikey" || len(itemsKey) != 4 { // sitelink, apikey, instructions, tags
		t.Fatalf("apikey config items = %v", itemsKey)
	}
}

func TestGazelleSearchURL(t *testing.T) {
	site := "https://gazelle.example/"
	caps := gazelleTestCaps()

	g := newGazelle(site, GazelleOptions{}, caps)
	q := models.NewTorznabQuery()
	q.SearchTerm = "Mr. Robot"
	q.Categories = []int{models.CatAudio.ID}
	url := g.SearchURL(q)
	// dots are replaced by spaces AFTER encoding: "Mr.+Robot" -> "Mr +Robot"
	if !strings.HasPrefix(url, site+"ajax.php?action=browse&order_by=time&order_way=desc&") {
		t.Fatalf("url = %s", url)
	}
	if !strings.Contains(url, "searchstr=Mr +Robot") {
		t.Fatalf("searchstr encoding = %s", url)
	}
	if !strings.Contains(url, "filter_cat[1]=1") {
		t.Fatalf("filter_cat missing: %s", url)
	}

	// imdb query (cataloguenumber) and music filters
	q2 := models.NewTorznabQuery()
	q2.ImdbID = "tt1234567"
	q2.Artist = "Some Artist"
	q2.Label = "Some Label"
	year := 2020
	q2.Year = &year
	q2.Album = "Some Album"
	g2 := newGazelle(site, GazelleOptions{}, caps)
	u2 := g2.SearchURL(q2)
	for _, part := range []string{
		"cataloguenumber=tt1234567", "artistname=Some+Artist",
		"recordlabel=Some+Label", "year=2020", "groupname=Some+Album",
	} {
		if !strings.Contains(u2, part) {
			t.Fatalf("%s missing in %s", part, u2)
		}
	}

	// imdbInTags flips cataloguenumber to taglist
	g3 := newGazelle(site, GazelleOptions{ImdbInTags: true}, caps)
	u3 := g3.SearchURL(q2)
	if !strings.Contains(u3, "taglist=tt1234567") || strings.Contains(u3, "cataloguenumber") {
		t.Fatalf("imdbInTags url = %s", u3)
	}

	// freeleech / freeload flags
	g4 := newGazelle(site, GazelleOptions{SupportsFreeleechOnly: true}, caps)
	g4.Config.FreeleechOnly.Value = true
	if u := g4.SearchURL(models.NewTorznabQuery()); !strings.Contains(u, "freetorrent=1") {
		t.Fatalf("freeleech url = %s", u)
	}
	g5 := newGazelle(site, GazelleOptions{SupportsFreeloadOnly: true}, caps)
	g5.Config.FreeloadOnly.Value = true
	if u := g5.SearchURL(models.NewTorznabQuery()); !strings.Contains(u, "freetorrent=4") {
		t.Fatalf("freeload url = %s", u)
	}
}

func TestGazelleLoginAndSearch(t *testing.T) {
	site := newGazelleSite(t, true)
	g := newGazelle(site.URL+"/", GazelleOptions{}, gazelleTestCaps())
	g.Client = testClient(t)

	status, err := g.ApplyConfiguration(context.Background(), []indexer.ConfigItem{
		{ID: "username", Value: "okuser"},
		{ID: "password", Value: "okpass"},
	})
	if err != nil {
		t.Fatalf("ApplyConfiguration: %v", err)
	}
	if status != indexer.ConfigRequiresTesting {
		t.Fatalf("status = %v, want RequiresTesting", status)
	}
	if !g.IsConfigured() {
		t.Fatal("expected configured after login")
	}
	if got := g.CookieHeader(); !strings.HasPrefix(got, "session=s1") {
		t.Fatalf("CookieHeader = %q", got)
	}

	// full search round trip
	res, err := g.ResultsForQuery(context.Background(), &models.TorznabQuery{
		QueryType: "search", SearchTerm: "album", Cache: true,
	}, false)
	if err != nil {
		t.Fatalf("ResultsForQuery: %v", err)
	}
	releases := res.Releases
	if len(releases) != 3 {
		t.Fatalf("got %d releases, want 3", len(releases))
	}

	// sorted by PublishDate descending: 1002 (11-15), 1001 (11-14), 2001 (05-01)
	first := releases[0]
	if first.Title != "Test Artist - Test Album [2020] [Album] MP3 / 320 / WEB" {
		t.Fatalf("title[0] = %q", first.Title)
	}
	if first.PublishDate.Format("2006-01-02 15:04:05") != "2023-11-15 08:00:00" {
		t.Fatalf("date[0] = %v", first.PublishDate)
	}
	if first.Size == nil || *first.Size != 104857600 {
		t.Fatalf("size[0] = %v", first.Size)
	}
	if first.Seeders == nil || *first.Seeders != 1 || first.Peers == nil || *first.Peers != 1 {
		t.Fatalf("seeders/peers[0] = %v/%v", first.Seeders, first.Peers)
	}
	// freeleech -> DownloadVolumeFactor 0 -> no usetoken in the download link
	if first.DownloadVolumeFactor == nil || *first.DownloadVolumeFactor != 0 {
		t.Fatalf("dvf[0] = %v", first.DownloadVolumeFactor)
	}
	if first.Link == nil || !strings.HasPrefix(first.Link.String(), site.URL+"/torrents.php?action=download&id=1002") {
		t.Fatalf("link[0] = %v", first.Link)
	}
	if first.Details == nil || first.Details.String() != site.URL+"/torrents.php?torrentid=1002" {
		t.Fatalf("details[0] = %v", first.Details)
	}
	if first.Category == nil {
		t.Fatal("category[0] nil")
	}
	found := false
	for _, c := range first.Category {
		if c == models.CatAudio.ID {
			found = true
		}
	}
	if !found {
		t.Fatalf("category[0] = %v, want 3000 included", first.Category)
	}

	second := releases[1]
	if second.Title != "Test Artist - Test Album [2020] [Album] FLAC / Lossless / Cue / CD" {
		t.Fatalf("title[1] = %q", second.Title)
	}
	if second.Description != "Tags: rock, 2020s\n" {
		t.Fatalf("description[1] = %q", second.Description)
	}
	if len(second.Genres) != 2 || second.Genres[0] != "rock" {
		t.Fatalf("genres[1] = %v", second.Genres)
	}
	if second.Poster == nil || second.Poster.String() != site.URL+"/static/common/covers/100.jpg" {
		t.Fatalf("poster[1] = %v", second.Poster)
	}
	if second.Files == nil || *second.Files != 8 || second.Grabs == nil || *second.Grabs != 10 {
		t.Fatalf("files/grabs[1] = %v/%v", second.Files, second.Grabs)
	}
	if second.Link == nil || !strings.Contains(second.Link.String(), "id=1001") {
		t.Fatalf("link[1] = %v", second.Link)
	}

	// single-torrent group (no torrents array): "Select Category" maps to cat "1"
	third := releases[2]
	if third.Title != "Lone Release" {
		t.Fatalf("title[2] = %q", third.Title)
	}
	found = false
	for _, c := range third.Category {
		if c == models.CatAudio.ID {
			found = true
		}
	}
	if !found {
		t.Fatalf("category[2] = %v, want 3000 included", third.Category)
	}
	if third.Poster == nil || third.Poster.String() != "https://img.example/cover.png" {
		t.Fatalf("poster[2] = %v", third.Poster)
	}
}

func TestGazelleLoginError(t *testing.T) {
	site := newGazelleSite(t, false)
	g := newGazelle(site.URL+"/", GazelleOptions{}, gazelleTestCaps())
	g.Client = testClient(t)

	_, err := g.ApplyConfiguration(context.Background(), []indexer.ConfigItem{
		{ID: "username", Value: "okuser"},
		{ID: "password", Value: "wrong"},
	})
	if err == nil || err.Error() != "Invalid username or password." {
		t.Fatalf("err = %v", err)
	}
	if g.IsConfigured() {
		t.Fatal("must not be configured after failed login")
	}

	// page without #loginform and without logout.php
	site2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, `<html><body>totally unexpected</body></html>`)
	}))
	t.Cleanup(site2.Close)
	g2 := newGazelle(site2.URL+"/", GazelleOptions{}, gazelleTestCaps())
	g2.Client = testClient(t)
	_, err = g2.ApplyConfiguration(context.Background(), []indexer.ConfigItem{
		{ID: "username", Value: "okuser"},
		{ID: "password", Value: "okpass"},
	})
	if err == nil || err.Error() != "Unexpected response during login, see log for HTML response." {
		t.Fatalf("err = %v", err)
	}
}

func TestGazelleCookieLogin(t *testing.T) {
	site := newGazelleSite(t, true)
	g := newGazelle(site.URL+"/", GazelleOptions{Has2FA: true}, gazelleTestCaps())
	g.Client = testClient(t)

	status, err := g.ApplyConfiguration(context.Background(), []indexer.ConfigItem{
		{ID: "cookie", Value: "session=abc123"},
	})
	if err != nil {
		t.Fatalf("ApplyConfiguration: %v", err)
	}
	if status != indexer.ConfigCompleted {
		t.Fatalf("status = %v, want Completed", status)
	}
	if !g.IsConfigured() {
		t.Fatal("expected configured")
	}

	// bad cookie: site returns the HTML login page for unknown cookies
	g2 := newGazelle(site.URL+"/", GazelleOptions{Has2FA: true}, gazelleTestCaps())
	g2.Client = testClient(t)
	_, err = g2.ApplyConfiguration(context.Background(), []indexer.ConfigItem{
		{ID: "cookie", Value: "session=WRONG"},
	})
	if err == nil || !strings.Contains(err.Error(), "Your cookie did not work:") {
		t.Fatalf("err = %v", err)
	}
}

func TestGazelleAPIKey(t *testing.T) {
	site := newGazelleSite(t, true)
	key := strings.Repeat("a", 41)

	g := newGazelle(site.URL+"/", GazelleOptions{UseAPIKey: true}, gazelleTestCaps())
	g.Client = testClient(t)
	status, err := g.ApplyConfiguration(context.Background(), []indexer.ConfigItem{
		{ID: "apikey", Value: key},
	})
	if err != nil {
		t.Fatalf("ApplyConfiguration: %v", err)
	}
	if status != indexer.ConfigCompleted || !g.IsConfigured() {
		t.Fatalf("status=%v configured=%v", status, g.IsConfigured())
	}
	if site.lastAuth != key {
		t.Fatalf("Authorization = %q, want the raw API key (format {0})", site.lastAuth)
	}

	// wrong length
	g2 := newGazelle(site.URL+"/", GazelleOptions{UseAPIKey: true}, gazelleTestCaps())
	g2.Client = testClient(t)
	_, err = g2.ApplyConfiguration(context.Background(), []indexer.ConfigItem{
		{ID: "apikey", Value: strings.Repeat("a", 40)},
	})
	if err == nil || err.Error() != "Invalid API Key configured: expected length: 41, got 40" {
		t.Fatalf("err = %v", err)
	}

	// legacy length accepted
	g3 := newGazelle(site.URL+"/", GazelleOptions{UseAPIKey: true}, gazelleTestCaps())
	g3.APIKeyLengthLegacy = 32
	g3.Client = testClient(t)
	_, err = g3.ApplyConfiguration(context.Background(), []indexer.ConfigItem{
		{ID: "apikey", Value: strings.Repeat("b", 32)},
	})
	if err != nil {
		t.Fatalf("legacy key err = %v", err)
	}

	// API failure JSON surfaces the error reason
	fail := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"status":"failure","error":"bad key"}`)
	}))
	t.Cleanup(fail.Close)
	g4 := newGazelle(fail.URL+"/", GazelleOptions{UseAPIKey: true}, gazelleTestCaps())
	g4.Client = testClient(t)
	_, err = g4.PerformQuery(context.Background(), models.NewTorznabQuery())
	if err == nil || err.Error() != `Unexpected response from indexer request: "bad key"` {
		t.Fatalf("err = %v", err)
	}
}

func TestGazelleDownloadTokenFlip(t *testing.T) {
	var mu sync.Mutex
	var withoutTokenHits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if r.URL.Query().Get("usetoken") == "1" {
			w.Header().Set("Content-Type", "text/html")
			_, _ = fmt.Fprint(w, "<html>You do not have any freeleech tokens left.</html>")
			return
		}
		withoutTokenHits++
		_, _ = w.Write([]byte("d8:announce"))
	}))
	t.Cleanup(srv.Close)

	g := newGazelle(srv.URL+"/", GazelleOptions{SupportsFreeleechTokens: true}, gazelleTestCaps())
	g.Config.UseToken.Value = true
	g.Client = testClient(t)

	link, _ := url.Parse(srv.URL + "/torrents.php?action=download&usetoken=1&id=42")
	content, err := g.Download(context.Background(), link)
	if err != nil {
		t.Fatalf("Download: %v", err)
	}
	if string(content) != "d8:announce" {
		t.Fatalf("content = %q", content)
	}
	mu.Lock()
	if withoutTokenHits != 1 {
		t.Fatalf("withoutTokenHits = %d", withoutTokenHits)
	}
	mu.Unlock()

	// happy path: torrent content returned as-is even with usetoken=1
	link2, _ := url.Parse(srv.URL + "/torrents.php?action=download&id=43")
	content, err = g.Download(context.Background(), link2)
	if err != nil || string(content) != "d8:announce" {
		t.Fatalf("content = %q err = %v", content, err)
	}
}

func TestGazellePasskeyDownloadURL(t *testing.T) {
	g := newGazelle("https://gazelle.example/", GazelleOptions{
		UsePassKey:              true,
		SupportsFreeleechTokens: true,
	}, gazelleTestCaps())
	g.Config.Passkey.Value = "PK123"
	g.Config.UseToken.Value = true

	got := g.getDownloadURL(99, true)
	want := "https://gazelle.example/torrents.php?action=download&usetoken=1&torrent_pass=PK123&id=99"
	if got != want {
		t.Fatalf("url = %q want %q", got, want)
	}
	got = g.getDownloadURL(99, false)
	want = "https://gazelle.example/torrents.php?action=download&torrent_pass=PK123&id=99"
	if got != want {
		t.Fatalf("url = %q want %q", got, want)
	}

	g2 := newGazelle("https://gazelle.example/", GazelleOptions{UseAuthKey: true}, gazelleTestCaps())
	g2.Config.Authkey.Value = "AK"
	if got := g2.getDownloadURL(7, true); got != "https://gazelle.example/torrents.php?action=download&authkey=AK&id=7" {
		t.Fatalf("url = %q", got)
	}
}

func TestGazelleReLoginOnHTML(t *testing.T) {
	site := newGazelleSite(t, true)
	g := newGazelle(site.URL+"/", GazelleOptions{}, gazelleTestCaps())
	g.Client = testClient(t)

	if _, err := g.ApplyConfiguration(context.Background(), []indexer.ConfigItem{
		{ID: "username", Value: "okuser"},
		{ID: "password", Value: "okpass"},
	}); err != nil {
		t.Fatalf("login: %v", err)
	}
	loginsAfterConfig := site.loginPosts

	// expire the session server-side: the stale cookie gets the HTML login
	// page and PerformQuery must re-login and retry
	site.ExpireSession()
	if _, err := g.ResultsForQuery(context.Background(), &models.TorznabQuery{
		QueryType: "search", SearchTerm: "x", Cache: true,
	}, false); err != nil {
		t.Fatalf("search after session expiry: %v", err)
	}
	if site.loginPosts != loginsAfterConfig+1 {
		t.Fatalf("loginPosts = %d, want %d", site.loginPosts, loginsAfterConfig+1)
	}
}

func TestGazelleHooks(t *testing.T) {
	// GetSearchTerm / GetInfoURL / ReleaseInfoPostParse / ShouldSkipRelease overrides
	g := newGazelle("https://gazelle.example/", GazelleOptions{}, gazelleTestCaps())
	g.GetSearchTerm = func(q *models.TorznabQuery) string { return "hooked" }
	q := models.NewTorznabQuery()
	q.SearchTerm = "ignored"
	if u := g.SearchURL(q); !strings.Contains(u, "searchstr=hooked") {
		t.Fatalf("url = %s", u)
	}

	g.GetInfoURL = func(id int, gid *int) string {
		return fmt.Sprintf("https://custom.example/t/%d", id)
	}
	g.ReleaseInfoPostParse = func(r *models.ReleaseInfo, torrent, result map[string]any) bool {
		return false
	}
	g.ShouldSkipRelease = func(torrent map[string]any) bool { return true }

	releases, err := g.parseBrowse([]byte(gazelleBrowseFixture))
	if err != nil {
		t.Fatalf("parseBrowse: %v", err)
	}
	if len(releases) != 0 {
		t.Fatalf("hooks should exclude every release, got %d", len(releases))
	}
}
