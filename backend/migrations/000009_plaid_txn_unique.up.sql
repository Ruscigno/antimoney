-- Real idempotency guarantee for Plaid imports: the application-level dedupe
-- (SELECT-then-INSERT in PlaidService) is racy under concurrent imports; this
-- partial unique index is the DB backstop that makes a duplicate import
-- impossible regardless of timing.

-- Defensive dedup first (mirrors migration 000008): keep the oldest transaction
-- per (book_guid, plaid transaction_id); splits cascade via ON DELETE CASCADE.
-- No-op on databases without duplicates.
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
