import { useState, useEffect, useMemo } from 'react';
import { Link } from 'react-router-dom';
import { getAccounts } from '../api/client';
import type { Account, AccountType } from '../types';
import { ACCOUNT_TYPE_COLORS } from '../types';
import { t, formatCurrency } from '../i18n';

/* ──────────────────────────────────────────────── Data layer ── */

interface DashboardData {
    assets: number;
    liabilities: number;
    income: number;
    expenses: number;
    rAssets: number;
    rLiabilities: number;
    rIncome: number;
    rExpenses: number;
    topAccounts: { guid: string; name: string; value: number; rValue: number; color: string; type: AccountType }[];
    expenseAccounts: { name: string; value: number; rValue: number; color: string }[];
    incomeAccounts: { name: string; value: number; rValue: number; color: string }[];
}

const EXPENSE_PALETTE = [
    '#ec4899', '#f43f5e', '#f97316', '#eab308', '#a855f7',
    '#6366f1', '#14b8a6', '#06b6d4', '#84cc16', '#64748b',
];

function computeDashboard(accounts: Account[]): DashboardData {
    const sums: Record<string, number> = { ASSET: 0, BANK: 0, CASH: 0, LIABILITY: 0, CREDIT: 0, INCOME: 0, EXPENSE: 0 };
    const rSums: Record<string, number> = { ...sums };
    Object.keys(rSums).forEach(k => rSums[k] = 0);

    const topAccounts: DashboardData['topAccounts'] = [];
    const expenseAccounts: DashboardData['expenseAccounts'] = [];
    const incomeAccounts: DashboardData['incomeAccounts'] = [];

    accounts.forEach(a => {
        if (a.account_type in sums) {
            sums[a.account_type] += (a.balance || 0);
            rSums[a.account_type] += (a.reconciled_balance || 0);
        }
        if (a.placeholder || a.account_type === 'ROOT' || a.account_type === 'EQUITY') return;

        const val = Math.abs(a.balance || 0);
        const rVal = Math.abs(a.reconciled_balance || 0);
        if (val > 0.005 || rVal > 0.005) {
            topAccounts.push({
                guid: a.guid, name: a.name, value: val, rValue: rVal,
                color: ACCOUNT_TYPE_COLORS[a.account_type] || '#64748b',
                type: a.account_type,
            });
        }
        if (a.account_type === 'EXPENSE' && val > 0.005) {
            expenseAccounts.push({ name: a.name, value: val, rValue: rVal, color: '' });
        }
        if (a.account_type === 'INCOME' && val > 0.005) {
            incomeAccounts.push({ name: a.name, value: val, rValue: rVal, color: '' });
        }
    });

    // Assign palette colors to expense accounts
    expenseAccounts.sort((a, b) => b.value - a.value);
    expenseAccounts.forEach((e, i) => e.color = EXPENSE_PALETTE[i % EXPENSE_PALETTE.length]);

    incomeAccounts.sort((a, b) => b.value - a.value);
    incomeAccounts.forEach((e, i) => e.color = EXPENSE_PALETTE[i % EXPENSE_PALETTE.length]);

    topAccounts.sort((a, b) => b.value - a.value);

    return {
        assets: sums.ASSET + sums.BANK + sums.CASH,
        liabilities: Math.abs(sums.LIABILITY + sums.CREDIT),
        income: Math.abs(sums.INCOME),
        expenses: sums.EXPENSE,
        rAssets: rSums.ASSET + rSums.BANK + rSums.CASH,
        rLiabilities: Math.abs(rSums.LIABILITY + rSums.CREDIT),
        rIncome: Math.abs(rSums.INCOME),
        rExpenses: rSums.EXPENSE,
        topAccounts: topAccounts.slice(0, 10),
        expenseAccounts,
        incomeAccounts,
    };
}

/* ──────────────────────────────────────────────── SVG Charts ── */

