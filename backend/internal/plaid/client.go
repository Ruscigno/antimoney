package plaid

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math"
	"time"

	plaidapi "github.com/plaid/plaid-go/v26/plaid"
)

// ErrReauthRequired signals Plaid's ITEM_LOGIN_REQUIRED: the stored credentials
// no longer work and the user must re-authenticate the bank connection
// (resolved by disconnecting and reconnecting via Link).
var ErrReauthRequired = errors.New("plaid item requires re-authentication")

// PlaidTxn is a normalized Plaid transaction. AmountNum > 0 means money leaving
// the bank account (a purchase/payment).
type PlaidTxn struct {
	TransactionID string
	// PendingTransactionID links a posted transaction back to the pending
	// transaction it replaces (Plaid issues a NEW id when a pending posts).
	PendingTransactionID string
	Date                 time.Time
	Description          string
	AmountNum            int64 // positive = debit from bank account
	AmountDenom          int64
	AccountID            string
	Pending              bool
}

// SyncDelta is one page of /transactions/sync. Modified entries are full
// transactions to re-stage; Removed lists transaction_ids to drop (e.g. a
// pending transaction that posted under a new id).
type SyncDelta struct {
	Added    []PlaidTxn
	Modified []PlaidTxn
	Removed  []string
}

// PlaidAccount is a bank account from /accounts/get.
type PlaidAccount struct {
	AccountID string
	Name      string
	Mask      string
	Type      string
}

// PlaidClient is the interface over the Plaid REST API.
type PlaidClient interface {
	CreateLinkToken(ctx context.Context, userID string) (linkToken string, err error)
	ExchangePublicToken(ctx context.Context, publicToken string) (accessToken, itemID, institutionName string, err error)
	GetAccounts(ctx context.Context, accessToken string) ([]PlaidAccount, error)
	SyncTransactions(ctx context.Context, accessToken, cursor string) (delta SyncDelta, nextCursor string, hasMore bool, err error)
	RemoveItem(ctx context.Context, accessToken string) error
}

type realPlaidClient struct {
	api *plaidapi.APIClient
}

// NewRealPlaidClient creates a PlaidClient backed by the Plaid SDK.
// env must be "sandbox" or "production".
func NewRealPlaidClient(clientID, secret, env string) PlaidClient {
	cfg := plaidapi.NewConfiguration()
	cfg.AddDefaultHeader("PLAID-CLIENT-ID", clientID)
	cfg.AddDefaultHeader("PLAID-SECRET", secret)
	if env == "production" {
		cfg.UseEnvironment(plaidapi.Production)
	} else {
		cfg.UseEnvironment(plaidapi.Sandbox)
	}
	return &realPlaidClient{api: plaidapi.NewAPIClient(cfg)}
}

func (c *realPlaidClient) CreateLinkToken(ctx context.Context, userID string) (string, error) {
	user := plaidapi.NewLinkTokenCreateRequestUser(userID)
	req := plaidapi.NewLinkTokenCreateRequest(
		"Antimoney",
		"en",
		[]plaidapi.CountryCode{plaidapi.COUNTRYCODE_CA},
		*user,
	)
	req.SetProducts([]plaidapi.Products{plaidapi.PRODUCTS_TRANSACTIONS})
	resp, _, err := c.api.PlaidApi.LinkTokenCreate(ctx).LinkTokenCreateRequest(*req).Execute()
	if err != nil {
		return "", plaidErr("CreateLinkToken", err)
	}
	return resp.GetLinkToken(), nil
}

func (c *realPlaidClient) ExchangePublicToken(ctx context.Context, publicToken string) (string, string, string, error) {
	req := plaidapi.NewItemPublicTokenExchangeRequest(publicToken)
	resp, _, err := c.api.PlaidApi.ItemPublicTokenExchange(ctx).ItemPublicTokenExchangeRequest(*req).Execute()
	if err != nil {
		return "", "", "", plaidErr("ExchangePublicToken", err)
	}
	accessToken := resp.GetAccessToken()
	itemID := resp.GetItemId()

	institutionName := ""
	itemResp, _, ierr := c.api.PlaidApi.ItemGet(ctx).ItemGetRequest(
		*plaidapi.NewItemGetRequest(accessToken),
	).Execute()
	if ierr == nil {
		item := itemResp.GetItem()
		if item.HasInstitutionId() {
			instReq := plaidapi.NewInstitutionsGetByIdRequest(
				item.GetInstitutionId(),
				[]plaidapi.CountryCode{plaidapi.COUNTRYCODE_CA},
			)
			instResp, _, instErr := c.api.PlaidApi.InstitutionsGetById(ctx).InstitutionsGetByIdRequest(*instReq).Execute()
			if instErr == nil {
				institutionName = instResp.Institution.GetName()
			}
		}
	}
	return accessToken, itemID, institutionName, nil
}

