import { useState, useEffect, useMemo } from 'react';
import type { RegisterEntry } from '../types';
import { t, formatCurrency, formatDate } from '../i18n';
import { getAccountRegister, getReconciledBalance, batchReconcileSplits } from '../api/client';
import { handleDateShortcut } from '../utils/date';

interface ReconcileWizardProps {
    accountGuids: string[];
    accountName: string;
    onClose: () => void;
    onFinished: () => void;
}

type Step = 'info' | 'select';

export default function ReconcileWizard({ accountGuids, accountName, onClose, onFinished }: ReconcileWizardProps) {
    const [step, setStep] = useState<Step>('info');
    const [statementDate, setStatementDate] = useState(() => new Date().toISOString().slice(0, 10));
    const [endingBalance, setEndingBalance] = useState('');
    const [startingBalance, setStartingBalance] = useState(0);
    const [entries, setEntries] = useState<RegisterEntry[]>([]);
    const [selected, setSelected] = useState<Set<string>>(new Set());
    const [loading, setLoading] = useState(false);
    const [error, setError] = useState<string | null>(null);

    // Load reconciled balances on mount
    const guidsKey = accountGuids.join(',');
    useEffect(() => {
        Promise.all(accountGuids.map(id => getReconciledBalance(id)))
            .then(data => {
                const total = data.reduce((sum, curr) => sum + curr.balance, 0);
                setStartingBalance(total);
            })
            .catch(console.error);
    }, [guidsKey]);

    const handleStartReconcile = async () => {
        if (!endingBalance.trim()) {
            setError(t('reconcile.endingRequired'));
            return;
        }
        setError(null);
        setLoading(true);
        try {
            const regs = await Promise.all(accountGuids.map(id => getAccountRegister(id)));
            const allRegs = regs.flat();
            // Only show unreconciled entries up to statement date
            const cutoff = new Date(statementDate + 'T23:59:59Z');
            const unreconciled = allRegs.filter(e =>
                e && e.reconcile_state !== 'y' && new Date(e.post_date) <= cutoff
            );
            unreconciled.sort((a, b) => new Date(a.post_date).getTime() - new Date(b.post_date).getTime());

            setEntries(unreconciled);
            setStep('select');
        } catch (err) {
            setError('Failed to load register');
        } finally {
            setLoading(false);
        }
    };

    const toggleEntry = (splitGuid: string) => {
        setSelected(prev => {
            const next = new Set(prev);
            if (next.has(splitGuid)) next.delete(splitGuid);
            else next.add(splitGuid);
            return next;
        });
    };

    const selectAll = () => {
        setSelected(new Set(entries.map(e => e.split_guid)));
    };

    const deselectAll = () => {
        setSelected(new Set());
    };

    // Separate deposits and withdrawals
    const deposits = useMemo(() => entries.filter(e => e.deposit != null), [entries]);
    const withdrawals = useMemo(() => entries.filter(e => e.withdrawal != null), [entries]);

    // Calculate totals of selected items
    const selectedDepositsTotal = useMemo(() =>
        deposits.filter(e => selected.has(e.split_guid)).reduce((s, e) => s + (e.deposit || 0), 0),
        [deposits, selected]
    );

    const selectedWithdrawalsTotal = useMemo(() =>
        withdrawals.filter(e => selected.has(e.split_guid)).reduce((s, e) => s + (e.withdrawal || 0), 0),
        [withdrawals, selected]
    );

    const endingNum = parseFloat(endingBalance) || 0;
    const reconciledBalance = startingBalance + selectedDepositsTotal - selectedWithdrawalsTotal;
    const difference = endingNum - reconciledBalance;
    const isBalanced = Math.abs(difference) < 0.005;

    const handleFinish = async () => {
        if (!isBalanced) return;
        setLoading(true);
        try {
            await batchReconcileSplits(Array.from(selected));
            onFinished();
            onClose();
        } catch (err) {
            setError('Failed to reconcile');
        } finally {
            setLoading(false);
        }
    };

    // Step 1: Reconcile Information
    if (step === 'info') {
        return (
            <div className="modal-overlay" onClick={onClose}>
                <div className="modal" onClick={e => e.stopPropagation()} style={{ maxWidth: 460 }}>
                    <h2 className="modal-title" style={{ marginBottom: 8 }}>
                        {accountName} — {t('reconcile.title')}
                    </h2>
                    <p style={{ color: 'var(--text-secondary)', fontSize: '0.85rem', marginBottom: 20 }}>
                        {t('reconcile.infoDesc')}
                    </p>

                    <div style={{ display: 'grid', gridTemplateColumns: 'auto 1fr', gap: '12px 16px', alignItems: 'center' }}>
                        <label className="form-label" style={{ margin: 0, textAlign: 'right' }}>
                            {t('reconcile.statementDate')}
                        </label>
                        <input
                            type="date"
                            className="form-input"
                            value={statementDate}
                            onChange={e => setStatementDate(e.target.value)}
                            onKeyDown={e => handleDateShortcut(e, statementDate, setStatementDate)}
                        />

                        <label className="form-label" style={{ margin: 0, textAlign: 'right' }}>
                            {t('reconcile.startingBalance')}
                        </label>
                        <div style={{ padding: '8px 12px', background: 'var(--bg-tertiary)', borderRadius: 'var(--radius-sm)', fontSize: '0.9rem' }}>
                            {formatCurrency(startingBalance)}
                        </div>

                        <label className="form-label" style={{ margin: 0, textAlign: 'right' }}>
                            {t('reconcile.endingBalance')}
                        </label>
                        <input
                            type="number"
                            step="0.01"
                            className="form-input"
                            placeholder="0.00"
                            value={endingBalance}
                            onChange={e => setEndingBalance(e.target.value)}
                            autoFocus
                        />
                    </div>

                    {error && (
                        <div style={{ marginTop: 12, padding: '8px 12px', background: 'rgba(244,63,94,0.1)', borderRadius: 'var(--radius-sm)', color: 'var(--color-expense)', fontSize: '0.85rem' }}>
                            {error}
                        </div>
                    )}

                    <div className="form-actions" style={{ marginTop: 20 }}>
                        <button className="btn btn-secondary" onClick={onClose}>{t('accounts.cancel')}</button>
                        <button className="btn btn-primary" onClick={handleStartReconcile} disabled={loading}>
                            {loading ? t('common.loading') : t('reconcile.start')}
                        </button>
                    </div>
                </div>
            </div>
        );
    }

    // Step 2: Select transactions
    const renderRow = (entry: RegisterEntry) => {
        const isChecked = selected.has(entry.split_guid);
        return (
            <tr
                key={entry.split_guid}
                onClick={() => toggleEntry(entry.split_guid)}
                style={{ cursor: 'pointer', background: isChecked ? 'rgba(99,102,241,0.08)' : undefined }}
            >
                <td style={{ textAlign: 'center' }}>
                    <input type="checkbox" checked={isChecked} onChange={() => { }} style={{ cursor: 'pointer' }} />
                </td>
                <td style={{ fontSize: '0.8rem', whiteSpace: 'nowrap' }}>{formatDate(entry.post_date)}</td>
                <td style={{ fontSize: '0.8rem', maxWidth: 200, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
                    {entry.description}
                </td>
                <td style={{ textAlign: 'right', fontSize: '0.8rem', fontVariantNumeric: 'tabular-nums' }}>
                    {entry.deposit != null ? formatCurrency(entry.deposit) : formatCurrency(entry.withdrawal || 0)}
                </td>
            </tr>
        );
    };

    return (
        <div className="modal-overlay" onClick={onClose}>
            <div className="modal" onClick={e => e.stopPropagation()} style={{ maxWidth: 900, maxHeight: '85vh', display: 'flex', flexDirection: 'column' }}>
                <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: 12 }}>
                    <h2 className="modal-title" style={{ margin: 0 }}>
                        {accountName} — {t('reconcile.title')}
                    </h2>
                    <div style={{ display: 'flex', gap: 8 }}>
                        <button className="btn btn-secondary" onClick={selectAll} style={{ fontSize: '0.75rem', padding: '4px 10px' }}>
                            {t('reconcile.selectAll')}
                        </button>
                        <button className="btn btn-secondary" onClick={deselectAll} style={{ fontSize: '0.75rem', padding: '4px 10px' }}>
                            {t('reconcile.deselectAll')}
                        </button>
                    </div>
                </div>

                <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 16, flex: 1, overflow: 'hidden' }}>
                    {/* Funds In */}
                    <div style={{ display: 'flex', flexDirection: 'column', overflow: 'hidden' }}>
                        <h3 style={{ fontSize: '0.85rem', fontWeight: 600, marginBottom: 8, color: 'var(--color-income)' }}>
                            {t('reconcile.fundsIn')}
                        </h3>
                        <div style={{ flex: 1, overflow: 'auto', borderRadius: 'var(--radius-sm)', border: '1px solid var(--border-color)' }}>
                            <table style={{ width: '100%', borderCollapse: 'collapse' }}>
                                <thead>
                                    <tr style={{ borderBottom: '1px solid var(--border-color)' }}>
                                        <th style={{ width: 30, padding: '6px 4px' }}></th>
                                        <th style={{ padding: '6px 8px', textAlign: 'left', fontSize: '0.75rem', textTransform: 'uppercase', color: 'var(--text-muted)' }}>{t('register.date')}</th>
                                        <th style={{ padding: '6px 8px', textAlign: 'left', fontSize: '0.75rem', textTransform: 'uppercase', color: 'var(--text-muted)' }}>{t('register.description')}</th>
                                        <th style={{ padding: '6px 8px', textAlign: 'right', fontSize: '0.75rem', textTransform: 'uppercase', color: 'var(--text-muted)' }}>{t('reconcile.amount')}</th>
                                    </tr>
                                </thead>
                                <tbody>
                                    {deposits.map(renderRow)}
                                </tbody>
                            </table>
                        </div>
                        <div style={{ textAlign: 'right', padding: '8px 0', fontSize: '0.85rem', fontWeight: 600, color: 'var(--color-income)' }}>
                            {t('reconcile.total')}: {formatCurrency(selectedDepositsTotal)}
                        </div>
                    </div>

                    {/* Funds Out */}
                    <div style={{ display: 'flex', flexDirection: 'column', overflow: 'hidden' }}>
                        <h3 style={{ fontSize: '0.85rem', fontWeight: 600, marginBottom: 8, color: 'var(--color-expense)' }}>
                            {t('reconcile.fundsOut')}
                        </h3>
                        <div style={{ flex: 1, overflow: 'auto', borderRadius: 'var(--radius-sm)', border: '1px solid var(--border-color)' }}>
                            <table style={{ width: '100%', borderCollapse: 'collapse' }}>
                                <thead>
                                    <tr style={{ borderBottom: '1px solid var(--border-color)' }}>
                                        <th style={{ width: 30, padding: '6px 4px' }}></th>
                                        <th style={{ padding: '6px 8px', textAlign: 'left', fontSize: '0.75rem', textTransform: 'uppercase', color: 'var(--text-muted)' }}>{t('register.date')}</th>
                                        <th style={{ padding: '6px 8px', textAlign: 'left', fontSize: '0.75rem', textTransform: 'uppercase', color: 'var(--text-muted)' }}>{t('register.description')}</th>
                                        <th style={{ padding: '6px 8px', textAlign: 'right', fontSize: '0.75rem', textTransform: 'uppercase', color: 'var(--text-muted)' }}>{t('reconcile.amount')}</th>
                                    </tr>
                                </thead>
                                <tbody>
                                    {withdrawals.map(renderRow)}
                                </tbody>
                            </table>
                        </div>
                        <div style={{ textAlign: 'right', padding: '8px 0', fontSize: '0.85rem', fontWeight: 600, color: 'var(--color-expense)' }}>
                            {t('reconcile.total')}: {formatCurrency(selectedWithdrawalsTotal)}
                        </div>
                    </div>
                </div>

                {/* Summary bar */}
                <div style={{
                    marginTop: 12, padding: '12px 16px',
                    background: 'var(--bg-tertiary)',
                    borderRadius: 'var(--radius-sm)',
                    display: 'grid', gridTemplateColumns: 'auto 1fr', gap: '4px 20px',
                    fontSize: '0.85rem',
                }}>
                    <span style={{ color: 'var(--text-muted)', textAlign: 'right' }}>{t('reconcile.statementDate')}:</span>
                    <span>{statementDate}</span>
                    <span style={{ color: 'var(--text-muted)', textAlign: 'right' }}>{t('reconcile.startingBalance')}:</span>
                    <span>{formatCurrency(startingBalance)}</span>
                    <span style={{ color: 'var(--text-muted)', textAlign: 'right' }}>{t('reconcile.endingBalance')}:</span>
                    <span>{formatCurrency(endingNum)}</span>
                    <span style={{ color: 'var(--text-muted)', textAlign: 'right' }}>{t('reconcile.reconciledBalance')}:</span>
                    <span>{formatCurrency(reconciledBalance)}</span>
                    <span style={{ color: 'var(--text-muted)', textAlign: 'right', fontWeight: 700 }}>{t('reconcile.difference')}:</span>
                    <span style={{ fontWeight: 700, color: isBalanced ? 'var(--color-income)' : 'var(--color-expense)' }}>
                        {formatCurrency(difference)}
                    </span>
                </div>

                {error && (
                    <div style={{ marginTop: 8, padding: '8px 12px', background: 'rgba(244,63,94,0.1)', borderRadius: 'var(--radius-sm)', color: 'var(--color-expense)', fontSize: '0.85rem' }}>
                        {error}
                    </div>
                )}

                <div className="form-actions" style={{ marginTop: 12 }}>
                    <button className="btn btn-secondary" onClick={onClose}>{t('accounts.cancel')}</button>
                    <button
                        className="btn btn-primary"
                        onClick={handleFinish}
                        disabled={!isBalanced || loading || selected.size === 0}
                        style={{ opacity: isBalanced && selected.size > 0 ? 1 : 0.5 }}
                    >
                        {loading ? t('common.loading') : t('reconcile.finish')}
                    </button>
                </div>
            </div>
        </div>
    );
}
