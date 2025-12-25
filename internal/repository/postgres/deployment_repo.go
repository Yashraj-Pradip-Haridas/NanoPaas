package postgres

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/lib/pq"
	"go.uber.org/zap"

	"github.com/nanopaas/nanopaas/internal/domain"
)

// DeploymentRepository handles deployment persistence in PostgreSQL
type DeploymentRepository struct {
	pool   *pgxpool.Pool
	logger *zap.Logger
}

// NewDeploymentRepository creates a new deployment repository
func NewDeploymentRepository(pool *pgxpool.Pool, logger *zap.Logger) *DeploymentRepository {
	return &DeploymentRepository{
		pool:   pool,
		logger: logger,
	}
}

// Create creates a new deployment in the database
func (r *DeploymentRepository) Create(ctx context.Context, deployment *domain.Deployment) error {
	query := `
		INSERT INTO deployments (
			id, app_id, build_id, image_id, status,
			target_replicas, container_ids, created_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
	`

	_, err := r.pool.Exec(ctx, query,
		deployment.ID,
		deployment.AppID,
		deployment.BuildID,
		deployment.ImageID,
		string(deployment.Status),
		deployment.Replicas,
		pq.Array(deployment.ContainerIDs),
		deployment.CreatedAt,
	)

	if err != nil {
		r.logger.Error("Failed to create deployment",
			zap.String("deployment_id", deployment.ID.String()),
			zap.Error(err),
		)
		return err
	}

	return nil
}

// GetByID retrieves a deployment by ID
func (r *DeploymentRepository) GetByID(ctx context.Context, id uuid.UUID) (*domain.Deployment, error) {
	query := `
		SELECT id, app_id, build_id, image_id, status,
			   target_replicas, current_replicas, container_ids,
			   error_message, created_at, started_at, completed_at
		FROM deployments
		WHERE id = $1
	`

	deployment := &domain.Deployment{}
	var startedAt, completedAt *time.Time
	var containerIDs []string
	var targetReplicas, currentReplicas int

	err := r.pool.QueryRow(ctx, query, id).Scan(
		&deployment.ID,
		&deployment.AppID,
		&deployment.BuildID,
		&deployment.ImageID,
		&deployment.Status,
		&targetReplicas,
		&currentReplicas,
		pq.Array(&containerIDs),
		&deployment.ErrorMessage,
		&deployment.CreatedAt,
		&startedAt,
		&completedAt,
	)

	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, nil
		}
		r.logger.Error("Failed to get deployment", zap.Error(err))
		return nil, err
	}

	deployment.StartedAt = startedAt
	deployment.CompletedAt = completedAt
	deployment.ContainerIDs = containerIDs
	deployment.Replicas = targetReplicas

	return deployment, nil
}

// ListByApp retrieves all deployments for an app
func (r *DeploymentRepository) ListByApp(ctx context.Context, appID uuid.UUID, limit, offset int) ([]*domain.Deployment, error) {
	query := `
		SELECT id, app_id, build_id, image_id, status,
			   target_replicas, current_replicas, container_ids,
			   error_message, created_at, started_at, completed_at
		FROM deployments
		WHERE app_id = $1
		ORDER BY created_at DESC
		LIMIT $2 OFFSET $3
	`

	rows, err := r.pool.Query(ctx, query, appID, limit, offset)
	if err != nil {
		r.logger.Error("Failed to list deployments", zap.Error(err))
		return nil, err
	}
	defer rows.Close()

	var deployments []*domain.Deployment
	for rows.Next() {
		deployment := &domain.Deployment{}
		var startedAt, completedAt *time.Time
		var containerIDs []string
		var targetReplicas, currentReplicas int

		err := rows.Scan(
			&deployment.ID,
			&deployment.AppID,
			&deployment.BuildID,
			&deployment.ImageID,
			&deployment.Status,
			&targetReplicas,
			&currentReplicas,
			pq.Array(&containerIDs),
			&deployment.ErrorMessage,
			&deployment.CreatedAt,
			&startedAt,
			&completedAt,
		)
		if err != nil {
			r.logger.Error("Failed to scan deployment row", zap.Error(err))
			continue
		}

		deployment.StartedAt = startedAt
		deployment.CompletedAt = completedAt
		deployment.ContainerIDs = containerIDs
		deployment.Replicas = targetReplicas
		deployments = append(deployments, deployment)
	}

	return deployments, nil
}

