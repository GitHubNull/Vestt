// IndexerApiController ports upstream IndexerApiController.cs: the indexer
// list DTO, config get/post, test, delete and the Cache view.
//
// Ported from: Jackett.Server/Controllers/IndexerApiController.cs.
package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"

	"vestt/common/indexer"
	"vestt/common/models"
	"vestt/common/services"
	"vestt/common/utils/stringutil"
)

// IndexerHandlers serves /api/v2.0/indexers (admin session-guarded by the
// server wiring, upstream [Authorize]).
type IndexerHandlers struct {
	Config  *services.ConfigService
	Manager *services.IndexerManagerService
	Cache   *services.CacheService
	Logger  *slog.Logger
}

func (h *IndexerHandlers) logger() *slog.Logger {
	if h.Logger != nil {
		return h.Logger
	}
	return slog.Default()
}

func (h *IndexerHandlers) Routes(r chi.Router) {
	r.Get("/", h.Indexers)
	// static segment beats {indexerId} at the same level (chi routing).
	r.Route("/{indexerId}", func(r chi.Router) {
		r.Get("/Config", h.GetConfig)
		r.Post("/Config", h.UpdateConfig)
		r.Post("/Test", h.Test)
		r.Delete("/", h.Delete)
	})
	r.Get("/Cache", h.CacheView)
}

// currentIndexer resolves the route's indexer (upstream RequiresIndexer):
// GetIndexer throwing is a 500 ("Unknown indexer: X"), never a 404.
func (h *IndexerHandlers) currentIndexer(w http.ResponseWriter, r *http.Request) (indexer.Indexer, bool) {
	id := chi.URLParam(r, "indexerId")
	idx, err := h.Manager.GetIndexer(id)
	if err != nil {
		h.logger().Error(fmt.Sprintf("Error resolving indexer %s: %v", id, err))
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return nil, false
	}
	return idx, true
}

// decodeJSON decodes a JSON request body (upstream [FromBody] model
// binding).
func decodeJSON(r *http.Request, v any) error {
	return json.NewDecoder(r.Body).Decode(v)
}

// capability is one caps entry of the indexer list DTO (upstream
// DTO/Indexer.cs Capability: ID as string, Name).
type capability struct {
	ID   string `json:"ID"`
	Name string `json:"Name"`
}

// indexerDTO mirrors upstream DTO/Indexer.cs field-for-field (lowercase
// names, PascalCase caps entries).
type indexerDTO struct {
	ID                   string       `json:"id"`
	Name                 string       `json:"name"`
	Description          string       `json:"description"`
	Type                 string       `json:"type"`
	Configured           bool         `json:"configured"`
	SiteLink             string       `json:"site_link"`
	AlternativeSiteLinks []string     `json:"alternativesitelinks"`
	Language             string       `json:"language"`
	Tags                 []string     `json:"tags"`
	LastError            string       `json:"last_error"`
	PotatoEnabled        bool         `json:"potatoenabled"`
	Caps                 []capability `json:"caps"`
}

// Indexers serves GET /api/v2.0/indexers?configured= (upstream Indexers
// action over IndexerService.GetAllIndexers — non-meta, Name-ordered).
func (h *IndexerHandlers) Indexers(w http.ResponseWriter, r *http.Request) {
	noStore(w)
	configuredOnly := r.URL.Query().Get("configured") == "true"
	dto := make([]indexerDTO, 0)
	for _, idx := range h.Manager.GetAllIndexers() {
		if configuredOnly && !idx.IsConfigured() {
			continue
		}
		caps := idx.Capabilities()
		potato := false
		var capsList []capability
		if caps != nil {
			for _, c := range caps.Categories.Tree() {
				if models.CatMovies.Contains(c) {
					potato = true
				}
			}
			for _, c := range caps.Categories.FlatList(true) {
				capsList = append(capsList, capability{ID: fmt.Sprint(c.ID), Name: c.Name})
			}
		}
		if capsList == nil {
			capsList = []capability{}
		}
		tags := idx.Tags()
		if tags == nil {
			tags = []string{}
		}
		alts := idx.AlternativeSiteLinks()
		if alts == nil {
			alts = []string{}
		}
		lastError := idx.LastError()
		dto = append(dto, indexerDTO{
			ID:                   idx.ID(),
			Name:                 idx.Name(),
			Description:          idx.Description(),
			Type:                 idx.Type(),
			Configured:           idx.IsConfigured(),
			SiteLink:             idx.SiteLink(),
			AlternativeSiteLinks: alts,
			Language:             idx.Language(),
			Tags:                 tags,
			LastError:            lastError,
			PotatoEnabled:        potato,
			Caps:                 capsList,
		})
	}
	writeJSON(w, http.StatusOK, dto)
}

