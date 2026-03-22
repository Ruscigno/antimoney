import { useState, useEffect } from 'react';
import { getAccounts, deleteAccount } from '../api/client';
import AccountTree, { buildTree, collectGuids } from '../components/AccountTree';
import AccountForm from '../components/AccountForm';
import ReconcileWizard from '../components/ReconcileWizard';
import type { Account } from '../types';
import { t } from '../i18n';

export default function Accounts() {
    const [accounts, setAccounts] = useState<Account[]>([]);
    const [loading, setLoading] = useState(true);
    const [showForm, setShowForm] = useState(false);
    const [editingAccount, setEditingAccount] = useState<Account | null>(null);
    const [showReconciled, setShowReconciled] = useState(false);
    const [showReconcile, setShowReconcile] = useState(false);
    const [reconcileData, setReconcileData] = useState<{ name: string, guids: string[] } | null>(null);
    const [filterToday, setFilterToday] = useState(true);

    const loadData = () => {
        setLoading(true);
        let end: string | undefined = undefined;
        if (filterToday) {
            const now = new Date();
            end = `${now.getFullYear()}-${String(now.getMonth() + 1).padStart(2, '0')}-${String(now.getDate()).padStart(2, '0')}`;
        }
        
        getAccounts(undefined, end)
            .then(setAccounts)
            .catch(console.error)
            .finally(() => setLoading(false));
    };

    useEffect(() => { loadData(); }, [filterToday]);

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

    const handleReconcile = (account: Account) => {
        // Find the account in the tree to collect descendant GUIDs
        const tree = buildTree(accounts);
        const findInTree = (nodes: Account[]): Account | null => {
            for (const node of nodes) {
                if (node.guid === account.guid) return node;
                if (node.children) {
                    const found = findInTree(node.children);
                    if (found) return found;
                }
            }
            return null;
        };
        const treeNode = findInTree(tree);
        const guids = treeNode ? collectGuids(treeNode) : [account.guid];

        setReconcileData({ name: account.name, guids });
        setShowReconcile(true);
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
                <div style={{ display: 'flex', gap: 12, alignItems: 'center' }}>
                    
                    <div className="toggle-group-container" style={{ display: 'flex', gap: 8 }}>
                        <div
                            className="toggle-group"
                            style={{
                                display: 'inline-flex', borderRadius: 'var(--radius-sm)',
                                border: '1px solid var(--border-color)', overflow: 'hidden',
                            }}
                        >
                            <button
                                className={`toggle-btn ${filterToday ? 'active' : ''}`}
                                onClick={() => setFilterToday(true)}
                                style={{
                                    padding: '6px 14px', fontSize: '0.78rem', fontWeight: 500,
                                    border: 'none', cursor: 'pointer',
                                    background: filterToday ? 'var(--color-primary)' : 'var(--bg-tertiary)',
                                    color: filterToday ? '#fff' : 'var(--text-secondary)',
                                    transition: 'all 0.15s',
                                }}
                            >
                                {t('register.today')}
                            </button>
                            <button
                                className={`toggle-btn ${!filterToday ? 'active' : ''}`}
                                onClick={() => setFilterToday(false)}
                                style={{
                                    padding: '6px 14px', fontSize: '0.78rem', fontWeight: 500,
                                    border: 'none', cursor: 'pointer',
                                    background: !filterToday ? 'var(--color-primary)' : 'var(--bg-tertiary)',
                                    color: !filterToday ? '#fff' : 'var(--text-secondary)',
                                    transition: 'all 0.15s',
                                }}
                            >
                                {t('dashboard.total')}
                            </button>
                        </div>

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
                                {t('register.balance')}
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
                    onReconcile={handleReconcile}
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

            {showReconcile && reconcileData && (
                <ReconcileWizard
                    accountGuids={reconcileData.guids}
                    accountName={reconcileData.name}
                    onClose={() => setShowReconcile(false)}
                    onFinished={loadData}
                />
            )}
        </div>
    );
}
