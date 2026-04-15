package services

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/user/antimoney/internal/auth"
	"github.com/user/antimoney/internal/gnc"
	"github.com/user/antimoney/internal/models"
)

var (
	ErrUnbalancedTransaction = errors.New("transaction splits do not sum to zero")
	ErrVersionConflict       = errors.New("version conflict: record was modified by another user")
	ErrNotFound              = errors.New("not found")
	ErrPlaceholderAccount    = errors.New("cannot post splits to a placeholder account")
	ErrInvalidSplit          = errors.New("invalid split: value_denom and quantity_denom must be non-zero")
	ErrTooFewSplits          = errors.New("a transaction must have at least 2 splits")
)

// TransactionService handles the business logic for creating and managing transactions.
type TransactionService struct {
	pool *pgxpool.Pool
}

func NewTransactionService(pool *pgxpool.Pool) *TransactionService {
	return &TransactionService{pool: pool}
}

// CreateTransactionRequest is the payload for creating a new transaction.
type CreateTransactionRequest struct {
	CustomID    string               `json:"custom_id"`
	PostDate    time.Time            `json:"post_date"`
	Description string               `json:"description"`
	Splits      []CreateSplitRequest `json:"splits"`
}

type CreateSplitRequest struct {
	AccountGUID   string `json:"account_guid"`
	Memo          string `json:"memo"`
	ValueNum      int64  `json:"value_num"`
	ValueDenom    int64  `json:"value_denom"`
	QuantityNum   int64  `json:"quantity_num"`
	QuantityDenom int64  `json:"quantity_denom"`
}

