import { useState, useEffect } from 'react';
import { t } from '../i18n';
import {
    getSnapshotConfig,
    upsertSnapshotConfig,
    listSnapshots,
    takeSnapshot,
    restoreSnapshot,
    deleteSnapshot,
} from '../api/client';
import type { SnapshotConfig, SnapshotSummary } from '../types';

function triggerLabel(trigger: string): string {
    if (trigger === 'scheduled') return t('snapshots.triggerScheduled');
    if (trigger === 'active') return t('snapshots.triggerActive');
    return t('snapshots.triggerManual');
}

function triggerBadgeStyle(trigger: string): React.CSSProperties {
    const base: React.CSSProperties = {
        display: 'inline-block',
        padding: '2px 8px',
        borderRadius: 4,
        fontSize: '0.75rem',
        fontWeight: 600,
    };
    if (trigger === 'scheduled') return { ...base, background: '#3b82f620', color: '#3b82f6' };
    if (trigger === 'active') return { ...base, background: '#22c55e20', color: '#22c55e' };
    return { ...base, background: '#64748b20', color: '#64748b' };
}

export default function Snapshots() {
    const [, setConfig] = useState<SnapshotConfig | null>(null);
    const [snapshots, setSnapshots] = useState<SnapshotSummary[]>([]);
    const [frequencyHours, setFrequencyHours] = useState(0);
    const [ttlHours, setTtlHours] = useState(0);
    const [activeMode, setActiveMode] = useState(false);
    const [label, setLabel] = useState('');
    const [saving, setSaving] = useState(false);
    const [taking, setTaking] = useState(false);
    const [restoringId, setRestoringId] = useState<string | null>(null);
    const [deletingId, setDeletingId] = useState<string | null>(null);
    const [message, setMessage] = useState<{ type: 'success' | 'error'; text: string } | null>(null);

    useEffect(() => {
        Promise.all([
            getSnapshotConfig().catch(() => null),
            listSnapshots().catch(() => []),
        ]).then(([cfg, snaps]) => {
            if (cfg) {
                setConfig(cfg);
                setFrequencyHours(cfg.frequency_hours);
                setTtlHours(cfg.ttl_hours);
                setActiveMode(cfg.active_mode);
            }
            setSnapshots(snaps ?? []);
        });
    }, []);

    const showMessage = (type: 'success' | 'error', text: string) => {
        setMessage({ type, text });
        setTimeout(() => setMessage(null), 4000);
    };

    const handleSaveConfig = async (e: React.FormEvent) => {
        e.preventDefault();
        setSaving(true);
        try {
            const cfg = await upsertSnapshotConfig({
                frequency_hours: frequencyHours,
                ttl_hours: ttlHours,
                active_mode: activeMode,
            });
            setConfig(cfg);
            showMessage('success', t('snapshots.configSaved'));
        } catch {
            showMessage('error', t('snapshots.configError'));
        } finally {
            setSaving(false);
        }
    };

    const handleTakeSnapshot = async (e: React.FormEvent) => {
        e.preventDefault();
        setTaking(true);
        try {
            const ss = await takeSnapshot(label.trim());
            setSnapshots(prev => [ss, ...prev]);
            setLabel('');
            showMessage('success', t('snapshots.snapshotTaken'));
        } catch {
            showMessage('error', t('snapshots.snapshotError'));
        } finally {
            setTaking(false);
        }
    };

    const handleRestore = async (id: string) => {
        if (!window.confirm(t('snapshots.restoreConfirm'))) return;
        setRestoringId(id);
        try {
            await restoreSnapshot(id);
            showMessage('success', t('snapshots.restoreSuccess'));
            setTimeout(() => window.location.reload(), 1000);
        } catch {
            showMessage('error', t('snapshots.restoreError'));
            setRestoringId(null);
        }
    };

    const handleDelete = async (id: string) => {
        if (!window.confirm(t('snapshots.deleteConfirm'))) return;
        setDeletingId(id);
        try {
            await deleteSnapshot(id);
            setSnapshots(prev => prev.filter(s => s.id !== id));
            showMessage('success', t('snapshots.deleteSuccess'));
        } catch {
            showMessage('error', t('snapshots.deleteError'));
        } finally {
            setDeletingId(null);
        }
    };

    return (
        <div className="page-container">
            <div className="page-header">
                <h1 className="page-title">{t('snapshots.title')}</h1>
                <p className="page-subtitle">{t('snapshots.subtitle')}</p>
            </div>

            {message && (
                <div
                    className={`message ${message.type === 'success' ? 'message-success' : 'message-error'}`}
                    style={{ marginBottom: 16 }}
                >
                    {message.text}
                </div>
            )}

            {/* Schedule configuration */}
            <div className="card" style={{ marginBottom: 24 }}>
                <h2 className="card-title">{t('snapshots.scheduleTitle')}</h2>
                <p style={{ color: 'var(--color-text-secondary)', marginBottom: 16, fontSize: '0.9rem' }}>
                    {t('snapshots.scheduleDesc')}
                </p>
                <form onSubmit={handleSaveConfig}>
                    <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 16, marginBottom: 16 }}>
                        <div className="form-group">
                            <label className="form-label">{t('snapshots.frequency')}</label>
                            <input
                                type="number"
                                min={0}
                                className="form-input"
                                value={frequencyHours}
                                onChange={e => setFrequencyHours(Math.max(0, parseInt(e.target.value) || 0))}
                            />
                        </div>
                        <div className="form-group">
                            <label className="form-label">{t('snapshots.ttl')}</label>
                            <input
                                type="number"
                                min={0}
                                className="form-input"
                                value={ttlHours}
                                onChange={e => setTtlHours(Math.max(0, parseInt(e.target.value) || 0))}
                            />
                        </div>
                    </div>
                    <div style={{ display: 'flex', alignItems: 'center', gap: 10, marginBottom: 20 }}>
                        <input
                            type="checkbox"
                            id="activeMode"
                            checked={activeMode}
                            onChange={e => setActiveMode(e.target.checked)}
                            style={{ width: 16, height: 16 }}
                        />
                        <label htmlFor="activeMode" style={{ fontSize: '0.9rem', cursor: 'pointer' }}>
                            {t('snapshots.activeMode')}
                        </label>
                    </div>
                    <button type="submit" className="btn btn-primary" disabled={saving}>
                        {saving ? t('common.saving') : t('snapshots.saveConfig')}
                    </button>
                </form>
            </div>

            {/* Manual snapshot */}
            <div className="card" style={{ marginBottom: 24 }}>
                <h2 className="card-title">{t('snapshots.manualTitle')}</h2>
                <p style={{ color: 'var(--color-text-secondary)', marginBottom: 16, fontSize: '0.9rem' }}>
                    {t('snapshots.manualDesc')}
                </p>
                <form onSubmit={handleTakeSnapshot} style={{ display: 'flex', gap: 12, alignItems: 'flex-end' }}>
                    <div className="form-group" style={{ flex: 1, marginBottom: 0 }}>
                        <label className="form-label">{t('snapshots.label')}</label>
                        <input
                            type="text"
                            className="form-input"
                            placeholder={t('snapshots.labelPlaceholder')}
                            value={label}
                            onChange={e => setLabel(e.target.value)}
                            maxLength={200}
                        />
                    </div>
                    <button type="submit" className="btn btn-primary" disabled={taking} style={{ whiteSpace: 'nowrap' }}>
                        {taking ? t('snapshots.taking') : t('snapshots.takeNow')}
                    </button>
                </form>
            </div>

            {/* Snapshot list */}
            <div className="card">
                <h2 className="card-title">
                    {t('snapshots.listTitle')}
                    {snapshots.length > 0 && (
                        <span style={{
                            marginLeft: 8,
                            background: 'var(--color-border)',
                            borderRadius: 12,
                            padding: '2px 8px',
                            fontSize: '0.8rem',
                            fontWeight: 500,
                        }}>
                            {snapshots.length}
                        </span>
                    )}
                </h2>

                {snapshots.length === 0 ? (
                    <p style={{ color: 'var(--color-text-secondary)', fontStyle: 'italic' }}>
                        {t('snapshots.noSnapshots')}
                    </p>
                ) : (
                    <table className="table" style={{ width: '100%' }}>
                        <thead>
                            <tr>
                                <th>{t('snapshots.colDate')}</th>
                                <th>{t('snapshots.colLabel')}</th>
                                <th>{t('snapshots.colSource')}</th>
                                <th style={{ textAlign: 'right' }}>{t('snapshots.colActions')}</th>
                            </tr>
                        </thead>
                        <tbody>
                            {snapshots.map(s => (
                                <tr key={s.id}>
                                    <td style={{ whiteSpace: 'nowrap', fontSize: '0.85rem' }}>
                                        {new Date(s.created_at).toLocaleString()}
                                    </td>
                                    <td style={{ color: s.label ? 'inherit' : 'var(--color-text-secondary)', fontStyle: s.label ? 'normal' : 'italic' }}>
                                        {s.label || '—'}
                                    </td>
                                    <td>
                                        <span style={triggerBadgeStyle(s.trigger)}>
                                            {triggerLabel(s.trigger)}
                                        </span>
                                    </td>
                                    <td style={{ textAlign: 'right', whiteSpace: 'nowrap' }}>
                                        <button
                                            className="btn btn-primary"
                                            style={{ marginRight: 8, fontSize: '0.8rem', padding: '4px 10px' }}
                                            onClick={() => handleRestore(s.id)}
                                            disabled={restoringId === s.id || deletingId === s.id}
                                        >
                                            {restoringId === s.id ? t('snapshots.restoring') : t('snapshots.restore')}
                                        </button>
                                        <button
                                            className="btn btn-secondary"
                                            style={{ fontSize: '0.8rem', padding: '4px 10px' }}
                                            onClick={() => handleDelete(s.id)}
                                            disabled={restoringId === s.id || deletingId === s.id}
                                        >
                                            {t('snapshots.delete')}
                                        </button>
                                    </td>
                                </tr>
                            ))}
                        </tbody>
                    </table>
                )}
            </div>
        </div>
    );
}
