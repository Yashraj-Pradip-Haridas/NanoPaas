package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"

	"github.com/nanopaas/nanopaas/internal/domain"
)

// AppRepository handles app persistence in PostgreSQL
type AppRepository struct {
	pool   *pgxpool.Pool
	logger *zap.Logger
}

// NewAppRepository creates a new app repository
func NewAppRepository(pool *pgxpool.Pool, logger *zap.Logger) *AppRepository {
	return &AppRepository{
		pool:   pool,
		logger: logger,
	}
}

// Create creates a new app in the database
func (r *AppRepository) Create(ctx context.Context, app *domain.App) error {
	query := `
		INSERT INTO apps (
			id, name, slug, description, status, env_vars, labels,
			current_image_id, previous_image_id, replicas, target_replicas,
			memory_limit, cpu_quota, subdomain, exposed_port, internal_port,
			created_at, updated_at, owner_id
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19
		)
	`

	_, err := r.pool.Exec(ctx, query,
		app.ID,
		app.Name,
		app.Slug,
		app.Description,
		string(app.Status),
		app.EnvVars,
		app.Labels,
		app.CurrentImageID,
		app.PreviousImageID,
		app.Replicas,
		app.TargetReplicas,
		app.MemoryLimit,
		app.CPUQuota,
		app.Subdomain,
		app.ExposedPort,
		app.InternalPort,
		app.CreatedAt,
		app.UpdatedAt,
		app.OwnerID,
	)

	if err != nil {
		return fmt.Errorf("failed to create app: %w", err)
	}

	r.logger.Debug("App created", zap.String("app_id", app.ID.String()))
	return nil
}

// GetByID retrieves an app by ID
func (r *AppRepository) GetByID(ctx context.Context, id uuid.UUID) (*domain.App, error) {
	query := `
		SELECT id, name, slug, description, status, env_vars, labels,
			current_image_id, previous_image_id, replicas, target_replicas,
			memory_limit, cpu_quota, subdomain, exposed_port, internal_port,
			created_at, updated_at, started_at, stopped_at, owner_id
		FROM apps
		WHERE id = $1
	`

	app := &domain.App{}
	var status string
	var startedAt, stoppedAt *time.Time

	err := r.pool.QueryRow(ctx, query, id).Scan(
		&app.ID,
		&app.Name,
		&app.Slug,
		&app.Description,
		&status,
		&app.EnvVars,
		&app.Labels,
		&app.CurrentImageID,
		&app.PreviousImageID,
		&app.Replicas,
		&app.TargetReplicas,
		&app.MemoryLimit,
		&app.CPUQuota,
		&app.Subdomain,
		&app.ExposedPort,
		&app.InternalPort,
		&app.CreatedAt,
		&app.UpdatedAt,
		&startedAt,
		&stoppedAt,
		&app.OwnerID,
	)

	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, fmt.Errorf("app not found")
		}
		return nil, fmt.Errorf("failed to get app: %w", err)
	}

	app.Status = domain.AppStatus(status)
	app.StartedAt = startedAt
	app.StoppedAt = stoppedAt

	return app, nil
}

// GetBySlug retrieves an app by slug
func (r *AppRepository) GetBySlug(ctx context.Context, slug string) (*domain.App, error) {
	query := `
		SELECT id, name, slug, description, status, env_vars, labels,
			current_image_id, previous_image_id, replicas, target_replicas,
			memory_limit, cpu_quota, subdomain, exposed_port, internal_port,
			created_at, updated_at, started_at, stopped_at, owner_id
		FROM apps
		WHERE slug = $1
	`

	app := &domain.App{}
	var status string
	var startedAt, stoppedAt *time.Time

	err := r.pool.QueryRow(ctx, query, slug).Scan(
		&app.ID,
		&app.Name,
		&app.Slug,
		&app.Description,
		&status,
		&app.EnvVars,
		&app.Labels,
		&app.CurrentImageID,
		&app.PreviousImageID,
		&app.Replicas,
		&app.TargetReplicas,
		&app.MemoryLimit,
		&app.CPUQuota,
		&app.Subdomain,
		&app.ExposedPort,
		&app.InternalPort,
		&app.CreatedAt,
		&app.UpdatedAt,
		&startedAt,
		&stoppedAt,
		&app.OwnerID,
	)

	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, fmt.Errorf("app not found")
		}
		return nil, fmt.Errorf("failed to get app: %w", err)
	}

	app.Status = domain.AppStatus(status)
	app.StartedAt = startedAt
	app.StoppedAt = stoppedAt

	return app, nil
}

// List retrieves all apps for an owner
func (r *AppRepository) List(ctx context.Context, ownerID uuid.UUID, limit, offset int) ([]*domain.App, error) {
	query := `
		SELECT id, name, slug, description, status, env_vars, labels,
			current_image_id, previous_image_id, replicas, target_replicas,
			memory_limit, cpu_quota, subdomain, exposed_port, internal_port,
			created_at, updated_at, started_at, stopped_at, owner_id
		FROM apps
		WHERE owner_id = $1
		ORDER BY created_at DESC
		LIMIT $2 OFFSET $3
	`

	rows, err := r.pool.Query(ctx, query, ownerID, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("failed to list apps: %w", err)
	}
	defer rows.Close()

	var apps []*domain.App
	for rows.Next() {
		app := &domain.App{}
		var status string
		var startedAt, stoppedAt *time.Time

		err := rows.Scan(
			&app.ID,
			&app.Name,
			&app.Slug,
			&app.Description,
			&status,
			&app.EnvVars,
			&app.Labels,
			&app.CurrentImageID,
			&app.PreviousImageID,
			&app.Replicas,
			&app.TargetReplicas,
			&app.MemoryLimit,
			&app.CPUQuota,
			&app.Subdomain,
			&app.ExposedPort,
			&app.InternalPort,
			&app.CreatedAt,
			&app.UpdatedAt,
			&startedAt,
			&stoppedAt,
			&app.OwnerID,
		)

		if err != nil {
			return nil, fmt.Errorf("failed to scan app: %w", err)
		}

		app.Status = domain.AppStatus(status)
		app.StartedAt = startedAt
		app.StoppedAt = stoppedAt

		apps = append(apps, app)
	}

	return apps, nil
}

