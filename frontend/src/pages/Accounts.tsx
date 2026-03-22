import { useState, useEffect } from 'react';
import { getAccounts, deleteAccount } from '../api/client';
import AccountTree from '../components/AccountTree';
import AccountForm from '../components/AccountForm';
import type { Account } from '../types';
import { t } from '../i18n';

export default function Accounts() {
    const [accounts, setAccounts] = useState<Account[]>([]);
    const [loading, setLoading] = useState(true);
    const [showForm, setShowForm] = useState(false);
    const [editingAccount, setEditingAccount] = useState<Account | null>(null);
    const [showReconciled, setShowReconciled] = useState(false);

    const loadData = () => {
        setLoading(true);
        getAccounts()
            .then(setAccounts)
            .catch(console.error)
            .finally(() => setLoading(false));
    };

    useEffect(() => { loadData(); }, []);

    const handleEdit = (account: Account) => {
        setEditingAccount(account);
        setShowForm(true);
    };

    const handleDelete = async (account: Account) => {
        if (!window.confirm(t('accounts.confirmDelete'))) return;
        try {
            await deleteAccount(account.guid);
            loadData();
        } catch (err: any) {
            alert(err.message || 'Failed to delete');
        }
    };

    const handleCloseForm = () => {
        setShowForm(false);
        setEditingAccount(null);
    };

    if (loading) {
        return <div className="loading"><div className="loading-spinner" />{t('common.loading')}</div>;
    }

    return (
        <div>
            <div className="page-header" style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'flex-start' }}>
                <div>
                    <h1 className="page-title">{t('accounts.title')}</h1>
                    <p className="page-subtitle">{t('accounts.subtitle')}</p>
                </div>
                <div style={{ display: 'flex', gap: 8, alignItems: 'center' }}>
                    <div
                        className="toggle-group"
                        style={{
                            display: 'inline-flex', borderRadius: 'var(--radius-sm)',
                            border: '1px solid var(--border-color)', overflow: 'hidden',
                        }}
                    >
                        <button
                            className={`toggle-btn ${!showReconciled ? 'active' : ''}`}
                            onClick={() => setShowReconciled(false)}
                            style={{
                                padding: '6px 14px', fontSize: '0.78rem', fontWeight: 500,
                                border: 'none', cursor: 'pointer',
                                background: !showReconciled ? 'var(--color-primary)' : 'var(--bg-tertiary)',
                                color: !showReconciled ? '#fff' : 'var(--text-secondary)',
                                transition: 'all 0.15s',
                            }}
                        >
                            {t('dashboard.total')}
                        </button>
                        <button
                            className={`toggle-btn ${showReconciled ? 'active' : ''}`}
                            onClick={() => setShowReconciled(true)}
                            style={{
                                padding: '6px 14px', fontSize: '0.78rem', fontWeight: 500,
                                border: 'none', cursor: 'pointer',
                                background: showReconciled ? 'var(--color-primary)' : 'var(--bg-tertiary)',
                                color: showReconciled ? '#fff' : 'var(--text-secondary)',
                                transition: 'all 0.15s',
                            }}
                        >
                            {t('dashboard.reconciled')}
                        </button>
                    </div>
                    <button className="btn btn-primary" onClick={() => setShowForm(true)}>
                        {t('accounts.newAccount')}
                    </button>
                </div>
            </div>

            <div className="card" style={{ padding: 0, overflow: 'hidden' }}>
                <AccountTree
                    accounts={accounts}
                    onEdit={handleEdit}
                    onDelete={handleDelete}
                    showReconciled={showReconciled}
                />
            </div>

            {showForm && (
                <AccountForm
                    accounts={accounts}
                    editingAccount={editingAccount}
                    onClose={handleCloseForm}
                    onSaved={loadData}
                />
            )}
        </div>
    );
}
