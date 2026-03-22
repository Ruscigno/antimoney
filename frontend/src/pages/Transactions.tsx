import { useState, useEffect } from 'react';
import { getTransactions, deleteTransaction } from '../api/client';
import TransactionForm from '../components/TransactionForm';
import type { Transaction } from '../types';
import { t, formatCurrency, formatDate } from '../i18n';
import { useShortcut } from '../hooks/useShortcuts';

function formatSplitCurrency(value: number, denom: number): string {
    return formatCurrency(value / denom);
}

export default function Transactions() {
    const [transactions, setTransactions] = useState<Transaction[]>([]);
    const [loading, setLoading] = useState(true);
    const [showForm, setShowForm] = useState(false);
    const [editTxGuid, setEditTxGuid] = useState<string | null>(null);

    // N shortcut opens new transaction form
    useShortcut('n', () => setShowForm(true), t('shortcuts.newTx'), undefined, []);

    const loadData = () => {
        setLoading(true);
        getTransactions()
            .then(t => {
                const sorted = (t || []).sort((a, b) => new Date(b.post_date).getTime() - new Date(a.post_date).getTime());
                setTransactions(sorted);
            })
            .catch(console.error)
            .finally(() => setLoading(false));
    };

    useEffect(() => { loadData(); }, []);

    const handleDelete = async (guid: string) => {
        if (!confirm(t('transactions.confirmDelete'))) return;
        try {
            await deleteTransaction(guid);
            loadData();
        } catch (err) {
            console.error(err);
        }
    };

    if (loading) {
        return <div className="loading"><div className="loading-spinner" />{t('common.loading')}</div>;
    }

    const todayStr = new Date().toISOString().split('T')[0];

    const getRowClass = (postDateStr: string) => {
        const d = postDateStr.split('T')[0];
        let timeClass = '';
        if (d < todayStr) timeClass = 'row-past';
        else if (d === todayStr) timeClass = 'row-today';
        else timeClass = 'row-future';

        return ['hoverable-row', timeClass].filter(Boolean).join(' ');
    };

    return (
        <div>
            <div className="page-header" style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'flex-start' }}>
                <div>
                    <h1 className="page-title">{t('transactions.title')}</h1>
                    <p className="page-subtitle">{t('transactions.subtitle')}</p>
                </div>
                <button className="btn btn-primary" onClick={() => setShowForm(true)} id="btn-new-tx-global">
                    {t('common.newTransaction')}
                    <kbd className="kbd-hint" style={{ marginLeft: 6 }}>N</kbd>
                </button>
            </div>

            {transactions.length === 0 ? (
                <div className="empty-state">
                    <div className="empty-state-icon">📝</div>
                    <p>{t('transactions.noTransactions')}</p>
                </div>
            ) : (
                <div className="register-table-wrapper">
                    <table className="register-table">
                        <thead>
                            <tr>
                                <th className="col-num"># or ID</th>
                                <th>{t('register.date')}</th>
                                <th>{t('register.description')}</th>
                                <th>Splits</th>
                                <th></th>
                            </tr>
                        </thead>
                        <tbody>
                            {transactions.map((txn, idx) => (
                                <tr
                                    key={txn.guid}
                                    onClick={() => setEditTxGuid(txn.guid)}
                                    style={{ cursor: 'pointer' }}
                                    className={getRowClass(txn.post_date)}
                                >
                                    <td className="col-num">{txn.custom_id || idx + 1}</td>
                                    <td className="col-date">{formatDate(txn.post_date)}</td>
                                    <td className="col-description">{txn.description}</td>
                                    <td>
                                        <div style={{ display: 'flex', flexDirection: 'column', gap: 2, fontSize: '0.8rem' }}>
                                            {txn.splits?.map(s => (
                                                <span key={s.guid} style={{ color: s.value_num >= 0 ? 'var(--color-income)' : 'var(--color-expense)' }}>
                                                    {s.account_name || s.account_guid.slice(0, 8)} → {formatSplitCurrency(s.value_num, s.value_denom)}
                                                </span>
                                            ))}
                                        </div>
                                    </td>
                                    <td>
                                        <button className="btn btn-danger" onClick={(e) => { e.stopPropagation(); handleDelete(txn.guid); }} style={{ padding: '4px 10px', fontSize: '0.75rem' }}>
                                            {t('transactions.delete')}
                                        </button>
                                    </td>
                                </tr>
                            ))}
                        </tbody>
                    </table>
                </div>
            )}

            {(showForm || editTxGuid) && (
                <TransactionForm
                    onClose={() => { setShowForm(false); setEditTxGuid(null); }}
                    onCreated={loadData}
                    editTxGuid={editTxGuid || undefined}
                />
            )}
        </div>
    );
}
