import { describe, it, expect } from 'vitest';
import type { RegisterEntry } from '../types';
import { filterRegisterEntries, entryMatchesQuery } from './registerSearch';

function makeEntry(overrides: Partial<RegisterEntry> = {}): RegisterEntry {
    return {
        transaction_guid: 'tx-1',
        custom_id: '75',
        post_date: '2025-09-30T11:00:00Z',
        description: 'Payment, Rene Ratti',
        transfer_account: '-- Split Transaction --',
        transfer_account_guid: '',
        deposit: 2400,
        withdrawal: null,
        balance: 2521.38,
        split_memo: 'Rent',
        split_guid: 'split-1',
        reconcile_state: 'c',
        ...overrides,
    };
}

const entries: RegisterEntry[] = [
    makeEntry(),
    makeEntry({
        transaction_guid: 'tx-2',
        custom_id: '79',
        description: 'Avion, Minimum Payment',
        transfer_account: 'RBC Avion Visa Infinite',
        deposit: null,
        withdrawal: 200,
        split_memo: '',
    }),
    makeEntry({
        transaction_guid: 'tx-3',
        custom_id: '81',
        description: 'Transfer From RBC to Tangerine',
        transfer_account: 'Tangerine',
        deposit: null,
        withdrawal: 209.75,
        split_memo: '',
    }),
];

describe('filterRegisterEntries', () => {
    it('returns all entries for an empty or whitespace query', () => {
        expect(filterRegisterEntries(entries, '')).toHaveLength(3);
        expect(filterRegisterEntries(entries, '   ')).toHaveLength(3);
    });

    it('matches a partial substring of the description (case-insensitive)', () => {
        const result = filterRegisterEntries(entries, 'rene');
        expect(result).toHaveLength(1);
        expect(result[0].transaction_guid).toBe('tx-1');

        // partial fragment, different case
        expect(filterRegisterEntries(entries, 'AVI')).toHaveLength(1);
    });

    it('matches against the transfer account name', () => {
        const result = filterRegisterEntries(entries, 'tangerine');
        expect(result).toHaveLength(1);
        expect(result[0].transaction_guid).toBe('tx-3');
    });

    it('matches against the split memo', () => {
        const result = filterRegisterEntries(entries, 'rent');
        expect(result).toHaveLength(1);
        expect(result[0].transaction_guid).toBe('tx-1');
    });

    it('matches against the custom id (# column)', () => {
        const result = filterRegisterEntries(entries, '79');
        expect(result).toHaveLength(1);
        expect(result[0].custom_id).toBe('79');
    });

    it('matches against the formatted amount', () => {
        const result = filterRegisterEntries(entries, '209.75');
        expect(result).toHaveLength(1);
        expect(result[0].transaction_guid).toBe('tx-3');
    });

    it('combines multiple tokens with AND in any order', () => {
        expect(filterRegisterEntries(entries, 'rbc tangerine')).toHaveLength(1);
        expect(filterRegisterEntries(entries, 'tangerine rbc')).toHaveLength(1);
        // both tokens must be present somewhere
        expect(filterRegisterEntries(entries, 'rbc rent')).toHaveLength(0);
    });

    it('returns an empty array when nothing matches', () => {
        expect(filterRegisterEntries(entries, 'nonexistent')).toHaveLength(0);
    });
});

describe('entryMatchesQuery', () => {
    it('matches everything for an empty query', () => {
        expect(entryMatchesQuery(makeEntry(), '')).toBe(true);
    });

    it('uses the withdrawal amount when there is no deposit', () => {
        const entry = makeEntry({ deposit: null, withdrawal: 200 });
        expect(entryMatchesQuery(entry, '200.00')).toBe(true);
    });
});
