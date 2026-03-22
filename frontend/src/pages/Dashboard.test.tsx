import { describe, it, expect, vi } from 'vitest';
import { render, screen, waitFor } from '@testing-library/react';
import Dashboard from '../pages/Dashboard';
import { BrowserRouter } from 'react-router-dom';

vi.mock('../api/client', () => ({
    apiClient: { get: vi.fn().mockResolvedValue({ data: [] }) },
    getAccounts: vi.fn().mockResolvedValue([]),
    getTransactions: vi.fn().mockResolvedValue([]),
    getReconciledBalance: vi.fn().mockResolvedValue(0),
}));

describe('Dashboard Page', () => {
    it('renders correctly', async () => {
        render(
            <BrowserRouter>
                <Dashboard />
            </BrowserRouter>
        );
        await waitFor(() => {
            expect(screen.getAllByText(/Overview/i)[0]).toBeInTheDocument();
        });
    });
});
