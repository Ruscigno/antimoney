import { useState, useEffect, useCallback, useRef } from 'react';
import { useParams, useLocation } from 'react-router-dom';
import { getAccount, getAccountRegisterPaged, deleteTransaction, getAccounts } from '../api/client';
import Register from '../components/Register';
import TransactionForm from '../components/TransactionForm';
import ReconcileWizard from '../components/ReconcileWizard';
import AccountBreadcrumbs from '../components/AccountBreadcrumbs';
import type { Account, RegisterEntry } from '../types';
import { t } from '../i18n';
import { useShortcut } from '../hooks/useShortcuts';

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
                const sorted = (page.entries || []).sort((a, b) => {
                    if (a.post_date < b.post_date) return -1;
                    if (a.post_date > b.post_date) return 1;
                    const idA = a.custom_id || '';
                    const idB = b.custom_id || '';
                    return idA.localeCompare(idB, undefined, { numeric: true, sensitivity: 'base' });
                });
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
                merged.sort((a, b) => {
                    if (a.post_date < b.post_date) return -1;
                    if (a.post_date > b.post_date) return 1;
                    const idA = a.custom_id || '';
                    const idB = b.custom_id || '';
                    return idA.localeCompare(idB, undefined, { numeric: true, sensitivity: 'base' });
                });

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
        setEntries(prev => prev.map(entry =>
            entry.split_guid === splitGuid
                ? { ...entry, reconcile_state: newState }
                : entry
        ));
    }, []);

    // Refresh around the same cursor date that was loaded (preserves scroll position)
    const refreshCurrentPage = useCallback(() => {
        loadInitialData(pageCursorRef.current);
    }, [loadInitialData]);

    // Full reload (after creating/editing/deleting transactions or finishing reconcile wizard)
    const handleDataChanged = useCallback(() => {
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

    useEffect(() => {
        // jumpCursorDateRef.current is the cursor date from the current navigation —
        // kept in sync on every render, but intentionally not in deps so we only
        // reload when the account id changes, not on every location change.
        loadInitialData(jumpCursorDateRef.current);
        // eslint-disable-next-line react-hooks/exhaustive-deps
    }, [loadInitialData]);

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
                    <h1 className="page-title">{account.name}</h1>
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

            <Register
                entries={entries}
                accountName={account.name}
                accountType={account.account_type}
                scrollTargetDate={jumpCursorDate}
                onReconcileStateChanged={handleReconcileStateChanged}
                onEditTransaction={setEditTxGuid}
                onDuplicateTransaction={setDuplicateTxGuid}
                onDeleteTransaction={handleDeleteTransaction}
                hasBefore={hasBefore}
                hasAfter={hasAfter}
                onLoadMore={loadMore}
                loadingMore={loadingMore}
            />

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
        </div>
    );
}
