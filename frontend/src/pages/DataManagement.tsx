import { useState, useRef, useEffect, useCallback } from 'react';
import { usePlaidLink } from 'react-plaid-link';
import { t } from '../i18n';
import { getAccounts, plaidGetLinkToken, plaidExchange, plaidLink, plaidListItems, plaidDisconnect, plaidSync } from '../api/client';
import type { Account, PlaidBankAccount, PlaidItem, SyncSuggestion } from '../types';
import ImportMatcher from '../components/ImportMatcher';

export default function DataManagement() {
    const [accounts, setAccounts] = useState<Account[]>([]);
    const [selectedAccount, setSelectedAccount] = useState<string>('');
    const [importing, setImporting] = useState(false);
    const [importingCSV, setImportingCSV] = useState(false);
    const [importingGnucash, setImportingGnucash] = useState(false);
    const [exporting, setExporting] = useState(false);
    const [resetting, setResetting] = useState(false);
    const [message, setMessage] = useState<{ type: 'success' | 'error', text: string } | null>(null);
    const fileInputRef = useRef<HTMLInputElement>(null);
    const csvInputRef = useRef<HTMLInputElement>(null);
    const gnucashInputRef = useRef<HTMLInputElement>(null);

    const [linkToken, setLinkToken] = useState<string | null>(null);
    const [plaidConnecting, setPlaidConnecting] = useState(false);
    const [plaidStep, setPlaidStep] = useState<'idle' | 'linking' | 'mapping' | 'done'>('idle');
    const [plaidItem, setPlaidItem] = useState<{ guid: string; institution: string } | null>(null);
    const [plaidBankAccounts, setPlaidBankAccounts] = useState<PlaidBankAccount[]>([]);
    const [plaidMappings, setPlaidMappings] = useState<Record<string, string>>({});
    const [plaidImportPending, setPlaidImportPending] = useState(false);
    const [plaidMessage, setPlaidMessage] = useState<{ type: 'success' | 'error'; text: string } | null>(null);
    const [plaidItems, setPlaidItems] = useState<PlaidItem[]>([]);
    const [syncingItemGuid, setSyncingItemGuid] = useState<string | null>(null);
    const [dmSuggestions, setDmSuggestions] = useState<SyncSuggestion[] | null>(null);
    const [dmInstitution, setDmInstitution] = useState('');

    const loadPlaidItems = useCallback(() => {
        plaidListItems().then(r => setPlaidItems(r.items)).catch(() => {});
    }, []);

    useEffect(() => { loadPlaidItems(); }, [loadPlaidItems]);

    const handlePlaidSyncNow = async (item: PlaidItem) => {
        setSyncingItemGuid(item.guid);
        setPlaidMessage(null);
        try {
            const result = await plaidSync(item.guid);
            const moreSuffix = (result.has_more ? ` ${t('plaid.syncMore')}` : '')
                + (result.in_progress ? ` ${t('plaid.syncInProgress')}` : '');
            if (result.count > 0) {
                // Spec §6.2: report "<N> new transactions" AND open the matcher.
                setPlaidMessage({
                    type: 'success',
                    text: t('plaid.syncSuccess').replace('{{count}}', String(result.count)) + moreSuffix,
                });
                setDmInstitution(item.institution_name);
                setDmSuggestions(result.suggestions);
            } else {
                setPlaidMessage({ type: 'success', text: t('plaid.syncNone') + moreSuffix });
            }
            loadPlaidItems();
        } catch (e) {
            // The backend returns the literal "reconnect_required" for Plaid's
            // ITEM_LOGIN_REQUIRED — map it to the re-auth guidance message.
            const text = e instanceof Error && e.message === 'reconnect_required'
                ? t('plaid.reconnectNeeded')
                : t('plaid.syncError').replace('{{institution}}', item.institution_name);
            setPlaidMessage({ type: 'error', text });
        } finally {
            setSyncingItemGuid(null);
        }
    };

    const handlePlaidDisconnect = async (item: PlaidItem) => {
        if (!window.confirm(t('plaid.disconnectConfirm'))) return;
        try {
            await plaidDisconnect(item.guid);
            setPlaidMessage({ type: 'success', text: t('plaid.disconnected') });
            loadPlaidItems();
        } catch (e) {
            setPlaidMessage({
                type: 'error',
                text: e instanceof Error ? e.message : t('plaid.syncError').replace('{{institution}}', item.institution_name),
            });
        }
    };

    useEffect(() => {
        getAccounts().then(data => {
            // Flatten the tree for the select dropdown
            const list: Account[] = [];
            const flatten = (accs: Account[]) => {
                accs.forEach(a => {
                    list.push(a);
                    if (a.children) flatten(a.children);
                });
            };
            flatten(data);
            const nonPlaceholder = list.filter(a => !a.placeholder);
            setAccounts(nonPlaceholder);
            if (nonPlaceholder.length > 0) setSelectedAccount(nonPlaceholder[0].guid);
        }).catch(console.error);
    }, []);

    const handleExport = async () => {
        setExporting(true);
        setMessage(null);
        try {
                const res = await fetch('/api/data/export', {
                credentials: 'include',
            });
            if (!res.ok) throw new Error(`Export failed: ${res.statusText}`);
            const data = await res.json();
            const blob = new Blob([JSON.stringify(data, null, 2)], { type: 'application/json' });
            const url = URL.createObjectURL(blob);
            const a = document.createElement('a');
            a.href = url;
            a.download = 'export.json';
            document.body.appendChild(a);
            a.click();
            document.body.removeChild(a);
            URL.revokeObjectURL(url);
            setMessage({ type: 'success', text: t('data.exportSuccess') || 'Export successful!' });
        } catch (err: any) {
            setMessage({ type: 'error', text: err.message || t('data.exportError') || 'Export failed' });
        } finally {
            setExporting(false);
        }
    };

    const handleImportClick = () => {
        fileInputRef.current?.click();
    };

    const handleFileChange = async (e: React.ChangeEvent<HTMLInputElement>) => {
        const file = e.target.files?.[0];
        if (!file) return;

        setImporting(true);
        setMessage(null);

        try {
            const formData = new FormData();
            formData.append('file', file);

            const res = await fetch('/api/data/import', {
                method: 'POST',
                credentials: 'include',
                body: formData,
            });

            if (!res.ok) {
                const errBody = await res.json().catch(() => ({}));
                throw new Error(errBody.error || `Import failed: ${res.statusText}`);
            }

            setMessage({ type: 'success', text: t('data.importSuccess') || 'Import successful!' });
            if (fileInputRef.current) fileInputRef.current.value = '';
        } catch (err: any) {
            setMessage({ type: 'error', text: err.message || t('data.importError') || 'Import failed' });
        } finally {
            setImporting(false);
        }
    };

    const handleCSVImportClick = () => {
        if (!selectedAccount) {
            alert("Please select an account first");
            return;
        }
        csvInputRef.current?.click();
    };

    const handleCSVFileChange = async (e: React.ChangeEvent<HTMLInputElement>) => {
        const file = e.target.files?.[0];
        if (!file) return;

        setImportingCSV(true);
        setMessage(null);

        try {
            const formData = new FormData();
            formData.append('file', file);
            formData.append('account_guid', selectedAccount);

            const res = await fetch('/api/data/import/csv', {
                method: 'POST',
                credentials: 'include',
                body: formData,
            });

            if (!res.ok) {
                const errBody = await res.json().catch(() => ({}));
                throw new Error(errBody.error || `Import failed: ${res.statusText}`);
            }

            const result = await res.json();
            setMessage({ type: 'success', text: t('data.importCsvSuccess').replace('{{count}}', result.count.toString()) });
            if (csvInputRef.current) csvInputRef.current.value = '';
        } catch (err: any) {
            setMessage({ type: 'error', text: err.message || 'CSV Import failed' });
        } finally {
            setImportingCSV(false);
        }
    };

    const handleGnucashImportClick = () => {
        gnucashInputRef.current?.click();
    };

    const handleGnucashFileChange = async (e: React.ChangeEvent<HTMLInputElement>) => {
        const file = e.target.files?.[0];
        if (!file) return;

        setImportingGnucash(true);
        setMessage(null);

        try {
            const formData = new FormData();
            formData.append('file', file);

            const res = await fetch('/api/data/import/gnucash', {
                method: 'POST',
                credentials: 'include',
                body: formData,
            });

            if (!res.ok) {
                const errBody = await res.json().catch(() => ({}));
                throw new Error(errBody.error || `Import failed: ${res.statusText}`);
            }

            setMessage({ type: 'success', text: t('data.importGnucashSuccess') || 'GnuCash import successful!' });
            if (gnucashInputRef.current) gnucashInputRef.current.value = '';
        } catch (err: any) {
            setMessage({ type: 'error', text: err.message || 'GnuCash import failed' });
        } finally {
            setImportingGnucash(false);
        }
    };

    const handleFactoryReset = async () => {
        if (!window.confirm("Are you sure you want to factory reset your account?\n\nThis will PERMANENTLY delete all your transactions and custom accounts. This action cannot be undone.")) {
            return;
        }

        setResetting(true);
        setMessage(null);

        try {
            const res = await fetch('/api/data/reset', {
                method: 'POST',
                credentials: 'include',
            });

            if (!res.ok) {
                const errBody = await res.json().catch(() => ({}));
                throw new Error(errBody.error || `Reset failed: ${res.statusText}`);
            }

            setMessage({ type: 'success', text: 'Account factory reset successful! Reloading...' });
            setTimeout(() => {
                window.location.reload();
            }, 1000);
        } catch (err: any) {
            setMessage({ type: 'error', text: err.message || 'Reset failed' });
        } finally {
            setResetting(false);
        }
    };

    const handleConnectBank = async () => {
        setPlaidConnecting(true);
        setPlaidMessage(null);
        try {
            const { link_token } = await plaidGetLinkToken();
            setLinkToken(link_token);
            setPlaidStep('linking');
        } catch (e: any) {
            setPlaidMessage({ type: 'error', text: e.message });
        } finally {
            setPlaidConnecting(false);
        }
    };

    const { open: openPlaidLink, ready: plaidLinkReady } = usePlaidLink({
        token: linkToken ?? '',
        onSuccess: async (publicToken) => {
            setPlaidConnecting(true);
            setPlaidMessage(null);
            try {
                const result = await plaidExchange(publicToken);
                setPlaidItem({ guid: result.item_guid, institution: result.institution_name });
                setPlaidBankAccounts(result.accounts);
                setPlaidMappings({});
                setPlaidStep('mapping');
            } catch (e: any) {
                setPlaidMessage({ type: 'error', text: e.message });
                setPlaidStep('idle');
            } finally {
                setPlaidConnecting(false);
            }
        },
        onExit: () => {
            if (plaidStep === 'linking') setPlaidStep('idle');
        },
    });

    useEffect(() => {
        if (plaidStep === 'linking' && plaidLinkReady && linkToken) {
            openPlaidLink();
        }
    }, [plaidStep, plaidLinkReady, linkToken, openPlaidLink]);

    const handleSubmitMappings = async () => {
        if (!plaidItem) return;
        const mappings = Object.entries(plaidMappings)
            .filter(([, v]) => v !== '')
            .map(([account_id, account_guid]) => ({ account_id, account_guid }));
        setPlaidConnecting(true);
        setPlaidMessage(null);
        try {
            await plaidLink(plaidItem.guid, mappings, plaidImportPending);
            setPlaidStep('done');
            setPlaidMessage({ type: 'success', text: `${t('plaid.connected')}: ${plaidItem.institution}` });
            loadPlaidItems();
        } catch (e: any) {
            setPlaidMessage({ type: 'error', text: e.message });
        } finally {
            setPlaidConnecting(false);
        }
    };

    return (
        <div className="data-management">
            <div className="page-header">
                <h1 className="page-title">{t('nav.data') || 'Data Management'}</h1>
                <p className="page-subtitle">Import and export your financial data.</p>
            </div>

            {message && (
                <div className={`alert alert-${message.type}`} style={{ padding: 12, marginBottom: 16, borderRadius: 4, background: message.type === 'error' ? '#fecdd3' : '#dcfce3', color: message.type === 'error' ? '#9f1239' : '#166534' }}>
                    {message.text}
                </div>
            )}

            <div className="card" style={{ padding: 24, display: 'flex', flexDirection: 'column', gap: 16 }}>
                <div>
                    <h3>{t('data.exportTitle') || 'Export Data'}</h3>
                    <p style={{ color: 'var(--text-muted)' }}>{t('data.exportDesc') || 'Download your accounts and transactions as a JSON file.'}</p>
                    <button className="btn btn-secondary" onClick={handleExport} disabled={exporting} style={{ marginTop: 8 }}>
                        {exporting ? t('common.loading') || 'Loading...' : t('data.exportBtn') || 'Export to JSON'}
                    </button>
                </div>

                <hr style={{ border: 'none', borderTop: '1px solid var(--border-color)', margin: '16px 0' }} />

                <div>
                    <h3>{t('data.importTitle') || 'Import Data'}</h3>
                    <p style={{ color: 'var(--text-muted)' }}>{t('data.importDesc') || 'Import accounts and transactions from a JSON file. This will REPLACE your current book data.'}</p>
                    <input
                        type="file"
                        accept=".json"
                        ref={fileInputRef}
                        onChange={handleFileChange}
                        style={{ display: 'none' }}
                    />
                    <button className="btn btn-primary" onClick={handleImportClick} disabled={importing || resetting} style={{ marginTop: 8 }}>
                        {importing ? t('common.loading') || 'Loading...' : t('data.importBtn') || 'Import from JSON'}
                    </button>
                </div>

                <hr style={{ border: 'none', borderTop: '1px solid var(--border-color)', margin: '16px 0' }} />

                <div>
                    <h3>{t('data.importGnucashTitle') || 'Import Data (GnuCash)'}</h3>
                    <p style={{ color: 'var(--text-muted)' }}>{t('data.importGnucashDesc') || 'Import accounts and transactions from a .gnucash file. This will REPLACE your current book data.'}</p>
                    <input
                        type="file"
                        accept=".gnucash"
                        ref={gnucashInputRef}
                        onChange={handleGnucashFileChange}
                        style={{ display: 'none' }}
                    />
                    <button className="btn btn-primary" onClick={handleGnucashImportClick} disabled={importingGnucash || resetting} style={{ marginTop: 8 }}>
                        {importingGnucash ? t('common.loading') || 'Loading...' : t('data.importGnucashBtn') || 'Import from GnuCash'}
                    </button>
                </div>

                <hr style={{ border: 'none', borderTop: '1px solid var(--border-color)', margin: '16px 0' }} />

                <div>
                    <h3>{t('data.importCsvTitle')}</h3>
                    <p style={{ color: 'var(--text-muted)' }}>{t('data.importCsvDesc')}</p>
                    
                    <div style={{ display: 'flex', gap: 8, alignItems: 'center', marginTop: 12 }}>
                        <select 
                            className="form-control" 
                            value={selectedAccount} 
                            onChange={(e) => setSelectedAccount(e.target.value)}
                            style={{ flex: 1, padding: '8px 12px', borderRadius: 6, border: '1px solid var(--border-color)', background: 'var(--bg-card)', color: 'var(--text-main)' }}
                        >
                            <option value="">{t('data.selectTargetAccount')}</option>
                            {accounts.map(acc => (
                                <option key={acc.guid} value={acc.guid}>
                                    {acc.name} ({t(`type.${acc.account_type}`)})
                                </option>
                            ))}
                        </select>

                        <input
                            type="file"
                            accept=".csv"
                            ref={csvInputRef}
                            onChange={handleCSVFileChange}
                            style={{ display: 'none' }}
                        />
                        <button 
                            className="btn btn-primary" 
                            onClick={handleCSVImportClick} 
                            disabled={importingCSV || !selectedAccount}
                        >
                            {importingCSV ? t('common.loading') : t('data.importCsvBtn')}
                        </button>
                    </div>
                </div>

                <hr style={{ border: 'none', borderTop: '1px solid rgba(244, 63, 94, 0.3)', margin: '16px 0' }} />

                <div>
                    <h3 style={{ color: 'var(--color-expense)' }}>Danger Zone</h3>
                    <p style={{ color: 'var(--text-muted)' }}>Permanently delete all data in your account and reset to the default chart of accounts.</p>
                    <button className="btn btn-danger" onClick={handleFactoryReset} disabled={resetting || importing} style={{ marginTop: 8 }}>
                        {resetting ? t('common.loading') || 'Loading...' : 'Factory Reset Account'}
                    </button>
                </div>
            </div>

            {/* ─── Connect Bank ───────────────────────────────────────────── */}
            <section className="data-section" style={{ marginTop: 24 }}>
                <h2>{t('plaid.connectBank')}</h2>

                {plaidMessage && (
                    <div className={`message ${plaidMessage.type}`}>{plaidMessage.text}</div>
                )}

                {plaidStep === 'idle' && (
                    <button className="btn btn-primary" onClick={handleConnectBank} disabled={plaidConnecting}>
                        {plaidConnecting ? t('plaid.connecting') : t('plaid.connectBank')}
                    </button>
                )}

                {plaidStep === 'mapping' && plaidItem && (
                    <div className="plaid-mapping">
                        <p><strong>{plaidItem.institution}</strong> — {t('plaid.mapAccounts')}</p>
                        <table>
                            <tbody>
                                {plaidBankAccounts.map(ba => (
                                    <tr key={ba.account_id}>
                                        <td>{ba.name} (…{ba.mask})</td>
                                        <td>
                                            <select
                                                value={plaidMappings[ba.account_id] ?? ''}
                                                onChange={e => setPlaidMappings(m => ({ ...m, [ba.account_id]: e.target.value }))}
                                            >
                                                <option value="">{t('plaid.noMapping')}</option>
                                                {accounts.map(a => (
                                                    <option key={a.guid} value={a.guid}>{a.name}</option>
                                                ))}
                                            </select>
                                        </td>
                                    </tr>
                                ))}
                            </tbody>
                        </table>
                        <label>
                            <input
                                type="checkbox"
                                checked={plaidImportPending}
                                onChange={e => setPlaidImportPending(e.target.checked)}
                            />
                            {' '}{t('plaid.importPending')}
                        </label>
                        <div style={{ marginTop: '1rem' }}>
                            <button className="btn btn-primary" onClick={handleSubmitMappings} disabled={plaidConnecting}>
                                {plaidConnecting ? t('plaid.connecting') : t('plaid.save')}
                            </button>
                        </div>
                    </div>
                )}

                {plaidStep === 'done' && (
                    <p>{t('plaid.connected')} ✓</p>
                )}

                {plaidItems.length > 0 && (
                    <div className="plaid-items">
                        <h3>{t('plaid.connectedBanks')}</h3>
                        {plaidItems.map(item => (
                            <div key={item.guid} className="plaid-item-row">
                                <span>
                                    <strong>{item.institution_name}</strong>
                                    {' — '}
                                    {item.last_synced_at
                                        ? t('plaid.lastSynced').replace('{{date}}', new Date(item.last_synced_at).toLocaleString())
                                        : t('plaid.neverSynced')}
                                </span>
                                <span className="plaid-item-actions">
                                    <button
                                        className="btn btn-secondary"
                                        onClick={() => handlePlaidSyncNow(item)}
                                        disabled={syncingItemGuid !== null}
                                    >
                                        {syncingItemGuid === item.guid
                                            ? t('plaid.syncing').replace('{{institution}}', item.institution_name)
                                            : t('plaid.syncNow')}
                                    </button>
                                    <button className="btn btn-danger" onClick={() => handlePlaidDisconnect(item)}>
                                        {t('plaid.disconnect')}
                                    </button>
                                </span>
                            </div>
                        ))}
                    </div>
                )}
            </section>

            {dmSuggestions && (
                <ImportMatcher
                    institutionName={dmInstitution}
                    suggestions={dmSuggestions}
                    onClose={() => setDmSuggestions(null)}
                    onImported={(count) => {
                        setDmSuggestions(null);
                        setPlaidMessage({ type: 'success', text: t('plaid.importSuccess').replace('{{count}}', String(count)) });
                    }}
                />
            )}
        </div>
    );
}
