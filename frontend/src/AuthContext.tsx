import { createContext, useContext, useState, useEffect, type ReactNode } from 'react'
import { api, type User } from './api'

interface AuthContextType {
    user: User | null
    isLoading: boolean
    isAuthenticated: boolean
    login: () => void
    logout: () => void
    setTokens: (accessToken: string, refreshToken: string) => void
}

const AuthContext = createContext<AuthContextType | undefined>(undefined)

export function AuthProvider({ children }: { children: ReactNode }) {
    const [user, setUser] = useState<User | null>(null)
    const [isLoading, setIsLoading] = useState(true)

    useEffect(() => {
        const token = api.getToken()
        if (token) {
            loadUser()
        } else {
            setIsLoading(false)
        }
    }, [])

    const loadUser = async () => {
        try {
            const userData = await api.getCurrentUser()
            setUser(userData)
        } catch {
            api.setToken(null)
        } finally {
            setIsLoading(false)
        }
    }

    const login = () => {
        window.location.href = api.getAuthUrl()
    }

    const logout = () => {
        api.setToken(null)
        localStorage.removeItem('refresh_token')
        setUser(null)
        window.location.href = '/login'
    }

    const setTokens = (accessToken: string, refreshToken: string) => {
        api.setToken(accessToken)
        localStorage.setItem('refresh_token', refreshToken)
        loadUser()
    }

    return (
        <AuthContext.Provider
            value={{
                user,
                isLoading,
                isAuthenticated: !!user,
                login,
                logout,
                setTokens,
            }}
        >
            {children}
        </AuthContext.Provider>
    )
}

export function useAuth() {
    const context = useContext(AuthContext)
    if (context === undefined) {
        throw new Error('useAuth must be used within an AuthProvider')
    }
    return context
}
