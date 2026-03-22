import { useNavigate } from 'react-router-dom';
import type { RegisterEntry } from '../types';
import { t, formatCurrency, formatDate } from '../i18n';
import { updateSplitReconcileState } from '../api/client';

interface RegisterProps {
    entries: RegisterEntry[];
    accountName: string;
    onReconcileChanged?: () => void;
}

const RECONCILE_CYCLE: Record<string, string> = { n: 'c', c: 'y', y: 'n' };

export default function Register({ entries, accountName, onReconcileChanged }: RegisterProps) {
    const navigate = useNavigate();

    if (!entries || entries.length === 0) {
        return (
            <div className="empty-state">
                <div className="empty-state-icon">📋</div>
                <p>{t('register.noEntries')} <strong>{accountName}</strong></p>
            </div>
        );
    }

    const handleReconcileClick = async (splitGuid: string, currentState: string) => {
        const nextState = RECONCILE_CYCLE[currentState] || 'n';
        try {
            await updateSplitReconcileState(splitGuid, nextState);
            onReconcileChanged?.();
        } catch (err) {
            console.error('Failed to update reconcile state:', err);
        }
    };

    const reconcileIcon = (state: string) => t(`register.reconcile.${state}` as any) || '○';
    const reconcileTooltip = (state: string) => t(`register.reconcile.tooltip.${state}` as any) || '';
    const reconcileColor = (state: string) => {
        switch (state) {
            case 'y': return 'var(--color-income)';
            case 'c': return 'var(--color-info, #60a5fa)';
            default: return 'var(--text-muted)';
        }
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
                        <tr key={`${entry.transaction_guid}-${i}`}>
                            <td className="col-num">{i + 1}</td>
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
                                                background: 'none',
                                                border: 'none',
                                                cursor: 'pointer',
                                                padding: '2px 4px',
                                                fontSize: '0.75rem',
                                                color: 'var(--text-muted)',
                                                borderRadius: 4,
                                                transition: 'color 0.15s, background 0.15s',
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
                            <td style={{ textAlign: 'center' }}>
                                <button
                                    className="btn-reconcile"
                                    title={reconcileTooltip(entry.reconcile_state)}
                                    onClick={() => handleReconcileClick(entry.split_guid, entry.reconcile_state)}
                                    style={{
                                        background: 'none',
                                        border: 'none',
                                        cursor: 'pointer',
                                        fontSize: '1rem',
                                        color: reconcileColor(entry.reconcile_state),
                                        padding: '2px 6px',
                                        borderRadius: 4,
                                        transition: 'transform 0.15s',
                                    }}
                                    onMouseEnter={e => { (e.target as HTMLButtonElement).style.transform = 'scale(1.3)'; }}
                                    onMouseLeave={e => { (e.target as HTMLButtonElement).style.transform = 'scale(1)'; }}
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
