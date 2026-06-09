import { useState, useEffect } from 'react';
import type { Account, SyncSuggestion } from '../types';
import { t } from '../i18n';
import { getAccounts, plaidImport } from '../api/client';

interface Props {
    institutionName: string;
    suggestions: SyncSuggestion[];
    onClose: () => void;
    onImported: (count: number) => void;
}

interface Row {
    suggestion: SyncSuggestion;
    categoryGUID: string;
    included: boolean;
}

export default function ImportMatcher({ institutionName, suggestions, onClose, onImported }: Props) {
    const [rows, setRows] = useState<Row[]>(
        suggestions.map(s => ({
            suggestion: s,
            categoryGUID: s.suggested_category_guid ?? '',
            included: true,
        })),
    );
    const [accounts, setAccounts] = useState<Account[]>([]);
    const [importing, setImporting] = useState(false);
    const [error, setError] = useState<string | null>(null);

    useEffect(() => {
        getAccounts().then(data => {
            const list: Account[] = [];
            const flatten = (accs: Account[]) => {
                accs.forEach(a => {
                    list.push(a);
                    if (a.children) flatten(a.children);
                });
            };
            flatten(data);
            setAccounts(list.filter(a => !a.placeholder));
        }).catch(() => {});
    }, []);

    const setCategory = (idx: number, guid: string) => {
        setRows(r => r.map((row, i) => i === idx ? { ...row, categoryGUID: guid } : row));
    };

    const toggleIncluded = (idx: number) => {
        setRows(r => r.map((row, i) => i === idx ? { ...row, included: !row.included } : row));
    };

    const includedRows = rows.filter(r => r.included);
    const allCategorized = includedRows.every(r => r.categoryGUID !== '');

    const handleConfirm = async () => {
        if (!allCategorized) return;
        setImporting(true);
        setError(null);
        try {
            const payload = includedRows.map(r => ({
                transaction_id: r.suggestion.transaction_id,
                bank_account_guid: r.suggestion.bank_account_guid,
                category_account_guid: r.categoryGUID,
                description: r.suggestion.description,
                date: r.suggestion.date,
                amount_num: r.suggestion.amount_num,
                amount_denom: r.suggestion.amount_denom,
            }));
            const result = await plaidImport(payload);
            onImported(result.imported);
        } catch (e: any) {
            setError(t('plaid.importError'));
        } finally {
            setImporting(false);
        }
    };

    const formatAmount = (num: number, denom: number) =>
        (Math.abs(num) / denom).toFixed(2);

    return (
        <div className="modal-overlay" onClick={e => e.target === e.currentTarget && onClose()}>
            <div className="modal">
                <div className="modal-header">
                    <h2>{institutionName} — {t('plaid.mapAccounts')}</h2>
                    <button className="modal-close" onClick={onClose}>✕</button>
                </div>
                <div className="modal-body" style={{ overflowY: 'auto', maxHeight: '60vh' }}>
                    {error && <div className="message error">{error}</div>}
                    <table style={{ width: '100%', borderCollapse: 'collapse' }}>
                        <thead>
                            <tr>
                                <th style={{ textAlign: 'left', padding: '4px 8px' }}>Date</th>
                                <th style={{ textAlign: 'left', padding: '4px 8px' }}>Description</th>
                                <th style={{ textAlign: 'right', padding: '4px 8px' }}>Amount</th>
                                <th style={{ textAlign: 'left', padding: '4px 8px' }}>Bank Account</th>
                                <th style={{ textAlign: 'left', padding: '4px 8px' }}>Category</th>
                                <th style={{ textAlign: 'center', padding: '4px 8px' }}>Include</th>
                            </tr>
                        </thead>
                        <tbody>
                            {rows.map((row, i) => (
                                <tr key={row.suggestion.transaction_id} style={{ opacity: row.included ? 1 : 0.4 }}>
                                    <td style={{ padding: '4px 8px', whiteSpace: 'nowrap' }}>{row.suggestion.date}</td>
                                    <td style={{ padding: '4px 8px' }}>{row.suggestion.description}</td>
                                    <td style={{ padding: '4px 8px', textAlign: 'right', fontVariantNumeric: 'tabular-nums' }}>
                                        {formatAmount(row.suggestion.amount_num, row.suggestion.amount_denom)}
                                    </td>
                                    <td style={{ padding: '4px 8px' }}>{row.suggestion.bank_account_name}</td>
                                    <td style={{ padding: '4px 8px' }}>
                                        <select
                                            value={row.categoryGUID}
                                            onChange={e => setCategory(i, e.target.value)}
                                            disabled={!row.included}
                                            style={{ minWidth: '160px' }}
                                        >
                                            <option value="">{t('plaid.categoryRequired')}</option>
                                            {accounts.map(a => (
                                                <option key={a.guid} value={a.guid}>{a.name}</option>
                                            ))}
                                        </select>
                                    </td>
                                    <td style={{ padding: '4px 8px', textAlign: 'center' }}>
                                        <input
                                            type="checkbox"
                                            checked={row.included}
                                            onChange={() => toggleIncluded(i)}
                                        />
                                    </td>
                                </tr>
                            ))}
                        </tbody>
                    </table>
                </div>
                <div className="modal-footer" style={{ display: 'flex', justifyContent: 'flex-end', gap: '0.5rem', padding: '1rem' }}>
                    <button className="btn btn-secondary" onClick={onClose} disabled={importing}>
                        Cancel
                    </button>
                    <button
                        className="btn btn-primary"
                        onClick={handleConfirm}
                        disabled={importing || !allCategorized || includedRows.length === 0}
                    >
                        {importing
                            ? t('plaid.connecting')
                            : t('plaid.confirmImport').replace('{{count}}', String(includedRows.length))}
                    </button>
                </div>
            </div>
        </div>
    );
}
