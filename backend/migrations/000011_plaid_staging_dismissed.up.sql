-- Permanent dismissal for staged Plaid transactions (review round 6, #4):
-- without it, a suggestion the user never wants to import reappears on every
-- sync forever. Dismissed rows are excluded from suggestions but kept for the
-- dedupe/pending-correlation logic; disconnect still cascades them away.
ALTER TABLE plaid_staged_transactions
    ADD COLUMN dismissed BOOLEAN NOT NULL DEFAULT false;

-- Amendments to migration 000010, folded here instead of editing it in place
-- (databases that already ran 000010 would otherwise silently drift):
-- 1. Staged amounts must have a positive denominator (gnc invariant).
ALTER TABLE plaid_staged_transactions
    ADD CONSTRAINT plaid_staged_amount_denom_positive CHECK (amount_denom > 0);

-- 2. Re-run 000010's defensive 1:1 cleanup WITH the OCC version bump it was
-- missing, recording the stripped links in the audit table first. Idempotent:
-- a no-op unless duplicates appeared in the meantime. (Rows stripped by the
-- original 000010 cannot be identified retroactively; their missed +1 version
-- bump is benign — OCC still functions, one stale write window was simply not
-- narrowed.)
INSERT INTO plaid_migration_audit (migration, payload)
SELECT '000011', jsonb_build_object('account_guid', a.guid, 'plaid', a.metadata->'plaid')
FROM accounts a
WHERE a.metadata->'plaid'->>'account_id' IS NOT NULL
  AND a.ctid NOT IN (
    SELECT min(ctid)
    FROM accounts
    WHERE metadata->'plaid'->>'account_id' IS NOT NULL
    GROUP BY book_guid, metadata->'plaid'->>'account_id'
);

UPDATE accounts
SET metadata   = metadata - 'plaid',
    updated_at = NOW(),
    version    = version + 1
WHERE metadata->'plaid'->>'account_id' IS NOT NULL
  AND ctid NOT IN (
    SELECT min(ctid)
    FROM accounts
    WHERE metadata->'plaid'->>'account_id' IS NOT NULL
    GROUP BY book_guid, metadata->'plaid'->>'account_id'
);
