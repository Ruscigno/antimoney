ALTER TABLE accounts DROP COLUMN commodity_guid;
ALTER TABLE accounts DROP COLUMN commodity_scu;

ALTER TABLE transactions DROP COLUMN currency_guid;

DROP TABLE IF EXISTS prices;
DROP TABLE IF EXISTS commodities CASCADE;
