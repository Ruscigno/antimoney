package plaid

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"time"

	"strings"

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
	ErrInvalidEncKey        = errors.New("PLAID_TOKEN_ENC_KEY must be one or more comma-separated base64-encoded 32-byte keys")
	// ErrLegacyTokenNeedsFlag: the token decrypts in the legacy nil-AAD format
	// but the sunset flag is off — a CLEAN recovery path exists (enable
	// PLAID_LEGACY_TOKEN_FALLBACK, let the re-seal migrate, disable), so
	// destructive fallbacks must not engage.
	ErrLegacyTokenNeedsFlag = errors.New("token is sealed in the legacy format; enable PLAID_LEGACY_TOKEN_FALLBACK to migrate it cleanly")
	// ErrConcurrentModification: the row changed (re-link bumped its version)
	// between read and write — the operation is safe to simply retry.
	ErrConcurrentModification = errors.New("item changed concurrently; retry")
)

// maxSyncPages caps the number of /transactions/sync pages per API call to
// stay within the Cloud Run 30s timeout.
const maxSyncPages = 3

// PlaidService owns all Plaid business logic.
type PlaidService struct {
	pool   *pgxpool.Pool
	client PlaidClient
	// encKeys holds the token-encryption keys: encKeys[0] encrypts; ALL keys
	// are tried on decrypt. Rotation: prepend a new key — old tokens keep
	// decrypting and are opportunistically re-sealed with the primary key on
	// the next successful use (loadAndDecryptToken). A retired key can only be
	// dropped from the list once no stored token still depends on it.
	encKeys [][]byte
	// allowLegacyTokens enables the nil-AAD decrypt fallback for ciphertexts
	// sealed by pre-AAD builds. OFF by default: the fallback reopens the
	// ciphertext-swap window for legacy blobs, so it must be a deliberate,
	// temporary choice (enable, let re-seal migrate the tokens, disable).
	allowLegacyTokens bool
	txSvc             *services.TransactionService
	cat               Categorizer
}

// NewPlaidService creates a PlaidService. encKeysBase64 is one or more
// comma-separated base64-encoded 32-byte keys (first = active encrypt key).
// allowLegacyTokens temporarily accepts pre-AAD ciphertexts (sunset flag).
func NewPlaidService(pool *pgxpool.Pool, client PlaidClient, encKeysBase64 string, allowLegacyTokens bool, txSvc *services.TransactionService) (*PlaidService, error) {
	var keys [][]byte
	for _, part := range strings.Split(encKeysBase64, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		key, err := base64.StdEncoding.DecodeString(part)
		if err != nil || len(key) != 32 {
			return nil, ErrInvalidEncKey
		}
		keys = append(keys, key)
	}
	if len(keys) == 0 {
		return nil, ErrInvalidEncKey
	}
	return &PlaidService{
		pool:              pool,
		client:            client,
		encKeys:           keys,
		allowLegacyTokens: allowLegacyTokens,
		txSvc:             txSvc,
		cat:               NewHistoryCategorizer(pool),
	}, nil
}

// tokenAAD binds a token ciphertext to its owning book and Plaid item: a row
// copied to another book/item fails GCM authentication on decrypt.
func tokenAAD(bookGUID, itemID string) []byte {
	return []byte(bookGUID + "|" + itemID)
}

func (s *PlaidService) encryptToken(bookGUID, itemID, token string) (ciphertext, nonce []byte, err error) {
	return encrypt(s.encKeys[0], token, tokenAAD(bookGUID, itemID))
}

// decryptToken tries every key with the canonical AAD and — only when the
// sunset flag allowLegacyTokens is on — every key with a nil AAD (ciphertexts
// sealed before AAD was introduced). stale=true means the caller should
// re-seal with the primary key + AAD.
func (s *PlaidService) decryptToken(bookGUID, itemID string, ciphertext, nonce []byte) (token string, stale bool, err error) {
	var lastErr error
	for i, key := range s.encKeys {
		if token, err := decrypt(key, ciphertext, nonce, tokenAAD(bookGUID, itemID)); err == nil {
			return token, i > 0, nil
		} else {
			lastErr = err
		}
	}
	for _, key := range s.encKeys {
		if token, err := decrypt(key, ciphertext, nonce, nil); err == nil {
			if s.allowLegacyTokens {
				log.Printf("plaid: token for item %s decrypted via legacy nil-AAD format; re-sealing (disable PLAID_LEGACY_TOKEN_FALLBACK once no legacy tokens remain)", itemID)
				return token, true, nil
			}
			// Detection-only probe: the token IS recoverable via the sunset
			// flag. Never hand it out with the flag off, but tell the caller
			// the clean path exists so nothing destructive engages.
			return "", false, ErrLegacyTokenNeedsFlag
		} else {
			lastErr = err
		}
	}
	return "", false, lastErr
}

