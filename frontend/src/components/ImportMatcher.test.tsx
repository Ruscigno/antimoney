import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, fireEvent, waitFor } from '@testing-library/react';
import ImportMatcher from './ImportMatcher';
import type { Account, SyncSuggestion } from '../types';

// Hoisted mocks so the vi.mock factory can reference them safely.
const { getAccounts, plaidImport } = vi.hoisted(() => ({
    getAccounts: vi.fn(),
    plaidImport: vi.fn(),
}));

vi.mock('../api/client', () => ({ getAccounts, plaidImport }));
// Return the key itself so assertions don't depend on translation text.
vi.mock('../i18n', () => ({ t: (k: string) => k }));

const acct = (over: Partial<Account>): Account => ({
    guid: 'x', name: 'x', account_type: 'EXPENSE', parent_guid: null,
    placeholder: false, description: '', metadata: {}, version: 1,
    balance: 0, reconciled_balance: 0, ...over,
});

// A placeholder parent (must be filtered out) with a real child, plus a real sibling.
const accounts: Account[] = [
    acct({
        guid: 'exp', name: 'Expenses', placeholder: true,
        children: [acct({ guid: 'groc', name: 'Groceries', parent_guid: 'exp' })],
    }),
    acct({ guid: 'rest', name: 'Restaurants' }),
];

const suggestion = (over: Partial<SyncSuggestion> = {}): SyncSuggestion => ({
    transaction_id: 'tx1',
    date: '2026-06-01',
    description: 'COFFEE SHOP',
    amount_num: 542,
    amount_denom: 100,
    bank_account_guid: 'bank1',
    bank_account_name: 'RBC Chequing',
    suggested_category_guid: '',
    suggested_category_name: '',
    ...over,
});

const confirmBtn = () => screen.getByRole('button', { name: /confirmImport/ });

beforeEach(() => {
    getAccounts.mockReset().mockResolvedValue(accounts);
    plaidImport.mockReset().mockResolvedValue({ imported: 1 });
});

describe('ImportMatcher', () => {
    it('renders a row per suggestion and filters placeholder accounts from the category list', async () => {
        render(<ImportMatcher institutionName="RBC" suggestions={[suggestion()]} onClose={() => {}} onImported={() => {}} />);

        expect(screen.getByText('COFFEE SHOP')).toBeInTheDocument();
        expect(screen.getByText('5.42')).toBeInTheDocument();
        expect(screen.getByText('RBC Chequing')).toBeInTheDocument();

        await waitFor(() => expect(screen.getByRole('option', { name: 'Groceries' })).toBeInTheDocument());
        expect(screen.getByRole('option', { name: 'Restaurants' })).toBeInTheDocument();
        expect(screen.queryByRole('option', { name: 'Expenses' })).not.toBeInTheDocument();
    });

    it('disables confirm until every included row has a category', async () => {
        render(<ImportMatcher institutionName="RBC" suggestions={[suggestion()]} onClose={() => {}} onImported={() => {}} />);
        await waitFor(() => expect(screen.getByRole('option', { name: 'Groceries' })).toBeInTheDocument());

        expect(confirmBtn()).toBeDisabled();
        fireEvent.change(screen.getByRole('combobox'), { target: { value: 'groc' } });
        expect(confirmBtn()).toBeEnabled();
    });

    it('imports the categorized rows and reports the imported count', async () => {
        const onImported = vi.fn();
        render(<ImportMatcher institutionName="RBC" suggestions={[suggestion({ suggested_category_guid: 'groc' })]} onClose={() => {}} onImported={onImported} />);
        await waitFor(() => expect(screen.getByRole('option', { name: 'Groceries' })).toBeInTheDocument());

        fireEvent.click(confirmBtn());

        await waitFor(() => expect(onImported).toHaveBeenCalledWith(1));
        expect(plaidImport).toHaveBeenCalledWith([
            expect.objectContaining({
                transaction_id: 'tx1',
                bank_account_guid: 'bank1',
                category_account_guid: 'groc',
                amount_num: 542,
                amount_denom: 100,
            }),
        ]);
    });

    it('disables confirm when every row is excluded', async () => {
        render(<ImportMatcher institutionName="RBC" suggestions={[suggestion({ suggested_category_guid: 'groc' })]} onClose={() => {}} onImported={() => {}} />);
        await waitFor(() => expect(screen.getByRole('option', { name: 'Groceries' })).toBeInTheDocument());

        expect(confirmBtn()).toBeEnabled();
        fireEvent.click(screen.getByRole('checkbox'));
        expect(confirmBtn()).toBeDisabled();
    });

    it('calls onClose when cancel is clicked', async () => {
        const onClose = vi.fn();
        render(<ImportMatcher institutionName="RBC" suggestions={[suggestion()]} onClose={onClose} onImported={() => {}} />);
        // Let the async getAccounts effect settle before asserting (avoids act warning).
        await waitFor(() => expect(screen.getByRole('option', { name: 'Groceries' })).toBeInTheDocument());
        fireEvent.click(screen.getByRole('button', { name: 'plaid.cancel' }));
        expect(onClose).toHaveBeenCalled();
    });
});
