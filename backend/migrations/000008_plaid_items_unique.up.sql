-- Prevent duplicate Plaid item rows for the same book (e.g. a re-link after a
-- network error or re-auth). The Exchange path upserts on this key.

-- Audit trail for every destructive data fix performed by plaid migrations:
-- rows are copied here before being deleted/stripped, so nothing is lost
-- irrecoverably. (Amending this executed file is safe: rows matched by the
-- statements below cannot exist on a database that ran the original version —
-- the plaid feature ships in the same release as these migrations, so the
-- dedups were provably no-ops everywhere they already ran.)
CREATE TABLE IF NOT EXISTS plaid_migration_audit (
    id          BIGSERIAL   PRIMARY KEY,
    migration   TEXT        NOT NULL,
    payload     JSONB       NOT NULL,
    backed_up_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Defensive dedup: an existing deployment may already hold duplicate
-- (book_guid, item_id) rows from before the upsert guard. Without this, the
-- ADD CONSTRAINT below would fail and abort startup (golang-migrate). Keep ONE
-- row per key (min(ctid) picks an arbitrary survivor — ctid is a physical
-- position, not insertion order); a no-op on a fresh database.
INSERT INTO plaid_migration_audit (migration, payload)
SELECT '000008', to_jsonb(p.*)
FROM plaid_items p
WHERE p.ctid NOT IN (
    SELECT min(ctid)
    FROM plaid_items
    GROUP BY book_guid, item_id
);

DELETE FROM plaid_items
WHERE ctid NOT IN (
    SELECT min(ctid)
    FROM plaid_items
    GROUP BY book_guid, item_id
);

ALTER TABLE plaid_items
    ADD CONSTRAINT plaid_items_book_item_unique UNIQUE (book_guid, item_id);
