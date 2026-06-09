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
}
