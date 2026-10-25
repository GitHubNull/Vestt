// mechanism: json-api (passkey-authenticated browse.php?api&passkey JSON
// array API, details.php / download.php?id=&passkey= links)
//
// Ported from: Definitions/SceneHD.cs.
//
// 澄清记录:
//   - upstream LoadValuesFromJson pins a trusted TLS certificate for the
//     site (SceneHD.cs:82-87, AddTrustedCertificate with a hardcoded
//     SHA1 fingerprint); Go's tls.Config pinning is managed by the http
//     stack, not the indexer — omitted (the port trusts the system roots,
//     same as every other indexer).
//   - upstream PerformQuery has no HTTP status check: a failing response
//     whose body contains "User not found or passkey not set" throws
//     "The passkey is invalid. Check the indexer configuration."
//     (SceneHD.cs:125-126); Vestt maps that to KindAuth (credential
//     rejected). Any other failing body falls into the JSON parse and
//     surfaces as KindParse, exactly like upstream's OnParseError.
//   - upstream DateTime.ParseExact(added, "yyyy-MM-dd HH:mm:ss") uses no
//     DateTimeStyles, so the parsed kind is Unspecified (displayed as-is);
//     the Go port parses in the local zone (same wall clock, Vestt
//     convention as shared/gazelle).
package manual

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
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

// SceneHD ports Jackett.Common/Indexers/Definitions/SceneHD.cs.
type SceneHD struct {
	indexer.Base
	cfg *sceneHDConfig
}

// sceneHDConfig ports upstream ConfigurationDataPasskey
// (Models/IndexerConfig/ConfigurationDataPasskey.cs).
type sceneHDConfig struct {
	configdata.ConfigData
	Passkey      *configdata.StringItem
	Instructions *configdata.InfoItem
}

// sceneHDInstructions mirrors the upstream ConfigurationDataPasskey
// instruction message (SceneHD.cs:43-44).
const sceneHDInstructions = "You can find the Passkey if you generate a RSS feed link. It's the last parameter in the URL."

func newSceneHDConfig() *sceneHDConfig {
	return &sceneHDConfig{
		ConfigData:   *configdata.NewConfigData(),
		Passkey:      configdata.NewStringItem("Passkey"),
		Instructions: configdata.NewInfoItem("", sceneHDInstructions),
	}
}

// NewSceneHD builds the indexer (services.Factory shape).
func NewSceneHD(deps services.Deps) (indexer.Indexer, error) {
	s := &SceneHD{cfg: newSceneHDConfig()}
	s.Base = indexer.Base{
		IDValue:          "scenehd",
		NameValue:        "SceneHD",
		DescriptionValue: "SceneHD is Private site for HD TV / MOVIES",
		SiteLinkValue:    "https://scenehd.org/",
		DefaultSiteLink:  "https://scenehd.org/",
		LangValue:        "en-US",
		TypeValue:        "private",
		Caps:             sceneHDCaps(),
		Config:           s.cfg,
		Client:           deps.Client,
		Saver:            deps.Saver,
		Cache:            deps.Cache,
	}
	return s, nil
}

func sceneHDCaps() *models.Capabilities {
	caps := models.NewCapabilities()
	caps.TVSearchParams = []string{models.TVParamQ, models.TVParamSeason, models.TVParamEp}
	caps.MovieSearchParams = []string{models.MovieParamQ, models.MovieParamImdbID}
	caps.MusicSearchParams = []string{models.MusicParamQ}

	c := caps.Categories
	c.AddCategoryMapping("2", models.CatMoviesUHD, "Movie/2160")
	c.AddCategoryMapping("1", models.CatMoviesHD, "Movie/1080")
	c.AddCategoryMapping("4", models.CatMoviesHD, "Movie/720")
	c.AddCategoryMapping("8", models.CatMoviesBluRay, "Movie/BD5/9")
	c.AddCategoryMapping("6", models.CatTVUHD, "TV/2160")
	c.AddCategoryMapping("5", models.CatTVHD, "TV/1080")
	c.AddCategoryMapping("7", models.CatTVHD, "TV/720")
	c.AddCategoryMapping("22", models.CatMoviesBluRay, "Bluray/Complete")
	c.AddCategoryMapping("10", models.CatXXX, "XXX")
	c.AddCategoryMapping("16", models.CatMoviesOther, "Subpacks")
	c.AddCategoryMapping("13", models.CatAudioVideo, "MVID")
	c.AddCategoryMapping("9", models.CatOther, "Other")
	return caps
}

