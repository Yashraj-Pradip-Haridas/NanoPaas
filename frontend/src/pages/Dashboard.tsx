import { Link } from 'react-router-dom'
import { useQuery } from '@tanstack/react-query'
import { api, type App } from '../api'
import { Plus, Activity, Cpu, ExternalLink } from 'lucide-react'

function StatusBadge({ status }: { status: App['status'] }) {
    const statusClasses: Record<string, string> = {
        running: 'status-running',
        stopped: 'status-stopped',
        building: 'status-building',
        deploying: 'status-building',
        failed: 'status-failed',
        created: 'status-stopped',
    }

    return (
        <span className={`status-badge ${statusClasses[status] || ''}`}>
            <Activity size={12} />
            {status}
        </span>
    )
}

function AppCard({ app }: { app: App }) {
    return (
        <Link to={`/apps/${app.id}`} style={{ textDecoration: 'none' }}>
            <div className="app-card">
                <div className="app-card-header">
                    <div>
                        <div className="app-name">{app.name}</div>
                        {app.url && (
                            <div className="app-url">
                                <ExternalLink size={12} style={{ marginRight: 4 }} />
                                {app.url}
                            </div>
                        )}
                    </div>
                    <StatusBadge status={app.status} />
                </div>

                {app.description && (
                    <p style={{
                        fontSize: 13,
                        color: 'var(--text-secondary)',
                        marginBottom: 16,
                        lineHeight: 1.5
                    }}>
                        {app.description}
                    </p>
                )}

                <div className="app-stats">
                    <div className="stat">
                        <span className="stat-value">{app.replicas}</span>
                        <span className="stat-label">Replicas</span>
                    </div>
                    <div className="stat">
                        <span className="stat-value">{Math.round(app.memory_limit / 1024 / 1024)}MB</span>
                        <span className="stat-label">Memory</span>
                    </div>
                    <div className="stat">
                        <span className="stat-value">{app.exposed_port}</span>
                        <span className="stat-label">Port</span>
                    </div>
                </div>
            </div>
        </Link>
    )
}

export default function Dashboard() {
    const { data: apps, isLoading, error } = useQuery({
        queryKey: ['apps'],
        queryFn: () => api.getApps(),
    })

    if (isLoading) {
        return (
            <div className="loading">
                <div className="spinner" />
            </div>
        )
    }

    if (error) {
        return (
            <div className="empty-state">
                <div className="empty-state-icon">⚠️</div>
                <div className="empty-state-title">Failed to load apps</div>
                <p>{(error as Error).message}</p>
            </div>
        )
    }

    return (
        <>
            <div className="page-header" style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'flex-start' }}>
                <div>
                    <h1 className="page-title">Dashboard</h1>
                    <p className="page-description">Manage your deployed applications</p>
                </div>
                <Link to="/apps/new" className="btn btn-primary">
                    <Plus size={18} />
                    New App
                </Link>
            </div>

            {apps && apps.length > 0 ? (
                <div className="app-grid">
                    {apps.map((app) => (
                        <AppCard key={app.id} app={app} />
                    ))}
                </div>
            ) : (
                <div className="card">
                    <div className="empty-state">
                        <Cpu size={48} style={{ opacity: 0.3, marginBottom: 16 }} />
                        <div className="empty-state-title">No apps yet</div>
                        <p style={{ marginBottom: 24 }}>
                            Deploy your first app by connecting a GitHub repository.
                        </p>
                        <Link to="/apps/new" className="btn btn-primary">
                            <Plus size={18} />
                            Create your first app
                        </Link>
                    </div>
                </div>
            )}

            <div style={{ marginTop: 32 }}>
                <h2 style={{ fontSize: 18, marginBottom: 16 }}>Quick Stats</h2>
                <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fit, minmax(200px, 1fr))', gap: 16 }}>
                    <div className="card">
                        <div className="card-body" style={{ textAlign: 'center' }}>
                            <div style={{ fontSize: 32, fontWeight: 700, color: 'var(--info)' }}>
                                {apps?.length || 0}
                            </div>
                            <div style={{ color: 'var(--text-secondary)', fontSize: 14 }}>Total Apps</div>
                        </div>
                    </div>
                    <div className="card">
                        <div className="card-body" style={{ textAlign: 'center' }}>
                            <div style={{ fontSize: 32, fontWeight: 700, color: 'var(--accent)' }}>
                                {apps?.filter(a => a.status === 'running').length || 0}
                            </div>
                            <div style={{ color: 'var(--text-secondary)', fontSize: 14 }}>Running</div>
                        </div>
                    </div>
                    <div className="card">
                        <div className="card-body" style={{ textAlign: 'center' }}>
                            <div style={{ fontSize: 32, fontWeight: 700, color: 'var(--purple)' }}>
                                {apps?.reduce((sum, a) => sum + a.replicas, 0) || 0}
                            </div>
                            <div style={{ color: 'var(--text-secondary)', fontSize: 14 }}>Containers</div>
                        </div>
                    </div>
                </div>
            </div>
        </>
    )
}
