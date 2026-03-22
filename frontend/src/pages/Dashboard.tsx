import { useState, useEffect } from 'react';
import { getAccounts } from '../api/client';
import type { Account } from '../types';
import { t, formatCurrency } from '../i18n';

interface CategorySummary {
    label: string;
    total: number;
    cssClass: string;
    icon: string;
}

function computeSummaries(accounts: Account[]): CategorySummary[] {
    const sums: Record<string, number> = {
        ASSET: 0, BANK: 0, CASH: 0,
        LIABILITY: 0, CREDIT: 0,
        INCOME: 0,
        EXPENSE: 0,
    };

    accounts.forEach(a => {
        if (a.account_type in sums) {
            sums[a.account_type] += (a.balance || 0);
        }
    });

    const assets = sums.ASSET + sums.BANK + sums.CASH;
    const liabilities = sums.LIABILITY + sums.CREDIT;
    const income = Math.abs(sums.INCOME);
    const expenses = sums.EXPENSE;

    return [
        { label: t('dashboard.assets'), total: assets, cssClass: 'asset', icon: '🏦' },
        { label: t('dashboard.liabilities'), total: Math.abs(liabilities), cssClass: 'liability', icon: '💳' },
        { label: t('dashboard.income'), total: income, cssClass: 'income', icon: '📈' },
        { label: t('dashboard.expenses'), total: expenses, cssClass: 'expense', icon: '📉' },
    ];
}

export default function Dashboard() {
    const [accounts, setAccounts] = useState<Account[]>([]);
    const [loading, setLoading] = useState(true);

    useEffect(() => {
        getAccounts()
            .then(setAccounts)
            .catch(console.error)
            .finally(() => setLoading(false));
    }, []);

    if (loading) {
        return <div className="loading"><div className="loading-spinner" />{t('common.loading')}</div>;
    }

    const summaries = computeSummaries(accounts);
    const netWorth = summaries[0].total - summaries[1].total;

    return (
        <div>
            <div className="page-header">
                <h1 className="page-title">{t('dashboard.title')}</h1>
                <p className="page-subtitle">{t('dashboard.subtitle')}</p>
            </div>

            <div className="stats-grid">
                {summaries.map(s => (
                    <div key={s.label} className={`card stat-card ${s.cssClass}`}>
                        <div className="card-title">{s.icon} {s.label}</div>
                        <div className={`card-value ${s.total >= 0 ? 'positive' : 'negative'}`}>
                            {formatCurrency(s.total)}
                        </div>
                    </div>
                ))}
            </div>

            <div className="card" style={{ maxWidth: 400 }}>
                <div className="card-title">💰 {t('dashboard.netWorth')}</div>
                <div className={`card-value ${netWorth >= 0 ? 'positive' : 'negative'}`} style={{ fontSize: '2rem' }}>
                    {formatCurrency(netWorth)}
                </div>
                <p style={{ color: 'var(--text-muted)', fontSize: '0.8rem', marginTop: 8 }}>
                    {t('dashboard.netWorthDesc')}
                </p>
            </div>
        </div>
    );
}
