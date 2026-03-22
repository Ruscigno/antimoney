package handlers

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/user/antimoney/internal/services"
)

type AccountHandler struct {
	accountSvc *services.AccountService
	txSvc      *services.TransactionService
}

func NewAccountHandler(accountSvc *services.AccountService, txSvc *services.TransactionService) *AccountHandler {
	return &AccountHandler{accountSvc: accountSvc, txSvc: txSvc}
}

func (h *AccountHandler) Routes() chi.Router {
	r := chi.NewRouter()
	r.Get("/", h.list)
	r.Post("/", h.create)
	r.Get("/{id}", h.get)
	r.Put("/{id}", h.update)
	r.Delete("/{id}", h.delete)
	r.Get("/{id}/register", h.register)
	r.Get("/{id}/reconciled-balance", h.reconciledBalance)
	r.Post("/{id}/reconcile", h.reconcileAccount)
	return r
}

func (h *AccountHandler) list(w http.ResponseWriter, r *http.Request) {
	start := r.URL.Query().Get("start")
	end := r.URL.Query().Get("end")
	accounts, err := h.accountSvc.ListAccountsTree(r.Context(), start, end)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, accounts)
}

func (h *AccountHandler) create(w http.ResponseWriter, r *http.Request) {
	var req services.CreateAccountRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	account, err := h.accountSvc.CreateAccount(r.Context(), req)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, account)
}

func (h *AccountHandler) get(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	account, err := h.accountSvc.GetAccount(r.Context(), id)
	if err != nil {
		if errors.Is(err, services.ErrNotFound) {
			writeError(w, http.StatusNotFound, "account not found")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, account)
}

func (h *AccountHandler) update(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var req services.UpdateAccountRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	account, err := h.accountSvc.UpdateAccount(r.Context(), id, req)
	if err != nil {
		if errors.Is(err, services.ErrNotFound) {
			writeError(w, http.StatusNotFound, "account not found")
			return
		}
		if errors.Is(err, services.ErrVersionConflict) {
			writeError(w, http.StatusConflict, "version conflict: record was modified by another user")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, account)
}

func (h *AccountHandler) delete(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if err := h.accountSvc.DeleteAccount(r.Context(), id); err != nil {
		if errors.Is(err, services.ErrNotFound) {
			writeError(w, http.StatusNotFound, "account not found")
			return
		}
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *AccountHandler) register(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")

	cursorDate := r.URL.Query().Get("cursor_date")
	direction := r.URL.Query().Get("direction")
	limitStr := r.URL.Query().Get("limit")

	limit := 50
	if limitStr != "" {
		if l, err := strconv.Atoi(limitStr); err == nil && l > 0 {
			limit = l
		}
	}

	// If no cursor_date, fall back to loading everything (for backwards compat)
	if cursorDate == "" {
		entries, err := h.txSvc.GetAccountRegister(r.Context(), id)
		if err != nil {
			if errors.Is(err, services.ErrNotFound) {
				writeError(w, http.StatusNotFound, "account not found")
				return
			}
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, entries)
		return
	}

	if direction == "" {
		direction = "around"
	}

	result, err := h.txSvc.GetAccountRegisterPaged(r.Context(), id, cursorDate, direction, limit)
	if err != nil {
		if errors.Is(err, services.ErrNotFound) {
			writeError(w, http.StatusNotFound, "account not found")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (h *AccountHandler) reconciledBalance(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	balance, err := h.txSvc.GetReconciledBalance(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]float64{"balance": balance})
}

func (h *AccountHandler) reconcileAccount(w http.ResponseWriter, r *http.Request) {
	var req struct {
		AccountGUIDs []string `json:"account_guids"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if len(req.AccountGUIDs) == 0 {
		writeError(w, http.StatusBadRequest, "account_guids required")
		return
	}

	count, err := h.txSvc.ReconcileAccountSplits(r.Context(), req.AccountGUIDs)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]int64{"reconciled": count})
}
