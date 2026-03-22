package services

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
	CurrencyGUID string               `json:"currency_guid"`
	PostDate     time.Time            `json:"post_date"`
	Description  string               `json:"description"`
	Splits       []CreateSplitRequest `json:"splits"`
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
		if sp.ValueNum != 0 {
			validSplits = append(validSplits, sp)
			values = append(values, gnc.New(sp.ValueNum, sp.ValueDenom))
		}
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
			"SELECT guid FROM accounts WHERE book_guid = $1 AND name = 'Imbalance' AND commodity_guid = $2 LIMIT 1",
			bookGUID, req.CurrencyGUID,
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
					`INSERT INTO accounts (guid, name, account_type, commodity_guid, commodity_scu, parent_guid, book_guid, placeholder, description, metadata, version, created_at, updated_at)
					 VALUES ($1, 'Imbalance', 'EQUITY', $2, 100, $3, $4, false, 'Automatically created imbalance account', '{}', 1, $5, $5)`,
					imbalanceAcctGUID, req.CurrencyGUID, rootAcctGUID, bookGUID, now,
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
		`INSERT INTO transactions (guid, currency_guid, book_guid, post_date, enter_date, description, metadata, version, created_at, updated_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, 1, $8, $8)
		 RETURNING guid`,
		txGUID, req.CurrencyGUID, bookGUID, postDate, now, req.Description, json.RawMessage("{}"), now,
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
		GUID:         txGUID,
		CurrencyGUID: req.CurrencyGUID,
		BookGUID:     &bk,
		PostDate:     postDate,
		EnterDate:    now,
		Description:  req.Description,
		Metadata:     json.RawMessage("{}"),
		Version:      1,
		CreatedAt:    now,
		UpdatedAt:    now,
		Splits:       resultSplits,
	}, nil
}

// GetTransaction retrieves a transaction with all its splits, scoped to user's book.
func (s *TransactionService) GetTransaction(ctx context.Context, guid string) (*models.Transaction, error) {
	bookGUID := auth.BookGUIDFromCtx(ctx)
	txn := &models.Transaction{}
	err := s.pool.QueryRow(ctx,
		`SELECT guid, currency_guid, post_date, enter_date, description, metadata, version, created_at, updated_at
		 FROM transactions WHERE guid = $1 AND book_guid = $2`, guid, bookGUID,
	).Scan(&txn.GUID, &txn.CurrencyGUID, &txn.PostDate, &txn.EnterDate,
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
		`SELECT guid, currency_guid, post_date, enter_date, description, metadata, version, created_at, updated_at
		 FROM transactions WHERE book_guid = $3
		 ORDER BY post_date DESC LIMIT $1 OFFSET $2`, limit, offset, bookGUID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var txns []models.Transaction
	for rows.Next() {
		var txn models.Transaction
		if err := rows.Scan(&txn.GUID, &txn.CurrencyGUID, &txn.PostDate, &txn.EnterDate,
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
		`SELECT t.guid, t.post_date, t.description,
		        s.guid, s.value_num, s.value_denom, s.quantity_num, s.quantity_denom, s.memo, s.reconcile_state
		 FROM splits s
		 JOIN transactions t ON s.tx_guid = t.guid
		 WHERE s.account_guid = $1
		 ORDER BY t.post_date ASC, t.enter_date ASC`, accountGUID,
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
		if err := rows.Scan(&txGUID, &postDate, &description,
			&splitGUID, &valueNum, &valueDenom, &qtyNum, &qtyDenom, &memo, &reconcileState); err != nil {
			return nil, err
		}

		amount := gnc.New(qtyNum, qtyDenom)
		runningBalance = runningBalance.Add(amount)

		entry := models.RegisterEntry{
			TransactionGUID: txGUID,
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

func normalizePostDate(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 11, 0, 0, 0, time.UTC)
}
