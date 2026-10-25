package shared

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"vestt/common/indexer"
	"vestt/common/indexer/configdata"
	"vestt/common/models"
)

func newBrazilian(site string) *Brazilian {
	b := &Brazilian{}
	b.IDValue = "braziliantest"
	b.NameValue = "Blaze"
	b.SiteLinkValue = site
	b.DefaultSiteLink = site
	b.LangValue = "pt-BR"
	b.TypeValue = "public"
	b.Caps = NewBrazilianCaps()
	b.Config = configdata.NewConfigData()
	return b
}

func TestBrazilianCaps(t *testing.T) {
	caps := NewBrazilianCaps()
	if fmt.Sprintf("%v", caps.MovieSearchParams) != "[q]" {
		t.Fatalf("movie params = %v", caps.MovieSearchParams)
	}
	if fmt.Sprintf("%v", caps.TVSearchParams) != "[q season ep]" {
		t.Fatalf("tv params = %v", caps.TVSearchParams)
	}
	if got := caps.Categories.GetTrackerCategories(); fmt.Sprintf("%v", got) != "[filmes series]" {
		t.Fatalf("tracker categories = %v", got)
	}
	if !caps.SupportsCategory(models.CatMovies.ID) || !caps.SupportsCategory(models.CatTV.ID) {
		t.Fatal("categories missing from tree")
	}
	if got := BrazilianDescription("Blaze"); got != "Blaze is a Public Torrent Tracker for Movies and TV Shows dubbed in Brazilian Portuguese" {
		t.Fatalf("description = %q", got)
	}
}

func TestBrazilianConfigJSON(t *testing.T) {
	b := newBrazilian("https://blaze.example/")
	items, err := configdata.ItemsJSON(b.Config)
	if err != nil {
		t.Fatalf("ItemsJSON: %v", err)
	}
	got := make([]string, 0, len(items))
	for _, m := range items {
		got = append(got, fmt.Sprintf("%s:%s", m["id"], m["type"]))
	}
	want := []string{"sitelink:inputstring", "tags:inputtags"}
	if fmt.Sprintf("%v", got) != fmt.Sprintf("%v", want) {
		t.Fatalf("items = %v, want %v", got, want)
	}
}

func TestBrazilianSearchURL(t *testing.T) {
	b := newBrazilian("https://blaze.example/")

	q := models.NewTorznabQuery()
	q.SearchTerm = "Filme Teste"
	if got := b.SearchURL(q); got != "https://blaze.example/?s=Filme+Teste" {
		t.Fatalf("url = %q", got)
	}

	season := 2
	q2 := models.NewTorznabQuery()
	q2.SearchTerm = "Série"
	q2.Season = &season
	if got := b.SearchURL(q2); got != "https://blaze.example/?s=S%C3%A9rie+2" {
		t.Fatalf("url = %q", got)
	}

	// empty term → bare site link
	if got := b.SearchURL(models.NewTorznabQuery()); got != "https://blaze.example/" {
		t.Fatalf("url = %q", got)
	}

	// custom search key (SimpleRequestGenerator argument)
	b.SearchQueryParamsKey = "?search="
	q3 := models.NewTorznabQuery()
	q3.SearchTerm = "x"
	if got := b.SearchURL(q3); got != "https://blaze.example/?search=x" {
		t.Fatalf("url = %q", got)
	}
}

func TestBrazilianApplyConfiguration(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, "ok")
	}))
	t.Cleanup(srv.Close)

	b := newBrazilian(srv.URL + "/")
	b.Client = testClient(t)
	status, err := b.ApplyConfiguration(context.Background(), nil)
	if err != nil {
		t.Fatalf("ApplyConfiguration: %v", err)
	}
	if status != indexer.ConfigCompleted || !b.IsConfigured() {
		t.Fatalf("status=%v configured=%v", status, b.IsConfigured())
	}
}

const brazilianRow = `<div class="item">
	<div class="title"><a href="https://blaze.example/filme-exemplo">Filme Exemplo Torrent (1.5GB) Dublado 1080p</a></div>
	<span>Gênero: Ação, Drama</span>
	<span>Legenda: S/L | Inglês</span>
	<span>Áudio: Português</span>
	<span>Lançamento: 2023</span>
	<span>Tamanho: 1.5 GB</span>
	<a class="download" href="magnet:?xt=urn:btih:ABC123&amp;dn=Filme.Exemplo.2023.1080p.WEB-DL.DUAL&amp;tr=http://tracker">MAGNET</a>
</div>`

func TestBrazilianRowExtraction(t *testing.T) {
	if got := ExtractGenres(brazilianRow); fmt.Sprintf("%v", got) != "[Ação Drama]" {
		t.Fatalf("genres = %v", got)
	}
	if got := ExtractSubtitles(brazilianRow); fmt.Sprintf("%v", got) != "[S/L Inglês]" {
		t.Fatalf("subtitles = %v", got)
	}
	if got := ExtractLanguages(brazilianRow); fmt.Sprintf("%v", got) != "[Português]" {
		t.Fatalf("languages = %v", got)
	}
	// Idioma fallback when Áudio is absent
	if got := ExtractLanguages(`<span>Idioma: Inglês</span>`); fmt.Sprintf("%v", got) != "[Inglês]" {
		t.Fatalf("languages fallback = %v", got)
	}

	date := ExtractReleaseDate(brazilianRow)
	if date.Format("2006") != "2023" || date.Month() != time.January || date.Day() != 1 {
		t.Fatalf("date = %v", date)
	}
	// missing label → today
	if got := ExtractReleaseDate("<p>nothing here</p>"); got.Year() != time.Now().Year() {
		t.Fatalf("default date = %v", got)
	}

	if got := ExtractSize(brazilianRow); got != 1610612736 {
		t.Fatalf("size = %d", got)
	}
}

