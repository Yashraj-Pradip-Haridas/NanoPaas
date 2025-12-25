const API_BASE = import.meta.env.VITE_API_URL || 'http://localhost:8080'

interface RequestOptions {
    method?: string
    body?: unknown
    headers?: Record<string, string>
}

class ApiClient {
    private baseUrl: string
    private token: string | null = null

    constructor(baseUrl: string) {
        this.baseUrl = baseUrl
        this.token = localStorage.getItem('access_token')
    }

    setToken(token: string | null) {
        this.token = token
        if (token) {
            localStorage.setItem('access_token', token)
        } else {
            localStorage.removeItem('access_token')
        }
    }

    getToken() {
        return this.token
    }

    async request<T>(endpoint: string, options: RequestOptions = {}): Promise<T> {
        const headers: Record<string, string> = {
            'Content-Type': 'application/json',
            ...options.headers,
        }

        if (this.token) {
            headers['Authorization'] = `Bearer ${this.token}`
        }

        const response = await fetch(`${this.baseUrl}${endpoint}`, {
            method: options.method || 'GET',
            headers,
            body: options.body ? JSON.stringify(options.body) : undefined,
        })

        if (response.status === 401) {
            this.setToken(null)
            window.location.href = '/login'
            throw new Error('Unauthorized')
        }

        if (!response.ok) {
            const error = await response.json().catch(() => ({ error: 'Request failed' }))
            throw new Error(error.error || 'Request failed')
        }

        return response.json()
    }

    // Auth
    getAuthUrl() {
        return `${this.baseUrl}/api/v1/auth/github`
    }

    async getCurrentUser() {
        return this.request<User>('/api/v1/auth/me')
    }

    async refreshToken(refreshToken: string) {
        return this.request<TokenPair>('/api/v1/auth/refresh', {
            method: 'POST',
            body: { refresh_token: refreshToken },
        })
    }

    // Apps
    async getApps() {
        return this.request<App[]>('/api/v1/apps')
    }

    async getApp(id: string) {
        return this.request<App>(`/api/v1/apps/${id}`)
    }

    async createApp(data: CreateAppRequest) {
        return this.request<App>('/api/v1/apps', {
            method: 'POST',
            body: data,
        })
    }

    async updateApp(id: string, data: Partial<CreateAppRequest>) {
        return this.request<App>(`/api/v1/apps/${id}`, {
            method: 'PUT',
            body: data,
        })
    }

    async deleteApp(id: string) {
        return this.request<void>(`/api/v1/apps/${id}`, {
            method: 'DELETE',
        })
    }

    async deployApp(id: string, imageId: string, replicas?: number) {
        return this.request<DeployResponse>(`/api/v1/apps/${id}/deploy`, {
            method: 'POST',
            body: { image_id: imageId, replicas },
        })
    }

    async scaleApp(id: string, replicas: number) {
        return this.request<void>(`/api/v1/apps/${id}/scale`, {
            method: 'POST',
            body: { replicas },
        })
    }

    async restartApp(id: string) {
        return this.request<void>(`/api/v1/apps/${id}/restart`, {
            method: 'POST',
        })
    }

    async stopApp(id: string) {
        return this.request<void>(`/api/v1/apps/${id}/stop`, {
            method: 'POST',
        })
    }

    async setEnvVars(id: string, envVars: Record<string, string>) {
        return this.request<void>(`/api/v1/apps/${id}/env`, {
            method: 'POST',
            body: envVars,
        })
    }

    // Builds
    async createBuild(appId: string, sourceType: string) {
        return this.request<Build>(`/api/v1/apps/${appId}/builds`, {
            method: 'POST',
            body: { source_type: sourceType },
        })
    }

    async getBuild(buildId: string) {
        return this.request<Build>(`/api/v1/builds/${buildId}`)
    }

    async startBuildFromGit(appId: string, repoUrl: string, branch?: string) {
        return this.request<Build>(`/api/v1/apps/${appId}/builds/git`, {
            method: 'POST',
            body: { repo_url: repoUrl, branch: branch || 'main' },
        })
    }

    // GitHub
    async getRepos() {
        return this.request<Repository[]>('/api/v1/github/repos')
    }

    async getRepo(owner: string, repo: string) {
        return this.request<Repository>(`/api/v1/github/repos/${owner}/${repo}`)
    }

    // Health
    async getHealth() {
        return this.request<{ status: string }>('/health')
    }

    // Containers
    async getContainers() {
        return this.request<Container[]>('/api/v1/containers')
    }
}

export const api = new ApiClient(API_BASE)

// Types
export interface User {
    id: string
    email: string
    name: string
    avatar_url?: string
    github_login?: string
    role: string
    created_at: string
}

export interface TokenPair {
    access_token: string
    refresh_token: string
    expires_at: string
    token_type: string
}

export interface App {
    id: string
    name: string
    slug: string
    description?: string
    status: 'created' | 'building' | 'deploying' | 'running' | 'stopped' | 'failed'
    url?: string
    replicas: number
    target_replicas: number
    current_image_id?: string
    env_vars?: Record<string, string>
    exposed_port: number
    memory_limit: number
    cpu_quota: number
    created_at: string
    updated_at: string
}

export interface CreateAppRequest {
    name: string
    slug?: string
    description?: string
    env_vars?: Record<string, string>
    exposed_port?: number
}

export interface DeployResponse {
    message: string
    deployment_id: string
    status: string
    url: string
}

export interface Build {
    id: string
    app_id: string
    status: 'queued' | 'running' | 'succeeded' | 'failed' | 'cancelled'
    source: string
    source_url?: string
    image_tag?: string
    created_at: string
    started_at?: string
    completed_at?: string
    error_message?: string
}

export interface Repository {
    id: number
    name: string
    full_name: string
    description?: string
    private: boolean
    html_url: string
    clone_url: string
    default_branch: string
    language?: string
    updated_at: string
}

export interface Container {
    id: string
    name: string
    image: string
    status: string
    state: string
    ports: Record<string, string>
    created_at: string
    ip_address: string
}
