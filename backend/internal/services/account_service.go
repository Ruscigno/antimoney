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
	"github.com/user/antimoney/internal/models"
)

// AccountService handles CRUD and tree operations for accounts.
type AccountService struct {
	pool *pgxpool.Pool
}

func NewAccountService(pool *pgxpool.Pool) *AccountService {
	return &AccountService{pool: pool}
}

type CreateAccountRequest struct {
	Name        string             `json:"name"`
	AccountType models.AccountType `json:"account_type"`
	ParentGUID  *string            `json:"parent_guid"`
	Placeholder bool               `json:"placeholder"`
	Description string             `json:"description"`
}

type UpdateAccountRequest struct {
	Name        *string             `json:"name"`
	Description *string             `json:"description"`
	Placeholder *bool               `json:"placeholder"`
	AccountType *models.AccountType `json:"account_type"`
	ParentGUID  *string             `json:"parent_guid"`
	Version     int                 `json:"version"` // Required for OCC
}

func (s *AccountService) CreateAccount(ctx context.Context, req CreateAccountRequest) (*models.Account, error) {
	bookGUID := auth.BookGUIDFromCtx(ctx)
	guid := uuid.New().String()
	now := time.Now().UTC()
	account := &models.Account{
		GUID:        guid,
		Name:        req.Name,
		AccountType: req.AccountType,
		ParentGUID:  req.ParentGUID,
		BookGUID:    &bookGUID,
		Placeholder: req.Placeholder,
		Description: req.Description,
		Metadata:    json.RawMessage("{}"),
		Version:     1,
		CreatedAt:   now,
		UpdatedAt:   now,
	}

	_, err := s.pool.Exec(ctx,
		`INSERT INTO accounts (guid, name, account_type, parent_guid, book_guid, placeholder, description, metadata, version, created_at, updated_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)`,
		account.GUID, account.Name, account.AccountType,
		account.ParentGUID, account.BookGUID, account.Placeholder, account.Description, account.Metadata,
		account.Version, account.CreatedAt, account.UpdatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("insert account: %w", err)
	}

	return account, nil
}

func (s *AccountService) GetAccount(ctx context.Context, guid string) (*models.Account, error) {
	bookGUID := auth.BookGUIDFromCtx(ctx)
	acc := &models.Account{}
	err := s.pool.QueryRow(ctx,
		`SELECT guid, name, account_type, parent_guid,
		        placeholder, description, metadata, version, created_at, updated_at
		 FROM accounts WHERE guid = $1 AND book_guid = $2`, guid, bookGUID,
	).Scan(&acc.GUID, &acc.Name, &acc.AccountType,
		&acc.ParentGUID, &acc.Placeholder, &acc.Description, &acc.Metadata,
		&acc.Version, &acc.CreatedAt, &acc.UpdatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return acc, nil
}

func (s *AccountService) UpdateAccount(ctx context.Context, guid string, req UpdateAccountRequest) (*models.Account, error) {
	bookGUID := auth.BookGUIDFromCtx(ctx)
	now := time.Now().UTC()

	result, err := s.pool.Exec(ctx,
		`UPDATE accounts SET
			name = COALESCE($2, name),
			description = COALESCE($3, description),
			placeholder = COALESCE($4, placeholder),
			account_type = COALESCE($8, account_type),
			parent_guid = COALESCE($9, parent_guid),
			version = version + 1,
			updated_at = $5
		 WHERE guid = $1 AND version = $6 AND book_guid = $7`,
		guid, req.Name, req.Description, req.Placeholder, now, req.Version, bookGUID,
		req.AccountType, req.ParentGUID,
	)
	if err != nil {
		return nil, fmt.Errorf("update account: %w", err)
	}
	if result.RowsAffected() == 0 {
		var exists bool
		s.pool.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM accounts WHERE guid = $1 AND book_guid = $2)", guid, bookGUID).Scan(&exists)
		if !exists {
			return nil, ErrNotFound
		}
		return nil, ErrVersionConflict
	}

	return s.GetAccount(ctx, guid)
}

func (s *AccountService) DeleteAccount(ctx context.Context, guid string) error {
	bookGUID := auth.BookGUIDFromCtx(ctx)

	var splitCount int
	s.pool.QueryRow(ctx, "SELECT COUNT(*) FROM splits WHERE account_guid = $1", guid).Scan(&splitCount)
	if splitCount > 0 {
		return fmt.Errorf("cannot delete account with %d existing splits", splitCount)
	}

	var childCount int
	s.pool.QueryRow(ctx, "SELECT COUNT(*) FROM accounts WHERE parent_guid = $1", guid).Scan(&childCount)
	if childCount > 0 {
		return fmt.Errorf("cannot delete account with %d child accounts", childCount)
	}

	result, err := s.pool.Exec(ctx, "DELETE FROM accounts WHERE guid = $1 AND book_guid = $2", guid, bookGUID)
	if err != nil {
		return err
	}
	if result.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// ListAccountsTree returns all accounts for the user's book.
func (s *AccountService) ListAccountsTree(ctx context.Context) ([]models.Account, error) {
	bookGUID := auth.BookGUIDFromCtx(ctx)
	rows, err := s.pool.Query(ctx,
		`SELECT a.guid, a.name, a.account_type,
		        a.parent_guid, a.placeholder, a.description, a.metadata, a.version,
		        a.created_at, a.updated_at,
		        COALESCE(SUM(s.quantity_num::float / NULLIF(s.quantity_denom, 0)), 0) as balance,
		        COALESCE(SUM(CASE WHEN s.reconcile_state = 'y' THEN s.quantity_num::float / NULLIF(s.quantity_denom, 0) ELSE 0 END), 0) as reconciled_balance,
		        MAX(CASE WHEN s.reconcile_state = 'y' THEN t.post_date ELSE NULL END) as last_reconciled
		 FROM accounts a
		 LEFT JOIN splits s ON a.guid = s.account_guid
		 LEFT JOIN transactions t ON s.tx_guid = t.guid
		 WHERE a.book_guid = $1
		 GROUP BY a.guid
		 ORDER BY a.account_type, a.name`, bookGUID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	accounts := make([]models.Account, 0)
	for rows.Next() {
		var acc models.Account
		if err := rows.Scan(&acc.GUID, &acc.Name, &acc.AccountType,
			&acc.ParentGUID, &acc.Placeholder, &acc.Description,
			&acc.Metadata, &acc.Version, &acc.CreatedAt, &acc.UpdatedAt,
			&acc.Balance, &acc.ReconciledBalance, &acc.LastReconciled); err != nil {
			return nil, err
		}
		accounts = append(accounts, acc)
	}

	return accounts, nil
}
