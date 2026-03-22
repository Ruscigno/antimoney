import { describe, it, expect, vi } from 'vitest';
import { render, screen, waitFor } from '@testing-library/react';
import Accounts from './Accounts';
import { BrowserRouter } from 'react-router-dom';

vi.mock('../api/client', () => ({
    apiClient: {
        get: vi.fn().mockResolvedValue({ data: [] }),
        post: vi.fn().mockResolvedValue({ data: {} }),
    },
    getAccounts: vi.fn().mockResolvedValue([]),
    deleteAccount: vi.fn().mockResolvedValue({}),
}));

describe('Accounts Page', () => {
    it('renders correctly', async () => {
        render(
            <BrowserRouter>
                <Accounts />
            </BrowserRouter>
        );
        await waitFor(() => {
            expect(screen.getAllByText(/Accounts/i)[0]).toBeInTheDocument();
        });
    });
});
