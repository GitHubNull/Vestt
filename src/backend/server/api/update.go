package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"vestt/common/utils"
)

// Version-check implementation for POST /api/v2.0/server/update
// (SPEC-14 §3.4; backend-api.md §1.5). Only the notify part of upstream
// UpdateService.CheckForUpdates (Jackett.Common/Services/UpdateService.cs
// L77-183) is ported: query the latest release, record it and log; the
// download/replace logic has no Vestt equivalent (backend-api.md §8).

// saved_state keys (data-model.md §2.7).
const (
	stateKeyLastCheck     = "update.last_check"
	stateKeyLatestVersion = "update.latest_version"
)

// defaultUpdateAPIURL is the GitHub latest-release endpoint of this
// repository (SPEC-14 §3.4).
const defaultUpdateAPIURL = "https://api.github.com/repos/GitHubNull/Vestt/releases/latest"

// updateAPIURL is injectable for tests (httptest fake GitHub).
var updateAPIURL = defaultUpdateAPIURL

// updateHTTPClient performs the release query with the §1.5 10s budget.
var updateHTTPClient = &http.Client{Timeout: 10 * time.Second}

// githubRelease is the subset of the GitHub releases/latest response used here.
type githubRelease struct {
	TagName string `json:"tag_name"`
	HTMLURL string `json:"html_url"`
	Name    string `json:"name"`
}

// fetchLatestRelease queries the GitHub latest release. GitHub rejects
// requests without a User-Agent, hence "vestt/<version>".
func fetchLatestRelease(ctx context.Context, version string) (*githubRelease, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, updateAPIURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "vestt/"+version)
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := updateHTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("release query returned status %d", resp.StatusCode)
	}
	var release githubRelease
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&release); err != nil {
		return nil, fmt.Errorf("decode release response: %w", err)
	}
	if release.TagName == "" {
		return nil, fmt.Errorf("release response has no tag_name")
	}
	return &release, nil
}

// checkForUpdate runs the version check: fetch, persist last_check/latest
// tag, then log according to the semver relation (upstream semantics:
// downgrade warn / up-to-date info / new release info). Failures are
// silently logged — the endpoint keeps its 204 success semantics
// (backend-api.md §1.5).
func (h *ServerConfigHandlers) checkForUpdate(ctx context.Context) {
	current := h.Config.AppVersion()
	slog.Info("Checking for updates...", "version", current)

	release, err := fetchLatestRelease(ctx, current)
	if err != nil {
		slog.Warn("update check failed", "error", utils.Sanitize(err.Error()))
		return
	}

	// Successful check: record timestamp + latest tag (§1.5 side effects).
	if err := h.Config.SaveState(ctx, stateKeyLastCheck,
		mustJSON(time.Now().UTC().Format(time.RFC3339))); err != nil {
		slog.Warn("persist update last_check failed", "error", utils.Sanitize(err.Error()))
	}
	if err := h.Config.SaveState(ctx, stateKeyLatestVersion,
		mustJSON(release.TagName)); err != nil {
		slog.Warn("persist update latest_version failed", "error", utils.Sanitize(err.Error()))
	}

	cmp, err := semverCompare(release.TagName, current)
	if err != nil {
		slog.Warn("failed to compare versions",
			"latest", release.TagName, "current", current, "error", utils.Sanitize(err.Error()))
		return
	}
	switch {
	case cmp > 0:
		slog.Info(fmt.Sprintf("New release found. Current version: v%s New version: %s", current, release.TagName),
			"latest_url", release.HTMLURL)
	case cmp < 0:
		slog.Warn(fmt.Sprintf("Downgrade detected. Current version: v%s New version: %s", current, release.TagName))
	default:
		slog.Info(fmt.Sprintf("Vestt is already updated. Current version: v%s", current))
	}
}

func mustJSON(v any) string {
	raw, err := json.Marshal(v)
	if err != nil {
		return "null"
	}
	return string(raw)
}

// semverCompare compares two semantic versions ("MAJOR.MINOR.PATCH[-pre][+build]",
// optional leading "v" / surrounding spaces) and returns -1/0/1 (a<b/a==b/a>b).
// Prerelease precedence follows semver.org §11 (1.0.0-alpha < 1.0.0-alpha.1 <
// 1.0.0-beta < 1.0.0; numeric identifiers compare numerically and rank below
// alphanumeric ones); build metadata is ignored. Dirty build suffixes
// ("0.1.0-dirty") behave as regular prerelease identifiers.
func semverCompare(a, b string) (int, error) {
	amaj, amin, apat, apre, err := parseSemver(a)
	if err != nil {
		return 0, err
	}
	bmaj, bmin, bpat, bpre, err := parseSemver(b)
	if err != nil {
		return 0, err
	}
	if amaj != bmaj {
		return sign(amaj - bmaj), nil
	}
	if amin != bmin {
		return sign(amin - bmin), nil
	}
	if apat != bpat {
		return sign(apat - bpat), nil
	}
	return comparePrerelease(apre, bpre), nil
}

func sign(n int) int {
	switch {
	case n < 0:
		return -1
	case n > 0:
		return 1
	default:
		return 0
	}
}

// parseSemver splits a version into core numbers and prerelease identifiers.
// Build metadata (+...) is discarded.
func parseSemver(s string) (major, minor, patch int, pre []string, err error) {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "v")
	if i := strings.IndexByte(s, '+'); i >= 0 {
		s = s[:i]
	}
	preStr := ""
	if i := strings.IndexByte(s, '-'); i >= 0 {
		preStr = s[i+1:]
		s = s[:i]
	}
	core := strings.Split(s, ".")
	if len(core) != 3 {
		return 0, 0, 0, nil, fmt.Errorf("invalid semver %q", s)
	}
	if major, err = strconv.Atoi(core[0]); err != nil || major < 0 {
		return 0, 0, 0, nil, fmt.Errorf("invalid semver %q", s)
	}
	if minor, err = strconv.Atoi(core[1]); err != nil || minor < 0 {
		return 0, 0, 0, nil, fmt.Errorf("invalid semver %q", s)
	}
	if patch, err = strconv.Atoi(core[2]); err != nil || patch < 0 {
		return 0, 0, 0, nil, fmt.Errorf("invalid semver %q", s)
	}
	if preStr != "" {
		pre = strings.Split(preStr, ".")
	}
	return major, minor, patch, pre, nil
}

func isNumericID(id string) bool {
	if id == "" {
		return false
	}
	for _, r := range id {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// comparePrerelease implements semver.org §11 rule 11.4: a version without
// prerelease outranks one with; otherwise identifiers compare pairwise
// (numeric < alphanumeric, numerics numerically, alphanumerics in ASCII
// order) and a longer identifier set outranks its prefix.
func comparePrerelease(a, b []string) int {
	switch {
	case len(a) == 0 && len(b) == 0:
		return 0
	case len(a) == 0:
		return 1
	case len(b) == 0:
		return -1
	}
	for i := 0; i < len(a) && i < len(b); i++ {
		an, bn := isNumericID(a[i]), isNumericID(b[i])
		switch {
		case an && bn:
			x, _ := strconv.Atoi(a[i])
			y, _ := strconv.Atoi(b[i])
			if x != y {
				return sign(x - y)
			}
		case an:
			return -1
		case bn:
			return 1
		default:
			if a[i] != b[i] {
				return sign(strings.Compare(a[i], b[i]))
			}
		}
	}
	return sign(len(a) - len(b))
}