func TestBrazilianMagnetAndCategory(t *testing.T) {
	button := `<a class="download" href="magnet:?xt=urn:btih:ABC123&amp;dn=Filme.Exemplo.2023.1080p.WEB-DL.DUAL&amp;tr=http://tracker">MAGNET</a>`
	magnet := ExtractMagnet(button)
	if magnet == nil || magnet.Scheme != "magnet" {
		t.Fatalf("magnet = %v", magnet)
	}
	if got := magnet.Query().Get("dn"); got != "Filme.Exemplo.2023.1080p.WEB-DL.DUAL" {
		t.Fatalf("dn = %q", got)
	}

	// no href → nil (degenerate path)
	if got := ExtractMagnet(`<a class="download">MAGNET</a>`); got != nil {
		t.Fatalf("magnet = %v", got)
	}

	// category detection: temporada / season / Sxx patterns → TV
	if got := ExtractCategory("Filme Exemplo", "Um Filme Qualquer"); fmt.Sprintf("%v", got) != "[2000]" {
		t.Fatalf("category = %v", got)
	}
	if got := ExtractCategory("", "Série Exemplo Temporada 2"); fmt.Sprintf("%v", got) != "[5000]" {
		t.Fatalf("category = %v", got)
	}
	if got := ExtractCategory("", "Show S01E02 1080p"); fmt.Sprintf("%v", got) != "[5000]" {
		t.Fatalf("category = %v", got)
	}
	if got := ExtractCategory("", "Show S2 COMPLETE"); fmt.Sprintf("%v", got) != "[5000]" {
		t.Fatalf("category = %v", got)
	}
	// row title link text wins over the fallback title
	if got := ExtractCategory("Série Dublada Temporada 1", "ignored movie title"); fmt.Sprintf("%v", got) != "[5000]" {
		t.Fatalf("category = %v", got)
	}
	// fallback title is only consulted when the row title is not TV
	if got := ExtractCategory("Série Dublada", "Show S03"); fmt.Sprintf("%v", got) != "[5000]" {
		t.Fatalf("category = %v", got)
	}
}

func TestBrazilianGetBytes(t *testing.T) {
	if got := GetBytes("1.5 GB"); got != 1610612736 {
		t.Fatalf("1.5 GB = %d", got)
	}
	// multiple units: keep the LAST range (upstream RightToLeft)
	if got := GetBytes("1.5 GB 2 GB"); got != 2147483648 {
		t.Fatalf("multi = %d", got)
	}
	if got := GetBytes("700 MB"); got != 734003200 {
		t.Fatalf("700 MB = %d", got)
	}
}

func TestBrazilianCleanTitle(t *testing.T) {
	cases := []struct{ in, want string }{
		// upstream parity: removal artifacts (double dots next to digits)
		// survive the .NET-equivalent pipeline
		{"Movie.Title.2023.1080p.WEBRip.DUBLADO.VACATORRENT.COM", "Movie Title.2023"},
		{"Movie Title 2023 1080p WEBRip Dublado COMANDO4K.COM", "Movie Title 2023"},
		{"Filme Exemplo (1.5GB) Dublado [1080p] COMANDO4K.COM", "Filme Exemplo"},
		{"Anime.Ep.01.[Erai-raws].MKV.LEG", "Anime Ep.01"},
		{"   ", ""},
	}
	for _, c := range cases {
		if got := CleanTitle(c.in); got != c.want {
			t.Fatalf("CleanTitle(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestBrazilianExtractSizeByResolution(t *testing.T) {
	if got := ExtractSizeByResolution("Movie 720p"); got != 1024*1024*1024 {
		t.Fatalf("720p = %d", got)
	}
	if got := ExtractSizeByResolution("Movie 1080p"); got != int64(2.5*1024*1024*1024) {
		t.Fatalf("1080p = %d", got)
	}
	if got := ExtractSizeByResolution("Movie 2160p"); got != 5*1024*1024*1024 {
		t.Fatalf("2160p = %d", got)
	}
	if got := ExtractSizeByResolution("Movie DVDSCR"); got != 512*1024*1024 {
		t.Fatalf("other = %d", got)
	}
}

func TestBrazilianExtractTitleOrDefault(t *testing.T) {
	b := newBrazilian("https://blaze.example/")

	// magnet dn parameter (followed by more params) drives the title
	button := `<a href="magnet:?xt=urn:btih:ABC&amp;dn=Some+Movie+2021+1080p+WEB-DL&amp;tr=udp://x">x</a>`
	if got := b.ExtractTitleOrDefault(button, "Ignored"); got != "Some Movie 2021 1080p" {
		t.Fatalf("title = %q", got)
	}

	// 上游 bug 复刻: a trailing &dn= (group 2) is discarded upstream too
	buttonTrailing := `<a href="magnet:?xt=urn:btih:ABC&amp;dn=Trailing.Name.2023">x</a>`
	if got := b.ExtractTitleOrDefault(buttonTrailing, "Fallback Title 720p"); got != "Fallback Title 720p" {
		t.Fatalf("title = %q", got)
	}

	// fallback to the description element via the GetTitleElement hook
	b.GetTitleElement = func(downloadButtonHTML string) string {
		return "Descrição 2160p"
	}
	if got := b.ExtractTitleOrDefault(`<a href="/dl">x</a>`, ""); got != "Descrição 2160p" {
		t.Fatalf("title = %q", got)
	}

	// default title wins; resolution comes from the description element when
	// one is present (upstream takes resolution from description text first)
	if got := b.ExtractTitleOrDefault(`<a href="/dl">x</a>`, "Simple Title"); got != "Simple Title 2160p" {
		t.Fatalf("title = %q", got)
	}
}
