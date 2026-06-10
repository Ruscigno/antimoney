package plaid

import (
	"context"
	"fmt"
	"log"
	"math"
	"time"

	plaidapi "github.com/plaid/plaid-go/v26/plaid"
)

// PlaidTxn is a normalized Plaid transaction. AmountNum > 0 means money leaving
// the bank account (a purchase/payment).
type PlaidTxn struct {
	TransactionID string
	Date          time.Time
	Description   string
	AmountNum     int64 // positive = debit from bank account
	AmountDenom   int64
	AccountID     string
	Pending       bool
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
	SyncTransactions(ctx context.Context, accessToken, cursor string) (added []PlaidTxn, nextCursor string, hasMore bool, err error)
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

func (c *realPlaidClient) SyncTransactions(ctx context.Context, accessToken, cursor string) ([]PlaidTxn, string, bool, error) {
	req := plaidapi.NewTransactionsSyncRequest(accessToken)
	if cursor != "" {
		req.SetCursor(cursor)
	}
	resp, _, err := c.api.PlaidApi.TransactionsSync(ctx).TransactionsSyncRequest(*req).Execute()
	if err != nil {
		return nil, "", false, plaidErr("SyncTransactions", err)
	}
	added := make([]PlaidTxn, 0, len(resp.Added))
	for _, t := range resp.Added {
		date, _ := time.Parse("2006-01-02", t.GetDate())
		// Plaid returns amounts as float64; round to whole cents (denom = 100).
		// math.Round is essential because float64 cannot represent most cent
		// fractions exactly (e.g. 0.10 is stored as 0.0999999...), so plain
		// int64(f*100) would truncate to the wrong cent; the integer cent values
		// that result after rounding ARE exactly representable.
		amountNum := int64(math.Round(t.GetAmount() * 100))
		added = append(added, PlaidTxn{
			TransactionID: t.GetTransactionId(),
			Date:          date,
			Description:   t.GetName(),
			AmountNum:     amountNum,
			AmountDenom:   100,
			AccountID:     t.GetAccountId(),
			Pending:       t.GetPending(),
		})
	}
	return added, resp.GetNextCursor(), resp.GetHasMore(), nil
}

func (c *realPlaidClient) RemoveItem(ctx context.Context, accessToken string) error {
	req := plaidapi.NewItemRemoveRequest(accessToken)
	_, _, err := c.api.PlaidApi.ItemRemove(ctx).ItemRemoveRequest(*req).Execute()
	return plaidErr("RemoveItem", err)
}

// plaidErr logs the underlying Plaid error server-side (so production failures are
// diagnosable) and returns a generic error for the caller. Plaid API errors carry
// error_code / error_message / request_id — not credentials — so they are safe to
// log; the Plaid SDK's error exposes the full response body via Body().
func plaidErr(op string, err error) error {
	if err == nil {
		return nil
	}
	if e, ok := err.(interface{ Body() []byte }); ok {
		log.Printf("plaid %s error: %v; body=%s", op, err, e.Body())
	} else {
		log.Printf("plaid %s error: %v", op, err)
	}
	return fmt.Errorf("plaid API error during %s", op)
}