// CreateTransaction creates a transaction with its splits as a single atomic operation.
// It enforces the zero-sum invariant, normalizes timestamps, and scopes to user's book.
func (s *TransactionService) CreateTransaction(ctx context.Context, req CreateTransactionRequest) (*models.Transaction, error) {
	bookGUID := auth.BookGUIDFromCtx(ctx)

	// 1. Validate splits
	validSplits := make([]CreateSplitRequest, 0, len(req.Splits))
	var values []gnc.Numeric
	for _, sp := range req.Splits {
		if sp.ValueNum == 0 {
			continue // zero-value splits are silently dropped
		}
		if sp.ValueDenom == 0 || sp.QuantityDenom == 0 {
			return nil, ErrInvalidSplit
		}
		validSplits = append(validSplits, sp)
		values = append(values, gnc.New(sp.ValueNum, sp.ValueDenom))
	}
	req.Splits = validSplits

	sum := gnc.Sum(values)

	// 2. Normalize post_date to 11:00 UTC (per GnuCash convention)
	postDate := normalizePostDate(req.PostDate)

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	// 2.5. Auto-balance if needed
	if !sum.IsZero() {
		var imbalanceAcctGUID string
		err := tx.QueryRow(ctx,
			"SELECT guid FROM accounts WHERE book_guid = $1 AND name = 'Imbalance' LIMIT 1",
			bookGUID,
		).Scan(&imbalanceAcctGUID)

		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				var rootAcctGUID string
				if errRoot := tx.QueryRow(ctx, "SELECT root_account_guid FROM books WHERE guid = $1", bookGUID).Scan(&rootAcctGUID); errRoot != nil {
					return nil, fmt.Errorf("find root account: %w", errRoot)
				}

				imbalanceAcctGUID = uuid.New().String()
				now := time.Now().UTC()
				_, errIns := tx.Exec(ctx,
					`INSERT INTO accounts (guid, name, account_type, parent_guid, book_guid, placeholder, description, metadata, version, created_at, updated_at)
					 VALUES ($1, 'Imbalance', 'EQUITY', $2, $3, false, 'Automatically created imbalance account', '{}', 1, $4, $4)`,
					imbalanceAcctGUID, rootAcctGUID, bookGUID, now,
				)
				if errIns != nil {
					return nil, fmt.Errorf("create imbalance account: %w", errIns)
				}
			} else {
				return nil, fmt.Errorf("lookup imbalance account: %w", err)
			}
		}

		negSum := sum.Neg()
		req.Splits = append(req.Splits, CreateSplitRequest{
			AccountGUID:   imbalanceAcctGUID,
			Memo:          "Auto-balancing split",
			ValueNum:      negSum.Num,
			ValueDenom:    negSum.Denom,
			QuantityNum:   negSum.Num,
			QuantityDenom: negSum.Denom,
		})
	}

	if len(req.Splits) < 2 {
		return nil, fmt.Errorf("a transaction must have at least 2 splits")
	}

	// 3. Verify no splits target placeholder accounts, AND accounts belong to user's book
	for _, sp := range req.Splits {
		var placeholder bool
		err := tx.QueryRow(ctx,
			"SELECT placeholder FROM accounts WHERE guid = $1 AND book_guid = $2",
			sp.AccountGUID, bookGUID,
		).Scan(&placeholder)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return nil, fmt.Errorf("account %s not found or access denied", sp.AccountGUID)
			}
			return nil, fmt.Errorf("lookup account %s: %w", sp.AccountGUID, err)
		}
		if placeholder {
			return nil, ErrPlaceholderAccount
		}
	}

	// 4. Insert transaction scoped to book
	txGUID := uuid.New().String()
	now := time.Now().UTC()

	err = tx.QueryRow(ctx,
		`INSERT INTO transactions (guid, custom_id, book_guid, post_date, enter_date, description, metadata, version, created_at, updated_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, 1, $8, $8)
		 RETURNING guid`,
		txGUID, req.CustomID, bookGUID, postDate, now, req.Description, json.RawMessage("{}"), now,
	).Scan(&txGUID)
	if err != nil {
		return nil, fmt.Errorf("insert transaction: %w", err)
	}

	// 5. Insert splits
	resultSplits := make([]models.Split, len(req.Splits))
	for i, sp := range req.Splits {
		splitGUID := uuid.New().String()
		_, err := tx.Exec(ctx,
			`INSERT INTO splits (guid, tx_guid, account_guid, memo, value_num, value_denom, quantity_num, quantity_denom, created_at)
			 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
			splitGUID, txGUID, sp.AccountGUID, sp.Memo,
			sp.ValueNum, sp.ValueDenom, sp.QuantityNum, sp.QuantityDenom, now,
		)
		if err != nil {
			return nil, fmt.Errorf("insert split %d: %w", i, err)
		}
		resultSplits[i] = models.Split{
			GUID:          splitGUID,
			TxGUID:        txGUID,
			AccountGUID:   sp.AccountGUID,
			Memo:          sp.Memo,
			ValueNum:      sp.ValueNum,
			ValueDenom:    sp.ValueDenom,
			QuantityNum:   sp.QuantityNum,
			QuantityDenom: sp.QuantityDenom,
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit transaction: %w", err)
	}

	bk := bookGUID
	return &models.Transaction{
		GUID:        txGUID,
		CustomID:    req.CustomID,
		BookGUID:    &bk,
		PostDate:    postDate,
		EnterDate:   now,
		Description: req.Description,
		Metadata:    json.RawMessage("{}"),
		Version:     1,
		CreatedAt:   now,
		UpdatedAt:   now,
		Splits:      resultSplits,
	}, nil
}

// UpdateTransactionRequest is the payload for updating an existing transaction.
type UpdateTransactionRequest struct {
	CustomID    string               `json:"custom_id"`
	PostDate    time.Time            `json:"post_date"`
	Description string               `json:"description"`
	Splits      []CreateSplitRequest `json:"splits"`
}

// UpdateTransaction updates an existing transaction and replaces its splits.
// It also resets the reconcile state of all new splits to 'n', effectively unreconciling the transaction.
func (s *TransactionService) UpdateTransaction(ctx context.Context, txGUID string, req UpdateTransactionRequest) (*models.Transaction, error) {
	bookGUID := auth.BookGUIDFromCtx(ctx)

	// 1. Verify transaction exists and belongs to the user
	var existingVersion int
	err := s.pool.QueryRow(ctx, "SELECT version FROM transactions WHERE guid = $1 AND book_guid = $2", txGUID, bookGUID).Scan(&existingVersion)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("lookup transaction: %w", err)
	}

	// 2. Validate splits
	validSplits := make([]CreateSplitRequest, 0, len(req.Splits))
	var values []gnc.Numeric
	for _, sp := range req.Splits {
		if sp.ValueNum == 0 {
			continue // zero-value splits are silently dropped
		}
		if sp.ValueDenom == 0 || sp.QuantityDenom == 0 {
			return nil, ErrInvalidSplit
		}
		validSplits = append(validSplits, sp)
		values = append(values, gnc.New(sp.ValueNum, sp.ValueDenom))
	}
	req.Splits = validSplits

	sum := gnc.Sum(values)
	if !sum.IsZero() {
		return nil, ErrUnbalancedTransaction
	}

	if len(req.Splits) < 2 {
		return nil, ErrTooFewSplits
	}

	postDate := normalizePostDate(req.PostDate)

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	// 3. Verify no splits target placeholder accounts
	for _, sp := range req.Splits {
		var placeholder bool
		err := tx.QueryRow(ctx,
			"SELECT placeholder FROM accounts WHERE guid = $1 AND book_guid = $2",
			sp.AccountGUID, bookGUID,
		).Scan(&placeholder)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return nil, fmt.Errorf("account %s not found or access denied", sp.AccountGUID)
			}
			return nil, fmt.Errorf("lookup account %s: %w", sp.AccountGUID, err)
		}
		if placeholder {
			return nil, ErrPlaceholderAccount
		}
	}

	now := time.Now().UTC()

	// 4. Update transaction
	_, err = tx.Exec(ctx,
		`UPDATE transactions SET custom_id = $1, post_date = $2, description = $3, version = version + 1, updated_at = $4
		 WHERE guid = $5 AND book_guid = $6`,
		req.CustomID, postDate, req.Description, now, txGUID, bookGUID,
	)
	if err != nil {
		return nil, fmt.Errorf("update transaction: %w", err)
	}

	// 5. Delete old splits
	_, err = tx.Exec(ctx, "DELETE FROM splits WHERE tx_guid = $1", txGUID)
	if err != nil {
		return nil, fmt.Errorf("delete old splits: %w", err)
	}

	// 6. Insert new splits (always unreconciled 'n')
	resultSplits := make([]models.Split, len(req.Splits))
	for i, sp := range req.Splits {
		splitGUID := uuid.New().String()
		_, err := tx.Exec(ctx,
			`INSERT INTO splits (guid, tx_guid, account_guid, memo, value_num, value_denom, quantity_num, quantity_denom, reconcile_state, created_at)
			 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, 'n', $9)`,
			splitGUID, txGUID, sp.AccountGUID, sp.Memo,
			sp.ValueNum, sp.ValueDenom, sp.QuantityNum, sp.QuantityDenom, now,
		)
		if err != nil {
			return nil, fmt.Errorf("insert split %d: %w", i, err)
		}
		resultSplits[i] = models.Split{
			GUID:           splitGUID,
			TxGUID:         txGUID,
			AccountGUID:    sp.AccountGUID,
			Memo:           sp.Memo,
			ValueNum:       sp.ValueNum,
			ValueDenom:     sp.ValueDenom,
			QuantityNum:    sp.QuantityNum,
			QuantityDenom:  sp.QuantityDenom,
			ReconcileState: "n",
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit transaction update: %w", err)
	}

	bk := bookGUID
	return &models.Transaction{
		GUID:        txGUID,
		CustomID:    req.CustomID,
		BookGUID:    &bk,
		PostDate:    postDate,
		Description: req.Description,
		Version:     existingVersion + 1,
		UpdatedAt:   now,
		Splits:      resultSplits,
	}, nil
}

// GetTransaction retrieves a transaction with all its splits, scoped to user's book.
func (s *TransactionService) GetTransaction(ctx context.Context, guid string) (*models.Transaction, error) {
	bookGUID := auth.BookGUIDFromCtx(ctx)
	txn := &models.Transaction{}
	err := s.pool.QueryRow(ctx,
		`SELECT guid, custom_id, post_date, enter_date, description, metadata, version, created_at, updated_at
		 FROM transactions WHERE guid = $1 AND book_guid = $2`, guid, bookGUID,
	).Scan(&txn.GUID, &txn.CustomID, &txn.PostDate, &txn.EnterDate,
		&txn.Description, &txn.Metadata, &txn.Version, &txn.CreatedAt, &txn.UpdatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}

	rows, err := s.pool.Query(ctx,
		`SELECT s.guid, s.tx_guid, s.account_guid, s.memo,
		        s.value_num, s.value_denom, s.quantity_num, s.quantity_denom,
		        s.reconcile_state, a.name
		 FROM splits s JOIN accounts a ON s.account_guid = a.guid
		 WHERE s.tx_guid = $1
		 ORDER BY s.created_at`, guid,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var sp models.Split
		if err := rows.Scan(&sp.GUID, &sp.TxGUID, &sp.AccountGUID, &sp.Memo,
			&sp.ValueNum, &sp.ValueDenom, &sp.QuantityNum, &sp.QuantityDenom,
			&sp.ReconcileState, &sp.AccountName); err != nil {
			return nil, err
		}
		txn.Splits = append(txn.Splits, sp)
	}

	return txn, nil
}

// ListTransactions returns all transactions for the user's book.
func (s *TransactionService) ListTransactions(ctx context.Context, limit, offset int) ([]models.Transaction, error) {
	bookGUID := auth.BookGUIDFromCtx(ctx)
	if limit <= 0 {
		limit = 50
	}

	rows, err := s.pool.Query(ctx,
		`SELECT guid, custom_id, post_date, enter_date, description, metadata, version, created_at, updated_at
		 FROM transactions WHERE book_guid = $3
		 ORDER BY post_date DESC, custom_id DESC LIMIT $1 OFFSET $2`, limit, offset, bookGUID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var txns []models.Transaction
	for rows.Next() {
		var txn models.Transaction
		if err := rows.Scan(&txn.GUID, &txn.CustomID, &txn.PostDate, &txn.EnterDate,
			&txn.Description, &txn.Metadata, &txn.Version, &txn.CreatedAt, &txn.UpdatedAt); err != nil {
			return nil, err
		}
		txns = append(txns, txn)
	}

	// Load splits for each transaction
	for i := range txns {
		splitRows, err := s.pool.Query(ctx,
			`SELECT s.guid, s.tx_guid, s.account_guid, s.memo,
			        s.value_num, s.value_denom, s.quantity_num, s.quantity_denom,
			        s.reconcile_state, a.name
			 FROM splits s JOIN accounts a ON s.account_guid = a.guid
			 WHERE s.tx_guid = $1
			 ORDER BY s.created_at`, txns[i].GUID,
		)
		if err != nil {
			return nil, err
		}
		for splitRows.Next() {
			var sp models.Split
			if err := splitRows.Scan(&sp.GUID, &sp.TxGUID, &sp.AccountGUID, &sp.Memo,
				&sp.ValueNum, &sp.ValueDenom, &sp.QuantityNum, &sp.QuantityDenom,
				&sp.ReconcileState, &sp.AccountName); err != nil {
				splitRows.Close()
				return nil, err
			}
			txns[i].Splits = append(txns[i].Splits, sp)
		}
		splitRows.Close()
	}

	return txns, nil
}

// DeleteTransaction removes a transaction and its splits (CASCADE), scoped to user's book.
func (s *TransactionService) DeleteTransaction(ctx context.Context, guid string) error {
	bookGUID := auth.BookGUIDFromCtx(ctx)
	result, err := s.pool.Exec(ctx,
		"DELETE FROM transactions WHERE guid = $1 AND book_guid = $2", guid, bookGUID,
	)
	if err != nil {
		return err
	}
	if result.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// GetAccountRegister returns the register entries for a specific account.
func (s *TransactionService) GetAccountRegister(ctx context.Context, accountGUID string) ([]models.RegisterEntry, error) {
	bookGUID := auth.BookGUIDFromCtx(ctx)

	// Verify account belongs to user's book
	var accountType models.AccountType
	err := s.pool.QueryRow(ctx,
		"SELECT account_type FROM accounts WHERE guid = $1 AND book_guid = $2",
		accountGUID, bookGUID,
	).Scan(&accountType)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}

	rows, err := s.pool.Query(ctx,
		`SELECT t.guid, t.custom_id, t.post_date, t.description,
		        s.guid, s.value_num, s.value_denom, s.quantity_num, s.quantity_denom, s.memo, s.reconcile_state
		 FROM splits s
		 JOIN transactions t ON s.tx_guid = t.guid
		 WHERE s.account_guid = $1
		 ORDER BY t.post_date ASC, t.custom_id ASC, t.enter_date ASC`, accountGUID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var entries []models.RegisterEntry
	runningBalance := gnc.Zero()

	for rows.Next() {
		var (
			txGUID         string
			customID       string
			postDate       time.Time
			description    string
			splitGUID      string
			valueNum       int64
			valueDenom     int64
			qtyNum         int64
			qtyDenom       int64
			memo           string
			reconcileState string
		)
		if err := rows.Scan(&txGUID, &customID, &postDate, &description,
			&splitGUID, &valueNum, &valueDenom, &qtyNum, &qtyDenom, &memo, &reconcileState); err != nil {
			return nil, err
		}

		amount := gnc.New(qtyNum, qtyDenom)
		runningBalance = runningBalance.Add(amount)

		entry := models.RegisterEntry{
			TransactionGUID: txGUID,
			CustomID:        customID,
			PostDate:        postDate,
			Description:     description,
			Balance:         runningBalance.ToFloat64(),
			SplitMemo:       memo,
			SplitGUID:       splitGUID,
			ReconcileState:  reconcileState,
		}

		f := amount.ToFloat64()
		if accountType.IsDebitNormal() {
			if f >= 0 {
				entry.Deposit = &f
			} else {
				abs := -f
				entry.Withdrawal = &abs
			}
		} else {
			if f <= 0 {
				abs := -f
				entry.Deposit = &abs
			} else {
				entry.Withdrawal = &f
			}
		}

		transferName, transferGUID, err := s.getTransferAccount(ctx, txGUID, accountGUID)
		if err != nil {
			return nil, err
		}
		entry.TransferAccount = transferName
		entry.TransferAccountGUID = transferGUID

		entries = append(entries, entry)
	}

	return entries, nil
}

// GetAccountRegisterPaged returns a paginated slice of register entries.
// cursorDate is an ISO date string (YYYY-MM-DD). direction is "before", "after", or "around".
// For "around", it returns entries centered on the given date.
// For "before", returns entries before the cursor date (loading older).
// For "after", returns entries after the cursor date (loading newer).
func (s *TransactionService) GetAccountRegisterPaged(ctx context.Context, accountGUID, cursorDate, direction string, limit int) (*models.RegisterPage, error) {
	bookGUID := auth.BookGUIDFromCtx(ctx)

	// Verify account belongs to user's book
	var accountType models.AccountType
	err := s.pool.QueryRow(ctx,
		"SELECT account_type FROM accounts WHERE guid = $1 AND book_guid = $2",
		accountGUID, bookGUID,
	).Scan(&accountType)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}

	// Count total entries
	var totalCount int
	err = s.pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM splits s JOIN transactions t ON s.tx_guid = t.guid
		 WHERE s.account_guid = $1 AND t.book_guid = $2`, accountGUID, bookGUID,
	).Scan(&totalCount)
	if err != nil {
		return nil, fmt.Errorf("count entries: %w", err)
	}

	// Parse cursor: try integer first
	cursorOffset, offsetErr := strconv.Atoi(cursorDate)
	isOffset := offsetErr == nil

	// Determine offset and limit based on direction
	var queryOffset, queryLimit int

	switch direction {
	case "before":
		if isOffset {
			queryOffset = cursorOffset - limit
			queryLimit = limit
			if queryOffset < 0 {
				queryLimit = cursorOffset // only fetch what's left
				queryOffset = 0
			}
		} else {
			// fallback if it's somehow a date
			parsedDate, _ := time.Parse("2006-01-02", cursorDate)
			cursorTimestamp := time.Date(parsedDate.Year(), parsedDate.Month(), parsedDate.Day(), 11, 0, 0, 0, time.UTC)
			var countBefore int
			_ = s.pool.QueryRow(ctx,
				`SELECT COUNT(*) FROM splits s JOIN transactions t ON s.tx_guid = t.guid
				 WHERE s.account_guid = $1 AND t.book_guid = $2 AND t.post_date < $3`,
				accountGUID, bookGUID, cursorTimestamp,
			).Scan(&countBefore)
			queryLimit = limit
			queryOffset = countBefore - limit
			if queryOffset < 0 {
				queryLimit = countBefore
				queryOffset = 0
			}
		}

	case "after":
		if isOffset {
			queryOffset = cursorOffset + 1
			queryLimit = limit
		} else {
			// fallback
			parsedDate, _ := time.Parse("2006-01-02", cursorDate)
			cursorTimestamp := time.Date(parsedDate.Year(), parsedDate.Month(), parsedDate.Day(), 11, 0, 0, 0, time.UTC)
			var countUpTo int
			_ = s.pool.QueryRow(ctx,
				`SELECT COUNT(*) FROM splits s JOIN transactions t ON s.tx_guid = t.guid
				 WHERE s.account_guid = $1 AND t.book_guid = $2 AND t.post_date <= $3`,
				accountGUID, bookGUID, cursorTimestamp,
			).Scan(&countUpTo)
			queryOffset = countUpTo
			queryLimit = limit
		}

	default: // "around"
		parsedDate, err := time.Parse("2006-01-02", cursorDate)
		if err != nil {
			return nil, fmt.Errorf("invalid cursor_date for around: %w", err)
		}
		cursorTimestamp := time.Date(parsedDate.Year(), parsedDate.Month(), parsedDate.Day(), 11, 0, 0, 0, time.UTC)
		
		var countBefore int
		err = s.pool.QueryRow(ctx,
			`SELECT COUNT(*) FROM splits s JOIN transactions t ON s.tx_guid = t.guid
			 WHERE s.account_guid = $1 AND t.book_guid = $2 AND t.post_date < $3`,
			accountGUID, bookGUID, cursorTimestamp,
		).Scan(&countBefore)
		if err != nil {
			return nil, fmt.Errorf("count before: %w", err)
		}
		
		beforeCount := limit * 3 / 4
		queryOffset = countBefore - beforeCount
		if queryOffset < 0 {
			queryOffset = 0
		}
		queryLimit = limit
	}

	if queryLimit <= 0 {
		return &models.RegisterPage{
			HasBefore:  queryOffset > 0,
			HasAfter:   queryOffset < totalCount,
			TotalCount: totalCount,
		}, nil
	}

	// Compute running balance for all entries before our page
	priorBalance := gnc.Zero()
	if queryOffset > 0 {
		rows, err := s.pool.Query(ctx,
			`SELECT s.quantity_num, s.quantity_denom
			 FROM splits s
			 JOIN transactions t ON s.tx_guid = t.guid
			 WHERE s.account_guid = $1 AND t.book_guid = $2
			 ORDER BY t.post_date ASC, t.custom_id ASC, t.enter_date ASC
			 LIMIT $3`,
			accountGUID, bookGUID, queryOffset,
		)
		if err != nil {
			return nil, fmt.Errorf("compute prior balance: %w", err)
		}
		defer rows.Close()

		for rows.Next() {
			var qtyNum, qtyDenom int64
			if err := rows.Scan(&qtyNum, &qtyDenom); err != nil {
				return nil, err
			}
			priorBalance = priorBalance.Add(gnc.New(qtyNum, qtyDenom))
		}
		rows.Close()
	}

	// Fetch the page of entries
	pageRows, err := s.pool.Query(ctx,
		`SELECT t.guid, t.custom_id, t.post_date, t.description,
		        s.guid, s.value_num, s.value_denom, s.quantity_num, s.quantity_denom, s.memo, s.reconcile_state
		 FROM splits s
		 JOIN transactions t ON s.tx_guid = t.guid
		 WHERE s.account_guid = $1 AND t.book_guid = $2
		 ORDER BY t.post_date ASC, t.custom_id ASC, t.enter_date ASC
		 LIMIT $3 OFFSET $4`,
		accountGUID, bookGUID, queryLimit, queryOffset,
	)
	if err != nil {
		return nil, fmt.Errorf("query page: %w", err)
	}
	defer pageRows.Close()

	var entries []models.RegisterEntry
	runningBalance := priorBalance

	for pageRows.Next() {
		var (
			txGUID         string
			customID       string
			postDate       time.Time
			description    string
			splitGUID      string
			valueNum       int64
			valueDenom     int64
			qtyNum         int64
			qtyDenom       int64
			memo           string
			reconcileState string
		)
		if err := pageRows.Scan(&txGUID, &customID, &postDate, &description,
			&splitGUID, &valueNum, &valueDenom, &qtyNum, &qtyDenom, &memo, &reconcileState); err != nil {
			return nil, err
		}

		amount := gnc.New(qtyNum, qtyDenom)
		runningBalance = runningBalance.Add(amount)

		entry := models.RegisterEntry{
			TransactionGUID: txGUID,
			CustomID:        customID,
			PostDate:        postDate,
			Description:     description,
			Balance:         runningBalance.ToFloat64(),
			SplitMemo:       memo,
			SplitGUID:       splitGUID,
			ReconcileState:  reconcileState,
		}

		f := amount.ToFloat64()
		if accountType.IsDebitNormal() {
			if f >= 0 {
				entry.Deposit = &f
			} else {
				abs := -f
				entry.Withdrawal = &abs
			}
		} else {
			if f <= 0 {
				abs := -f
				entry.Deposit = &abs
			} else {
				entry.Withdrawal = &f
			}
		}

		transferName, transferGUID, err := s.getTransferAccount(ctx, txGUID, accountGUID)
		if err != nil {
			return nil, err
		}
		entry.TransferAccount = transferName
		entry.TransferAccountGUID = transferGUID

		entries = append(entries, entry)
	}

	endOffset := queryOffset + len(entries) - 1
	if len(entries) == 0 {
		endOffset = queryOffset
	}
	return &models.RegisterPage{
		Entries:     entries,
		HasBefore:   queryOffset > 0,
		HasAfter:    endOffset < totalCount-1,
		FirstOffset: queryOffset,
		LastOffset:  endOffset,
		TotalCount:  totalCount,
	}, nil
}

