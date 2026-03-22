import { useState, useEffect } from 'react';
import { getCommodities, deleteCommodity } from '../api/client';
import type { Commodity } from '../types';
import { t } from '../i18n';
import CommodityForm from '../components/CommodityForm';

export default function Commodities() {
    const [commodities, setCommodities] = useState<Commodity[]>([]);
    const [loading, setLoading] = useState(true);
    const [showForm, setShowForm] = useState(false);

    const loadData = () => {
        setLoading(true);
        getCommodities()
            .then(data => setCommodities(data || []))
            .catch(console.error)
            .finally(() => setLoading(false));
    };

    useEffect(() => { loadData(); }, []);

    const handleDelete = async (guid: string) => {
        if (!window.confirm(t('commodities.confirmDelete'))) return;
        try {
            await deleteCommodity(guid);
            loadData();
        } catch (err: any) {
            alert(err.message || 'Failed to delete');
        }
    };

    if (loading) {
        return <div className="loading"><div className="loading-spinner" />{t('common.loading')}</div>;
    }

    return (
        <div>
            <div className="page-header" style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'flex-start' }}>
                <div>
                    <h1 className="page-title">{t('commodities.title')}</h1>
                    <p className="page-subtitle">{t('commodities.subtitle')}</p>
                </div>
                <button className="btn btn-primary" onClick={() => setShowForm(true)} style={{ display: 'flex', alignItems: 'center', gap: '8px' }}>
                    + {t('commodities.newCurrency')}
                </button>
            </div>

            {commodities.length === 0 ? (
                <div className="empty-state">
                    <p>{t('commodities.noCommodities')}</p>
                </div>
            ) : (
                <div className="register-table-wrapper">
                    <table className="register-table">
                        <thead>
                            <tr>
                                <th>{t('commodities.namespace')}</th>
                                <th>{t('commodities.mnemonic')}</th>
                                <th>{t('commodities.fullname')}</th>
                                <th>{t('commodities.fraction')}</th>
                                <th style={{ textAlign: 'right', width: 80 }}>{t('common.actions')}</th>
                            </tr>
                        </thead>
                        <tbody>
                            {commodities.map(c => (
                                <tr key={c.guid}>
                                    <td><span className="badge" style={{ background: 'var(--bg-tertiary)' }}>{c.namespace}</span></td>
                                    <td style={{ fontWeight: 'bold' }}>{c.mnemonic}</td>
                                    <td>{c.fullname}</td>
                                    <td>1 / {c.fraction}</td>
                                    <td style={{ textAlign: 'right' }}>
                                        <button
                                            className="btn btn-danger btn-sm"
                                            onClick={() => handleDelete(c.guid)}
                                            style={{ padding: '4px 8px', fontSize: '0.75rem' }}
                                        >
                                            {t('transactions.delete')}
                                        </button>
                                    </td>
                                </tr>
                            ))}
                        </tbody>
                    </table>
                </div>
            )}

            {showForm && (
                <CommodityForm
                    onClose={() => setShowForm(false)}
                    onCreated={() => {
                        setShowForm(false);
                        loadData();
                    }}
                />
            )}
        </div>
    );
}
