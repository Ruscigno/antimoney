package plaid

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/user/antimoney/internal/handlers"
)

// PlaidHandler is the thin HTTP layer over PlaidService.
type PlaidHandler struct {
	svc *PlaidService
}

func NewPlaidHandler(svc *PlaidService) *PlaidHandler {
	return &PlaidHandler{svc: svc}
}

func (h *PlaidHandler) Routes() chi.Router {
	r := chi.NewRouter()
	r.Post("/link-token", h.handleLinkToken)
	r.Post("/exchange", h.handleExchange)
	r.Post("/link", h.handleLink)
	r.Post("/sync", h.handleSync)
	r.Post("/import", h.handleImport)
	r.Delete("/items/{guid}", h.handleDisconnect)
	r.Get("/items", h.handleListItems)
	return r
}

func (h *PlaidHandler) handleLinkToken(w http.ResponseWriter, r *http.Request) {
	token, err := h.svc.CreateLinkToken(r.Context())
	if err != nil {
		log.Printf("plaid link-token: %v", err)
		handlers.WriteErrorPublic(w, http.StatusInternalServerError, "could not create link token")
		return
	}
	handlers.WriteJSONPublic(w, http.StatusOK, map[string]string{"link_token": token})
}

func (h *PlaidHandler) handleExchange(w http.ResponseWriter, r *http.Request) {
	var req struct {
		PublicToken string `json:"public_token"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.PublicToken == "" {
		handlers.WriteErrorPublic(w, http.StatusBadRequest, "public_token is required")
		return
	}
	result, err := h.svc.Exchange(r.Context(), req.PublicToken)
	if err != nil {
		log.Printf("plaid exchange: %v", err)
		handlers.WriteErrorPublic(w, http.StatusInternalServerError, "exchange failed")
		return
	}
	handlers.WriteJSONPublic(w, http.StatusOK, map[string]interface{}{
		"item_guid":        result.ItemGUID,
		"institution_name": result.InstitutionName,
		"accounts":         result.Accounts,
	})
}

func (h *PlaidHandler) handleLink(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ItemGUID      string           `json:"item_guid"`
		Mappings      []AccountMapping `json:"mappings"`
		ImportPending bool             `json:"import_pending"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ItemGUID == "" {
		handlers.WriteErrorPublic(w, http.StatusBadRequest, "item_guid is required")
		return
	}
	if err := h.svc.LinkAccounts(r.Context(), req.ItemGUID, req.Mappings, req.ImportPending); err != nil {
		if errors.Is(err, ErrDuplicateLink) {
			handlers.WriteErrorPublic(w, http.StatusConflict, "account already linked")
			return
		}
		if errors.Is(err, ErrAccountAlreadyLinked) {
			handlers.WriteErrorPublic(w, http.StatusConflict, "account already linked to a bank")
			return
		}
		if errors.Is(err, ErrItemNotFound) {
			handlers.WriteErrorPublic(w, http.StatusNotFound, "item not found")
			return
		}
		if errors.Is(err, ErrAccountNotFound) {
			handlers.WriteErrorPublic(w, http.StatusNotFound, "account not found")
			return
		}
		log.Printf("plaid link: %v", err)
		handlers.WriteErrorPublic(w, http.StatusInternalServerError, "link failed")
		return
	}
	handlers.WriteJSONPublic(w, http.StatusOK, map[string]string{"message": "linked"})
}

func (h *PlaidHandler) handleSync(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ItemGUID string `json:"item_guid"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ItemGUID == "" {
		handlers.WriteErrorPublic(w, http.StatusBadRequest, "item_guid is required")
		return
	}
	result, err := h.svc.Sync(r.Context(), req.ItemGUID)
	if err != nil {
		if errors.Is(err, ErrItemNotFound) {
			handlers.WriteErrorPublic(w, http.StatusNotFound, "item not found")
			return
		}
		if errors.Is(err, ErrReauthRequired) {
			// The frontend matches this exact error string to show the
			// "reconnect your bank" message instead of a generic failure.
			handlers.WriteErrorPublic(w, http.StatusConflict, "reconnect_required")
			return
		}
		log.Printf("plaid sync: %v", err)
		handlers.WriteErrorPublic(w, http.StatusInternalServerError, "sync failed")
		return
	}
	handlers.WriteJSONPublic(w, http.StatusOK, result)
}

// maxImportRows bounds a single /import call so it cannot blow through the
// 30s request timeout mid-way and leave a partial import behind.
const maxImportRows = 500

func (h *PlaidHandler) handleImport(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Rows []ImportRow `json:"rows"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		handlers.WriteErrorPublic(w, http.StatusBadRequest, "invalid request")
		return
	}
	if len(req.Rows) == 0 {
		handlers.WriteErrorPublic(w, http.StatusBadRequest, "rows is required")
		return
	}
	if len(req.Rows) > maxImportRows {
		handlers.WriteErrorPublic(w, http.StatusBadRequest, "too many rows (max 500)")
		return
	}
	result, err := h.svc.Import(r.Context(), req.Rows)
	if err != nil {
		log.Printf("plaid import: %v", err)
		handlers.WriteErrorPublic(w, http.StatusInternalServerError, "import failed")
		return
	}
	handlers.WriteJSONPublic(w, http.StatusOK, result)
}

func (h *PlaidHandler) handleDisconnect(w http.ResponseWriter, r *http.Request) {
	itemGUID := chi.URLParam(r, "guid")
	if err := h.svc.Disconnect(r.Context(), itemGUID); err != nil {
		if errors.Is(err, ErrItemNotFound) {
			handlers.WriteErrorPublic(w, http.StatusNotFound, "item not found")
			return
		}
		log.Printf("plaid disconnect: %v", err)
		handlers.WriteErrorPublic(w, http.StatusInternalServerError, "disconnect failed")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *PlaidHandler) handleListItems(w http.ResponseWriter, r *http.Request) {
	items, err := h.svc.ListItems(r.Context())
	if err != nil {
		log.Printf("plaid list items: %v", err)
		handlers.WriteErrorPublic(w, http.StatusInternalServerError, "list failed")
		return
	}
	handlers.WriteJSONPublic(w, http.StatusOK, map[string]interface{}{"items": items})
}