func (s *SceneHD) searchURL() string   { return s.SiteLink() + "browse.php?" }
func (s *SceneHD) detailsURL() string  { return s.SiteLink() + "details.php?" }
func (s *SceneHD) downloadURL() string { return s.SiteLink() + "download.php?" }

// ApplyConfiguration ports upstream SceneHD.ApplyConfiguration
// (SceneHD.cs:89-101): validate the passkey length, run a test query,
// mark configured on success.
func (s *SceneHD) ApplyConfiguration(ctx context.Context, items []indexer.ConfigItem) (indexer.ConfigStatus, error) {
	if err := s.ApplyItems(items); err != nil {
		return indexer.ConfigCompleted, s.sceneHDConfigError(err)
	}
	if err := s.LoadValuesFromJSON(nil, false); err != nil {
		return indexer.ConfigCompleted, s.sceneHDConfigError(err)
	}

	if len(s.cfg.Passkey.Value) != 32 {
		return indexer.ConfigCompleted, s.sceneHDConfigError(errors.New("Invalid Passkey configured. Expected length: 32")) //nolint:staticcheck // upstream SceneHD.cs error text, kept 1:1
	}

	releases, err := s.PerformQuery(ctx, models.NewTorznabQuery())
	if err != nil {
		return indexer.ConfigCompleted, s.sceneHDConfigError(err)
	}
	if len(releases) == 0 {
		return indexer.ConfigCompleted, s.sceneHDConfigError(errors.New("Could not find releases from this URL.")) //nolint:staticcheck // upstream SceneHD.cs error text, kept 1:1
	}
	s.SetConfigured(true)
	s.SaveConfig(ctx)
	return indexer.ConfigCompleted, nil
}

// ResultsForQuery implements indexer.Indexer.
func (s *SceneHD) ResultsForQuery(ctx context.Context, q *models.TorznabQuery, isMeta bool) (*indexer.IndexerResult, error) {
	return s.ResultsForQueryWith(ctx, q, isMeta, s.PerformQuery)
}

// Test implements indexer.Indexer (upstream TestIndexer).
func (s *SceneHD) Test(ctx context.Context) error {
	return s.Base.Test(ctx, s.PerformQuery)
}

// sceneHDTorrent is one browse.php API row (upstream JArray item, strict
// Newtonsoft Value<T> casts — mirrored with typed JSON fields).
type sceneHDTorrent struct {
	ID            int64  `json:"id"`
	Name          string `json:"name"`
	Added         string `json:"added"`
	IsFreeleech   int    `json:"is_freeleech"`
	Size          int64  `json:"size"`
	TimesComplete int64  `json:"times_completed"`
	NumFiles      int64  `json:"numfiles"`
	Seeders       int64  `json:"seeders"`
	Leechers      int64  `json:"leechers"`
	ImdbID        string `json:"imdbid"`
	Category      string `json:"category"`
}

