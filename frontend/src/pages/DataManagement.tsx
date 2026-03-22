import { useState, useRef } from 'react';
import { t } from '../i18n';

export default function DataManagement() {
    const [importing, setImporting] = useState(false);
    const [exporting, setExporting] = useState(false);
    const [message, setMessage] = useState<{ type: 'success' | 'error', text: string } | null>(null);
    const fileInputRef = useRef<HTMLInputElement>(null);

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
                    <button className="btn btn-primary" onClick={handleImportClick} disabled={importing} style={{ marginTop: 8 }}>
                        {importing ? t('common.loading') || 'Loading...' : t('data.importBtn') || 'Import from JSON'}
                    </button>
                </div>
            </div>
        </div>
    );
}
