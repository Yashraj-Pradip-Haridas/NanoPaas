import { useEffect } from 'react'
import { useNavigate, useSearchParams } from 'react-router-dom'
import { useAuth } from '../AuthContext'

export default function AuthCallback() {
    const [searchParams] = useSearchParams()
    const { setTokens } = useAuth()
    const navigate = useNavigate()

    useEffect(() => {
        const accessToken = searchParams.get('access_token')
        const refreshToken = searchParams.get('refresh_token')
        const error = searchParams.get('error')

        if (error) {
            console.error('Auth error:', error)
            navigate('/login?error=' + error)
            return
        }

        if (accessToken && refreshToken) {
            setTokens(accessToken, refreshToken)
            navigate('/')
        } else {
            navigate('/login')
        }
    }, [searchParams, setTokens, navigate])

    return (
        <div className="loading">
            <div className="spinner" />
        </div>
    )
}
