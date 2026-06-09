-- Prevent duplicate Plaid item rows for the same book (e.g. a re-link after a
-- network error or re-auth). The Exchange path upserts on this key.
ALTER TABLE plaid_items
    ADD CONSTRAINT plaid_items_book_item_unique UNIQUE (book_guid, item_id);
