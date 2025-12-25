-- NanoPaaS Database Schema
-- PostgreSQL Migration

-- Enable UUID extension
CREATE EXTENSION IF NOT EXISTS "uuid-ossp";

-- Apps table
CREATE TABLE IF NOT EXISTS apps (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    name VARCHAR(255) NOT NULL,
    slug VARCHAR(255) NOT NULL UNIQUE,
    description TEXT,
    status VARCHAR(50) NOT NULL DEFAULT 'created',
    env_vars JSONB DEFAULT '{}',
    labels JSONB DEFAULT '{}',
    current_image_id VARCHAR(255),
    previous_image_id VARCHAR(255),
    replicas INTEGER NOT NULL DEFAULT 0,
    target_replicas INTEGER NOT NULL DEFAULT 1,
    memory_limit BIGINT NOT NULL DEFAULT 536870912, -- 512MB
    cpu_quota BIGINT NOT NULL DEFAULT 50000,
    subdomain VARCHAR(255) NOT NULL,
    exposed_port INTEGER NOT NULL DEFAULT 8080,
    internal_port INTEGER,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    started_at TIMESTAMP WITH TIME ZONE,
    stopped_at TIMESTAMP WITH TIME ZONE,
    owner_id UUID NOT NULL,
    
    CONSTRAINT apps_status_check CHECK (status IN ('created', 'building', 'deploying', 'running', 'stopped', 'failed'))
);

-- Builds table
CREATE TABLE IF NOT EXISTS builds (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    app_id UUID NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
    status VARCHAR(50) NOT NULL DEFAULT 'queued',
    source VARCHAR(50) NOT NULL,
    source_path TEXT,
    source_url TEXT,
    git_ref VARCHAR(255),
    git_commit VARCHAR(64),
    dockerfile_path VARCHAR(255) DEFAULT 'Dockerfile',
    build_args JSONB DEFAULT '{}',
    image_tag VARCHAR(255),
    image_id VARCHAR(255),
    logs_key VARCHAR(255),
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    started_at TIMESTAMP WITH TIME ZONE,
    completed_at TIMESTAMP WITH TIME ZONE,
    error_message TEXT,
    trigger_type VARCHAR(50) DEFAULT 'manual',
    
    CONSTRAINT builds_status_check CHECK (status IN ('queued', 'running', 'succeeded', 'failed', 'cancelled')),
    CONSTRAINT builds_source_check CHECK (source IN ('gzip', 'git', 'url'))
);

-- Deployments table
CREATE TABLE IF NOT EXISTS deployments (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    app_id UUID NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
    build_id UUID REFERENCES builds(id),
    image_id VARCHAR(255) NOT NULL,
    status VARCHAR(50) NOT NULL DEFAULT 'pending',
    replicas INTEGER NOT NULL DEFAULT 1,
    container_ids JSONB DEFAULT '[]',
    previous_image_id VARCHAR(255),
    rollback_reason TEXT,
    rolled_back_from_id UUID,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    started_at TIMESTAMP WITH TIME ZONE,
    completed_at TIMESTAMP WITH TIME ZONE,
    error_message TEXT,
    retry_count INTEGER NOT NULL DEFAULT 0,
    
    CONSTRAINT deployments_status_check CHECK (status IN ('pending', 'running', 'succeeded', 'failed', 'rolled_back'))
);

-- Indexes
CREATE INDEX IF NOT EXISTS idx_apps_owner_id ON apps(owner_id);
CREATE INDEX IF NOT EXISTS idx_apps_status ON apps(status);
CREATE INDEX IF NOT EXISTS idx_apps_slug ON apps(slug);

CREATE INDEX IF NOT EXISTS idx_builds_app_id ON builds(app_id);
CREATE INDEX IF NOT EXISTS idx_builds_status ON builds(status);
CREATE INDEX IF NOT EXISTS idx_builds_created_at ON builds(created_at DESC);

CREATE INDEX IF NOT EXISTS idx_deployments_app_id ON deployments(app_id);
CREATE INDEX IF NOT EXISTS idx_deployments_status ON deployments(status);
CREATE INDEX IF NOT EXISTS idx_deployments_created_at ON deployments(created_at DESC);

-- Updated at trigger function
CREATE OR REPLACE FUNCTION update_updated_at_column()
RETURNS TRIGGER AS $$
BEGIN
    NEW.updated_at = NOW();
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

-- Apply trigger to apps table
DROP TRIGGER IF EXISTS apps_updated_at ON apps;
CREATE TRIGGER apps_updated_at
    BEFORE UPDATE ON apps
    FOR EACH ROW
    EXECUTE FUNCTION update_updated_at_column();

-- Comments
COMMENT ON TABLE apps IS 'Applications managed by NanoPaaS';
COMMENT ON TABLE builds IS 'Build history for applications';
COMMENT ON TABLE deployments IS 'Deployment history for applications';

COMMENT ON COLUMN apps.env_vars IS 'Environment variables as JSON object';
COMMENT ON COLUMN apps.memory_limit IS 'Memory limit in bytes';
COMMENT ON COLUMN apps.cpu_quota IS 'CPU quota in microseconds (100000 = 100% of 1 CPU)';
