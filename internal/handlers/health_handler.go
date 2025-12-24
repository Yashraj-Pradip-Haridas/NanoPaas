package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"go.uber.org/zap"

	"github.com/nanopaas/nanopaas/internal/infrastructure/docker"
)

// HealthHandler handles health check endpoints
type HealthHandler struct {
	dockerClient *docker.Client
	logger       *zap.Logger
	startTime    time.Time
}

// HealthResponse represents the health check response
type HealthResponse struct {
	Status    string            `json:"status"`
	Timestamp string            `json:"timestamp"`
	Uptime    string            `json:"uptime"`
	Checks    map[string]string `json:"checks,omitempty"`
}

// NewHealthHandler creates a new health handler
func NewHealthHandler(dockerClient *docker.Client, logger *zap.Logger) *HealthHandler {
	return &HealthHandler{
		dockerClient: dockerClient,
		logger:       logger,
		startTime:    time.Now(),
	}
}

// Health returns basic health status
func (h *HealthHandler) Health(w http.ResponseWriter, r *http.Request) {
	response := HealthResponse{
		Status:    "ok",
		Timestamp: time.Now().UTC().Format(time.RFC3339),
		Uptime:    time.Since(h.startTime).String(),
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

// DockerHealth returns Docker daemon health status
func (h *HealthHandler) DockerHealth(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	checks := make(map[string]string)
	status := "ok"

	// Check Docker connection
	if err := h.dockerClient.Ping(ctx); err != nil {
		checks["docker"] = "unhealthy: " + err.Error()
		status = "degraded"
		h.logger.Warn("Docker health check failed", zap.Error(err))
	} else {
		checks["docker"] = "healthy"
	}

	// Get Docker info
	if info, err := h.dockerClient.Info(ctx); err == nil {
		checks["docker_version"] = info.ServerVersion
		checks["containers_running"] = string(rune('0' + info.ContainersRunning))
	}

	response := HealthResponse{
		Status:    status,
		Timestamp: time.Now().UTC().Format(time.RFC3339),
		Uptime:    time.Since(h.startTime).String(),
		Checks:    checks,
	}

	w.Header().Set("Content-Type", "application/json")
	if status != "ok" {
		w.WriteHeader(http.StatusServiceUnavailable)
	}
	json.NewEncoder(w).Encode(response)
}

// Ready returns readiness status
func (h *HealthHandler) Ready(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	// Check if Docker is available
	if err := h.dockerClient.Ping(ctx); err != nil {
		http.Error(w, "not ready: docker unavailable", http.StatusServiceUnavailable)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"status": "ready",
	})
}