// loadAndDecryptToken loads an item's access token, decrypting with rotation
// and (optionally) legacy fallbacks, and opportunistically re-seals stale
// ciphertexts (retired key or legacy nil-AAD format) with the primary key —
// this is what makes key rotation eventually complete without forcing users
// to re-link. expectedVersion guards the re-seal with OCC: if a concurrent
// Exchange (re-link) replaced the token meanwhile, the conditional UPDATE
// matches zero rows and the fresh token is never overwritten.
func (s *PlaidService) loadAndDecryptToken(ctx context.Context, bookGUID, itemGUID, itemID string, ciphertext, nonce []byte, expectedVersion int) (string, error) {
	token, stale, err := s.decryptToken(bookGUID, itemID, ciphertext, nonce)
	if err != nil {
		return "", err
	}
	if stale {
		if ct, n, sealErr := s.encryptToken(bookGUID, itemID, token); sealErr == nil {
			if _, upErr := s.pool.Exec(ctx,
				`UPDATE plaid_items
				 SET access_token_ciphertext = $1, access_token_nonce = $2, updated_at = NOW(), version = version + 1
				 WHERE guid = $3 AND book_guid = $4 AND version = $5`,
				ct, n, itemGUID, bookGUID, expectedVersion,
			); upErr != nil {
				log.Printf("plaid: re-seal token for item %s: %v", itemGUID, upErr) // best-effort
			}
		}
	}
	return token, nil
}