func (c *realPlaidClient) GetAccounts(ctx context.Context, accessToken string) ([]PlaidAccount, error) {
	req := plaidapi.NewAccountsGetRequest(accessToken)
	resp, _, err := c.api.PlaidApi.AccountsGet(ctx).AccountsGetRequest(*req).Execute()
	if err != nil {
		return nil, plaidErr("GetAccounts", err)
	}
	out := make([]PlaidAccount, 0, len(resp.Accounts))
	for _, a := range resp.Accounts {
		out = append(out, PlaidAccount{
			AccountID: a.GetAccountId(),
			Name:      a.GetName(),
			Mask:      a.GetMask(),
			Type:      string(a.GetType()),
		})
	}
	return out, nil
}

func (c *realPlaidClient) SyncTransactions(ctx context.Context, accessToken, cursor string) (SyncDelta, string, bool, error) {
	req := plaidapi.NewTransactionsSyncRequest(accessToken)
	if cursor != "" {
		req.SetCursor(cursor)
	}
	resp, _, err := c.api.PlaidApi.TransactionsSync(ctx).TransactionsSyncRequest(*req).Execute()
	if err != nil {
		return SyncDelta{}, "", false, plaidErr("SyncTransactions", err)
	}
	delta := SyncDelta{
		Added:    convertTxns(resp.Added),
		Modified: convertTxns(resp.Modified),
		Removed:  make([]string, 0, len(resp.Removed)),
	}
	for _, r := range resp.Removed {
		delta.Removed = append(delta.Removed, r.GetTransactionId())
	}
	return delta, resp.GetNextCursor(), resp.GetHasMore(), nil
}

// convertTxns normalizes Plaid SDK transactions (used for both Added and
// Modified — a modified transaction is simply re-staged with its new values).
func convertTxns(in []plaidapi.Transaction) []PlaidTxn {
	out := make([]PlaidTxn, 0, len(in))
	for _, t := range in {
		date, err := time.Parse("2006-01-02", t.GetDate())
		if err != nil {
			// Don't import a transaction with a zero date — log and skip at the
			// source instead of letting a corrupted row flow downstream.
			log.Printf("plaid SyncTransactions: skipping txn %s: unparseable date %q: %v",
				t.GetTransactionId(), t.GetDate(), err)
			continue
		}
		// Plaid returns amounts as float64; round to whole cents (denom = 100).
		// math.Round is essential because float64 cannot represent most cent
		// fractions exactly: 0.29 * 100 evaluates to 28.999999999999996, so plain
		// int64(f*100) would truncate it to 28 instead of 29. The integer cent
		// values that result after rounding ARE exactly representable.
		// (Accepted float boundary — see the ADR in docs/adr.md.)
		amountNum := int64(math.Round(t.GetAmount() * 100))
		out = append(out, PlaidTxn{
			TransactionID:        t.GetTransactionId(),
			PendingTransactionID: t.GetPendingTransactionId(),
			Date:                 date,
			Description:          t.GetName(),
			AmountNum:            amountNum,
			// MVP limitation: denom 100 assumes 2-decimal currencies (CAD/USD).
			// Zero-decimal (JPY) or 3-decimal (BHD) currencies would need the
			// account commodity's exponent — see spec §13 Known limitations.
			AmountDenom: 100,
			AccountID:   t.GetAccountId(),
			Pending:     t.GetPending(),
		})
	}
	return out
}

func (c *realPlaidClient) RemoveItem(ctx context.Context, accessToken string) error {
	req := plaidapi.NewItemRemoveRequest(accessToken)
	_, _, err := c.api.PlaidApi.ItemRemove(ctx).ItemRemoveRequest(*req).Execute()
	return plaidErr("RemoveItem", err)
}

// plaidErr logs a whitelisted subset of the Plaid error server-side (so
// production failures are diagnosable without dumping a third-party-controlled
// response body into application logs) and returns a generic error for the
// caller. error_code/error_type/request_id are Plaid-defined enums/ids — never
// credentials or free-form content.
func plaidErr(op string, err error) error {
	if err == nil {
		return nil
	}
	if e, ok := err.(interface{ Body() []byte }); ok {
		var pe struct {
			ErrorCode string `json:"error_code"`
			ErrorType string `json:"error_type"`
			RequestID string `json:"request_id"`
		}
		if jsonErr := json.Unmarshal(e.Body(), &pe); jsonErr == nil && pe.ErrorCode != "" {
			log.Printf("plaid %s error: code=%s type=%s request_id=%s", op, pe.ErrorCode, pe.ErrorType, pe.RequestID)
			if pe.ErrorCode == "ITEM_LOGIN_REQUIRED" {
				return ErrReauthRequired
			}
		} else {
			log.Printf("plaid %s error: %v (unparseable error body)", op, err)
		}
	} else {
		log.Printf("plaid %s error: %v", op, err)
	}
	return fmt.Errorf("plaid API error during %s", op)
}
