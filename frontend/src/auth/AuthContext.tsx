import { createContext, useContext, useState, useEffect, type ReactNode } from 'react';

interface AuthUser {
    user_id: string;
    book_guid: string;
    email: string;
    name: string;
}

interface AuthContextValue {
    user: AuthUser | null;
    token: string | null;
    login: (email: string, password: string) => Promise<void>;
    register: (email: string, password: string, name: string) => Promise<void>;
    logout: () => void;
    loading: boolean;
}

export const AuthContext = createContext<AuthContextValue | null>(null);

export function useAuth() {
    const ctx = useContext(AuthContext);
    if (!ctx) throw new Error('useAuth must be used within AuthProvider');
    return ctx;
}

const AUTH_BASE = '/auth';

export function AuthProvider({ children }: { children: ReactNode }) {
    const [user, setUser] = useState<AuthUser | null>(null);
    const [token, setToken] = useState<string | null>(null);
    const [loading, setLoading] = useState(true);

    // Restore token from localStorage on mount
    useEffect(() => {
        const saved = localStorage.getItem('antimoney-token');
        if (saved) {
            setToken(saved);
            // Validate token
            fetch(`${AUTH_BASE}/me`, {
                headers: { Authorization: `Bearer ${saved}` },
            })
                .then(r => {
                    if (!r.ok) throw new Error('invalid');
                    return r.json();
                })
                .then(data => {
                    setUser(data);
                    setToken(saved);
                })
                .catch(() => {
                    localStorage.removeItem('antimoney-token');
                    setToken(null);
                })
                .finally(() => setLoading(false));
        } else {
            setLoading(false);
        }
    }, []);

    const login = async (email: string, password: string) => {
        const res = await fetch(`${AUTH_BASE}/login`, {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ email, password }),
        });
        if (!res.ok) {
            const body = await res.json().catch(() => ({}));
            throw new Error(body.error || 'Login failed');
        }
        const data = await res.json();
        localStorage.setItem('antimoney-token', data.token);
        setToken(data.token);
        setUser({
            user_id: data.user_id,
            book_guid: data.book_guid,
            email: data.email,
            name: data.name,
        });
    };

    const register = async (email: string, password: string, name: string) => {
        const res = await fetch(`${AUTH_BASE}/register`, {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ email, password, name }),
        });
        if (!res.ok) {
            const body = await res.json().catch(() => ({}));
            throw new Error(body.error || 'Registration failed');
        }
        const data = await res.json();
        localStorage.setItem('antimoney-token', data.token);
        setToken(data.token);
        setUser({
            user_id: data.user_id,
            book_guid: data.book_guid,
            email: data.email,
            name: data.name,
        });
    };

    const logout = () => {
        localStorage.removeItem('antimoney-token');
        setToken(null);
        setUser(null);
    };

    return (
        <AuthContext.Provider value={{ user, token, login, register, logout, loading }}>
            {children}
        </AuthContext.Provider>
    );
}
