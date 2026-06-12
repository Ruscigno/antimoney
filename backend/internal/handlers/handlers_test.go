package handlers_test

import (
	"bytes"
	"context"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/user/antimoney/internal/auth"
	"github.com/user/antimoney/internal/handlers"
	"github.com/user/antimoney/internal/services"
	"github.com/user/antimoney/internal/testutil"
)

func TestHandlers(t *testing.T) {
	ctx := context.Background()
	db, err := testutil.SetupDB(ctx, "../../migrations")
	if err != nil {
		t.Fatalf("Failed to setup DB: %v", err)
	}
	defer db.Teardown(ctx)

	auth.SetJWTSecret("test-secret-1234")
	userSvc := auth.NewUserService(db.Pool)

	// Register a test user
	req := auth.RegisterRequest{
		Email:    "handler@test.com",
		Password: "password",
		Name:     "Handler Test",
	}
	res, err := userSvc.Register(ctx, req)
	if err != nil {
		t.Fatalf("Failed to register test user: %v", err)
	}

	txSvc := services.NewTransactionService(db.Pool)
	acctSvc := services.NewAccountService(db.Pool)
	snapshotSvc := services.NewSnapshotService(db.Pool)

	txHandler := handlers.NewTransactionHandler(txSvc)
	acctHandler := handlers.NewAccountHandler(acctSvc, txSvc)
	importHandler := handlers.NewImportExportHandler(db.Pool, txSvc, snapshotSvc)

	r := chi.NewRouter()
	r.Use(auth.RequireAuth)

	r.Mount("/transactions", txHandler.Routes())
	r.Mount("/accounts", acctHandler.Routes())
	r.Mount("/data", importHandler.Routes())

	ts := httptest.NewServer(r)
	defer ts.Close()

	client := &http.Client{Timeout: 5 * time.Second}
	doReq := func(method, path string, body interface{}) *http.Response {
		var b []byte
		if body != nil {
			b, _ = json.Marshal(body)
		}
		req, _ := http.NewRequest(method, ts.URL+path, bytes.NewReader(b))
		req.Header.Set("Authorization", "Bearer "+res.Token)
		req.Header.Set("Content-Type", "application/json")
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("Request %s %s failed: %v", method, path, err)
		}
		return resp
	}

	// 1. Get Accounts (Tree)
	resp := doReq("GET", "/accounts", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /accounts failed: %d", resp.StatusCode)
	}

	// Create an account
	createAccReq := map[string]interface{}{
		"name":         "Handler Asset",
		"account_type": "ASSET",
		"description":  "desc",
	}
	resp = doReq("POST", "/accounts", createAccReq)
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		t.Fatalf("POST /accounts failed: %d", resp.StatusCode)
	}

	var acc map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&acc)
	accGUID := acc["guid"].(string)

	resp = doReq("POST", "/accounts", map[string]interface{}{
		"name":         "Handler Exp",
		"account_type": "EXPENSE",
	})
	json.NewDecoder(resp.Body).Decode(&acc)
	expGUID := acc["guid"].(string)

	// Create a transaction
	createTxReq := map[string]interface{}{
		"post_date":   time.Now().Format(time.RFC3339),
		"description": "Lunch",
		"splits": []map[string]interface{}{
			{"account_guid": accGUID, "value_num": -1500, "value_denom": 100, "quantity_num": -1500, "quantity_denom": 100},
			{"account_guid": expGUID, "value_num": 1500, "value_denom": 100, "quantity_num": 1500, "quantity_denom": 100},
		},
	}
	resp = doReq("POST", "/transactions", createTxReq)
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		t.Fatalf("POST /transactions failed: %d", resp.StatusCode)
	}

	var tx map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&tx)
	txGUID := tx["guid"].(string)

	// List Transactions
	resp = doReq("GET", "/transactions", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /transactions failed")
	}

	// Update Transaction
	createTxReq["description"] = "Updated Lunch"
	resp = doReq("PUT", "/transactions/"+txGUID, createTxReq)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("PUT /transactions failed: %d", resp.StatusCode)
	}

	// Export data
	resp = doReq("GET", "/data/export", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /data/export failed: %d", resp.StatusCode)
	}

	var exportData map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&exportData)
	if len(exportData) == 0 {
		t.Fatalf("Exported data is empty")
	}

	// Update Account
	_ = doReq("PUT", "/accounts/"+accGUID, map[string]interface{}{
		"name":         "Updated Handler Asset",
		"account_type": "ASSET",
		"version":      1,
	})

	// Get Account Register
	resp = doReq("GET", "/accounts/"+accGUID+"/register", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /accounts/register failed: %d", resp.StatusCode)
	}

	resp = doReq("GET", "/accounts/"+accGUID+"/reconciled-balance", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /accounts/reconciled-balance failed: %d", resp.StatusCode)
	}

	// Reconcile Account
	resp = doReq("POST", "/accounts/"+accGUID+"/reconcile", map[string]interface{}{
		"account_guids": []string{accGUID},
	})
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		t.Fatalf("POST /accounts/reconcile failed: %d", resp.StatusCode)
	}

	// Import Data
	importPayloadBody, err := os.ReadFile("../../../scripts/import.json")
	if err != nil {
		t.Fatalf("failed to read import.json: %v", err)
	}

	bodyBuf := new(bytes.Buffer)
	writer := multipart.NewWriter(bodyBuf)
	part, _ := writer.CreateFormFile("file", "import.json")
	part.Write(importPayloadBody)
	writer.Close()

	importReq, _ := http.NewRequest("POST", ts.URL+"/data/import", bodyBuf)
	importReq.Header.Set("Authorization", "Bearer "+res.Token)
	importReq.Header.Set("Content-Type", writer.FormDataContentType())

	// The import is the one bulk request in this suite, so it gets a dedicated
	// client with a generous timeout. The 35s is pure slow-runner tolerance
	// chosen with margin (the batched import takes ~1-2s), NOT derived from any
	// server cap: this test router mounts no timeout middleware, and in
	// production handleImport writes its own 500 before chi could 504. If the
	// import ever exceeds this, the client times out and the error is CHECKED
	// below — a diagnostic failure, never a nil-resp panic.
	importClient := &http.Client{Timeout: 35 * time.Second}
	resp, err = importClient.Do(importReq)
	if err != nil {
		t.Fatalf("POST /data/import request failed: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST /data/import failed: %d", resp.StatusCode)
	}

	// Delete Transaction (might be 404 if import wiped DB)
	_ = doReq("DELETE", "/transactions/"+txGUID, nil)

	// Delete Account (might be 404 if import wiped DB)
	_ = doReq("DELETE", "/accounts/"+expGUID, nil)
}

