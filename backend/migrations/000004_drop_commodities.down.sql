-- Commodities: currencies, stocks, mutual funds
CREATE TABLE commodities (
    guid UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    namespace VARCHAR(255) NOT NULL DEFAULT 'CURRENCY',
    mnemonic VARCHAR(32) NOT NULL,
    fullname VARCHAR(255) NOT NULL DEFAULT '',
    fraction INT NOT NULL DEFAULT 100,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(namespace, mnemonic)
);

ALTER TABLE accounts ADD COLUMN commodity_guid UUID REFERENCES commodities(guid);
ALTER TABLE accounts ADD COLUMN commodity_scu INT NOT NULL DEFAULT 100;

ALTER TABLE transactions ADD COLUMN currency_guid UUID REFERENCES commodities(guid);

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

CREATE INDEX idx_prices_commodity ON prices(commodity_guid, currency_guid, price_date);
CREATE INDEX idx_accounts_commodity ON accounts(commodity_guid);
