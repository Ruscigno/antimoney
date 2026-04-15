import { createContext, useContext, useState, useEffect, type ReactNode } from 'react';

interface AuthUser {
    user_id: string;
    book_guid: string;
    email: string;
    name: string;
}

interface AuthContextValue {
    user: AuthUser | null;
    login: (email: string, password: string) => Promise<void>;
    register: (email: string, password: string, name: string) => Promise<void>;
    logout: () => Promise<void>;
    loading: boolean;
}

export const AuthContext = createContext<AuthContextValue | null>(null);

export function useAuth() {
    const ctx = useContext(AuthContext);
    if (!ctx) throw new Error('useAuth must be used within AuthProvider');
    return ctx;
}

const AUTH_BASE = '/auth';
const USER_CACHE_KEY = 'antimoney-user';

export function AuthProvider({ children }: { children: ReactNode }) {
    const [user, setUser] = useState<AuthUser | null>(null);
    const [loading, setLoading] = useState(true);

    // On mount: restore cached user info immediately, then validate with server.
    // The HttpOnly auth_token cookie is sent automatically — no manual token handling.
    useEffect(() => {
        const saved = localStorage.getItem(USER_CACHE_KEY);
        if (saved) {
            try { setUser(JSON.parse(saved) as AuthUser); } catch { /* stale cache */ }
        }

        fetch(`${AUTH_BASE}/me`, { credentials: 'include' })
            .then(r => {
                if (!r.ok) throw new Error('invalid');
                return r.json();
            })
            .then(data => {
                const u: AuthUser = {
                    user_id: data.user_id,
                    book_guid: data.book_guid,
                    email: data.email,
                    name: data.name || '',
                };
                setUser(u);
                localStorage.setItem(USER_CACHE_KEY, JSON.stringify(u));
            })
            .catch(() => {
                localStorage.removeItem(USER_CACHE_KEY);
                setUser(null);
            })
            .finally(() => setLoading(false));
    }, []);

    const login = async (email: string, password: string) => {
        const res = await fetch(`${AUTH_BASE}/login`, {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            credentials: 'include',
            body: JSON.stringify({ email, password }),
        });
        if (!res.ok) {
            const body = await res.json().catch(() => ({}));
            throw new Error(body.error || 'Login failed');
        }
        const data = await res.json();
        const u: AuthUser = {
            user_id: data.user_id,
            book_guid: data.book_guid,
            email: data.email,
            name: data.name || '',
        };
        setUser(u);
        localStorage.setItem(USER_CACHE_KEY, JSON.stringify(u));
    };

    const register = async (email: string, password: string, name: string) => {
        const res = await fetch(`${AUTH_BASE}/register`, {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            credentials: 'include',
            body: JSON.stringify({ email, password, name }),
        });
        if (!res.ok) {
            const body = await res.json().catch(() => ({}));
            throw new Error(body.error || 'Registration failed');
        }
        const data = await res.json();
        const u: AuthUser = {
            user_id: data.user_id,
            book_guid: data.book_guid,
            email: data.email,
            name: data.name || '',
        };
        setUser(u);
        localStorage.setItem(USER_CACHE_KEY, JSON.stringify(u));
    };

    const logout = async () => {
        // Best-effort: revoke the JTI on the server and clear the cookie.
        await fetch(`${AUTH_BASE}/logout`, {
            method: 'POST',
            credentials: 'include',
        }).catch(() => { /* server unreachable — local state still cleared */ });
        localStorage.removeItem(USER_CACHE_KEY);
        setUser(null);
    };

    return (
        <AuthContext.Provider value={{ user, login, register, logout, loading }}>
            {children}
        </AuthContext.Provider>
    );
}