// CreateLinkToken creates a Plaid Link token for the requesting user, with the
// Link UI in the given (already-whitelisted) language.
func (s *PlaidService) CreateLinkToken(ctx context.Context, language string) (string, error) {
	userID := auth.UserIDFromCtx(ctx)
	if userID == "" {
		return "", errors.New("missing user id in context")
	}
	return s.client.CreateLinkToken(ctx, userID, language)
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

	ciphertext, nonce, err := s.encryptToken(bookGUID, itemID, accessToken)
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

	var institutionName string
	err := s.pool.QueryRow(ctx,
		`SELECT institution_name FROM plaid_items WHERE guid = $1 AND book_guid = $2`,
		itemGUID, bookGUID,
	).Scan(&institutionName)
	if err != nil {
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
	// InProgress signals another sync holds this item's lock right now: the
	// suggestions returned are valid but possibly incomplete.
	InProgress bool `json:"in_progress,omitempty"`
}

// Sync fetches new transactions for an item, deduplicates, categorizes, and
// returns suggestions. The cursor and last_synced_at are persisted.
func (s *PlaidService) Sync(ctx context.Context, itemGUID string) (*SyncResult, error) {
	bookGUID := auth.BookGUIDFromCtx(ctx)

	var cursor string
	var ciphertext, nonce []byte
	var importPending bool
	var itemID string
	var itemVersion int
	err := s.pool.QueryRow(ctx,
		`SELECT item_id, COALESCE(sync_cursor,''), access_token_ciphertext, access_token_nonce, import_pending, version
		 FROM plaid_items WHERE guid = $1 AND book_guid = $2`,
		itemGUID, bookGUID,
	).Scan(&itemID, &cursor, &ciphertext, &nonce, &importPending, &itemVersion)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrItemNotFound
	}
	if err != nil {
		return nil, err
	}

	accessToken, err := s.loadAndDecryptToken(ctx, bookGUID, itemGUID, itemID, ciphertext, nonce, itemVersion)
	if err != nil {
		return nil, fmt.Errorf("decrypt access token: %w", err)
	}

	// Per-item advisory lock: concurrent syncs (a second tab, or auto-sync
	// racing the manual button) would interleave /transactions/sync pagination,
	// could persist a regressed cursor, and can trip Plaid's
	// TRANSACTIONS_SYNC_MUTATION_DURING_PAGINATION. The loser skips fetching and
	// serves the durable staged suggestions — correct and race-free.
	hasMore := false
	lockConn, err := s.pool.Acquire(ctx)
	if err != nil {
		return nil, fmt.Errorf("acquire conn for sync lock: %w", err)
	}
	var gotLock bool
	if err := lockConn.QueryRow(ctx,
		`SELECT pg_try_advisory_lock(hashtextextended($1, 0))`, itemGUID,
	).Scan(&gotLock); err != nil {
		lockConn.Release()
		return nil, fmt.Errorf("acquire sync lock: %w", err)
	}
	inProgress := false
	if gotLock {
		// The unlock+release MUST be deferred: a panic inside fetchAndStage is
		// recovered by chi's middleware, and an inline unlock would never run —
		// leaking the session lock (and the pooled conn) for the life of the
		// instance, silently sending every future sync of this item down the
		// lock-skip path.
		hm, fetchErr := func() (bool, error) {
			defer func() {
				if _, unlockErr := lockConn.Exec(context.WithoutCancel(ctx),
					`SELECT pg_advisory_unlock(hashtextextended($1, 0))`, itemGUID); unlockErr != nil {
					log.Printf("plaid sync: release lock for %s: %v", itemGUID, unlockErr)
				}
				lockConn.Release()
			}()
			return s.fetchAndStage(ctx, bookGUID, itemGUID, accessToken, cursor)
		}()
		if fetchErr != nil {
			return nil, fetchErr
		}
		hasMore = hm
	} else {
		lockConn.Release()
		// Tell the caller a concurrent fetch is running: the staged suggestions
		// below are valid but possibly incomplete.
		inProgress = true
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

	// Suggestions are rebuilt from durable staging on every sync — a lost
	// response or dismissed modal costs nothing: unimported rows reappear,
	// while explicitly DISMISSED rows never do. The dedupe against already-
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
		   AND NOT st.dismissed
		   AND ($3 OR NOT st.pending)
		   AND NOT EXISTS (
		     SELECT 1 FROM transactions t
		     WHERE t.book_guid = st.book_guid
		       AND t.metadata->'plaid'->>'transaction_id' = st.transaction_id)
		   -- pending→posted race defense: hide a posted txn whose pending
		   -- predecessor was already imported WITH THE SAME VALUE on the linked
		   -- bank account (sign-aware: the import writes the bank split as
		   -- -amount, so a reversal must NOT collide with the original charge;
		   -- a diverged value stays visible so the user can act on it).
		   AND (st.pending_transaction_id IS NULL OR NOT EXISTS (
		     SELECT 1 FROM transactions t2
		     JOIN splits s2 ON s2.tx_guid = t2.guid
		     JOIN accounts a2 ON a2.guid = s2.account_guid AND a2.book_guid = t2.book_guid
		     WHERE t2.book_guid = st.book_guid
		       AND t2.metadata->'plaid'->>'transaction_id' = st.pending_transaction_id
		       AND a2.metadata->'plaid'->>'account_id' = st.plaid_account_id
		       AND s2.value_num = -st.amount_num AND s2.value_denom = st.amount_denom))
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

	type mappedRow struct {
		st   stagedTxn
		bank struct{ GUID, Name string }
	}
	mapped := make([]mappedRow, 0, len(staged))
	for _, st := range staged {
		bank, ok := bankAccountByPlaidID[st.PlaidAccountID]
		if !ok {
			continue // unmapped bank account: the row stays staged
		}
		mapped = append(mapped, mappedRow{st: st, bank: bank})
	}

	// Categorize in a constant number of queries when supported (large syncs
	// must stay inside the 30s request timeout); other Categorizer
	// implementations fall back to per-row suggestion.
	cats := make([]string, len(mapped))
	if bc, ok := s.cat.(batchCategorizer); ok {
		descs := make([]string, len(mapped))
		for i, m := range mapped {
			descs[i] = m.st.Description
		}
		cats = bc.SuggestBatch(ctx, bookGUID, descs)
	} else {
		for i, m := range mapped {
			cats[i], _ = s.cat.Suggest(ctx, bookGUID, PlaidTxn{Description: m.st.Description})
		}
	}

	suggestions := make([]SyncSuggestion, 0, len(mapped))
	catGUIDs := make(map[string]bool)
	for i, m := range mapped {
		if cats[i] != "" {
			catGUIDs[cats[i]] = true
		}
		suggestions = append(suggestions, SyncSuggestion{
			TransactionID:         m.st.TransactionID,
			Date:                  m.st.Date.Format("2006-01-02"),
			Description:           m.st.Description,
			AmountNum:             m.st.AmountNum,
			AmountDenom:           m.st.AmountDenom,
			BankAccountGUID:       m.bank.GUID,
			BankAccountName:       m.bank.Name,
			SuggestedCategoryGUID: cats[i],
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

	return &SyncResult{Count: len(suggestions), Suggestions: suggestions, HasMore: hasMore, InProgress: inProgress}, nil
}

// fetchAndStage pulls up to maxSyncPages of /transactions/sync, staging each
// page durably BEFORE the cursor moves past it: Plaid never re-sends data
// behind the cursor, so anything not persisted here would be lost if the
// response never reached the user. A staging failure leaves the cursor put and
// a retry re-stages idempotently. Returns whether more pages remain.
func (s *PlaidService) fetchAndStage(ctx context.Context, bookGUID, itemGUID, accessToken, cursor string) (bool, error) {
	hasMore := false
	for i := 0; i < maxSyncPages; i++ {
		delta, nextCursor, more, err := s.client.SyncTransactions(ctx, accessToken, cursor)
		if err != nil {
			// ITEM_LOGIN_REQUIRED must reach the user as "reconnect your bank",
			// not as a generic failure.
			if errors.Is(err, ErrReauthRequired) {
				return false, ErrReauthRequired
			}
			log.Printf("plaid SyncTransactions error: %v", err)
			return false, fmt.Errorf("sync failed")
		}
		if err := s.stageDelta(ctx, bookGUID, itemGUID, delta); err != nil {
			return false, fmt.Errorf("stage sync delta: %w", err)
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
		// Surface it: a persistent failure here would otherwise return 200
		// forever while silently re-fetching the same pages on every sync.
		// Retrying is safe — staging is durable and the upserts are idempotent.
		return false, fmt.Errorf("persist sync cursor: %w", err)
	}
	if _, err := s.pool.Exec(ctx,
		`UPDATE accounts
		 SET metadata = jsonb_set(COALESCE(metadata,'{}'), '{plaid,last_synced_at}', to_jsonb($1::text), true),
		     updated_at = $2,
		     version    = version + 1
		 WHERE book_guid = $3 AND metadata->'plaid'->>'item_guid' = $4`,
		now.Format(time.RFC3339), now, bookGUID, itemGUID,
	); err != nil {
		// Same policy as the cursor write above: a DB failure here must be
		// visible, not a silent log (the auto-sync trigger reads this value).
		return false, fmt.Errorf("propagate last_synced_at: %w", err)
	}
	return hasMore, nil
}

// stageDelta applies one /transactions/sync page to durable staging. Removed
// ids are dropped; added and modified transactions are upserted. A posted
// transaction whose pending predecessor was already imported is skipped
// entirely — importing it again would duplicate the money movement.
func (s *PlaidService) stageDelta(ctx context.Context, bookGUID, itemGUID string, delta SyncDelta) error {
	// Upserts run BEFORE removals: a pending→posted delta carries the pending
	// predecessor in Removed, and deleting it first would destroy the state the
	// posted row must inherit from it (the dismissed flag, notably).
	all := append(append([]PlaidTxn{}, delta.Added...), delta.Modified...)

	// Two passes: zero-amount entries are handled AFTER every upsert. A $0
	// settle and its non-zero pending can arrive in the SAME page in either
	// order, and the stale-suggestion cleanup must win regardless of ordering.
	var zeroed []PlaidTxn
	for _, txn := range all {
		if txn.AmountNum == 0 {
			zeroed = append(zeroed, txn)
			continue
		}
		dismissed := false
		if txn.PendingTransactionID != "" {
			imported, err := s.transactionExists(ctx, bookGUID, txn.PendingTransactionID)
			if err != nil {
				return fmt.Errorf("pending correlation for %s: %w", txn.TransactionID, err)
			}
			if imported {
				match, err := s.importedValueMatches(ctx, bookGUID, txn.PendingTransactionID, txn.AccountID, txn.AmountNum, txn.AmountDenom)
				if err != nil {
					return fmt.Errorf("pending value check for %s: %w", txn.TransactionID, err)
				}
				if match {
					// Same value: the posted txn is the one already imported as
					// pending — drop both staged copies and skip.
					if _, err := s.pool.Exec(ctx,
						`DELETE FROM plaid_staged_transactions WHERE book_guid = $1 AND transaction_id = ANY($2)`,
						bookGUID, []string{txn.PendingTransactionID, txn.TransactionID},
					); err != nil {
						return fmt.Errorf("drop replaced pending %s: %w", txn.PendingTransactionID, err)
					}
					continue
				}
				// Value diverged (tips/adjustments): keep the posted txn staged so
				// the user sees it as a suggestion instead of the book silently
				// keeping the stale pending amount forever.
				log.Printf("plaid sync: posted txn %s diverges in value from imported pending %s; keeping it as a suggestion", txn.TransactionID, txn.PendingTransactionID)
			}
			// "Never import this transaction" must survive the id change when a
			// dismissed pending posts under a new transaction_id.
			if err := s.pool.QueryRow(ctx,
				`SELECT COALESCE(bool_or(dismissed), false) FROM plaid_staged_transactions
				 WHERE book_guid = $1 AND transaction_id = $2`,
				bookGUID, txn.PendingTransactionID,
			).Scan(&dismissed); err != nil {
				return fmt.Errorf("dismissed propagation for %s: %w", txn.TransactionID, err)
			}
		}
		// dismissed semantics: a row can be dismissed (incl. inherited from its
		// pending predecessor) but never un-dismissed by a bank-side update —
		// "never import this transaction" is permanent (spec §13).
		if _, err := s.pool.Exec(ctx,
			`INSERT INTO plaid_staged_transactions
				(book_guid, item_guid, transaction_id, pending_transaction_id, plaid_account_id,
				 post_date, description, amount_num, amount_denom, pending, dismissed)
			 VALUES ($1, $2, $3, NULLIF($4,''), $5, $6, $7, $8, $9, $10, $11)
			 ON CONFLICT (book_guid, transaction_id) DO UPDATE SET
				pending_transaction_id = EXCLUDED.pending_transaction_id,
				post_date              = EXCLUDED.post_date,
				description            = EXCLUDED.description,
				amount_num             = EXCLUDED.amount_num,
				amount_denom           = EXCLUDED.amount_denom,
				pending                = EXCLUDED.pending,
				dismissed              = plaid_staged_transactions.dismissed OR EXCLUDED.dismissed`,
			bookGUID, itemGUID, txn.TransactionID, txn.PendingTransactionID, txn.AccountID,
			txn.Date, txn.Description, txn.AmountNum, txn.AmountDenom, txn.Pending, dismissed,
		); err != nil {
			return fmt.Errorf("stage txn %s: %w", txn.TransactionID, err)
		}
	}

	for _, txn := range zeroed {
		// A $0 transaction cannot exist in a double-entry book (zero-value
		// splits are dropped, leaving <2 splits) — importing it would fail
		// forever and the pending→posted value correlation can never match a
		// split that was never written. Skip at the source, but:
		// (a) drop the stale non-zero staged copies of BOTH the zeroed txn and
		//     its pending predecessor (running after the upsert pass, so an
		//     in-page pending staged moments ago is cleaned too), and
		// (b) surface the divergence when a txn we already imported settles $0.
		staleIDs := []string{txn.TransactionID}
		if txn.PendingTransactionID != "" {
			staleIDs = append(staleIDs, txn.PendingTransactionID)
		}
		if _, err := s.pool.Exec(ctx,
			`DELETE FROM plaid_staged_transactions
			 WHERE book_guid = $1 AND transaction_id = ANY($2) AND NOT dismissed`,
			bookGUID, staleIDs,
		); err != nil {
			return fmt.Errorf("drop zeroed staged txn %s: %w", txn.TransactionID, err)
		}
		for _, id := range staleIDs {
			if imported, err := s.transactionExists(ctx, bookGUID, id); err != nil {
				return fmt.Errorf("zero-amount divergence check for %s: %w", id, err)
			} else if imported {
				log.Printf("plaid sync: txn %s was imported with a non-zero value but the bank now reports $0 — books may diverge from the bank", id)
			}
		}
		log.Printf("plaid sync: skipping zero-amount txn %s (not representable in a double-entry book)", txn.TransactionID)
	}

	if len(delta.Removed) > 0 {
		// Surface (but never auto-apply) bank-side removals of transactions the
		// user already imported: deleting financial data behind the user's back
		// is wrong, but the book-vs-bank divergence must not pass silently.
		impRows, err := s.pool.Query(ctx,
			`SELECT metadata->'plaid'->>'transaction_id' FROM transactions
			 WHERE book_guid = $1 AND metadata->'plaid'->>'transaction_id' = ANY($2)`,
			bookGUID, delta.Removed,
		)
		if err != nil {
			return fmt.Errorf("check removed against imported: %w", err)
		}
		for impRows.Next() {
			var id string
			if err := impRows.Scan(&id); err != nil {
				impRows.Close()
				return fmt.Errorf("scan removed-imported id: %w", err)
			}
			log.Printf("plaid sync: bank removed transaction %s which was already imported into book %s; books may diverge from the bank", id, bookGUID)
		}
		impRows.Close()
		if err := impRows.Err(); err != nil {
			return fmt.Errorf("iterate removed-imported ids: %w", err)
		}

		// Dismissed rows are kept as tombstones instead of deleted: the posted
		// successor of a dismissed pending may only arrive in a LATER sync page
		// (or call), and the dismissal inheritance reads the predecessor's row.
		// Tombstones are invisible (suggestions filter on dismissed) and bounded
		// by the number of explicit dismissals.
		if _, err := s.pool.Exec(ctx,
			`DELETE FROM plaid_staged_transactions
			 WHERE book_guid = $1 AND transaction_id = ANY($2) AND NOT dismissed`,
			bookGUID, delta.Removed,
		); err != nil {
			return fmt.Errorf("remove staged: %w", err)
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

// importedValueMatches reports whether the transaction imported under
// plaidTxnID moved exactly this amount on the LINKED BANK ACCOUNT. The import
// writes the bank split as -amount, so comparing only that split (sign-aware)
// is what detects pending→posted drift: matching against ANY split would let a
// reversal (-amount) collide with the bank split of the original charge and be
// silently discarded as "same value".
func (s *PlaidService) importedValueMatches(ctx context.Context, bookGUID, plaidTxnID, plaidAccountID string, num, denom int64) (bool, error) {
	var match bool
	err := s.pool.QueryRow(ctx,
		`SELECT EXISTS (
			SELECT 1 FROM transactions t
			JOIN splits s ON s.tx_guid = t.guid
			JOIN accounts a ON a.guid = s.account_guid AND a.book_guid = t.book_guid
			WHERE t.book_guid = $1
			  AND t.metadata->'plaid'->>'transaction_id' = $2
			  AND a.metadata->'plaid'->>'account_id' = $3
			  AND s.value_num = -($4::bigint) AND s.value_denom = $5)`,
		bookGUID, plaidTxnID, plaidAccountID, num, denom,
	).Scan(&match)
	return match, err
}

// DismissStaged permanently hides staged transactions from future suggestions
// (the rows are kept for dedupe/pending correlation; disconnect cascades them).
func (s *PlaidService) DismissStaged(ctx context.Context, transactionIDs []string) (int, error) {
	bookGUID := auth.BookGUIDFromCtx(ctx)
	ct, err := s.pool.Exec(ctx,
		`UPDATE plaid_staged_transactions SET dismissed = true
		 WHERE book_guid = $1 AND transaction_id = ANY($2)`,
		bookGUID, transactionIDs,
	)
	if err != nil {
		return 0, err
	}
	return int(ct.RowsAffected()), nil
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
			pendingID   *string
		)
		err := s.pool.QueryRow(ctx,
			`SELECT post_date, description, amount_num, amount_denom, plaid_account_id, pending_transaction_id
			 FROM plaid_staged_transactions
			 WHERE book_guid = $1 AND transaction_id = $2`,
			bookGUID, row.TransactionID,
		).Scan(&postDate, &description, &amountNum, &amountDenom, &plaidAcctID, &pendingID)
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

		// pending→posted race defense at import time: by now any concurrent
		// import of the pending predecessor has committed, so this check is
		// reliable even when staging happened mid-race. Same value → benign
		// duplicate, skip; diverged value → the user explicitly confirmed the
		// correction in the matcher, proceed (and log).
		if pendingID != nil {
			imported, err := s.transactionExists(ctx, bookGUID, *pendingID)
			if err != nil {
				return result, fmt.Errorf("pending correlation for %s: %w", row.TransactionID, err)
			}
			if imported {
				match, err := s.importedValueMatches(ctx, bookGUID, *pendingID, plaidAcctID, amountNum, amountDenom)
				if err != nil {
					return result, fmt.Errorf("pending value check for %s: %w", row.TransactionID, err)
				}
				if match {
					s.deleteStaged(ctx, bookGUID, row.TransactionID)
					continue
				}
				log.Printf("plaid import: %s diverges in value from imported pending %s; importing as user-confirmed correction", row.TransactionID, *pendingID)
			}
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
	var itemID string
	var itemVersion int
	err := s.pool.QueryRow(ctx,
		`SELECT item_id, access_token_ciphertext, access_token_nonce, version FROM plaid_items WHERE guid = $1 AND book_guid = $2`,
		itemGUID, bookGUID,
	).Scan(&itemID, &ciphertext, &nonce, &itemVersion)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrItemNotFound
	}
	if err != nil {
		return err
	}

	forced := false
	accessToken, err := s.loadAndDecryptToken(ctx, bookGUID, itemGUID, itemID, ciphertext, nonce, itemVersion)
	if err != nil {
		if errors.Is(err, ErrLegacyTokenNeedsFlag) {
			// The token is perfectly recoverable: it decrypts in the legacy
			// format and only the sunset flag is off. Abort — never destroy a
			// token that has a clean migration path.
			log.Printf("plaid disconnect: item %s holds a legacy-format token; enable PLAID_LEGACY_TOKEN_FALLBACK, retry the disconnect (or a sync, which re-seals it), then disable the flag", itemGUID)
			return fmt.Errorf("token needs the legacy fallback flag: %w", err)
		}
		// A genuinely undecryptable token (every configured key + probes
		// failed) is unusable for /item/remove. Aborting forever would brick
		// the item (no sync, no disconnect, down-migration guard blocks
		// rollback) — but the cause may be a RECOVERABLE key misconfiguration,
		// so the row is ARCHIVED to plaid_migration_audit before deletion: an
		// operator who restores the key can restore the row from the audit
		// trail. The Plaid-side Item must be removed in the dashboard.
		forced = true
		log.Printf("plaid disconnect: token for item %s is undecryptable (%v); archiving row to plaid_migration_audit and deleting locally — if this is a key misconfiguration, restore the key and recover the row from the audit table; remove the Item in the Plaid dashboard to stop billing", itemGUID, err)
	} else if rmErr := s.client.RemoveItem(ctx, accessToken); rmErr != nil {
		// Abort: the token is VALID, so deleting the local row would destroy
		// the only copy while the Item stays alive (and billable) at Plaid.
		// Keeping the row lets the user simply retry the disconnect.
		log.Printf("plaid RemoveItem failed; aborting disconnect: %v", rmErr)
		return fmt.Errorf("plaid item removal failed")
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	if forced {
		// Archive the full row (ciphertext included) atomically with the
		// deletion, OCC-guarded on the version read alongside the ciphertext:
		// if a concurrent re-link (Exchange bumps version) replaced the token
		// between the failed decrypt and this commit, archiving/deleting would
		// destroy a NEW, VALID token — abort instead and let the caller retry
		// against the fresh row.
		ct, err := tx.Exec(ctx,
			`INSERT INTO plaid_migration_audit (migration, payload)
			 SELECT 'force-disconnect', to_jsonb(p.*) FROM plaid_items p
			 WHERE p.guid = $1 AND p.book_guid = $2 AND p.version = $3`,
			itemGUID, bookGUID, itemVersion,
		)
		if err != nil {
			return fmt.Errorf("archive item before forced disconnect: %w", err)
		}
		if ct.RowsAffected() == 0 {
			return ErrConcurrentModification
		}
	}

	_, err = tx.Exec(ctx,
		`UPDATE accounts
		 SET metadata = metadata - 'plaid', updated_at = NOW(), version = version + 1
		 WHERE book_guid = $1 AND metadata->'plaid'->>'item_guid' = $2`,
		bookGUID, itemGUID,
	)
	if err != nil {
		return err
	}

	if forced {
		ct, err := tx.Exec(ctx,
			`DELETE FROM plaid_items WHERE guid = $1 AND book_guid = $2 AND version = $3`,
			itemGUID, bookGUID, itemVersion,
		)
		if err != nil {
			return err
		}
		if ct.RowsAffected() == 0 {
			return ErrConcurrentModification
		}
	} else if _, err := tx.Exec(ctx,
		`DELETE FROM plaid_items WHERE guid = $1 AND book_guid = $2`, itemGUID, bookGUID,
	); err != nil {
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
