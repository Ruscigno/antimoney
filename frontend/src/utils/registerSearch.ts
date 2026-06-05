import type { RegisterEntry } from '../types';

/**
 * Build the lowercase haystack searched for a single register entry.
 *
 * Includes the human-readable text fields (description, transfer account,
 * memo), the custom id shown in the "#" column, and the transaction amount
 * formatted to 2 decimals without a currency symbol — so a user can find a
 * row by typing e.g. "rent", "tangerine" or "720.77".
 */
function entryHaystack(entry: RegisterEntry): string {
    const amount = entry.deposit ?? entry.withdrawal;
    const amountStr = amount != null ? Math.abs(amount).toFixed(2) : '';
    return [
        entry.description,
        entry.transfer_account,
        entry.split_memo,
        entry.custom_id,
        amountStr,
    ]
        .filter(Boolean)
        .join(' ')
        .toLowerCase();
}

/** Split a query into lowercase, whitespace-separated tokens. */
function tokenize(query: string): string[] {
    return query.toLowerCase().split(/\s+/).filter(Boolean);
}

function matchesTokens(entry: RegisterEntry, tokens: string[]): boolean {
    if (tokens.length === 0) return true;
    const haystack = entryHaystack(entry);
    return tokens.every(token => haystack.includes(token));
}

/**
 * Returns true when every whitespace-separated token in `query` appears
 * somewhere in the entry (case-insensitive substring / partial match).
 * Multiple tokens are combined with AND, so "rene rent" matches a row whose
 * text contains both words in any order. An empty query matches everything.
 */
export function entryMatchesQuery(entry: RegisterEntry, query: string): boolean {
    return matchesTokens(entry, tokenize(query));
}

/**
 * Filter register entries by a partial-match search query. Returns the input
 * array unchanged when the query is empty (or whitespace only). The query is
 * tokenized once, not per entry.
 */
export function filterRegisterEntries(entries: RegisterEntry[], query: string): RegisterEntry[] {
    const tokens = tokenize(query);
    if (tokens.length === 0) return entries;
    return entries.filter(entry => matchesTokens(entry, tokens));
}
