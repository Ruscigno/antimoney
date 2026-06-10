package plaid

import (
	"context"
	"testing"
	"time"

	"github.com/user/antimoney/internal/auth"
	"github.com/user/antimoney/internal/models"
	"github.com/user/antimoney/internal/services"
	"github.com/user/antimoney/internal/testutil"
)

func TestHistoryCategorizer(t *testing.T) {
	ctx := context.Background()
	db, err := testutil.SetupDB(ctx, "../../migrations")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Teardown(ctx)

	authSvc := auth.NewUserService(db.Pool)
	res, err := authSvc.Register(ctx, auth.RegisterRequest{Email: "cat@test.com", Password: "pass", Name: "Cat"})
	if err != nil {
		t.Fatal(err)
	}
	ctx = context.WithValue(ctx, auth.BookGUIDKey, res.BookGUID)

	accSvc := services.NewAccountService(db.Pool)
	txSvc := services.NewTransactionService(db.Pool)

	bank, _ := accSvc.CreateAccount(ctx, services.CreateAccountRequest{
		Name: "Chequing", AccountType: models.AccountTypeBank, Description: "",
	})
	dining, _ := accSvc.CreateAccount(ctx, services.CreateAccountRequest{
		Name: "Dining", AccountType: models.AccountTypeExpense, Description: "",
	})

	// Prior transaction: "TIM HORTONS #123" → Dining expense
	_, err = txSvc.CreateTransaction(ctx, services.CreateTransactionRequest{
		PostDate:    time.Date(2026, 1, 1, 11, 0, 0, 0, time.UTC),
		Description: "TIM HORTONS #123",
		Splits: []services.CreateSplitRequest{
			{AccountGUID: bank.GUID, ValueNum: -250, ValueDenom: 100, QuantityNum: -250, QuantityDenom: 100},
			{AccountGUID: dining.GUID, ValueNum: 250, ValueDenom: 100, QuantityNum: 250, QuantityDenom: 100},
		},
	})
	if err != nil {
		t.Fatalf("seed transaction: %v", err)
	}

	cat := NewHistoryCategorizer(db.Pool)

	// Substring match: "tim hortons" matches "TIM HORTONS #123"
	got, ok := cat.Suggest(ctx, res.BookGUID, PlaidTxn{Description: "Tim Hortons"})
	if !ok {
		t.Fatal("expected a category suggestion")
	}
	if got != dining.GUID {
		t.Fatalf("got %q, want dining GUID %q", got, dining.GUID)
	}

	// Unknown payee → no suggestion
	_, ok = cat.Suggest(ctx, res.BookGUID, PlaidTxn{Description: "Unknown XYZ"})
	if ok {
		t.Fatal("expected no suggestion for unknown payee")
	}

	// Exact-match priority (spec §7): an older exact match must beat a NEWER
	// substring match.
	coffee, _ := accSvc.CreateAccount(ctx, services.CreateAccountRequest{
		Name: "Coffee", AccountType: models.AccountTypeExpense,
	})
	// Older transaction whose description matches exactly (after normalization).
	_, err = txSvc.CreateTransaction(ctx, services.CreateTransactionRequest{
		PostDate:    time.Date(2025, 6, 1, 11, 0, 0, 0, time.UTC),
		Description: "  Tim Hortons ", // trims + case-folds to "tim hortons"
		Splits: []services.CreateSplitRequest{
			{AccountGUID: bank.GUID, ValueNum: -300, ValueDenom: 100, QuantityNum: -300, QuantityDenom: 100},
			{AccountGUID: coffee.GUID, ValueNum: 300, ValueDenom: 100, QuantityNum: 300, QuantityDenom: 100},
		},
	})
	if err != nil {
		t.Fatalf("seed exact-match transaction: %v", err)
	}
	// The substring candidate ("TIM HORTONS #123" → Dining, post 2026-01-01) is
	// MORE RECENT than the exact candidate (2025-06-01); exact must still win.
	got, ok = cat.Suggest(ctx, res.BookGUID, PlaidTxn{Description: "Tim Hortons"})
	if !ok {
		t.Fatal("expected a suggestion")
	}
	if got != coffee.GUID {
		t.Fatalf("exact match must take priority: got %q, want coffee GUID %q", got, coffee.GUID)
	}
}
