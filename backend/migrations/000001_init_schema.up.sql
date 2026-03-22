-- Antimoney Schema v1: Core double-entry accounting tables
-- Inspired by the GnuCash SQL schema, modernized with JSONB and OCC.

CREATE EXTENSION IF NOT EXISTS "pgcrypto";

-- Books: root container for the entire ledger
CREATE TABLE books (
    guid UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    root_account_guid UUID,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Commodities: currencies, stocks, mutual funds
CREATE TABLE commodities (
    guid UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    namespace VARCHAR(255) NOT NULL DEFAULT 'CURRENCY',   -- CURRENCY, STOCK, FUND, etc.
    mnemonic VARCHAR(32) NOT NULL,                         -- BRL, USD, EUR, AAPL, etc.
    fullname VARCHAR(255) NOT NULL DEFAULT '',
    fraction INT NOT NULL DEFAULT 100,                     -- SCU: 100 for cents, 1000 for mills
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(namespace, mnemonic)
);

-- Accounts: hierarchical chart of accounts (adjacency list)
CREATE TABLE accounts (
    guid UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name VARCHAR(255) NOT NULL,
    account_type VARCHAR(32) NOT NULL,                     -- ASSET, LIABILITY, INCOME, EXPENSE, EQUITY, BANK, CASH, CREDIT, etc.
    commodity_guid UUID NOT NULL REFERENCES commodities(guid),
    commodity_scu INT NOT NULL DEFAULT 100,
    parent_guid UUID REFERENCES accounts(guid),
    placeholder BOOLEAN NOT NULL DEFAULT FALSE,
    description TEXT NOT NULL DEFAULT '',
    metadata JSONB NOT NULL DEFAULT '{}',                  -- Replaces KVP slots table
    version INT NOT NULL DEFAULT 1,                        -- Optimistic Concurrency Control
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Transactions: zero-sum containers for splits
CREATE TABLE transactions (
    guid UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    currency_guid UUID NOT NULL REFERENCES commodities(guid),
    post_date TIMESTAMPTZ NOT NULL,
    enter_date TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    description TEXT NOT NULL DEFAULT '',
    metadata JSONB NOT NULL DEFAULT '{}',
    version INT NOT NULL DEFAULT 1,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Splits: individual commodity movements within a transaction
CREATE TABLE splits (
    guid UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tx_guid UUID NOT NULL REFERENCES transactions(guid) ON DELETE CASCADE,
    account_guid UUID NOT NULL REFERENCES accounts(guid),
    memo TEXT NOT NULL DEFAULT '',
    -- Value: amount in the TRANSACTION's currency (for zero-sum balancing)
    value_num BIGINT NOT NULL DEFAULT 0,
    value_denom BIGINT NOT NULL DEFAULT 1,
    -- Quantity: amount in the ACCOUNT's commodity (for account balance)
    quantity_num BIGINT NOT NULL DEFAULT 0,
    quantity_denom BIGINT NOT NULL DEFAULT 1,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Prices: historical exchange rates between commodities
CREATE TABLE prices (
    guid UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    commodity_guid UUID NOT NULL REFERENCES commodities(guid),
    currency_guid UUID NOT NULL REFERENCES commodities(guid),
    price_date TIMESTAMPTZ NOT NULL,
    source VARCHAR(255) NOT NULL DEFAULT 'user',
    value_num BIGINT NOT NULL,
    value_denom BIGINT NOT NULL DEFAULT 1,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Indexes for performance
CREATE INDEX idx_accounts_parent ON accounts(parent_guid);
CREATE INDEX idx_accounts_type ON accounts(account_type);
CREATE INDEX idx_accounts_commodity ON accounts(commodity_guid);
CREATE INDEX idx_splits_tx ON splits(tx_guid);
CREATE INDEX idx_splits_account ON splits(account_guid);
CREATE INDEX idx_transactions_post_date ON transactions(post_date);
CREATE INDEX idx_prices_commodity ON prices(commodity_guid, currency_guid, price_date);
CREATE INDEX idx_accounts_metadata ON accounts USING GIN (metadata);
CREATE INDEX idx_transactions_metadata ON transactions USING GIN (metadata);

-- Set the foreign key on books -> root_account
ALTER TABLE books ADD CONSTRAINT fk_books_root_account
    FOREIGN KEY (root_account_guid) REFERENCES accounts(guid);
