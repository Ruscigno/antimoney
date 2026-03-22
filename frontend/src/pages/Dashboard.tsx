import { useState, useEffect, useMemo } from 'react';
import { getAccounts } from '../api/client';
import type { Account, AccountType } from '../types';
import { ACCOUNT_TYPE_COLORS } from '../types';
import { t, formatCurrency } from '../i18n';

interface CategorySummary {
    label: string;
    total: number;
    reconciledTotal: number;
    cssClass: string;
    icon: string;
}

/** Individual account with its balance for the bar chart */
interface BarItem {
    name: string;
    value: number;
    reconciledValue: number;
    color: string;
    accountType: AccountType;
}

function computeSummaries(accounts: Account[]): {
    summaries: CategorySummary[];
    barItems: BarItem[];
} {
    const sums: Record<string, number> = {
        ASSET: 0, BANK: 0, CASH: 0,
        LIABILITY: 0, CREDIT: 0,
        INCOME: 0,
        EXPENSE: 0,
    };
    const reconSums: Record<string, number> = { ...sums };
    Object.keys(reconSums).forEach(k => reconSums[k] = 0);

    const bars: BarItem[] = [];

    accounts.forEach(a => {
        if (a.account_type in sums) {
            sums[a.account_type] += (a.balance || 0);
            reconSums[a.account_type] += (a.reconciled_balance || 0);
        }
        // Only show non-placeholder leaf accounts with non-zero balance in bar chart
        if (!a.placeholder && a.account_type !== 'ROOT' && a.account_type !== 'EQUITY') {
            const val = Math.abs(a.balance || 0);
            const rVal = Math.abs(a.reconciled_balance || 0);
            if (val > 0.005 || rVal > 0.005) {
                bars.push({
                    name: a.name,
                    value: val,
                    reconciledValue: rVal,
                    color: ACCOUNT_TYPE_COLORS[a.account_type] || '#64748b',
                    accountType: a.account_type,
                });
            }
        }
    });

    const assets = sums.ASSET + sums.BANK + sums.CASH;
    const liabilities = sums.LIABILITY + sums.CREDIT;
    const income = Math.abs(sums.INCOME);
    const expenses = sums.EXPENSE;

    const rAssets = reconSums.ASSET + reconSums.BANK + reconSums.CASH;
    const rLiabilities = reconSums.LIABILITY + reconSums.CREDIT;
    const rIncome = Math.abs(reconSums.INCOME);
    const rExpenses = reconSums.EXPENSE;

    return {
        summaries: [
            { label: t('dashboard.assets'), total: assets, reconciledTotal: rAssets, cssClass: 'asset', icon: '🏦' },
            { label: t('dashboard.liabilities'), total: Math.abs(liabilities), reconciledTotal: Math.abs(rLiabilities), cssClass: 'liability', icon: '💳' },
            { label: t('dashboard.income'), total: income, reconciledTotal: rIncome, cssClass: 'income', icon: '📈' },
            { label: t('dashboard.expenses'), total: expenses, reconciledTotal: rExpenses, cssClass: 'expense', icon: '📉' },
        ],
        barItems: bars.sort((a, b) => b.value - a.value).slice(0, 8),
    };
}

/** Simple horizontal bar chart using SVG */
function BarChart({ items, showReconciled }: { items: BarItem[]; showReconciled: boolean }) {
    if (items.length === 0) return null;

    const maxVal = Math.max(...items.map(i => showReconciled ? i.reconciledValue : i.value), 1);
    const barH = 28;
    const gap = 6;
    const labelW = 130;
    const valueW = 90;
    const chartW = 400;
    const totalH = items.length * (barH + gap);

    return (
        <svg viewBox={`0 0 ${labelW + chartW + valueW + 20} ${totalH + 10}`} style={{ width: '100%', height: totalH + 10 }}>
            {items.map((item, i) => {
                const val = showReconciled ? item.reconciledValue : item.value;
                const barW = (val / maxVal) * chartW;
                const y = i * (barH + gap) + 5;
                return (
                    <g key={item.name + i}>
                        {/* Label */}
                        <text x={labelW - 8} y={y + barH / 2 + 4} textAnchor="end"
                            fill="var(--text-secondary)" fontSize="11" fontFamily="var(--font-sans, Inter, sans-serif)">
                            {item.name.length > 16 ? item.name.slice(0, 15) + '…' : item.name}
                        </text>
                        {/* Bar background */}
                        <rect x={labelW} y={y + 2} width={chartW} height={barH - 4} rx={4}
                            fill="var(--bg-tertiary)" />
                        {/* Bar fill — animated */}
                        <rect x={labelW} y={y + 2} width={barW} height={barH - 4} rx={4}
                            fill={item.color} opacity={0.85}>
                            <animate attributeName="width" from="0" to={barW} dur="0.6s" fill="freeze" />
                        </rect>
                        {/* Value */}
                        <text x={labelW + chartW + 8} y={y + barH / 2 + 4} textAnchor="start"
                            fill="var(--text-primary)" fontSize="11" fontWeight="600"
                            fontFamily="var(--font-sans, Inter, sans-serif)">
                            {formatCurrency(val)}
                        </text>
                    </g>
                );
            })}
        </svg>
    );
}