// Update updates an app
func (r *AppRepository) Update(ctx context.Context, app *domain.App) error {
	query := `
		UPDATE apps SET
			name = $2,
			description = $3,
			status = $4,
			env_vars = $5,
			labels = $6,
			current_image_id = $7,
			previous_image_id = $8,
			replicas = $9,
			target_replicas = $10,
			memory_limit = $11,
			cpu_quota = $12,
			subdomain = $13,
			exposed_port = $14,
			internal_port = $15,
			updated_at = $16,
			started_at = $17,
			stopped_at = $18
		WHERE id = $1
	`

	result, err := r.pool.Exec(ctx, query,
		app.ID,
		app.Name,
		app.Description,
		string(app.Status),
		app.EnvVars,
		app.Labels,
		app.CurrentImageID,
		app.PreviousImageID,
		app.Replicas,
		app.TargetReplicas,
		app.MemoryLimit,
		app.CPUQuota,
		app.Subdomain,
		app.ExposedPort,
		app.InternalPort,
		app.UpdatedAt,
		app.StartedAt,
		app.StoppedAt,
	)

	if err != nil {
		return fmt.Errorf("failed to update app: %w", err)
	}

	if result.RowsAffected() == 0 {
		return fmt.Errorf("app not found")
	}

	r.logger.Debug("App updated", zap.String("app_id", app.ID.String()))
	return nil
}

// Delete deletes an app
func (r *AppRepository) Delete(ctx context.Context, id uuid.UUID) error {
	query := `DELETE FROM apps WHERE id = $1`

	result, err := r.pool.Exec(ctx, query, id)
	if err != nil {
		return fmt.Errorf("failed to delete app: %w", err)
	}

	if result.RowsAffected() == 0 {
		return fmt.Errorf("app not found")
	}

	r.logger.Debug("App deleted", zap.String("app_id", id.String()))
	return nil
}

// UpdateStatus updates only the app status
func (r *AppRepository) UpdateStatus(ctx context.Context, id uuid.UUID, status domain.AppStatus) error {
	query := `UPDATE apps SET status = $2, updated_at = $3 WHERE id = $1`

	result, err := r.pool.Exec(ctx, query, id, string(status), time.Now().UTC())
	if err != nil {
		return fmt.Errorf("failed to update status: %w", err)
	}

	if result.RowsAffected() == 0 {
		return fmt.Errorf("app not found")
	}

	return nil
}

// UpdateEnvVars updates only the environment variables
func (r *AppRepository) UpdateEnvVars(ctx context.Context, id uuid.UUID, envVars map[string]string) error {
	query := `UPDATE apps SET env_vars = $2, updated_at = $3 WHERE id = $1`

	result, err := r.pool.Exec(ctx, query, id, envVars, time.Now().UTC())
	if err != nil {
		return fmt.Errorf("failed to update env vars: %w", err)
	}

	if result.RowsAffected() == 0 {
		return fmt.Errorf("app not found")
	}

	return nil
}

// CountByOwner returns the number of apps for an owner
func (r *AppRepository) CountByOwner(ctx context.Context, ownerID uuid.UUID) (int64, error) {
	query := `SELECT COUNT(*) FROM apps WHERE owner_id = $1`

	var count int64
	err := r.pool.QueryRow(ctx, query, ownerID).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("failed to count apps: %w", err)
	}

	return count, nil
}

// ListRunning returns all running apps
func (r *AppRepository) ListRunning(ctx context.Context) ([]*domain.App, error) {
	query := `
		SELECT id, name, slug, description, status, env_vars, labels,
			current_image_id, previous_image_id, replicas, target_replicas,
			memory_limit, cpu_quota, subdomain, exposed_port, internal_port,
			created_at, updated_at, started_at, stopped_at, owner_id
		FROM apps
		WHERE status = 'running'
		ORDER BY created_at DESC
	`

	rows, err := r.pool.Query(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("failed to list running apps: %w", err)
	}
	defer rows.Close()

	var apps []*domain.App
	for rows.Next() {
		app := &domain.App{}
		var status string
		var startedAt, stoppedAt *time.Time

		err := rows.Scan(
			&app.ID,
			&app.Name,
			&app.Slug,
			&app.Description,
			&status,
			&app.EnvVars,
			&app.Labels,
			&app.CurrentImageID,
			&app.PreviousImageID,
			&app.Replicas,
			&app.TargetReplicas,
			&app.MemoryLimit,
			&app.CPUQuota,
			&app.Subdomain,
			&app.ExposedPort,
			&app.InternalPort,
			&app.CreatedAt,
			&app.UpdatedAt,
			&startedAt,
			&stoppedAt,
			&app.OwnerID,
		)

		if err != nil {
			return nil, fmt.Errorf("failed to scan app: %w", err)
		}

		app.Status = domain.AppStatus(status)
		app.StartedAt = startedAt
		app.StoppedAt = stoppedAt

		apps = append(apps, app)
	}

	return apps, nil
}
