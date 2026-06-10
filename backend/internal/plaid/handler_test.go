package plaid

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/user/antimoney/internal/auth"
	"github.com/user/antimoney/internal/models"
	"github.com/user/antimoney/internal/services"
	"github.com/user/antimoney/internal/testutil"
)

func setupTestHandler(t *testing.T) (*PlaidHandler, *fakePlaidClient, *testutil.TestDB, string, string) {
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

	fake := newFakeClient()
	txSvc := services.NewTransactionService(db.Pool)
	svc, err := NewPlaidService(db.Pool, fake, encKey, txSvc)
	if err != nil {
		t.Fatal(err)
	}
	return NewPlaidHandler(svc), fake, db, res.BookGUID, res.UserID
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
	h, _, db, bookGUID, userID := setupTestHandler(t)
	defer db.Teardown(context.Background())

	accSvc := services.NewAccountService(db.Pool)
	ctx := context.WithValue(context.Background(), auth.BookGUIDKey, bookGUID)
	bankAcc, err := accSvc.CreateAccount(ctx, services.CreateAccountRequest{
		Name: "Chequing", AccountType: models.AccountTypeBank,
	})
	if err != nil {
		t.Fatal(err)
	}
	expAcc, err := accSvc.CreateAccount(ctx, services.CreateAccountRequest{
		Name: "Food", AccountType: models.AccountTypeExpense,
	})
	if err != nil {
		t.Fatal(err)
	}

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
	var importResp ImportResult
	json.NewDecoder(w.Body).Decode(&importResp)
	if importResp.Imported != 2 || len(importResp.Failed) != 0 {
		t.Fatalf("expected 2 imported / 0 failed, got %d / %v", importResp.Imported, importResp.Failed)
	}

	// Invariant: imported splits land as cleared ('c') — 2 txns × 2 splits.
	var cleared int
	if err := db.Pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM splits s
		 JOIN transactions t ON t.guid = s.tx_guid
		 WHERE t.book_guid = $1
		   AND t.metadata->'plaid'->>'transaction_id' IS NOT NULL
		   AND s.reconcile_state = 'c'`, bookGUID,
	).Scan(&cleared); err != nil {
		t.Fatal(err)
	}
	if cleared != 4 {
		t.Fatalf("expected 4 cleared splits, got %d", cleared)
	}

	// Invariant: the Plaid transaction_id dedupe key is persisted in metadata.
	var withMeta int
	if err := db.Pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM transactions
		 WHERE book_guid = $1
		   AND metadata->'plaid'->>'transaction_id' IN ('txn-001','txn-002')`, bookGUID,
	).Scan(&withMeta); err != nil {
		t.Fatal(err)
	}
	if withMeta != 2 {
		t.Fatalf("expected 2 transactions with plaid metadata, got %d", withMeta)
	}

	// Invariant: the sync cursor advanced and was persisted (fake: 1 page → "cursor-1").
	var cursor string
	if err := db.Pool.QueryRow(ctx,
		`SELECT sync_cursor FROM plaid_items WHERE guid = $1`, exchangeResp.ItemGUID,
	).Scan(&cursor); err != nil {
		t.Fatal(err)
	}
	if cursor != "cursor-1" {
		t.Fatalf("expected persisted cursor 'cursor-1', got %q", cursor)
	}

	// Dedupe: re-importing the same rows must import 0 and fail 0.
	w = httptest.NewRecorder()
	h.handleImport(w, authedRequest("POST", "/import", map[string]interface{}{"rows": rows}, bookGUID, userID))
	if w.Code != http.StatusOK {
		t.Fatalf("re-import: got %d: %s", w.Code, w.Body)
	}
	var reimport ImportResult
	json.NewDecoder(w.Body).Decode(&reimport)
	if reimport.Imported != 0 || len(reimport.Failed) != 0 {
		t.Fatalf("re-import: expected 0 imported / 0 failed, got %d / %v", reimport.Imported, reimport.Failed)
	}

	// Dedupe: a re-sync after import yields no new suggestions.
	w = httptest.NewRecorder()
	h.handleSync(w, authedRequest("POST", "/sync", map[string]string{"item_guid": exchangeResp.ItemGUID}, bookGUID, userID))
	if w.Code != http.StatusOK {
		t.Fatalf("re-sync: got %d: %s", w.Code, w.Body)
	}
	var resync SyncResult
	json.NewDecoder(w.Body).Decode(&resync)
	if resync.Count != 0 {
		t.Fatalf("re-sync: expected 0 suggestions, got %d", resync.Count)
	}
}

