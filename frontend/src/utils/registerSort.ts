import type { RegisterEntry } from '../types';

/**
 * Order register entries oldest → newest, breaking ties on the numeric custom
 * id so "9" sorts before "10" (not lexicographically). Used for both the
 * initial register load and incremental "load more" merges, so full-register
 * search results stay in the same order as the paginated view.
 */
export function compareEntries(a: RegisterEntry, b: RegisterEntry): number {
    if (a.post_date < b.post_date) return -1;
    if (a.post_date > b.post_date) return 1;
    const idA = a.custom_id || '';
    const idB = b.custom_id || '';
    return idA.localeCompare(idB, undefined, { numeric: true, sensitivity: 'base' });
}