/** Multi-segment donut chart */
function SegmentDonut({ segments, size = 120, thickness = 14 }: {
    segments: { value: number; color: string; label: string }[];
    size?: number;
    thickness?: number;
}) {
    const r = (size - thickness) / 2;
    const cx = size / 2, cy = size / 2;
    const circumference = 2 * Math.PI * r;
    const total = segments.reduce((s, seg) => s + seg.value, 0);
    let offset = 0;

    if (total === 0) {
        return (
            <svg viewBox={`0 0 ${size} ${size}`} style={{ width: size, height: size }}>
                <circle cx={cx} cy={cy} r={r} fill="none" stroke="var(--bg-tertiary)" strokeWidth={thickness} />
            </svg>
        );
    }

    return (
        <svg viewBox={`0 0 ${size} ${size}`} style={{ width: size, height: size }}>
            <circle cx={cx} cy={cy} r={r} fill="none" stroke="var(--bg-tertiary)" strokeWidth={thickness} />
            {segments.map((seg, i) => {
                const pct = seg.value / total;
                const dashLen = pct * circumference;
                const dashOff = -offset * circumference;
                offset += pct;
                return (
                    <circle key={i} cx={cx} cy={cy} r={r} fill="none"
                        stroke={seg.color} strokeWidth={thickness}
                        strokeDasharray={`${dashLen} ${circumference - dashLen}`}
                        strokeDashoffset={dashOff}
                        strokeLinecap="butt"
                        transform={`rotate(-90 ${cx} ${cy})`}
                        style={{ transition: 'stroke-dasharray 0.6s ease' }}
                    />
                );
            })}
        </svg>
    );
}

/** Horizontal bar chart */
function HorizBars({ items }: { items: { label: string; value: number; color: string; guid?: string }[] }) {
    if (items.length === 0) {
        return <div style={{ color: 'var(--text-muted)', fontSize: '0.8rem', textAlign: 'center', padding: 20 }}>{t('dashboard.noData')}</div>;
    }
    const maxVal = Math.max(...items.map(i => i.value), 1);

    return (
        <div style={{ display: 'flex', flexDirection: 'column', gap: 6 }}>
            {items.map((item, i) => (
                <div key={i} style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
                    <span style={{ flex: '0 0 130px', maxWidth: 130, fontSize: '0.72rem', color: 'var(--text-secondary)', textAlign: 'right', whiteSpace: 'nowrap', overflow: 'hidden', textOverflow: 'ellipsis' }}>
                        {item.guid ? (
                            <Link to={`/accounts/${item.guid}`} style={{ color: 'inherit', textDecoration: 'none' }} title={item.label}>
                                {item.label}
                            </Link>
                        ) : (
                            item.label
                        )}
                    </span>
                    <div style={{ flex: 1, height: 18, background: 'var(--bg-tertiary)', borderRadius: 4, overflow: 'hidden', position: 'relative' }}>
                        <div style={{
                            height: '100%', borderRadius: 4,
                            background: item.color, opacity: 0.85,
                            width: `${(item.value / maxVal) * 100}%`,
                            transition: 'width 0.5s ease',
                        }} />
                    </div>
                    <span style={{ minWidth: 80, fontSize: '0.72rem', fontWeight: 600, color: Math.abs(item.value) < 0.005 ? 'var(--text-muted)' : 'var(--text-primary)', textAlign: 'right' }}>
                        {formatCurrency(item.value)}
                    </span>
                </div>
            ))}
        </div>
    );
}

