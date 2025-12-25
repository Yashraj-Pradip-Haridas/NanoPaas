package orchestrator

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/nanopaas/nanopaas/internal/domain"
	"github.com/nanopaas/nanopaas/internal/infrastructure/docker"
)

// OrchestratorConfig holds orchestrator configuration
type OrchestratorConfig struct {
	HealthCheckInterval time.Duration
	MaxRetries          int
	RetryBackoff        time.Duration
	DeploymentTimeout   time.Duration
}

// DefaultOrchestratorConfig returns default configuration
func DefaultOrchestratorConfig() OrchestratorConfig {
	return OrchestratorConfig{
		HealthCheckInterval: 30 * time.Second,
		MaxRetries:          3,
		RetryBackoff:        5 * time.Second,
		DeploymentTimeout:   5 * time.Minute,
	}
}

// Orchestrator manages container lifecycle and deployments
type Orchestrator struct {
	config       OrchestratorConfig
	dockerClient *docker.Client
	logger       *zap.Logger

	// Active deployments
	deployments   map[uuid.UUID]*domain.Deployment
	deploymentsMu sync.RWMutex

	// App container tracking
	appContainers   map[uuid.UUID][]string // appID -> []containerID
	appContainersMu sync.RWMutex

	// Health monitoring
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// NewOrchestrator creates a new orchestrator
func NewOrchestrator(config OrchestratorConfig, dockerClient *docker.Client, logger *zap.Logger) *Orchestrator {
	ctx, cancel := context.WithCancel(context.Background())

	o := &Orchestrator{
		config:        config,
		dockerClient:  dockerClient,
		logger:        logger,
		deployments:   make(map[uuid.UUID]*domain.Deployment),
		appContainers: make(map[uuid.UUID][]string),
		ctx:           ctx,
		cancel:        cancel,
	}

	// Start health monitor
	o.wg.Add(1)
	go o.healthMonitor()

	logger.Info("Orchestrator started",
		zap.Duration("health_check_interval", config.HealthCheckInterval),
	)

	return o
}

// Deploy deploys an application
func (o *Orchestrator) Deploy(ctx context.Context, app *domain.App) (*domain.Deployment, error) {
	if !app.CanDeploy() {
		return nil, fmt.Errorf("app is not in a deployable state: %s", app.Status)
	}

	if app.CurrentImageID == "" {
		return nil, fmt.Errorf("no image available for deployment")
	}

	// Create deployment record
	deployment := domain.NewDeployment(app.ID, app.CurrentImageID, app.TargetReplicas)
	deployment.PreviousImageID = app.PreviousImageID

	o.deploymentsMu.Lock()
	o.deployments[deployment.ID] = deployment
	o.deploymentsMu.Unlock()

	o.logger.Info("Starting deployment",
		zap.String("deployment_id", deployment.ID.String()),
		zap.String("app_id", app.ID.String()),
		zap.String("image", app.CurrentImageID),
		zap.Int("replicas", app.TargetReplicas),
	)

	// Mark as deploying
	app.MarkDeploying()
	deployment.Start()

	// Deploy with timeout
	deployCtx, cancel := context.WithTimeout(ctx, o.config.DeploymentTimeout)
	defer cancel()

	// Stop old containers gracefully
	if err := o.stopAppContainers(deployCtx, app.ID); err != nil {
		o.logger.Warn("Failed to stop old containers", zap.Error(err))
	}

	// Start new containers
	containerIDs, err := o.startContainers(deployCtx, app, deployment)
	if err != nil {
		deployment.Fail(err)
		app.MarkFailed()

		// Attempt rollback
		if app.PreviousImageID != "" {
			o.logger.Info("Attempting rollback",
				zap.String("app_id", app.ID.String()),
				zap.String("previous_image", app.PreviousImageID),
			)
			if rollbackErr := o.rollback(ctx, app); rollbackErr != nil {
				o.logger.Error("Rollback failed", zap.Error(rollbackErr))
			}
		}

		return deployment, err
	}

	// Track containers
	o.appContainersMu.Lock()
	o.appContainers[app.ID] = containerIDs
	o.appContainersMu.Unlock()

	// Success
	deployment.Succeed(containerIDs)
	app.Replicas = len(containerIDs)
	app.MarkRunning()

	o.logger.Info("Deployment succeeded",
		zap.String("deployment_id", deployment.ID.String()),
		zap.String("app_id", app.ID.String()),
		zap.Int("replicas", len(containerIDs)),
		zap.Duration("duration", deployment.Duration()),
	)

	return deployment, nil
}

// startContainers starts the specified number of container replicas
func (o *Orchestrator) startContainers(ctx context.Context, app *domain.App, deployment *domain.Deployment) ([]string, error) {
	containerIDs := make([]string, 0, app.TargetReplicas)

	for i := 0; i < app.TargetReplicas; i++ {
		containerName := app.GetContainerName(i)

		opts := docker.ContainerOptions{
			Name:          containerName,
			Image:         app.CurrentImageID,
			Env:           app.GetEnvSlice(),
			Labels:        o.buildLabels(app, deployment, i),
			ExposedPorts:  []string{fmt.Sprintf("%d", app.ExposedPort)},
			Memory:        app.MemoryLimit,
			CPUQuota:      app.CPUQuota,
			RestartPolicy: "on-failure",
		}

		containerID, err := o.dockerClient.CreateContainer(ctx, opts)
		if err != nil {
			// Clean up any containers we've created
			for _, id := range containerIDs {
				o.dockerClient.RemoveContainer(ctx, id, true)
			}
			return nil, fmt.Errorf("failed to create container %s: %w", containerName, err)
		}

		if err := o.dockerClient.StartContainer(ctx, containerID); err != nil {
			o.dockerClient.RemoveContainer(ctx, containerID, true)
			for _, id := range containerIDs {
				o.dockerClient.RemoveContainer(ctx, id, true)
			}
			return nil, fmt.Errorf("failed to start container %s: %w", containerName, err)
		}

		containerIDs = append(containerIDs, containerID)
		deployment.AddContainerID(containerID[:12])

		o.logger.Debug("Container started",
			zap.String("container_id", containerID[:12]),
			zap.String("name", containerName),
			zap.Int("replica", i),
		)
	}

	return containerIDs, nil
}

// buildLabels creates labels for a container
func (o *Orchestrator) buildLabels(app *domain.App, deployment *domain.Deployment, replica int) map[string]string {
	return map[string]string{
		"nanopaas.app.id":                            app.ID.String(),
		"nanopaas.app.name":                          app.Name,
		"nanopaas.app.slug":                          app.Slug,
		"nanopaas.deployment.id":                     deployment.ID.String(),
		"nanopaas.replica":                           fmt.Sprintf("%d", replica),
		"traefik.enable":                             "true",
		"traefik.http.routers." + app.Slug + ".rule": fmt.Sprintf("Host(`%s.localhost`)", app.Subdomain),
		"traefik.http.services." + app.Slug + ".loadbalancer.server.port": fmt.Sprintf("%d", app.ExposedPort),
	}
}

// stopAppContainers stops all containers for an app
func (o *Orchestrator) stopAppContainers(ctx context.Context, appID uuid.UUID) error {
	o.appContainersMu.RLock()
	containerIDs := o.appContainers[appID]
	o.appContainersMu.RUnlock()

	timeout := 30
	var errs []error

	for _, containerID := range containerIDs {
		if err := o.dockerClient.StopContainer(ctx, containerID, &timeout); err != nil {
			errs = append(errs, err)
		}
		if err := o.dockerClient.RemoveContainer(ctx, containerID, true); err != nil {
			errs = append(errs, err)
		}
	}

	o.appContainersMu.Lock()
	delete(o.appContainers, appID)
	o.appContainersMu.Unlock()

	if len(errs) > 0 {
		return fmt.Errorf("errors stopping containers: %v", errs)
	}
	return nil
}

// rollback reverts to the previous image
func (o *Orchestrator) rollback(ctx context.Context, app *domain.App) error {
	if !app.Rollback() {
		return fmt.Errorf("no previous image to rollback to")
	}

	o.logger.Info("Rolling back",
		zap.String("app_id", app.ID.String()),
		zap.String("image", app.CurrentImageID),
	)

	// Create rollback deployment
	deployment := domain.NewDeployment(app.ID, app.CurrentImageID, app.TargetReplicas)
	deployment.RollbackReason = "automatic rollback after failed deployment"

	deployment.Start()

	containerIDs, err := o.startContainers(ctx, app, deployment)
	if err != nil {
		deployment.Fail(err)
		deployment.MarkRolledBack("rollback failed: " + err.Error())
		return err
	}

	o.appContainersMu.Lock()
	o.appContainers[app.ID] = containerIDs
	o.appContainersMu.Unlock()

	deployment.Succeed(containerIDs)
	app.Replicas = len(containerIDs)
	app.MarkRunning()

	o.logger.Info("Rollback succeeded",
		zap.String("app_id", app.ID.String()),
		zap.Int("replicas", len(containerIDs)),
	)

	return nil
}

// Scale adjusts the number of replicas for an app
func (o *Orchestrator) Scale(ctx context.Context, app *domain.App, targetReplicas int) error {
	if targetReplicas < 0 {
		return fmt.Errorf("invalid replica count: %d", targetReplicas)
	}

	if targetReplicas > 10 {
		return fmt.Errorf("maximum replica count is 10")
	}

	// Ensure app has an image to deploy
	if app.CurrentImageID == "" && targetReplicas > 0 {
		return fmt.Errorf("cannot scale app: no image available, please build or deploy first")
	}

	o.appContainersMu.Lock()
	currentContainers := o.appContainers[app.ID]
	currentCount := len(currentContainers)
	o.appContainersMu.Unlock()

	o.logger.Info("Scaling app",
		zap.String("app_id", app.ID.String()),
		zap.Int("current", currentCount),
		zap.Int("target", targetReplicas),
	)

	if targetReplicas == currentCount {
		return nil
	}

	app.TargetReplicas = targetReplicas

	var err error
	if targetReplicas > currentCount {
		// Scale up
		err = o.scaleUp(ctx, app, currentContainers, targetReplicas-currentCount)
	} else {
		// Scale down
		err = o.scaleDown(ctx, app, currentContainers, currentCount-targetReplicas)
	}

	if err != nil {
		return err
	}

	// Update app status after successful scaling
	app.Replicas = targetReplicas
	if targetReplicas > 0 {
		app.MarkRunning()
	} else {
		app.MarkStopped()
	}

	return nil
}

// scaleUp adds more replicas
func (o *Orchestrator) scaleUp(ctx context.Context, app *domain.App, currentContainers []string, count int) error {
	startReplica := len(currentContainers)

	o.logger.Info("scaleUp called",
		zap.String("app_id", app.ID.String()),
		zap.String("image", app.CurrentImageID),
		zap.Int("count", count),
	)

	for i := 0; i < count; i++ {
		replica := startReplica + i
		containerName := app.GetContainerName(replica)

		opts := docker.ContainerOptions{
			Name:          containerName,
			Image:         app.CurrentImageID,
			Env:           app.GetEnvSlice(),
			Labels:        o.buildScaleLabels(app, replica),
			ExposedPorts:  []string{fmt.Sprintf("%d", app.ExposedPort)},
			Memory:        app.MemoryLimit,
			CPUQuota:      app.CPUQuota,
			RestartPolicy: "on-failure",
		}

		o.logger.Debug("Creating container",
			zap.String("name", containerName),
			zap.String("image", opts.Image),
		)

		// Try to remove any existing container with the same name (cleanup from previous runs)
		// This is a best-effort cleanup - we ignore errors if container doesn't exist
		existingContainers, _ := o.dockerClient.ListContainers(ctx, true)
		for _, c := range existingContainers {
			if c.Name == containerName || c.Name == "/"+containerName {
				o.logger.Info("Removing existing container with same name",
					zap.String("name", containerName),
					zap.String("id", c.ID),
				)
				o.dockerClient.RemoveContainer(ctx, c.ID, true)
			}
		}

		containerID, err := o.dockerClient.CreateContainer(ctx, opts)
		if err != nil {
			o.logger.Error("Failed to create container",
				zap.Error(err),
				zap.String("name", containerName),
				zap.String("image", opts.Image),
			)
			return fmt.Errorf("failed to create replica %d: %w", replica, err)
		}

		if err := o.dockerClient.StartContainer(ctx, containerID); err != nil {
			o.dockerClient.RemoveContainer(ctx, containerID, true)
			return fmt.Errorf("failed to start replica %d: %w", replica, err)
		}

		o.appContainersMu.Lock()
		o.appContainers[app.ID] = append(o.appContainers[app.ID], containerID)
		o.appContainersMu.Unlock()

		o.logger.Debug("Scaled up replica",
			zap.String("container_id", containerID[:12]),
			zap.Int("replica", replica),
		)
	}

	app.Replicas = len(currentContainers) + count
	return nil
}

// scaleDown removes replicas
func (o *Orchestrator) scaleDown(ctx context.Context, app *domain.App, currentContainers []string, count int) error {
	timeout := 30

	// Remove from the end
	toRemove := currentContainers[len(currentContainers)-count:]

	for _, containerID := range toRemove {
		if err := o.dockerClient.StopContainer(ctx, containerID, &timeout); err != nil {
			o.logger.Warn("Failed to stop container during scale down", zap.Error(err))
		}
		if err := o.dockerClient.RemoveContainer(ctx, containerID, true); err != nil {
			o.logger.Warn("Failed to remove container during scale down", zap.Error(err))
		}

		o.logger.Debug("Scaled down replica", zap.String("container_id", containerID[:12]))
	}

	o.appContainersMu.Lock()
	o.appContainers[app.ID] = currentContainers[:len(currentContainers)-count]
	o.appContainersMu.Unlock()

	app.Replicas = len(currentContainers) - count
	return nil
}

// buildScaleLabels creates labels for scaled containers
func (o *Orchestrator) buildScaleLabels(app *domain.App, replica int) map[string]string {
	return map[string]string{
		"nanopaas.app.id":                            app.ID.String(),
		"nanopaas.app.name":                          app.Name,
		"nanopaas.app.slug":                          app.Slug,
		"nanopaas.replica":                           fmt.Sprintf("%d", replica),
		"traefik.enable":                             "true",
		"traefik.http.routers." + app.Slug + ".rule": fmt.Sprintf("Host(`%s.localhost`)", app.Subdomain),
		"traefik.http.services." + app.Slug + ".loadbalancer.server.port": fmt.Sprintf("%d", app.ExposedPort),
	}
}

// Stop stops an application
func (o *Orchestrator) Stop(ctx context.Context, app *domain.App) error {
	if err := o.stopAppContainers(ctx, app.ID); err != nil {
		return err
	}
	app.MarkStopped()
	app.Replicas = 0

	o.logger.Info("App stopped", zap.String("app_id", app.ID.String()))
	return nil
}

// Restart restarts an application
func (o *Orchestrator) Restart(ctx context.Context, app *domain.App) error {
	o.appContainersMu.RLock()
	containerIDs := o.appContainers[app.ID]
	o.appContainersMu.RUnlock()

	timeout := 30
	for _, containerID := range containerIDs {
		if err := o.dockerClient.RestartContainer(ctx, containerID, &timeout); err != nil {
			o.logger.Warn("Failed to restart container", zap.Error(err), zap.String("id", containerID[:12]))
		}
	}

	o.logger.Info("App restarted", zap.String("app_id", app.ID.String()))
	return nil
}

// GetAppContainers returns container IDs for an app
func (o *Orchestrator) GetAppContainers(appID uuid.UUID) []string {
	o.appContainersMu.RLock()
	defer o.appContainersMu.RUnlock()
	return o.appContainers[appID]
}

// healthMonitor monitors container health
func (o *Orchestrator) healthMonitor() {
	defer o.wg.Done()

	ticker := time.NewTicker(o.config.HealthCheckInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			o.checkContainerHealth()
		case <-o.ctx.Done():
			o.logger.Debug("Health monitor stopped")
			return
		}
	}
}

