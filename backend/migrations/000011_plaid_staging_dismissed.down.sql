ALTER TABLE plaid_staged_transactions
    DROP CONSTRAINT IF EXISTS plaid_staged_amount_denom_positive;
ALTER TABLE plaid_staged_transactions
    DROP COLUMN IF EXISTS dismissed;
