package handlers

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/user/antimoney/internal/services"
)

type CommodityHandler struct {
	svc *services.CommodityService
}

func NewCommodityHandler(svc *services.CommodityService) *CommodityHandler {
	return &CommodityHandler{svc: svc}
}

func (h *CommodityHandler) Routes() chi.Router {
	r := chi.NewRouter()
	r.Get("/", h.list)
	r.Post("/", h.create)
	r.Delete("/{id}", h.delete)
	return r
}

func (h *CommodityHandler) list(w http.ResponseWriter, r *http.Request) {
	commodities, err := h.svc.ListCommodities(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, commodities)
}

func (h *CommodityHandler) create(w http.ResponseWriter, r *http.Request) {
	var req services.CreateCommodityRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	commodity, err := h.svc.CreateCommodity(r.Context(), req)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, commodity)
}

func (h *CommodityHandler) delete(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if err := h.svc.DeleteCommodity(r.Context(), id); err != nil {
		if errors.Is(err, services.ErrNotFound) {
			writeError(w, http.StatusNotFound, "commodity not found")
		} else {
			writeError(w, http.StatusInternalServerError, err.Error())
		}
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
