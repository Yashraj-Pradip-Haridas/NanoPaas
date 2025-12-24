package domain

import (
	"time"

	"github.com/google/uuid"
)

// BuildStatus represents the status of a build
type BuildStatus string

const (
	BuildStatusQueued    BuildStatus = "queued"
	BuildStatusRunning   BuildStatus = "running"
	BuildStatusSucceeded BuildStatus = "succeeded"
	BuildStatusFailed    BuildStatus = "failed"
	BuildStatusCancelled BuildStatus = "cancelled"
)

// BuildSource represents the source type for a build
type BuildSource string

const (
	BuildSourceGzip BuildSource = "gzip"
	BuildSourceGit  BuildSource = "git"
	BuildSourceURL  BuildSource = "url"
)

// Build represents a build job
type Build struct {
	ID           uuid.UUID   `json:"id"`
	AppID        uuid.UUID   `json:"app_id"`
	Status       BuildStatus `json:"status"`
	Source       BuildSource `json:"source"`
	SourcePath   string      `json:"source_path,omitempty"`
	SourceURL    string      `json:"source_url,omitempty"`
	GitRef       string      `json:"git_ref,omitempty"`
	GitCommit    string      `json:"git_commit,omitempty"`

	// Docker build info
	DockerfilePath string            `json:"dockerfile_path"`
	BuildArgs      map[string]string `json:"build_args,omitempty"`
	ImageTag       string            `json:"image_tag,omitempty"`
	ImageID        string            `json:"image_id,omitempty"`

	// Build output
	LogsKey string `json:"logs_key,omitempty"` // Redis key for logs

	// Timestamps
	CreatedAt   time.Time  `json:"created_at"`
	StartedAt   *time.Time `json:"started_at,omitempty"`
	CompletedAt *time.Time `json:"completed_at,omitempty"`

	// Error tracking
	ErrorMessage string `json:"error_message,omitempty"`

	// Metadata
	TriggerType string `json:"trigger_type,omitempty"` // manual, webhook, etc.
}

// NewBuild creates a new build
func NewBuild(appID uuid.UUID, source BuildSource) *Build {
	return &Build{
		ID:             uuid.New(),
		AppID:          appID,
		Status:         BuildStatusQueued,
		Source:         source,
		DockerfilePath: "Dockerfile",
		BuildArgs:      make(map[string]string),
		CreatedAt:      time.Now().UTC(),
		TriggerType:    "manual",
	}
}

// Start marks the build as running
func (b *Build) Start() {
	now := time.Now().UTC()
	b.Status = BuildStatusRunning
	b.StartedAt = &now
}

// Succeed marks the build as succeeded
func (b *Build) Succeed(imageID, imageTag string) {
	now := time.Now().UTC()
	b.Status = BuildStatusSucceeded
	b.CompletedAt = &now
	b.ImageID = imageID
	b.ImageTag = imageTag
}

// Fail marks the build as failed
func (b *Build) Fail(err error) {
	now := time.Now().UTC()
	b.Status = BuildStatusFailed
	b.CompletedAt = &now
	if err != nil {
		b.ErrorMessage = err.Error()
	}
}

// Cancel marks the build as cancelled
func (b *Build) Cancel() {
	now := time.Now().UTC()
	b.Status = BuildStatusCancelled
	b.CompletedAt = &now
}

// Duration returns the build duration
func (b *Build) Duration() time.Duration {
	if b.StartedAt == nil {
		return 0
	}
	end := time.Now().UTC()
	if b.CompletedAt != nil {
		end = *b.CompletedAt
	}
	return end.Sub(*b.StartedAt)
}

// IsComplete checks if the build is in a terminal state
func (b *Build) IsComplete() bool {
	return b.Status == BuildStatusSucceeded ||
		b.Status == BuildStatusFailed ||
		b.Status == BuildStatusCancelled
}

// GetLogsKey returns the Redis key for build logs
func (b *Build) GetLogsKey() string {
	if b.LogsKey != "" {
		return b.LogsKey
	}
	return "build:logs:" + b.ID.String()
}

// SetLogsKey sets the Redis key for build logs
func (b *Build) SetLogsKey(key string) {
	b.LogsKey = key
}

// GenerateImageTag generates the Docker image tag for this build
func (b *Build) GenerateImageTag(appSlug string) string {
	return "nanopaas/" + appSlug + ":" + b.ID.String()[:8]
}
