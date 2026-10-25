// mechanism: gazelle-api (ajax.php?action=browse JSON API, 41-char API key
// sent as the "Authorization" header, ajax.php?action=download downloads
// with optional freeleech-token usetoken=1)
//
// Ported from: Definitions/Redacted.cs (+ Abstract/GazelleTracker.cs).
//
// 澄清记录:
//   - upstream sets webclient.EmulateBrowser = false (Redacted.cs:44,
//     Issue #9751); Vestt's http stack has no per-indexer UA flag and the
//     shared gazelle port does not set EmulateBrowser on its requests, so
//     the default browser UA is sent (cosmetic only).
//   - upstream ShouldSkipRelease coerces torrent["isFreeload"] via
//     (string)JToken + bool.TryParse (Redacted.cs:92-95): Newtonsoft renders
//     a JSON bool as "True"/"False" (parseable) and a JSON string as-is,
//     while a JSON number renders "1"/"0" which bool.TryParse rejects —
//     redactedBoolLoose mirrors exactly that (numbers → false).
//   - upstream GetDownloadUrl builds
//     "{SiteLink}ajax.php?action=download{&usetoken=1}&id={id}"
//     (Redacted.cs:104-107); replicated via the GetDownloadURL hook. The
//     shared gazelle base computes canUseToken before the volume factors
//     are assigned (same ordering as upstream FillReleaseInfoFromJson), so
//     canUseToken is always true on this path — every link carries
//     "&usetoken=1" when the UseToken config flag is on, verbatim.
//   - upstream self-test (GazelleTracker.ApplyConfiguration, API-key path)
//     runs PerformQuery and wraps failures as "Your API Key did not work";
//     the Go port maps "Invalid API Key configured*" → KindConfig and
//     "Your API Key did not work*" / API failure JSON → KindAuth
//     (SPEC-09 §4).
package manual

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"

	"vestt/common/indexer"
	"vestt/common/indexer/configdata"
	"vestt/common/indexer/shared"
	"vestt/common/models"
	"vestt/common/services"
)

// Redacted ports Jackett.Common/Indexers/Definitions/Redacted.cs.
type Redacted struct {
	shared.Gazelle
}

// redactedInstructionMessage mirrors the upstream
// instructionMessageOptional constructor argument (Redacted.cs:41).
const redactedInstructionMessage = "<ol><li>Go to Redacted's site and open your account settings.</li><li>Go to <b>Access Settings</b> tab and copy the API Key.</li><li>Ensure that you've checked <b>Confirm API Key</b>.</li><li>Finally, click <b>Save Profile</b>.</li></ol>"

// redactedAccountInactivity mirrors the upstream AddDynamic DisplayInfo item
// (Redacted.cs:45).
const redactedAccountInactivity = "To keep your account active, sign in and browse the site at least once every 120 days. Seeding torrents does not count as account activity, so in order to remain active you need to sign in and browse the site. Some scripts or automated tools may be detected as activity on your account, but it is best to sign in with a browser from time to time just to make sure you will not be flagged as inactive. Donors and certain user classes (Power User+) are exempt from automatic account disabling due to inactivity. If you wish to always maintain an active account consider donating or reaching a higher user class."

// redactedMinimumRatio is stamped on every release (upstream Redacted
// proportional-ratio system, Redacted.cs:87).
var redactedMinimumRatio = 0.6

// NewRedacted builds the indexer (services.Factory shape).
func NewRedacted(deps services.Deps) (indexer.Indexer, error) {
	r := &Redacted{}
	r.Base = indexer.Base{
		IDValue:          "redacted",
		NameValue:        "Redacted",
		DescriptionValue: "Redacted is a Private site for MUSIC",
		SiteLinkValue:    "https://redacted.sh/",
		DefaultSiteLink:  "https://redacted.sh/",
		LegacySiteLinks:  []string{"https://redacted.ch/"},
		LangValue:        "en-US",
		TypeValue:        "private",
		Caps:             redactedCaps(),
		Client:           deps.Client,
		Saver:            deps.Saver,
		Cache:            deps.Cache,
	}
	cfg := shared.NewGazelleConfig(shared.GazelleOptions{
		SupportsFreeleechTokens: true,
		SupportsFreeloadOnly:    true,
		Has2FA:                  false,
		UseAPIKey:               true,
		InstructionMessage:      redactedInstructionMessage,
	})
	// upstream constructor: configData.AddDynamic("Account Inactivity", ...)
	cfg.AddDynamic("accountinactivity", configdata.NewInfoItem("Account Inactivity", redactedAccountInactivity))
	r.Bind(cfg)
	r.UseAPIKey = true
	// ShouldSkipRelease: freeload-only filter on top of the base
	// freeleech-only filter (Redacted.cs:92-102).
	r.ShouldSkipRelease = func(torrent map[string]any) bool {
		isFreeload := redactedBoolLoose(torrent["isFreeload"])

		if r.Config.FreeloadOnly != nil && r.Config.FreeloadOnly.Value && !isFreeload {
			return true
		}

		// base.ShouldSkipRelease: skip non-freeleech when freeleech only.
		isFreeleech := redactedBoolLoose(torrent["isFreeleech"])
		return r.Config.FreeleechOnly != nil && r.Config.FreeleechOnly.Value && !isFreeleech
	}
	// GetDownloadUrl: ajax.php download endpoint (Redacted.cs:104-107).
	r.GetDownloadURL = func(torrentID int, canUseToken bool) string {
		s := r.SiteLink() + "ajax.php?action=download"
		if r.useTokenFlag() && canUseToken {
			s += "&usetoken=1"
		}
		return s + "&id=" + strconv.Itoa(torrentID)
	}
	return r, nil
}

