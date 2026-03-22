import { BrowserRouter, Routes, Route } from 'react-router-dom';
import Sidebar from './components/Sidebar';
import Dashboard from './pages/Dashboard';
import Accounts from './pages/Accounts';
import AccountRegister from './pages/AccountRegister';
import Transactions from './pages/Transactions';
import Commodities from './pages/Commodities';
import LoginPage from './pages/LoginPage';
import { useGlobalShortcuts } from './hooks/useShortcuts';
import { initLocale } from './i18n';
import { AuthProvider, useAuth } from './auth/AuthContext';

// Initialize locale from localStorage before render
initLocale();

function AppContent() {
    useGlobalShortcuts();
    const { user, loading, logout } = useAuth();

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
            <Sidebar onLogout={logout} userEmail={user.email} />
            <main className="main-content">
                <Routes>
                    <Route path="/" element={<Dashboard />} />
                    <Route path="/accounts" element={<Accounts />} />
                    <Route path="/accounts/:id" element={<AccountRegister />} />
                    <Route path="/transactions" element={<Transactions />} />
                    <Route path="/commodities" element={<Commodities />} />
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
