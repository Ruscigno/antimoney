import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, waitFor } from '@testing-library/react';
import { MemoryRouter, Routes, Route } from 'react-router-dom';
import AccountRegister from './AccountRegister';
import type { Account } from '../types';

const h = vi.hoisted(() => ({
    getAccount: vi.fn(),
    getAccounts: vi.fn(),
    getAccountRegister: vi.fn(),
    getAccountRegisterPaged: vi.fn(),
    deleteTransaction: vi.fn(),
    plaidSync: vi.fn(),
    plaidImport: vi.fn(),
    plaidDismiss: vi.fn(),
}));

vi.mock('../api/client', () => h);
vi.mock('../i18n', () => ({ t: (k: string) => k }));
vi.mock('../hooks/useShortcuts', () => ({ useShortcut: vi.fn() }));
vi.mock('../components/Register', () => ({ default: () => <div data-testid="register" /> }));
vi.mock('../components/TransactionForm', () => ({ default: () => null }));
vi.mock('../components/ReconcileWizard', () => ({ default: () => null }));
vi.mock('../components/AccountBreadcrumbs', () => ({ default: () => null }));

const account = (metadata: Record<string, unknown>): Account => ({
    guid: 'acc-1', name: 'Chequing', account_type: 'BANK', parent_guid: null,
    placeholder: false, description: '', metadata, version: 1,
    balance: 0, reconciled_balance: 0,
});

const emptyPage = { entries: [], has_before: false, has_after: false, first_offset: 0, last_offset: 0 };

function renderRegister() {
    return render(
        <MemoryRouter initialEntries={['/accounts/acc-1']}>
            <Routes>
                <Route path="/accounts/:id" element={<AccountRegister />} />
            </Routes>
        </MemoryRouter>,
    );
}

beforeEach(() => {
    h.getAccounts.mockReset().mockResolvedValue([]);
    h.getAccountRegisterPaged.mockReset().mockResolvedValue(emptyPage);
    h.plaidSync.mockReset().mockResolvedValue({ count: 0, suggestions: [], has_more: false });
    window.matchMedia = vi.fn().mockImplementation((q: string) => ({
        matches: false, media: q, onchange: null,
        addListener: vi.fn(), removeListener: vi.fn(),
        addEventListener: vi.fn(), removeEventListener: vi.fn(), dispatchEvent: vi.fn(),
    })) as unknown as typeof window.matchMedia;
});

describe('AccountRegister — Plaid auto-sync wiring', () => {
    it('triggers a sync on first open of the day for a stale linked account', async () => {
        h.getAccount.mockReset().mockResolvedValue(account({
            plaid: { item_guid: 'item-1', institution_name: 'RBC', last_synced_at: '2020-01-01T12:00:00Z' },
        }));

        renderRegister();

        await waitFor(() => expect(h.plaidSync).toHaveBeenCalledWith('item-1'));
    });

    it('does not sync an account without a Plaid link', async () => {
        h.getAccount.mockReset().mockResolvedValue(account({}));

        renderRegister();

        await waitFor(() => expect(h.getAccountRegisterPaged).toHaveBeenCalled());
        expect(h.plaidSync).not.toHaveBeenCalled();
    });
});
