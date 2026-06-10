import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, fireEvent, waitFor } from '@testing-library/react';
import DataManagement from './DataManagement';
import type { Account } from '../types';

// Hoisted mocks so the vi.mock factories can reference them.
const h = vi.hoisted(() => ({
    getAccounts: vi.fn(),
    plaidGetLinkToken: vi.fn(),
    plaidExchange: vi.fn(),
    plaidLink: vi.fn(),
    plaidListItems: vi.fn(),
    plaidDisconnect: vi.fn(),
    plaidSync: vi.fn(),
    plaidImport: vi.fn(),
    onSuccess: { fn: null as null | ((token: string, meta: unknown) => void) },
}));

vi.mock('../api/client', () => ({
    getAccounts: h.getAccounts,
    plaidGetLinkToken: h.plaidGetLinkToken,
    plaidExchange: h.plaidExchange,
    plaidLink: h.plaidLink,
    plaidListItems: h.plaidListItems,
    plaidDisconnect: h.plaidDisconnect,
    plaidSync: h.plaidSync,
    plaidImport: h.plaidImport,
}));

// Mock Plaid Link: capture onSuccess; open() fires it (simulating a successful login).
vi.mock('react-plaid-link', () => ({
    usePlaidLink: (config: { onSuccess: (t: string, m: unknown) => void }) => {
        h.onSuccess.fn = config.onSuccess;
        return { open: () => h.onSuccess.fn?.('public-token', {}), ready: true };
    },
}));

vi.mock('../i18n', () => ({ t: (k: string) => k }));

const acct = (over: Partial<Account>): Account => ({
    guid: 'x', name: 'x', account_type: 'ASSET', parent_guid: null,
    placeholder: false, description: '', metadata: {}, version: 1,
    balance: 0, reconciled_balance: 0, ...over,
});

const accounts: Account[] = [acct({ guid: 'a1', name: 'Chequing' }), acct({ guid: 'a2', name: 'Savings' })];
const bankAccounts = [{ account_id: 'pa1', name: 'RBC Chequing', mask: '1234', type: 'depository' }];

beforeEach(() => {
    h.getAccounts.mockReset().mockResolvedValue(accounts);
    h.plaidGetLinkToken.mockReset().mockResolvedValue({ link_token: 'lt' });
    h.plaidExchange.mockReset().mockResolvedValue({ item_guid: 'item1', institution_name: 'RBC', accounts: bankAccounts });
    h.plaidLink.mockReset().mockResolvedValue(undefined);
    h.plaidListItems.mockReset().mockResolvedValue({ items: [] });
    h.plaidDisconnect.mockReset().mockResolvedValue(undefined);
    h.plaidSync.mockReset().mockResolvedValue({ count: 0, suggestions: [] });
    h.plaidImport.mockReset().mockResolvedValue({ imported: 0 });
    h.onSuccess.fn = null;
    window.matchMedia = window.matchMedia || (vi.fn().mockImplementation((q: string) => ({
        matches: false, media: q, onchange: null,
        addListener: vi.fn(), removeListener: vi.fn(),
        addEventListener: vi.fn(), removeEventListener: vi.fn(), dispatchEvent: vi.fn(),
    })) as unknown as typeof window.matchMedia);
});

