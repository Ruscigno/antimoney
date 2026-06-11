CREATE TABLE plaid_items (
    guid                     UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    book_guid                UUID        NOT NULL REFERENCES books(guid) ON DELETE CASCADE,
    item_id                  TEXT        NOT NULL,
    institution_name         TEXT        NOT NULL DEFAULT '',
    access_token_ciphertext  BYTEA       NOT NULL,
    access_token_nonce       BYTEA       NOT NULL,
    sync_cursor              TEXT,
    import_pending           BOOLEAN     NOT NULL DEFAULT false,
    last_synced_at           TIMESTAMPTZ,
    version                  INT         NOT NULL DEFAULT 1,
    created_at               TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at               TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_plaid_items_book_guid ON plaid_items(book_guid);