// checkContainerHealth checks health of all managed containers
func (o *Orchestrator) checkContainerHealth() {
	o.appContainersMu.RLock()
	appContainersCopy := make(map[uuid.UUID][]string)
	for k, v := range o.appContainers {
		appContainersCopy[k] = v
	}
	o.appContainersMu.RUnlock()

	for appID, containerIDs := range appContainersCopy {
		for _, containerID := range containerIDs {
			healthy, err := o.dockerClient.HealthCheck(o.ctx, containerID)
			if err != nil {
				o.logger.Warn("Health check failed",
					zap.String("app_id", appID.String()),
					zap.String("container_id", containerID[:12]),
					zap.Error(err),
				)
				continue
			}

			if !healthy {
				o.logger.Warn("Container unhealthy, restarting",
					zap.String("app_id", appID.String()),
					zap.String("container_id", containerID[:12]),
				)
				timeout := 10
				o.dockerClient.RestartContainer(o.ctx, containerID, &timeout)
			}
		}
	}
}

// Shutdown gracefully shuts down the orchestrator
func (o *Orchestrator) Shutdown() {
	o.logger.Info("Shutting down orchestrator...")
	o.cancel()
	o.wg.Wait()
	o.logger.Info("Orchestrator stopped")
}

// GetDeployment returns a deployment by ID
func (o *Orchestrator) GetDeployment(deploymentID uuid.UUID) (*domain.Deployment, bool) {
	o.deploymentsMu.RLock()
	defer o.deploymentsMu.RUnlock()
	d, ok := o.deployments[deploymentID]
	return d, ok
}

// ListDeployments returns all active deployments
func (o *Orchestrator) ListDeployments() []*domain.Deployment {
	o.deploymentsMu.RLock()
	defer o.deploymentsMu.RUnlock()

	deployments := make([]*domain.Deployment, 0, len(o.deployments))
	for _, d := range o.deployments {
		deployments = append(deployments, d)
	}
	return deployments
}
