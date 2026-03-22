package services

import (
	"context"
	"testing"

	"github.com/user/antimoney/internal/auth"
	"github.com/user/antimoney/internal/models"
	"github.com/user/antimoney/internal/testutil"
)

func TestAccountService(t *testing.T) {
	ctx := context.Background()
	// Set the book GUID in the context as required
	db, err := testutil.SetupDB(ctx, "../../migrations")
	if err != nil {
		t.Fatalf("Failed to setup DB: %v", err)
	}
	defer db.Teardown(ctx)

	// create user/book
	authService := auth.NewUserService(db.Pool)
	res, err := authService.Register(ctx, auth.RegisterRequest{
		Email:    "acc@example.com",
		Password: "pass",
		Name:     "Acc User",
	})
	if err != nil {
		t.Fatalf("Failed to register: %v", err)
	}

	// Set BookGUID into context since services use auth.BookGUIDFromCtx
	ctx = context.WithValue(ctx, auth.BookGUIDKey, res.BookGUID)

	svc := NewAccountService(db.Pool)

	// List default accounts (ListAccountsTree)
	accs, err := svc.ListAccountsTree(ctx)
	if err != nil {
		t.Fatalf("ListAccountsTree failed: %v", err)
	}
	if len(accs) == 0 {
		t.Fatalf("Expected some default seeded accounts")
	}

	// Create new account
	var parentGUID string
	for _, a := range accs {
		if a.AccountType == models.AccountTypeAsset {
			parentGUID = a.GUID
			break
		}
	}

	desc := "Test Description"

	createReq := CreateAccountRequest{
		Name:        "Test Bank",
		AccountType: models.AccountTypeBank,
		ParentGUID:  &parentGUID,
		Description: desc,
	}
	newAcc, err := svc.CreateAccount(ctx, createReq)
	if err != nil {
		t.Fatalf("CreateAccount failed: %v", err)
	}
	if newAcc.Name != "Test Bank" {
		t.Fatalf("Unexpected new account: %+v", newAcc)
	}

	// Update account
	newName := "Updated Bank"
	tType := models.AccountTypeBank
	updateReq := UpdateAccountRequest{
		Name:        &newName,
		AccountType: &tType,
		Description: &desc,
		Version:     newAcc.Version,
	}
	newAcc, err = svc.UpdateAccount(ctx, newAcc.GUID, updateReq)
	if err != nil {
		t.Fatalf("UpdateAccount failed: %v", err)
	}
	if newAcc.Name != newName {
		t.Fatalf("Expected name %s, got %s", newName, newAcc.Name)
	}

	// Delete account
	err = svc.DeleteAccount(ctx, newAcc.GUID)
	if err != nil {
		t.Fatalf("DeleteAccount failed: %v", err)
	}
}
