package plaid

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/user/antimoney/internal/auth"
	"github.com/user/antimoney/internal/services"
)

var (
	ErrItemNotFound         = errors.New("plaid item not found or access denied")
	ErrDuplicateLink        = errors.New("this bank account is already linked to another Antimoney account")
	ErrAccountNotFound      = errors.New("account not found or access denied")
	ErrAccountAlreadyLinked = errors.New("this Antimoney account is already linked to a bank account")
	ErrInvalidEncKey        = errors.New("PLAID_TOKEN_ENC_KEY must be a base64-encoded 32-byte key")
)

// maxSyncPages caps the number of /transactions/sync pages per API call to
// stay within the Cloud Run 30s timeout.
const maxSyncPages = 3

// PlaidService owns all Plaid business logic.
type PlaidService struct {
	pool   *pgxpool.Pool
	client PlaidClient
	encKey []byte
	txSvc  *services.TransactionService
	cat    Categorizer
}

// NewPlaidService creates a PlaidService. encKeyBase64 is a base64-encoded 32-byte key.
func NewPlaidService(pool *pgxpool.Pool, client PlaidClient, encKeyBase64 string, txSvc *services.TransactionService) (*PlaidService, error) {
	key, err := base64.StdEncoding.DecodeString(encKeyBase64)
	if err != nil || len(key) != 32 {
		return nil, ErrInvalidEncKey
	}
	return &PlaidService{
		pool:   pool,
		client: client,
		encKey: key,
		txSvc:  txSvc,
		cat:    NewHistoryCategorizer(pool),
	}, nil
}

// CreateLinkToken creates a Plaid Link token for the requesting user.
func (s *PlaidService) CreateLinkToken(ctx context.Context) (string, error) {
	userID := auth.UserIDFromCtx(ctx)
	if userID == "" {
		return "", errors.New("missing user id in context")
	}
	return s.client.CreateLinkToken(ctx, userID)
}

// ExchangeResult is the response from Exchange.
type ExchangeResult struct {
	ItemGUID        string
	InstitutionName string
	Accounts        []PlaidAccount
}

// Exchange exchanges a public_token for an access_token, encrypts and persists
// it in plaid_items, and returns the list of bank accounts.
func (s *PlaidService) Exchange(ctx context.Context, publicToken string) (*ExchangeResult, error) {
	bookGUID := auth.BookGUIDFromCtx(ctx)

	accessToken, itemID, institutionName, err := s.client.ExchangePublicToken(ctx, publicToken)
	if err != nil {
		log.Printf("plaid exchange error: %v", err)
		return nil, fmt.Errorf("exchange failed")
	}

	ciphertext, nonce, err := encrypt(s.encKey, accessToken)
	if err != nil {
		return nil, fmt.Errorf("encrypt access token: %w", err)
	}

	itemGUID := uuid.New().String()
	now := time.Now().UTC()
	// Upsert on the (book_guid, item_id) unique key: re-linking the same Plaid
	// item (network retry, re-auth) refreshes the stored token in place instead
	// of inserting a duplicate row. RETURNING yields the surviving row's guid.
	err = s.pool.QueryRow(ctx,
		`INSERT INTO plaid_items
			(guid, book_guid, item_id, institution_name, access_token_ciphertext,
			 access_token_nonce, version, created_at, updated_at)
		 VALUES ($1, $2, $3, $4, $5, $6, 1, $7, $7)
		 ON CONFLICT (book_guid, item_id) DO UPDATE SET
			institution_name        = EXCLUDED.institution_name,
			access_token_ciphertext = EXCLUDED.access_token_ciphertext,
			access_token_nonce      = EXCLUDED.access_token_nonce,
			updated_at              = EXCLUDED.updated_at,
			version                 = plaid_items.version + 1
		 RETURNING guid`,
		itemGUID, bookGUID, itemID, institutionName, ciphertext, nonce, now,
	).Scan(&itemGUID)
	if err != nil {
		return nil, fmt.Errorf("upsert plaid_items: %w", err)
	}

	accounts, err := s.client.GetAccounts(ctx, accessToken)
	if err != nil {
		log.Printf("plaid GetAccounts error: %v", err)
		return nil, fmt.Errorf("fetch accounts failed")
	}

	return &ExchangeResult{
		ItemGUID:        itemGUID,
		InstitutionName: institutionName,
		Accounts:        accounts,
	}, nil
}

