package services

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/user/antimoney/internal/auth"
	"github.com/user/antimoney/internal/models"
)

var ErrSnapshotNotFound = errors.New("snapshot not found")
var ErrSnapshotConfigNotFound = errors.New("snapshot config not found")

type SnapshotService struct {
	pool *pgxpool.Pool
}

func NewSnapshotService(pool *pgxpool.Pool) *SnapshotService {
	return &SnapshotService{pool: pool}
}

// --- Config methods (scoped to the requesting book via context) ---

func (s *SnapshotService) GetConfig(ctx context.Context) (*models.SnapshotConfig, error) {
	bookGUID := auth.BookGUIDFromCtx(ctx)
	var cfg models.SnapshotConfig
	err := s.pool.QueryRow(ctx,
		`SELECT id, book_guid, frequency_hours, ttl_hours, active_mode, created_at, updated_at
		 FROM snapshot_configs WHERE book_guid = $1`, bookGUID,
	).Scan(&cfg.ID, &cfg.BookGUID, &cfg.FrequencyHours, &cfg.TTLHours, &cfg.ActiveMode, &cfg.CreatedAt, &cfg.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrSnapshotConfigNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get snapshot config: %w", err)
	}
	return &cfg, nil
}

func (s *SnapshotService) UpsertConfig(ctx context.Context, frequencyHours, ttlHours int, activeMode bool) (*models.SnapshotConfig, error) {
	bookGUID := auth.BookGUIDFromCtx(ctx)
	var cfg models.SnapshotConfig
	err := s.pool.QueryRow(ctx,
		`INSERT INTO snapshot_configs (book_guid, frequency_hours, ttl_hours, active_mode)
		 VALUES ($1, $2, $3, $4)
		 ON CONFLICT (book_guid) DO UPDATE
		   SET frequency_hours = EXCLUDED.frequency_hours,
		       ttl_hours        = EXCLUDED.ttl_hours,
		       active_mode      = EXCLUDED.active_mode,
		       updated_at       = NOW()
		 RETURNING id, book_guid, frequency_hours, ttl_hours, active_mode, created_at, updated_at`,
		bookGUID, frequencyHours, ttlHours, activeMode,
	).Scan(&cfg.ID, &cfg.BookGUID, &cfg.FrequencyHours, &cfg.TTLHours, &cfg.ActiveMode, &cfg.CreatedAt, &cfg.UpdatedAt)
	if err != nil {
		return nil, fmt.Errorf("upsert snapshot config: %w", err)
	}
	return &cfg, nil
}

// --- Snapshot methods (scoped to the requesting book via context) ---

func (s *SnapshotService) ListSnapshots(ctx context.Context) ([]models.SnapshotSummary, error) {
	bookGUID := auth.BookGUIDFromCtx(ctx)
	rows, err := s.pool.Query(ctx,
		`SELECT id, book_guid, label, trigger, created_at
		 FROM snapshots WHERE book_guid = $1
		 ORDER BY created_at DESC`, bookGUID)
	if err != nil {
		return nil, fmt.Errorf("list snapshots: %w", err)
	}
	defer rows.Close()

	var result []models.SnapshotSummary
	for rows.Next() {
		var ss models.SnapshotSummary
		if err := rows.Scan(&ss.ID, &ss.BookGUID, &ss.Label, &ss.Trigger, &ss.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan snapshot: %w", err)
		}
		result = append(result, ss)
	}
	if result == nil {
		result = []models.SnapshotSummary{}
	}
	return result, nil
}

// GetSnapshot returns the full snapshot including its data payload.
// Returns ErrSnapshotNotFound if the snapshot doesn't exist or belongs to a different book.
func (s *SnapshotService) GetSnapshot(ctx context.Context, id string) (*models.Snapshot, error) {
	bookGUID := auth.BookGUIDFromCtx(ctx)
	var snap models.Snapshot
	var dataRaw []byte
	err := s.pool.QueryRow(ctx,
		`SELECT id, book_guid, label, trigger, data, created_at
		 FROM snapshots WHERE id = $1 AND book_guid = $2`,
		id, bookGUID,
	).Scan(&snap.ID, &snap.BookGUID, &snap.Label, &snap.Trigger, &dataRaw, &snap.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrSnapshotNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get snapshot: %w", err)
	}
	snap.Data = json.RawMessage(dataRaw)
	return &snap, nil
}

