import { useState, useRef, useEffect } from 'react';
import { t } from '../i18n';
import { getAccounts } from '../api/client';
import type { Account } from '../types';

export default function DataManagement() {
    const [accounts, setAccounts] = useState<Account[]>([]);
    const [selectedAccount, setSelectedAccount] = useState<string>('');
    const [importing, setImporting] = useState(false);
    const [importingCSV, setImportingCSV] = useState(false);
    const [exporting, setExporting] = useState(false);
    const [resetting, setResetting] = useState(false);
    const [message, setMessage] = useState<{ type: 'success' | 'error', text: string } | null>(null);
    const fileInputRef = useRef<HTMLInputElement>(null);
    const csvInputRef = useRef<HTMLInputElement>(null);

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
            const token = localStorage.getItem('antimoney-token');
            const res = await fetch('/api/data/export', {
                headers: { 'Authorization': `Bearer ${token}` }
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
            const token = localStorage.getItem('antimoney-token');
            const formData = new FormData();
            formData.append('file', file);

            const res = await fetch('/api/data/import', {
                method: 'POST',
                headers: { 'Authorization': `Bearer ${token}` },
                body: formData
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
            const token = localStorage.getItem('antimoney-token');
            const formData = new FormData();
            formData.append('file', file);
            formData.append('account_guid', selectedAccount);

            const res = await fetch('/api/data/import/csv', {
                method: 'POST',
                headers: { 'Authorization': `Bearer ${token}` },
                body: formData
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

    const handleFactoryReset = async () => {
        if (!window.confirm("Are you sure you want to factory reset your account?\n\nThis will PERMANENTLY delete all your transactions and custom accounts. This action cannot be undone.")) {
            return;
        }

        setResetting(true);
        setMessage(null);

        try {
            const token = localStorage.getItem('antimoney-token');
            const res = await fetch('/api/data/reset', {
                method: 'POST',
                headers: { 'Authorization': `Bearer ${token}` }
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
        </div>
    );
}
