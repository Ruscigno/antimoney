import { useState, useEffect } from 'react';
import { useParams } from 'react-router-dom';
import { getAccount, getAccountRegister, deleteTransaction, getAccounts } from '../api/client';
import Register from '../components/Register';
import TransactionForm from '../components/TransactionForm';
import ReconcileWizard from '../components/ReconcileWizard';
import AccountBreadcrumbs from '../components/AccountBreadcrumbs';
import type { Account, RegisterEntry } from '../types';
import { t } from '../i18n';
import { useShortcut } from '../hooks/useShortcuts';

export default function AccountRegister() {
    const { id } = useParams<{ id: string }>();
    const [account, setAccount] = useState<Account | null>(null);
    const [allAccounts, setAllAccounts] = useState<Account[]>([]);
    const [entries, setEntries] = useState<RegisterEntry[]>([]);
    const [loading, setLoading] = useState(true);
    const [showForm, setShowForm] = useState(false);
    const [editTxGuid, setEditTxGuid] = useState<string | null>(null);
    const [showReconcile, setShowReconcile] = useState(false);

    // N shortcut opens new transaction form
    useShortcut('n', () => setShowForm(true), t('shortcuts.newTx'), undefined, []);

    const loadData = () => {
        if (!id) return;
        setLoading(true);
        Promise.all([getAccount(id), getAccountRegister(id), getAccounts()])
            .then(([acc, reg, all]) => {
                setAccount(acc);
                setAllAccounts(all);
                const sorted = (reg || []).sort((a, b) => {
                    if (a.post_date < b.post_date) return -1;
                    if (a.post_date > b.post_date) return 1;
                    const idA = a.custom_id || '';
                    const idB = b.custom_id || '';
                    return idA.localeCompare(idB, undefined, { numeric: true, sensitivity: 'base' });
                });
                setEntries(sorted);
            })
            .catch(console.error)
            .finally(() => setLoading(false));
    };

    const handleDeleteTransaction = async (guid: string) => {
        try {
            await deleteTransaction(guid);
            loadData();
        } catch (err: any) {
            alert(err.message || 'Failed to delete transaction');
        }
    };

    useEffect(() => {
        loadData();
    }, [id]);

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
                onReconcileChanged={loadData}
                onEditTransaction={setEditTxGuid}
                onDeleteTransaction={handleDeleteTransaction}
            />

            {(showForm || editTxGuid) && (
                <TransactionForm
                    onClose={() => { setShowForm(false); setEditTxGuid(null); }}
                    onCreated={loadData}
                    defaultAccountGuid={account.guid}
                    editTxGuid={editTxGuid || undefined}
                />
            )}

            {showReconcile && (
                <ReconcileWizard
                    accountGuids={[account.guid]}
                    accountName={account.name}
                    accountType={account.account_type}
                    onClose={() => setShowReconcile(false)}
                    onFinished={loadData}
                />
            )}
        </div>
    );
}
