import { useAuth } from '../AuthContext'
import { Github, Box } from 'lucide-react'

export default function Login() {
    const { login, isLoading } = useAuth()

    if (isLoading) {
        return (
            <div className="loading">
                <div className="spinner" />
            </div>
        )
    }

    return (
        <div className="login-page">
            <div className="login-box">
                <div style={{ marginBottom: 24, display: 'flex', justifyContent: 'center' }}>
                    <Box size={48} color="var(--info)" />
                </div>

                <h1 className="login-logo">NanoPaaS</h1>
                <h2 className="login-title">Welcome back</h2>
                <p className="login-subtitle">
                    Deploy your apps with ease. Connect your GitHub account to get started.
                </p>

                <button className="btn btn-github btn-lg" onClick={login} style={{ width: '100%' }}>
                    <Github size={20} />
                    Continue with GitHub
                </button>

                <p style={{ marginTop: 24, fontSize: 13, color: 'var(--text-secondary)' }}>
                    By signing in, you agree to our Terms of Service and Privacy Policy.
                </p>
            </div>
        </div>
    )
}
