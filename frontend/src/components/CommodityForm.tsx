import { useState, useRef, useEffect } from 'react';
import { createCommodity } from '../api/client';
import { t } from '../i18n';
import { useShortcut } from '../hooks/useShortcuts';

interface CommodityFormProps {
    onClose: () => void;
    onCreated: () => void;
}

export default function CommodityForm({ onClose, onCreated }: CommodityFormProps) {
    const [namespace, setNamespace] = useState('CURRENCY');
    const [mnemonic, setMnemonic] = useState('');
    const [fullname, setFullname] = useState('');
    const [fraction, setFraction] = useState('100');
    const [error, setError] = useState<string | null>(null);
    const [loading, setLoading] = useState(false);

    const inputRef = useRef<HTMLInputElement>(null);

    useShortcut('Escape', onClose, t('shortcuts.close'), undefined, [onClose]);

    useEffect(() => {
        setTimeout(() => inputRef.current?.focus(), 100);
    }, []);

    const handleSubmit = async (e: React.FormEvent) => {
        e.preventDefault();
        setError(null);

        const fracNum = parseInt(fraction, 10);
        if (isNaN(fracNum) || fracNum <= 0) {
            setError(t('commodities.fractionInvalid'));
            return;
        }

        if (!mnemonic.trim()) {
            setError(t('commodities.mnemonicRequired'));
            return;
        }

        setLoading(true);
        try {
            await createCommodity({
                namespace: namespace.trim().toUpperCase(),
                mnemonic: mnemonic.trim().toUpperCase(),
                fullname: fullname.trim(),
                fraction: fracNum,
            });
            onCreated();
            onClose();
        } catch (err: any) {
            setError(err.message || t('commodities.createError'));
        } finally {
            setLoading(false);
        }
    };

    return (
        <div className="modal-overlay" onClick={onClose}>
            <div className="modal" onClick={e => e.stopPropagation()} style={{ maxWidth: 400 }}>
                <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: 20 }}>
                    <h2 className="modal-title" style={{ margin: 0 }}>{t('commodities.newCurrency')}</h2>
                    <kbd className="kbd-hint">Esc</kbd>
                </div>

                <form onSubmit={handleSubmit}>
                    <div className="form-group">
                        <label className="form-label">{t('commodities.namespace')}</label>
                        <input
                            type="text"
                            className="form-input"
                            value={namespace}
                            onChange={e => setNamespace(e.target.value.toUpperCase())}
                            required
                        />
                    </div>

                    <div className="form-group">
                        <label className="form-label">{t('commodities.mnemonic')}</label>
                        <input
                            ref={inputRef}
                            type="text"
                            className="form-input"
                            placeholder="e.g. BRL, USD, BTC"
                            value={mnemonic}
                            onChange={e => setMnemonic(e.target.value.toUpperCase())}
                            required
                        />
                    </div>

                    <div className="form-group">
                        <label className="form-label">{t('commodities.fullname')} (Optional)</label>
                        <input
                            type="text"
                            className="form-input"
                            placeholder={t('commodities.fullnamePlaceholder')}
                            value={fullname}
                            onChange={e => setFullname(e.target.value)}
                        />
                    </div>

                    <div className="form-group">
                        <label className="form-label">{t('commodities.fraction')} (Smallest Unit)</label>
                        <select
                            className="form-input"
                            value={fraction}
                            onChange={e => setFraction(e.target.value)}
                        >
                            <option value="1">1 (No fractions)</option>
                            <option value="10">10 (1/10th)</option>
                            <option value="100">100 (Cents - Default)</option>
                            <option value="1000">1000 (Mills)</option>
                            <option value="10000">10000</option>
                            <option value="100000000">100000000 (Satoshis)</option>
                        </select>
                    </div>

                    {error && <div className="error-message" style={{ marginBottom: 15 }}>{error}</div>}

                    <div className="form-actions" style={{ display: 'flex', justifyContent: 'flex-end', gap: 10, marginTop: 20 }}>
                        <button type="button" className="btn btn-secondary" onClick={onClose} disabled={loading}>
                            {t('form.cancel')}
                        </button>
                        <button type="submit" className="btn btn-primary" disabled={loading}>
                            {loading ? t('form.creating') : t('commodities.newCurrency')}
                        </button>
                    </div>
                </form>
            </div>
        </div>
    );
}