type AccountMapping struct {
	PlaidAccountID string `json:"account_id"`
	AccountGUID    string `json:"account_guid"`
}

// LinkAccounts writes 1:1 mappings onto accounts.metadata and sets import_pending on the item.
func (s *PlaidService) LinkAccounts(ctx context.Context, itemGUID string, mappings []AccountMapping, importPending bool) error {
	bookGUID := auth.BookGUIDFromCtx(ctx)

	var storedBookGUID string
	err := s.pool.QueryRow(ctx, `SELECT book_guid FROM plaid_items WHERE guid = $1`, itemGUID).Scan(&storedBookGUID)
	if err != nil || storedBookGUID != bookGUID {
		return ErrItemNotFound
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	for _, m := range mappings {
		var count int
		if err := tx.QueryRow(ctx,
			`SELECT COUNT(*) FROM accounts
			 WHERE book_guid = $1
			   AND metadata->'plaid'->>'account_id' = $2
			   AND guid != $3`,
			bookGUID, m.PlaidAccountID, m.AccountGUID,
		).Scan(&count); err != nil {
			return err
		}
		if count > 0 {
			return ErrDuplicateLink
		}

		// The other half of the 1:1 invariant: the target account must exist in
		// this book and must not already carry a different plaid link — the
		// jsonb_set below would otherwise silently overwrite it (last-write-wins)
		// and orphan the previous link. Re-linking the identical pair is allowed
		// (idempotent re-map).
		var existingItem, existingAcct *string
		err := tx.QueryRow(ctx,
			`SELECT metadata->'plaid'->>'item_guid', metadata->'plaid'->>'account_id'
			 FROM accounts WHERE guid = $1 AND book_guid = $2`,
			m.AccountGUID, bookGUID,
		).Scan(&existingItem, &existingAcct)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrAccountNotFound
		}
		if err != nil {
			return fmt.Errorf("check account %s: %w", m.AccountGUID, err)
		}
		if existingItem != nil &&
			(*existingItem != itemGUID || existingAcct == nil || *existingAcct != m.PlaidAccountID) {
			return ErrAccountAlreadyLinked
		}

		link, _ := json.Marshal(map[string]string{
			"item_guid":  itemGUID,
			"account_id": m.PlaidAccountID,
		})
		ct, err := tx.Exec(ctx,
			`UPDATE accounts
			 SET metadata   = jsonb_set(COALESCE(metadata, '{}'), '{plaid}', $1::jsonb),
			     updated_at = NOW()
			 WHERE guid = $2 AND book_guid = $3`,
			link, m.AccountGUID, bookGUID,
		)
		if err != nil {
			return fmt.Errorf("link account %s: %w", m.AccountGUID, err)
		}
		// Never report "linked" when nothing was written (the SELECT above proved
		// existence inside this tx, so this is a belt-and-braces consistency check).
		if ct.RowsAffected() == 0 {
			return ErrAccountNotFound
		}
	}

	_, err = tx.Exec(ctx,
		`UPDATE plaid_items SET import_pending = $1, updated_at = NOW() WHERE guid = $2 AND book_guid = $3`,
		importPending, itemGUID, bookGUID,
	)
	if err != nil {
		return err
	}

	return tx.Commit(ctx)
}

type SyncSuggestion struct {
	TransactionID         string `json:"transaction_id"`
	Date                  string `json:"date"`
	Description           string `json:"description"`
	AmountNum             int64  `json:"amount_num"`
	AmountDenom           int64  `json:"amount_denom"`
	BankAccountGUID       string `json:"bank_account_guid"`
	BankAccountName       string `json:"bank_account_name"`
	SuggestedCategoryGUID string `json:"suggested_category_guid,omitempty"`
	SuggestedCategoryName string `json:"suggested_category_name,omitempty"`
}

