package domain

import (
	"time"

	"github.com/google/uuid"
)

// DeploymentStatus represents the status of a deployment
type DeploymentStatus string

const (
	DeploymentStatusPending   DeploymentStatus = "pending"
	DeploymentStatusRunning   DeploymentStatus = "running"
	DeploymentStatusSucceeded DeploymentStatus = "succeeded"
	DeploymentStatusFailed    DeploymentStatus = "failed"
	DeploymentStatusRolledBack DeploymentStatus = "rolled_back"
)

// Deployment represents a deployment attempt
type Deployment struct {
	ID           uuid.UUID        `json:"id"`
	AppID        uuid.UUID        `json:"app_id"`
	BuildID      uuid.UUID        `json:"build_id,omitempty"`
	ImageID      string           `json:"image_id"`
	Status       DeploymentStatus `json:"status"`
	Replicas     int              `json:"replicas"`
	ContainerIDs []string         `json:"container_ids,omitempty"`

	// Rollback info
	PreviousImageID    string `json:"previous_image_id,omitempty"`
	RollbackReason     string `json:"rollback_reason,omitempty"`
	RolledBackFromID   *uuid.UUID `json:"rolled_back_from_id,omitempty"`

	// Timestamps
	CreatedAt   time.Time  `json:"created_at"`
	StartedAt   *time.Time `json:"started_at,omitempty"`
	CompletedAt *time.Time `json:"completed_at,omitempty"`

	// Error tracking
	ErrorMessage string `json:"error_message,omitempty"`
	RetryCount   int    `json:"retry_count"`
}

// NewDeployment creates a new deployment
func NewDeployment(appID uuid.UUID, imageID string, replicas int) *Deployment {
	return &Deployment{
		ID:        uuid.New(),
		AppID:     appID,
		ImageID:   imageID,
		Status:    DeploymentStatusPending,
		Replicas:  replicas,
		CreatedAt: time.Now().UTC(),
	}
}

// Start marks the deployment as running
func (d *Deployment) Start() {
	now := time.Now().UTC()
	d.Status = DeploymentStatusRunning
	d.StartedAt = &now
}

// Succeed marks the deployment as succeeded
func (d *Deployment) Succeed(containerIDs []string) {
	now := time.Now().UTC()
	d.Status = DeploymentStatusSucceeded
	d.CompletedAt = &now
	d.ContainerIDs = containerIDs
}

// Fail marks the deployment as failed
func (d *Deployment) Fail(err error) {
	now := time.Now().UTC()
	d.Status = DeploymentStatusFailed
	d.CompletedAt = &now
	if err != nil {
		d.ErrorMessage = err.Error()
	}
}

// MarkRolledBack marks this deployment as rolled back
func (d *Deployment) MarkRolledBack(reason string) {
	now := time.Now().UTC()
	d.Status = DeploymentStatusRolledBack
	d.CompletedAt = &now
	d.RollbackReason = reason
}

// AddContainerID adds a container ID to the deployment
func (d *Deployment) AddContainerID(containerID string) {
	d.ContainerIDs = append(d.ContainerIDs, containerID)
}

// Duration returns the deployment duration
func (d *Deployment) Duration() time.Duration {
	if d.StartedAt == nil {
		return 0
	}
	end := time.Now().UTC()
	if d.CompletedAt != nil {
		end = *d.CompletedAt
	}
	return end.Sub(*d.StartedAt)
}

// CanRetry checks if the deployment can be retried
func (d *Deployment) CanRetry(maxRetries int) bool {
	return d.Status == DeploymentStatusFailed && d.RetryCount < maxRetries
}

// IncrementRetry increments the retry count
func (d *Deployment) IncrementRetry() {
	d.RetryCount++
	d.Status = DeploymentStatusPending
	d.ErrorMessage = ""
}
