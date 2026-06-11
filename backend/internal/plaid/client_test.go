package plaid

import (
	"errors"
	"testing"

	plaidapi "github.com/plaid/plaid-go/v26/plaid"
)

func sdkTxn(id, date string, amount float64) plaidapi.Transaction {
	var t plaidapi.Transaction
	t.SetTransactionId(id)
	t.SetDate(date)
	t.SetName("desc " + id)
	t.SetAmount(amount)
	t.SetAccountId("acct-1")
	t.SetPending(false)
	return t
}

// TestConvertTxnsRounding pins the float→cents boundary (ADR-001): values
// whose float64 product would truncate wrong must round to the exact cent.
func TestConvertTxnsRounding(t *testing.T) {
	cases := []struct {
		amount float64
		want   int64
	}{
		{0.29, 29},   // 0.29*100 == 28.999999999999996 → plain int64() would give 28
		{0.58, 58},   // 57.99999999999999 → 57 without rounding
		{-0.29, -29}, // sign-symmetric
		{0.07, 7},
		{0.10, 10},
		{54.99, 5499},
		{-120.00, -12000},
	}
	for _, c := range cases {
		out := convertTxns([]plaidapi.Transaction{sdkTxn("t", "2026-06-01", c.amount)})
		if len(out) != 1 {
			t.Fatalf("amount %v: expected 1 txn, got %d", c.amount, len(out))
		}
		if out[0].AmountNum != c.want || out[0].AmountDenom != 100 {
			t.Fatalf("amount %v: got %d/%d, want %d/100", c.amount, out[0].AmountNum, out[0].AmountDenom, c.want)
		}
	}
}

func TestConvertTxnsSkipsUnparseableDate(t *testing.T) {
	out := convertTxns([]plaidapi.Transaction{
		sdkTxn("bad", "not-a-date", 1.00),
		sdkTxn("good", "2026-06-02", 2.00),
	})
	if len(out) != 1 || out[0].TransactionID != "good" {
		t.Fatalf("expected only the parseable txn, got %+v", out)
	}
}

func TestConvertTxnsCarriesPendingCorrelation(t *testing.T) {
	txn := sdkTxn("posted-1", "2026-06-03", 3.50)
	txn.SetPendingTransactionId("pend-1")
	out := convertTxns([]plaidapi.Transaction{txn})
	if len(out) != 1 || out[0].PendingTransactionID != "pend-1" {
		t.Fatalf("pending_transaction_id must be carried through, got %+v", out)
	}
}

// bodyErr stubs the Plaid SDK error type (exposes the raw response body).
type bodyErr struct{ body []byte }

func (e *bodyErr) Error() string { return "plaid sdk error" }
func (e *bodyErr) Body() []byte  { return e.body }

func TestPlaidErrClassification(t *testing.T) {
	// ITEM_LOGIN_REQUIRED in the parsed error_code → sentinel for the reauth flow.
	err := plaidErr("Sync", &bodyErr{body: []byte(`{"error_code":"ITEM_LOGIN_REQUIRED","error_type":"ITEM_ERROR","request_id":"req-1"}`)})
	if !errors.Is(err, ErrReauthRequired) {
		t.Fatalf("ITEM_LOGIN_REQUIRED must map to ErrReauthRequired, got %v", err)
	}

	// Any other code → generic error naming the operation, never the body.
	err = plaidErr("Sync", &bodyErr{body: []byte(`{"error_code":"RATE_LIMIT","error_type":"RATE_LIMIT_EXCEEDED","request_id":"req-2"}`)})
	if errors.Is(err, ErrReauthRequired) || err == nil {
		t.Fatalf("non-reauth code must be generic, got %v", err)
	}
	if got := err.Error(); got != "plaid API error during Sync" {
		t.Fatalf("generic error must not leak details, got %q", got)
	}

	// Unparseable body and plain errors → generic.
	if err := plaidErr("Op", &bodyErr{body: []byte("<html>oops</html>")}); errors.Is(err, ErrReauthRequired) {
		t.Fatal("unparseable body must not classify as reauth")
	}
	if err := plaidErr("Op", errors.New("net: timeout")); err == nil || errors.Is(err, ErrReauthRequired) {
		t.Fatalf("plain error must be generic, got %v", err)
	}
	if err := plaidErr("Op", nil); err != nil {
		t.Fatalf("nil error must stay nil, got %v", err)
	}
}
