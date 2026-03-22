import { useState } from 'react';
import { useNavigate } from 'react-router-dom';
import type { Account } from '../types';
import { ACCOUNT_TYPE_COLORS } from '../types';
import { t, formatCurrency } from '../i18n';

interface AccountTreeProps {
    accounts: Account[];
    onEdit?: (account: Account) => void;
    onDelete?: (account: Account) => void;
}

function buildTree(accounts: Account[]): Account[] {
    const map = new Map<string, Account>();
    const roots: Account[] = [];

    accounts.forEach(a => {
        map.set(a.guid, { ...a, children: [] });
    });

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

function TreeNode({ account, depth, onEdit, onDelete }: {
    account: Account;
    depth: number;
    onEdit?: (account: Account) => void;
    onDelete?: (account: Account) => void;
}) {
    const [expanded, setExpanded] = useState(depth < 2);
    const [showActions, setShowActions] = useState(false);
    const navigate = useNavigate();
    const hasChildren = account.children && account.children.length > 0;
    const isRoot = account.account_type === 'ROOT';

    if (isRoot) {
        return (
            <>
                {account.children?.map(child => (
                    <TreeNode key={child.guid} account={child} depth={depth} onEdit={onEdit} onDelete={onDelete} />
                ))}
            </>
        );
    }

    const color = ACCOUNT_TYPE_COLORS[account.account_type] || '#64748b';
    const typeLabel = t(`type.${account.account_type}` as any);

    return (
        <li className="account-tree-item">
            <div
                className="account-tree-row"
                style={{ paddingLeft: `${12 + depth * 20}px` }}
                onMouseEnter={() => setShowActions(true)}
                onMouseLeave={() => setShowActions(false)}
            >
                <span
                    className={`account-tree-toggle ${expanded ? 'expanded' : ''}`}
                    onClick={(e) => { e.stopPropagation(); setExpanded(!expanded); }}
                    style={{ visibility: hasChildren ? 'visible' : 'hidden' }}
                >
                    ▶
                </span>
                <span
                    className="account-type-badge"
                    style={{ background: `${color}22`, color }}
                >
                    {typeLabel}
                </span>
                <span
                    className="account-name"
                    onClick={() => !account.placeholder && navigate(`/accounts/${account.guid}`)}
                    style={{ cursor: account.placeholder ? 'default' : 'pointer' }}
                >
                    {account.name}
                    {account.placeholder && <span style={{ color: 'var(--text-muted)', fontSize: '0.75rem', marginLeft: 6 }}>📁</span>}
                </span>

                {showActions && (
                    <span className="account-actions" style={{ display: 'inline-flex', gap: 4, marginLeft: 8 }}>
                        {onEdit && (
                            <button
                                className="btn-icon"
                                title={t('accounts.editAccount')}
                                onClick={(e) => { e.stopPropagation(); onEdit(account); }}
                                style={{
                                    background: 'none', border: 'none', cursor: 'pointer',
                                    fontSize: '0.75rem', color: 'var(--text-muted)', padding: '2px 4px',
                                    borderRadius: 4, transition: 'color 0.15s',
                                }}
                                onMouseEnter={e => { (e.target as HTMLElement).style.color = 'var(--color-primary)'; }}
                                onMouseLeave={e => { (e.target as HTMLElement).style.color = 'var(--text-muted)'; }}
                            >
                                ✎
                            </button>
                        )}
                        {onDelete && (
                            <button
                                className="btn-icon"
                                title={t('accounts.deleteAccount')}
                                onClick={(e) => { e.stopPropagation(); onDelete(account); }}
                                style={{
                                    background: 'none', border: 'none', cursor: 'pointer',
                                    fontSize: '0.75rem', color: 'var(--text-muted)', padding: '2px 4px',
                                    borderRadius: 4, transition: 'color 0.15s',
                                }}
                                onMouseEnter={e => { (e.target as HTMLElement).style.color = 'var(--color-expense)'; }}
                                onMouseLeave={e => { (e.target as HTMLElement).style.color = 'var(--text-muted)'; }}
                            >
                                🗑
                            </button>
                        )}
                    </span>
                )}

                <span className="account-balance" style={{ color: (account.balance || 0) >= 0 ? 'var(--color-income)' : 'var(--color-expense)' }}>
                    {formatCurrency(account.balance || 0)}
                </span>
            </div>
            {expanded && hasChildren && (
                <ul className="account-children">
                    {account.children!.map(child => (
                        <TreeNode key={child.guid} account={child} depth={depth + 1} onEdit={onEdit} onDelete={onDelete} />
                    ))}
                </ul>
            )}
        </li>
    );
}

export default function AccountTree({ accounts, onEdit, onDelete }: AccountTreeProps) {
    const tree = buildTree(accounts);

    if (tree.length === 0) {
        return (
            <div className="empty-state">
                <div className="empty-state-icon">📂</div>
                <p>{t('accounts.noAccounts')}</p>
            </div>
        );
    }

    return (
        <ul className="account-tree">
            {tree.map(account => (
                <TreeNode key={account.guid} account={account} depth={0} onEdit={onEdit} onDelete={onDelete} />
            ))}
        </ul>
    );
}
