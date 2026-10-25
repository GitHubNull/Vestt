// mechanism: json-api (AvistaZ-family username/password/PID login against
// api/v1/jackett/auth, Bearer token on api/v1/jackett/torrents searches,
// silent token renewal + single retry on 401/412)
//
// Ported from: Definitions/PrivateHD.cs (+ Abstract/AvistazTracker.cs).
//
// 澄清记录:
//   - upstream PrivateHD.cs:31 adds a dynamic "Account Inactivity"
//     DisplayInfo on top of the base ConfigurationDataAvistaZTracker's
//     equally-named item, so the upstream form literally shows the info
//     twice (GetAllConfigurationItems = properties + dynamics); replicated
//     via AddDynamic (same id "accountinactivity", PrivateHD's 90-day
//     message rides as the dynamic entry).
//   - upstream RenewalTokenAsync/PerformQuery throw generic Exceptions;
//     Vestt SPEC-09 §4 maps auth-endpoint failures to KindAuth and
//     configuration failures to KindConfig (same deviation pattern as
//     PassThePopcorn authError).
//   - the post-renewal 401 retry in upstream PerformQuery
//     (AvistazTracker.cs:236-240) is only tried once; a still-failing retry
//     surfaces as "Invalid status code 401" from the >= 400 check
//     (AvistazTracker.cs:252-254) — mapped to KindAuth here.
package manual

import (
	"context"
	"errors"
	"strings"

	"vestt/common/httpclient"
	"vestt/common/indexer"
	"vestt/common/indexer/configdata"
	"vestt/common/indexer/shared"
	"vestt/common/models"
	"vestt/common/services"
)

// PrivateHD ports Jackett.Common/Indexers/Definitions/PrivateHD.cs.
type PrivateHD struct {
	shared.Avistaz
}

// privatehdAccountInactivity mirrors the upstream AddDynamic DisplayInfo
// item (PrivateHD.cs:31).
const privatehdAccountInactivity = "To avoid account deletion you must login at least 1 time every 90 days, and you must download at least 1 torrent every 6 months. Simply keeping torrents seeding long term will not protect your account"

// NewPrivateHD builds the indexer (services.Factory shape).
func NewPrivateHD(deps services.Deps) (indexer.Indexer, error) {
	p := &PrivateHD{}
	cfg := shared.NewAvistazConfig("")
	// upstream constructor: configData.AddDynamic("Account Inactivity", ...)
	cfg.AddDynamic("accountinactivity", configdata.NewInfoItem("Account Inactivity", privatehdAccountInactivity))
	p.Base = indexer.Base{
		IDValue:          "privatehd",
		NameValue:        "PrivateHD",
		DescriptionValue: "PrivateHD is a Private BitTorrent site for High Quality, High Definition (HD) MOVIES and TV Shows",
		SiteLinkValue:    "https://privatehd.to/",
		DefaultSiteLink:  "https://privatehd.to/",
		LangValue:        "en-US",
		TypeValue:        "private",
		Caps:             privatehdCaps(),
		Config:           cfg,
		Client:           deps.Client,
		Saver:            deps.Saver,
		Cache:            deps.Cache,
	}
	p.Bind(cfg)
	p.WireDelay()
	return p, nil
}

func privatehdCaps() *models.Capabilities {
	caps := models.NewCapabilities()
	def, max := 50, 50
	caps.LimitsDefault = &def
	caps.LimitsMax = &max
	caps.TVSearchParams = []string{
		models.TVParamQ, models.TVParamSeason, models.TVParamEp,
		models.TVParamImdbID, models.TVParamTvdbID, models.TVParamGenre,
	}
	caps.MovieSearchParams = []string{
		models.MovieParamQ, models.MovieParamImdbID, models.MovieParamTmdbID, models.MovieParamGenre,
	}
	caps.TVSearchImdbAvailable = true

	c := caps.Categories
	c.AddCategoryMapping("1", models.CatMovies, "")
	c.AddCategoryMapping("1", models.CatMoviesUHD, "")
	c.AddCategoryMapping("1", models.CatMoviesHD, "")
	c.AddCategoryMapping("1", models.CatMoviesSD, "")
	c.AddCategoryMapping("2", models.CatTV, "")
	c.AddCategoryMapping("2", models.CatTVUHD, "")
	c.AddCategoryMapping("2", models.CatTVHD, "")
	c.AddCategoryMapping("2", models.CatTVSD, "")
	return caps
}

// ApplyConfiguration ports upstream AvistazTracker.ApplyConfiguration
// (AvistazTracker.cs:186-197): load values, renew the token, run a test
// query, mark configured on success.
func (p *PrivateHD) ApplyConfiguration(ctx context.Context, items []indexer.ConfigItem) (indexer.ConfigStatus, error) {
	if err := p.ApplyItems(items); err != nil {
		return indexer.ConfigCompleted, p.privatehdConfigError(err)
	}
	if err := p.LoadValuesFromJSON(nil, false); err != nil {
		return indexer.ConfigCompleted, p.privatehdConfigError(err)
	}
	if err := p.RenewalToken(ctx); err != nil {
		return indexer.ConfigCompleted, p.privatehdAuthError(err)
	}
	releases, err := p.PerformQuery(ctx, models.NewTorznabQuery())
	if err != nil {
		return indexer.ConfigCompleted, p.privatehdConfigError(err)
	}
	if len(releases) == 0 {
		return indexer.ConfigCompleted, p.privatehdConfigError(errors.New("Could not find releases.")) //nolint:staticcheck // upstream AvistazTracker.cs error text, kept 1:1
	}
	p.SetConfigured(true)
	p.SaveConfig(ctx)
	return indexer.ConfigCompleted, nil
}

// PerformQuery wraps the shared AvistaZ query with the Vestt error-kind
// mapping (401 after renewal → KindAuth).
func (p *PrivateHD) PerformQuery(ctx context.Context, query *models.TorznabQuery) ([]*models.ReleaseInfo, error) {
	releases, err := p.Avistaz.PerformQuery(ctx, query)
	if err != nil {
		return nil, p.privatehdMapQueryError(err)
	}
	return releases, nil
}

// privatehdAuthError maps token-renewal failures to KindAuth.
func (p *PrivateHD) privatehdAuthError(err error) error {
	var ie *indexer.IndexerError
	if errors.As(err, &ie) {
		return err
	}
	var tm *httpclient.TooManyRequests
	if errors.As(err, &tm) {
		return err
	}
	return &indexer.IndexerError{IndexerID: p.ID(), Kind: indexer.KindAuth, Message: err.Error()}
}

// privatehdConfigError maps configuration/self-test failures to KindConfig.
func (p *PrivateHD) privatehdConfigError(err error) error {
	var ie *indexer.IndexerError
	if errors.As(err, &ie) {
		return err
	}
	return &indexer.IndexerError{IndexerID: p.ID(), Kind: indexer.KindConfig, Message: err.Error()}
}

// privatehdMapQueryError maps the "still 401 after renewal" case
// (AvistazTracker.cs:252-254) to KindAuth; everything else passes through.
func (p *PrivateHD) privatehdMapQueryError(err error) error {
	var ie *indexer.IndexerError
	if errors.As(err, &ie) {
		return err
	}
	var tm *httpclient.TooManyRequests
	if errors.As(err, &tm) {
		return err
	}
	if strings.HasPrefix(err.Error(), "Invalid status code 401") {
		return &indexer.IndexerError{IndexerID: p.ID(), Kind: indexer.KindAuth, Message: err.Error()}
	}
	return err
}
