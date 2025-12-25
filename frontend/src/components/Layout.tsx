import { Link, Outlet, useLocation } from 'react-router-dom'
import { useAuth } from '../AuthContext'
import {
    LayoutDashboard,
    Box,
    Settings,
    PlusCircle,
    LogOut,
    Github
} from 'lucide-react'

export default function Layout() {
    const { user, logout } = useAuth()
    const location = useLocation()

    const navItems = [
        { path: '/', icon: LayoutDashboard, label: 'Dashboard' },
        { path: '/apps/new', icon: PlusCircle, label: 'New App' },
    ]

    return (
        <div className="app-layout">
            <aside className="sidebar">
                <div className="sidebar-header">
                    <Box size={24} />
                    <span className="sidebar-logo">NanoPaaS</span>
                </div>

                <nav className="sidebar-nav">
                    <div className="nav-section">
                        <div className="nav-section-title">Navigation</div>
                        {navItems.map((item) => (
                            <Link
                                key={item.path}
                                to={item.path}
                                className={`nav-link ${location.pathname === item.path ? 'active' : ''}`}
                            >
                                <item.icon size={18} />
                                {item.label}
                            </Link>
                        ))}
                    </div>

                    <div className="nav-section">
                        <div className="nav-section-title">Settings</div>
                        <Link to="/settings" className="nav-link">
                            <Settings size={18} />
                            Settings
                        </Link>
                    </div>
                </nav>

                <div className="sidebar-footer">
                    <div className="user-menu">
                        <div className="user-avatar">
                            {user?.avatar_url ? (
                                <img src={user.avatar_url} alt={user.name} />
                            ) : (
                                user?.name?.charAt(0).toUpperCase()
                            )}
                        </div>
                        <div style={{ flex: 1 }}>
                            <div style={{ fontWeight: 500, fontSize: 14 }}>{user?.name}</div>
                            <div style={{ fontSize: 12, color: 'var(--text-secondary)', display: 'flex', alignItems: 'center', gap: 4 }}>
                                <Github size={12} />
                                {user?.github_login}
                            </div>
                        </div>
                        <button
                            onClick={logout}
                            style={{
                                background: 'none',
                                border: 'none',
                                color: 'var(--text-secondary)',
                                padding: 8,
                                borderRadius: 6,
                                cursor: 'pointer'
                            }}
                            title="Logout"
                        >
                            <LogOut size={18} />
                        </button>
                    </div>
                </div>
            </aside>

            <main className="main-content">
                <Outlet />
            </main>
        </div>
    )
}
