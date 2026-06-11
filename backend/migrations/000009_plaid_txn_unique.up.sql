-- Real idempotency guarantee for Plaid imports: the application-level dedupe
-- (SELECT-then-INSERT in PlaidService) is racy under concurrent imports; this
-- partial unique index is the DB backstop that makes a duplicate import
-- impossible regardless of timing.

-- Defensive dedup with audit trail: duplicates (which can only exist from the
-- pre-backstop race — the plaid feature ships with these migrations, so this
-- was a provable no-op everywhere the original version ran) are copied to
-- plaid_migration_audit (created in 000008) before deletion. min(ctid) picks
-- an arbitrary survivor; splits cascade via ON DELETE CASCADE.
INSERT INTO plaid_migration_audit (migration, payload)
SELECT '000009', to_jsonb(t.*)
FROM transactions t
WHERE t.metadata->'plaid'->>'transaction_id' IS NOT NULL
  AND t.ctid NOT IN (
    SELECT min(ctid)
    FROM transactions
    WHERE metadata->'plaid'->>'transaction_id' IS NOT NULL
    GROUP BY book_guid, metadata->'plaid'->>'transaction_id'
);

DELETE FROM transactions
WHERE metadata->'plaid'->>'transaction_id' IS NOT NULL
  AND ctid NOT IN (
    SELECT min(ctid)
    FROM transactions
    WHERE metadata->'plaid'->>'transaction_id' IS NOT NULL
    GROUP BY book_guid, metadata->'plaid'->>'transaction_id'
);

CREATE UNIQUE INDEX idx_transactions_plaid_txn
ON transactions (book_guid, (metadata->'plaid'->>'transaction_id'))
WHERE metadata->'plaid'->>'transaction_id' IS NOT NULL;
