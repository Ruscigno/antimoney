import { Link } from 'react-router-dom';
import type { Account } from '../types';
import { t } from '../i18n';

interface BreadcrumbsProps {
    currentAccount: Account;
    allAccounts: Account[];
}

export default function AccountBreadcrumbs({ currentAccount, allAccounts }: BreadcrumbsProps) {
    const buildPath = (acc: Account): Account[] => {
        const path: Account[] = [acc];
        let curr = acc;
        while (curr.parent_guid) {
            const parent = allAccounts.find(a => a.guid === curr.parent_guid);
            if (parent && parent.account_type !== 'ROOT') {
                path.unshift(parent);
                curr = parent;
            } else {
                break;
            }
        }
        return path;
    };

    const path = buildPath(currentAccount);

    return (
        <nav className="breadcrumbs" style={{
            display: 'flex',
            alignItems: 'center',
            gap: 6,
            marginBottom: 8,
            fontSize: '0.78rem',
            color: 'var(--text-muted)'
        }}>
            <Link 
                to="/accounts" 
                style={{ color: 'var(--color-primary)', textDecoration: 'none', fontWeight: 500 }}
            >
                {t('accounts.title')}
            </Link>
            
            {path.map((acc) => (
                <span key={acc.guid} style={{ display: 'flex', alignItems: 'center', gap: 6 }}>
                    <span style={{ opacity: 0.5 }}>/</span>
                    {acc.guid === currentAccount.guid ? (
                        <span style={{ color: 'var(--text-secondary)', fontWeight: 600 }}>{acc.name}</span>
                    ) : (
                        <Link 
                            to={`/accounts/${acc.guid}`} 
                            style={{ color: 'inherit', textDecoration: 'none' }}
                            onMouseEnter={e => e.currentTarget.style.color = 'var(--text-primary)'}
                            onMouseLeave={e => e.currentTarget.style.color = 'inherit'}
                        >
                            {acc.name}
                        </Link>
                    )}
                </span>
            ))}
        </nav>
    );
}
