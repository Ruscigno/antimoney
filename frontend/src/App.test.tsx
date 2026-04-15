import { describe, it, expect, vi } from 'vitest';
import { render, screen, waitFor } from '@testing-library/react';
import { AuthProvider } from './auth/AuthContext';
import App from './App';

// Mock /auth/me to return 401 (unauthenticated) so AuthProvider resolves quickly.
vi.stubGlobal('fetch', vi.fn().mockResolvedValue({
    ok: false,
    status: 401,
    json: async () => ({ error: 'invalid token' }),
}));

describe('App', () => {
    it('renders login page when not authenticated', async () => {
        render(
            <AuthProvider>
                <App />
            </AuthProvider>
        );

        // Wait for the /auth/me check to resolve and the login page to appear
        await waitFor(() => {
            expect(screen.getAllByText(/Sign In/i)[0]).toBeInTheDocument();
        });
    });
});
