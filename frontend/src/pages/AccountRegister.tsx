import { useState, useEffect } from 'react';
import { useParams } from 'react-router-dom';
import { getAccount, getAccountRegister } from '../api/client';
import Register from '../components/Register';
import TransactionForm from '../components/TransactionForm';
import ReconcileWizard from '../components/ReconcileWizard';
import type { Account, RegisterEntry } from '../types';
import { t } from '../i18n';
import { useShortcut } from '../hooks/useShortcuts';

export default function AccountRegister() {
    const { id } = useParams<{ id: string }>();
    const [account, setAccount] = useState<Account | null>(null);
    const [entries, setEntries] = useState<RegisterEntry[]>([]);
    const [loading, setLoading] = useState(true);
    const [showForm, setShowForm] = useState(false);
    const [showReconcile, setShowReconcile] = useState(false);

    // N shortcut opens new transaction form
    useShortcut('n', () => setShowForm(true), t('shortcuts.newTx'), undefined, []);

    const loadData = () => {
        if (!id) return;
        setLoading(true);
        Promise.all([getAccount(id), getAccountRegister(id)])
            .then(([acc, reg]) => {
                setAccount(acc);
                setEntries(reg || []);
            })
            .catch(console.error)
            .finally(() => setLoading(false));
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

            <Register entries={entries} accountName={account.name} onReconcileChanged={loadData} />

            {showForm && (
                <TransactionForm
                    onClose={() => setShowForm(false)}
                    onCreated={loadData}
                    defaultAccountGuid={account.guid}
                />
            )}

            {showReconcile && (
                <ReconcileWizard
                    accountGuid={account.guid}
                    accountName={account.name}
                    onClose={() => setShowReconcile(false)}
                    onFinished={loadData}
                />
            )}
        </div>
    );
}
