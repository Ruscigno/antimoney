package plaid

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

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
	// Legacy nil-AAD fallback OFF, mirroring the production default.
	svc, err := NewPlaidService(db.Pool, fake, encKey, false, txSvc)
	if err != nil {
		t.Fatal(err)
	}
	return NewPlaidHandler(svc, nil), fake, db, res.BookGUID, res.UserID
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

	// POST /import — only the staged id and the chosen category cross the wire;
	// all financial data is resolved server-side from staging.
	rows := make([]ImportRow, 0, len(syncResp.Suggestions))
	for _, s := range syncResp.Suggestions {
		rows = append(rows, ImportRow{
			TransactionID:       s.TransactionID,
			CategoryAccountGUID: expAcc.GUID,
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

// fakeTxn builds a PlaidTxn for the stub bank account.
func fakeTxn(id, desc string, amount int64, pending bool) PlaidTxn {
	return PlaidTxn{
		TransactionID: id,
		Date:          time.Date(2026, 6, 5, 0, 0, 0, 0, time.UTC),
		Description:   desc,
		AmountNum:     amount,
		AmountDenom:   100,
		AccountID:     "plaid-acct-001",
		Pending:       pending,
	}
}

// exchangeAndLink runs the connect flow and links the stub bank account.
func exchangeAndLink(t *testing.T, h *PlaidHandler, db *testutil.TestDB, bookGUID, userID string, importPending bool) (itemGUID, bankGUID string) {
	t.Helper()
	accSvc := services.NewAccountService(db.Pool)
	ctx := context.WithValue(context.Background(), auth.BookGUIDKey, bookGUID)
	bank, err := accSvc.CreateAccount(ctx, services.CreateAccountRequest{Name: "Chequing", AccountType: models.AccountTypeBank})
	if err != nil {
		t.Fatal(err)
	}

	w := httptest.NewRecorder()
	h.handleExchange(w, authedRequest("POST", "/exchange", map[string]string{"public_token": "tok"}, bookGUID, userID))
	var ex struct {
		ItemGUID string `json:"item_guid"`
	}
	json.NewDecoder(w.Body).Decode(&ex)

	w = httptest.NewRecorder()
	h.handleLink(w, authedRequest("POST", "/link", map[string]interface{}{
		"item_guid":      ex.ItemGUID,
		"mappings":       []map[string]string{{"account_id": "plaid-acct-001", "account_guid": bank.GUID}},
		"import_pending": importPending,
	}, bookGUID, userID))
	if w.Code != http.StatusOK {
		t.Fatalf("link: got %d: %s", w.Code, w.Body)
	}
	return ex.ItemGUID, bank.GUID
}

func doSync(t *testing.T, h *PlaidHandler, itemGUID, bookGUID, userID string) SyncResult {
	t.Helper()
	w := httptest.NewRecorder()
	h.handleSync(w, authedRequest("POST", "/sync", map[string]string{"item_guid": itemGUID}, bookGUID, userID))
	if w.Code != http.StatusOK {
		t.Fatalf("sync: got %d: %s", w.Code, w.Body)
	}
	var res SyncResult
	json.NewDecoder(w.Body).Decode(&res)
	return res
}

// TestPlaidStagingSurvivesLostResponse proves finding #1's fix: suggestions
// are durable — a dropped response or closed tab does not lose transactions
// even though the Plaid cursor has already advanced.
func TestPlaidStagingSurvivesLostResponse(t *testing.T) {
	h, _, db, bookGUID, userID := setupTestHandler(t)
	defer db.Teardown(context.Background())
	itemGUID, _ := exchangeAndLink(t, h, db, bookGUID, userID, false)

	first := doSync(t, h, itemGUID, bookGUID, userID)
	if first.Count != 2 {
		t.Fatalf("first sync: expected 2 suggestions, got %d", first.Count)
	}
	// Pretend the response was lost (nothing imported). The fake has no more
	// pages, so a second sync fetches nothing new — the suggestions must come
	// back from staging anyway.
	second := doSync(t, h, itemGUID, bookGUID, userID)
	if second.Count != 2 {
		t.Fatalf("re-sync after lost response: expected 2 suggestions from staging, got %d", second.Count)
	}
}

// TestPlaidRemovedAndModified covers the /transactions/sync deltas that were
// previously ignored (finding #2).
func TestPlaidRemovedAndModified(t *testing.T) {
	h, fake, db, bookGUID, userID := setupTestHandler(t)
	defer db.Teardown(context.Background())
	fake.onePagePerSync = true
	fake.deltaPages = []SyncDelta{
		{Added: []PlaidTxn{fakeTxn("txn-A", "Coffee", 100, false), fakeTxn("txn-B", "Books", 200, false)}},
		{Modified: []PlaidTxn{fakeTxn("txn-B", "Books (corrected)", 999, false)}, Removed: []string{"txn-A"}},
	}
	itemGUID, _ := exchangeAndLink(t, h, db, bookGUID, userID, false)

	first := doSync(t, h, itemGUID, bookGUID, userID)
	if first.Count != 2 {
		t.Fatalf("first sync: expected 2, got %d", first.Count)
	}
	second := doSync(t, h, itemGUID, bookGUID, userID)
	if second.Count != 1 {
		t.Fatalf("after removed+modified: expected 1 suggestion, got %d", second.Count)
	}
	got := second.Suggestions[0]
	if got.TransactionID != "txn-B" || got.AmountNum != 999 || got.Description != "Books (corrected)" {
		t.Fatalf("modified delta not applied: %+v", got)
	}
}

// TestPlaidPendingToPostedAfterImport: a pending transaction that was imported
// and later posts under a NEW transaction_id must not be suggested again
// (correlated via PendingTransactionID — finding #2's duplication vector).
func TestPlaidPendingToPostedAfterImport(t *testing.T) {
	h, fake, db, bookGUID, userID := setupTestHandler(t)
	defer db.Teardown(context.Background())
	// Same value as the pending txn: must be correlated away. (A diverged value
	// staying visible is covered by TestPlaidPendingPostedValueDivergence.)
	posted := fakeTxn("txn-Q", "Coffee (posted)", 100, false)
	posted.PendingTransactionID = "txn-P"
	fake.onePagePerSync = true
	fake.deltaPages = []SyncDelta{
		{Added: []PlaidTxn{fakeTxn("txn-P", "Coffee", 100, true)}},
		{Removed: []string{"txn-P"}, Added: []PlaidTxn{posted}},
	}
	itemGUID, _ := exchangeAndLink(t, h, db, bookGUID, userID, true) // import_pending on

	accSvc := services.NewAccountService(db.Pool)
	ctx := context.WithValue(context.Background(), auth.BookGUIDKey, bookGUID)
	cat, _ := accSvc.CreateAccount(ctx, services.CreateAccountRequest{Name: "Dining", AccountType: models.AccountTypeExpense})

	first := doSync(t, h, itemGUID, bookGUID, userID)
	if first.Count != 1 || first.Suggestions[0].TransactionID != "txn-P" {
		t.Fatalf("expected pending txn-P suggested, got %+v", first.Suggestions)
	}
	w := httptest.NewRecorder()
	h.handleImport(w, authedRequest("POST", "/import", map[string]interface{}{
		"rows": []ImportRow{{TransactionID: "txn-P", CategoryAccountGUID: cat.GUID}},
	}, bookGUID, userID))
	if w.Code != http.StatusOK {
		t.Fatalf("import pending: got %d: %s", w.Code, w.Body)
	}

	second := doSync(t, h, itemGUID, bookGUID, userID)
	if second.Count != 0 {
		t.Fatalf("posted version of an imported pending txn must not re-surface, got %d: %+v", second.Count, second.Suggestions)
	}
}

// TestPlaidCursorResumesAfterPageCap: the 3-page cap stops mid-stream with
// has_more=true; the next sync resumes from the persisted cursor (finding #10
// and test gap #17a).
func TestPlaidCursorResumesAfterPageCap(t *testing.T) {
	h, fake, db, bookGUID, userID := setupTestHandler(t)
	defer db.Teardown(context.Background())
	fake.deltaPages = []SyncDelta{
		{Added: []PlaidTxn{fakeTxn("t1", "One", 100, false)}},
		{Added: []PlaidTxn{fakeTxn("t2", "Two", 200, false)}},
		{Added: []PlaidTxn{fakeTxn("t3", "Three", 300, false)}},
		{Added: []PlaidTxn{fakeTxn("t4", "Four", 400, false)}},
	}
	itemGUID, _ := exchangeAndLink(t, h, db, bookGUID, userID, false)

	first := doSync(t, h, itemGUID, bookGUID, userID)
	if !first.HasMore || first.Count != 3 {
		t.Fatalf("first sync: expected 3 suggestions + has_more, got %d / %v", first.Count, first.HasMore)
	}
	var cursor string
	if err := db.Pool.QueryRow(context.Background(),
		`SELECT sync_cursor FROM plaid_items WHERE guid = $1`, itemGUID).Scan(&cursor); err != nil {
		t.Fatal(err)
	}
	if cursor != "cursor-3" {
		t.Fatalf("expected cursor-3 persisted at the cap, got %q", cursor)
	}

	second := doSync(t, h, itemGUID, bookGUID, userID)
	if second.HasMore || second.Count != 4 {
		t.Fatalf("second sync: expected 4 suggestions + no has_more, got %d / %v", second.Count, second.HasMore)
	}
}

// TestPlaidDisconnectAbortsOnPlaidFailure: a failed /item/remove must NOT
// delete the local row (the only copy of the access token) — finding #8.
func TestPlaidDisconnectAbortsOnPlaidFailure(t *testing.T) {
	h, fake, db, bookGUID, userID := setupTestHandler(t)
	defer db.Teardown(context.Background())
	fake.removeErr = errors.New("plaid is down")

	w := httptest.NewRecorder()
	h.handleExchange(w, authedRequest("POST", "/exchange", map[string]string{"public_token": "tok"}, bookGUID, userID))
	var ex struct {
		ItemGUID string `json:"item_guid"`
	}
	json.NewDecoder(w.Body).Decode(&ex)

	req := httptest.NewRequest("DELETE", "/items/"+ex.ItemGUID, nil)
	ctx := context.WithValue(req.Context(), auth.BookGUIDKey, bookGUID)
	ctx = context.WithValue(ctx, auth.UserIDKey, userID)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("guid", ex.ItemGUID)
	req = req.WithContext(context.WithValue(ctx, chi.RouteCtxKey, rctx))

	w = httptest.NewRecorder()
	h.handleDisconnect(w, req)
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("disconnect with Plaid failure: got %d, want 500", w.Code)
	}
	var cnt int
	if err := db.Pool.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM plaid_items WHERE guid = $1`, ex.ItemGUID).Scan(&cnt); err != nil {
		t.Fatal(err)
	}
	if cnt != 1 {
		t.Fatalf("plaid_items row must survive a failed disconnect, got %d rows", cnt)
	}
}

// TestPlaidSyncDecryptFailure: corrupted ciphertext surfaces as a 500, never a
// silent success (test gap #17c).
func TestPlaidSyncDecryptFailure(t *testing.T) {
	h, _, db, bookGUID, userID := setupTestHandler(t)
	defer db.Teardown(context.Background())
	itemGUID, _ := exchangeAndLink(t, h, db, bookGUID, userID, false)

	if _, err := db.Pool.Exec(context.Background(),
		`UPDATE plaid_items SET access_token_ciphertext = 'corrupted' WHERE guid = $1`, itemGUID); err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	h.handleSync(w, authedRequest("POST", "/sync", map[string]string{"item_guid": itemGUID}, bookGUID, userID))
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("decrypt failure: got %d, want 500", w.Code)
	}
}

// TestIsPlaidDupViolation pins the exact classification used to treat a
// concurrent-import race as a benign skip (test gap #17b).
func TestIsPlaidDupViolation(t *testing.T) {
	dup := &pgconn.PgError{Code: "23505", ConstraintName: "idx_transactions_plaid_txn"}
	if !isPlaidDupViolation(fmt.Errorf("insert transaction: %w", dup)) {
		t.Fatal("wrapped 23505 on the plaid index must classify as dup")
	}
	other := &pgconn.PgError{Code: "23505", ConstraintName: "some_other_unique"}
	if isPlaidDupViolation(other) {
		t.Fatal("23505 on another constraint must NOT classify as dup")
	}
	if isPlaidDupViolation(errors.New("plain")) {
		t.Fatal("plain error must NOT classify as dup")
	}
}

func importRows(t *testing.T, h *PlaidHandler, bookGUID, userID string, rows []ImportRow) ImportResult {
	t.Helper()
	w := httptest.NewRecorder()
	h.handleImport(w, authedRequest("POST", "/import", map[string]interface{}{"rows": rows}, bookGUID, userID))
	if w.Code != http.StatusOK {
		t.Fatalf("import: got %d: %s", w.Code, w.Body)
	}
	var res ImportResult
	json.NewDecoder(w.Body).Decode(&res)
	return res
}

func expenseAccount(t *testing.T, db *testutil.TestDB, bookGUID, name string) string {
	t.Helper()
	accSvc := services.NewAccountService(db.Pool)
	ctx := context.WithValue(context.Background(), auth.BookGUIDKey, bookGUID)
	acc, err := accSvc.CreateAccount(ctx, services.CreateAccountRequest{Name: name, AccountType: models.AccountTypeExpense})
	if err != nil {
		t.Fatal(err)
	}
	return acc.GUID
}

// TestPlaidRemovedAfterImport (#9a): a bank-side removal of an imported
// transaction never deletes the user's books; only the staged copy goes.
func TestPlaidRemovedAfterImport(t *testing.T) {
	h, fake, db, bookGUID, userID := setupTestHandler(t)
	defer db.Teardown(context.Background())
	fake.onePagePerSync = true
	fake.deltaPages = []SyncDelta{
		{Added: []PlaidTxn{fakeTxn("txn-X", "Coffee", 100, false)}},
		{Removed: []string{"txn-X"}},
	}
	itemGUID, _ := exchangeAndLink(t, h, db, bookGUID, userID, false)
	cat := expenseAccount(t, db, bookGUID, "Dining")

	doSync(t, h, itemGUID, bookGUID, userID)
	importRows(t, h, bookGUID, userID, []ImportRow{{TransactionID: "txn-X", CategoryAccountGUID: cat}})

	second := doSync(t, h, itemGUID, bookGUID, userID) // applies the removed delta
	if second.Count != 0 {
		t.Fatalf("expected 0 suggestions, got %d", second.Count)
	}
	var txCount int
	if err := db.Pool.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM transactions WHERE book_guid = $1 AND metadata->'plaid'->>'transaction_id' = 'txn-X'`,
		bookGUID).Scan(&txCount); err != nil {
		t.Fatal(err)
	}
	if txCount != 1 {
		t.Fatalf("imported transaction must survive a bank-side removal, got %d", txCount)
	}
}

// TestPlaidModifiedAfterImport (#9b): a modification arriving after import
// updates staging but never silently rewrites the imported transaction.
func TestPlaidModifiedAfterImport(t *testing.T) {
	h, fake, db, bookGUID, userID := setupTestHandler(t)
	defer db.Teardown(context.Background())
	fake.onePagePerSync = true
	fake.deltaPages = []SyncDelta{
		{Added: []PlaidTxn{fakeTxn("txn-X", "Coffee", 100, false)}},
		{Modified: []PlaidTxn{fakeTxn("txn-X", "Coffee", 999, false)}},
	}
	itemGUID, _ := exchangeAndLink(t, h, db, bookGUID, userID, false)
	cat := expenseAccount(t, db, bookGUID, "Dining")

	doSync(t, h, itemGUID, bookGUID, userID)
	importRows(t, h, bookGUID, userID, []ImportRow{{TransactionID: "txn-X", CategoryAccountGUID: cat}})

	second := doSync(t, h, itemGUID, bookGUID, userID)
	if second.Count != 0 {
		t.Fatalf("modified-after-import must stay hidden (already imported), got %d", second.Count)
	}
	var splitCount int
	if err := db.Pool.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM splits s JOIN transactions t ON t.guid = s.tx_guid
		 WHERE t.book_guid = $1 AND t.metadata->'plaid'->>'transaction_id' = 'txn-X' AND s.value_num IN (100, -100)`,
		bookGUID).Scan(&splitCount); err != nil {
		t.Fatal(err)
	}
	if splitCount != 2 {
		t.Fatalf("imported transaction value must not be rewritten, got %d original-value splits", splitCount)
	}
}

// TestPlaidUnmappedAccountStaysStaged (#9c): rows for an unmapped bank account
// stay invisible but staged, and surface once the account is mapped.
func TestPlaidUnmappedAccountStaysStaged(t *testing.T) {
	h, fake, db, bookGUID, userID := setupTestHandler(t)
	defer db.Teardown(context.Background())
	other := fakeTxn("txn-Z", "Mystery", 100, false)
	other.AccountID = "plaid-acct-999" // not mapped
	fake.deltaPages = []SyncDelta{{Added: []PlaidTxn{other}}}
	itemGUID, _ := exchangeAndLink(t, h, db, bookGUID, userID, false)

	first := doSync(t, h, itemGUID, bookGUID, userID)
	if first.Count != 0 {
		t.Fatalf("unmapped account txn must not be suggested, got %d", first.Count)
	}
	var staged int
	if err := db.Pool.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM plaid_staged_transactions WHERE book_guid = $1 AND transaction_id = 'txn-Z'`,
		bookGUID).Scan(&staged); err != nil {
		t.Fatal(err)
	}
	if staged != 1 {
		t.Fatalf("unmapped txn must stay staged, got %d", staged)
	}

	// Map the account → the staged row becomes a suggestion.
	accSvc := services.NewAccountService(db.Pool)
	ctx := context.WithValue(context.Background(), auth.BookGUIDKey, bookGUID)
	acc2, _ := accSvc.CreateAccount(ctx, services.CreateAccountRequest{Name: "Savings", AccountType: models.AccountTypeBank})
	w := httptest.NewRecorder()
	h.handleLink(w, authedRequest("POST", "/link", map[string]interface{}{
		"item_guid": itemGUID,
		"mappings":  []map[string]string{{"account_id": "plaid-acct-999", "account_guid": acc2.GUID}},
	}, bookGUID, userID))
	if w.Code != http.StatusOK {
		t.Fatalf("link second account: %d: %s", w.Code, w.Body)
	}
	second := doSync(t, h, itemGUID, bookGUID, userID)
	if second.Count != 1 || second.Suggestions[0].TransactionID != "txn-Z" {
		t.Fatalf("after mapping, staged txn must surface, got %+v", second.Suggestions)
	}
}

