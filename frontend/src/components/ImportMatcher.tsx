import { useState, useEffect } from 'react';
import type { Account, SyncSuggestion } from '../types';
import { t } from '../i18n';
import { getAccounts, plaidImport, plaidDismiss } from '../api/client';

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
    const [dismissing, setDismissing] = useState<string | null>(null);
    const [error, setError] = useState<string | null>(null);

    // Permanently hide a suggestion the user never wants to import — otherwise
    // it would reappear on every future sync.
    const handleDismiss = async (transactionId: string) => {
        setDismissing(transactionId);
        setError(null);
        try {
            await plaidDismiss([transactionId]);
            setRows(r => r.filter(row => row.suggestion.transaction_id !== transactionId));
        } catch {
            setError(t('plaid.importError'));
        } finally {
            setDismissing(null);
        }
    };

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
                category_account_guid: r.categoryGUID,
            }));
            const result = await plaidImport(payload);
            if (result.error) {
                // Batch interrupted mid-way: report the partial progress and keep
                // the modal open — retry is safe (server-side dedupe).
                setError(t('plaid.importInterrupted').replace('{{imported}}', String(result.imported)));
            } else if (result.failed && result.failed.length > 0) {
                // Partial failure: keep the modal open so the user can retry —
                // already-imported rows are deduped server-side on retry.
                setError(
                    t('plaid.importPartial')
                        .replace('{{imported}}', String(result.imported))
                        .replace('{{failed}}', String(result.failed.length)),
                );
            } else {
                onImported(result.imported);
            }
        } catch {
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
                    <h2>{institutionName} — {t('plaid.importTitle')}</h2>
                    <button className="modal-close" onClick={onClose}>✕</button>
                </div>
                <div className="modal-body" style={{ overflowY: 'auto', maxHeight: '60vh' }}>
                    {error && <div className="message error">{error}</div>}
                    <div className="import-matcher-scroll">
                    <table>
                        <thead>
                            <tr>
                                <th style={{ textAlign: 'left', padding: '4px 8px' }}>{t('plaid.colDate')}</th>
                                <th style={{ textAlign: 'left', padding: '4px 8px' }}>{t('plaid.colDescription')}</th>
                                <th style={{ textAlign: 'right', padding: '4px 8px' }}>{t('plaid.colAmount')}</th>
                                <th style={{ textAlign: 'left', padding: '4px 8px' }}>{t('plaid.colBankAccount')}</th>
                                <th style={{ textAlign: 'left', padding: '4px 8px' }}>{t('plaid.colCategory')}</th>
                                <th style={{ textAlign: 'center', padding: '4px 8px' }}>{t('plaid.colInclude')}</th>
                                <th aria-label={t('plaid.dismiss')} />
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
                                            className="im-category-select"
                                            value={row.categoryGUID}
                                            onChange={e => setCategory(i, e.target.value)}
                                            disabled={!row.included}
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
                                    <td style={{ padding: '4px 8px', textAlign: 'center' }}>
                                        <button
                                            className="btn btn-secondary"
                                            onClick={() => handleDismiss(row.suggestion.transaction_id)}
                                            disabled={importing || dismissing === row.suggestion.transaction_id}
                                            title={t('plaid.dismissHint')}
                                        >
                                            {t('plaid.dismiss')}
                                        </button>
                                    </td>
                                </tr>
                            ))}
                        </tbody>
                    </table>
                    </div>
                </div>
                <div className="modal-footer" style={{ display: 'flex', justifyContent: 'flex-end', gap: '0.5rem', padding: '1rem' }}>
                    <button className="btn btn-secondary" onClick={onClose} disabled={importing}>
                        {t('plaid.cancel')}
                    </button>
                    <button
                        className="btn btn-primary"
                        onClick={handleConfirm}
                        disabled={importing || !allCategorized || includedRows.length === 0}
                    >
                        {importing
                            ? t('plaid.importing')
                            : t('plaid.confirmImport').replace('{{count}}', String(includedRows.length))}
                    </button>
                </div>
            </div>
        </div>
    );
}
