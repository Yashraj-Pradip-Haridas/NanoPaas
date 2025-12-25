import { useState, useEffect, useRef } from 'react'
import { useParams, useNavigate } from 'react-router-dom'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { api, type App } from '../api'
import {
    Activity,
    Play,
    Square,
    RefreshCw,
    Trash2,
    ExternalLink,
    Settings,
    Terminal,
    Plus,
    Minus,
    Save,
    ArrowLeft
} from 'lucide-react'

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

function LogViewer({ logs }: { logs: string[] }) {
    const logRef = useRef<HTMLDivElement>(null)

    useEffect(() => {
        if (logRef.current) {
            logRef.current.scrollTop = logRef.current.scrollHeight
        }
    }, [logs])

    return (
        <div className="log-container" ref={logRef}>
            {logs.length === 0 ? (
                <div style={{ color: 'var(--text-secondary)', textAlign: 'center', padding: 24 }}>
                    No logs available
                </div>
            ) : (
                logs.map((log, i) => (
                    <div key={i} className="log-line">
                        <span className="log-message">{log}</span>
                    </div>
                ))
            )}
        </div>
    )
}

export default function AppDetails() {
    const { id } = useParams<{ id: string }>()
    const navigate = useNavigate()
    const queryClient = useQueryClient()
    const [activeTab, setActiveTab] = useState<'overview' | 'logs' | 'env' | 'settings'>('overview')
    const [envVars, setEnvVars] = useState<{ key: string; value: string }[]>([])
    const [logs, setLogs] = useState<string[]>([])

    const { data: app, isLoading, error } = useQuery({
        queryKey: ['app', id],
        queryFn: () => api.getApp(id!),
        enabled: !!id,
        refetchInterval: 5000,
    })

    useEffect(() => {
        if (app?.env_vars) {
            setEnvVars(Object.entries(app.env_vars).map(([key, value]) => ({ key, value })))
        }
    }, [app?.env_vars])

    // WebSocket connection for logs
    useEffect(() => {
        if (!id || activeTab !== 'logs') return

        const wsProtocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:'
        const wsUrl = `${wsProtocol}//localhost:8080/ws/apps/${id}/logs`

        let ws: WebSocket | null = null
        let reconnectTimer: number | null = null

        const connect = () => {
            ws = new WebSocket(wsUrl)

            ws.onopen = () => {
                console.log('WebSocket connected for logs')
            }

            ws.onmessage = (event) => {
                try {
                    const data = JSON.parse(event.data)
                    if (data.type === 'log' && data.data) {
                        setLogs(prev => [...prev.slice(-500), data.data])
                    }
                } catch {
                    // Plain text log
                    setLogs(prev => [...prev.slice(-500), event.data])
                }
            }

            ws.onerror = (err) => {
                console.error('WebSocket error:', err)
            }

            ws.onclose = () => {
                console.log('WebSocket closed, reconnecting in 3s...')
                reconnectTimer = window.setTimeout(connect, 3000)
            }
        }

        connect()

        return () => {
            if (ws) ws.close()
            if (reconnectTimer) clearTimeout(reconnectTimer)
        }
    }, [id, activeTab])

    const restartMutation = useMutation({
        mutationFn: () => api.restartApp(id!),
        onSuccess: () => queryClient.invalidateQueries({ queryKey: ['app', id] }),
    })

    // Start mutation: scale to 1 if no replicas, otherwise restart
    const startMutation = useMutation({
        mutationFn: async () => {
            if (app && app.replicas === 0) {
                return api.scaleApp(id!, 1)
            }
            return api.restartApp(id!)
        },
        onSuccess: () => queryClient.invalidateQueries({ queryKey: ['app', id] }),
    })

    const stopMutation = useMutation({
        mutationFn: () => api.stopApp(id!),
        onSuccess: () => queryClient.invalidateQueries({ queryKey: ['app', id] }),
    })

    const scaleMutation = useMutation({
        mutationFn: (replicas: number) => api.scaleApp(id!, replicas),
        onSuccess: () => queryClient.invalidateQueries({ queryKey: ['app', id] }),
    })

    const envMutation = useMutation({
        mutationFn: (vars: Record<string, string>) => api.setEnvVars(id!, vars),
        onSuccess: () => queryClient.invalidateQueries({ queryKey: ['app', id] }),
    })

    const deleteMutation = useMutation({
        mutationFn: () => api.deleteApp(id!),
        onSuccess: () => navigate('/'),
    })

    const handleSaveEnv = () => {
        const vars = envVars.reduce((acc, { key, value }) => {
            if (key.trim()) {
                acc[key.trim()] = value
            }
            return acc
        }, {} as Record<string, string>)
        envMutation.mutate(vars)
    }

    if (isLoading) {
        return (
            <div className="loading">
                <div className="spinner" />
            </div>
        )
    }

    if (error || !app) {
        return (
            <div className="empty-state">
                <div className="empty-state-title">App not found</div>
                <button className="btn btn-secondary" onClick={() => navigate('/')}>
                    <ArrowLeft size={16} />
                    Back to Dashboard
                </button>
            </div>
        )
    }

    return (
        <>
            <div style={{ marginBottom: 24 }}>
                <button
                    onClick={() => navigate('/')}
                    style={{
                        background: 'none',
                        border: 'none',
                        color: 'var(--text-secondary)',
                        cursor: 'pointer',
                        display: 'flex',
                        alignItems: 'center',
                        gap: 6,
                        marginBottom: 16,
                        padding: 0
                    }}
                >
                    <ArrowLeft size={16} />
                    Back to Dashboard
                </button>

                <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'flex-start' }}>
                    <div>
                        <div style={{ display: 'flex', alignItems: 'center', gap: 12 }}>
                            <h1 className="page-title">{app.name}</h1>
                            <StatusBadge status={app.status} />
                        </div>
                        {app.url && (
                            <a
                                href={app.url}
                                target="_blank"
                                rel="noopener noreferrer"
                                style={{
                                    display: 'inline-flex',
                                    alignItems: 'center',
                                    gap: 6,
                                    color: 'var(--info)',
                                    fontSize: 14,
                                    marginTop: 4
                                }}
                            >
                                <ExternalLink size={14} />
                                {app.url}
                            </a>
                        )}
                    </div>

                    <div style={{ display: 'flex', gap: 8 }}>
                        {app.status === 'running' ? (
                            <>
                                <button
                                    className="btn btn-secondary"
                                    onClick={() => restartMutation.mutate()}
                                    disabled={restartMutation.isPending}
                                >
                                    <RefreshCw size={16} />
                                    Restart
                                </button>
                                <button
                                    className="btn btn-secondary"
                                    onClick={() => stopMutation.mutate()}
                                    disabled={stopMutation.isPending}
                                >
                                    <Square size={16} />
                                    Stop
                                </button>
                            </>
                        ) : (
                            <button
                                className="btn btn-primary"
                                onClick={() => startMutation.mutate()}
                                disabled={startMutation.isPending}
                            >
                                <Play size={16} />
                                {app.replicas === 0 ? 'Start' : 'Restart'}
                            </button>
                        )}
                    </div>
                </div>
            </div>

            {/* Tabs */}
            <div style={{
                display: 'flex',
                gap: 4,
                borderBottom: '1px solid var(--border-color)',
                marginBottom: 24
            }}>
                {[
                    { id: 'overview', label: 'Overview', icon: Activity },
                    { id: 'logs', label: 'Logs', icon: Terminal },
                    { id: 'env', label: 'Environment', icon: Settings },
                    { id: 'settings', label: 'Settings', icon: Settings },
                ].map((tab) => (
                    <button
                        key={tab.id}
                        onClick={() => setActiveTab(tab.id as any)}
                        style={{
                            padding: '12px 16px',
                            background: 'none',
                            border: 'none',
                            color: activeTab === tab.id ? 'var(--info)' : 'var(--text-secondary)',
                            borderBottom: activeTab === tab.id ? '2px solid var(--info)' : '2px solid transparent',
                            display: 'flex',
                            alignItems: 'center',
                            gap: 8,
                            cursor: 'pointer',
                            marginBottom: -1
                        }}
                    >
                        <tab.icon size={16} />
                        {tab.label}
                    </button>
                ))}
            </div>

            {/* Tab Content */}
            {activeTab === 'overview' && (
                <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fit, minmax(250px, 1fr))', gap: 16 }}>
                    <div className="card">
                        <div className="card-header">
                            <span className="card-title">Replicas</span>
                        </div>
                        <div className="card-body">
                            <div style={{ display: 'flex', alignItems: 'center', gap: 16 }}>
                                <button
                                    className="btn btn-secondary"
                                    onClick={() => scaleMutation.mutate(Math.max(0, app.replicas - 1))}
                                    disabled={app.replicas <= 0 || scaleMutation.isPending}
                                >
                                    <Minus size={16} />
                                </button>
                                <span style={{ fontSize: 32, fontWeight: 700, minWidth: 60, textAlign: 'center' }}>
                                    {app.replicas}
                                </span>
                                <button
                                    className="btn btn-secondary"
                                    onClick={() => scaleMutation.mutate(app.replicas + 1)}
                                    disabled={app.replicas >= 10 || scaleMutation.isPending}
                                >
                                    <Plus size={16} />
                                </button>
                            </div>
                            <p style={{ color: 'var(--text-secondary)', fontSize: 13, marginTop: 8 }}>
                                Max 10 replicas
                            </p>
                        </div>
                    </div>

                    <div className="card">
                        <div className="card-header">
                            <span className="card-title">Resources</span>
                        </div>
                        <div className="card-body">
                            <div style={{ marginBottom: 12 }}>
                                <div style={{ color: 'var(--text-secondary)', fontSize: 12 }}>Memory Limit</div>
                                <div style={{ fontSize: 20, fontWeight: 600 }}>
                                    {Math.round(app.memory_limit / 1024 / 1024)} MB
                                </div>
                            </div>
                            <div>
                                <div style={{ color: 'var(--text-secondary)', fontSize: 12 }}>CPU Quota</div>
                                <div style={{ fontSize: 20, fontWeight: 600 }}>
                                    {(app.cpu_quota / 100000).toFixed(1)} cores
                                </div>
                            </div>
                        </div>
                    </div>

                    <div className="card">
                        <div className="card-header">
                            <span className="card-title">Configuration</span>
                        </div>
                        <div className="card-body">
                            <div style={{ marginBottom: 12 }}>
                                <div style={{ color: 'var(--text-secondary)', fontSize: 12 }}>Port</div>
                                <div style={{ fontSize: 16, fontWeight: 500 }}>{app.exposed_port}</div>
                            </div>
                            <div>
                                <div style={{ color: 'var(--text-secondary)', fontSize: 12 }}>Created</div>
                                <div style={{ fontSize: 14 }}>
                                    {new Date(app.created_at).toLocaleDateString()}
                                </div>
                            </div>
                        </div>
                    </div>
                </div>
            )}

            {activeTab === 'logs' && (
                <div className="card">
                    <div className="card-header">
                        <span className="card-title">Application Logs</span>
                        <button className="btn btn-secondary" onClick={() => setLogs([])}>
                            Clear
                        </button>
                    </div>
                    <div className="card-body" style={{ padding: 0 }}>
                        <LogViewer logs={logs} />
                    </div>
                </div>
            )}

            {activeTab === 'env' && (
                <div className="card">
                    <div className="card-header">
                        <span className="card-title">Environment Variables</span>
                        <button
                            className="btn btn-primary"
                            onClick={handleSaveEnv}
                            disabled={envMutation.isPending}
                        >
                            <Save size={14} />
                            Save
                        </button>
                    </div>
                    <div className="card-body">
                        {envVars.map((env, index) => (
                            <div key={index} style={{ display: 'flex', gap: 8, marginBottom: 8 }}>
                                <input
                                    type="text"
                                    className="form-input"
                                    placeholder="KEY"
                                    value={env.key}
                                    onChange={(e) => {
                                        const newVars = [...envVars]
                                        newVars[index].key = e.target.value
                                        setEnvVars(newVars)
                                    }}
                                    style={{ flex: 1 }}
                                />
                                <input
                                    type="text"
                                    className="form-input"
                                    placeholder="value"
                                    value={env.value}
                                    onChange={(e) => {
                                        const newVars = [...envVars]
                                        newVars[index].value = e.target.value
                                        setEnvVars(newVars)
                                    }}
                                    style={{ flex: 2 }}
                                />
                                <button
                                    className="btn btn-secondary"
                                    onClick={() => setEnvVars(envVars.filter((_, i) => i !== index))}
                                >
                                    <Trash2 size={14} />
                                </button>
                            </div>
                        ))}
                        <button
                            className="btn btn-secondary"
                            onClick={() => setEnvVars([...envVars, { key: '', value: '' }])}
                            style={{ marginTop: 8 }}
                        >
                            <Plus size={14} />
                            Add Variable
                        </button>
                    </div>
                </div>
            )}

            {activeTab === 'settings' && (
                <div className="card" style={{ borderColor: 'var(--danger)' }}>
                    <div className="card-header">
                        <span className="card-title" style={{ color: 'var(--danger)' }}>Danger Zone</span>
                    </div>
                    <div className="card-body">
                        <p style={{ marginBottom: 16, color: 'var(--text-secondary)' }}>
                            Once you delete an app, there is no going back. Please be certain.
                        </p>
                        <button
                            className="btn btn-danger"
                            onClick={() => {
                                if (confirm(`Are you sure you want to delete ${app.name}?`)) {
                                    deleteMutation.mutate()
                                }
                            }}
                            disabled={deleteMutation.isPending}
                        >
                            <Trash2 size={14} />
                            Delete App
                        </button>
                    </div>
                </div>
            )}
        </>
    )
}
