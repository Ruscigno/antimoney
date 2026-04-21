import { useState, useRef, useEffect } from 'react';
import type { Account } from '../types';
import { t } from '../i18n';

interface AccountPickerProps {
    accounts: Account[];
    value: string;
    onChange: (guid: string) => void;
    id?: string;
    tabIndex?: number;
}

/**
 * Searchable account picker with type-ahead filtering.
 * Replaces the plain <select> with a combobox that filters as you type.
 */
export default function AccountPicker({ accounts, value, onChange, id, tabIndex }: AccountPickerProps) {
    const [query, setQuery] = useState('');
    const [open, setOpen] = useState(false);
    const [highlightIdx, setHighlightIdx] = useState(0);
    const wrapperRef = useRef<HTMLDivElement>(null);
    const inputRef = useRef<HTMLInputElement>(null);

    // Get selected account name for display
    const selected = accounts.find(a => a.guid === value);

    // Filter accounts by query
    const filtered = query
        ? accounts.filter(a =>
            a.name.toLowerCase().includes(query.toLowerCase()) ||
            a.account_type.toLowerCase().includes(query.toLowerCase())
        )
        : accounts;

    // Close when clicking outside
    useEffect(() => {
        function handleClickOutside(e: MouseEvent) {
            if (wrapperRef.current && !wrapperRef.current.contains(e.target as Node)) {
                setOpen(false);
                setQuery('');
            }
        }
        document.addEventListener('mousedown', handleClickOutside);
        return () => document.removeEventListener('mousedown', handleClickOutside);
    }, []);

    // Reset highlight when filtered list changes
    useEffect(() => {
        setHighlightIdx(0);
    }, [query]);

    const handleSelect = (guid: string) => {
        onChange(guid);
        setOpen(false);
        setQuery('');
    };

    const handleKeyDown = (e: React.KeyboardEvent) => {
        if (!open) {
            if (e.key === 'ArrowDown' || e.key === 'Enter') {
                e.preventDefault();
                setOpen(true);
            }
            return;
        }

        switch (e.key) {
            case 'ArrowDown':
                e.preventDefault();
                setHighlightIdx(i => Math.min(i + 1, filtered.length - 1));
                break;
            case 'ArrowUp':
                e.preventDefault();
                setHighlightIdx(i => Math.max(i - 1, 0));
                break;
            case 'Enter':
                e.preventDefault();
                if (filtered[highlightIdx]) {
                    handleSelect(filtered[highlightIdx].guid);
                }
                break;
            case 'Escape':
                e.preventDefault();
                e.stopPropagation(); // Don't close the parent modal
                setOpen(false);
                setQuery('');
                break;
        }
    };

    const typeLabel = (type: string) => t(`type.${type}`);

    return (
        <div className="account-picker" ref={wrapperRef}>
            <div
                className={`account-picker-trigger form-input ${open ? 'focused' : ''}`}
                tabIndex={open ? -1 : (tabIndex ?? 0)}
                onClick={() => {
                    setOpen(true);
                    setTimeout(() => inputRef.current?.focus(), 0);
                }}
                onKeyDown={handleKeyDown}
            >
                {open ? (
                    <input
                        ref={inputRef}
                        type="text"
                        className="account-picker-search"
                        placeholder={t('form.selectAccount')}
                        value={query}
                        onChange={e => setQuery(e.target.value)}
                        onKeyDown={handleKeyDown}
                        id={id}
                    />
                ) : (
                    <span className={selected ? '' : 'account-picker-placeholder'} id={id}>
                        {selected ? `${selected.name} (${typeLabel(selected.account_type)})` : t('form.selectAccount')}
                    </span>
                )}
                <span className="account-picker-arrow">{open ? '▲' : '▼'}</span>
            </div>

            {open && (
                <ul className="account-picker-dropdown">
                    {filtered.length === 0 ? (
                        <li className="account-picker-empty">No matching accounts</li>
                    ) : (
                        filtered.map((a, idx) => (
                            <li
                                key={a.guid}
                                className={`account-picker-option ${idx === highlightIdx ? 'highlighted' : ''} ${a.guid === value ? 'selected' : ''}`}
                                onClick={() => handleSelect(a.guid)}
                                onMouseEnter={() => setHighlightIdx(idx)}
                            >
                                <span className="account-picker-option-name">{a.name}</span>
                                <span className="account-picker-option-type">{typeLabel(a.account_type)}</span>
                            </li>
                        ))
                    )}
                </ul>
            )}
        </div>
    );
}
