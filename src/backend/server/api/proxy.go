// DownloadController / ImageController / BlackholeController ports: the
// anonymous proxy endpoints /dl/{indexerId}, /img/{indexerId} and
// /bh/{indexerId}. Only jackett_apikey is checked (upstream [AllowAnonymous]
// + explicit API key comparison).
//
// Ported from: Jackett.Server/Controllers/DownloadController.cs,
// ImageController.cs, BlackholeController.cs and
// Jackett.Server/ActionFilters/DownloadActionFilter.cs.
package api

import (
	"crypto/rand"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"

	"vestt/common/services"
	"vestt/common/utils/bencode"
	"vestt/common/utils/stringutil"
)

// ProxyHandlers serves /dl, /img and /bh.
type ProxyHandlers struct {
	Config  *services.ConfigService
	Manager *services.IndexerManagerService
	Logger  *slog.Logger
}

func (h *ProxyHandlers) logger() *slog.Logger {
	if h.Logger != nil {
		return h.Logger
	}
	return slog.Default()
}

func (h *ProxyHandlers) Routes(r chi.Router) {
	// upstream routes are dl/{indexerId} — clients request the proxied URL
	// with a trailing slash (ConvertToProxyLink's format string), so both
	// spellings are registered.
	r.Get("/dl/{indexerId}", h.Download)
	r.Get("/dl/{indexerId}/", h.Download)
	r.Get("/img/{indexerId}", h.DownloadImage)
	r.Get("/img/{indexerId}/", h.DownloadImage)
	r.Get("/bh/{indexerId}", h.Blackhole)
	r.Get("/bh/{indexerId}/", h.Blackhole)
}

// downloadQueryParam resolves a query parameter with the upstream
// DownloadActionFilter fallback: some RSS readers don't decode &amp; before
// requesting, so when "path" is absent the raw query string is re-parsed
// after un-escaping &amp; (the dl/img routes only, matching the filter's
// application to DownloadController and ImageController).
func downloadQueryParam(r *http.Request, key string) string {
	q := r.URL.Query()
	if v := q.Get(key); v != "" {
		return v
	}
	if key == "path" && q.Get("path") == "" && r.URL.RawQuery != "" {
		fixed := strings.ReplaceAll(r.URL.RawQuery, "&amp;", "&")
		if parsed, err := url.ParseQuery(fixed); err == nil {
			return parsed.Get(key)
		}
	}
	return ""
}

// magnetBytes reports whether content begins with the ASCII bytes of
// "magnet:" (upstream's byte-level prefix check).
func magnetBytes(b []byte) bool {
	return len(b) >= 7 && b[0] == 'm' && b[1] == 'a' && b[2] == 'g' &&
		b[3] == 'n' && b[4] == 'e' && b[5] == 't' && b[6] == ':'
}