// TakeSnapshot captures the current book state and stores it as a new snapshot.
func (s *SnapshotService) TakeSnapshot(ctx context.Context, label string, trigger models.SnapshotTrigger) (*models.SnapshotSummary, error) {
	bookGUID := auth.BookGUIDFromCtx(ctx)
	return takeSnapshotForBook(ctx, s.pool, bookGUID, label, trigger)
}

// DeleteSnapshot deletes a snapshot. Returns ErrSnapshotNotFound if it doesn't belong to the book.
func (s *SnapshotService) DeleteSnapshot(ctx context.Context, id string) error {
	bookGUID := auth.BookGUIDFromCtx(ctx)
	tag, err := s.pool.Exec(ctx,
		`DELETE FROM snapshots WHERE id = $1 AND book_guid = $2`, id, bookGUID)
	if err != nil {
		return fmt.Errorf("delete snapshot: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrSnapshotNotFound
	}
	return nil
}

// PurgeExpiredSnapshots deletes snapshots that have exceeded their TTL across all books.
func (s *SnapshotService) PurgeExpiredSnapshots(ctx context.Context) error {
	_, err := s.pool.Exec(ctx,
		`DELETE FROM snapshots sn
		 USING snapshot_configs sc
		 WHERE sn.book_guid = sc.book_guid
		   AND sc.ttl_hours > 0
		   AND sn.created_at < NOW() - (sc.ttl_hours * INTERVAL '1 hour')`)
	if err != nil {
		return fmt.Errorf("purge expired snapshots: %w", err)
	}
	return nil
}

// --- Scheduler-only methods (no book_guid in context; operate across all books) ---

// GetAllActiveConfigs returns all snapshot configs for the scheduler to iterate.
func (s *SnapshotService) GetAllActiveConfigs(ctx context.Context) ([]models.SnapshotConfig, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT id, book_guid, frequency_hours, ttl_hours, active_mode, created_at, updated_at
		 FROM snapshot_configs
		 WHERE frequency_hours > 0 OR active_mode = TRUE`)
	if err != nil {
		return nil, fmt.Errorf("get all active configs: %w", err)
	}
	defer rows.Close()

	var configs []models.SnapshotConfig
	for rows.Next() {
		var cfg models.SnapshotConfig
		if err := rows.Scan(&cfg.ID, &cfg.BookGUID, &cfg.FrequencyHours, &cfg.TTLHours, &cfg.ActiveMode, &cfg.CreatedAt, &cfg.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan config: %w", err)
		}
		configs = append(configs, cfg)
	}
	return configs, nil
}

// GetLastSnapshotTime returns the creation time of the most recent snapshot for a book.
func (s *SnapshotService) GetLastSnapshotTime(ctx context.Context, bookGUID string) (*time.Time, error) {
	var t *time.Time
	err := s.pool.QueryRow(ctx,
		`SELECT MAX(created_at) FROM snapshots WHERE book_guid = $1`, bookGUID,
	).Scan(&t)
	if err != nil {
		return nil, fmt.Errorf("get last snapshot time: %w", err)
	}
	return t, nil
}

// GetLastDataChangeTime returns the latest updated_at across transactions and accounts for a book.
func (s *SnapshotService) GetLastDataChangeTime(ctx context.Context, bookGUID string) (*time.Time, error) {
	var t *time.Time
	err := s.pool.QueryRow(ctx,
		`SELECT GREATEST(
		     (SELECT MAX(updated_at) FROM transactions WHERE book_guid = $1),
		     (SELECT MAX(updated_at) FROM accounts    WHERE book_guid = $1)
		 )`, bookGUID,
	).Scan(&t)
	if err != nil {
		return nil, fmt.Errorf("get last data change time: %w", err)
	}
	return t, nil
}

// TakeSnapshotForBook is the scheduler entry point — does not rely on auth context.
func (s *SnapshotService) TakeSnapshotForBook(ctx context.Context, bookGUID string, label string, trigger models.SnapshotTrigger) error {
	_, err := takeSnapshotForBook(ctx, s.pool, bookGUID, label, trigger)
	return err
}

// takeSnapshotForBook is the internal implementation used by both TakeSnapshot and TakeSnapshotForBook.
func takeSnapshotForBook(ctx context.Context, pool *pgxpool.Pool, bookGUID string, label string, trigger models.SnapshotTrigger) (*models.SnapshotSummary, error) {
	// Build export payload by querying the same way as handleExport.
	data, err := buildExportData(ctx, pool, bookGUID)
	if err != nil {
		return nil, fmt.Errorf("build export data: %w", err)
	}

	raw, err := json.Marshal(data)
	if err != nil {
		return nil, fmt.Errorf("marshal snapshot data: %w", err)
	}

	var ss models.SnapshotSummary
	err = pool.QueryRow(ctx,
		`INSERT INTO snapshots (book_guid, label, trigger, data)
		 VALUES ($1, $2, $3, $4)
		 RETURNING id, book_guid, label, trigger, created_at`,
		bookGUID, label, string(trigger), raw,
	).Scan(&ss.ID, &ss.BookGUID, &ss.Label, &ss.Trigger, &ss.CreatedAt)
	if err != nil {
		return nil, fmt.Errorf("insert snapshot: %w", err)
	}
	return &ss, nil
}

// buildExportData collects all accounts and transactions for a book.
// Mirrors the query logic in handleExport without requiring an HTTP context.
func buildExportData(ctx context.Context, pool *pgxpool.Pool, bookGUID string) (exportData, error) {
	var data exportData

	rows, err := pool.Query(ctx,
		`SELECT guid, name, account_type, parent_guid, placeholder, description
		 FROM accounts WHERE book_guid = $1
		 ORDER BY parent_guid ASC NULLS FIRST`, bookGUID)
	if err != nil {
		return data, fmt.Errorf("query accounts: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var acc exportAccount
		if err := rows.Scan(&acc.GUID, &acc.Name, &acc.AccountType, &acc.ParentGUID, &acc.Placeholder, &acc.Description); err != nil {
			return data, fmt.Errorf("scan account: %w", err)
		}
		data.Accounts = append(data.Accounts, acc)
	}
	rows.Close()

	txRows, err := pool.Query(ctx,
		`SELECT guid, CAST(post_date AS varchar), CAST(enter_date AS varchar), description
		 FROM transactions WHERE book_guid = $1`, bookGUID)
	if err != nil {
		return data, fmt.Errorf("query transactions: %w", err)
	}
	defer txRows.Close()
	for txRows.Next() {
		var tx exportTransaction
		if err := txRows.Scan(&tx.GUID, &tx.PostDate, &tx.EnterDate, &tx.Description); err != nil {
			return data, fmt.Errorf("scan transaction: %w", err)
		}

		spRows, err := pool.Query(ctx,
			`SELECT guid, account_guid, memo, value_num, value_denom, quantity_num, quantity_denom, reconcile_state
			 FROM splits WHERE tx_guid = $1`, tx.GUID)
		if err == nil {
			for spRows.Next() {
				var s exportSplit
				if err := spRows.Scan(&s.GUID, &s.AccountGUID, &s.Memo, &s.ValueNum, &s.ValueDenom, &s.QuantityNum, &s.QuantityDenom, &s.ReconcileState); err == nil {
					tx.Splits = append(tx.Splits, s)
				}
			}
			spRows.Close()
		}
		data.Transactions = append(data.Transactions, tx)
	}
	txRows.Close()

	return data, nil
}

// Local aliases to avoid importing the handlers package (would create a cycle).
type exportData struct {
	Accounts     []exportAccount     `json:"accounts"`
	Transactions []exportTransaction `json:"transactions"`
}

type exportAccount struct {
	GUID        string  `json:"guid"`
	Name        string  `json:"name"`
	AccountType string  `json:"account_type"`
	ParentGUID  *string `json:"parent_guid"`
	Placeholder bool    `json:"placeholder"`
	Description string  `json:"description"`
}

type exportTransaction struct {
	GUID        string        `json:"guid"`
	PostDate    string        `json:"post_date"`
	EnterDate   string        `json:"enter_date"`
	Description string        `json:"description"`
	Splits      []exportSplit `json:"splits"`
}

type exportSplit struct {
	GUID           string `json:"guid"`
	AccountGUID    string `json:"account_guid"`
	Memo           string `json:"memo"`
	ValueNum       int64  `json:"value_num"`
	ValueDenom     int64  `json:"value_denom"`
	QuantityNum    int64  `json:"quantity_num"`
	QuantityDenom  int64  `json:"quantity_denom"`
	ReconcileState string `json:"reconcile_state"`
}
