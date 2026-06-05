import { describe, it, expect } from 'vitest';
import type { RegisterEntry } from '../types';
import { compareEntries } from './registerSort';

function entry(post_date: string, custom_id: string): RegisterEntry {
    return {
        transaction_guid: `tx-${post_date}-${custom_id}`,
        custom_id,
        post_date,
        description: '',
        transfer_account: '',
        transfer_account_guid: '',
        deposit: null,
        withdrawal: null,
        balance: 0,
        split_memo: '',
        split_guid: `split-${post_date}-${custom_id}`,
        reconcile_state: 'n',
    };
}

describe('compareEntries', () => {
    it('orders by post_date ascending', () => {
        expect(compareEntries(entry('2025-01-01T11:00:00Z', '1'), entry('2025-02-01T11:00:00Z', '1'))).toBeLessThan(0);
        expect(compareEntries(entry('2025-02-01T11:00:00Z', '1'), entry('2025-01-01T11:00:00Z', '1'))).toBeGreaterThan(0);
    });

    it('breaks ties on custom_id numerically, not lexicographically', () => {
        const date = '2025-01-01T11:00:00Z';
        // Lexicographic order would put "10" before "9"; numeric must not.
        expect(compareEntries(entry(date, '9'), entry(date, '10'))).toBeLessThan(0);
        expect(compareEntries(entry(date, '10'), entry(date, '9'))).toBeGreaterThan(0);
    });

    it('treats equal dates and equal ids as equal', () => {
        const date = '2025-01-01T11:00:00Z';
        expect(compareEntries(entry(date, '5'), entry(date, '5'))).toBe(0);
        expect(compareEntries(entry(date, ''), entry(date, ''))).toBe(0);
    });

    it('handles empty custom_id without throwing', () => {
        const date = '2025-01-01T11:00:00Z';
        // An empty id should sort before a non-empty one and never throw.
        expect(compareEntries(entry(date, ''), entry(date, '1'))).toBeLessThan(0);
    });

    it('sorts a full array into the expected order', () => {
        const date = '2025-01-01T11:00:00Z';
        const sorted = [
            entry('2025-03-01T11:00:00Z', '1'),
            entry(date, '10'),
            entry(date, '2'),
            entry('2024-12-31T11:00:00Z', '99'),
        ].sort(compareEntries);
        expect(sorted.map(e => `${e.post_date}#${e.custom_id}`)).toEqual([
            '2024-12-31T11:00:00Z#99',
            `${date}#2`,
            `${date}#10`,
            '2025-03-01T11:00:00Z#1',
        ]);
    });
});
