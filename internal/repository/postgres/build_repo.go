package postgres

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"

	"github.com/nanopaas/nanopaas/internal/domain"
)

// BuildRepository handles build persistence in PostgreSQL
type BuildRepository struct {
	pool   *pgxpool.Pool
	logger *zap.Logger
}

// NewBuildRepository creates a new build repository
func NewBuildRepository(pool *pgxpool.Pool, logger *zap.Logger) *BuildRepository {
	return &BuildRepository{
		pool:   pool,
		logger: logger,
	}
}

// Create creates a new build in the database
func (r *BuildRepository) Create(ctx context.Context, build *domain.Build) error {
	query := `
		INSERT INTO builds (
			id, app_id, status, source, source_url, git_ref,
			dockerfile_path, image_tag, build_args, created_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
	`

	_, err := r.pool.Exec(ctx, query,
		build.ID,
		build.AppID,
		string(build.Status),
		string(build.Source),
		build.SourceURL,
		build.GitRef,
		build.DockerfilePath,
		build.ImageTag,
		build.BuildArgs,
		build.CreatedAt,
	)

	if err != nil {
		r.logger.Error("Failed to create build",
			zap.String("build_id", build.ID.String()),
			zap.Error(err),
		)
		return err
	}

	return nil
}

// GetByID retrieves a build by ID
func (r *BuildRepository) GetByID(ctx context.Context, id uuid.UUID) (*domain.Build, error) {
	query := `
		SELECT id, app_id, status, source, source_url, git_ref,
			   dockerfile_path, image_tag, image_id, build_args,
			   error_message, created_at, started_at, completed_at
		FROM builds
		WHERE id = $1
	`

	build := &domain.Build{}
	var startedAt, completedAt *time.Time
	var buildArgs map[string]string

	err := r.pool.QueryRow(ctx, query, id).Scan(
		&build.ID,
		&build.AppID,
		&build.Status,
		&build.Source,
		&build.SourceURL,
		&build.GitRef,
		&build.DockerfilePath,
		&build.ImageTag,
		&build.ImageID,
		&buildArgs,
		&build.ErrorMessage,
		&build.CreatedAt,
		&startedAt,
		&completedAt,
	)

	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, nil
		}
		r.logger.Error("Failed to get build", zap.Error(err))
		return nil, err
	}

	build.StartedAt = startedAt
	build.CompletedAt = completedAt
	build.BuildArgs = buildArgs

	return build, nil
}

// ListByApp retrieves all builds for an app
func (r *BuildRepository) ListByApp(ctx context.Context, appID uuid.UUID, limit, offset int) ([]*domain.Build, error) {
	query := `
		SELECT id, app_id, status, source, source_url, git_ref,
			   dockerfile_path, image_tag, image_id, build_args,
			   error_message, created_at, started_at, completed_at
		FROM builds
		WHERE app_id = $1
		ORDER BY created_at DESC
		LIMIT $2 OFFSET $3
	`

	rows, err := r.pool.Query(ctx, query, appID, limit, offset)
	if err != nil {
		r.logger.Error("Failed to list builds", zap.Error(err))
		return nil, err
	}
	defer rows.Close()

	var builds []*domain.Build
	for rows.Next() {
		build := &domain.Build{}
		var startedAt, completedAt *time.Time
		var buildArgs map[string]string

		err := rows.Scan(
			&build.ID,
			&build.AppID,
			&build.Status,
			&build.Source,
			&build.SourceURL,
			&build.GitRef,
			&build.DockerfilePath,
			&build.ImageTag,
			&build.ImageID,
			&buildArgs,
			&build.ErrorMessage,
			&build.CreatedAt,
			&startedAt,
			&completedAt,
		)
		if err != nil {
			r.logger.Error("Failed to scan build row", zap.Error(err))
			continue
		}

		build.StartedAt = startedAt
		build.CompletedAt = completedAt
		build.BuildArgs = buildArgs
		builds = append(builds, build)
	}

	return builds, nil
}