type SyncResult struct {
	Count       int              `json:"count"`
	Suggestions []SyncSuggestion `json:"suggestions"`
}

// Sync fetches new transactions for an item, deduplicates, categorizes, and
// returns suggestions. The cursor and last_synced_at are persisted.
func (s *PlaidService) Sync(ctx context.Context, itemGUID string) (*SyncResult, error) {
	bookGUID := auth.BookGUIDFromCtx(ctx)

	var cursor string
	var ciphertext, nonce []byte
	var importPending bool
	err := s.pool.QueryRow(ctx,
		`SELECT COALESCE(sync_cursor,''), access_token_ciphertext, access_token_nonce, import_pending
		 FROM plaid_items WHERE guid = $1 AND book_guid = $2`,
		itemGUID, bookGUID,
	).Scan(&cursor, &ciphertext, &nonce, &importPending)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrItemNotFound
	}
	if err != nil {
		return nil, err
	}

	accessToken, err := decrypt(s.encKey, ciphertext, nonce)
	if err != nil {
		return nil, fmt.Errorf("decrypt access token: %w", err)
	}

	var allAdded []PlaidTxn
	for i := 0; i < maxSyncPages; i++ {
		added, nextCursor, hasMore, err := s.client.SyncTransactions(ctx, accessToken, cursor)
		if err != nil {
			// ITEM_LOGIN_REQUIRED must reach the user as "reconnect your bank",
			// not as a generic failure.
			if errors.Is(err, ErrReauthRequired) {
				return nil, ErrReauthRequired
			}
			log.Printf("plaid SyncTransactions error: %v", err)
			return nil, fmt.Errorf("sync failed")
		}
		allAdded = append(allAdded, added...)
		cursor = nextCursor
		if !hasMore {
			break
		}
	}

	now := time.Now().UTC()
	if _, err := s.pool.Exec(ctx,
		`UPDATE plaid_items
		 SET sync_cursor = $1, last_synced_at = $2, updated_at = $2, version = version + 1
		 WHERE guid = $3 AND book_guid = $4`,
		cursor, now, itemGUID, bookGUID,
	); err != nil {
		log.Printf("plaid sync: persist cursor: %v", err)
	}
	if _, err := s.pool.Exec(ctx,
		`UPDATE accounts
		 SET metadata = jsonb_set(COALESCE(metadata,'{}'), '{plaid,last_synced_at}', to_jsonb($1::text), true),
		     updated_at = $2
		 WHERE book_guid = $3 AND metadata->'plaid'->>'item_guid' = $4`,
		now.Format(time.RFC3339), now, bookGUID, itemGUID,
	); err != nil {
		log.Printf("plaid sync: propagate last_synced_at: %v", err)
	}

	bankAccountByPlaidID := make(map[string]struct{ GUID, Name string })
	rows, err := s.pool.Query(ctx,
		`SELECT guid, name, metadata->'plaid'->>'account_id'
		 FROM accounts
		 WHERE book_guid = $1 AND metadata->'plaid'->>'item_guid' = $2`,
		bookGUID, itemGUID,
	)
	if err != nil {
		return nil, fmt.Errorf("load linked accounts: %w", err)
	}
	for rows.Next() {
		var g, n, pid string
		if err := rows.Scan(&g, &n, &pid); err != nil {
			rows.Close()
			return nil, fmt.Errorf("scan linked account: %w", err)
		}
		bankAccountByPlaidID[pid] = struct{ GUID, Name string }{g, n}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate linked accounts: %w", err)
	}

	suggestions := make([]SyncSuggestion, 0, len(allAdded))
	for _, txn := range allAdded {
		if txn.Pending && !importPending {
			continue
		}
		bank, ok := bankAccountByPlaidID[txn.AccountID]
		if !ok {
			continue
		}
		// A DB error here must NOT be swallowed: treating it as "not a duplicate"
		// would re-import already-imported transactions.
		exists, err := s.transactionExists(ctx, bookGUID, txn.TransactionID)
		if err != nil {
			return nil, fmt.Errorf("dedupe check for %s: %w", txn.TransactionID, err)
		}
		if exists {
			continue
		}

		catGUID, _ := s.cat.Suggest(ctx, bookGUID, txn)
		catName := ""
		if catGUID != "" {
			if err := s.pool.QueryRow(ctx, `SELECT name FROM accounts WHERE guid = $1 AND book_guid = $2`, catGUID, bookGUID).Scan(&catName); err != nil && !errors.Is(err, pgx.ErrNoRows) {
				log.Printf("plaid sync: load category name for %s: %v", catGUID, err)
			}
		}

		suggestions = append(suggestions, SyncSuggestion{
			TransactionID:         txn.TransactionID,
			Date:                  txn.Date.Format("2006-01-02"),
			Description:           txn.Description,
			AmountNum:             txn.AmountNum,
			AmountDenom:           txn.AmountDenom,
			BankAccountGUID:       bank.GUID,
			BankAccountName:       bank.Name,
			SuggestedCategoryGUID: catGUID,
			SuggestedCategoryName: catName,
		})
	}

	return &SyncResult{Count: len(suggestions), Suggestions: suggestions}, nil
}