// Download serves GET /dl/{indexerId} (upstream DownloadController.DownloadAsync).
func (h *ProxyHandlers) Download(w http.ResponseWriter, r *http.Request) {
	noStore(w)
	indexerID := chi.URLParam(r, "indexerId")
	pathParam := downloadQueryParam(r, "path")
	file := downloadQueryParam(r, "file")

	fail := func(err error) {
		h.logger().Error(fmt.Sprintf("Error downloading. indexer: %s path: %s\n%v",
			strings.ReplaceAll(indexerID, "\n", ""), strings.ReplaceAll(pathParam, "\n", ""), err))
		http.Error(w, "Error downloading", http.StatusNotFound)
	}

	if h.Config.Config().APIKey != r.URL.Query().Get("jackett_apikey") {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	idx, err := h.Manager.GetIndexer(indexerID)
	if err != nil {
		fail(err)
		return
	}
	if !idx.IsConfigured() {
		h.logger().Warn(fmt.Sprintf("Rejected a request to %s which is unconfigured.", idx.Name()))
		http.Error(w, "This indexer is not configured.", http.StatusForbidden)
		return
	}
	rawPath, err := decodeProxyPath(pathParam)
	if err != nil {
		fail(err)
		return
	}
	target, err := url.Parse(rawPath)
	if err != nil {
		fail(err)
		return
	}
	downloadBytes, err := idx.Download(r.Context(), target)
	if err != nil {
		fail(err)
		return
	}

	// magnet URLs: redirect instead of serving bytes
	if magnetBytes(downloadBytes) {
		http.Redirect(w, r, string(downloadBytes), http.StatusFound)
		return
	}

	// re-encode so torrent dictionaries get canonically sorted keys
	// (BencodeNET Parse/EncodeAsBytes observable behaviour)
	torrent, err := bencode.Parse(downloadBytes)
	if err != nil {
		h.logger().Error(string(downloadBytes))
		fail(fmt.Errorf("BencodeParser failed: %w", err))
		return
	}
	sorted, err := bencode.Encode(torrent)
	if err != nil {
		fail(err)
		return
	}

	fileName := stringutil.MakeValidFileName(file, '_', false) + ".torrent"
	w.Header().Set("Content-Type", "application/x-bittorrent")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", fileName))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(sorted)
}

// DownloadImage serves GET /img/{indexerId} (upstream ImageController.DownloadImageAsync):
// status passthrough for non-OK/Continue/PartialContent, content-type header
// or image/jpeg, any error → 500.
func (h *ProxyHandlers) DownloadImage(w http.ResponseWriter, r *http.Request) {
	noStore(w)
	indexerID := chi.URLParam(r, "indexerId")
	pathParam := downloadQueryParam(r, "path")

	fail := func(err error) {
		h.logger().Debug(fmt.Sprintf("Error downloading image. indexer: %s path: %s\n%v",
			strings.ReplaceAll(indexerID, "\n", ""), strings.ReplaceAll(pathParam, "\n", ""), err))
		w.WriteHeader(http.StatusInternalServerError)
	}

	if h.Config.Config().APIKey != r.URL.Query().Get("jackett_apikey") {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	idx, err := h.Manager.GetIndexer(indexerID)
	if err != nil {
		fail(err)
		return
	}
	if !idx.IsConfigured() {
		h.logger().Warn(fmt.Sprintf("Rejected a request to %s which is unconfigured.", idx.Name()))
		http.Error(w, "This indexer is not configured.", http.StatusForbidden)
		return
	}
	rawPath, err := decodeProxyPath(pathParam)
	if err != nil {
		fail(err)
		return
	}
	target, err := url.Parse(rawPath)
	if err != nil {
		fail(err)
		return
	}
	resp, err := idx.DownloadImage(r.Context(), target)
	if err != nil {
		fail(err)
		return
	}
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusContinue &&
		resp.StatusCode != http.StatusPartialContent {
		w.WriteHeader(resp.StatusCode)
		return
	}
	contentType := "image/jpeg"
	if ct := resp.Headers.Get("content-type"); ct != "" {
		contentType = ct
	}
	w.Header().Set("Content-Type", contentType)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(resp.Body)
}

// Blackhole serves GET /bh/{indexerId} (upstream BlackholeController.BlackholeAsync):
// always HTTP 200 with {"result":"success"} or {"result":"error","error":msg}.
func (h *ProxyHandlers) Blackhole(w http.ResponseWriter, r *http.Request) {
	noStore(w)
	indexerID := chi.URLParam(r, "indexerId")
	pathParam := downloadQueryParam(r, "path")
	file := r.URL.Query().Get("file") // no DownloadActionFilter on this route upstream

	result := map[string]string{"result": "success"}
	writeReply := func() {
		writeJSON(w, http.StatusOK, result)
	}

	fail := func(err error) {
		h.logger().Error(fmt.Sprintf("Error downloading to blackhole. indexer: %s path: %s\n%v",
			strings.ReplaceAll(indexerID, "\n", ""), strings.ReplaceAll(pathParam, "\n", ""), err))
		result["result"] = "error"
		result["error"] = err.Error()
	}

	if h.Config.Config().APIKey != r.URL.Query().Get("jackett_apikey") {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	idx, err := h.Manager.GetIndexer(indexerID)
	if err != nil {
		fail(err)
		writeReply()
		return
	}
	if !idx.IsConfigured() {
		h.logger().Warn(fmt.Sprintf("Rejected a request to %s which is unconfigured.", idx.Name()))
		fail(fmt.Errorf("This indexer is not configured."))
		writeReply()
		return
	}
	rawPath, err := decodeProxyPath(pathParam)
	if err != nil {
		fail(err)
		writeReply()
		return
	}
	remoteFile, err := url.Parse(rawPath)
	if err != nil {
		fail(err)
		writeReply()
		return
	}
	fileExtension := ".torrent"

	var downloadBytes []byte
	if strings.HasPrefix(remoteFile.String(), "magnet") {
		downloadBytes = []byte(remoteFile.String())
	} else {
		downloadBytes, err = idx.Download(r.Context(), remoteFile)
		if err != nil {
			fail(err)
			writeReply()
			return
		}
	}

	if magnetBytes(downloadBytes) {
		fileExtension = ".magnet"
	}

	blackholeDir := h.Config.Config().BlackholeDir
	if strings.TrimSpace(blackholeDir) == "" {
		fail(fmt.Errorf("Blackhole directory not set!"))
		writeReply()
		return
	}
	if st, err := os.Stat(blackholeDir); err != nil || !st.IsDir() {
		fail(fmt.Errorf("Blackhole directory does not exist: %s", blackholeDir))
		writeReply()
		return
	}

	fileName := fmt.Sprint(dotNetTicksNow()) + "-" + stringutil.MakeValidFileName(idx.Name(), '_', false)
	if strings.TrimSpace(file) == "" {
		fileName += fileExtension
	} else {
		fileName += "-" + stringutil.MakeValidFileName(file+fileExtension, '_', false)
	}

	if err := os.WriteFile(filepath.Join(blackholeDir, fileName), downloadBytes, 0o644); err != nil {
		// upstream catches IOException only (e.g. name too long) and falls
		// back to a GUID name
		if isIOException(err) {
			if err := os.WriteFile(filepath.Join(blackholeDir, fmt.Sprint(newGUID())+fileExtension), downloadBytes, 0o644); err != nil {
				fail(err)
				writeReply()
				return
			}
		} else {
			fail(err)
			writeReply()
			return
		}
	}
	writeReply()
}

// dotNetTicksNow mirrors DateTime.Now.Ticks: 100-ns intervals since
// 0001-01-01T00:00:00.
func dotNetTicksNow() int64 {
	return time.Now().UnixNano()/100 + 621355968000000000
}

// newGUID is a random GUID string for the blackhole IOException fallback.
func newGUID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// isIOException reports whether err is a filesystem error of the kind
// upstream's IOException catch would match (path/name problems), as opposed
// to permission or existence errors which surface as generic exceptions.
func isIOException(err error) bool {
	return errors.Is(err, syscall.ENAMETOOLONG) ||
		errors.Is(err, syscall.EINVAL) ||
		errors.Is(err, syscall.EIO)
}
