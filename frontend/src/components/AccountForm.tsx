import { useState, useEffect } from 'react';
import type { Account, AccountType } from '../types';
import { USER_ACCOUNT_TYPES, ACCOUNT_TYPE_COLORS } from '../types';
import { t } from '../i18n';
import { createAccount, updateAccount, getCommodities } from '../api/client';

interface AccountFormProps {
    accounts: Account[];
    editingAccount?: Account | null;
    onClose: () => void;
    onSaved: () => void;
}

function flattenAccounts(
    accounts: Account[],
    depth = 0,
    parentPrefix = '',
    _isLast = true,
): { account: Account; depth: number; prefix: string }[] {
    const result: { account: Account; depth: number; prefix: string }[] = [];
    // Filter out ROOT accounts at this level
    const visible = accounts.filter(a => a.account_type !== 'ROOT');
    // But if a ROOT account has children, flatten them at same depth
    for (const a of accounts) {
        if (a.account_type === 'ROOT') {
            if (a.children) {
                result.push(...flattenAccounts(a.children, depth, parentPrefix, true));
            }
            continue;
        }
    }
    for (let i = 0; i < visible.length; i++) {
        const a = visible[i];
        const last = i === visible.length - 1;
        const connector = depth === 0 ? '' : (last ? '└── ' : '├── ');
        const prefix = parentPrefix + connector;
        result.push({ account: a, depth, prefix });
        if (a.children && a.children.length > 0) {
            const childPrefix = depth === 0 ? '' : parentPrefix + (last ? '    ' : '│   ');
            result.push(...flattenAccounts(a.children, depth + 1, childPrefix, last));
        }
    }
    return result;
}

function buildTree(accounts: Account[]): Account[] {
    const map = new Map<string, Account>();
    const roots: Account[] = [];
    accounts.forEach(a => map.set(a.guid, { ...a, children: [] }));
    accounts.forEach(a => {
        const node = map.get(a.guid)!;
        if (a.parent_guid && map.has(a.parent_guid)) {
            map.get(a.parent_guid)!.children!.push(node);
        } else if (!a.parent_guid || a.account_type === 'ROOT') {
            roots.push(node);
        } else {
            roots.push(node);
        }
    });
    return roots;
}

