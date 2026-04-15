package handlers

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/user/antimoney/internal/auth"
	"github.com/user/antimoney/internal/models"
	"github.com/user/antimoney/internal/services"
)

type SnapshotHandler struct {
	svc  *services.SnapshotService
	pool interface {
		// Used only for restore — injected via NewSnapshotHandler.
	}
	importExport *ImportExportHandler
}

func NewSnapshotHandler(svc *services.SnapshotService, ie *ImportExportHandler) *SnapshotHandler {
	return &SnapshotHandler{svc: svc, importExport: ie}
}

func (h *SnapshotHandler) Routes() chi.Router {
	r := chi.NewRouter()
	r.Get("/config", h.handleGetConfig)
	r.Put("/config", h.handleUpsertConfig)
	r.Get("/", h.handleList)
	r.Post("/", h.handleCreate)
	r.Get("/{id}", h.handleGet)
	r.Post("/{id}/restore", h.handleRestore)
	r.Delete("/{id}", h.handleDelete)
	return r
}

// GET /api/snapshots/config
func (h *SnapshotHandler) handleGetConfig(w http.ResponseWriter, r *http.Request) {
	cfg, err := h.svc.GetConfig(r.Context())
	if errors.Is(err, services.ErrSnapshotConfigNotFound) {
		writeError(w, http.StatusNotFound, "no config found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, cfg)
}

// PUT /api/snapshots/config
func (h *SnapshotHandler) handleUpsertConfig(w http.ResponseWriter, r *http.Request) {
	var req struct {
		FrequencyHours int  `json:"frequency_hours"`
		TTLHours       int  `json:"ttl_hours"`
		ActiveMode     bool `json:"active_mode"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.FrequencyHours < 0 || req.TTLHours < 0 {
		writeError(w, http.StatusBadRequest, "frequency_hours and ttl_hours must be >= 0")
		return
	}

	cfg, err := h.svc.UpsertConfig(r.Context(), req.FrequencyHours, req.TTLHours, req.ActiveMode)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, cfg)
}

// GET /api/snapshots
func (h *SnapshotHandler) handleList(w http.ResponseWriter, r *http.Request) {
	list, err := h.svc.ListSnapshots(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, list)
}

// POST /api/snapshots
func (h *SnapshotHandler) handleCreate(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Label string `json:"label"`
	}
	// Label is optional — ignore decode errors.
	json.NewDecoder(r.Body).Decode(&req)

	ss, err := h.svc.TakeSnapshot(r.Context(), req.Label, models.SnapshotTriggerManual)
	if err != nil {
		log.Printf("snapshot create failed: %v", err)
		writeError(w, http.StatusInternalServerError, "failed to take snapshot")
		return
	}
	writeJSON(w, http.StatusCreated, ss)
}

// GET /api/snapshots/{id}
func (h *SnapshotHandler) handleGet(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	snap, err := h.svc.GetSnapshot(r.Context(), id)
	if errors.Is(err, services.ErrSnapshotNotFound) {
		writeError(w, http.StatusNotFound, "snapshot not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, snap)
}

// POST /api/snapshots/{id}/restore
func (h *SnapshotHandler) handleRestore(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	snap, err := h.svc.GetSnapshot(r.Context(), id)
	if errors.Is(err, services.ErrSnapshotNotFound) {
		writeError(w, http.StatusNotFound, "snapshot not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	var data ExportData
	if err := json.Unmarshal(snap.Data, &data); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to parse snapshot data")
		return
	}

	// Take an instant snapshot before making any change
	if _, err := h.svc.TakeSnapshot(r.Context(), "Auto-backup before restore", models.SnapshotTriggerManual); err != nil {
		log.Printf("snapshot auto-backup failed: %v", err)
		writeError(w, http.StatusInternalServerError, "failed to create auto-backup before restore")
		return
	}

	bookGUID := auth.BookGUIDFromCtx(r.Context())
	if err := performImport(r.Context(), h.importExport.pool, bookGUID, data); err != nil {
		log.Printf("snapshot restore failed: %v", err)
		writeError(w, http.StatusInternalServerError, "restore failed")
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"message": "restore successful"})
}

// DELETE /api/snapshots/{id}
func (h *SnapshotHandler) handleDelete(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	err := h.svc.DeleteSnapshot(r.Context(), id)
	if errors.Is(err, services.ErrSnapshotNotFound) {
		writeError(w, http.StatusNotFound, "snapshot not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusNoContent, nil)
}
