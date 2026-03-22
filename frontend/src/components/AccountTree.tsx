import { useState } from 'react';
import { useNavigate } from 'react-router-dom';
import type { Account } from '../types';
import { ACCOUNT_TYPE_COLORS } from '../types';
import { t, formatCurrency, formatDate } from '../i18n';

interface AccountTreeProps {
    accounts: Account[];
    onEdit?: (account: Account) => void;
    onDelete?: (account: Account) => void;
    onReconcile?: (account: Account) => void;
    showReconciled?: boolean;
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

/** Collect all GUIDs from a tree node and its descendants */
function collectGuids(account: Account): string[] {
    const guids = [account.guid];
    if (account.children) {
        for (const child of account.children) {
            guids.push(...collectGuids(child));
        }
    }
    return guids;
}

/** Action button with proper sizing and click handling */
function ActionBtn({ icon, title, onClick, hoverColor }: {
    icon: string;
    title: string;
    onClick: (e: React.MouseEvent) => void;
    hoverColor: string;
}) {
    return (
        <button
            className="tree-action-btn"
            title={title}
            onClick={onClick}
            style={{
                background: 'none',
                border: '1px solid transparent',
                cursor: 'pointer',
                fontSize: '0.8rem',
                color: 'var(--text-muted)',
                padding: '2px 6px',
                borderRadius: 4,
                lineHeight: 1,
                transition: 'all 0.15s',
                display: 'inline-flex',
                alignItems: 'center',
                justifyContent: 'center',
                minWidth: 24,
                minHeight: 24,
            }}
            onMouseEnter={e => {
                const el = e.currentTarget;
                el.style.color = hoverColor;
                el.style.borderColor = hoverColor + '44';
                el.style.background = hoverColor + '11';
            }}
            onMouseLeave={e => {
                const el = e.currentTarget;
                el.style.color = 'var(--text-muted)';
                el.style.borderColor = 'transparent';
                el.style.background = 'none';
            }}
        >
            {icon}
        </button>
    );
}

function TreeNode({ account, depth, onEdit, onDelete, onReconcile, showReconciled }: {
    account: Account;
    depth: number;
    onEdit?: (account: Account) => void;
    onDelete?: (account: Account) => void;
    onReconcile?: (account: Account) => void;
    showReconciled: boolean;
}) {
    const [expanded, setExpanded] = useState(depth < 2);
    const navigate = useNavigate();
    const hasChildren = account.children && account.children.length > 0;
    const isRoot = account.account_type === 'ROOT';

    if (isRoot) {
        return (
            <>
                {account.children?.map(child => (
                    <TreeNode key={child.guid} account={child} depth={depth} onEdit={onEdit} onDelete={onDelete} onReconcile={onReconcile} showReconciled={showReconciled} />
                ))}
            </>
        );
    }

    const color = ACCOUNT_TYPE_COLORS[account.account_type] || '#64748b';
    const typeLabel = t(`type.${account.account_type}` as any);
    const displayBalance = showReconciled ? account.reconciled_balance : (account.balance || 0);

    return (
        <li className="account-tree-item">
            <div
                className="account-tree-row"
                style={{ paddingLeft: `${12 + depth * 20}px` }}
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

                {/* Action buttons — always in DOM, shown on row hover via CSS */}
                <span className="account-actions" style={{ display: 'inline-flex', gap: 2, marginLeft: 8 }}>
                    {onReconcile && (
                        <ActionBtn
                            icon="✓"
                            title={t('accounts.reconcile')}
                            onClick={(e) => { e.stopPropagation(); onReconcile(account); }}
                            hoverColor="var(--color-income)"
                        />
                    )}
                    {onEdit && (
                        <ActionBtn
                            icon="✎"
                            title={t('accounts.editAccount')}
                            onClick={(e) => { e.stopPropagation(); onEdit(account); }}
                            hoverColor="var(--color-primary)"
                        />
                    )}
                    {onDelete && (
                        <ActionBtn
                            icon="🗑"
                            title={t('accounts.deleteAccount')}
                            onClick={(e) => { e.stopPropagation(); onDelete(account); }}
                            hoverColor="var(--color-expense)"
                        />
                    )}
                </span>

                {/* Last Reconciled */}
                <span style={{
                    fontSize: '0.72rem',
                    color: 'var(--text-secondary)',
                    width: 90,
                    textAlign: 'right',
                    marginLeft: 'auto',
                    flexShrink: 0,
                }}>
                    {account.last_reconciled ? formatDate(account.last_reconciled) : ''}
                </span>

                <span className="account-balance" style={{
                    color: displayBalance >= 0 ? 'var(--color-income)' : 'var(--color-expense)',
                    width: 110,
                    textAlign: 'right',
                    flexShrink: 0,
                }}>
                    {formatCurrency(displayBalance)}
                </span>
            </div>
            {expanded && hasChildren && (
                <ul className="account-children">
                    {account.children!.map(child => (
                        <TreeNode key={child.guid} account={child} depth={depth + 1} onEdit={onEdit} onDelete={onDelete} onReconcile={onReconcile} showReconciled={showReconciled} />
                    ))}
                </ul>
            )}
        </li>
    );
}

export { buildTree, collectGuids };

export default function AccountTree({ accounts, onEdit, onDelete, onReconcile, showReconciled = false }: AccountTreeProps) {
    const tree = buildTree(accounts);

    if (tree.length === 0) {
        return (
            <div className="empty-state">
                <div className="empty-state-icon">📂</div>
                <p>{t('accounts.noAccounts')}</p>
            </div>
        );
    }

    // Header row
    return (
        <div>
            <div style={{
                display: 'flex', justifyContent: 'flex-end', alignItems: 'center',
                padding: '8px 16px',
                borderBottom: '1px solid var(--border-color)',
                fontSize: '0.7rem', textTransform: 'uppercase', letterSpacing: '0.05em',
                color: 'var(--text-muted)', fontWeight: 600,
            }}>
                <span style={{ width: 90, textAlign: 'right', flexShrink: 0 }}>{t('accounts.lastReconciled')}</span>
                <span style={{ width: 110, textAlign: 'right', flexShrink: 0 }}>
                    {showReconciled ? t('accounts.reconciledBalance') : t('register.balance')}
                </span>
            </div>
            <ul className="account-tree">
                {tree.map(account => (
                    <TreeNode key={account.guid} account={account} depth={0} onEdit={onEdit} onDelete={onDelete} onReconcile={onReconcile} showReconciled={showReconciled} />
                ))}
            </ul>
        </div>
    );
}
