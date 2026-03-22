import { describe, it, expect, vi } from 'vitest';
import { render, screen, waitFor } from '@testing-library/react';
import Transactions from './Transactions';
import { BrowserRouter } from 'react-router-dom';

vi.mock('../api/client', () => ({
    apiClient: {
        get: vi.fn().mockResolvedValue({ data: [] }),
        post: vi.fn().mockResolvedValue({ data: {} }),
    },
    getTransactions: vi.fn().mockResolvedValue([]),
    deleteTransaction: vi.fn().mockResolvedValue({}),
}));

describe('Transactions Page', () => {
    it('renders correctly', async () => {
        // Need to supply window.matchMedia mock because of the useShortcuts hook or similar
        window.matchMedia = vi.fn().mockImplementation(query => ({
            matches: false,
            media: query,
            onchange: null,
            addListener: vi.fn(),
            removeListener: vi.fn(),
            addEventListener: vi.fn(),
            removeEventListener: vi.fn(),
            dispatchEvent: vi.fn(),
        }));

        render(
            <BrowserRouter>
                <Transactions />
            </BrowserRouter>
        );
        await waitFor(() => {
            expect(screen.getAllByText(/Transactions/i)[0]).toBeInTheDocument();
        });
    });
});
