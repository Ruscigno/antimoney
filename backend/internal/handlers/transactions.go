package handlers

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/user/antimoney/internal/services"
)

type TransactionHandler struct {
	svc *services.TransactionService
}

func NewTransactionHandler(svc *services.TransactionService) *TransactionHandler {
	return &TransactionHandler{svc: svc}
}

func (h *TransactionHandler) Routes() chi.Router {
	r := chi.NewRouter()
	r.Get("/", h.list)
	r.Post("/", h.create)
	r.Get("/{id}", h.get)
	r.Put("/{id}", h.update)
	r.Delete("/{id}", h.delete)
	r.Post("/splits/reconcile", h.batchReconcile)
	r.Patch("/splits/{splitId}/toggle", h.toggleAcknowledge)
	return r
}

func (h *TransactionHandler) list(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))

	txns, err := h.svc.ListTransactions(r.Context(), limit, offset)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, txns)
}

func (h *TransactionHandler) create(w http.ResponseWriter, r *http.Request) {
	var req services.CreateTransactionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	txn, err := h.svc.CreateTransaction(r.Context(), req)
	if err != nil {
		if errors.Is(err, services.ErrUnbalancedTransaction) ||
			errors.Is(err, services.ErrPlaceholderAccount) ||
			errors.Is(err, services.ErrInvalidSplit) ||
			errors.Is(err, services.ErrTooFewSplits) {
			writeError(w, http.StatusUnprocessableEntity, err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusCreated, txn)
}

func (h *TransactionHandler) get(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	txn, err := h.svc.GetTransaction(r.Context(), id)
	if err != nil {
		if errors.Is(err, services.ErrNotFound) {
			writeError(w, http.StatusNotFound, "transaction not found")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, txn)
}

func (h *TransactionHandler) update(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var req services.UpdateTransactionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	txn, err := h.svc.UpdateTransaction(r.Context(), id, req)
	if err != nil {
		if errors.Is(err, services.ErrNotFound) {
			writeError(w, http.StatusNotFound, "transaction not found")
			return
		}
		if errors.Is(err, services.ErrUnbalancedTransaction) ||
			errors.Is(err, services.ErrPlaceholderAccount) ||
			errors.Is(err, services.ErrInvalidSplit) ||
			errors.Is(err, services.ErrTooFewSplits) {
			writeError(w, http.StatusUnprocessableEntity, err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, txn)
}

func (h *TransactionHandler) delete(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if err := h.svc.DeleteTransaction(r.Context(), id); err != nil {
		if errors.Is(err, services.ErrNotFound) {
			writeError(w, http.StatusNotFound, "transaction not found")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
func (h *TransactionHandler) batchReconcile(w http.ResponseWriter, r *http.Request) {
	var req struct {
		SplitGUIDs []string `json:"split_guids"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if err := h.svc.BatchReconcileSplits(r.Context(), req.SplitGUIDs); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *TransactionHandler) toggleAcknowledge(w http.ResponseWriter, r *http.Request) {
	splitID := chi.URLParam(r, "splitId")

	var req struct {
		State string `json:"state"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if err := h.svc.ToggleSplitAcknowledge(r.Context(), splitID, req.State); err != nil {
		if errors.Is(err, services.ErrNotFound) {
			writeError(w, http.StatusNotFound, "split not found")
			return
		}
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
