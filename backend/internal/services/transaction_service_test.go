package services

import (
	"context"
	"testing"
	"time"

	"github.com/user/antimoney/internal/auth"
	"github.com/user/antimoney/internal/models"
	"github.com/user/antimoney/internal/testutil"
)

func TestTransactionService(t *testing.T) {
	ctx := context.Background()
	db, err := testutil.SetupDB(ctx, "../../migrations")
	if err != nil {
		t.Fatalf("Failed to setup DB: %v", err)
	}
	defer db.Teardown(ctx)

	authService := auth.NewUserService(db.Pool)
	res, err := authService.Register(ctx, auth.RegisterRequest{
		Email:    "tx@example.com",
		Password: "pass",
		Name:     "Tx User",
	})
	if err != nil {
		t.Fatalf("Failed to register: %v", err)
	}

	// Add BookGUID to context
	ctx = context.WithValue(ctx, auth.BookGUIDKey, res.BookGUID)

	accSvc := NewAccountService(db.Pool)
	txSvc := NewTransactionService(db.Pool)

	// Create Asset Account
	desc := "Test"
	assetReq := CreateAccountRequest{
		Name:        "Test Asset",
		AccountType: models.AccountTypeAsset,
		Description: desc,
	}
	assetAcc, err := accSvc.CreateAccount(ctx, assetReq)
	if err != nil {
		t.Fatalf("Failed to create asset account: %v", err)
	}

	expenseReq := CreateAccountRequest{
		Name:        "Test Expense",
		AccountType: models.AccountTypeExpense,
		Description: desc,
	}
	expenseAcc, err := accSvc.CreateAccount(ctx, expenseReq)
	if err != nil {
		t.Fatalf("Failed to create expense account: %v", err)
	}

	// Create a balanced transaction
	now := time.Now()
	customId := "TX-100"
	req := CreateTransactionRequest{
		PostDate:    now,
		Description: "Office Supplies",
		CustomID:    customId,
		Splits: []CreateSplitRequest{
			{AccountGUID: assetAcc.GUID, ValueNum: -100, ValueDenom: 100, QuantityNum: -100, QuantityDenom: 100},
			{AccountGUID: expenseAcc.GUID, ValueNum: 100, ValueDenom: 100, QuantityNum: 100, QuantityDenom: 100},
		},
	}

	tx, err := txSvc.CreateTransaction(ctx, req)
	if err != nil {
		t.Fatalf("CreateTransaction failed: %v", err)
	}
	if tx.GUID == "" {
		t.Fatalf("Returned empty tx.GUID")
	}
	txGUID := tx.GUID

	// List transactions
	txs, err := txSvc.ListTransactions(ctx, 10, 0)
	if err != nil {
		t.Fatalf("ListTransactions failed: %v", err)
	}
	if len(txs) != 1 {
		t.Fatalf("Expected 1 transaction, got %d", len(txs))
	}

	// Update the transaction
	splits := []CreateSplitRequest{
		{AccountGUID: assetAcc.GUID, ValueNum: -200, ValueDenom: 100, QuantityNum: -200, QuantityDenom: 100},
		{AccountGUID: expenseAcc.GUID, ValueNum: 200, ValueDenom: 100, QuantityNum: 200, QuantityDenom: 100},
	}
	updateReq := UpdateTransactionRequest{
		PostDate:    now,
		Description: "New Supplies",
		CustomID:    customId,
		Splits:      splits,
	}
	_, err = txSvc.UpdateTransaction(ctx, txGUID, updateReq)
	if err != nil {
		t.Fatalf("UpdateTransaction failed: %v", err)
	}

	// Fetch Account Register
	reg, err := txSvc.GetAccountRegister(ctx, assetAcc.GUID)
	if err != nil {
		t.Fatalf("GetAccountRegister failed: %v", err)
	}
	if len(reg) == 0 {
		t.Fatalf("Expected register entries")
	}

	// Direct transfer: the asset register's transfer column should resolve to
	// the single other account in the balanced transaction (batched lookup).
	if got := reg[0].TransferAccount; got != "Test Expense" {
		t.Fatalf("expected transfer account 'Test Expense', got %q", got)
	}
	if got := reg[0].TransferAccountGUID; got != expenseAcc.GUID {
		t.Fatalf("expected transfer account guid %s, got %s", expenseAcc.GUID, got)
	}

	// Split transaction (3 splits): the transfer column should resolve to the
	// "-- Split Transaction --" sentinel with an empty guid.
	expense2, err := accSvc.CreateAccount(ctx, CreateAccountRequest{
		Name:        "Test Expense 2",
		AccountType: models.AccountTypeExpense,
		Description: desc,
	})
	if err != nil {
		t.Fatalf("Failed to create second expense account: %v", err)
	}
	_, err = txSvc.CreateTransaction(ctx, CreateTransactionRequest{
		PostDate:    now,
		Description: "Split Purchase",
		Splits: []CreateSplitRequest{
			{AccountGUID: assetAcc.GUID, ValueNum: -300, ValueDenom: 100, QuantityNum: -300, QuantityDenom: 100},
			{AccountGUID: expenseAcc.GUID, ValueNum: 100, ValueDenom: 100, QuantityNum: 100, QuantityDenom: 100},
			{AccountGUID: expense2.GUID, ValueNum: 200, ValueDenom: 100, QuantityNum: 200, QuantityDenom: 100},
		},
	})
	if err != nil {
		t.Fatalf("CreateTransaction (split) failed: %v", err)
	}
	reg2, err := txSvc.GetAccountRegister(ctx, assetAcc.GUID)
	if err != nil {
		t.Fatalf("GetAccountRegister (split) failed: %v", err)
	}
	var foundSplit bool
	for _, e := range reg2 {
		if e.Description == "Split Purchase" {
			foundSplit = true
			if e.TransferAccount != "-- Split Transaction --" {
				t.Fatalf("expected split transaction label, got %q", e.TransferAccount)
			}
			if e.TransferAccountGUID != "" {
				t.Fatalf("expected empty transfer guid for split, got %q", e.TransferAccountGUID)
			}
		}
	}
	if !foundSplit {
		t.Fatalf("split transaction not found in register")
	}

	// Test Unbalanced Transaction (auto-balancing)
	req2 := CreateTransactionRequest{
		PostDate:    now,
		Description: "Unbalanced",
		Splits: []CreateSplitRequest{
			{AccountGUID: assetAcc.GUID, ValueNum: -500, ValueDenom: 100, QuantityNum: -500, QuantityDenom: 100},
		},
	}
	_, err = txSvc.CreateTransaction(ctx, req2)
	if err != nil {
		t.Fatalf("CreateTransaction Auto-balance failed: %v", err)
	}

	// Check if Imbalance account was created
	accs, _ := accSvc.ListAccountsTree(ctx, "", "")
	imbalanceFound := false
	for _, a := range accs {
		if a.Name == "Imbalance" {
			imbalanceFound = true
			break
		}
	}
	if !imbalanceFound {
		t.Fatalf("Auto-balancing did not create Imbalance account properly if it wasn't pre-existing")
	}

	// Test splitting acknowledgement (Toggle Split Acknowledge)
	splitsList, _ := txSvc.ListTransactions(ctx, 10, 0)
	var splitGUID string
	for _, t := range splitsList {
		if t.Description == "Unbalanced" {
			splitGUID = t.Splits[0].GUID
			break
		}
	}
	if err := txSvc.ToggleSplitAcknowledge(ctx, splitGUID, "c"); err != nil {
		t.Fatalf("ToggleSplitAcknowledge failed: %v", err)
	}

	err = txSvc.DeleteTransaction(ctx, txGUID)
	if err != nil {
		t.Fatalf("DeleteTransaction failed: %v", err)
	}
}
