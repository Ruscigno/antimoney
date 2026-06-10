-- Staging for synced Plaid transactions (review round 5, findings #1/#2/#5).
--
-- The /transactions/sync cursor advances at fetch time; without durable
-- staging, suggestions lived only in the HTTP response and React state, so a
-- closed tab or dropped response lost those transactions forever. Sync now
-- stages every fetched transaction BEFORE the cursor is persisted; suggestions
-- are rebuilt from this table on every sync, and rows are deleted on import.
-- It also lets Import read date/description/amount server-side instead of
-- trusting the client, and gives `modified`/`removed` deltas a place to apply.
CREATE TABLE plaid_staged_transactions (
    book_guid              UUID        NOT NULL REFERENCES books(guid) ON DELETE CASCADE,
    item_guid              UUID        NOT NULL REFERENCES plaid_items(guid) ON DELETE CASCADE,
    transaction_id         TEXT        NOT NULL,
    pending_transaction_id TEXT,
    plaid_account_id       TEXT        NOT NULL,
    post_date              DATE        NOT NULL,
    description            TEXT        NOT NULL DEFAULT '',
    amount_num             BIGINT      NOT NULL,
    amount_denom           BIGINT      NOT NULL,
    pending                BOOLEAN     NOT NULL DEFAULT false,
    created_at             TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (book_guid, transaction_id)
);

CREATE INDEX idx_plaid_staged_item ON plaid_staged_transactions(item_guid);

-- DB backstop for the 1:1 link invariant (finding #7): two concurrent
-- LinkAccounts calls could both pass the application-level check. Defensive
-- cleanup first: keep the oldest holder of each plaid account_id per book and
-- strip the link from any later duplicates (no-op on healthy databases).
UPDATE accounts SET metadata = metadata - 'plaid'
WHERE metadata->'plaid'->>'account_id' IS NOT NULL
  AND ctid NOT IN (
    SELECT min(ctid)
    FROM accounts
    WHERE metadata->'plaid'->>'account_id' IS NOT NULL
    GROUP BY book_guid, metadata->'plaid'->>'account_id'
);

CREATE UNIQUE INDEX idx_accounts_plaid_account
ON accounts (book_guid, (metadata->'plaid'->>'account_id'))
WHERE metadata->'plaid'->>'account_id' IS NOT NULL;
