-- Multi-tenancy: add users table and scope books to users.

-- Users table
CREATE TABLE users (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    email VARCHAR(255) NOT NULL UNIQUE,
    password_hash VARCHAR(255) NOT NULL,
    name VARCHAR(255) NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Add user ownership to books
ALTER TABLE books ADD COLUMN user_id UUID REFERENCES users(id);
CREATE INDEX idx_books_user ON books(user_id);

-- Scope accounts to a book
ALTER TABLE accounts ADD COLUMN book_guid UUID REFERENCES books(guid);
CREATE INDEX idx_accounts_book ON accounts(book_guid);

-- Scope transactions to a book
ALTER TABLE transactions ADD COLUMN book_guid UUID REFERENCES books(guid);
CREATE INDEX idx_transactions_book ON transactions(book_guid);
