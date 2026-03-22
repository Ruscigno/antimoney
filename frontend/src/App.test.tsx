import { describe, it, expect, vi } from 'vitest';
import { render, screen } from '@testing-library/react';
import { AuthProvider } from './auth/AuthContext';
import App from './App';

vi.mock('./api/client', () => ({
    apiClient: {
        get: vi.fn().mockResolvedValue({ data: { user_id: '123' } }),
    },
}));

describe('App', () => {
    it('renders login page when not authenticated', () => {
        // Note: since the client checks local storage, we just render and verify login text wrapper
        render(
            <AuthProvider>
                <App />
            </AuthProvider>
        );

        // If it navigates to login
        expect(screen.getAllByText(/Sign In/i)[0]).toBeInTheDocument();
    });
});
