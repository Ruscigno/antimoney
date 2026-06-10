export interface PlaidAccountMeta {
    item_guid?: string;
    last_synced_at?: string;
    // Denormalized at link time so the register can label the sync status with
    // the bank's name (spec §6.2) without an extra fetch.
    institution_name?: string;
}

// Timezone used to decide the "first open of the day" auto-sync boundary.
// KNOWN LIMITATION: hardcoded to the target bank's locale (RBC = Canada).
// For users in other timezones the day-rollover can be off by a few hours.
// TODO: make this configurable per book/user.
export const AUTO_SYNC_TIMEZONE = 'America/Toronto';

// shouldAutoSyncToday reports whether a Plaid-linked account should auto-sync on
// open: it must have a linked item and not have synced yet *today* (evaluated in
// `timeZone`). `now` and `timeZone` are injectable for testing.
export function shouldAutoSyncToday(
    meta: PlaidAccountMeta | undefined,
    now: Date = new Date(),
    timeZone: string = AUTO_SYNC_TIMEZONE,
): boolean {
    if (!meta?.item_guid) return false;
    const today = now.toLocaleDateString('en-CA', { timeZone });
    const lastSynced = meta.last_synced_at
        ? new Date(meta.last_synced_at).toLocaleDateString('en-CA', { timeZone })
        : null;
    return !lastSynced || lastSynced < today;
}
