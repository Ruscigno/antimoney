-- Revert multi-tenancy changes
ALTER TABLE transactions DROP COLUMN IF EXISTS book_guid;
ALTER TABLE accounts DROP COLUMN IF EXISTS book_guid;
ALTER TABLE books DROP COLUMN IF EXISTS user_id;
DROP TABLE IF EXISTS users;
