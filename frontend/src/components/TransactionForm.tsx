import { useState, useEffect, useRef } from 'react';
import type { Account, Commodity } from '../types';
import { handleDateShortcut } from '../utils/date';
import { createTransaction } from '../api/client';
import { getCommodities, getAccounts } from '../api/client';
import { t, formatCurrency } from '../i18n';
import { useShortcut } from '../hooks/useShortcuts';
import AccountPicker from './AccountPicker';

interface TransactionFormProps {
    onClose: () => void;
    onCreated: () => void;
    /** Pre-fill the first split with this account */
    defaultAccountGuid?: string;
}

interface SplitInput {
    account_guid: string;
    amount: string;
    memo: string;
}

export default function TransactionForm({ onClose, onCreated, defaultAccountGuid }: TransactionFormProps) {
    const [description, setDescription] = useState('');
    const [postDate, setPostDate] = useState(new Date().toISOString().split('T')[0]);
    const [splits, setSplits] = useState<SplitInput[]>([
        { account_guid: defaultAccountGuid || '', amount: '', memo: '' },
        { account_guid: '', amount: '', memo: '' },
    ]);
    const [accounts, setAccounts] = useState<Account[]>([]);
    const [commodities, setCommodities] = useState<Commodity[]>([]);
    const [error, setError] = useState<string | null>(null);
    const [loading, setLoading] = useState(false);
    const descriptionRef = useRef<HTMLInputElement>(null);

    // ESC closes the modal
    useShortcut('Escape', onClose, t('shortcuts.close'), undefined, [onClose]);

    // Ctrl+Enter submits the form
    useShortcut('Enter', () => {
        const form = document.querySelector('.modal form') as HTMLFormElement;
        if (form) form.requestSubmit();
    }, 'Submit form', { ctrl: true });

    useEffect(() => {
        Promise.all([getAccounts(), getCommodities()]).then(([accts, comms]) => {
            setAccounts(accts.filter(a => !a.placeholder && a.account_type !== 'ROOT'));
            setCommodities(comms);
        });
    }, []);

    // Auto-focus description field
    useEffect(() => {
        setTimeout(() => descriptionRef.current?.focus(), 100);
    }, []);

    const updateSplit = (index: number, field: keyof SplitInput, value: string) => {
        const updated = [...splits];
        updated[index] = { ...updated[index], [field]: value };

        // Auto-balance logic for simple 2-split transactions
        if (field === 'amount' && Object.keys(updated).length === 2 && splits.length === 2) {
            const otherIdx = index === 0 ? 1 : 0;
            const parsedOld = parseFloat(splits[index].amount.replace(',', '.'));
            const parsedOther = parseFloat(splits[otherIdx].amount.replace(',', '.'));

            const isOtherEmpty = splits[otherIdx].amount === '';
            const isOtherMirroring = !isNaN(parsedOld) && !isNaN(parsedOther) && (parsedOld + parsedOther === 0);

            if (isOtherEmpty || isOtherMirroring) {
                const parsedNew = parseFloat(value.replace(',', '.'));
                if (!isNaN(parsedNew)) {
                    updated[otherIdx] = { ...updated[otherIdx], amount: (-parsedNew).toString() };
                } else if (value === '' || value === '-') {
                    updated[otherIdx] = { ...updated[otherIdx], amount: '' };
                }
            }
        }

        setSplits(updated);
    };

    const addSplit = () => {
        setSplits([...splits, { account_guid: '', amount: '', memo: '' }]);
    };

    const removeSplit = (index: number) => {
        setSplits(splits.filter((_, i) => i !== index));
    };

    const handleSubmit = async (e: React.FormEvent) => {
        e.preventDefault();
        setError(null);

        const currency = commodities.find(c => c.mnemonic === 'BRL') || commodities[0];
        if (!currency) {
            setError(t('form.noCurrency'));
            return;
        }

        const validSplits = splits.filter(s => s.account_guid !== '' || s.amount.trim() !== '');

        if (validSplits.length < 1) {
            setError(t('form.invalidAmount'));
            return;
        }

        const parsedSplits = validSplits.map(s => {
            const amount = parseFloat(s.amount.replace(',', '.'));
            if (isNaN(amount)) return null;
            const valueNum = Math.round(amount * currency.fraction);
            return {
                account_guid: s.account_guid,
                memo: s.memo,
                value_num: valueNum,
                value_denom: currency.fraction,
                quantity_num: valueNum,
                quantity_denom: currency.fraction,
            };
        });

        if (parsedSplits.some(s => s === null)) {
            setError(t('form.invalidAmount'));
            return;
        }

        if (parsedSplits.some(s => !s!.account_guid)) {
            setError(t('form.selectAccountError'));
            return;
        }

        setLoading(true);
        try {
            await createTransaction({
                currency_guid: currency.guid,
                post_date: new Date(postDate + 'T11:00:00Z').toISOString(),
                description,
                splits: parsedSplits as NonNullable<(typeof parsedSplits)[0]>[],
            });
            onCreated();
            onClose();
        } catch (err) {
            setError(err instanceof Error ? err.message : t('form.createError'));
        } finally {
            setLoading(false);
        }
    };

    const total = splits.reduce((sum, s) => {
        const n = parseFloat(s.amount.replace(',', '.'));
        return sum + (isNaN(n) ? 0 : n);
    }, 0);
    const isBalanced = Math.abs(total) < 0.005;

    return (
        <div className="modal-overlay" onClick={onClose}>
            <div className="modal" onClick={e => e.stopPropagation()}>
                <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: 20 }}>
                    <h2 className="modal-title" style={{ margin: 0 }}>{t('form.newTransaction')}</h2>
                    <kbd className="kbd-hint">Esc</kbd>
                </div>

                <form onSubmit={handleSubmit}>
                    <div className="form-row" style={{ gridTemplateColumns: '140px 1fr' }}>
                        <div className="form-group">
                            <label className="form-label">{t('form.date')}</label>
                            <input
                                type="date"
                                className="form-input"
                                value={postDate}
                                onChange={e => setPostDate(e.target.value)}
                                onKeyDown={e => handleDateShortcut(e, postDate, setPostDate)}
                                required
                            />
                        </div>
                        <div className="form-group">
                            <label className="form-label">{t('form.description')}</label>
                            <input
                                ref={descriptionRef}
                                type="text"
                                className="form-input"
                                placeholder={t('form.descriptionPlaceholder')}
                                value={description}
                                onChange={e => setDescription(e.target.value)}
                                required
                                id="tx-description"
                            />
                        </div>
                    </div>

                    <div className="form-group">
                        <label className="form-label">{t('form.splits')}</label>
                        <p style={{ fontSize: '0.75rem', color: 'var(--text-muted)', marginBottom: 8 }}>
                            {t('form.splitsHelp')}
                        </p>

                        {splits.map((split, i) => (
                            <div className="split-row" key={i}>
                                <AccountPicker
                                    accounts={accounts}
                                    value={split.account_guid}
                                    onChange={(guid) => updateSplit(i, 'account_guid', guid)}
                                    id={`split-account-${i}`}
                                />
                                <input
                                    type="text"
                                    className="form-input"
                                    placeholder={t('form.value')}
                                    value={split.amount}
                                    onChange={e => updateSplit(i, 'amount', e.target.value)}
                                    id={`split-value-${i}`}
                                />
                                <input
                                    type="text"
                                    className="form-input"
                                    placeholder={t('form.memo')}
                                    value={split.memo}
                                    onChange={e => updateSplit(i, 'memo', e.target.value)}
                                />
                                <button
                                    type="button"
                                    className="split-remove-btn"
                                    onClick={() => removeSplit(i)}
                                    disabled={splits.length <= 1}
                                    title={t('form.removeSplit')}
                                    aria-label="Remove split"
                                >
                                    ✕
                                </button>
                            </div>
                        ))}

                        <button type="button" className="btn btn-secondary" onClick={addSplit} style={{ marginTop: 8 }}>
                            {t('form.addSplit')}
                        </button>
                    </div>

                    <div style={{ fontSize: '0.85rem', color: isBalanced ? 'var(--color-income)' : 'var(--text-muted)', fontWeight: 600, marginBottom: 8 }}>
                        {t('form.balance')}: {formatCurrency(total)} {isBalanced ? t('form.balanced') : t('form.unbalanced')}
                    </div>

                    {error && (
                        <div style={{ padding: '10px 14px', background: 'rgba(244,63,94,0.1)', border: '1px solid rgba(244,63,94,0.2)', borderRadius: 'var(--radius-sm)', color: 'var(--color-expense)', fontSize: '0.85rem', marginBottom: 12 }}>
                            {error}
                        </div>
                    )}

                    <div className="form-actions">
                        <span style={{ flex: 1, fontSize: '0.7rem', color: 'var(--text-muted)' }}>
                            <kbd className="kbd-hint">Ctrl+↵</kbd> submit
                        </span>
                        <button type="button" className="btn btn-secondary" onClick={onClose}>{t('form.cancel')}</button>
                        <button type="submit" className="btn btn-primary" disabled={loading} id="tx-submit">
                            {loading ? t('form.creating') : t('form.create')}
                        </button>
                    </div>
                </form>
            </div>
        </div>
    );
}