// TestPlaidImportFailedPaths (#9d): never-staged ids and malformed category
// ids land in failed[], not in a 500.
func TestPlaidImportFailedPaths(t *testing.T) {
	h, _, db, bookGUID, userID := setupTestHandler(t)
	defer db.Teardown(context.Background())
	itemGUID, _ := exchangeAndLink(t, h, db, bookGUID, userID, false)
	cat := expenseAccount(t, db, bookGUID, "Dining")
	doSync(t, h, itemGUID, bookGUID, userID)

	res := importRows(t, h, bookGUID, userID, []ImportRow{
		{TransactionID: "txn-never-staged", CategoryAccountGUID: cat},
		{TransactionID: "txn-001", CategoryAccountGUID: "not-a-uuid"},
	})
	if res.Imported != 0 || len(res.Failed) != 2 {
		t.Fatalf("expected 0 imported / 2 failed, got %d / %v", res.Imported, res.Failed)
	}
}

// TestPlaidConcurrentImports (#9e): two truly concurrent imports of the same
// rows produce exactly one transaction per row (dedupe + unique-index backstop).
func TestPlaidConcurrentImports(t *testing.T) {
	h, _, db, bookGUID, userID := setupTestHandler(t)
	defer db.Teardown(context.Background())
	itemGUID, _ := exchangeAndLink(t, h, db, bookGUID, userID, false)
	cat := expenseAccount(t, db, bookGUID, "Dining")
	first := doSync(t, h, itemGUID, bookGUID, userID)

	rows := make([]ImportRow, 0, len(first.Suggestions))
	for _, s := range first.Suggestions {
		rows = append(rows, ImportRow{TransactionID: s.TransactionID, CategoryAccountGUID: cat})
	}

	ctx := context.WithValue(context.Background(), auth.BookGUIDKey, bookGUID)
	ctx = context.WithValue(ctx, auth.UserIDKey, userID)
	results := make([]*ImportResult, 2)
	errs := make([]error, 2)
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results[i], errs[i] = h.svc.Import(ctx, rows)
		}(i)
	}
	wg.Wait()

	total := 0
	for i := 0; i < 2; i++ {
		if errs[i] != nil {
			t.Fatalf("concurrent import %d errored: %v", i, errs[i])
		}
		total += results[i].Imported
		if len(results[i].Failed) != 0 {
			t.Fatalf("concurrent import %d reported failures: %v", i, results[i].Failed)
		}
	}
	if total != len(rows) {
		t.Fatalf("expected %d total imported across both runs, got %d", len(rows), total)
	}
	var txCount int
	if err := db.Pool.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM transactions WHERE book_guid = $1 AND metadata->'plaid'->>'transaction_id' IS NOT NULL`,
		bookGUID).Scan(&txCount); err != nil {
		t.Fatal(err)
	}
	if txCount != len(rows) {
		t.Fatalf("expected exactly %d plaid transactions in DB, got %d", len(rows), txCount)
	}
}

// TestPlaidPendingPostedRace (M1): a posted txn staged before its pending
// predecessor's import committed must be hidden from suggestions and skipped
// by import (same value).
func TestPlaidPendingPostedRace(t *testing.T) {
	h, fake, db, bookGUID, userID := setupTestHandler(t)
	defer db.Teardown(context.Background())
	fake.onePagePerSync = true
	fake.deltaPages = []SyncDelta{
		{Added: []PlaidTxn{fakeTxn("txn-P", "Coffee", 100, true)}},
	}
	itemGUID, _ := exchangeAndLink(t, h, db, bookGUID, userID, true)
	cat := expenseAccount(t, db, bookGUID, "Dining")

	doSync(t, h, itemGUID, bookGUID, userID)
	importRows(t, h, bookGUID, userID, []ImportRow{{TransactionID: "txn-P", CategoryAccountGUID: cat}})

	// Simulate the race: the posted txn-Q was staged by a concurrent sync that
	// ran BEFORE txn-P's import committed (so stageDelta's correlation missed).
	if _, err := db.Pool.Exec(context.Background(),
		`INSERT INTO plaid_staged_transactions
			(book_guid, item_guid, transaction_id, pending_transaction_id, plaid_account_id, post_date, description, amount_num, amount_denom, pending)
		 VALUES ($1, $2, 'txn-Q', 'txn-P', 'plaid-acct-001', '2026-06-05', 'Coffee (posted)', 100, 100, false)`,
		bookGUID, itemGUID); err != nil {
		t.Fatal(err)
	}

	res := doSync(t, h, itemGUID, bookGUID, userID)
	if res.Count != 0 {
		t.Fatalf("race-staged posted txn (same value) must be hidden, got %d: %+v", res.Count, res.Suggestions)
	}
	imp := importRows(t, h, bookGUID, userID, []ImportRow{{TransactionID: "txn-Q", CategoryAccountGUID: cat}})
	if imp.Imported != 0 || len(imp.Failed) != 0 {
		t.Fatalf("import of race-staged posted txn must be a benign skip, got %d / %v", imp.Imported, imp.Failed)
	}
	var txCount int
	db.Pool.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM transactions WHERE book_guid = $1 AND metadata->'plaid'->>'transaction_id' IN ('txn-P','txn-Q')`,
		bookGUID).Scan(&txCount)
	if txCount != 1 {
		t.Fatalf("expected exactly 1 transaction (no pending→posted duplicate), got %d", txCount)
	}
}