// TestImportFailureRollsBackAtomically exercises the batch ERROR path (PR #7
// review #2): an import whose split references a nonexistent account violates
// the splits.account_guid FK mid-batch; the handler must return 500 and the
// single DB transaction must roll back integrally — including the destructive
// cleanup DELETEs that ran before the failing insert.
func TestImportFailureRollsBackAtomically(t *testing.T) {
	ctx := context.Background()
	db, err := testutil.SetupDB(ctx, "../../migrations")
	if err != nil {
		t.Fatalf("Failed to setup DB: %v", err)
	}
	defer db.Teardown(ctx)

	auth.SetJWTSecret("test-secret-1234")
	userSvc := auth.NewUserService(db.Pool)
	res, err := userSvc.Register(ctx, auth.RegisterRequest{Email: "rollback@test.com", Password: "password", Name: "Rollback"})
	if err != nil {
		t.Fatal(err)
	}

	txSvc := services.NewTransactionService(db.Pool)
	snapshotSvc := services.NewSnapshotService(db.Pool)
	importHandler := handlers.NewImportExportHandler(db.Pool, txSvc, snapshotSvc)
	acctSvc := services.NewAccountService(db.Pool)
	acctHandler := handlers.NewAccountHandler(acctSvc, txSvc)

	r := chi.NewRouter()
	r.Use(auth.RequireAuth)
	r.Mount("/data", importHandler.Routes())
	r.Mount("/accounts", acctHandler.Routes())
	ts := httptest.NewServer(r)
	defer ts.Close()

	client := &http.Client{Timeout: 10 * time.Second}
	authedJSON := func(method, path string, body []byte, contentType string) *http.Response {
		req, _ := http.NewRequest(method, ts.URL+path, bytes.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+res.Token)
		req.Header.Set("Content-Type", contentType)
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("%s %s failed: %v", method, path, err)
		}
		return resp
	}

	// Baseline: the registration seeded a chart of accounts.
	countAccounts := func() int {
		var n int
		if err := db.Pool.QueryRow(ctx,
			`SELECT COUNT(*) FROM accounts WHERE book_guid = $1`, res.BookGUID).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	before := countAccounts()
	if before == 0 {
		t.Fatal("expected a seeded chart of accounts")
	}

	// Malformed import: the split references an account guid that the payload
	// never defines → FK violation inside the "insert split" batch.
	payload := `{
		"accounts": [
			{"guid": "11111111-1111-1111-1111-111111111111", "name": "Root", "account_type": "ROOT", "parent_guid": null, "placeholder": true, "description": ""}
		],
		"transactions": [
			{"guid": "22222222-2222-2222-2222-222222222222", "post_date": "2026-01-02T11:00:00Z", "enter_date": "2026-01-02T11:00:00Z", "description": "broken",
			 "splits": [
				{"guid": "33333333-3333-3333-3333-333333333333", "account_guid": "99999999-9999-9999-9999-999999999999", "memo": "", "value_num": 100, "value_denom": 100, "quantity_num": 100, "quantity_denom": 100, "reconcile_state": "n"}
			 ]}
		]
	}`
	bodyBuf := new(bytes.Buffer)
	writer := multipart.NewWriter(bodyBuf)
	part, _ := writer.CreateFormFile("file", "broken.json")
	part.Write([]byte(payload))
	writer.Close()

	resp := authedJSON("POST", "/data/import", bodyBuf.Bytes(), writer.FormDataContentType())
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("failing import: got %d, want 500", resp.StatusCode)
	}

	// Atomicity: the import's cleanup DELETEs ran in the same transaction as
	// the failing batch — a rollback must leave the original chart intact.
	if after := countAccounts(); after != before {
		t.Fatalf("failed import must roll back integrally: %d accounts before, %d after", before, after)
	}
}

