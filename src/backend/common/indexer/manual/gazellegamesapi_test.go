package manual

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
	"vestt/common/services"
)

const ggnSearchFixture = `{
  "status": "success",
  "response": {
    "2700": {
      "year": 2013,
      "Artists": [ {"name": "Windows"}, {"name": "Mac"} ],
      "Torrents": {
        "5555": {
          "ID": 5555,
          "ReleaseTitle": "Some Game: GOTY Edition",
          "FreeTorrent": "1",
          "CategoryID": "1",
          "Format": "ISO",
          "Encoding": "Raw",
          "Region": "PAL",
          "Language": "English",
          "Scene": "1",
          "Miscellaneous": "",
          "GameDOXType": "",
          "GameDOXVers": "",
          "Dupable": "0",
          "RemasterTitle": "",
          "RemasterYear": 0,
          "Time": "2023-11-14 22:13:20",
          "Size": "2147483648",
          "Snatched": 42,
          "Seeders": 12,
          "Leechers": 3,
          "FileCount": 5
        },
        "5556": {
          "ID": 5556,
          "ReleaseTitle": "Some Game: GOTY Edition",
          "FreeTorrent": "0",
          "CategoryID": "1",
          "Format": "ISO",
          "Encoding": "Raw",
          "Region": "PAL",
          "Language": "English",
          "Scene": "1",
          "Miscellaneous": "",
          "GameDOXType": "",
          "GameDOXVers": "",
          "Dupable": "1",
          "RemasterTitle": "GOTY",
          "RemasterYear": 2014,
          "Time": "2023-11-15 08:00:00",
          "Size": "1073741824",
          "Snatched": 7,
          "Seeders": 3,
          "Leechers": 1,
          "FileCount": 2
        }
      }
    }
  }
}`

// ggnBrowseFixture feeds the shared.Gazelle self-test
// (ajax.php?action=browse — see the indexer's 澄清记录).
const ggnBrowseFixture = `{
  "status": "success",
  "response": {
    "results": [
      {
        "groupId": 10,
        "groupName": "Browse Group",
        "artist": "",
        "cover": "",
        "tags": [],
        "groupTime": "1700000000",
        "groupYear": "0",
        "releaseType": "Unknown",
        "torrents": [
          {
            "torrentId": 88,
            "time": "2023-11-14 22:13:20",
            "size": 1024,
            "seeders": 1,
            "leechers": 0,
            "snatches": 2,
            "fileCount": 1,
            "category": "1",
            "isFreeleech": false,
            "isPersonalFreeleech": false,
            "isNeutralLeech": false
          }
        ]
      }
    ]
  }
}`

// ggnSite is a fake GazelleGames api.php site.
type ggnSite struct {
	*httptest.Server

	mu         sync.Mutex
	hits       []string
	lastAPIKey string
	fail       bool
}

func newGGNSite(t *testing.T) *ggnSite {
	t.Helper()
	s := &ggnSite{}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()
		s.hits = append(s.hits, r.URL.RequestURI())
		s.lastAPIKey = r.Header.Get("X-API-Key")
		switch r.URL.Path {
		case "/ajax.php":
			// the shared self-test browse (see the indexer's 澄清记录)
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprint(w, ggnBrowseFixture)
		case "/api.php":
			if r.URL.Query().Get("action") == "browse" {
				w.Header().Set("Content-Type", "application/json")
				_, _ = fmt.Fprint(w, ggnBrowseFixture)
				return
			}
			if s.fail {
				w.Header().Set("Content-Type", "application/json")
				_, _ = fmt.Fprint(w, `{"status":"failure","error":"bad key"}`)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprint(w, ggnSearchFixture)
		case "/torrents.php":
			if r.URL.Query().Get("usetoken") == "1" {
				w.Header().Set("Content-Type", "text/html")
				_, _ = fmt.Fprint(w, "<html>You do not have any freeleech tokens left.</html>")
				return
			}
			_, _ = w.Write([]byte("d8:announce"))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(s.Close)
	return s
}

func (s *ggnSite) paths() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.hits...)
}