// TestPlaidPendingPostedValueDivergence (M2): a posted txn whose value differs
// from the imported pending must stay visible as a suggestion.
func TestPlaidPendingPostedValueDivergence(t *testing.T) {
	h, fake, db, bookGUID, userID := setupTestHandler(t)
	defer db.Teardown(context.Background())
	posted := fakeTxn("txn-Q", "Coffee + tip", 120, false) // diverges from 100
	posted.PendingTransactionID = "txn-P"
	fake.onePagePerSync = true
	fake.deltaPages = []SyncDelta{
		{Added: []PlaidTxn{fakeTxn("txn-P", "Coffee", 100, true)}},
		{Removed: []string{"txn-P"}, Added: []PlaidTxn{posted}},
	}
	itemGUID, _ := exchangeAndLink(t, h, db, bookGUID, userID, true)
	cat := expenseAccount(t, db, bookGUID, "Dining")

	doSync(t, h, itemGUID, bookGUID, userID)
	importRows(t, h, bookGUID, userID, []ImportRow{{TransactionID: "txn-P", CategoryAccountGUID: cat}})

	second := doSync(t, h, itemGUID, bookGUID, userID)
	if second.Count != 1 || second.Suggestions[0].TransactionID != "txn-Q" || second.Suggestions[0].AmountNum != 120 {
		t.Fatalf("value-diverged posted txn must stay suggested, got %+v", second.Suggestions)
	}
}

