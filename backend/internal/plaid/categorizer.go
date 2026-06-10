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

// batchCategorizer is an optional fast-path: categorize many descriptions in a
// constant number of queries instead of up to two per transaction (keeps large
// syncs inside the 30s request timeout). Sync type-asserts for it.
type batchCategorizer interface {
	SuggestBatch(ctx context.Context, bookGUID string, descriptions []string) []string
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

	// Escape LIKE metacharacters for the substring pass — bank descriptions
	// like "100% JUICE" must match literally, not as wildcards.
	escaped := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(q)

	for _, attempt := range []struct {
		cond  string
		param string
	}{
		{`LOWER(TRIM(t.description)) = $2`, q},
		{`LOWER(t.description) LIKE '%' || $2 || '%' ESCAPE '\'`, escaped},
	} {
		var accountGUID string
		err := c.pool.QueryRow(ctx, fmt.Sprintf(baseSQL, attempt.cond), bookGUID, attempt.param).Scan(&accountGUID)
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

// batchSQL resolves the most recent matching counter account per input pattern
// in one round trip; %s is one of the two constant conditions below.
const batchSQL = `
	SELECT DISTINCT ON (q.orig) q.orig, s.account_guid
	FROM unnest($2::text[], $3::int[]) AS q(pat, orig)
	JOIN transactions t ON t.book_guid = $1 AND %s
	JOIN splits s ON s.tx_guid = t.guid
	JOIN accounts a ON a.guid = s.account_guid AND a.book_guid = $1
	WHERE a.account_type NOT IN ('BANK', 'ASSET', 'CASH', 'ROOT')
	ORDER BY q.orig, t.post_date DESC`

// SuggestBatch resolves all descriptions in two queries total — an exact pass,
// then a substring pass over whatever the first missed — preserving Suggest's
// exact-beats-substring priority. On a query error it falls back to the
// per-row path so a batching problem can never disable categorization.
func (c *HistoryCategorizer) SuggestBatch(ctx context.Context, bookGUID string, descriptions []string) []string {
	out := make([]string, len(descriptions))

	var pats []string
	var idxs []int32
	for i, d := range descriptions {
		if q := strings.ToLower(strings.TrimSpace(d)); q != "" {
			pats = append(pats, q)
			idxs = append(idxs, int32(i))
		}
	}
	if len(pats) == 0 {
		return out
	}
	if !c.runBatch(ctx, bookGUID, `LOWER(TRIM(t.description)) = q.pat`, pats, idxs, out) {
		return c.suggestPerRow(ctx, bookGUID, descriptions)
	}

	esc := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	pats, idxs = nil, nil
	for i, d := range descriptions {
		if out[i] != "" {
			continue
		}
		if q := strings.ToLower(strings.TrimSpace(d)); q != "" {
			pats = append(pats, esc.Replace(q))
			idxs = append(idxs, int32(i))
		}
	}
	if len(pats) > 0 {
		if !c.runBatch(ctx, bookGUID, `LOWER(t.description) LIKE '%' || q.pat || '%' ESCAPE '\'`, pats, idxs, out) {
			return c.suggestPerRow(ctx, bookGUID, descriptions)
		}
	}
	return out
}

func (c *HistoryCategorizer) runBatch(ctx context.Context, bookGUID, cond string, pats []string, idxs []int32, out []string) bool {
	rows, err := c.pool.Query(ctx, fmt.Sprintf(batchSQL, cond), bookGUID, pats, idxs)
	if err != nil {
		log.Printf("categorizer batch: %v", err)
		return false
	}
	defer rows.Close()
	for rows.Next() {
		var orig int32
		var guid string
		if err := rows.Scan(&orig, &guid); err != nil {
			log.Printf("categorizer batch scan: %v", err)
			return false
		}
		out[orig] = guid
	}
	if err := rows.Err(); err != nil {
		log.Printf("categorizer batch rows: %v", err)
		return false
	}
	return true
}

func (c *HistoryCategorizer) suggestPerRow(ctx context.Context, bookGUID string, descriptions []string) []string {
	out := make([]string, len(descriptions))
	for i, d := range descriptions {
		if guid, ok := c.Suggest(ctx, bookGUID, PlaidTxn{Description: d}); ok {
			out[i] = guid
		}
	}
	return out
}
