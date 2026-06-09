package plaid

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/user/antimoney/internal/auth"
	"github.com/user/antimoney/internal/models"
	"github.com/user/antimoney/internal/services"
	"github.com/user/antimoney/internal/testutil"
)

func setupTestHandler(t *testing.T) (*PlaidHandler, *testutil.TestDB, string, string) {
	t.Helper()
	ctx := context.Background()
	db, err := testutil.SetupDB(ctx, "../../migrations")
	if err != nil {
		t.Fatal(err)
	}

	authSvc := auth.NewUserService(db.Pool)
	res, err := authSvc.Register(ctx, auth.RegisterRequest{Email: "plaid@test.com", Password: "pass", Name: "Plaid"})
	if err != nil {
		t.Fatal(err)
	}

	key := make([]byte, 32)
	encKey := base64.StdEncoding.EncodeToString(key)

	txSvc := services.NewTransactionService(db.Pool)
	svc, err := NewPlaidService(db.Pool, newFakeClient(), encKey, txSvc)
	if err != nil {
		t.Fatal(err)
	}
	return NewPlaidHandler(svc), db, res.BookGUID, res.UserID
}

func authedRequest(method, path string, body interface{}, bookGUID, userID string) *http.Request {
	var buf bytes.Buffer
	if body != nil {
		json.NewEncoder(&buf).Encode(body)
	}
	r := httptest.NewRequest(method, path, &buf)
	r.Header.Set("Content-Type", "application/json")
	ctx := context.WithValue(r.Context(), auth.BookGUIDKey, bookGUID)
	ctx = context.WithValue(ctx, auth.UserIDKey, userID)
	return r.WithContext(ctx)
}

func TestPlaidExchangeAndSync(t *testing.T) {
	h, db, bookGUID, userID := setupTestHandler(t)
	defer db.Teardown(context.Background())

	accSvc := services.NewAccountService(db.Pool)
	ctx := context.WithValue(context.Background(), auth.BookGUIDKey, bookGUID)
	bankAcc, _ := accSvc.CreateAccount(ctx, services.CreateAccountRequest{
		Name: "Chequing", AccountType: models.AccountTypeBank,
	})
	expAcc, _ := accSvc.CreateAccount(ctx, services.CreateAccountRequest{
		Name: "Food", AccountType: models.AccountTypeExpense,
	})

	// POST /exchange
	w := httptest.NewRecorder()
	h.handleExchange(w, authedRequest("POST", "/exchange", map[string]string{"public_token": "tok"}, bookGUID, userID))
	if w.Code != http.StatusOK {
		t.Fatalf("exchange: got %d, want 200: %s", w.Code, w.Body)
	}
	var exchangeResp struct {
		ItemGUID string `json:"item_guid"`
	}
	json.NewDecoder(w.Body).Decode(&exchangeResp)
	if exchangeResp.ItemGUID == "" {
		t.Fatal("expected item_guid in response")
	}

	// POST /link
	w = httptest.NewRecorder()
	h.handleLink(w, authedRequest("POST", "/link", map[string]interface{}{
		"item_guid": exchangeResp.ItemGUID,
		"mappings": []map[string]string{
			{"account_id": "plaid-acct-001", "account_guid": bankAcc.GUID},
		},
		"import_pending": false,
	}, bookGUID, userID))
	if w.Code != http.StatusOK {
		t.Fatalf("link: got %d: %s", w.Code, w.Body)
	}

	// POST /sync
	w = httptest.NewRecorder()
	h.handleSync(w, authedRequest("POST", "/sync", map[string]string{"item_guid": exchangeResp.ItemGUID}, bookGUID, userID))
	if w.Code != http.StatusOK {
		t.Fatalf("sync: got %d: %s", w.Code, w.Body)
	}
	var syncResp SyncResult
	json.NewDecoder(w.Body).Decode(&syncResp)
	if syncResp.Count != 2 {
		t.Fatalf("expected 2 suggestions, got %d", syncResp.Count)
	}

	// POST /import
	rows := make([]ImportRow, 0, len(syncResp.Suggestions))
	for _, s := range syncResp.Suggestions {
		rows = append(rows, ImportRow{
			TransactionID:       s.TransactionID,
			BankAccountGUID:     s.BankAccountGUID,
			CategoryAccountGUID: expAcc.GUID,
			Description:         s.Description,
			Date:                s.Date,
			AmountNum:           s.AmountNum,
			AmountDenom:         s.AmountDenom,
		})
	}
	w = httptest.NewRecorder()
	h.handleImport(w, authedRequest("POST", "/import", map[string]interface{}{"rows": rows}, bookGUID, userID))
	if w.Code != http.StatusOK {
		t.Fatalf("import: got %d: %s", w.Code, w.Body)
	}
	var importResp map[string]int
	json.NewDecoder(w.Body).Decode(&importResp)
	if importResp["imported"] != 2 {
		t.Fatalf("expected 2 imported, got %d", importResp["imported"])
	}
}

func TestPlaidIsolation(t *testing.T) {
	h, db, bookGUID, userID := setupTestHandler(t)
	defer db.Teardown(context.Background())

	authSvc := auth.NewUserService(db.Pool)
	resB, _ := authSvc.Register(context.Background(), auth.RegisterRequest{Email: "b@test.com", Password: "pass", Name: "B"})

	// Exchange under user A
	w := httptest.NewRecorder()
	h.handleExchange(w, authedRequest("POST", "/exchange", map[string]string{"public_token": "tok"}, bookGUID, userID))
	var exResp struct {
		ItemGUID string `json:"item_guid"`
	}
	json.NewDecoder(w.Body).Decode(&exResp)

	// Sync as user B → must get 404
	w = httptest.NewRecorder()
	h.handleSync(w, authedRequest("POST", "/sync", map[string]string{"item_guid": exResp.ItemGUID}, resB.BookGUID, resB.UserID))
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for cross-book sync, got %d: %s", w.Code, w.Body)
	}
}

func TestPlaidDisconnect(t *testing.T) {
	h, db, bookGUID, userID := setupTestHandler(t)
	defer db.Teardown(context.Background())

	w := httptest.NewRecorder()
	h.handleExchange(w, authedRequest("POST", "/exchange", map[string]string{"public_token": "tok"}, bookGUID, userID))
	var exResp struct {
		ItemGUID string `json:"item_guid"`
	}
	json.NewDecoder(w.Body).Decode(&exResp)

	req := httptest.NewRequest("DELETE", "/items/"+exResp.ItemGUID, nil)
	ctx := context.WithValue(req.Context(), auth.BookGUIDKey, bookGUID)
	ctx = context.WithValue(ctx, auth.UserIDKey, userID)
	req = req.WithContext(ctx)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("guid", exResp.ItemGUID)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

	w = httptest.NewRecorder()
	h.handleDisconnect(w, req)
	if w.Code != http.StatusNoContent {
		t.Fatalf("disconnect: got %d: %s", w.Code, w.Body)
	}
}
