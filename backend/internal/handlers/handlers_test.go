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

	resp, _ = client.Do(importReq)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST /data/import failed: %d", resp.StatusCode)
	}

	// Delete Transaction (might be 404 if import wiped DB)
	_ = doReq("DELETE", "/transactions/"+txGUID, nil)

	// Delete Account (might be 404 if import wiped DB)
	_ = doReq("DELETE", "/accounts/"+expGUID, nil)
}