// TestCSVImportSmoke covers the CSV path, a shared caller of the import
// machinery that previously had no HTTP test (PR #7 review #3).
func TestCSVImportSmoke(t *testing.T) {
	ctx := context.Background()
	db, err := testutil.SetupDB(ctx, "../../migrations")
	if err != nil {
		t.Fatalf("Failed to setup DB: %v", err)
	}
	defer db.Teardown(ctx)

	auth.SetJWTSecret("test-secret-1234")
	userSvc := auth.NewUserService(db.Pool)
	res, err := userSvc.Register(ctx, auth.RegisterRequest{Email: "csv@test.com", Password: "password", Name: "CSV"})
	if err != nil {
		t.Fatal(err)
	}

	txSvc := services.NewTransactionService(db.Pool)
	acctSvc := services.NewAccountService(db.Pool)
	snapshotSvc := services.NewSnapshotService(db.Pool)
	importHandler := handlers.NewImportExportHandler(db.Pool, txSvc, snapshotSvc)
	acctHandler := handlers.NewAccountHandler(acctSvc, txSvc)

	r := chi.NewRouter()
	r.Use(auth.RequireAuth)
	r.Mount("/data", importHandler.Routes())
	r.Mount("/accounts", acctHandler.Routes())
	ts := httptest.NewServer(r)
	defer ts.Close()

	client := &http.Client{Timeout: 10 * time.Second}

	// Target account for the CSV rows.
	ctxBook := context.WithValue(ctx, auth.BookGUIDKey, res.BookGUID)
	bank, err := acctSvc.CreateAccount(ctxBook, services.CreateAccountRequest{Name: "CSV Chequing", AccountType: "BANK"})
	if err != nil {
		t.Fatal(err)
	}

	csvBody := "Date,Description,Amount\n2026-01-02,Coffee,-4.50\n2026-01-03,Salary,1500.00\n"
	bodyBuf := new(bytes.Buffer)
	writer := multipart.NewWriter(bodyBuf)
	writer.WriteField("account_guid", bank.GUID)
	part, _ := writer.CreateFormFile("file", "rows.csv")
	part.Write([]byte(csvBody))
	writer.Close()

	req, _ := http.NewRequest("POST", ts.URL+"/data/import/csv", bodyBuf)
	req.Header.Set("Authorization", "Bearer "+res.Token)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("POST /data/import/csv failed: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("CSV import: got %d, want 200", resp.StatusCode)
	}
	var out map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if count, _ := out["count"].(float64); count < 2 {
		t.Fatalf("expected 2 imported CSV rows, got %v", out["count"])
	}

	// The rows actually landed as transactions on the target account.
	var txns int
	if err := db.Pool.QueryRow(ctx,
		`SELECT COUNT(DISTINCT t.guid) FROM transactions t
		 JOIN splits s ON s.tx_guid = t.guid
		 WHERE t.book_guid = $1 AND s.account_guid = $2`,
		res.BookGUID, bank.GUID).Scan(&txns); err != nil {
		t.Fatal(err)
	}
	if txns != 2 {
		t.Fatalf("expected 2 transactions on the CSV target account, got %d", txns)
	}
}