// TestPlaidDismiss (#4): dismissed staged rows never reappear as suggestions.
func TestPlaidDismiss(t *testing.T) {
	h, _, db, bookGUID, userID := setupTestHandler(t)
	defer db.Teardown(context.Background())
	itemGUID, _ := exchangeAndLink(t, h, db, bookGUID, userID, false)

	first := doSync(t, h, itemGUID, bookGUID, userID)
	if first.Count != 2 {
		t.Fatalf("expected 2 suggestions, got %d", first.Count)
	}
	w := httptest.NewRecorder()
	h.handleDismiss(w, authedRequest("POST", "/dismiss", map[string]interface{}{
		"transaction_ids": []string{"txn-001"},
	}, bookGUID, userID))
	if w.Code != http.StatusOK {
		t.Fatalf("dismiss: got %d: %s", w.Code, w.Body)
	}
	var dis map[string]int
	json.NewDecoder(w.Body).Decode(&dis)
	if dis["dismissed"] != 1 {
		t.Fatalf("expected 1 dismissed, got %d", dis["dismissed"])
	}

	second := doSync(t, h, itemGUID, bookGUID, userID)
	if second.Count != 1 || second.Suggestions[0].TransactionID != "txn-002" {
		t.Fatalf("dismissed txn must not reappear, got %+v", second.Suggestions)
	}
}

