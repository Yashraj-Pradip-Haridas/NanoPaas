-- NanoPaaS Migration: Builds and Deployments
-- Version: 003
-- Description: Add tables for builds and deployments tracking

-- ============================================================
-- BUILDS TABLE
-- ============================================================
CREATE TABLE IF NOT EXISTS builds (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    app_id UUID NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
    status VARCHAR(50) NOT NULL DEFAULT 'pending',
    source VARCHAR(50) NOT NULL, -- 'git', 'gzip', 'url', 'upload'
    source_url TEXT,
    git_ref VARCHAR(255),
    dockerfile_path VARCHAR(255) DEFAULT 'Dockerfile',
    image_tag VARCHAR(255),
    image_id VARCHAR(255),
    build_args JSONB DEFAULT '{}',
    error_message TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    started_at TIMESTAMPTZ,
    completed_at TIMESTAMPTZ,
    
    CONSTRAINT builds_status_check CHECK (status IN ('pending', 'running', 'success', 'failed', 'cancelled'))
);

-- Indexes for builds
CREATE INDEX IF NOT EXISTS idx_builds_app_id ON builds(app_id);
CREATE INDEX IF NOT EXISTS idx_builds_status ON builds(status);
CREATE INDEX IF NOT EXISTS idx_builds_created_at ON builds(created_at DESC);
CREATE INDEX IF NOT EXISTS idx_builds_app_status ON builds(app_id, status);

-- ============================================================
-- DEPLOYMENTS TABLE
-- ============================================================
CREATE TABLE IF NOT EXISTS deployments (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    app_id UUID NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
    build_id UUID REFERENCES builds(id) ON DELETE SET NULL,
    image_id VARCHAR(255) NOT NULL,
    status VARCHAR(50) NOT NULL DEFAULT 'pending',
    strategy VARCHAR(50) NOT NULL DEFAULT 'rolling', -- 'rolling', 'recreate', 'blue-green'
    target_replicas INTEGER NOT NULL DEFAULT 1,
    current_replicas INTEGER NOT NULL DEFAULT 0,
    container_ids TEXT[] DEFAULT '{}',
    environment JSONB DEFAULT '{}',
    error_message TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    started_at TIMESTAMPTZ,
    completed_at TIMESTAMPTZ,
    
    CONSTRAINT deployments_status_check CHECK (status IN ('pending', 'deploying', 'running', 'failed', 'stopped', 'rolled_back'))
);

-- Indexes for deployments
CREATE INDEX IF NOT EXISTS idx_deployments_app_id ON deployments(app_id);
CREATE INDEX IF NOT EXISTS idx_deployments_status ON deployments(status);
CREATE INDEX IF NOT EXISTS idx_deployments_created_at ON deployments(created_at DESC);
CREATE INDEX IF NOT EXISTS idx_deployments_app_status ON deployments(app_id, status);

-- ============================================================
-- BUILD LOGS TABLE (for storing build output)
-- ============================================================
CREATE TABLE IF NOT EXISTS build_logs (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    build_id UUID NOT NULL REFERENCES builds(id) ON DELETE CASCADE,
    line_number INTEGER NOT NULL,
    content TEXT NOT NULL,
    timestamp TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_build_logs_build_id ON build_logs(build_id);
CREATE INDEX IF NOT EXISTS idx_build_logs_build_line ON build_logs(build_id, line_number);

-- ============================================================
-- WEBHOOKS TABLE
-- ============================================================
CREATE TABLE IF NOT EXISTS webhooks (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    app_id UUID NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
    github_webhook_id BIGINT,
    repo_full_name VARCHAR(255) NOT NULL,
    events TEXT[] DEFAULT '{push}',
    secret VARCHAR(255),
    active BOOLEAN DEFAULT true,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_webhooks_app_id ON webhooks(app_id);
CREATE INDEX IF NOT EXISTS idx_webhooks_repo ON webhooks(repo_full_name);
CREATE UNIQUE INDEX IF NOT EXISTS idx_webhooks_app_repo ON webhooks(app_id, repo_full_name);

-- ============================================================
-- ADD MISSING COLUMNS TO APPS TABLE
-- ============================================================
ALTER TABLE apps ADD COLUMN IF NOT EXISTS git_repo_url TEXT;
ALTER TABLE apps ADD COLUMN IF NOT EXISTS git_branch VARCHAR(255) DEFAULT 'main';
ALTER TABLE apps ADD COLUMN IF NOT EXISTS auto_deploy BOOLEAN DEFAULT false;
ALTER TABLE apps ADD COLUMN IF NOT EXISTS current_build_id UUID REFERENCES builds(id) ON DELETE SET NULL;
ALTER TABLE apps ADD COLUMN IF NOT EXISTS current_deployment_id UUID REFERENCES deployments(id) ON DELETE SET NULL;

-- ============================================================
-- FUNCTIONS FOR UPDATED_AT TRIGGERS
-- ============================================================
CREATE OR REPLACE FUNCTION update_updated_at_column()
RETURNS TRIGGER AS $$
BEGIN
    NEW.updated_at = NOW();
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

-- Apply trigger to webhooks
DROP TRIGGER IF EXISTS update_webhooks_updated_at ON webhooks;
CREATE TRIGGER update_webhooks_updated_at
    BEFORE UPDATE ON webhooks
    FOR EACH ROW
    EXECUTE FUNCTION update_updated_at_column();
