-- Refuse to roll back while live bank connections exist: dropping plaid_items
-- destroys the only copies of the access tokens and leaves the Items alive
-- (and billable) at Plaid with no way to call /item/remove afterwards.
-- Disconnect every item via the API (DELETE /api/data/plaid/items/{guid})
-- before running this down migration.
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM plaid_items) THEN
        RAISE EXCEPTION 'plaid_items still holds live bank connections; disconnect them via the API before rolling back';
    END IF;
END $$;

DROP TABLE IF EXISTS plaid_items;
