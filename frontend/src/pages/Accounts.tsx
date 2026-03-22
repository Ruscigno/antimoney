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
                <button className="btn btn-primary" onClick={() => setShowForm(true)}>
                    {t('accounts.newAccount')}
                </button>
            </div>

            <div className="card" style={{ padding: 0, overflow: 'hidden' }}>
                <AccountTree
                    accounts={accounts}
                    onEdit={handleEdit}
                    onDelete={handleDelete}
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
