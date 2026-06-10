import { useState, useEffect, useCallback, useMemo, useRef } from 'react';
import { useParams, useLocation } from 'react-router-dom';
import { getAccount, getAccountRegister, getAccountRegisterPaged, deleteTransaction, getAccounts, plaidSync } from '../api/client';
import Register from '../components/Register';
import TransactionForm from '../components/TransactionForm';
import ReconcileWizard from '../components/ReconcileWizard';
import AccountBreadcrumbs from '../components/AccountBreadcrumbs';
import ImportMatcher from '../components/ImportMatcher';
import type { Account, RegisterEntry, SyncSuggestion } from '../types';
import { shouldAutoSyncToday, type PlaidAccountMeta } from '../utils/plaidSync';
import { t } from '../i18n';
import { useShortcut } from '../hooks/useShortcuts';
import { filterRegisterEntries } from '../utils/registerSearch';
import { compareEntries } from '../utils/registerSort';

const PAGE_SIZE = 50;

function getTodayStr(): string {
    const now = new Date();
    return `${now.getFullYear()}-${String(now.getMonth() + 1).padStart(2, '0')}-${String(now.getDate()).padStart(2, '0')}`;
}

export default function AccountRegister() {
    const { id } = useParams<{ id: string }>();
    const location = useLocation();
    // Derive the jump-cursor date from current navigation state on every render.
    // A plain useRef init would only run once at mount — wrong when React Router
    // reuses this component instance while navigating between accounts.
    const jumpCursorDate = (location.state as { cursorDate?: string } | null)?.cursorDate;
    // Keep a ref in sync so the effect can read the latest value without
    // needing to declare it as a dependency (we only reload when the account id changes).
    const jumpCursorDateRef = useRef<string | undefined>(jumpCursorDate);
    jumpCursorDateRef.current = jumpCursorDate;
    const [account, setAccount] = useState<Account | null>(null);
    const [allAccounts, setAllAccounts] = useState<Account[]>([]);
    const [entries, setEntries] = useState<RegisterEntry[]>([]);
    const [loading, setLoading] = useState(true);
    const [loadingMore, setLoadingMore] = useState(false);
    const [showForm, setShowForm] = useState(false);
    const [editTxGuid, setEditTxGuid] = useState<string | null>(null);
    const [duplicateTxGuid, setDuplicateTxGuid] = useState<string | null>(null);
    const [showReconcile, setShowReconcile] = useState(false);
    const [hasBefore, setHasBefore] = useState(false);
    const [hasAfter, setHasAfter] = useState(false);
    const [syncing, setSyncing] = useState(false);
    const [syncMessage, setSyncMessage] = useState<string | null>(null);
    const [importSuggestions, setImportSuggestions] = useState<SyncSuggestion[] | null>(null);
    const [importInstitution, setImportInstitution] = useState('');

    // Search filters across ALL of the account's transactions (not just the loaded
    // page). When a query is active we lazily fetch the full register once and
    // filter it client-side, preserving the server-computed running balances.
    // `search` tracks the input for instant feedback; `debouncedSearch` (200ms)
    // drives the actual fetch + filtering so typing stays smooth on large accounts.
    const [search, setSearch] = useState('');
    const [debouncedSearch, setDebouncedSearch] = useState('');
    const [allEntries, setAllEntries] = useState<RegisterEntry[] | null>(null);
    const [searchLoading, setSearchLoading] = useState(false);
    const [searchError, setSearchError] = useState<string | null>(null);
    const isSearching = debouncedSearch.trim() !== '';

    const clearSearch = useCallback(() => {
        setSearch('');
        setDebouncedSearch('');
        setSearchError(null);
    }, []);

    const firstOffsetRef = useRef<number | null>(null);
    const lastOffsetRef = useRef<number | null>(null);
    // Track the cursor date used for the current page so refreshes stay centred on the same date
    const pageCursorRef = useRef<string>(getTodayStr());

    // N shortcut opens new transaction form
    useShortcut('n', () => setShowForm(true), t('shortcuts.newTx'), undefined, []);

    // Initial load: fetch account info + page of entries around cursorDate (defaults to today)
    const loadInitialData = useCallback((cursorDate?: string) => {
        if (!id) return;
        setLoading(true);
        const dateToUse = cursorDate ?? getTodayStr();
        pageCursorRef.current = dateToUse;
        Promise.all([getAccount(id), getAccountRegisterPaged(id, dateToUse, 'around', PAGE_SIZE), getAccounts()])
            .then(([acc, page, all]) => {
                setAccount(acc);
                setAllAccounts(all);
                const sorted = (page.entries || []).sort(compareEntries);
                setEntries(sorted);
                setHasBefore(page.has_before);
                setHasAfter(page.has_after);
                firstOffsetRef.current = page.first_offset;
                lastOffsetRef.current = page.last_offset;
            })
            .catch(console.error)
            .finally(() => setLoading(false));
    }, [id]);

    // Load more entries (prepend older or append newer)
    const loadMore = useCallback(async (direction: 'before' | 'after') => {
        if (!id || loadingMore) return;
        const cursor = direction === 'before' ? firstOffsetRef.current : lastOffsetRef.current;
        if (cursor === null) return;

        setLoadingMore(true);
        try {
            const page = await getAccountRegisterPaged(id, String(cursor), direction, PAGE_SIZE);
            const newEntries = page.entries || [];
            if (newEntries.length === 0) {
                if (direction === 'before') setHasBefore(false);
                else setHasAfter(false);
                return;
            }

            setEntries(prev => {
                // Deduplicate by split_guid
                const existingGuids = new Set(prev.map(e => e.split_guid));
                const unique = newEntries.filter(e => !existingGuids.has(e.split_guid));
                if (unique.length === 0) {
                    return prev;
                }

                const merged = direction === 'before'
                    ? [...unique, ...prev]
                    : [...prev, ...unique];

                // Sort to be safe
                merged.sort(compareEntries);

                return merged;
            });

            if (direction === 'before') {
                setHasBefore(page.has_before);
                firstOffsetRef.current = page.first_offset;
            } else {
                setHasAfter(page.has_after);
                lastOffsetRef.current = page.last_offset;
            }
        } catch (err) {
            console.error('Failed to load more entries:', err);
        } finally {
            setLoadingMore(false);
        }
    }, [id, loadingMore]);

    // Handle reconcile state change locally without reloading
    const handleReconcileStateChanged = useCallback((splitGuid: string, newState: string) => {
        const patch = (entry: RegisterEntry) =>
            entry.split_guid === splitGuid ? { ...entry, reconcile_state: newState } : entry;
        setEntries(prev => prev.map(patch));
        // Keep the search cache (if loaded) in sync so toggles show while filtering
        setAllEntries(prev => (prev ? prev.map(patch) : prev));
    }, []);

    // Refresh around the same cursor date that was loaded (preserves scroll position)
    const refreshCurrentPage = useCallback(() => {
        loadInitialData(pageCursorRef.current);
    }, [loadInitialData]);

    // Full reload (after creating/editing/deleting transactions or finishing reconcile wizard).
    // Invalidate the search cache so an active filter refetches the latest data.
    const handleDataChanged = useCallback(() => {
        setAllEntries(null);
        refreshCurrentPage();
    }, [refreshCurrentPage]);

    const handleDeleteTransaction = async (guid: string) => {
        try {
            await deleteTransaction(guid);
            handleDataChanged();
        } catch (err: any) {
            alert(err.message || 'Failed to delete transaction');
        }
    };

    const triggerSync = async (itemGUID: string, institutionName: string) => {
        setSyncing(true);
        setSyncMessage(t('plaid.syncing').replace('{{institution}}', institutionName));
        try {
            const result = await plaidSync(itemGUID);
            if (result.count > 0) {
                setSyncMessage(t('plaid.syncSuccess').replace('{{count}}', String(result.count)));
                setImportSuggestions(result.suggestions);
                setImportInstitution(institutionName);
            } else {
                setSyncMessage(t('plaid.syncNone'));
                setTimeout(() => setSyncMessage(null), 3000);
            }
        } catch (e) {
            // "reconnect_required" is the backend's marker for ITEM_LOGIN_REQUIRED:
            // tell the user to re-authorize instead of showing a generic failure.
            setSyncMessage(
                e instanceof Error && e.message === 'reconnect_required'
                    ? t('plaid.reconnectNeeded')
                    : t('plaid.syncError').replace('{{institution}}', institutionName),
            );
        } finally {
            setSyncing(false);
        }
    };

    useEffect(() => {
        // jumpCursorDateRef.current is the cursor date from the current navigation —
        // kept in sync on every render, but intentionally not in deps so we only
        // reload when the account id changes, not on every location change.
        loadInitialData(jumpCursorDateRef.current);
    }, [loadInitialData]);

    // Debounce the search input so filtering/fetching doesn't run on every keystroke.
    useEffect(() => {
        const timer = setTimeout(() => setDebouncedSearch(search), 200);
        return () => clearTimeout(timer);
    }, [search]);

    // Reset the search when navigating to a different account.
    useEffect(() => {
        setSearch('');
        setDebouncedSearch('');
        setAllEntries(null);
        setSearchError(null);
    }, [id]);

    // Auto-sync on first open of the day if account is linked to Plaid.
    // Trigger decision (incl. the timezone limitation) lives in shouldAutoSyncToday.
    useEffect(() => {
        const plaidMeta = (account?.metadata as any)?.plaid as PlaidAccountMeta | undefined;
        if (plaidMeta?.item_guid && shouldAutoSyncToday(plaidMeta)) {
            triggerSync(plaidMeta.item_guid, account?.name ?? 'Bank');
        }
        // eslint-disable-next-line react-hooks/exhaustive-deps
    }, [account?.guid]);

    // Lazily fetch the full register the first time a search is active (or after
    // the cache is invalidated by a data change). Fetched once, then filtered
    // in-memory as the user keeps typing.
    useEffect(() => {
        if (!id || !isSearching || allEntries !== null) return;
        let cancelled = false;
        setSearchLoading(true);
        setSearchError(null);
        getAccountRegister(id)
            .then(all => { if (!cancelled) setAllEntries((all || []).sort(compareEntries)); })
            .catch(err => {
                if (!cancelled) setSearchError(t('register.searchError'));
                console.error(err);
            })
            .finally(() => { if (!cancelled) setSearchLoading(false); });
        return () => { cancelled = true; };
    }, [id, isSearching, allEntries]);

    // Entries shown in the register: filtered full set while searching, otherwise
    // the paginated window. Memoized so the filter only re-runs when inputs change.
    const displayEntries = useMemo(
        () => (isSearching ? filterRegisterEntries(allEntries ?? [], debouncedSearch) : entries),
        [isSearching, allEntries, debouncedSearch, entries],
    );

    if (loading) {
        return <div className="loading"><div className="loading-spinner" />{t('common.loading')}</div>;
    }

    if (!account) {
        return <div className="empty-state"><p>{t('accounts.notFound')}</p></div>;
    }

    return (
        <div>
            <AccountBreadcrumbs currentAccount={account} allAccounts={allAccounts} />
            <div className="page-header" style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'flex-start' }}>
                <div>
                    <h1 className="page-title">
                        {account.name}
                        {(account.metadata as any)?.plaid && (
                            <span style={{ marginLeft: '0.5rem' }}>
                                <button
                                    className="btn btn-secondary"
                                    onClick={() => {
                                        const meta = (account.metadata as any).plaid as { item_guid: string };
                                        triggerSync(meta.item_guid, account.name);
                                    }}
                                    disabled={syncing}
                                >
                                    {syncing ? '…' : t('plaid.syncNow')}
                                </button>
                            </span>
                        )}
                        {syncMessage && <span style={{ marginLeft: '0.5rem', fontSize: '0.875rem', color: 'var(--text-muted)' }}>{syncMessage}</span>}
                    </h1>
                    <p className="page-subtitle">{account.description || account.account_type}</p>
                </div>
                <div style={{ display: 'flex', gap: 8 }}>
                    <button
                        className="btn btn-secondary"
                        onClick={() => setShowReconcile(true)}
                        id="btn-reconcile"
                    >
                        {t('reconcile.button')}
                    </button>
                    <button className="btn btn-primary" onClick={() => setShowForm(true)} id="btn-new-tx">
                        {t('common.newTransaction')}
                        <kbd className="kbd-hint" style={{ marginLeft: 6 }}>N</kbd>
                    </button>
                </div>
            </div>

            <div className="register-search-bar">
                <div className="register-search-input-wrap">
                    <input
                        type="text"
                        className="form-input register-search-input"
                        placeholder={t('register.searchPlaceholder')}
                        value={search}
                        onChange={e => setSearch(e.target.value)}
                        aria-label={t('register.searchPlaceholder')}
                    />
                    {search !== '' && (
                        <button
                            type="button"
                            className="register-search-clear"
                            onClick={clearSearch}
                            title={t('register.searchClear')}
                            aria-label={t('register.searchClear')}
                        >
                            ×
                        </button>
                    )}
                </div>
                {isSearching && searchError && (
                    <span className="register-search-error" role="alert">{searchError}</span>
                )}
                {isSearching && !searchLoading && !searchError && (
                    <span className="register-search-count">
                        {displayEntries.length === 0
                            ? t('register.searchNoMatch')
                            : t('register.searchResults')
                                .replace('{{count}}', String(displayEntries.length))
                                .replace('{{total}}', String(allEntries?.length ?? 0))}
                    </span>
                )}
            </div>

            {isSearching && searchLoading && allEntries === null ? (
                <div className="loading"><div className="loading-spinner" />{t('common.loading')}</div>
            ) : isSearching && displayEntries.length === 0 ? null : (
                <Register
                    entries={displayEntries}
                    accountName={account.name}
                    accountType={account.account_type}
                    scrollTargetDate={isSearching ? undefined : jumpCursorDate}
                    onReconcileStateChanged={handleReconcileStateChanged}
                    onEditTransaction={setEditTxGuid}
                    onDuplicateTransaction={setDuplicateTxGuid}
                    onDeleteTransaction={handleDeleteTransaction}
                    hasBefore={isSearching ? false : hasBefore}
                    hasAfter={isSearching ? false : hasAfter}
                    onLoadMore={isSearching ? undefined : loadMore}
                    loadingMore={isSearching ? false : loadingMore}
                />
            )}

            {(showForm || editTxGuid || duplicateTxGuid) && (
                <TransactionForm
                    onClose={() => { setShowForm(false); setEditTxGuid(null); setDuplicateTxGuid(null); }}
                    onCreated={handleDataChanged}
                    defaultAccountGuid={account.guid}
                    editTxGuid={editTxGuid || undefined}
                    duplicateTxGuid={duplicateTxGuid || undefined}
                />
            )}

            {showReconcile && (
                <ReconcileWizard
                    accountGuids={[account.guid]}
                    accountName={account.name}
                    accountType={account.account_type}
                    onClose={() => setShowReconcile(false)}
                    onFinished={handleDataChanged}
                />
            )}

            {importSuggestions && (
                <ImportMatcher
                    institutionName={importInstitution}
                    suggestions={importSuggestions}
                    onClose={() => { setImportSuggestions(null); setSyncMessage(null); }}
                    onImported={(count) => {
                        setImportSuggestions(null);
                        setSyncMessage(t('plaid.importSuccess').replace('{{count}}', String(count)));
                        setTimeout(() => setSyncMessage(null), 4000);
                        handleDataChanged();
                    }}
                />
            )}
        </div>
    );
}
