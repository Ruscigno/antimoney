ALTER TABLE plaid_items
    DROP CONSTRAINT IF EXISTS plaid_items_book_item_unique;

-- The audit table is created by this migration's up; a full rollback must not
-- leave orphaned copies of item rows (token ciphertexts included) behind.
DROP TABLE IF EXISTS plaid_migration_audit;
