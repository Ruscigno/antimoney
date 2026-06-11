package plaid

import (
	"context"
	"fmt"
	"time"
)

// fakePlaidClient implements PlaidClient for tests; no network calls.
type fakePlaidClient struct {
	linkToken   string
	accessToken string
	itemID      string
	institution string
	accounts    []PlaidAccount
	deltaPages  []SyncDelta // one delta per SyncTransactions call
	pageIndex   int
	removeErr   error
	syncErr     error // returned by SyncTransactions when set
	// onePagePerSync reports has_more=false after every page, so each Sync()
	// call consumes exactly one page (simulates deltas arriving over time).
	onePagePerSync bool
}

func newFakeClient() *fakePlaidClient {
	return &fakePlaidClient{
		linkToken:   "link-sandbox-test-token",
		accessToken: "access-sandbox-test-token",
		itemID:      "item-sandbox-test-id",
		institution: "First Platypus Bank",
		accounts: []PlaidAccount{
			{AccountID: "plaid-acct-001", Name: "Plaid Checking", Mask: "0000", Type: "depository"},
		},
		deltaPages: []SyncDelta{
			{Added: []PlaidTxn{
				{
					TransactionID: "txn-001",
					Date:          time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC),
					Description:   "Tim Hortons",
					AmountNum:     250,
					AmountDenom:   100,
					AccountID:     "plaid-acct-001",
					Pending:       false,
				},
				{
					TransactionID: "txn-002",
					Date:          time.Date(2026, 6, 2, 0, 0, 0, 0, time.UTC),
					Description:   "Grocery Store",
					AmountNum:     5499,
					AmountDenom:   100,
					AccountID:     "plaid-acct-001",
					Pending:       false,
				},
			}},
		},
	}
}

func (f *fakePlaidClient) CreateLinkToken(_ context.Context, _, _ string) (string, error) {
	return f.linkToken, nil
}

func (f *fakePlaidClient) ExchangePublicToken(_ context.Context, _ string) (string, string, string, error) {
	return f.accessToken, f.itemID, f.institution, nil
}

func (f *fakePlaidClient) GetAccounts(_ context.Context, _ string) ([]PlaidAccount, error) {
	return f.accounts, nil
}

func (f *fakePlaidClient) SyncTransactions(_ context.Context, _, _ string) (SyncDelta, string, bool, error) {
	if f.syncErr != nil {
		return SyncDelta{}, "", false, f.syncErr
	}
	if f.pageIndex >= len(f.deltaPages) {
		return SyncDelta{}, fmt.Sprintf("cursor-%d", f.pageIndex), false, nil
	}
	page := f.deltaPages[f.pageIndex]
	f.pageIndex++
	hasMore := f.pageIndex < len(f.deltaPages)
	if f.onePagePerSync {
		hasMore = false
	}
	return page, fmt.Sprintf("cursor-%d", f.pageIndex), hasMore, nil
}

func (f *fakePlaidClient) RemoveItem(_ context.Context, _ string) error {
	return f.removeErr
}