// TestPlaidSyncLockSkipsFetch (#5): a sync that cannot take the per-item
// advisory lock serves staged suggestions without touching Plaid.
func TestPlaidSyncLockSkipsFetch(t *testing.T) {
	h, fake, db, bookGUID, userID := setupTestHandler(t)
	defer db.Teardown(context.Background())
	itemGUID, _ := exchangeAndLink(t, h, db, bookGUID, userID, false)
	doSync(t, h, itemGUID, bookGUID, userID) // stages 2, pageIndex=1

	// Hold the item's advisory lock on a separate connection.
	lockConn, err := db.Pool.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer lockConn.Release()
	if _, err := lockConn.Exec(context.Background(),
		`SELECT pg_advisory_lock(hashtextextended($1, 0))`, itemGUID); err != nil {
		t.Fatal(err)
	}
	defer lockConn.Exec(context.Background(), `SELECT pg_advisory_unlock(hashtextextended($1, 0))`, itemGUID)

	pagesBefore := fake.pageIndex
	res := doSync(t, h, itemGUID, bookGUID, userID)
	if fake.pageIndex != pagesBefore {
		t.Fatalf("locked sync must not fetch from Plaid (pageIndex %d -> %d)", pagesBefore, fake.pageIndex)
	}
	if res.Count != 2 {
		t.Fatalf("locked sync must still serve staged suggestions, got %d", res.Count)
	}
}

// TestPlaidSyncLockReleasedOnError (round-7 M1): a failed fetch must release
// the per-item advisory lock — otherwise every later sync silently takes the
// lock-skip path forever.
func TestPlaidSyncLockReleasedOnError(t *testing.T) {
	h, fake, db, bookGUID, userID := setupTestHandler(t)
	defer db.Teardown(context.Background())
	itemGUID, _ := exchangeAndLink(t, h, db, bookGUID, userID, false)

	fake.syncErr = errors.New("plaid exploded")
	w := httptest.NewRecorder()
	h.handleSync(w, authedRequest("POST", "/sync", map[string]string{"item_guid": itemGUID}, bookGUID, userID))
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("failed fetch: got %d, want 500", w.Code)
	}

	// Lock must be free again: the next sync FETCHES (pageIndex advances)
	// instead of silently serving staged-only results.
	fake.syncErr = nil
	pagesBefore := fake.pageIndex
	res := doSync(t, h, itemGUID, bookGUID, userID)
	if fake.pageIndex == pagesBefore {
		t.Fatal("lock leaked: sync after a failed fetch did not fetch again")
	}
	if res.InProgress {
		t.Fatal("lock leaked: sync reports another sync in progress")
	}
	if res.Count != 2 {
		t.Fatalf("expected 2 suggestions after recovery, got %d", res.Count)
	}
}