// GetActive gets the currently active deployment for an app
func (r *DeploymentRepository) GetActive(ctx context.Context, appID uuid.UUID) (*domain.Deployment, error) {
	query := `
		SELECT id, app_id, build_id, image_id, status,
			   target_replicas, current_replicas, container_ids,
			   error_message, created_at, started_at, completed_at
		FROM deployments
		WHERE app_id = $1 AND status IN ('running', 'pending', 'deploying')
		ORDER BY created_at DESC
		LIMIT 1
	`

	deployment := &domain.Deployment{}
	var startedAt, completedAt *time.Time
	var containerIDs []string
	var targetReplicas, currentReplicas int

	err := r.pool.QueryRow(ctx, query, appID).Scan(
		&deployment.ID,
		&deployment.AppID,
		&deployment.BuildID,
		&deployment.ImageID,
		&deployment.Status,
		&targetReplicas,
		&currentReplicas,
		pq.Array(&containerIDs),
		&deployment.ErrorMessage,
		&deployment.CreatedAt,
		&startedAt,
		&completedAt,
	)

	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, nil
		}
		r.logger.Error("Failed to get active deployment", zap.Error(err))
		return nil, err
	}

	deployment.StartedAt = startedAt
	deployment.CompletedAt = completedAt
	deployment.ContainerIDs = containerIDs
	deployment.Replicas = targetReplicas

	return deployment, nil
}

// UpdateStatus updates the status of a deployment
func (r *DeploymentRepository) UpdateStatus(ctx context.Context, id uuid.UUID, status domain.DeploymentStatus) error {
	query := `UPDATE deployments SET status = $2 WHERE id = $1`
	_, err := r.pool.Exec(ctx, query, id, string(status))
	if err != nil {
		r.logger.Error("Failed to update deployment status", zap.Error(err))
	}
	return err
}

// SetStarted marks a deployment as started
func (r *DeploymentRepository) SetStarted(ctx context.Context, id uuid.UUID) error {
	query := `UPDATE deployments SET status = 'deploying', started_at = NOW() WHERE id = $1`
	_, err := r.pool.Exec(ctx, query, id)
	if err != nil {
		r.logger.Error("Failed to set deployment started", zap.Error(err))
	}
	return err
}

// SetCompleted marks a deployment as completed
func (r *DeploymentRepository) SetCompleted(ctx context.Context, id uuid.UUID, containerIDs []string) error {
	query := `
		UPDATE deployments 
		SET status = 'running', container_ids = $2, current_replicas = $3, completed_at = NOW()
		WHERE id = $1
	`
	_, err := r.pool.Exec(ctx, query, id, pq.Array(containerIDs), len(containerIDs))
	if err != nil {
		r.logger.Error("Failed to set deployment completed", zap.Error(err))
	}
	return err
}

// SetFailed marks a deployment as failed
func (r *DeploymentRepository) SetFailed(ctx context.Context, id uuid.UUID, errorMessage string) error {
	query := `
		UPDATE deployments 
		SET status = 'failed', error_message = $2, completed_at = NOW()
		WHERE id = $1
	`
	_, err := r.pool.Exec(ctx, query, id, errorMessage)
	if err != nil {
		r.logger.Error("Failed to set deployment failed", zap.Error(err))
	}
	return err
}

// SetStopped marks a deployment as stopped
func (r *DeploymentRepository) SetStopped(ctx context.Context, id uuid.UUID) error {
	query := `UPDATE deployments SET status = 'stopped', current_replicas = 0, completed_at = NOW() WHERE id = $1`
	_, err := r.pool.Exec(ctx, query, id)
	if err != nil {
		r.logger.Error("Failed to set deployment stopped", zap.Error(err))
	}
	return err
}

// CountByApp counts deployments for an app
func (r *DeploymentRepository) CountByApp(ctx context.Context, appID uuid.UUID) (int64, error) {
	var count int64
	err := r.pool.QueryRow(ctx, "SELECT COUNT(*) FROM deployments WHERE app_id = $1", appID).Scan(&count)
	if err != nil {
		r.logger.Error("Failed to count deployments", zap.Error(err))
		return 0, err
	}
	return count, nil
}

// Delete deletes a deployment
func (r *DeploymentRepository) Delete(ctx context.Context, id uuid.UUID) error {
	_, err := r.pool.Exec(ctx, "DELETE FROM deployments WHERE id = $1", id)
	if err != nil {
		r.logger.Error("Failed to delete deployment", zap.Error(err))
	}
	return err
}

// StopAllForApp stops all active deployments for an app
func (r *DeploymentRepository) StopAllForApp(ctx context.Context, appID uuid.UUID) error {
	query := `
		UPDATE deployments 
		SET status = 'stopped', current_replicas = 0, completed_at = NOW()
		WHERE app_id = $1 AND status IN ('running', 'pending', 'deploying')
	`
	_, err := r.pool.Exec(ctx, query, appID)
	if err != nil {
		r.logger.Error("Failed to stop all deployments for app", zap.Error(err))
	}
	return err
}