// UpdateStatus updates the status of a build
func (r *BuildRepository) UpdateStatus(ctx context.Context, id uuid.UUID, status domain.BuildStatus) error {
	query := `UPDATE builds SET status = $2 WHERE id = $1`
	_, err := r.pool.Exec(ctx, query, id, string(status))
	if err != nil {
		r.logger.Error("Failed to update build status", zap.Error(err))
	}
	return err
}

// SetStarted marks a build as started
func (r *BuildRepository) SetStarted(ctx context.Context, id uuid.UUID) error {
	query := `UPDATE builds SET status = 'running', started_at = NOW() WHERE id = $1`
	_, err := r.pool.Exec(ctx, query, id)
	if err != nil {
		r.logger.Error("Failed to set build started", zap.Error(err))
	}
	return err
}

// SetCompleted marks a build as completed
func (r *BuildRepository) SetCompleted(ctx context.Context, id uuid.UUID, imageID string, imageTag string) error {
	query := `
		UPDATE builds 
		SET status = 'success', image_id = $2, image_tag = $3, completed_at = NOW()
		WHERE id = $1
	`
	_, err := r.pool.Exec(ctx, query, id, imageID, imageTag)
	if err != nil {
		r.logger.Error("Failed to set build completed", zap.Error(err))
	}
	return err
}

// SetFailed marks a build as failed
func (r *BuildRepository) SetFailed(ctx context.Context, id uuid.UUID, errorMessage string) error {
	query := `
		UPDATE builds 
		SET status = 'failed', error_message = $2, completed_at = NOW()
		WHERE id = $1
	`
	_, err := r.pool.Exec(ctx, query, id, errorMessage)
	if err != nil {
		r.logger.Error("Failed to set build failed", zap.Error(err))
	}
	return err
}

// GetLatestSuccessful gets the latest successful build for an app
func (r *BuildRepository) GetLatestSuccessful(ctx context.Context, appID uuid.UUID) (*domain.Build, error) {
	query := `
		SELECT id, app_id, status, source, source_url, git_ref,
			   dockerfile_path, image_tag, image_id, build_args,
			   error_message, created_at, started_at, completed_at
		FROM builds
		WHERE app_id = $1 AND status = 'success'
		ORDER BY completed_at DESC
		LIMIT 1
	`

	build := &domain.Build{}
	var startedAt, completedAt *time.Time
	var buildArgs map[string]string

	err := r.pool.QueryRow(ctx, query, appID).Scan(
		&build.ID,
		&build.AppID,
		&build.Status,
		&build.Source,
		&build.SourceURL,
		&build.GitRef,
		&build.DockerfilePath,
		&build.ImageTag,
		&build.ImageID,
		&buildArgs,
		&build.ErrorMessage,
		&build.CreatedAt,
		&startedAt,
		&completedAt,
	)

	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, nil
		}
		r.logger.Error("Failed to get latest successful build", zap.Error(err))
		return nil, err
	}

	build.StartedAt = startedAt
	build.CompletedAt = completedAt
	build.BuildArgs = buildArgs

	return build, nil
}

// CountByApp counts builds for an app
func (r *BuildRepository) CountByApp(ctx context.Context, appID uuid.UUID) (int64, error) {
	var count int64
	err := r.pool.QueryRow(ctx, "SELECT COUNT(*) FROM builds WHERE app_id = $1", appID).Scan(&count)
	if err != nil {
		r.logger.Error("Failed to count builds", zap.Error(err))
		return 0, err
	}
	return count, nil
}

// Delete deletes a build
func (r *BuildRepository) Delete(ctx context.Context, id uuid.UUID) error {
	_, err := r.pool.Exec(ctx, "DELETE FROM builds WHERE id = $1", id)
	if err != nil {
		r.logger.Error("Failed to delete build", zap.Error(err))
	}
	return err
}
