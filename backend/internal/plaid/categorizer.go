package plaid

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Categorizer suggests a counter (category) account for a Plaid transaction.
type Categorizer interface {
	Suggest(ctx context.Context, bookGUID string, txn PlaidTxn) (accountGUID string, ok bool)
}

// HistoryCategorizer finds the most recent prior transaction whose description
// contains the incoming description (case-insensitive) and returns the
// non-asset/bank/cash split account.
type HistoryCategorizer struct {
	pool *pgxpool.Pool
}

func NewHistoryCategorizer(pool *pgxpool.Pool) *HistoryCategorizer {
	return &HistoryCategorizer{pool: pool}
}

func (c *HistoryCategorizer) Suggest(ctx context.Context, bookGUID string, txn PlaidTxn) (string, bool) {
	q := strings.ToLower(strings.TrimSpace(txn.Description))
	if q == "" {
		return "", false
	}

	// Spec §7: a normalized exact match takes priority over a substring match,
	// even when a substring candidate is more recent. Both conditions are
	// constant SQL fragments — only $2 carries user data.
	const baseSQL = `
		SELECT s.account_guid
		FROM transactions t
		JOIN splits s ON s.tx_guid = t.guid
		JOIN accounts a ON a.guid = s.account_guid AND a.book_guid = $1
		WHERE t.book_guid = $1
		  AND %s
		  AND a.account_type NOT IN ('BANK', 'ASSET', 'CASH', 'ROOT')
		ORDER BY t.post_date DESC
		LIMIT 1`

	for _, cond := range []string{
		`LOWER(TRIM(t.description)) = $2`,
		`LOWER(t.description) LIKE '%' || $2 || '%'`,
	} {
		var accountGUID string
		err := c.pool.QueryRow(ctx, fmt.Sprintf(baseSQL, cond), bookGUID, q).Scan(&accountGUID)
		if err == nil {
			return accountGUID, true
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			log.Printf("categorizer: unexpected DB error: %v", err)
			return "", false
		}
	}
	return "", false
}