// PerformQuery ports upstream SceneHD.PerformQuery (SceneHD.cs:103-172).
func (s *SceneHD) PerformQuery(ctx context.Context, query *models.TorznabQuery) ([]*models.ReleaseInfo, error) {
	if s.Client == nil {
		return nil, errors.New("http client not configured")
	}
	passkey := s.cfg.Passkey.Value

	qc := shared.QueryParams{}
	qc.Add("api", "")
	qc.Add("passkey", passkey)
	search := query.GetQueryString()
	if query.IsImdbQuery() {
		search = query.ImdbID
	}
	qc.Add("search", search)

	categoryMapping := sceneHDDistinct(s.Caps.Categories.MapTorznabCapsToTrackers(query, false))
	if len(categoryMapping) > 0 {
		qc.Add("cat", strings.Join(categoryMapping, ","))
	}

	searchURL := s.searchURL() + qc.GetQueryString()
	res, err := s.Client.Do(ctx, s.ID(), &httpclient.Request{
		URL:     searchURL,
		Cookies: s.CookieHeader(),
	})
	if err != nil {
		return nil, err
	}

	if strings.Contains(string(res.Body), "User not found or passkey not set") {
		return nil, &indexer.IndexerError{
			IndexerID: s.ID(),
			Kind:      indexer.KindAuth,
			Message:   "The passkey is invalid. Check the indexer configuration.",
		}
	}

	var rows []sceneHDTorrent
	if err := json.Unmarshal(res.Body, &rows); err != nil {
		return nil, s.sceneHDParseFailure(string(res.Body), err)
	}

	var releases []*models.ReleaseInfo
	for _, item := range rows {
		title := item.Name
		if !query.IsImdbQuery() && !query.MatchQueryStringAND(title, 0, "") {
			continue
		}

		details, err := url.Parse(fmt.Sprintf("%sid=%d", s.detailsURL(), item.ID))
		if err != nil {
			return nil, s.sceneHDParseFailure(string(res.Body), err)
		}
		link, err := url.Parse(fmt.Sprintf("%sid=%d&passkey=%s", s.downloadURL(), item.ID, passkey))
		if err != nil {
			return nil, s.sceneHDParseFailure(string(res.Body), err)
		}
		// upstream: DateTime.ParseExact(added, "yyyy-MM-dd HH:mm:ss",
		// InvariantCulture) with no styles (Unspecified kind).
		publishDate, err := time.ParseInLocation("2006-01-02 15:04:05", item.Added, time.Local)
		if err != nil {
			return nil, s.sceneHDParseFailure(string(res.Body), err)
		}
		dlVolumeFactor := 1.0
		if item.IsFreeleech == 1 {
			dlVolumeFactor = 0
		}
		minimumRatio := 1.0
		var minimumSeedTime int64
		peers := item.Leechers + item.Seeders

		release := &models.ReleaseInfo{
			Title:                title,
			Link:                 link,
			Details:              details,
			Guid:                 details,
			Category:             s.Caps.Categories.MapTrackerCatToNewznab(item.Category),
			PublishDate:          publishDate,
			Size:                 &item.Size,
			Grabs:                &item.TimesComplete,
			Files:                &item.NumFiles,
			Seeders:              &item.Seeders,
			Peers:                &peers,
			MinimumRatio:         &minimumRatio,
			MinimumSeedTime:      &minimumSeedTime,
			DownloadVolumeFactor: &dlVolumeFactor,
			UploadVolumeFactor:   batch3F64(1),
		}
		if imdb, ok := parseutil.GetImdbID(item.ImdbID); ok {
			release.Imdb = &imdb
		}

		releases = append(releases, release)
	}

	return releases, nil
}

// sceneHDDistinct mirrors upstream .Distinct() (order preserved).
func sceneHDDistinct(list []string) []string {
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

// sceneHDConfigError maps configuration/self-test failures to KindConfig.
func (s *SceneHD) sceneHDConfigError(err error) error {
	var ie *indexer.IndexerError
	if errors.As(err, &ie) {
		return err
	}
	return &indexer.IndexerError{IndexerID: s.ID(), Kind: indexer.KindConfig, Message: err.Error()}
}

// sceneHDParseFailure mirrors upstream OnParseError.
func (s *SceneHD) sceneHDParseFailure(dump string, err error) error {
	s.log().Error(fmt.Sprintf("Error while parsing result: %v\n%s", err, dump))
	return &indexer.IndexerError{IndexerID: s.ID(), Kind: indexer.KindParse, Message: "Parse error"}
}

// log is a nil-safe logger (Base.Logger may be nil).
func (s *SceneHD) log() *slog.Logger {
	if s.Logger != nil {
		return s.Logger
	}
	return slog.Default()
}
