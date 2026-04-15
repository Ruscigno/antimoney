import { NavLink } from 'react-router-dom';
import { t } from '../i18n';

interface SidebarProps {
    onLogout?: () => void;
    userEmail?: string;
}

export default function Sidebar({ onLogout, userEmail }: SidebarProps) {
    return (
        <aside className="sidebar">
            <div className="sidebar-logo">
                <img src="/favicon.svg" alt="Antimoney logo" className="sidebar-logo-icon" />
                Antimoney
            </div>
            <nav className="sidebar-nav">
                <NavLink to="/" end className={({ isActive }) => `nav-item ${isActive ? 'active' : ''}`}>
                    <span className="nav-item-icon">📊</span>
                    {t('nav.dashboard')}
                    <kbd className="kbd-nav">⌥1</kbd>
                </NavLink>
                <NavLink to="/accounts" className={({ isActive }) => `nav-item ${isActive ? 'active' : ''}`}>
                    <span className="nav-item-icon">📂</span>
                    {t('nav.accounts')}
                    <kbd className="kbd-nav">⌥2</kbd>
                </NavLink>
                <NavLink to="/transactions" className={({ isActive }) => `nav-item ${isActive ? 'active' : ''}`}>
                    <span className="nav-item-icon">💰</span>
                    {t('nav.transactions')}
                    <kbd className="kbd-nav">⌥3</kbd>
                </NavLink>
                <NavLink to="/data" className={({ isActive }) => `nav-item ${isActive ? 'active' : ''}`}>
                    <span className="nav-item-icon">💾</span>
                    {t('nav.data')}
                    <kbd className="kbd-nav">⌥4</kbd>
                </NavLink>
                <NavLink to="/snapshots" className={({ isActive }) => `nav-item ${isActive ? 'active' : ''}`}>
                    <span className="nav-item-icon">📸</span>
                    {t('nav.snapshots')}
                </NavLink>
            </nav>

            {userEmail && (
                <div className="sidebar-user">
                    <div className="sidebar-user-email" title={userEmail}>
                        👤 {userEmail}
                    </div>
                    {onLogout && (
                        <button className="sidebar-logout-btn" onClick={onLogout} title="Sign out">
                            ↗ Sign out
                        </button>
                    )}
                </div>
            )}
        </aside>
    );
}