// transactionExists reports whether a transaction with the given Plaid id has
// already been imported into this book (the dedupe key).
func (s *PlaidService) transactionExists(ctx context.Context, bookGUID, plaidTxnID string) (bool, error) {
	var cnt int
	if err := s.pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM transactions
		 WHERE book_guid = $1 AND metadata->'plaid'->>'transaction_id' = $2`,
		bookGUID, plaidTxnID,
	).Scan(&cnt); err != nil {
		return false, err
	}
	return cnt > 0, nil
}

type ImportRow struct {
	TransactionID       string `json:"transaction_id"`
	BankAccountGUID     string `json:"bank_account_guid"`
	CategoryAccountGUID string `json:"category_account_guid"`
	Description         string `json:"description"`
	Date                string `json:"date"`
	AmountNum           int64  `json:"amount_num"`
	AmountDenom         int64  `json:"amount_denom"`
}

// ImportResult reports how many rows were imported and which transaction_ids
// failed, so the caller can detect a partially-dropped import.
type ImportResult struct {
	Imported int      `json:"imported"`
	Failed   []string `json:"failed,omitempty"`
}

// Import creates one cleared transaction per ImportRow.
// Sign convention: Plaid AmountNum > 0 = money leaving bank account.
// bank split = -AmountNum, category split = +AmountNum (maintains zero-sum).
//
// Known limitation: splits are imported as cleared ('c'), but editing the
// transaction later via TransactionService.UpdateTransaction resets every split
// to 'n' (documented in CLAUDE.md), silently un-clearing a Plaid import.
func (s *PlaidService) Import(ctx context.Context, rows []ImportRow) (*ImportResult, error) {
	bookGUID := auth.BookGUIDFromCtx(ctx)
	result := &ImportResult{}
	for _, row := range rows {
		exists, err := s.transactionExists(ctx, bookGUID, row.TransactionID)
		if err != nil {
			return result, fmt.Errorf("dedupe check for %s: %w", row.TransactionID, err)
		}
		if exists {
			continue
		}

		postDate, err := time.Parse("2006-01-02", row.Date)
		if err != nil {
			log.Printf("plaid import row %s: invalid date %q: %v", row.TransactionID, row.Date, err)
			result.Failed = append(result.Failed, row.TransactionID)
			continue
		}

		meta, _ := json.Marshal(map[string]interface{}{
			"plaid": map[string]string{"transaction_id": row.TransactionID},
		})
		_, err = s.txSvc.CreateTransaction(ctx, services.CreateTransactionRequest{
			PostDate:    postDate,
			Description: row.Description,
			Metadata:    meta,
			Splits: []services.CreateSplitRequest{
				{
					AccountGUID:    row.BankAccountGUID,
					ValueNum:       -row.AmountNum,
					ValueDenom:     row.AmountDenom,
					QuantityNum:    -row.AmountNum,
					QuantityDenom:  row.AmountDenom,
					ReconcileState: "c",
				},
				{
					AccountGUID:    row.CategoryAccountGUID,
					ValueNum:       row.AmountNum,
					ValueDenom:     row.AmountDenom,
					QuantityNum:    row.AmountNum,
					QuantityDenom:  row.AmountDenom,
					ReconcileState: "c",
				},
			},
		})
		if err != nil {
			var pgErr *pgconn.PgError
			if errors.As(err, &pgErr) && pgErr.Code == "23505" && pgErr.ConstraintName == "idx_transactions_plaid_txn" {
				// A concurrent import inserted this transaction between our
				// dedupe check and the insert; the partial unique index (the
				// real idempotency guarantee) caught it. Already imported — skip.
				continue
			}
			log.Printf("plaid import row %s: %v", row.TransactionID, err)
			result.Failed = append(result.Failed, row.TransactionID)
			continue
		}
		result.Imported++
	}
	return result, nil
}

// Disconnect calls Plaid /item/remove, deletes the plaid_items row, and clears
// the plaid link from all affected accounts.
func (s *PlaidService) Disconnect(ctx context.Context, itemGUID string) error {
	bookGUID := auth.BookGUIDFromCtx(ctx)

	var ciphertext, nonce []byte
	err := s.pool.QueryRow(ctx,
		`SELECT access_token_ciphertext, access_token_nonce FROM plaid_items WHERE guid = $1 AND book_guid = $2`,
		itemGUID, bookGUID,
	).Scan(&ciphertext, &nonce)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrItemNotFound
	}
	if err != nil {
		return err
	}

	accessToken, err := decrypt(s.encKey, ciphertext, nonce)
	if err != nil {
		return fmt.Errorf("decrypt access token: %w", err)
	}

	if rmErr := s.client.RemoveItem(ctx, accessToken); rmErr != nil {
		log.Printf("plaid RemoveItem: %v", rmErr)
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	_, err = tx.Exec(ctx,
		`UPDATE accounts
		 SET metadata = metadata - 'plaid', updated_at = NOW()
		 WHERE book_guid = $1 AND metadata->'plaid'->>'item_guid' = $2`,
		bookGUID, itemGUID,
	)
	if err != nil {
		return err
	}

	_, err = tx.Exec(ctx, `DELETE FROM plaid_items WHERE guid = $1 AND book_guid = $2`, itemGUID, bookGUID)
	if err != nil {
		return err
	}

	return tx.Commit(ctx)
}

// ListItem is a non-sensitive summary of a plaid_items row.
type ListItem struct {
	GUID            string  `json:"guid"`
	InstitutionName string  `json:"institution_name"`
	LastSyncedAt    *string `json:"last_synced_at"`
	ImportPending   bool    `json:"import_pending"`
}

// ListItems returns connected items for the current book (no access tokens).
func (s *PlaidService) ListItems(ctx context.Context) ([]ListItem, error) {
	bookGUID := auth.BookGUIDFromCtx(ctx)
	rows, err := s.pool.Query(ctx,
		`SELECT guid, institution_name,
		        to_char(last_synced_at AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS"Z"'),
		        import_pending
		 FROM plaid_items WHERE book_guid = $1 ORDER BY created_at`,
		bookGUID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []ListItem
	for rows.Next() {
		var item ListItem
		var lastSynced *string
		if err := rows.Scan(&item.GUID, &item.InstitutionName, &lastSynced, &item.ImportPending); err != nil {
			return nil, err
		}
		item.LastSyncedAt = lastSynced
		items = append(items, item)
	}
	if items == nil {
		items = []ListItem{}
	}
	return items, nil
}
