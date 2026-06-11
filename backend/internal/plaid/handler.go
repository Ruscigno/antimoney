package plaid

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/user/antimoney/internal/auth"
	"github.com/user/antimoney/internal/handlers"
)

// rateLimiter is the slice of the ratelimit API the handler needs — an
// interface so tests can stub the 429 path without a Redis instance.
// *ratelimit.Limiter satisfies it (including with a nil receiver: fail-open).
type rateLimiter interface {
	AllowN(ctx context.Context, key string, perMinute int) bool
}

// isUUID rejects malformed ids at the boundary — a non-UUID would otherwise
// fail the Postgres uuid cast deep in the service and surface as a 500.
func isUUID(s string) bool {
	_, err := uuid.Parse(s)
	return err == nil
}

// PlaidHandler is the thin HTTP layer over PlaidService.
type PlaidHandler struct {
	svc     *PlaidService
	limiter rateLimiter // nil-safe: rate checks fail open without Redis
}

func NewPlaidHandler(svc *PlaidService, limiter rateLimiter) *PlaidHandler {
	return &PlaidHandler{svc: svc, limiter: limiter}
}

// Per-user per-minute caps: Plaid calls are metered, so authenticated cost
// amplification needs a ceiling. Generous for humans, tight for loops.
const (
	linkTokenPerMinute = 10
	exchangePerMinute  = 10
	syncPerMinute      = 30
	maxLinkMappings    = 100
)

func (h *PlaidHandler) allow(r *http.Request, op string, perMinute int) bool {
	if h.limiter == nil {
		return true
	}
	return h.limiter.AllowN(r.Context(), "plaid:"+op+":"+auth.UserIDFromCtx(r.Context()), perMinute)
}

func (h *PlaidHandler) Routes() chi.Router {
	r := chi.NewRouter()
	r.Post("/link-token", h.handleLinkToken)
	r.Post("/exchange", h.handleExchange)
	r.Post("/link", h.handleLink)
	r.Post("/sync", h.handleSync)
	r.Post("/import", h.handleImport)
	r.Post("/dismiss", h.handleDismiss)
	r.Delete("/items/{guid}", h.handleDisconnect)
	r.Get("/items", h.handleListItems)
	return r
}

func (h *PlaidHandler) handleLinkToken(w http.ResponseWriter, r *http.Request) {
	if !h.allow(r, "lt", linkTokenPerMinute) {
		handlers.WriteErrorPublic(w, http.StatusTooManyRequests, "rate limit exceeded")
		return
	}
	// Optional body: {"language": "<app locale>"} — whitelisted to the Plaid
	// Link languages the app's locales map to; anything else falls back to en.
	var req struct {
		Language string `json:"language"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	language := "en"
	if strings.HasPrefix(strings.ToLower(req.Language), "pt") {
		language = "pt"
	}
	token, err := h.svc.CreateLinkToken(r.Context(), language)
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
	// Exchange fires two metered Plaid calls — same cost ceiling as link-token.
	if !h.allow(r, "exchange", exchangePerMinute) {
		handlers.WriteErrorPublic(w, http.StatusTooManyRequests, "rate limit exceeded")
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
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || !isUUID(req.ItemGUID) {
		handlers.WriteErrorPublic(w, http.StatusBadRequest, "item_guid is required")
		return
	}
	if len(req.Mappings) > maxLinkMappings {
		handlers.WriteErrorPublic(w, http.StatusBadRequest, "too many mappings (max 100)")
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
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || !isUUID(req.ItemGUID) {
		handlers.WriteErrorPublic(w, http.StatusBadRequest, "item_guid is required")
		return
	}
	if !h.allow(r, "sync", syncPerMinute) {
		handlers.WriteErrorPublic(w, http.StatusTooManyRequests, "rate limit exceeded")
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
		if result != nil && (result.Imported > 0 || len(result.Failed) > 0) {
			// Mid-batch failure: 207 Multi-Status carries the partial progress —
			// neither a lying 200 nor a blanket 500 (already-imported rows are
			// not a total failure, and a retry is safe via server-side dedupe).
			handlers.WriteJSONPublic(w, http.StatusMultiStatus, map[string]interface{}{
				"imported": result.Imported,
				"failed":   result.Failed,
				"error":    "import interrupted — retry for the remaining rows",
			})
			return
		}
		handlers.WriteErrorPublic(w, http.StatusInternalServerError, "import failed")
		return
	}
	handlers.WriteJSONPublic(w, http.StatusOK, result)
}

func (h *PlaidHandler) handleDismiss(w http.ResponseWriter, r *http.Request) {
	var req struct {
		TransactionIDs []string `json:"transaction_ids"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || len(req.TransactionIDs) == 0 {
		handlers.WriteErrorPublic(w, http.StatusBadRequest, "transaction_ids is required")
		return
	}
	if len(req.TransactionIDs) > maxImportRows {
		handlers.WriteErrorPublic(w, http.StatusBadRequest, "too many ids (max 500)")
		return
	}
	n, err := h.svc.DismissStaged(r.Context(), req.TransactionIDs)
	if err != nil {
		log.Printf("plaid dismiss: %v", err)
		handlers.WriteErrorPublic(w, http.StatusInternalServerError, "dismiss failed")
		return
	}
	handlers.WriteJSONPublic(w, http.StatusOK, map[string]int{"dismissed": n})
}

func (h *PlaidHandler) handleDisconnect(w http.ResponseWriter, r *http.Request) {
	itemGUID := chi.URLParam(r, "guid")
	if !isUUID(itemGUID) {
		handlers.WriteErrorPublic(w, http.StatusNotFound, "item not found")
		return
	}
	if err := h.svc.Disconnect(r.Context(), itemGUID); err != nil {
		if errors.Is(err, ErrItemNotFound) {
			handlers.WriteErrorPublic(w, http.StatusNotFound, "item not found")
			return
		}
		// Actionable outcomes get actionable statuses (precedent:
		// reconnect_required). The frontend shows a generic message either way;
		// the machine-readable code tells an operator what to actually do.
		if errors.Is(err, ErrLegacyTokenNeedsFlag) {
			handlers.WriteErrorPublic(w, http.StatusConflict, "legacy_token_needs_flag")
			return
		}
		if errors.Is(err, ErrConcurrentModification) {
			// Telemetry: contention here means disconnects racing re-links —
			// worth seeing in logs even though the client just retries.
			log.Printf("plaid disconnect: concurrent modification on item %s; client told to retry", itemGUID)
			handlers.WriteErrorPublic(w, http.StatusConflict, "conflict_retry")
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