/** Cash flow comparison bars (income vs expenses) */
function CashFlowBars({ income, expenses }: { income: number; expenses: number }) {
    const max = Math.max(income, expenses, 1);
    const net = income - expenses;

    return (
        <div style={{ display: 'flex', flexDirection: 'column', gap: 14 }}>
            {/* Income bar */}
            <div>
                <div style={{ display: 'flex', justifyContent: 'space-between', marginBottom: 4 }}>
                    <span style={{ fontSize: '0.75rem', color: 'var(--color-income)', fontWeight: 600 }}>
                        ↓ {t('dashboard.moneyIn')}
                    </span>
                    <span style={{ fontSize: '0.8rem', fontWeight: 700, color: Math.abs(income) < 0.005 ? 'var(--text-muted)' : 'var(--color-income)' }}>
                        {formatCurrency(income)}
                    </span>
                </div>
                <div style={{ height: 24, background: 'var(--bg-tertiary)', borderRadius: 6, overflow: 'hidden' }}>
                    <div style={{
                        height: '100%', borderRadius: 6,
                        background: 'linear-gradient(90deg, var(--color-income), #16a34a)',
                        width: `${(income / max) * 100}%`,
                        transition: 'width 0.5s ease',
                    }} />
                </div>
            </div>
            {/* Expenses bar */}
            <div>
                <div style={{ display: 'flex', justifyContent: 'space-between', marginBottom: 4 }}>
                    <span style={{ fontSize: '0.75rem', color: 'var(--color-expense)', fontWeight: 600 }}>
                        ↑ {t('dashboard.moneyOut')}
                    </span>
                    <span style={{ fontSize: '0.8rem', fontWeight: 700, color: Math.abs(expenses) < 0.005 ? 'var(--text-muted)' : 'var(--color-expense)' }}>
                        {formatCurrency(expenses)}
                    </span>
                </div>
                <div style={{ height: 24, background: 'var(--bg-tertiary)', borderRadius: 6, overflow: 'hidden' }}>
                    <div style={{
                        height: '100%', borderRadius: 6,
                        background: 'linear-gradient(90deg, var(--color-expense), #dc2626)',
                        width: `${(expenses / max) * 100}%`,
                        transition: 'width 0.5s ease',
                    }} />
                </div>
            </div>
            {/* Net line */}
            <div style={{
                display: 'flex', justifyContent: 'space-between', alignItems: 'center',
                padding: '8px 12px', borderRadius: 6,
                background: net >= 0 ? 'rgba(34,197,94,0.08)' : 'rgba(239,68,68,0.08)',
                border: `1px solid ${net >= 0 ? 'rgba(34,197,94,0.15)' : 'rgba(239,68,68,0.15)'}`,
            }}>
                <span style={{ fontSize: '0.72rem', fontWeight: 600, color: 'var(--text-secondary)', textTransform: 'uppercase', letterSpacing: '0.04em' }}>
                    {net >= 0 ? '📈' : '📉'} Net
                </span>
                <span style={{ fontSize: '0.9rem', fontWeight: 700, color: Math.abs(net) < 0.005 ? 'var(--text-muted)' : (net >= 0 ? 'var(--color-income)' : 'var(--color-expense)') }}>
                    {net >= 0 ? '+' : ''}{formatCurrency(net)}
                </span>
            </div>
        </div>
    );
}

/* ──────────────────────────────────────────────── Toggle ── */

function ToggleGroup({ value, onChange }: { value: boolean; onChange: (v: boolean) => void }) {
    const btnStyle = (active: boolean): React.CSSProperties => ({
        padding: '5px 12px', fontSize: '0.72rem', fontWeight: 500,
        border: 'none', cursor: 'pointer',
        background: active ? 'var(--color-primary)' : 'transparent',
        color: active ? '#fff' : 'var(--text-muted)',
        transition: 'all 0.15s',
    });
    return (
        <div style={{
            display: 'inline-flex', borderRadius: 'var(--radius-sm)',
            border: '1px solid var(--border-color)', overflow: 'hidden',
        }}>
            <button onClick={() => onChange(false)} style={btnStyle(!value)}>{t('dashboard.total')}</button>
            <button onClick={() => onChange(true)} style={btnStyle(value)}>{t('dashboard.reconciled')}</button>
        </div>
    );
}

/* ──────────────────────────────────────────────── Metric Card ── */

function MetricCard({ icon, label, value, colorVar, small }: {
    icon: string; label: string; value: number; colorVar: string; small?: boolean;
}) {
    const isZero = Math.abs(value) < 0.005;
    const color = isZero ? 'var(--text-muted)' : (value >= 0 ? 'var(--text-primary)' : 'var(--color-expense)');
    
    return (
        <div className="stat-card" style={{
            display: 'flex', flexDirection: 'column', gap: 2,
            padding: small ? '10px 14px' : '14px 18px',
            background: 'var(--bg-secondary)',
            borderRadius: 'var(--radius-md)',
            borderLeft: `3px solid ${isZero ? 'var(--border-color)' : `var(--color-${colorVar})`}`,
            minWidth: 0,
        }}>
            <span style={{ fontSize: '0.65rem', color: 'var(--text-muted)', textTransform: 'uppercase', letterSpacing: '0.04em', fontWeight: 600 }}>
                {icon} {label}
            </span>
            <span style={{
                fontSize: small ? '1rem' : '1.15rem', fontWeight: 700,
                color: color,
            }}>
                {formatCurrency(value)}
            </span>
        </div>
    );
}