export default function AccountForm({ accounts, editingAccount, onClose, onSaved }: AccountFormProps) {
    const isEdit = !!editingAccount;
    const [name, setName] = useState(editingAccount?.name || '');
    const [description, setDescription] = useState(editingAccount?.description || '');
    const [accountType, setAccountType] = useState<AccountType>(editingAccount?.account_type || 'BANK');
    const [parentGuid, setParentGuid] = useState<string>(editingAccount?.parent_guid || '');
    const [placeholder, setPlaceholder] = useState(editingAccount?.placeholder || false);
    const [error, setError] = useState<string | null>(null);
    const [loading, setLoading] = useState(false);
    const [commodityGuid, setCommodityGuid] = useState('');

    // ESC key to close
    useEffect(() => {
        const handleKey = (e: KeyboardEvent) => {
            if (e.key === 'Escape') onClose();
        };
        document.addEventListener('keydown', handleKey);
        return () => document.removeEventListener('keydown', handleKey);
    }, [onClose]);

    useEffect(() => {
        getCommodities().then(commodities => {
            const brl = commodities.find(c => c.mnemonic === 'BRL') || commodities[0];
            if (brl) setCommodityGuid(brl.guid);
        });
    }, []);

    // Find the root account guid for "top level" option
    const rootAccount = accounts.find(a => a.account_type === 'ROOT');
    const tree = buildTree(accounts);
    const flatList = flattenAccounts(tree);


    const handleSubmit = async (e: React.FormEvent) => {
        e.preventDefault();
        setError(null);

        if (!name.trim()) {
            setError(t('accounts.nameRequired'));
            return;
        }

        setLoading(true);
        try {
            if (isEdit) {
                const parent = parentGuid || rootAccount?.guid || null;
                await updateAccount(editingAccount!.guid, {
                    name: name.trim(),
                    description: description.trim(),
                    placeholder,
                    account_type: accountType,
                    parent_guid: parent,
                    version: editingAccount!.version,
                });
            } else {
                const parent = parentGuid || rootAccount?.guid || null;
                await createAccount({
                    name: name.trim(),
                    account_type: accountType,
                    commodity_guid: commodityGuid,
                    parent_guid: parent,
                    placeholder,
                    description: description.trim(),
                });
            }
            onSaved();
            onClose();
        } catch (err) {
            setError(err instanceof Error ? err.message : 'Error saving account');
        } finally {
            setLoading(false);
        }
    };


    return (
        <div className="modal-overlay" onClick={onClose}>
            <div className="modal" onClick={e => e.stopPropagation()} style={{ maxWidth: 520 }}>
                <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: 20 }}>
                    <h2 className="modal-title" style={{ margin: 0 }}>
                        {isEdit ? t('accounts.editAccount') : t('accounts.newAccount')}
                    </h2>
                    <kbd className="kbd-hint">Esc</kbd>
                </div>

                <form onSubmit={handleSubmit}>
                    <div className="form-group" style={{ marginBottom: 16 }}>
                        <label className="form-label">{t('accounts.name')}</label>
                        <input
                            type="text"
                            className="form-input"
                            value={name}
                            onChange={e => setName(e.target.value)}
                            autoFocus
                            required
                        />
                    </div>

                    <div className="form-group" style={{ marginBottom: 16 }}>
                        <label className="form-label">{t('accounts.description')}</label>
                        <input
                            type="text"
                            className="form-input"
                            value={description}
                            onChange={e => setDescription(e.target.value)}
                        />
                    </div>

                    {/* Parent Account */}
                    <div className="form-group" style={{ marginBottom: 16 }}>
                        <label className="form-label">{t('accounts.parentAccount')}</label>
                        <select
                            className="form-input"
                            value={parentGuid}
                            onChange={e => setParentGuid(e.target.value)}
                            style={{ padding: '8px 12px' }}
                        >
                            <option value="">{t('accounts.topLevel')}</option>
                            {flatList
                                .filter(f => editingAccount ? f.account.guid !== editingAccount.guid : true)
                                .map(({ account: a, prefix }) => (
                                    <option key={a.guid} value={a.guid}>
                                        {prefix}{a.name}
                                    </option>
                                ))}
                        </select>
                    </div>

                    {/* Account Type */}
                    <div className="form-group" style={{ marginBottom: 16 }}>
                        <label className="form-label">{t('accounts.accountType')}</label>
                        <div style={{ display: 'flex', flexWrap: 'wrap', gap: 6 }}>
                            {USER_ACCOUNT_TYPES.map(type => {
                                const color = ACCOUNT_TYPE_COLORS[type];
                                const isSelected = accountType === type;
                                return (
                                    <button
                                        key={type}
                                        type="button"
                                        onClick={() => setAccountType(type)}
                                        style={{
                                            padding: '6px 14px',
                                            borderRadius: 6,
                                            border: isSelected ? `2px solid ${color}` : '2px solid transparent',
                                            background: isSelected ? `${color}22` : 'var(--bg-tertiary)',
                                            color: isSelected ? color : 'var(--text-secondary)',
                                            fontWeight: isSelected ? 600 : 400,
                                            fontSize: '0.8rem',
                                            cursor: 'pointer',
                                            transition: 'all 0.15s',
                                        }}
                                    >
                                        {t(`type.${type}` as any)}
                                    </button>
                                );
                            })}
                        </div>
                    </div>

                    <div className="form-group" style={{ marginBottom: 16 }}>
                        <label style={{ display: 'flex', alignItems: 'center', gap: 8, cursor: 'pointer', fontSize: '0.85rem', color: 'var(--text-secondary)' }}>
                            <input
                                type="checkbox"
                                checked={placeholder}
                                onChange={e => setPlaceholder(e.target.checked)}
                                style={{ width: 16, height: 16 }}
                            />
                            {t('accounts.placeholder')}
                        </label>
                    </div>

                    {error && (
                        <div style={{ padding: '10px 14px', background: 'rgba(244,63,94,0.1)', border: '1px solid rgba(244,63,94,0.2)', borderRadius: 'var(--radius-sm)', color: 'var(--color-expense)', fontSize: '0.85rem', marginBottom: 12 }}>
                            {error}
                        </div>
                    )}

                    <div className="form-actions">
                        <button type="button" className="btn btn-secondary" onClick={onClose}>{t('accounts.cancel')}</button>
                        <button type="submit" className="btn btn-primary" disabled={loading}>
                            {loading ? t('common.loading') : (isEdit ? t('accounts.save') : t('accounts.create'))}
                        </button>
                    </div>
                </form>
            </div>
        </div>
    );
}
