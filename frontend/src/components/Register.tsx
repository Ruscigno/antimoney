import { useNavigate } from 'react-router-dom';
import type { RegisterEntry } from '../types';
import { t, formatCurrency, formatDate } from '../i18n';
import { toggleSplitAcknowledge } from '../api/client';

interface RegisterProps {
    entries: RegisterEntry[];
    accountName: string;
    onReconcileChanged?: () => void;
    onEditTransaction?: (guid: string) => void;
}

export default function Register({ entries, accountName, onReconcileChanged, onEditTransaction }: RegisterProps) {
    const navigate = useNavigate();

    if (!entries || entries.length === 0) {
        return (
            <div className="empty-state">
                <div className="empty-state-icon">📋</div>
                <p>{t('register.noEntries')} <strong>{accountName}</strong></p>
            </div>
        );
    }

    const reconcileIcon = (state: string) => {
        switch (state) {
            case 'y': return '●';
            case 'c': return '◐';
            default: return '○';
        }
    };
    const reconcileColor = (state: string) => {
        switch (state) {
            case 'y': return 'var(--color-income)';
            case 'c': return 'var(--color-info, #60a5fa)';
            default: return 'var(--text-muted)';
        }
    };
    const reconcileTooltip = (state: string) => {
        switch (state) {
            case 'y': return t('register.reconcile.tooltip.y');
            case 'c': return t('register.reconcile.tooltip.c');
            default: return t('register.reconcile.tooltip.n');
        }
    };

    // Click logic: n→c, c→n, y→n (can never set to y here)
    const handleReconcileClick = async (splitGuid: string, currentState: string) => {
        let newState: string;
        if (currentState === 'n') newState = 'c';
        else newState = 'n'; // c→n or y→n
        try {
            await toggleSplitAcknowledge(splitGuid, newState);
            onReconcileChanged?.();
        } catch (err) {
            console.error('Failed to toggle reconcile:', err);
        }
    };

    const todayStr = new Date().toISOString().split('T')[0];

    const getRowClass = (postDateStr: string, isHoverable: boolean) => {
        const d = postDateStr.split('T')[0];
        let timeClass = '';
        if (d < todayStr) timeClass = 'row-past';
        else if (d === todayStr) timeClass = 'row-today';
        else timeClass = 'row-future';

        return [isHoverable ? 'hoverable-row' : '', timeClass].filter(Boolean).join(' ');
    };

    return (
        <div className="register-table-wrapper">
            <table className="register-table">
                <thead>
                    <tr>
                        <th className="col-num">{t('register.num')}</th>
                        <th className="col-date">{t('register.date')}</th>
                        <th>{t('register.description')}</th>
                        <th>{t('register.transfer')}</th>
                        <th style={{ textAlign: 'center', width: 36 }}>{t('register.reconcile')}</th>
                        <th style={{ textAlign: 'right' }}>{t('register.deposit')}</th>
                        <th style={{ textAlign: 'right' }}>{t('register.withdrawal')}</th>
                        <th style={{ textAlign: 'right' }}>{t('register.balance')}</th>
                        <th>{t('register.memo')}</th>
                    </tr>
                </thead>
                <tbody>
                    {entries.map((entry, i) => (
                        <tr
                            key={`${entry.transaction_guid}-${i}`}
                            onClick={() => onEditTransaction?.(entry.transaction_guid)}
                            style={{ cursor: onEditTransaction ? 'pointer' : 'default' }}
                            className={getRowClass(entry.post_date, !!onEditTransaction)}
                        >
                            <td className="col-num">{entry.custom_id || i + 1}</td>
                            <td className="col-date">{formatDate(entry.post_date)}</td>
                            <td className="col-description">{entry.description}</td>
                            <td className="col-transfer">
                                {entry.transfer_account_guid ? (
                                    <span style={{ display: 'inline-flex', alignItems: 'center', gap: 6 }}>
                                        <span>{entry.transfer_account}</span>
                                        <button
                                            className="btn-icon btn-jump"
                                            title={t('register.jump')}
                                            onClick={() => navigate(`/accounts/${entry.transfer_account_guid}`)}
                                            style={{
                                                background: 'none', border: 'none', cursor: 'pointer',
                                                padding: '2px 4px', fontSize: '0.75rem', color: 'var(--text-muted)',
                                                borderRadius: 4, transition: 'color 0.15s, background 0.15s',
                                            }}
                                            onMouseEnter={e => {
                                                (e.target as HTMLButtonElement).style.color = 'var(--color-primary)';
                                                (e.target as HTMLButtonElement).style.background = 'rgba(99,102,241,0.1)';
                                            }}
                                            onMouseLeave={e => {
                                                (e.target as HTMLButtonElement).style.color = 'var(--text-muted)';
                                                (e.target as HTMLButtonElement).style.background = 'none';
                                            }}
                                        >
                                            ↗
                                        </button>
                                    </span>
                                ) : (
                                    <span>{entry.transfer_account}</span>
                                )}
                            </td>
                            <td style={{ textAlign: 'center' }} onClick={e => e.stopPropagation()}>
                                <button
                                    onClick={() => handleReconcileClick(entry.split_guid, entry.reconcile_state)}
                                    title={reconcileTooltip(entry.reconcile_state)}
                                    style={{
                                        background: 'none', border: 'none', cursor: 'pointer',
                                        fontSize: '1rem', color: reconcileColor(entry.reconcile_state),
                                        padding: '2px 6px', borderRadius: 4,
                                        transition: 'transform 0.12s',
                                    }}
                                    onMouseEnter={e => { (e.target as HTMLElement).style.transform = 'scale(1.3)'; }}
                                    onMouseLeave={e => { (e.target as HTMLElement).style.transform = 'scale(1)'; }}
                                >
                                    {reconcileIcon(entry.reconcile_state)}
                                </button>
                            </td>
                            <td className="col-deposit">
                                {entry.deposit != null ? formatCurrency(entry.deposit) : ''}
                            </td>
                            <td className="col-withdrawal">
                                {entry.withdrawal != null ? formatCurrency(entry.withdrawal) : ''}
                            </td>
                            <td className="col-balance" style={{ color: entry.balance >= 0 ? 'var(--color-income)' : 'var(--color-expense)' }}>
                                {formatCurrency(entry.balance)}
                            </td>
                            <td className="col-memo">{entry.split_memo}</td>
                        </tr>
                    ))}
                </tbody>
            </table>
        </div>
    );
}
