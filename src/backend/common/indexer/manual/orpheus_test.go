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
	"vestt/common/models"
	"vestt/common/services"
)

const orpheusBrowseFixture = `{
  "status": "success",
  "response": {
    "results": [
      {
        "groupId": 100,
        "groupName": "Test Album",
        "artist": "Test Artist",
        "cover": "",
        "tags": ["rock"],
        "groupTime": "2023-11-10 10:00:00",
        "groupYear": "2020",
        "releaseType": "Album",
        "torrents": [
          {
            "torrentId": 777,
            "time": "2023-11-10 10:00:00",
            "size": 104857600,
            "seeders": 5,
            "leechers": 2,
            "snatches": 10,
            "fileCount": 4,
            "format": "FLAC",
            "encoding": "Lossless",
            "media": "CD",
            "category": "Music",
            "isFreeleech": false,
            "isPersonalFreeleech": false,
            "isNeutralLeech": false
          },
          {
            "torrentId": 778,
            "time": "2023-11-12 12:00:00",
            "size": 52428800,
            "seeders": 3,
            "leechers": 0,
            "snatches": 4,
            "fileCount": 1,
            "format": "MP3",
            "encoding": "320",
            "media": "WEB",
            "category": "Music",
            "isFreeleech": true,
            "isPersonalFreeleech": false,
            "isNeutralLeech": false
          }
        ]
      }
    ]
  }
}`

// orpheusSite is a fake Orpheus ajax.php site.
type orpheusSite struct {
	*httptest.Server

	mu           sync.Mutex
	hits         []string
	lastAuth     string
	fail         bool
	downloadHits []string
}

func newOrpheusSite(t *testing.T) *orpheusSite {
	t.Helper()
	s := &orpheusSite{}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()
		s.hits = append(s.hits, r.URL.RequestURI())
		switch r.URL.Path {
		case "/ajax.php":
			if r.URL.Query().Get("action") == "download" {
				s.downloadHits = append(s.downloadHits, r.URL.RequestURI())
				if r.URL.Query().Get("usetoken") == "1" {
					w.Header().Set("Content-Type", "text/html")
					_, _ = fmt.Fprint(w, "<html>You do not have enough freeleech tokens</html>")
					return
				}
				_, _ = w.Write([]byte("d8:announce"))
				return
			}
			s.lastAuth = r.Header.Get("Authorization")
			if s.fail {
				w.Header().Set("Content-Type", "application/json")
				_, _ = fmt.Fprint(w, `{"status":"failure","error":"bad key"}`)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprint(w, orpheusBrowseFixture)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(s.Close)
	return s
}

func (s *orpheusSite) paths() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.hits...)
}

func newTestOrpheus(t *testing.T, site *orpheusSite) *Orpheus {
	t.Helper()
	client, err := httpclient.New(httpclient.Config{}, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("httpclient.New: %v", err)
	}
	ix, err := NewOrpheus(services.Deps{Client: client})
	if err != nil {
		t.Fatalf("NewOrpheus: %v", err)
	}
	o := ix.(*Orpheus)
	o.SiteLinkValue = site.URL + "/"
	o.DefaultSiteLink = site.URL + "/"
	return o
}

