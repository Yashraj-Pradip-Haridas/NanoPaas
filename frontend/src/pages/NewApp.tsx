import { useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { useQuery, useMutation } from '@tanstack/react-query'
import { api, type Repository } from '../api'
import { Github, Lock, ArrowRight, Search, RefreshCw } from 'lucide-react'

export default function NewApp() {
    const navigate = useNavigate()
    const [step, setStep] = useState<'select' | 'configure'>('select')
    const [selectedRepo, setSelectedRepo] = useState<Repository | null>(null)
    const [searchTerm, setSearchTerm] = useState('')
    const [appName, setAppName] = useState('')
    const [branch, setBranch] = useState('main')

    const { data: repos, isLoading, error, refetch } = useQuery({
        queryKey: ['github-repos'],
        queryFn: () => api.getRepos(),
    })

    const createAppMutation = useMutation({
        mutationFn: async () => {
            if (!selectedRepo) throw new Error('No repo selected')

            // Create app
            const app = await api.createApp({
                name: appName || selectedRepo.name,
                slug: (appName || selectedRepo.name).toLowerCase().replace(/[^a-z0-9-]/g, '-'),
                description: selectedRepo.description,
            })

            // Start build from Git
            await api.startBuildFromGit(app.id, selectedRepo.clone_url, app.slug, branch)

            return app
        },
        onSuccess: (app) => {
            navigate(`/apps/${app.id}`)
        },
    })

    const filteredRepos = repos?.filter(repo =>
        repo.name.toLowerCase().includes(searchTerm.toLowerCase()) ||
        repo.full_name.toLowerCase().includes(searchTerm.toLowerCase())
    )

    const handleSelectRepo = (repo: Repository) => {
        setSelectedRepo(repo)
        setAppName(repo.name)
        setBranch(repo.default_branch)
        setStep('configure')
    }

    if (step === 'configure' && selectedRepo) {
        return (
            <>
                <div className="page-header">
                    <h1 className="page-title">Configure App</h1>
                    <p className="page-description">
                        Deploying from <strong>{selectedRepo.full_name}</strong>
                    </p>
                </div>

                <div className="card" style={{ maxWidth: 600 }}>
                    <div className="card-body">
                        <div className="form-group">
                            <label className="form-label">App Name</label>
                            <input
                                type="text"
                                className="form-input"
                                value={appName}
                                onChange={(e) => setAppName(e.target.value)}
                                placeholder="my-awesome-app"
                            />
                            <p style={{ fontSize: 12, color: 'var(--text-secondary)', marginTop: 4 }}>
                                Your app will be available at {appName.toLowerCase().replace(/[^a-z0-9-]/g, '-')}.localhost
                            </p>
                        </div>

                        <div className="form-group">
                            <label className="form-label">Branch</label>
                            <input
                                type="text"
                                className="form-input"
                                value={branch}
                                onChange={(e) => setBranch(e.target.value)}
                                placeholder="main"
                            />
                        </div>

                        <div style={{ display: 'flex', gap: 12, marginTop: 24 }}>
                            <button
                                className="btn btn-secondary"
                                onClick={() => {
                                    setStep('select')
                                    setSelectedRepo(null)
                                }}
                            >
                                Back
                            </button>
                            <button
                                className="btn btn-primary"
                                onClick={() => createAppMutation.mutate()}
                                disabled={createAppMutation.isPending || !appName}
                            >
                                {createAppMutation.isPending ? (
                                    <>
                                        <RefreshCw size={16} className="spin" />
                                        Deploying...
                                    </>
                                ) : (
                                    <>
                                        Deploy
                                        <ArrowRight size={16} />
                                    </>
                                )}
                            </button>
                        </div>

                        {createAppMutation.error && (
                            <p style={{ color: 'var(--danger)', marginTop: 16, fontSize: 14 }}>
                                {(createAppMutation.error as Error).message}
                            </p>
                        )}
                    </div>
                </div>
            </>
        )
    }

    return (
        <>
            <div className="page-header">
                <h1 className="page-title">Create New App</h1>
                <p className="page-description">
                    Select a GitHub repository to deploy
                </p>
            </div>

            <div className="card">
                <div className="card-header">
                    <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
                        <Github size={18} />
                        <span className="card-title">Your Repositories</span>
                    </div>
                    <button className="btn btn-secondary" onClick={() => refetch()}>
                        <RefreshCw size={14} />
                        Refresh
                    </button>
                </div>

                <div style={{ padding: 16, borderBottom: '1px solid var(--border-color)' }}>
                    <div style={{ position: 'relative' }}>
                        <Search
                            size={18}
                            style={{
                                position: 'absolute',
                                left: 12,
                                top: '50%',
                                transform: 'translateY(-50%)',
                                color: 'var(--text-secondary)'
                            }}
                        />
                        <input
                            type="text"
                            className="form-input"
                            placeholder="Search repositories..."
                            value={searchTerm}
                            onChange={(e) => setSearchTerm(e.target.value)}
                            style={{ paddingLeft: 40 }}
                        />
                    </div>
                </div>

                <div className="card-body" style={{ padding: 0 }}>
                    {isLoading ? (
                        <div className="loading">
                            <div className="spinner" />
                        </div>
                    ) : error ? (
                        <div className="empty-state">
                            <p>Failed to load repositories. Make sure GitHub is connected.</p>
                        </div>
                    ) : filteredRepos && filteredRepos.length > 0 ? (
                        <div className="repo-list" style={{ padding: 8 }}>
                            {filteredRepos.map((repo) => (
                                <div
                                    key={repo.id}
                                    className="repo-item"
                                    onClick={() => handleSelectRepo(repo)}
                                    style={{ cursor: 'pointer' }}
                                >
                                    <div className="repo-info">
                                        <div className="repo-name">
                                            {repo.name}
                                            {repo.private && (
                                                <span className="repo-private">
                                                    <Lock size={10} /> Private
                                                </span>
                                            )}
                                        </div>
                                        {repo.description && (
                                            <div className="repo-description">{repo.description}</div>
                                        )}
                                        <div className="repo-meta">
                                            {repo.language && <span>{repo.language}</span>}
                                            <span>Updated {new Date(repo.updated_at).toLocaleDateString()}</span>
                                        </div>
                                    </div>
                                    <ArrowRight size={18} style={{ color: 'var(--text-secondary)' }} />
                                </div>
                            ))}
                        </div>
                    ) : (
                        <div className="empty-state">
                            <p>No repositories found</p>
                        </div>
                    )}
                </div>
            </div>
        </>
    )
}
