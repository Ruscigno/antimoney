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

	var storedBookGUID, institutionName string
	err := s.pool.QueryRow(ctx, `SELECT book_guid, institution_name FROM plaid_items WHERE guid = $1`, itemGUID).Scan(&storedBookGUID, &institutionName)
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

		// institution_name is denormalized into the link so the register can show
		// "Syncing <institution>…" without an extra fetch (spec §6.2).
		link, _ := json.Marshal(map[string]string{
			"item_guid":        itemGUID,
			"account_id":       m.PlaidAccountID,
			"institution_name": institutionName,
		})
		ct, err := tx.Exec(ctx,
			`UPDATE accounts
			 SET metadata   = jsonb_set(COALESCE(metadata, '{}'), '{plaid}', $1::jsonb),
			     updated_at = NOW(),
			     version    = version + 1
			 WHERE guid = $2 AND book_guid = $3`,
			link, m.AccountGUID, bookGUID,
		)
		if err != nil {
			// DB backstop for the 1:1 invariant under concurrency: a parallel
			// LinkAccounts that won the race trips idx_accounts_plaid_account.
			var pgErr *pgconn.PgError
			if errors.As(err, &pgErr) && pgErr.Code == "23505" && pgErr.ConstraintName == "idx_accounts_plaid_account" {
				return ErrDuplicateLink
			}
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
	// HasMore signals the page cap stopped mid-stream: another sync will
	// continue from the persisted cursor.
	HasMore bool `json:"has_more"`
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

	hasMore := false
	for i := 0; i < maxSyncPages; i++ {
		delta, nextCursor, more, err := s.client.SyncTransactions(ctx, accessToken, cursor)
		if err != nil {
			// ITEM_LOGIN_REQUIRED must reach the user as "reconnect your bank",
			// not as a generic failure.
			if errors.Is(err, ErrReauthRequired) {
				return nil, ErrReauthRequired
			}
			log.Printf("plaid SyncTransactions error: %v", err)
			return nil, fmt.Errorf("sync failed")
		}
		// Stage durably BEFORE the cursor moves past this page: Plaid never
		// re-sends data behind the cursor, so anything not persisted here would
		// be lost if the response never reached the user. A failure below means
		// the cursor stays put and a retry re-stages idempotently.
		if err := s.stageDelta(ctx, bookGUID, itemGUID, delta); err != nil {
			return nil, fmt.Errorf("stage sync delta: %w", err)
		}
		cursor = nextCursor
		hasMore = more
		if !more {
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
		     updated_at = $2,
		     version    = version + 1
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

	// Suggestions are rebuilt from durable staging on every sync — dismissed or
	// lost suggestions reappear until imported. The dedupe against already-
	// imported transactions happens in SQL (one query, not one per row); pgx
	// caches/prepares these statements automatically.
	type stagedTxn struct {
		TransactionID  string
		Date           time.Time
		Description    string
		AmountNum      int64
		AmountDenom    int64
		PlaidAccountID string
	}
	stRows, err := s.pool.Query(ctx,
		`SELECT st.transaction_id, st.post_date, st.description, st.amount_num, st.amount_denom, st.plaid_account_id
		 FROM plaid_staged_transactions st
		 WHERE st.book_guid = $1 AND st.item_guid = $2
		   AND ($3 OR NOT st.pending)
		   AND NOT EXISTS (
		     SELECT 1 FROM transactions t
		     WHERE t.book_guid = st.book_guid
		       AND t.metadata->'plaid'->>'transaction_id' = st.transaction_id)
		 ORDER BY st.post_date, st.transaction_id`,
		bookGUID, itemGUID, importPending,
	)
	if err != nil {
		return nil, fmt.Errorf("load staged transactions: %w", err)
	}
	var staged []stagedTxn
	for stRows.Next() {
		var st stagedTxn
		if err := stRows.Scan(&st.TransactionID, &st.Date, &st.Description, &st.AmountNum, &st.AmountDenom, &st.PlaidAccountID); err != nil {
			stRows.Close()
			return nil, fmt.Errorf("scan staged transaction: %w", err)
		}
		staged = append(staged, st)
	}
	stRows.Close()
	if err := stRows.Err(); err != nil {
		return nil, fmt.Errorf("iterate staged transactions: %w", err)
	}

	suggestions := make([]SyncSuggestion, 0, len(staged))
	catGUIDs := make(map[string]bool)
	for _, st := range staged {
		bank, ok := bankAccountByPlaidID[st.PlaidAccountID]
		if !ok {
			continue // unmapped bank account: the row stays staged
		}
		catGUID, _ := s.cat.Suggest(ctx, bookGUID, PlaidTxn{Description: st.Description})
		if catGUID != "" {
			catGUIDs[catGUID] = true
		}
		suggestions = append(suggestions, SyncSuggestion{
			TransactionID:         st.TransactionID,
			Date:                  st.Date.Format("2006-01-02"),
			Description:           st.Description,
			AmountNum:             st.AmountNum,
			AmountDenom:           st.AmountDenom,
			BankAccountGUID:       bank.GUID,
			BankAccountName:       bank.Name,
			SuggestedCategoryGUID: catGUID,
		})
	}

	// Batch-resolve all suggested category names in one query.
	if len(catGUIDs) > 0 {
		ids := make([]string, 0, len(catGUIDs))
		for id := range catGUIDs {
			ids = append(ids, id)
		}
		nameRows, err := s.pool.Query(ctx,
			`SELECT guid, name FROM accounts WHERE book_guid = $1 AND guid = ANY($2)`,
			bookGUID, ids,
		)
		if err != nil {
			return nil, fmt.Errorf("load category names: %w", err)
		}
		names := make(map[string]string, len(ids))
		for nameRows.Next() {
			var g, n string
			if err := nameRows.Scan(&g, &n); err != nil {
				nameRows.Close()
				return nil, fmt.Errorf("scan category name: %w", err)
			}
			names[g] = n
		}
		nameRows.Close()
		if err := nameRows.Err(); err != nil {
			return nil, fmt.Errorf("iterate category names: %w", err)
		}
		for i := range suggestions {
			suggestions[i].SuggestedCategoryName = names[suggestions[i].SuggestedCategoryGUID]
		}
	}

	return &SyncResult{Count: len(suggestions), Suggestions: suggestions, HasMore: hasMore}, nil
}

// stageDelta applies one /transactions/sync page to durable staging. Removed
// ids are dropped; added and modified transactions are upserted. A posted
// transaction whose pending predecessor was already imported is skipped
// entirely — importing it again would duplicate the money movement.
func (s *PlaidService) stageDelta(ctx context.Context, bookGUID, itemGUID string, delta SyncDelta) error {
	if len(delta.Removed) > 0 {
		if _, err := s.pool.Exec(ctx,
			`DELETE FROM plaid_staged_transactions WHERE book_guid = $1 AND transaction_id = ANY($2)`,
			bookGUID, delta.Removed,
		); err != nil {
			return fmt.Errorf("remove staged: %w", err)
		}
	}
	for _, txn := range append(append([]PlaidTxn{}, delta.Added...), delta.Modified...) {
		if txn.PendingTransactionID != "" {
			imported, err := s.transactionExists(ctx, bookGUID, txn.PendingTransactionID)
			if err != nil {
				return fmt.Errorf("pending correlation for %s: %w", txn.TransactionID, err)
			}
			if imported {
				if _, err := s.pool.Exec(ctx,
					`DELETE FROM plaid_staged_transactions WHERE book_guid = $1 AND transaction_id = ANY($2)`,
					bookGUID, []string{txn.PendingTransactionID, txn.TransactionID},
				); err != nil {
					return fmt.Errorf("drop replaced pending %s: %w", txn.PendingTransactionID, err)
				}
				continue
			}
		}
		if _, err := s.pool.Exec(ctx,
			`INSERT INTO plaid_staged_transactions
				(book_guid, item_guid, transaction_id, pending_transaction_id, plaid_account_id,
				 post_date, description, amount_num, amount_denom, pending)
			 VALUES ($1, $2, $3, NULLIF($4,''), $5, $6, $7, $8, $9, $10)
			 ON CONFLICT (book_guid, transaction_id) DO UPDATE SET
				pending_transaction_id = EXCLUDED.pending_transaction_id,
				post_date              = EXCLUDED.post_date,
				description            = EXCLUDED.description,
				amount_num             = EXCLUDED.amount_num,
				amount_denom           = EXCLUDED.amount_denom,
				pending                = EXCLUDED.pending`,
			bookGUID, itemGUID, txn.TransactionID, txn.PendingTransactionID, txn.AccountID,
			txn.Date, txn.Description, txn.AmountNum, txn.AmountDenom, txn.Pending,
		); err != nil {
			return fmt.Errorf("stage txn %s: %w", txn.TransactionID, err)
		}
	}
	return nil
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

// ImportRow identifies a staged transaction and the category the user picked.
// All financial data (date, description, amount, bank account) is read
// server-side from staging — a tampered client cannot inject arbitrary values
// into "Plaid" transactions.
type ImportRow struct {
	TransactionID       string `json:"transaction_id"`
	CategoryAccountGUID string `json:"category_account_guid"`
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
		if _, err := uuid.Parse(row.CategoryAccountGUID); err != nil {
			result.Failed = append(result.Failed, row.TransactionID)
			continue
		}

		// Financial data comes from staging, never from the client.
		var (
			postDate    time.Time
			description string
			amountNum   int64
			amountDenom int64
			plaidAcctID string
		)
		err := s.pool.QueryRow(ctx,
			`SELECT post_date, description, amount_num, amount_denom, plaid_account_id
			 FROM plaid_staged_transactions
			 WHERE book_guid = $1 AND transaction_id = $2`,
			bookGUID, row.TransactionID,
		).Scan(&postDate, &description, &amountNum, &amountDenom, &plaidAcctID)
		if errors.Is(err, pgx.ErrNoRows) {
			// Never staged, or already imported and cleaned up. A duplicate
			// click on an imported row is benign; anything else is a failure.
			exists, exErr := s.transactionExists(ctx, bookGUID, row.TransactionID)
			if exErr != nil {
				return result, fmt.Errorf("dedupe check for %s: %w", row.TransactionID, exErr)
			}
			if !exists {
				result.Failed = append(result.Failed, row.TransactionID)
			}
			continue
		}
		if err != nil {
			return result, fmt.Errorf("load staged %s: %w", row.TransactionID, err)
		}

		// Resolve the bank account from the server-side 1:1 mapping.
		var bankGUID string
		if err := s.pool.QueryRow(ctx,
			`SELECT guid FROM accounts WHERE book_guid = $1 AND metadata->'plaid'->>'account_id' = $2`,
			bookGUID, plaidAcctID,
		).Scan(&bankGUID); err != nil {
			// Unlinked since staging (or DB error): don't guess — fail the row.
			log.Printf("plaid import row %s: resolve bank account: %v", row.TransactionID, err)
			result.Failed = append(result.Failed, row.TransactionID)
			continue
		}

		exists, err := s.transactionExists(ctx, bookGUID, row.TransactionID)
		if err != nil {
			return result, fmt.Errorf("dedupe check for %s: %w", row.TransactionID, err)
		}
		if exists {
			continue
		}

		meta, _ := json.Marshal(map[string]interface{}{
			"plaid": map[string]string{"transaction_id": row.TransactionID},
		})
		_, err = s.txSvc.CreateTransaction(ctx, services.CreateTransactionRequest{
			PostDate:    postDate,
			Description: description,
			Metadata:    meta,
			Splits: []services.CreateSplitRequest{
				{
					AccountGUID:    bankGUID,
					ValueNum:       -amountNum,
					ValueDenom:     amountDenom,
					QuantityNum:    -amountNum,
					QuantityDenom:  amountDenom,
					ReconcileState: "c",
				},
				{
					AccountGUID:    row.CategoryAccountGUID,
					ValueNum:       amountNum,
					ValueDenom:     amountDenom,
					QuantityNum:    amountNum,
					QuantityDenom:  amountDenom,
					ReconcileState: "c",
				},
			},
		})
		if err != nil {
			if isPlaidDupViolation(err) {
				// A concurrent import inserted this transaction between our
				// dedupe check and the insert; the partial unique index (the
				// real idempotency guarantee) caught it. Already imported — the
				// staged copy can go.
				s.deleteStaged(ctx, bookGUID, row.TransactionID)
				continue
			}
			log.Printf("plaid import row %s: %v", row.TransactionID, err)
			result.Failed = append(result.Failed, row.TransactionID)
			continue
		}
		// Imported: drop the staged copy (failure here is benign — the SQL
		// NOT EXISTS dedupe hides imported rows from future suggestions).
		s.deleteStaged(ctx, bookGUID, row.TransactionID)
		result.Imported++
	}
	return result, nil
}

func (s *PlaidService) deleteStaged(ctx context.Context, bookGUID, transactionID string) {
	if _, err := s.pool.Exec(ctx,
		`DELETE FROM plaid_staged_transactions WHERE book_guid = $1 AND transaction_id = $2`,
		bookGUID, transactionID,
	); err != nil {
		log.Printf("plaid import: cleanup staged %s: %v", transactionID, err)
	}
}

// isPlaidDupViolation reports whether err is the unique-index violation raised
// when a concurrent import already inserted this Plaid transaction.
func isPlaidDupViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505" && pgErr.ConstraintName == "idx_transactions_plaid_txn"
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
		// Abort: deleting the local row would destroy the only copy of the
		// access token while the Item stays alive (and billable) at Plaid.
		// Keeping the row lets the user simply retry the disconnect.
		log.Printf("plaid RemoveItem failed; aborting disconnect: %v", rmErr)
		return fmt.Errorf("plaid item removal failed")
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	_, err = tx.Exec(ctx,
		`UPDATE accounts
		 SET metadata = metadata - 'plaid', updated_at = NOW(), version = version + 1
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