func TestOrpheusCaps(t *testing.T) {
	ix, err := NewOrpheus(services.Deps{})
	if err != nil {
		t.Fatalf("NewOrpheus: %v", err)
	}
	if ix.ID() != "orpheus" || ix.Name() != "Orpheus" || ix.Type() != "private" {
		t.Fatalf("identity: %s / %s / %s", ix.ID(), ix.Name(), ix.Type())
	}
	caps := ix.Capabilities()
	wantMusic := []string{
		models.MusicParamQ, models.MusicParamAlbum, models.MusicParamArtist,
		models.MusicParamLabel, models.MusicParamYear, models.MusicParamGenre,
	}
	if strings.Join(caps.MusicSearchParams, ",") != strings.Join(wantMusic, ",") {
		t.Fatalf("MusicSearchParams = %v", caps.MusicSearchParams)
	}
	wantBook := []string{models.BookParamQ, models.BookParamGenre}
	if strings.Join(caps.BookSearchParams, ",") != strings.Join(wantBook, ",") {
		t.Fatalf("BookSearchParams = %v", caps.BookSearchParams)
	}
	for _, tc := range []struct {
		tracker string
		want    int
	}{
		{"1", 3000}, {"2", 4000}, {"3", 7020}, {"4", 3030},
		{"5", 8000}, {"6", 8000}, {"7", 7030},
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

func TestOrpheusConfigForm(t *testing.T) {
	ix, err := NewOrpheus(services.Deps{})
	if err != nil {
		t.Fatalf("NewOrpheus: %v", err)
	}
	o := ix.(*Orpheus)
	items, err := o.ConfigurationForSetup(context.Background())
	if err != nil {
		t.Fatalf("ConfigurationForSetup: %v", err)
	}
	var ids, types []string
	for _, it := range items {
		ids = append(ids, it["id"].(string))
		types = append(types, it["type"].(string))
	}
	want := []string{
		"sitelink", "apikey", "usefreeleechtokenswhenavailable",
		"", "accountinactivity", "tags",
	}
	if strings.Join(ids, "|") != strings.Join(want, "|") {
		t.Fatalf("config ids = %v, want %v", ids, want)
	}
	// the dynamic "Account Inactivity" item is a DisplayInfo
	if types[4] != "displayinfo" {
		t.Fatalf("accountinactivity type = %q", types[4])
	}
}

func TestOrpheusSearchParse(t *testing.T) {
	site := newOrpheusSite(t)
	o := newTestOrpheus(t, site)
	key := strings.Repeat("o", 116)
	o.Config.APIKey.Value = key
	o.Config.UseToken.Value = true

	q := models.NewTorznabQuery()
	q.QueryType = "music"
	q.SearchTerm = "album"
	res, err := o.ResultsForQuery(context.Background(), q, false)
	if err != nil {
		t.Fatalf("ResultsForQuery: %v", err)
	}
	if len(res.Releases) != 2 {
		t.Fatalf("releases: %d", len(res.Releases))
	}

	// order by PublishDate desc: 778 (11-12) before 777 (11-10)
	first := res.Releases[0]
	if first.Title != "Test Artist - Test Album [2020] [Album] MP3 / 320 / WEB" {
		t.Fatalf("title[0] = %q", first.Title)
	}
	if first.DownloadVolumeFactor == nil || *first.DownloadVolumeFactor != 0 {
		t.Fatalf("dvf[0] = %v", first.DownloadVolumeFactor)
	}
	// upstream computes canUseToken before the DVF is assigned → the
	// freeleech link ALSO carries usetoken=1 (bug replicated, see the
	// indexer's 澄清记录)
	if first.Link == nil || first.Link.String() != site.URL+"/ajax.php?action=download&usetoken=1&id=778" {
		t.Fatalf("link[0] = %v", first.Link)
	}

	second := res.Releases[1]
	if second.Title != "Test Artist - Test Album [2020] [Album] FLAC / Lossless / CD" {
		t.Fatalf("title[1] = %q", second.Title)
	}
	if second.DownloadVolumeFactor == nil || *second.DownloadVolumeFactor != 1 {
		t.Fatalf("dvf[1] = %v", second.DownloadVolumeFactor)
	}
	// non-freeleech → canUseToken → usetoken=1 (UseToken enabled)
	if second.Link == nil || second.Link.String() != site.URL+"/ajax.php?action=download&usetoken=1&id=777" {
		t.Fatalf("link[1] = %v", second.Link)
	}
	if second.Details == nil || second.Details.String() != site.URL+"/torrents.php?torrentid=777" {
		t.Fatalf("details[1] = %v", second.Details)
	}
	// the upstream proportional-ratio stamp
	if second.MinimumRatio == nil || *second.MinimumRatio != 0.6 {
		t.Fatalf("minimum ratio[1] = %v", second.MinimumRatio)
	}

	// token {0} authorization format
	if site.lastAuth != "token "+key {
		t.Fatalf("Authorization = %q", site.lastAuth)
	}
	if paths := site.paths(); len(paths) != 1 || !strings.Contains(paths[0], "action=browse") {
		t.Fatalf("paths = %v", paths)
	}
}

func TestOrpheusApplyConfiguration(t *testing.T) {
	site := newOrpheusSite(t)
	o := newTestOrpheus(t, site)
	key := strings.Repeat("a", 116)

	status, err := o.ApplyConfiguration(context.Background(), []indexer.ConfigItem{
		{ID: "sitelink", Value: site.URL + "/"},
		{ID: "apikey", Value: key},
	})
	if err != nil {
		t.Fatalf("ApplyConfiguration: %v", err)
	}
	if status != indexer.ConfigCompleted || !o.IsConfigured() {
		t.Fatalf("status=%v configured=%v", status, o.IsConfigured())
	}
	if site.lastAuth != "token "+key {
		t.Fatalf("Authorization = %q", site.lastAuth)
	}

	// legacy 118-char key is accepted too
	o2 := newTestOrpheus(t, site)
	_, err = o2.ApplyConfiguration(context.Background(), []indexer.ConfigItem{
		{ID: "sitelink", Value: site.URL + "/"},
		{ID: "apikey", Value: strings.Repeat("b", 118)},
	})
	if err != nil {
		t.Fatalf("legacy key err = %v", err)
	}

	// wrong length
	o3 := newTestOrpheus(t, site)
	_, err = o3.ApplyConfiguration(context.Background(), []indexer.ConfigItem{
		{ID: "sitelink", Value: site.URL + "/"},
		{ID: "apikey", Value: strings.Repeat("a", 115)},
	})
	if err == nil || err.Error() != "Invalid API Key configured: expected length: 116 or 118, got 115" {
		t.Fatalf("err = %v", err)
	}
}

func TestOrpheusAPIKeyFailure(t *testing.T) {
	site := newOrpheusSite(t)
	site.fail = true
	o := newTestOrpheus(t, site)

	_, err := o.ApplyConfiguration(context.Background(), []indexer.ConfigItem{
		{ID: "sitelink", Value: site.URL + "/"},
		{ID: "apikey", Value: strings.Repeat("a", 116)},
	})
	if err == nil || !strings.Contains(err.Error(), `Your API Key did not work: Unexpected response from indexer request: "bad key"`) {
		t.Fatalf("err = %v", err)
	}
	if o.IsConfigured() {
		t.Fatal("must not be configured after API key failure")
	}
}

func TestOrpheusDownloadTokenFlip(t *testing.T) {
	site := newOrpheusSite(t)
	o := newTestOrpheus(t, site)
	o.Config.APIKey.Value = strings.Repeat("o", 116)
	o.Config.UseToken.Value = true

	link, _ := url.Parse(site.URL + "/ajax.php?action=download&usetoken=1&id=42")
	content, err := o.Download(context.Background(), link)
	if err != nil {
		t.Fatalf("Download: %v", err)
	}
	if string(content) != "d8:announce" {
		t.Fatalf("content = %q", content)
	}
	site.mu.Lock()
	defer site.mu.Unlock()
	if len(site.downloadHits) != 2 || site.downloadHits[1] != "/ajax.php?action=download&id=42" {
		t.Fatalf("downloadHits = %v", site.downloadHits)
	}
}
