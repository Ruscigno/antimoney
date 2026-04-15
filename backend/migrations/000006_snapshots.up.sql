-- snapshot_configs: one row per book, stores the user's scheduling preferences.
-- UNIQUE on book_guid enforces the "only one schedule" rule.
CREATE TABLE snapshot_configs (
    id              UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    book_guid       UUID        NOT NULL UNIQUE REFERENCES books(guid) ON DELETE CASCADE,
    frequency_hours INT         NOT NULL DEFAULT 0,    -- 0 = scheduled snapshots disabled
    ttl_hours       INT         NOT NULL DEFAULT 0,    -- 0 = keep forever
    active_mode     BOOLEAN     NOT NULL DEFAULT FALSE, -- true = snapshot every 5 min while active
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- snapshots: one row per point-in-time capture of a book's full data.
-- data stores the same JSON shape as the export endpoint (accounts + transactions).
CREATE TABLE snapshots (
    id          UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    book_guid   UUID        NOT NULL REFERENCES books(guid) ON DELETE CASCADE,
    label       TEXT        NOT NULL DEFAULT '',
    trigger     VARCHAR(16) NOT NULL DEFAULT 'manual', -- 'manual' | 'scheduled' | 'active'
    data        JSONB       NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_snapshots_book_created ON snapshots(book_guid, created_at DESC);