// useTokenFlag mirrors the upstream `useTokens` field (set from the
// UseTokenItem config value in LoadValuesFromJson).
func (r *Redacted) useTokenFlag() bool {
	return r.Config != nil && r.Config.UseToken != nil && r.Config.UseToken.Value
}

func redactedCaps() *models.Capabilities {
	caps := models.NewCapabilities()
	caps.MovieSearchParams = []string{models.MovieParamQ, models.MovieParamGenre}
	caps.MusicSearchParams = []string{
		models.MusicParamQ, models.MusicParamAlbum, models.MusicParamArtist,
		models.MusicParamLabel, models.MusicParamYear, models.MusicParamGenre,
	}
	caps.BookSearchParams = []string{models.BookParamQ, models.BookParamGenre}

	c := caps.Categories
	c.AddCategoryMapping("1", models.CatAudio, "Music")
	c.AddCategoryMapping("2", models.CatPC, "Applications")
	c.AddCategoryMapping("3", models.CatBooks, "E-Books")
	c.AddCategoryMapping("4", models.CatAudioAudiobook, "Audiobooks")
	c.AddCategoryMapping("5", models.CatMovies, "E-Learning Videos")
	c.AddCategoryMapping("6", models.CatAudio, "Comedy")
	c.AddCategoryMapping("7", models.CatBooks, "Comics")
	return caps
}

// ApplyConfiguration maps the shared gazelle API-key self-test errors to
// Vestt kinds (SPEC-09 §4).
func (r *Redacted) ApplyConfiguration(ctx context.Context, items []indexer.ConfigItem) (indexer.ConfigStatus, error) {
	status, err := r.Gazelle.ApplyConfiguration(ctx, items)
	if err != nil {
		return status, r.redactedMapConfigError(err)
	}
	return status, nil
}

// ResultsForQuery implements indexer.Indexer.
func (r *Redacted) ResultsForQuery(ctx context.Context, q *models.TorznabQuery, isMeta bool) (*indexer.IndexerResult, error) {
	return r.ResultsForQueryWith(ctx, q, isMeta, r.performQuery)
}

// Test implements indexer.Indexer.
func (r *Redacted) Test(ctx context.Context) error {
	return r.Base.Test(ctx, r.performQuery)
}

// performQuery ports upstream Redacted.PerformQuery (Redacted.cs:77-90):
// the base Gazelle query, AND-filtered against the query string, with the
// proportional-ratio MinimumRatio stamp.
func (r *Redacted) performQuery(ctx context.Context, query *models.TorznabQuery) ([]*models.ReleaseInfo, error) {
	releases, err := r.PerformQuery(ctx, query)
	if err != nil {
		return nil, r.redactedMapQueryError(err)
	}

	// results must contain search terms
	var out []*models.ReleaseInfo
	for _, release := range releases {
		if !query.MatchQueryStringAND(release.Title, 0, "") {
			continue
		}
		// the site has a proportional ratio system calculated using (1) the
		// total amount of data you've downloaded and (2) the total number of
		// torrents you're seeding.
		// So we are going to default the MR to the maximum ratio required to
		// cover the whole range as we cannot calculate this for each user.
		release.MinimumRatio = &redactedMinimumRatio
		out = append(out, release)
	}
	return out, nil
}

// redactedMapConfigError maps ApplyConfiguration failures: key validation →
// KindConfig; a key that failed the live self-test → KindAuth.
func (r *Redacted) redactedMapConfigError(err error) error {
	var ie *indexer.IndexerError
	if errors.As(err, &ie) {
		return err
	}
	msg := err.Error()
	kind := indexer.KindAuth
	if strings.HasPrefix(msg, "Invalid API Key configured") {
		kind = indexer.KindConfig
	}
	return &indexer.IndexerError{IndexerID: r.ID(), Kind: kind, Message: msg}
}

// redactedMapQueryError maps API-key rejection signatures to KindAuth
// (the shared gazelle base surfaces them as plain errors).
func (r *Redacted) redactedMapQueryError(err error) error {
	var ie *indexer.IndexerError
	if errors.As(err, &ie) {
		return err
	}
	msg := err.Error()
	if strings.HasPrefix(msg, "Invalid status code 401") ||
		strings.HasPrefix(msg, "Unexpected response from indexer request") {
		return &indexer.IndexerError{IndexerID: r.ID(), Kind: indexer.KindAuth, Message: msg}
	}
	return err
}

// redactedBoolLoose mirrors upstream (string)JToken + bool.TryParse for the
// freeleech/freeload flags: JSON bools and "true"/"false" strings count,
// JSON numbers render "1"/"0" which bool.TryParse rejects (→ false).
func redactedBoolLoose(v any) bool {
	switch t := v.(type) {
	case nil:
		return false
	case bool:
		return t
	case string:
		b, err := strconv.ParseBool(strings.TrimSpace(t))
		return err == nil && b
	case json.Number:
		return false
	default:
		return false
	}
}
