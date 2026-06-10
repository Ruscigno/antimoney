-- Permanent dismissal for staged Plaid transactions (review round 6, #4):
-- without it, a suggestion the user never wants to import reappears on every
-- sync forever. Dismissed rows are excluded from suggestions but kept for the
-- dedupe/pending-correlation logic; disconnect still cascades them away.
ALTER TABLE plaid_staged_transactions
    ADD COLUMN dismissed BOOLEAN NOT NULL DEFAULT false;
