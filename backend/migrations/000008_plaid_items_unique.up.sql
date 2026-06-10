-- Prevent duplicate Plaid item rows for the same book (e.g. a re-link after a
-- network error or re-auth). The Exchange path upserts on this key.

-- Defensive dedup first: an existing deployment may already hold duplicate
-- (book_guid, item_id) rows from before the upsert guard. Without this, the
-- ADD CONSTRAINT below would fail and abort startup (golang-migrate). Keep the
-- oldest row per key (min ctid); a no-op on a fresh database. (min(tid) needs
-- PostgreSQL 14+, which this project targets — postgres:16.)
DELETE FROM plaid_items
WHERE ctid NOT IN (
    SELECT min(ctid)
    FROM plaid_items
    GROUP BY book_guid, item_id
);

ALTER TABLE plaid_items
    ADD CONSTRAINT plaid_items_book_item_unique UNIQUE (book_guid, item_id);