/** Mini donut chart for net worth visualization */
function DonutChart({ value, max, color }: { value: number; max: number; color: string }) {
    const r = 38;
    const circumference = 2 * Math.PI * r;
    const pct = max > 0 ? Math.min(value / max, 1) : 0;
    const dashLen = pct * circumference;

    return (
        <svg viewBox="0 0 100 100" style={{ width: 90, height: 90 }}>
            <circle cx="50" cy="50" r={r} fill="none" stroke="var(--bg-tertiary)" strokeWidth="8" />
            <circle cx="50" cy="50" r={r} fill="none" stroke={color} strokeWidth="8"
                strokeDasharray={`${dashLen} ${circumference}`}
                strokeLinecap="round" transform="rotate(-90 50 50)">
                <animate attributeName="stroke-dasharray" from={`0 ${circumference}`}
                    to={`${dashLen} ${circumference}`} dur="0.8s" fill="freeze" />
            </circle>
        </svg>
    );
}

export default function Dashboard() {
    const [accounts, setAccounts] = useState<Account[]>([]);
    const [loading, setLoading] = useState(true);
    const [showReconciled, setShowReconciled] = useState(false);

    useEffect(() => {
        getAccounts()
            .then(setAccounts)
            .catch(console.error)
            .finally(() => setLoading(false));
    }, []);

    const { summaries, barItems } = useMemo(() => computeSummaries(accounts), [accounts]);

    if (loading) {
        return <div className="loading"><div className="loading-spinner" />{t('common.loading')}</div>;
    }

    const displaySummaries = summaries.map(s => ({
        ...s,
        displayValue: showReconciled ? s.reconciledTotal : s.total,
    }));

    const assetVal = showReconciled ? summaries[0].reconciledTotal : summaries[0].total;
    const liabVal = showReconciled ? summaries[1].reconciledTotal : summaries[1].total;
    const netWorth = assetVal - liabVal;
    const totalAbs = assetVal + liabVal;

    return (
        <div>
            {/* Header with toggle */}
            <div className="page-header" style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'flex-start' }}>
                <div>
                    <h1 className="page-title">{t('dashboard.title')}</h1>
                    <p className="page-subtitle">{t('dashboard.subtitle')}</p>
                </div>
                <div
                    className="toggle-group"
                    style={{
                        display: 'inline-flex', borderRadius: 'var(--radius-sm)',
                        border: '1px solid var(--border-color)', overflow: 'hidden',
                    }}
                >
                    <button
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

            {/* Summary cards */}
            <div className="stats-grid">
                {displaySummaries.map(s => (
                    <div key={s.label} className={`card stat-card ${s.cssClass}`}>
                        <div className="card-title">{s.icon} {s.label}</div>
                        <div className={`card-value ${s.displayValue >= 0 ? 'positive' : 'negative'}`}>
                            {formatCurrency(s.displayValue)}
                        </div>
                    </div>
                ))}
            </div>

            {/* Main content: net worth + chart */}
            <div style={{ display: 'grid', gridTemplateColumns: '320px 1fr', gap: 20, marginTop: 4 }}>
                {/* Net Worth card with donut */}
                <div className="card" style={{ display: 'flex', flexDirection: 'column', alignItems: 'center', justifyContent: 'center', padding: '28px 20px' }}>
                    <DonutChart value={assetVal} max={totalAbs || 1} color={netWorth >= 0 ? 'var(--color-income)' : 'var(--color-expense)'} />
                    <div className="card-title" style={{ marginTop: 12, fontSize: '0.75rem', color: 'var(--text-muted)', textTransform: 'uppercase', letterSpacing: '0.05em', fontWeight: 600 }}>
                        💰 {t('dashboard.netWorth')}
                    </div>
                    <div className={`card-value ${netWorth >= 0 ? 'positive' : 'negative'}`} style={{ fontSize: '1.8rem', marginTop: 4 }}>
                        {formatCurrency(netWorth)}
                    </div>
                    <p style={{ color: 'var(--text-muted)', fontSize: '0.75rem', marginTop: 6, textAlign: 'center' }}>
                        {t('dashboard.netWorthDesc')}
                    </p>
                </div>

                {/* Bar chart */}
                <div className="card" style={{ padding: '20px 24px', overflow: 'hidden' }}>
                    <div className="card-title" style={{ marginBottom: 16, fontSize: '0.85rem' }}>
                        📊 {t('dashboard.balanceOverview')}
                    </div>
                    <BarChart items={barItems} showReconciled={showReconciled} />
                </div>
            </div>
        </div>
    );
}
