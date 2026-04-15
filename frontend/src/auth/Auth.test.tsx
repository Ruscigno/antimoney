import { describe, it, expect, vi } from 'vitest';
import { render, screen } from '@testing-library/react';
import LoginPage from '../pages/LoginPage';
import { BrowserRouter } from 'react-router-dom';
import { AuthContext } from './AuthContext';

vi.mock('../../api/client', () => ({
    apiClient: {
        post: vi.fn().mockResolvedValue({ data: {} }),
    }
}));

describe('Auth Pages', () => {
    it('renders login correctly', () => {
        const mockAuthContext = {
            user: null,
            login: vi.fn(),
            register: vi.fn(),
            logout: vi.fn(),
            loading: false
        };

        render(
            <BrowserRouter>
                <AuthContext.Provider value={mockAuthContext}>
                    <LoginPage />
                </AuthContext.Provider>
            </BrowserRouter>
        );

        expect(screen.getByText(/Sign in to your account/i)).toBeInTheDocument();
    });
});
