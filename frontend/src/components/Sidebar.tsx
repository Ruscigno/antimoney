import { NavLink } from 'react-router-dom';
import { t } from '../i18n';

interface SidebarProps {
    onLogout?: () => void;
    userEmail?: string;
    isOpen?: boolean;
    onClose?: () => void;
}

export default function Sidebar({ onLogout, userEmail, isOpen = false, onClose }: SidebarProps) {
    return (
        <aside className="sidebar" data-open={isOpen}>
            <div className="sidebar-logo">
                <img src="/favicon.svg" alt="Antimoney logo" className="sidebar-logo-icon" />
                Antimoney
                <button className="sidebar-close-btn" onClick={onClose} aria-label="Close menu">✕</button>
            </div>
            <nav className="sidebar-nav">
                <NavLink to="/" end className={({ isActive }) => `nav-item ${isActive ? 'active' : ''}`} onClick={onClose}>
                    <span className="nav-item-icon">📊</span>
                    {t('nav.dashboard')}
                    <kbd className="kbd-nav">⌥1</kbd>
                </NavLink>
                <NavLink to="/accounts" className={({ isActive }) => `nav-item ${isActive ? 'active' : ''}`} onClick={onClose}>
                    <span className="nav-item-icon">📂</span>
                    {t('nav.accounts')}
                    <kbd className="kbd-nav">⌥2</kbd>
                </NavLink>
                <NavLink to="/transactions" className={({ isActive }) => `nav-item ${isActive ? 'active' : ''}`} onClick={onClose}>
                    <span className="nav-item-icon">💰</span>
                    {t('nav.transactions')}
                    <kbd className="kbd-nav">⌥3</kbd>
                </NavLink>
                <NavLink to="/data" className={({ isActive }) => `nav-item ${isActive ? 'active' : ''}`} onClick={onClose}>
                    <span className="nav-item-icon">💾</span>
                    {t('nav.data')}
                    <kbd className="kbd-nav">⌥4</kbd>
                </NavLink>
                <NavLink to="/snapshots" className={({ isActive }) => `nav-item ${isActive ? 'active' : ''}`} onClick={onClose}>
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