func newTestGGN(t *testing.T, site *ggnSite) *GazelleGamesAPI {
	t.Helper()
	client, err := httpclient.New(httpclient.Config{}, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("httpclient.New: %v", err)
	}
	ix, err := NewGazelleGamesAPI(services.Deps{Client: client})
	if err != nil {
		t.Fatalf("NewGazelleGamesAPI: %v", err)
	}
	g := ix.(*GazelleGamesAPI)
	g.SiteLinkValue = site.URL + "/"
	g.DefaultSiteLink = site.URL + "/"
	return g
}

func TestGazelleGamesCaps(t *testing.T) {
	ix, err := NewGazelleGamesAPI(services.Deps{})
	if err != nil {
		t.Fatalf("NewGazelleGamesAPI: %v", err)
	}
	if ix.ID() != "gazellegamesapi" || ix.Name() != "GazelleGames (API)" || ix.Type() != "private" || ix.Language() != "en-US" {
		t.Fatalf("identity: %s / %s / %s / %s", ix.ID(), ix.Name(), ix.Type(), ix.Language())
	}
	caps := ix.Capabilities()
	for _, tc := range []struct {
		tracker string
		want    int
	}{
		{"Windows", 4050}, {"DOS", 4050}, {"Mac", 1090}, {"Xbox", 1040},
		{"Xbox 360", 1050}, {"Nintendo DS", 1010}, {"Wii", 1030}, {"Wii U", 1130},
		{"PlayStation 3", 1080}, {"PlayStation 4", 1180},
		{"PlayStation Portable", 1020}, {"PlayStation Vita", 1120},
		{"Android", 4070}, {"iOS", 4060},
		{"1", 4050}, {"2", 4010}, {"3", 7020}, {"4", 3050},
	} {
		got := caps.Categories.MapTrackerCatToNewznab(tc.tracker)
		// AddCategoryMapping with a desc also creates a custom 1:1 category
		// (id ≥ 100000) that rides along in the mapping — upstream parity.
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

func TestGazelleGamesConfigForm(t *testing.T) {
	ix, err := NewGazelleGamesAPI(services.Deps{})
	if err != nil {
		t.Fatalf("NewGazelleGamesAPI: %v", err)
	}
	g := ix.(*GazelleGamesAPI)
	items, err := g.ConfigurationForSetup(context.Background())
	if err != nil {
		t.Fatalf("ConfigurationForSetup: %v", err)
	}
	var ids []string
	for _, it := range items {
		ids = append(ids, it["id"].(string))
	}
	// the AddDynamic key is "searchgroupnames" (GetDynamic lookup, as
	// upstream) while the serialized item id derives from the item name
	want := []string{
		"sitelink", "apikey", "passkey", "authkey",
		"usefreeleechtokenswhenavailable", "searchfreeleechonly",
		"", "searchgroupnamesonly", "tags",
	}
	if strings.Join(ids, "|") != strings.Join(want, "|") {
		t.Fatalf("config ids = %v, want %v", ids, want)
	}
}

func TestGazelleGamesSearchURL(t *testing.T) {
	ix, err := NewGazelleGamesAPI(services.Deps{})
	if err != nil {
		t.Fatalf("NewGazelleGamesAPI: %v", err)
	}
	g := ix.(*GazelleGamesAPI)
	g.SiteLinkValue = "https://ggn.example/"

	q := models.NewTorznabQuery()
	q.SearchTerm = "Some.Game"
	u := g.searchURL(q)
	if !strings.HasPrefix(u, "https://ggn.example/api.php?request=search&search_type=torrents&order_by=time&order_way=desc&") {
		t.Fatalf("url = %s", u)
	}
	// dots are removed BEFORE encoding for this indexer (unlike the base browse)
	if !strings.Contains(u, "searchstr=Some+Game") {
		t.Fatalf("searchstr = %s", u)
	}

	// PC/Games expands to DOS + Windows (+ numeric "1" filtered out)
	q.Categories = []int{models.CatPCGames.ID}
	u = g.searchURL(q)
	if !strings.Contains(u, "artistcheck[0]=DOS") || !strings.Contains(u, "artistcheck[1]=Windows") {
		t.Fatalf("artistcheck missing: %s", u)
	}
	if strings.Contains(u, "artistcheck[2]") {
		t.Fatalf("numeric tracker category must be filtered out: %s", u)
	}

	// the dynamic "Search Group Names Only" item switches searchstr → groupname
	g.Config.GetDynamic("searchgroupnames").(*configdata.BoolItem).Value = true
	if u = g.searchURL(q); !strings.Contains(u, "groupname=Some+Game") || strings.Contains(u, "searchstr") {
		t.Fatalf("groupname url = %s", u)
	}

	// freeleech-only adds freetorrent=1
	g.Config.FreeleechOnly.Value = true
	if u = g.searchURL(models.NewTorznabQuery()); !strings.Contains(u, "freetorrent=1") {
		t.Fatalf("freeleech url = %s", u)
	}
}

func TestGazelleGamesSearchParse(t *testing.T) {
	site := newGGNSite(t)
	g := newTestGGN(t, site)
	g.Config.APIKey.Value = strings.Repeat("k", 64)
	g.Config.Passkey.Value = "PK"
	g.Config.Authkey.Value = "AK"

	q := models.NewTorznabQuery()
	q.QueryType = "search"
	q.SearchTerm = "Some Game"
	q.Categories = []int{models.CatPCGames.ID}
	res, err := g.ResultsForQuery(context.Background(), q, false)
	if err != nil {
		t.Fatalf("ResultsForQuery: %v", err)
	}
	if len(res.Releases) != 2 {
		t.Fatalf("releases: %d", len(res.Releases))
	}

	// order by PublishDate desc: 5556 (11-15) before 5555 (11-14)
	first := res.Releases[0]
	wantTitle := "Some Game: GOTY Edition (2013) [GOTY 2014] [Windows, Mac, ISO, Raw, PAL, English, Scene, Trumpable]"
	if first.Title != wantTitle {
		t.Fatalf("title[0] = %q want %q", first.Title, wantTitle)
	}
	if first.Size == nil || *first.Size != 1073741824 {
		t.Fatalf("size[0] = %v", first.Size)
	}
	if first.Seeders == nil || *first.Seeders != 3 || first.Peers == nil || *first.Peers != 4 {
		t.Fatalf("seeders/peers[0] = %v/%v", first.Seeders, first.Peers)
	}
	if first.Grabs == nil || *first.Grabs != 7 || first.Files == nil || *first.Files != 2 {
		t.Fatalf("grabs/files[0] = %v/%v", first.Grabs, first.Files)
	}
	if first.DownloadVolumeFactor == nil || *first.DownloadVolumeFactor != 1 {
		t.Fatalf("dvf[0] = %v", first.DownloadVolumeFactor)
	}
	if first.UploadVolumeFactor == nil || *first.UploadVolumeFactor != 1 {
		t.Fatalf("uvf[0] = %v", first.UploadVolumeFactor)
	}
	if first.MinimumRatio == nil || *first.MinimumRatio != 1 || first.MinimumSeedTime == nil || *first.MinimumSeedTime != 288000 {
		t.Fatalf("minimum ratio/seed time[0] = %v/%v", first.MinimumRatio, first.MinimumSeedTime)
	}
	if first.PublishDate.Format("2006-01-02 15:04:05") != "2023-11-15 08:00:00" {
		t.Fatalf("date[0] = %v", first.PublishDate)
	}
	if first.Details == nil || first.Details.String() != site.URL+"/torrents.php?torrentid=5556&id=2700" {
		t.Fatalf("details[0] = %v", first.Details)
	}
	// upstream passes canUseToken=false; passkey/authkey are still appended
	if first.Link == nil || first.Link.String() != site.URL+"/torrents.php?action=download&torrent_pass=PK&authkey=AK&id=5556" {
		t.Fatalf("link[0] = %v", first.Link)
	}
	if first.Guid == nil || first.Guid.String() != first.Link.String() {
		t.Fatalf("guid[0] = %v", first.Guid)
	}
	found := false
	for _, c := range first.Category {
		if c == models.CatPCGames.ID {
			found = true
		}
	}
	if !found {
		t.Fatalf("category[0] = %v, want 4050 included", first.Category)
	}

	second := res.Releases[1]
	wantTitle2 := "Some Game: GOTY Edition (2013) [Windows, Mac, ISO, Raw, PAL, English, Scene]"
	if second.Title != wantTitle2 {
		t.Fatalf("title[1] = %q want %q", second.Title, wantTitle2)
	}
	if second.DownloadVolumeFactor == nil || *second.DownloadVolumeFactor != 0 {
		t.Fatalf("dvf[1] = %v", second.DownloadVolumeFactor)
	}
	if second.Size == nil || *second.Size != 2147483648 {
		t.Fatalf("size[1] = %v", second.Size)
	}

	if site.lastAPIKey != strings.Repeat("k", 64) {
		t.Fatalf("X-API-Key = %q", site.lastAPIKey)
	}
	paths := site.paths()
	if len(paths) != 1 || !strings.HasPrefix(paths[0], "/api.php?request=search&search_type=torrents") ||
		!strings.Contains(paths[0], "searchstr=Some+Game") ||
		!strings.Contains(paths[0], "artistcheck[0]=DOS") {
		t.Fatalf("paths = %v", paths)
	}
}

func TestGazelleGamesFreeleechOnly(t *testing.T) {
	site := newGGNSite(t)
	g := newTestGGN(t, site)
	g.Config.APIKey.Value = strings.Repeat("k", 64)
	g.Config.FreeleechOnly.Value = true

	res, err := g.ResultsForQuery(context.Background(), &models.TorznabQuery{QueryType: "search"}, false)
	if err != nil {
		t.Fatalf("ResultsForQuery: %v", err)
	}
	if len(res.Releases) != 1 {
		t.Fatalf("releases: %d, want only the freeleech torrent", len(res.Releases))
	}
	if paths := site.paths(); len(paths) != 1 || !strings.Contains(paths[0], "freetorrent=1") {
		t.Fatalf("paths = %v", paths)
	}
}

func TestGazelleGamesApplyConfiguration(t *testing.T) {
	site := newGGNSite(t)
	g := newTestGGN(t, site)
	key := strings.Repeat("a", 64)

	status, err := g.ApplyConfiguration(context.Background(), []indexer.ConfigItem{
		{ID: "sitelink", Value: site.URL + "/"},
		{ID: "apikey", Value: key},
	})
	if err != nil {
		t.Fatalf("ApplyConfiguration: %v", err)
	}
	if status != indexer.ConfigCompleted || !g.IsConfigured() {
		t.Fatalf("status=%v configured=%v", status, g.IsConfigured())
	}
	if site.lastAPIKey != key {
		t.Fatalf("X-API-Key = %q", site.lastAPIKey)
	}
	// the shared self-test runs the browse action (see 澄清记录)
	if paths := site.paths(); len(paths) != 1 || !strings.Contains(paths[0], "action=browse") {
		t.Fatalf("paths = %v", paths)
	}
	if g.SiteLink() != site.URL+"/" {
		t.Fatalf("site link = %q", g.SiteLink())
	}
}

func TestGazelleGamesAPIKeyLengthError(t *testing.T) {
	site := newGGNSite(t)
	g := newTestGGN(t, site)

	_, err := g.ApplyConfiguration(context.Background(), []indexer.ConfigItem{
		{ID: "sitelink", Value: site.URL + "/"},
		{ID: "apikey", Value: strings.Repeat("a", 63)},
	})
	if err == nil || err.Error() != "Invalid API Key configured: expected length: 64, got 63" {
		t.Fatalf("err = %v", err)
	}
}

func TestGazelleGamesAPIFailure(t *testing.T) {
	site := newGGNSite(t)
	site.fail = true
	g := newTestGGN(t, site)
	g.Config.APIKey.Value = strings.Repeat("k", 64)

	_, err := g.ResultsForQuery(context.Background(), &models.TorznabQuery{QueryType: "search"}, false)
	if err == nil || err.Error() != `Exception (gazellegamesapi): Unexpected response from indexer request: "bad key"` {
		t.Fatalf("err = %v", err)
	}
}

func TestGazelleGamesDownloadTokenFlip(t *testing.T) {
	site := newGGNSite(t)
	g := newTestGGN(t, site)
	g.Config.APIKey.Value = strings.Repeat("k", 64)
	g.Config.UseToken.Value = true

	link, _ := url.Parse(site.URL + "/torrents.php?action=download&usetoken=1&id=42")
	content, err := g.Download(context.Background(), link)
	if err != nil {
		t.Fatalf("Download: %v", err)
	}
	if string(content) != "d8:announce" {
		t.Fatalf("content = %q", content)
	}
}
