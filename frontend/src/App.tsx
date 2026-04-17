import { useState } from 'react';
import { BrowserRouter, Routes, Route, Navigate } from 'react-router-dom';
import Sidebar from './components/Sidebar';
import Dashboard from './pages/Dashboard';
import Accounts from './pages/Accounts';
import AccountRegister from './pages/AccountRegister';
import Transactions from './pages/Transactions';
import DataManagement from './pages/DataManagement';
import Snapshots from './pages/Snapshots';
import LoginPage from './pages/LoginPage';
import { useGlobalShortcuts } from './hooks/useShortcuts';
import { initLocale } from './i18n';
import { AuthProvider, useAuth } from './auth/AuthContext';

// Initialize locale from localStorage before render
initLocale();

function AppContent() {
    useGlobalShortcuts();
    const { user, loading, logout } = useAuth();
    const [sidebarOpen, setSidebarOpen] = useState(false);

    if (loading) {
        return (
            <div className="loading" style={{ minHeight: '100vh', display: 'flex', alignItems: 'center', justifyContent: 'center' }}>
                <div className="loading-spinner" />
            </div>
        );
    }

    if (!user) {
        return <LoginPage />;
    }

    return (
        <div className="app-layout">
            <Sidebar
                onLogout={logout}
                userEmail={user.email}
                isOpen={sidebarOpen}
                onClose={() => setSidebarOpen(false)}
            />
            {sidebarOpen && (
                <div className="sidebar-overlay" onClick={() => setSidebarOpen(false)} />
            )}
            <main className="main-content">
                <button
                    className="mobile-menu-btn"
                    onClick={() => setSidebarOpen(true)}
                    aria-label="Open menu"
                >
                    ☰
                </button>
                <Routes>
                    <Route path="/" element={<Dashboard />} />
                    <Route path="/accounts" element={<Accounts />} />
                    <Route path="/accounts/:id" element={<AccountRegister />} />
                    <Route path="/transactions" element={<Transactions />} />
                    <Route path="/data" element={<DataManagement />} />
                    <Route path="/snapshots" element={<Snapshots />} />
                    {/* Fallback route — redirect to dashboard for unknown routes (like /login when already logged in) */}
                    <Route path="*" element={<Navigate to="/" replace />} />
                </Routes>
            </main>
        </div>
    );
}

export default function App() {
    return (
        <BrowserRouter>
            <AuthProvider>
                <AppContent />
            </AuthProvider>
        </BrowserRouter>
    );
}