func (s *TransactionService) getTransferAccount(ctx context.Context, txGUID, excludeAccountGUID string) (string, string, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT a.guid, a.name FROM splits s JOIN accounts a ON s.account_guid = a.guid
		 WHERE s.tx_guid = $1 AND s.account_guid != $2`, txGUID, excludeAccountGUID,
	)
	if err != nil {
		return "", "", err
	}
	defer rows.Close()

	type acctInfo struct {
		guid string
		name string
	}
	var accts []acctInfo
	for rows.Next() {
		var a acctInfo
		if err := rows.Scan(&a.guid, &a.name); err != nil {
			return "", "", err
		}
		accts = append(accts, a)
	}

	if len(accts) == 1 {
		return accts[0].name, accts[0].guid, nil
	}
	return "-- Split Transaction --", "", nil
}

// ToggleSplitAcknowledge toggles a split between 'n' and 'c' states.
// It also allows going from 'y' back to 'n'. Setting to 'y' is NOT allowed here
// (must use BatchReconcileSplits via the reconcile wizard).
func (s *TransactionService) ToggleSplitAcknowledge(ctx context.Context, splitGUID, newState string) error {
	bookGUID := auth.BookGUIDFromCtx(ctx)

	// Only allow setting to 'n' or 'c' — never 'y'
	if newState != "n" && newState != "c" {
		return fmt.Errorf("invalid state for toggle: %s (only n or c allowed)", newState)
	}

	result, err := s.pool.Exec(ctx,
		`UPDATE splits SET reconcile_state = $1
		 WHERE guid = $2 AND EXISTS (
		   SELECT 1 FROM transactions t WHERE t.guid = splits.tx_guid AND t.book_guid = $3
		 )`, newState, splitGUID, bookGUID,
	)
	if err != nil {
		return fmt.Errorf("toggle reconcile state: %w", err)
	}
	if result.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// BatchReconcileSplits sets the reconcile_state to 'y' for a list of split GUIDs.
// This is used by the reconcile wizard to finalize reconciliation.
func (s *TransactionService) BatchReconcileSplits(ctx context.Context, splitGUIDs []string) error {
	bookGUID := auth.BookGUIDFromCtx(ctx)
	if len(splitGUIDs) == 0 {
		return nil
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx)

	for _, splitGUID := range splitGUIDs {
		result, err := tx.Exec(ctx,
			`UPDATE splits SET reconcile_state = 'y'
			 WHERE guid = $1 AND reconcile_state != 'y' AND EXISTS (
			   SELECT 1 FROM transactions t WHERE t.guid = splits.tx_guid AND t.book_guid = $2
			 )`, splitGUID, bookGUID,
		)
		if err != nil {
			return fmt.Errorf("update split %s: %w", splitGUID, err)
		}
		if result.RowsAffected() == 0 {
			// Already reconciled or not found — skip silently
		}
	}

	return tx.Commit(ctx)
}

// GetReconciledBalance returns the sum of all reconciled splits for an account.
func (s *TransactionService) GetReconciledBalance(ctx context.Context, accountGUID string) (float64, error) {
	bookGUID := auth.BookGUIDFromCtx(ctx)

	var balance float64
	err := s.pool.QueryRow(ctx,
		`SELECT COALESCE(SUM(s.quantity_num::float / NULLIF(s.quantity_denom, 0)), 0)
		 FROM splits s
		 JOIN transactions t ON s.tx_guid = t.guid
		 WHERE s.account_guid = $1 AND s.reconcile_state = 'y' AND t.book_guid = $2`,
		accountGUID, bookGUID,
	).Scan(&balance)
	if err != nil {
		return 0, err
	}
	return balance, nil
}

// ReconcileAccountSplits sets reconcile_state='y' for all unreconciled splits
// belonging to the given account GUIDs. This is used to reconcile an account
// and its children from the Chart of Accounts.
func (s *TransactionService) ReconcileAccountSplits(ctx context.Context, accountGUIDs []string) (int64, error) {
	bookGUID := auth.BookGUIDFromCtx(ctx)
	if len(accountGUIDs) == 0 {
		return 0, nil
	}

	// Build IN clause
	result, err := s.pool.Exec(ctx,
		`UPDATE splits SET reconcile_state = 'y'
		 WHERE account_guid = ANY($1) AND reconcile_state != 'y' AND EXISTS (
		   SELECT 1 FROM transactions t WHERE t.guid = splits.tx_guid AND t.book_guid = $2
		 )`, accountGUIDs, bookGUID,
	)
	if err != nil {
		return 0, fmt.Errorf("reconcile account splits: %w", err)
	}
	return result.RowsAffected(), nil
}

func normalizePostDate(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 11, 0, 0, 0, time.UTC)
}