/* ──────────────────────────────────────────────── Main ── */

function getDateRange(range: string): { start?: string, end?: string } {
    const now = new Date();
    if (range === 'this_month') {
        return {
            start: new Date(now.getFullYear(), now.getMonth(), 1).toISOString(),
            // End of month
            end: new Date(now.getFullYear(), now.getMonth() + 1, 0, 23, 59, 59, 999).toISOString()
        };
    }
    if (range === 'last_month') {
        return {
            start: new Date(now.getFullYear(), now.getMonth() - 1, 1).toISOString(),
            end: new Date(now.getFullYear(), now.getMonth(), 0, 23, 59, 59, 999).toISOString()
        };
    }
    if (range === 'this_year') {
        return {
            start: new Date(now.getFullYear(), 0, 1).toISOString(),
            end: new Date(now.getFullYear(), 11, 31, 23, 59, 59, 999).toISOString()
        };
    }
    if (range === 'last_year') {
        return {
            start: new Date(now.getFullYear() - 1, 0, 1).toISOString(),
            end: new Date(now.getFullYear() - 1, 11, 31, 23, 59, 59, 999).toISOString()
        };
    }
    return {};
}

export default function Dashboard() {
    const [accounts, setAccounts] = useState<Account[]>([]);
    const [loading, setLoading] = useState(true);
    const [showReconciled, setShowReconciled] = useState(false);
    const [timeRange, setTimeRange] = useState('all');

    useEffect(() => {
        setLoading(true);
        const { start, end } = getDateRange(timeRange);
        getAccounts(start, end)
            .then(setAccounts)
            .catch(console.error)
            .finally(() => setLoading(false));
    }, [timeRange]);

    const data = useMemo(() => computeDashboard(accounts), [accounts]);

    if (loading) {
        return <div className="loading"><div className="loading-spinner" />{t('common.loading')}</div>;
    }

    const assets = showReconciled ? data.rAssets : data.assets;
    const liab = showReconciled ? data.rLiabilities : data.liabilities;
    const income = showReconciled ? data.rIncome : data.income;
    const expenses = showReconciled ? data.rExpenses : data.expenses;
    const netWorth = assets - liab;

    // Donut segments for expense breakdown
    const expenseSegments = data.expenseAccounts.map(e => ({
        value: showReconciled ? e.rValue : e.value,
        color: e.color,
        label: e.name,
    }));

    // Top 10 accounts bar data
    const topItems = data.topAccounts.map(a => ({
        label: a.name,
        value: showReconciled ? a.rValue : a.value,
        color: a.color,
        guid: a.guid,
    }));

    return (
        <div>
            {/* Header */}
            <div className="page-header" style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'flex-start' }}>
                <div>
                    <h1 className="page-title">{t('dashboard.title')}</h1>
                    <p className="page-subtitle">{t('dashboard.subtitle')}</p>
                </div>
                <div style={{ display: 'flex', gap: 12, alignItems: 'center' }}>
                    <select
                        value={timeRange}
                        onChange={e => setTimeRange(e.target.value)}
                        style={{
                            padding: '6px 12px', borderRadius: 'var(--radius-sm)',
                            border: '1px solid var(--border-color)', background: 'var(--bg-glass)',
                            color: 'var(--text-primary)', fontSize: '0.8rem', cursor: 'pointer'
                        }}
                    >
                        <option value="all">{t('dashboard.allTime')}</option>
                        <option value="this_month">{t('dashboard.thisMonth')}</option>
                        <option value="last_month">{t('dashboard.lastMonth')}</option>
                        <option value="this_year">{t('dashboard.thisYear')}</option>
                        <option value="last_year">{t('dashboard.lastYear')}</option>
                    </select>
                    <ToggleGroup value={showReconciled} onChange={setShowReconciled} />
                </div>
            </div>

            {/* ── Row 1: Compact metric cards ── */}
            <div className="dashboard-stats-grid" style={{ gap: 12, marginBottom: 20 }}>
                <div className="stat-card" style={{
                    position: 'relative', overflow: 'hidden',
                    display: 'flex', flexDirection: 'column', alignItems: 'center',
                    justifyContent: 'center', padding: '14px 10px',
                    background: netWorth >= 0 ? 'rgba(34,197,94,0.06)' : 'rgba(239,68,68,0.06)',
                    border: `1px solid ${netWorth >= 0 ? 'rgba(34,197,94,0.12)' : 'rgba(239,68,68,0.12)'}`,
                    borderRadius: 'var(--radius-md)',
                }}>
                    <span className="card-title" style={{ fontSize: '0.65rem', color: 'var(--text-muted)', textTransform: 'uppercase', letterSpacing: '0.04em', fontWeight: 600 }}>
                        💰 {t('dashboard.netWorth')}
                    </span>
                    <span className="card-value" style={{
                        fontSize: '1.3rem', fontWeight: 700, marginTop: 2,
                        color: netWorth >= 0 ? 'var(--color-income)' : 'var(--color-expense)',
                    }}>
                        {formatCurrency(netWorth)}
                    </span>
                    <span style={{ fontSize: '0.6rem', color: 'var(--text-muted)', marginTop: 1 }}>
                        {t('dashboard.netWorthDesc')}
                    </span>
                </div>
                <MetricCard icon="🏦" label={t('dashboard.assets')} value={assets} colorVar="asset" small />
                <MetricCard icon="💳" label={t('dashboard.liabilities')} value={liab} colorVar="liability" small />
                <MetricCard icon="📈" label={t('dashboard.income')} value={income} colorVar="income" small />
                <MetricCard icon="📉" label={t('dashboard.expenses')} value={expenses} colorVar="expense" small />
            </div>

            {/* ── Row 2: Cash Flow + Expense Breakdown ── */}
            <div className="dashboard-two-col" style={{ gap: 16, marginBottom: 16 }}>
                {/* Cash Flow */}
                <div className="card" style={{ padding: '18px 20px' }}>
                    <div style={{ fontSize: '0.78rem', fontWeight: 600, color: 'var(--text-secondary)', marginBottom: 14, textTransform: 'uppercase', letterSpacing: '0.03em' }}>
                        💸 {t('dashboard.cashFlow')}
                    </div>
                    <CashFlowBars income={income} expenses={expenses} />
                </div>

                {/* Expense Breakdown Donut */}
                <div className="card" style={{ padding: '18px 20px' }}>
                    <div style={{ fontSize: '0.78rem', fontWeight: 600, color: 'var(--text-secondary)', marginBottom: 14, textTransform: 'uppercase', letterSpacing: '0.03em' }}>
                        🍩 {t('dashboard.expenseBreakdown')}
                    </div>
                    {expenseSegments.length === 0 ? (
                        <div style={{ color: 'var(--text-muted)', fontSize: '0.8rem', textAlign: 'center', padding: 30 }}>
                            {t('dashboard.noData')}
                        </div>
                    ) : (
                        <div style={{ display: 'flex', alignItems: 'center', gap: 20 }}>
                            <SegmentDonut segments={expenseSegments} size={130} thickness={16} />
                            <div style={{ display: 'flex', flexDirection: 'column', gap: 4, flex: 1, minWidth: 0 }}>
                                {data.expenseAccounts.slice(0, 6).map((e, i) => {
                                    const val = showReconciled ? e.rValue : e.value;
                                    return (
                                        <div key={i} style={{ display: 'flex', alignItems: 'center', gap: 6, fontSize: '0.72rem' }}>
                                            <span style={{ width: 8, height: 8, borderRadius: '50%', background: e.color, flexShrink: 0 }} />
                                            <span style={{ color: 'var(--text-secondary)', flex: 1, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
                                                {e.name}
                                            </span>
                                            <span style={{ fontWeight: 600, color: 'var(--text-primary)', whiteSpace: 'nowrap' }}>
                                                {formatCurrency(val)}
                                            </span>
                                        </div>
                                    );
                                })}
                            </div>
                        </div>
                    )}
                </div>
            </div>

            {/* ── Row 3: Top Accounts + Balance Overview ── */}
            <div className="dashboard-two-col" style={{ gap: 16, marginBottom: 0 }}>
                {/* Top 10 Accounts */}
                <div className="card" style={{ padding: '18px 20px' }}>
                    <div style={{ fontSize: '0.78rem', fontWeight: 600, color: 'var(--text-secondary)', marginBottom: 14, textTransform: 'uppercase', letterSpacing: '0.03em' }}>
                        🏆 {t('dashboard.topAccounts')}
                    </div>
                    <HorizBars items={topItems} />
                </div>

                {/* Balance Overview: assets vs liabilities visual */}
                <div className="card" style={{ padding: '18px 20px' }}>
                    <div style={{ fontSize: '0.78rem', fontWeight: 600, color: 'var(--text-secondary)', marginBottom: 14, textTransform: 'uppercase', letterSpacing: '0.03em' }}>
                        📊 {t('dashboard.balanceOverview')}
                    </div>
                    <div style={{ display: 'flex', flexDirection: 'column', gap: 16 }}>
                        {/* Net worth donut + breakdown */}
                        <div style={{ display: 'flex', alignItems: 'center', gap: 20 }}>
                            <SegmentDonut
                                segments={[
                                    { value: assets, color: 'var(--color-income)', label: t('dashboard.assets') },
                                    { value: liab, color: 'var(--color-expense)', label: t('dashboard.liabilities') },
                                ]}
                                size={100}
                                thickness={12}
                            />
                            <div style={{ flex: 1, display: 'flex', flexDirection: 'column', gap: 8 }}>
                                {/* Asset/Liability bars */}
                                {[
                                    { label: t('dashboard.assets'), val: assets, color: 'var(--color-income)' },
                                    { label: t('dashboard.liabilities'), val: liab, color: 'var(--color-expense)' },
                                ].map((item, i) => {
                                    const max = Math.max(assets, liab, 1);
                                    return (
                                        <div key={i}>
                                            <div style={{ display: 'flex', justifyContent: 'space-between', marginBottom: 3 }}>
                                                <span style={{ fontSize: '0.7rem', color: 'var(--text-muted)', fontWeight: 500 }}>{item.label}</span>
                                                <span style={{ fontSize: '0.75rem', fontWeight: 600, color: item.color }}>{formatCurrency(item.val)}</span>
                                            </div>
                                            <div style={{ height: 10, background: 'var(--bg-tertiary)', borderRadius: 5, overflow: 'hidden' }}>
                                                <div style={{
                                                    height: '100%', borderRadius: 5,
                                                    background: item.color, opacity: 0.8,
                                                    width: `${(item.val / max) * 100}%`,
                                                    transition: 'width 0.5s ease',
                                                }} />
                                            </div>
                                        </div>
                                    );
                                })}
                                {/* Income/Expense mini bars */}
                                {[
                                    { label: t('dashboard.income'), val: income, color: '#8b5cf6' },
                                    { label: t('dashboard.expenses'), val: expenses, color: '#ec4899' },
                                ].map((item, i) => {
                                    const max = Math.max(income, expenses, 1);
                                    return (
                                        <div key={`ie-${i}`}>
                                            <div style={{ display: 'flex', justifyContent: 'space-between', marginBottom: 3 }}>
                                                <span style={{ fontSize: '0.7rem', color: 'var(--text-muted)', fontWeight: 500 }}>{item.label}</span>
                                                <span style={{ fontSize: '0.75rem', fontWeight: 600, color: item.color }}>{formatCurrency(item.val)}</span>
                                            </div>
                                            <div style={{ height: 10, background: 'var(--bg-tertiary)', borderRadius: 5, overflow: 'hidden' }}>
                                                <div style={{
                                                    height: '100%', borderRadius: 5,
                                                    background: item.color, opacity: 0.8,
                                                    width: `${(item.val / max) * 100}%`,
                                                    transition: 'width 0.5s ease',
                                                }} />
                                            </div>
                                        </div>
                                    );
                                })}
                            </div>
                        </div>
                    </div>
                </div>
            </div>
        </div>
    );
}