// TestPlaidPendingPostedSignInversion (round-7 M2): a posted REVERSAL
// (-amount) of an imported pending charge must stay visible — the sign-blind
// comparison used to collide it with the original's bank split and discard it.
func TestPlaidPendingPostedSignInversion(t *testing.T) {
	h, fake, db, bookGUID, userID := setupTestHandler(t)
	defer db.Teardown(context.Background())
	reversal := fakeTxn("txn-R", "Coffee (reversed)", -100, false)
	reversal.PendingTransactionID = "txn-P"
	fake.onePagePerSync = true
	fake.deltaPages = []SyncDelta{
		{Added: []PlaidTxn{fakeTxn("txn-P", "Coffee", 100, true)}},
		{Removed: []string{"txn-P"}, Added: []PlaidTxn{reversal}},
	}
	itemGUID, _ := exchangeAndLink(t, h, db, bookGUID, userID, true)
	cat := expenseAccount(t, db, bookGUID, "Dining")

	doSync(t, h, itemGUID, bookGUID, userID)
	importRows(t, h, bookGUID, userID, []ImportRow{{TransactionID: "txn-P", CategoryAccountGUID: cat}})

	second := doSync(t, h, itemGUID, bookGUID, userID)
	if second.Count != 1 || second.Suggestions[0].TransactionID != "txn-R" || second.Suggestions[0].AmountNum != -100 {
		t.Fatalf("sign-inverted posted txn (reversal) must stay suggested, got %+v", second.Suggestions)
	}
}

// TestPlaidDismissedSurvivesPendingToPosted (round-7 M3): "never import this
// transaction" must survive the id change when a dismissed pending posts.
func TestPlaidDismissedSurvivesPendingToPosted(t *testing.T) {
	h, fake, db, bookGUID, userID := setupTestHandler(t)
	defer db.Teardown(context.Background())
	posted := fakeTxn("txn-Q", "Subscription (posted)", 100, false)
	posted.PendingTransactionID = "txn-P"
	fake.onePagePerSync = true
	fake.deltaPages = []SyncDelta{
		{Added: []PlaidTxn{fakeTxn("txn-P", "Subscription", 100, true)}},
		{Removed: []string{"txn-P"}, Added: []PlaidTxn{posted}},
	}
	itemGUID, _ := exchangeAndLink(t, h, db, bookGUID, userID, true)

	first := doSync(t, h, itemGUID, bookGUID, userID)
	if first.Count != 1 {
		t.Fatalf("expected pending suggestion, got %d", first.Count)
	}
	w := httptest.NewRecorder()
	h.handleDismiss(w, authedRequest("POST", "/dismiss", map[string]interface{}{"transaction_ids": []string{"txn-P"}}, bookGUID, userID))
	if w.Code != http.StatusOK {
		t.Fatalf("dismiss: %d: %s", w.Code, w.Body)
	}

	second := doSync(t, h, itemGUID, bookGUID, userID) // P posts as Q
	if second.Count != 0 {
		t.Fatalf("dismissed pending must stay dismissed after posting under a new id, got %+v", second.Suggestions)
	}
}

// TestPlaidDismissCrossBookIDOR: dismissing another book's staged transaction
// must affect zero rows and leave the victim's suggestions intact.
func TestPlaidDismissCrossBookIDOR(t *testing.T) {
	h, _, db, bookGUID, userID := setupTestHandler(t)
	defer db.Teardown(context.Background())
	itemGUID, _ := exchangeAndLink(t, h, db, bookGUID, userID, false)
	doSync(t, h, itemGUID, bookGUID, userID)

	authSvc := auth.NewUserService(db.Pool)
	resB, err := authSvc.Register(context.Background(), auth.RegisterRequest{Email: "evil@test.com", Password: "pass", Name: "Evil"})
	if err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	h.handleDismiss(w, authedRequest("POST", "/dismiss", map[string]interface{}{"transaction_ids": []string{"txn-001"}}, resB.BookGUID, resB.UserID))
	var dis map[string]int
	json.NewDecoder(w.Body).Decode(&dis)
	if dis["dismissed"] != 0 {
		t.Fatalf("cross-book dismiss must affect 0 rows, got %d", dis["dismissed"])
	}
	res := doSync(t, h, itemGUID, bookGUID, userID)
	if res.Count != 2 {
		t.Fatalf("victim's suggestions must be intact, got %d", res.Count)
	}
}