describe('DataManagement — Connect Bank mapping', () => {
    it('maps Plaid accounts to local accounts and submits the chosen mappings', async () => {
        const { container } = render(<DataManagement />);
        await waitFor(() => expect(h.getAccounts).toHaveBeenCalled());

        // idle → linking → (Plaid onSuccess) → exchange → mapping
        fireEvent.click(screen.getByRole('button', { name: 'plaid.connectBank' }));

        await waitFor(() =>
            expect(container.querySelector('.plaid-mapping')).toBeInTheDocument(),
        );
        // One row per Plaid bank account.
        expect(screen.getByText(/RBC Chequing/)).toBeInTheDocument();

        const select = container.querySelector('.plaid-mapping select') as HTMLSelectElement;
        const checkbox = container.querySelector('.plaid-mapping input[type="checkbox"]') as HTMLInputElement;
        fireEvent.change(select, { target: { value: 'a1' } });
        fireEvent.click(checkbox); // import_pending = true

        fireEvent.click(screen.getByRole('button', { name: 'plaid.save' }));

        await waitFor(() =>
            expect(h.plaidLink).toHaveBeenCalledWith('item1', [{ account_id: 'pa1', account_guid: 'a1' }], true),
        );
    });

    it('drops unmapped Plaid accounts from the submitted payload', async () => {
        const { container } = render(<DataManagement />);
        await waitFor(() => expect(h.getAccounts).toHaveBeenCalled());
        fireEvent.click(screen.getByRole('button', { name: 'plaid.connectBank' }));
        await waitFor(() => expect(container.querySelector('.plaid-mapping')).toBeInTheDocument());

        // Leave the mapping unselected → it must be filtered out.
        fireEvent.click(screen.getByRole('button', { name: 'plaid.save' }));
        await waitFor(() => expect(h.plaidLink).toHaveBeenCalledWith('item1', [], false));
    });
});

const connectedItem = {
    guid: 'item1',
    institution_name: 'RBC',
    last_synced_at: '2026-06-09T12:00:00Z',
    import_pending: false,
};

describe('DataManagement — Connected banks', () => {
    it('lists connected banks and disconnects after confirmation', async () => {
        h.plaidListItems.mockResolvedValue({ items: [connectedItem] });
        const confirmSpy = vi.spyOn(window, 'confirm').mockReturnValue(true);

        render(<DataManagement />);
        await waitFor(() => expect(screen.getByText('RBC')).toBeInTheDocument());
        expect(screen.getByText('plaid.connectedBanks')).toBeInTheDocument();

        fireEvent.click(screen.getByRole('button', { name: 'plaid.disconnect' }));
        await waitFor(() => expect(h.plaidDisconnect).toHaveBeenCalledWith('item1'));
        expect(confirmSpy).toHaveBeenCalled();
        confirmSpy.mockRestore();
    });

    it('does not disconnect when the confirmation is declined', async () => {
        h.plaidListItems.mockResolvedValue({ items: [connectedItem] });
        const confirmSpy = vi.spyOn(window, 'confirm').mockReturnValue(false);

        render(<DataManagement />);
        await waitFor(() => expect(screen.getByText('RBC')).toBeInTheDocument());
        fireEvent.click(screen.getByRole('button', { name: 'plaid.disconnect' }));

        expect(h.plaidDisconnect).not.toHaveBeenCalled();
        confirmSpy.mockRestore();
    });

    it('Sync now reports "no new transactions" when the sync is empty', async () => {
        h.plaidListItems.mockResolvedValue({ items: [connectedItem] });
        render(<DataManagement />);
        await waitFor(() => expect(screen.getByText('RBC')).toBeInTheDocument());

        fireEvent.click(screen.getByRole('button', { name: 'plaid.syncNow' }));
        await waitFor(() => expect(h.plaidSync).toHaveBeenCalledWith('item1'));
        await waitFor(() => expect(screen.getByText('plaid.syncNone')).toBeInTheDocument());
    });

    it('Sync now surfaces the reconnect-needed message on reconnect_required', async () => {
        h.plaidListItems.mockResolvedValue({ items: [connectedItem] });
        h.plaidSync.mockRejectedValue(new Error('reconnect_required'));
        render(<DataManagement />);
        await waitFor(() => expect(screen.getByText('RBC')).toBeInTheDocument());

        fireEvent.click(screen.getByRole('button', { name: 'plaid.syncNow' }));
        await waitFor(() => expect(screen.getByText('plaid.reconnectNeeded')).toBeInTheDocument());
    });
});