func TestPlaidIsolation(t *testing.T) {
	h, _, db, bookGUID, userID := setupTestHandler(t)
	defer db.Teardown(context.Background())

	authSvc := auth.NewUserService(db.Pool)
	resB, err := authSvc.Register(context.Background(), auth.RegisterRequest{Email: "b@test.com", Password: "pass", Name: "B"})
	if err != nil {
		t.Fatal(err)
	}

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
	h, _, db, bookGUID, userID := setupTestHandler(t)
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

// TestPlaidLinkInvariants covers both halves of the 1:1 mapping invariant and
// the silent-failure path (nonexistent target account).
func TestPlaidLinkInvariants(t *testing.T) {
	h, _, db, bookGUID, userID := setupTestHandler(t)
	defer db.Teardown(context.Background())

	accSvc := services.NewAccountService(db.Pool)
	ctx := context.WithValue(context.Background(), auth.BookGUIDKey, bookGUID)
	acctA, _ := accSvc.CreateAccount(ctx, services.CreateAccountRequest{Name: "A", AccountType: models.AccountTypeBank})
	acctB, _ := accSvc.CreateAccount(ctx, services.CreateAccountRequest{Name: "B", AccountType: models.AccountTypeBank})

	w := httptest.NewRecorder()
	h.handleExchange(w, authedRequest("POST", "/exchange", map[string]string{"public_token": "tok"}, bookGUID, userID))
	var ex struct {
		ItemGUID string `json:"item_guid"`
	}
	json.NewDecoder(w.Body).Decode(&ex)

	link := func(plaidAcct, acctGUID string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		h.handleLink(w, authedRequest("POST", "/link", map[string]interface{}{
			"item_guid": ex.ItemGUID,
			"mappings":  []map[string]string{{"account_id": plaidAcct, "account_guid": acctGUID}},
		}, bookGUID, userID))
		return w
	}

	// Initial link OK.
	if w := link("plaid-acct-001", acctA.GUID); w.Code != http.StatusOK {
		t.Fatalf("initial link: got %d: %s", w.Code, w.Body)
	}
	// Idempotent re-link of the identical pair OK.
	if w := link("plaid-acct-001", acctA.GUID); w.Code != http.StatusOK {
		t.Fatalf("idempotent re-link: got %d: %s", w.Code, w.Body)
	}
	// Same Plaid account → a different Antimoney account: 409.
	if w := link("plaid-acct-001", acctB.GUID); w.Code != http.StatusConflict {
		t.Fatalf("duplicate plaid link: got %d, want 409: %s", w.Code, w.Body)
	}
	// A different Plaid account → an account that already has a link: 409
	// (would silently overwrite the existing link otherwise).
	if w := link("plaid-acct-999", acctA.GUID); w.Code != http.StatusConflict {
		t.Fatalf("overwrite link: got %d, want 409: %s", w.Code, w.Body)
	}
	// Nonexistent target account: 404, never a silent 200.
	if w := link("plaid-acct-002", "00000000-0000-0000-0000-000000000000"); w.Code != http.StatusNotFound {
		t.Fatalf("bogus account: got %d, want 404: %s", w.Code, w.Body)
	}
}

func TestPlaidImportValidation(t *testing.T) {
	h, _, db, bookGUID, userID := setupTestHandler(t)
	defer db.Teardown(context.Background())

	w := httptest.NewRecorder()
	h.handleImport(w, authedRequest("POST", "/import", map[string]interface{}{"rows": []ImportRow{}}, bookGUID, userID))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("empty rows: got %d, want 400", w.Code)
	}

	w = httptest.NewRecorder()
	h.handleImport(w, authedRequest("POST", "/import", map[string]interface{}{"rows": make([]ImportRow, maxImportRows+1)}, bookGUID, userID))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("too many rows: got %d, want 400", w.Code)
	}
}

// TestPlaidUniqueIndexBackstop proves the DB-level idempotency guarantee: even
// if the application dedupe check is raced past, the partial unique index
// rejects a second transaction with the same Plaid id in the same book.
func TestPlaidUniqueIndexBackstop(t *testing.T) {
	_, _, db, bookGUID, _ := setupTestHandler(t)
	ctx := context.Background()
	defer db.Teardown(ctx)

	meta := `{"plaid":{"transaction_id":"dup-001"}}`
	insert := func(guid string) error {
		_, err := db.Pool.Exec(ctx,
			`INSERT INTO transactions (guid, book_guid, post_date, description, metadata)
			 VALUES ($1, $2, NOW(), 'dup test', $3)`,
			guid, bookGUID, meta,
		)
		return err
	}
	if err := insert("0a0a0a0a-0000-0000-0000-000000000001"); err != nil {
		t.Fatalf("first insert: %v", err)
	}
	err := insert("0a0a0a0a-0000-0000-0000-000000000002")
	if err == nil {
		t.Fatal("expected unique violation on duplicate plaid transaction_id, got nil")
	}
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "23505" {
		t.Fatalf("expected 23505 unique violation, got %v", err)
	}
}

func TestPlaidSyncReauthRequired(t *testing.T) {
	h, fake, db, bookGUID, userID := setupTestHandler(t)
	defer db.Teardown(context.Background())

	w := httptest.NewRecorder()
	h.handleExchange(w, authedRequest("POST", "/exchange", map[string]string{"public_token": "tok"}, bookGUID, userID))
	var ex struct {
		ItemGUID string `json:"item_guid"`
	}
	json.NewDecoder(w.Body).Decode(&ex)

	fake.syncErr = ErrReauthRequired
	w = httptest.NewRecorder()
	h.handleSync(w, authedRequest("POST", "/sync", map[string]string{"item_guid": ex.ItemGUID}, bookGUID, userID))
	if w.Code != http.StatusConflict {
		t.Fatalf("reauth: got %d, want 409: %s", w.Code, w.Body)
	}
	if !bytes.Contains(w.Body.Bytes(), []byte("reconnect_required")) {
		t.Fatalf("reauth: body should carry reconnect_required, got %s", w.Body)
	}
}