// TestPlaidDisconnectForcedWhenTokenUndecryptable (round-9 #2): a token that no
// key can decrypt is unusable for /item/remove forever — disconnect must clean
// up locally instead of bricking the item.
func TestPlaidDisconnectForcedWhenTokenUndecryptable(t *testing.T) {
	h, fake, db, bookGUID, userID := setupTestHandler(t)
	defer db.Teardown(context.Background())

	w := httptest.NewRecorder()
	h.handleExchange(w, authedRequest("POST", "/exchange", map[string]string{"public_token": "tok"}, bookGUID, userID))
	var ex struct {
		ItemGUID string `json:"item_guid"`
	}
	json.NewDecoder(w.Body).Decode(&ex)

	if _, err := db.Pool.Exec(context.Background(),
		`UPDATE plaid_items SET access_token_ciphertext = 'garbage' WHERE guid = $1`, ex.ItemGUID); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest("DELETE", "/items/"+ex.ItemGUID, nil)
	ctx := context.WithValue(req.Context(), auth.BookGUIDKey, bookGUID)
	ctx = context.WithValue(ctx, auth.UserIDKey, userID)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("guid", ex.ItemGUID)
	req = req.WithContext(context.WithValue(ctx, chi.RouteCtxKey, rctx))

	w = httptest.NewRecorder()
	h.handleDisconnect(w, req)
	if w.Code != http.StatusNoContent {
		t.Fatalf("forced disconnect: got %d, want 204: %s", w.Code, w.Body)
	}
	if fake.removeCalls != 0 {
		t.Fatalf("RemoveItem must not be attempted with an undecryptable token, called %d times", fake.removeCalls)
	}
	var cnt int
	db.Pool.QueryRow(context.Background(), `SELECT COUNT(*) FROM plaid_items WHERE guid = $1`, ex.ItemGUID).Scan(&cnt)
	if cnt != 0 {
		t.Fatalf("row must be deleted on forced disconnect, %d remain", cnt)
	}
	// Round-10 #1: the row must be ARCHIVED before deletion so a key
	// misconfiguration stays recoverable by an operator.
	var archived int
	db.Pool.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM plaid_migration_audit
		 WHERE migration = 'force-disconnect' AND payload->>'guid' = $1`, ex.ItemGUID).Scan(&archived)
	if archived != 1 {
		t.Fatalf("forced disconnect must archive the row to plaid_migration_audit, found %d", archived)
	}
}

// TestPlaidLegacyTokenRejectedWithoutFlag (round-10 #2): with the sunset flag
// OFF (the default), a nil-AAD legacy ciphertext must NOT decrypt — the
// anti-swap guarantee holds unconditionally in production.
func TestPlaidLegacyTokenRejectedWithoutFlag(t *testing.T) {
	h, _, db, bookGUID, userID := setupTestHandler(t) // flag off
	defer db.Teardown(context.Background())
	itemGUID, _ := exchangeAndLink(t, h, db, bookGUID, userID, false)

	key := make([]byte, 32)
	legacyCT, legacyNonce, err := encrypt(key, "access-sandbox-test-token", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Pool.Exec(context.Background(),
		`UPDATE plaid_items SET access_token_ciphertext = $1, access_token_nonce = $2 WHERE guid = $3`,
		legacyCT, legacyNonce, itemGUID); err != nil {
		t.Fatal(err)
	}

	w := httptest.NewRecorder()
	h.handleSync(w, authedRequest("POST", "/sync", map[string]string{"item_guid": itemGUID}, bookGUID, userID))
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("legacy token without the sunset flag must fail, got %d", w.Code)
	}
}

// TestPlaidLegacyTokenDisconnectAborts (round-11 #2): a legacy token with the
// sunset flag OFF has a CLEAN recovery path (enable the flag) — disconnect
// must abort with guidance, never archive-and-delete it.
func TestPlaidLegacyTokenDisconnectAborts(t *testing.T) {
	h, fake, db, bookGUID, userID := setupTestHandler(t) // flag off
	defer db.Teardown(context.Background())
	itemGUID, _ := exchangeAndLink(t, h, db, bookGUID, userID, false)

	key := make([]byte, 32)
	legacyCT, legacyNonce, err := encrypt(key, "access-sandbox-test-token", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Pool.Exec(context.Background(),
		`UPDATE plaid_items SET access_token_ciphertext = $1, access_token_nonce = $2 WHERE guid = $3`,
		legacyCT, legacyNonce, itemGUID); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest("DELETE", "/items/"+itemGUID, nil)
	ctx := context.WithValue(req.Context(), auth.BookGUIDKey, bookGUID)
	ctx = context.WithValue(ctx, auth.UserIDKey, userID)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("guid", itemGUID)
	req = req.WithContext(context.WithValue(ctx, chi.RouteCtxKey, rctx))

	w := httptest.NewRecorder()
	h.handleDisconnect(w, req)
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("legacy-token disconnect must abort, got %d", w.Code)
	}
	if fake.removeCalls != 0 {
		t.Fatalf("RemoveItem must not run on a legacy token with the flag off")
	}
	var cnt int
	db.Pool.QueryRow(context.Background(), `SELECT COUNT(*) FROM plaid_items WHERE guid = $1`, itemGUID).Scan(&cnt)
	if cnt != 1 {
		t.Fatalf("recoverable legacy row must be PRESERVED, got %d rows", cnt)
	}
	var archived int
	db.Pool.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM plaid_migration_audit WHERE migration = 'force-disconnect'`).Scan(&archived)
	if archived != 0 {
		t.Fatalf("legacy token must not be force-archived, found %d audit rows", archived)
	}
}

