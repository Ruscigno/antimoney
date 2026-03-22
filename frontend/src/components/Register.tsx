import { useNavigate } from 'react-router-dom';
import { useRef, useEffect, useCallback } from 'react';
import type { RegisterEntry, AccountType } from '../types';
import { t, formatCurrency, formatDate } from '../i18n';
import { toggleSplitAcknowledge } from '../api/client';

interface RegisterProps {
    entries: RegisterEntry[];
    accountName: string;
    accountType?: AccountType;
    onReconcileStateChanged?: (splitGuid: string, newState: string) => void;
    onEditTransaction?: (guid: string) => void;
    onDeleteTransaction?: (guid: string) => void;
    hasBefore?: boolean;
    hasAfter?: boolean;
    onLoadMore?: (direction: 'before' | 'after') => void;
    loadingMore?: boolean;
}

export default function Register({
    entries, accountName, accountType,
    onReconcileStateChanged, onEditTransaction, onDeleteTransaction,
    hasBefore, hasAfter, onLoadMore, loadingMore,
}: RegisterProps) {
    const navigate = useNavigate();
    const todayRef = useRef<HTMLTableRowElement>(null);
    const scrollContainerRef = useRef<HTMLDivElement>(null);
    const hasScrolledRef = useRef(false);

    // Scroll to today on initial load
    useEffect(() => {
        if (todayRef.current && !hasScrolledRef.current) {
            const timer = setTimeout(() => {
                todayRef.current?.scrollIntoView({ behavior: 'smooth', block: 'center' });
                hasScrolledRef.current = true;
            }, 100);
            return () => clearTimeout(timer);
        }
    }, [entries]);

    // Reset scroll tracking when entries are fully replaced (e.g. navigating to a new account)
    useEffect(() => {
        hasScrolledRef.current = false;
    }, [accountName]);

    // Infinite scroll handler
    const handleScroll = useCallback(() => {
        const container = scrollContainerRef.current;
        if (!container || loadingMore) return;

        const { scrollTop, scrollHeight, clientHeight } = container;

        // Load more above when near top
        if (scrollTop < 100 && hasBefore && onLoadMore) {
            const prevScrollHeight = scrollHeight;
            onLoadMore('before');
            // After loading, preserve scroll position
            requestAnimationFrame(() => {
                if (scrollContainerRef.current) {
                    const newScrollHeight = scrollContainerRef.current.scrollHeight;
                    scrollContainerRef.current.scrollTop = scrollTop + (newScrollHeight - prevScrollHeight);
                }
            });
        }

        // Load more below when near bottom
        if (scrollTop + clientHeight > scrollHeight - 100 && hasAfter && onLoadMore) {
            onLoadMore('after');
        }
    }, [hasBefore, hasAfter, onLoadMore, loadingMore]);

    useEffect(() => {
        const container = scrollContainerRef.current;
        if (!container) return;
        container.addEventListener('scroll', handleScroll, { passive: true });
        return () => container.removeEventListener('scroll', handleScroll);
    }, [handleScroll]);

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
            // Update locally — no full reload
            onReconcileStateChanged?.(splitGuid, newState);
        } catch (err) {
            console.error('Failed to toggle reconcile:', err);
        }
    };

    // Use local date for today comparison
    const now = new Date();
    const todayStr = `${now.getFullYear()}-${String(now.getMonth() + 1).padStart(2, '0')}-${String(now.getDate()).padStart(2, '0')}`;

    // Determine column names based on account type
    const isLiability = accountType === 'LIABILITY' || accountType === 'CREDIT';
    const isCreditNormal = isLiability || accountType === 'INCOME' || accountType === 'EQUITY';
    
    const depositLabel = isLiability ? t('register.decrease') : t('register.deposit');
    const withdrawalLabel = isLiability ? t('register.increase') : t('register.withdrawal');

    const getRowClass = (postDateStr: string, isHoverable: boolean) => {
        const d = postDateStr.split('T')[0];
        let timeClass = '';
        if (d < todayStr) timeClass = 'row-past';
        else if (d === todayStr) timeClass = 'row-today';
        else timeClass = 'row-future';

        return [isHoverable ? 'hoverable-row' : '', timeClass].filter(Boolean).join(' ');
    };

    // Find the best entry to scroll to on initial load:
    let focusIndex = entries.length > 0 ? entries.length - 1 : -1;
    for (let i = 0; i < entries.length; i++) {
        const d = entries[i].post_date.split('T')[0];
        if (d >= todayStr) {
            focusIndex = d === todayStr ? i : Math.max(0, i - 1);
            break;
        }
    }

    return (
        <div className="register-table-wrapper" ref={scrollContainerRef}>
            {loadingMore && hasBefore && (
                <div className="register-loading-indicator" style={{ textAlign: 'center', padding: '8px 0' }}>
                    <div className="loading-spinner" style={{ width: 16, height: 16, display: 'inline-block', marginRight: 8 }} />
                    {t('common.loading')}
                </div>
            )}
            {hasBefore && !loadingMore && (
                <div style={{ textAlign: 'center', padding: '6px 0' }}>
                    <button
                        className="btn btn-ghost btn-sm"
                        onClick={() => onLoadMore?.('before')}
                        style={{ fontSize: '0.8rem', opacity: 0.7 }}
                    >
                        ↑ {t('register.loadOlder')}
                    </button>
                </div>
            )}
            <table className="register-table">
                <thead>
                    <tr>
                        <th className="col-num">{t('register.num')}</th>
                        <th className="col-date">{t('register.date')}</th>
                        <th>{t('register.description')}</th>
                        <th>{t('register.transfer')}</th>
                        <th style={{ textAlign: 'center', width: 36 }}>{t('register.reconcile')}</th>
                        <th style={{ textAlign: 'right' }}>{depositLabel}</th>
                        <th style={{ textAlign: 'right' }}>{withdrawalLabel}</th>
                        <th style={{ textAlign: 'right' }}>{t('register.balance')}</th>
                        <th>{t('register.memo')}</th>
                        <th style={{ width: 32 }}></th>
                    </tr>
                </thead>
                <tbody>
                    {entries.map((entry, i) => {
                        const postDate = new Date(entry.post_date);
                        const postDateStr = `${postDate.getFullYear()}-${String(postDate.getMonth() + 1).padStart(2, '0')}-${String(postDate.getDate()).padStart(2, '0')}`;
                        const displayBalance = isCreditNormal ? -entry.balance : entry.balance;
                        const isZero = Math.abs(displayBalance) < 0.005;
                        const finalBalance = isZero ? 0 : displayBalance;
                        
                        const handleDelete = (e: React.MouseEvent) => {
                            e.stopPropagation();
                            if (window.confirm(t('transactions.confirmDelete'))) {
                                onDeleteTransaction?.(entry.transaction_guid);
                            }
                        };

                        return (
                            <tr
                                key={`${entry.transaction_guid}-${i}`}
                                ref={i === focusIndex ? todayRef : null}
                                onClick={() => onEditTransaction?.(entry.transaction_guid)}
                                style={{ cursor: onEditTransaction ? 'pointer' : 'default' }}
                                className={getRowClass(postDateStr, !!onEditTransaction)}
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
                                    {isLiability 
                                        ? (entry.withdrawal != null ? formatCurrency(entry.withdrawal) : '')
                                        : (entry.deposit != null ? formatCurrency(entry.deposit) : '')
                                    }
                                </td>
                                <td className="col-withdrawal">
                                    {isLiability
                                        ? (entry.deposit != null ? formatCurrency(entry.deposit) : '')
                                        : (entry.withdrawal != null ? formatCurrency(entry.withdrawal) : '')
                                    }
                                </td>
                                <td className="col-balance" style={{ color: isZero ? 'var(--text-muted)' : (finalBalance >= 0 ? 'var(--color-income)' : 'var(--color-expense)') }}>
                                    {formatCurrency(finalBalance)}
                                </td>
                                <td className="col-memo">{entry.split_memo}</td>
                                <td style={{ textAlign: 'center' }}>
                                    {onDeleteTransaction && (
                                        <button
                                            className="btn-delete-row"
                                            onClick={handleDelete}
                                            title={t('transactions.delete')}
                                            style={{
                                                background: 'none', border: 'none', cursor: 'pointer',
                                                color: 'var(--color-expense)', opacity: 0.4,
                                                fontSize: '0.9rem', transition: 'opacity 0.2s',
                                                padding: '4px'
                                            }}
                                            onMouseEnter={e => { (e.target as HTMLButtonElement).style.opacity = '1'; }}
                                            onMouseLeave={e => { (e.target as HTMLButtonElement).style.opacity = '0.4'; }}
                                        >
                                            🗑
                                        </button>
                                    )}
                                </td>
                            </tr>
                        );
                    })}
                </tbody>
            </table>
            {hasAfter && !loadingMore && (
                <div style={{ textAlign: 'center', padding: '6px 0' }}>
                    <button
                        className="btn btn-ghost btn-sm"
                        onClick={() => onLoadMore?.('after')}
                        style={{ fontSize: '0.8rem', opacity: 0.7 }}
                    >
                        ↓ {t('register.loadNewer')}
                    </button>
                </div>
            )}
            {loadingMore && hasAfter && (
                <div className="register-loading-indicator" style={{ textAlign: 'center', padding: '8px 0' }}>
                    <div className="loading-spinner" style={{ width: 16, height: 16, display: 'inline-block', marginRight: 8 }} />
                    {t('common.loading')}
                </div>
            )}
        </div>
    );
}
