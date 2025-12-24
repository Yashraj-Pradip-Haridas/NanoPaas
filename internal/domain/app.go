package domain

import (
	"time"

	"github.com/google/uuid"
)

// AppStatus represents the current status of an application
type AppStatus string

const (
	AppStatusCreated   AppStatus = "created"
	AppStatusBuilding  AppStatus = "building"
	AppStatusDeploying AppStatus = "deploying"
	AppStatusRunning   AppStatus = "running"
	AppStatusStopped   AppStatus = "stopped"
	AppStatusFailed    AppStatus = "failed"
)

// App represents a deployed application
type App struct {
	ID          uuid.UUID         `json:"id"`
	Name        string            `json:"name"`
	Slug        string            `json:"slug"` // URL-safe name
	Description string            `json:"description,omitempty"`
	Status      AppStatus         `json:"status"`
	EnvVars     map[string]string `json:"env_vars,omitempty"`
	Labels      map[string]string `json:"labels,omitempty"`

	// Docker-related fields
	CurrentImageID  string `json:"current_image_id,omitempty"`
	PreviousImageID string `json:"previous_image_id,omitempty"`
	Replicas        int    `json:"replicas"`
	TargetReplicas  int    `json:"target_replicas"`

	// Resource limits
	MemoryLimit int64 `json:"memory_limit"` // in bytes
	CPUQuota    int64 `json:"cpu_quota"`    // in microseconds

	// Routing
	Subdomain    string `json:"subdomain"`
	ExposedPort  int    `json:"exposed_port"`
	InternalPort int    `json:"internal_port,omitempty"`

	// Timestamps
	CreatedAt time.Time  `json:"created_at"`
	UpdatedAt time.Time  `json:"updated_at"`
	StartedAt *time.Time `json:"started_at,omitempty"`
	StoppedAt *time.Time `json:"stopped_at,omitempty"`

	// Ownership
	OwnerID uuid.UUID `json:"owner_id"`
}

// NewApp creates a new application with defaults
func NewApp(name, slug string, ownerID uuid.UUID) *App {
	now := time.Now().UTC()
	return &App{
		ID:             uuid.New(),
		Name:           name,
		Slug:           slug,
		Status:         AppStatusCreated,
		EnvVars:        make(map[string]string),
		Labels:         make(map[string]string),
		Replicas:       0,
		TargetReplicas: 1,
		MemoryLimit:    512 * 1024 * 1024, // 512MB default
		CPUQuota:       50000,              // 50% of one CPU
		Subdomain:      slug,
		ExposedPort:    8080,
		CreatedAt:      now,
		UpdatedAt:      now,
		OwnerID:        ownerID,
	}
}

// SetEnvVar sets an environment variable
func (a *App) SetEnvVar(key, value string) {
	if a.EnvVars == nil {
		a.EnvVars = make(map[string]string)
	}
	a.EnvVars[key] = value
	a.UpdatedAt = time.Now().UTC()
}

// DeleteEnvVar removes an environment variable
func (a *App) DeleteEnvVar(key string) {
	delete(a.EnvVars, key)
	a.UpdatedAt = time.Now().UTC()
}

// GetEnvSlice returns environment variables as a slice for Docker
func (a *App) GetEnvSlice() []string {
	envs := make([]string, 0, len(a.EnvVars))
	for k, v := range a.EnvVars {
		envs = append(envs, k+"="+v)
	}
	return envs
}

// CanDeploy checks if the app is in a state that allows deployment
func (a *App) CanDeploy() bool {
	return a.Status == AppStatusCreated ||
		a.Status == AppStatusRunning ||
		a.Status == AppStatusStopped ||
		a.Status == AppStatusFailed
}

// CanScale checks if the app can be scaled
func (a *App) CanScale() bool {
	return a.Status == AppStatusRunning
}

// MarkBuilding updates status to building
func (a *App) MarkBuilding() {
	a.Status = AppStatusBuilding
	a.UpdatedAt = time.Now().UTC()
}

// MarkDeploying updates status to deploying
func (a *App) MarkDeploying() {
	a.Status = AppStatusDeploying
	a.UpdatedAt = time.Now().UTC()
}

// MarkRunning updates status to running
func (a *App) MarkRunning() {
	now := time.Now().UTC()
	a.Status = AppStatusRunning
	a.StartedAt = &now
	a.UpdatedAt = now
}

// MarkStopped updates status to stopped
func (a *App) MarkStopped() {
	now := time.Now().UTC()
	a.Status = AppStatusStopped
	a.StoppedAt = &now
	a.UpdatedAt = now
}

// MarkFailed updates status to failed
func (a *App) MarkFailed() {
	a.Status = AppStatusFailed
	a.UpdatedAt = time.Now().UTC()
}

// Rollback reverts to the previous image
func (a *App) Rollback() bool {
	if a.PreviousImageID == "" {
		return false
	}
	a.CurrentImageID, a.PreviousImageID = a.PreviousImageID, a.CurrentImageID
	a.UpdatedAt = time.Now().UTC()
	return true
}

// UpdateImage updates the current image and stores the previous one
func (a *App) UpdateImage(newImageID string) {
	a.PreviousImageID = a.CurrentImageID
	a.CurrentImageID = newImageID
	a.UpdatedAt = time.Now().UTC()
}

// GetContainerName returns the container name for a given replica
func (a *App) GetContainerName(replica int) string {
	if replica == 0 {
		return a.Slug
	}
	return a.Slug + "-" + string(rune('0'+replica))
}
