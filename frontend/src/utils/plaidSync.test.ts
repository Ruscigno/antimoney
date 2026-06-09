import { describe, it, expect } from 'vitest';
import { shouldAutoSyncToday } from './plaidSync';

const TZ = 'America/Toronto';
// 2026-06-09 16:00 UTC == 12:00 EDT on 2026-06-09 (Toronto, UTC-4 in June).
const now = new Date('2026-06-09T16:00:00Z');

describe('shouldAutoSyncToday', () => {
    it('is false without a linked Plaid item', () => {
        expect(shouldAutoSyncToday(undefined, now, TZ)).toBe(false);
        expect(shouldAutoSyncToday({}, now, TZ)).toBe(false);
        expect(shouldAutoSyncToday({ last_synced_at: '2026-06-09T16:00:00Z' }, now, TZ)).toBe(false);
    });

    it('is true for a linked item that has never synced', () => {
        expect(shouldAutoSyncToday({ item_guid: 'i1' }, now, TZ)).toBe(true);
    });

    it('is false when already synced today (same date in the timezone)', () => {
        // 13:00 UTC == 09:00 EDT, still 2026-06-09 in Toronto.
        expect(shouldAutoSyncToday({ item_guid: 'i1', last_synced_at: '2026-06-09T13:00:00Z' }, now, TZ)).toBe(false);
    });

    it('is true when the last sync was a previous day in the timezone', () => {
        // 02:00 UTC == 22:00 EDT on 2026-06-08 in Toronto → previous day.
        expect(shouldAutoSyncToday({ item_guid: 'i1', last_synced_at: '2026-06-09T02:00:00Z' }, now, TZ)).toBe(true);
    });
});