// Config serves GET {indexerId}/Config (upstream GetConfigurationForSetup).
func (h *IndexerHandlers) GetConfig(w http.ResponseWriter, r *http.Request) {
	noStore(w)
	idx, ok := h.currentIndexer(w, r)
	if !ok {
		return
	}
	form, err := idx.ConfigurationForSetup(r.Context())
	if err != nil {
		h.logger().Error(fmt.Sprintf("Error getting configuration for %s: %v", idx.ID(), err))
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if form == nil {
		form = []map[string]any{}
	}
	writeJSON(w, http.StatusOK, form)
}

// UpdateConfig serves POST {indexerId}/Config (upstream UpdateConfig): any
// failure resets the base config and rethrows → 500 (not 400).
func (h *IndexerHandlers) UpdateConfig(w http.ResponseWriter, r *http.Request) {
	noStore(w)
	idx, ok := h.currentIndexer(w, r)
	if !ok {
		return
	}
	var items []indexer.ConfigItem
	if err := decodeJSON(r, &items); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := h.Manager.ConfigureIndexer(r.Context(), idx.ID(), items); err != nil {
		h.logger().Error(fmt.Sprintf("Error updating configuration for %s: %v", idx.ID(), err))
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// Test serves POST {indexerId}/Test (upstream Test): success clears
// LastError and returns 204; failure composes "{msg}: {inner}" into
// LastError and rethrows → 500.
func (h *IndexerHandlers) Test(w http.ResponseWriter, r *http.Request) {
	noStore(w)
	idx, ok := h.currentIndexer(w, r)
	if !ok {
		return
	}
	if err := h.Manager.TestIndexer(r.Context(), idx.ID()); err != nil {
		msg := err.Error()
		if inner := errors.Unwrap(err); inner != nil {
			msg += ": " + inner.Error()
		}
		idx.SetLastError(msg)
		h.logger().Error(fmt.Sprintf("Error testing indexer %s: %v", idx.ID(), err))
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	idx.SetLastError("")
	w.WriteHeader(http.StatusNoContent)
}

// Delete serves DELETE {indexerId} (upstream Delete action).
func (h *IndexerHandlers) Delete(w http.ResponseWriter, r *http.Request) {
	noStore(w)
	idx, ok := h.currentIndexer(w, r)
	if !ok {
		return
	}
	if err := h.Manager.DeleteIndexer(r.Context(), idx.ID()); err != nil {
		h.logger().Error(fmt.Sprintf("Error deleting indexer %s: %v", idx.ID(), err))
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// CacheView serves GET /api/v2.0/indexers/Cache (upstream Cache action):
// TrackerCacheResult list with proxy links; the blackhole link only exists
// for a non-magnet original link when a blackhole directory is configured
// (the IndexerApiController variant — no MagnetUri fallback).
func (h *IndexerHandlers) CacheView(w http.ResponseWriter, r *http.Request) {
	noStore(w)
	results := h.Cache.GetCachedResults(r.Context())
	serverURL := GetServerUrl(h.Config, r)
	blackhole := h.Config.Config().BlackholeDir != ""
	for _, res := range results {
		link := res.Link
		file := stringutil.MakeValidFileName(res.Title, '_', false)
		res.Link = ConvertToProxyLink(h.Config, link, serverURL, res.TrackerId, "dl", file)
		res.Poster = ConvertToProxyLink(h.Config, res.Poster, serverURL, res.TrackerId, "img", "poster")
		if link != nil && link.Scheme != "magnet" && blackhole {
			res.BlackholeLink = ConvertToProxyLink(h.Config, link, serverURL, res.TrackerId, "bh", file)
		}
	}
	writeJSON(w, http.StatusOK, results)
}
