-- Add reconcile_state to splits.
-- 'n' = not reconciled (default), 'c' = cleared/acknowledged, 'y' = reconciled
ALTER TABLE splits ADD COLUMN reconcile_state CHAR(1) NOT NULL DEFAULT 'n';