// TestPlaidModifiedToZeroDropsStaleStagedRow (round-10 #5): a Modified delta
// that zeroes a transaction must remove the stale non-zero suggestion.
func TestPlaidModifiedToZeroDropsStaleStagedRow(t *testing.T) {
	h, fake, db, bookGUID, userID := setupTestHandler(t)
	defer db.Teardown(context.Background())
	fake.onePagePerSync = true
	fake.deltaPages = []SyncDelta{
		{Added: []PlaidTxn{fakeTxn("txn-Z", "Hold", 500, false)}},
		{Modified: []PlaidTxn{fakeTxn("txn-Z", "Hold", 0, false)}},
	}
	itemGUID, _ := exchangeAndLink(t, h, db, bookGUID, userID, false)

	first := doSync(t, h, itemGUID, bookGUID, userID)
	if first.Count != 1 {
		t.Fatalf("expected the $5 suggestion, got %d", first.Count)
	}
	second := doSync(t, h, itemGUID, bookGUID, userID)
	if second.Count != 0 {
		t.Fatalf("zeroed txn must drop the stale $5 suggestion, got %+v", second.Suggestions)
	}
	var staged int
	db.Pool.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM plaid_staged_transactions WHERE book_guid = $1 AND transaction_id = 'txn-Z'`,
		bookGUID).Scan(&staged)
	if staged != 0 {
		t.Fatalf("stale staged row must be deleted, %d remain", staged)
	}
}

// TestPlaidZeroSettleDropsPendingStagedRow (round-11 #6): a $0 posted txn
// whose pending predecessor is NOT in the same delta's Removed list must still
// clear the pending's stale non-zero suggestion.
func TestPlaidZeroSettleDropsPendingStagedRow(t *testing.T) {
	h, fake, db, bookGUID, userID := setupTestHandler(t)
	defer db.Teardown(context.Background())
	zeroPosted := fakeTxn("txn-Q", "Hold (settled)", 0, false)
	zeroPosted.PendingTransactionID = "txn-P"
	fake.onePagePerSync = true
	fake.deltaPages = []SyncDelta{
		{Added: []PlaidTxn{fakeTxn("txn-P", "Hold", 500, true)}},
		{Added: []PlaidTxn{zeroPosted}}, // note: txn-P deliberately NOT in Removed
	}
	itemGUID, _ := exchangeAndLink(t, h, db, bookGUID, userID, true)

	first := doSync(t, h, itemGUID, bookGUID, userID)
	if first.Count != 1 {
		t.Fatalf("expected the pending $5 suggestion, got %d", first.Count)
	}
	second := doSync(t, h, itemGUID, bookGUID, userID)
	if second.Count != 0 {
		t.Fatalf("$0 settle must clear the pending's stale suggestion, got %+v", second.Suggestions)
	}
}

// TestPlaidLegacyNilAADTokenIsReSealed (round-9 #1/#7, round-10 #2): tokens
// sealed before AAD was introduced decrypt ONLY behind the sunset flag, and
// are re-sealed with the primary key + AAD on first use.
func TestPlaidLegacyNilAADTokenIsReSealed(t *testing.T) {
	h, _, db, bookGUID, userID := setupTestHandler(t)
	defer db.Teardown(context.Background())
	// This scenario opts into the sunset flag (default is OFF).
	h.svc.allowLegacyTokens = true
	itemGUID, _ := exchangeAndLink(t, h, db, bookGUID, userID, false)

	// Re-seal the row the legacy way: same key (all-zero test key), nil AAD.
	key := make([]byte, 32)
	legacyCT, legacyNonce, err := encrypt(key, "access-sandbox-test-token", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Pool.Exec(context.Background(),
		`UPDATE plaid_items SET access_token_ciphertext = $1, access_token_nonce = $2 WHERE guid = $3`,
		legacyCT, legacyNonce, itemGUID); err != nil {
		t.Fatal(err)
	}

	// Sync must succeed via the legacy fallback…
	res := doSync(t, h, itemGUID, bookGUID, userID)
	if res.Count != 2 {
		t.Fatalf("sync with legacy-sealed token: expected 2 suggestions, got %d", res.Count)
	}

	// …and the stored ciphertext must now authenticate WITH the canonical AAD.
	var ct, nonce []byte
	var itemID string
	if err := db.Pool.QueryRow(context.Background(),
		`SELECT access_token_ciphertext, access_token_nonce, item_id FROM plaid_items WHERE guid = $1`, itemGUID,
	).Scan(&ct, &nonce, &itemID); err != nil {
		t.Fatal(err)
	}
	if _, err := decrypt(key, ct, nonce, tokenAAD(bookGUID, itemID)); err != nil {
		t.Fatalf("token was not re-sealed with AAD after legacy decrypt: %v", err)
	}
}

// TestPlaidDismissalSurvivesCrossPagePendingToPosted (round-9 #8): the pending
// is removed in one page/call and the posted successor only arrives later —
// the dismissal must still be inherited (tombstone semantics).
func TestPlaidDismissalSurvivesCrossPagePendingToPosted(t *testing.T) {
	h, fake, db, bookGUID, userID := setupTestHandler(t)
	defer db.Teardown(context.Background())
	posted := fakeTxn("txn-Q", "Subscription (posted)", 100, false)
	posted.PendingTransactionID = "txn-P"
	fake.onePagePerSync = true
	fake.deltaPages = []SyncDelta{
		{Added: []PlaidTxn{fakeTxn("txn-P", "Subscription", 100, true)}},
		{Removed: []string{"txn-P"}}, // pending removed in its own call…
		{Added: []PlaidTxn{posted}},  // …posted arrives only on the NEXT call
	}
	itemGUID, _ := exchangeAndLink(t, h, db, bookGUID, userID, true)

	doSync(t, h, itemGUID, bookGUID, userID)
	w := httptest.NewRecorder()
	h.handleDismiss(w, authedRequest("POST", "/dismiss", map[string]interface{}{"transaction_ids": []string{"txn-P"}}, bookGUID, userID))
	if w.Code != http.StatusOK {
		t.Fatalf("dismiss: %d", w.Code)
	}

	doSync(t, h, itemGUID, bookGUID, userID) // applies Removed[txn-P]
	final := doSync(t, h, itemGUID, bookGUID, userID)
	if final.Count != 0 {
		t.Fatalf("dismissal must survive the cross-page pending→posted handoff, got %+v", final.Suggestions)
	}
}

// TestPlaidZeroAmountSkipped (round-9 #9): $0 transactions are not
// representable in a double-entry book and must be dropped at staging.
func TestPlaidZeroAmountSkipped(t *testing.T) {
	h, fake, db, bookGUID, userID := setupTestHandler(t)
	defer db.Teardown(context.Background())
	fake.deltaPages = []SyncDelta{{Added: []PlaidTxn{fakeTxn("txn-zero", "Card check", 0, false)}}}
	itemGUID, _ := exchangeAndLink(t, h, db, bookGUID, userID, false)

	res := doSync(t, h, itemGUID, bookGUID, userID)
	if res.Count != 0 {
		t.Fatalf("zero-amount txn must not be suggested, got %d", res.Count)
	}
	var staged int
	db.Pool.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM plaid_staged_transactions WHERE book_guid = $1`, bookGUID).Scan(&staged)
	if staged != 0 {
		t.Fatalf("zero-amount txn must not be staged, got %d rows", staged)
	}
}

// denyAllLimiter stubs the rate limiter for the 429 paths.
type denyAllLimiter struct{}

func (denyAllLimiter) AllowN(_ context.Context, _ string, _ int) bool { return false }

// TestPlaidRateLimited (round-9 #4/#6): every metered endpoint returns 429
// when the per-user budget is exhausted.
func TestPlaidRateLimited(t *testing.T) {
	h, _, db, bookGUID, userID := setupTestHandler(t)
	defer db.Teardown(context.Background())
	h.limiter = denyAllLimiter{}

	w := httptest.NewRecorder()
	h.handleLinkToken(w, authedRequest("POST", "/link-token", nil, bookGUID, userID))
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("link-token: got %d, want 429", w.Code)
	}
	w = httptest.NewRecorder()
	h.handleExchange(w, authedRequest("POST", "/exchange", map[string]string{"public_token": "tok"}, bookGUID, userID))
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("exchange: got %d, want 429", w.Code)
	}
	w = httptest.NewRecorder()
	h.handleSync(w, authedRequest("POST", "/sync", map[string]string{"item_guid": "00000000-0000-0000-0000-000000000001"}, bookGUID, userID))
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("sync: got %d, want 429", w.Code)
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
