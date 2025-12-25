package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/nanopaas/nanopaas/internal/domain"
	"github.com/nanopaas/nanopaas/internal/services/orchestrator"
	"github.com/nanopaas/nanopaas/internal/services/router"
)

// AppHandler handles application management endpoints
type AppHandler struct {
	orchestrator *orchestrator.Orchestrator
	router       *router.TraefikRouter
	logger       *zap.Logger
	apps         map[uuid.UUID]*domain.App // In-memory store (use DB in production)
}

// CreateAppRequest represents a request to create an app
type CreateAppRequest struct {
	Name        string            `json:"name"`
	Slug        string            `json:"slug"`
	Description string            `json:"description,omitempty"`
	EnvVars     map[string]string `json:"env_vars,omitempty"`
	ExposedPort int               `json:"exposed_port,omitempty"`
	MemoryLimit int64             `json:"memory_limit,omitempty"`
	CPUQuota    int64             `json:"cpu_quota,omitempty"`
}

// UpdateAppRequest represents a request to update an app
type UpdateAppRequest struct {
	Name        string            `json:"name,omitempty"`
	Description string            `json:"description,omitempty"`
	EnvVars     map[string]string `json:"env_vars,omitempty"`
	ExposedPort int               `json:"exposed_port,omitempty"`
	MemoryLimit int64             `json:"memory_limit,omitempty"`
	CPUQuota    int64             `json:"cpu_quota,omitempty"`
}

// DeployRequest represents a deployment request
type DeployRequest struct {
	ImageID  string `json:"image_id"`
	Replicas int    `json:"replicas,omitempty"`
}

// ScaleRequest represents a scaling request
type ScaleRequest struct {
	Replicas int `json:"replicas"`
}

// AppResponse represents an app in API responses
type AppResponse struct {
	ID              string            `json:"id"`
	Name            string            `json:"name"`
	Slug            string            `json:"slug"`
	Description     string            `json:"description,omitempty"`
	Status          string            `json:"status"`
	URL             string            `json:"url,omitempty"`
	Replicas        int               `json:"replicas"`
	TargetReplicas  int               `json:"target_replicas"`
	CurrentImageID  string            `json:"current_image_id,omitempty"`
	EnvVars         map[string]string `json:"env_vars,omitempty"`
	ExposedPort     int               `json:"exposed_port"`
	MemoryLimit     int64             `json:"memory_limit"`
	CPUQuota        int64             `json:"cpu_quota"`
	CreatedAt       string            `json:"created_at"`
	UpdatedAt       string            `json:"updated_at"`
}

// NewAppHandler creates a new app handler
func NewAppHandler(orch *orchestrator.Orchestrator, rtr *router.TraefikRouter, logger *zap.Logger) *AppHandler {
	return &AppHandler{
		orchestrator: orch,
		router:       rtr,
		logger:       logger,
		apps:         make(map[uuid.UUID]*domain.App),
	}
}

// Create creates a new application
func (h *AppHandler) Create(w http.ResponseWriter, r *http.Request) {
	var req CreateAppRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	if req.Name == "" {
		writeError(w, http.StatusBadRequest, "App name is required")
		return
	}

	if req.Slug == "" {
		req.Slug = slugify(req.Name)
	}

	// Check for duplicate slug
	for _, app := range h.apps {
		if app.Slug == req.Slug {
			writeError(w, http.StatusConflict, "App with this slug already exists")
			return
		}
	}

	// Create app
	ownerID := uuid.New() // Placeholder - get from auth in production
	app := domain.NewApp(req.Name, req.Slug, ownerID)
	app.Description = req.Description

	if req.ExposedPort > 0 {
		app.ExposedPort = req.ExposedPort
	}
	if req.MemoryLimit > 0 {
		app.MemoryLimit = req.MemoryLimit
	}
	if req.CPUQuota > 0 {
		app.CPUQuota = req.CPUQuota
	}
	for k, v := range req.EnvVars {
		app.SetEnvVar(k, v)
	}

	// Store app
	h.apps[app.ID] = app

	h.logger.Info("App created",
		zap.String("app_id", app.ID.String()),
		zap.String("name", app.Name),
		zap.String("slug", app.Slug),
	)

	writeJSON(w, http.StatusCreated, h.appToResponse(app))
}

// List returns all applications
func (h *AppHandler) List(w http.ResponseWriter, r *http.Request) {
	apps := make([]AppResponse, 0, len(h.apps))
	for _, app := range h.apps {
		apps = append(apps, h.appToResponse(app))
	}
	writeJSON(w, http.StatusOK, apps)
}

// Get returns an application by ID
func (h *AppHandler) Get(w http.ResponseWriter, r *http.Request) {
	appID := chi.URLParam(r, "appId")
	app, err := h.getApp(appID)
	if err != nil {
		writeError(w, http.StatusNotFound, "App not found")
		return
	}

	writeJSON(w, http.StatusOK, h.appToResponse(app))
}

// Update updates an application
func (h *AppHandler) Update(w http.ResponseWriter, r *http.Request) {
	appID := chi.URLParam(r, "appId")
	app, err := h.getApp(appID)
	if err != nil {
		writeError(w, http.StatusNotFound, "App not found")
		return
	}

	var req UpdateAppRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	if req.Name != "" {
		app.Name = req.Name
	}
	if req.Description != "" {
		app.Description = req.Description
	}
	if req.ExposedPort > 0 {
		app.ExposedPort = req.ExposedPort
	}
	if req.MemoryLimit > 0 {
		app.MemoryLimit = req.MemoryLimit
	}
	if req.CPUQuota > 0 {
		app.CPUQuota = req.CPUQuota
	}
	for k, v := range req.EnvVars {
		app.SetEnvVar(k, v)
	}

	h.logger.Info("App updated", zap.String("app_id", appID))
	writeJSON(w, http.StatusOK, h.appToResponse(app))
}

// Delete deletes an application
func (h *AppHandler) Delete(w http.ResponseWriter, r *http.Request) {
	appID := chi.URLParam(r, "appId")
	app, err := h.getApp(appID)
	if err != nil {
		writeError(w, http.StatusNotFound, "App not found")
		return
	}

	// Stop containers
	if err := h.orchestrator.Stop(r.Context(), app); err != nil {
		h.logger.Warn("Failed to stop app containers", zap.Error(err))
	}

	// Remove route
	h.router.RemoveRoute(r.Context(), app.ID)

	// Delete from store
	delete(h.apps, app.ID)

	h.logger.Info("App deleted", zap.String("app_id", appID))
	writeJSON(w, http.StatusOK, map[string]string{
		"message": "App deleted successfully",
	})
}

// Deploy deploys an application
func (h *AppHandler) Deploy(w http.ResponseWriter, r *http.Request) {
	appID := chi.URLParam(r, "appId")
	app, err := h.getApp(appID)
	if err != nil {
		writeError(w, http.StatusNotFound, "App not found")
		return
	}

	var req DeployRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	if req.ImageID == "" {
		writeError(w, http.StatusBadRequest, "image_id is required")
		return
	}

	if req.Replicas > 0 {
		app.TargetReplicas = req.Replicas
	}

	app.UpdateImage(req.ImageID)

	// Deploy
	deployment, err := h.orchestrator.Deploy(r.Context(), app)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Deployment failed: "+err.Error())
		return
	}

	// Update route
	containerIDs := h.orchestrator.GetAppContainers(app.ID)
	replicas := make([]router.Replica, 0, len(containerIDs))
	// Note: In production, get actual container IPs from Docker
	for i := range containerIDs {
		replicas = append(replicas, router.Replica{
			ContainerID: containerIDs[i],
			IPAddress:   "127.0.0.1", // Placeholder
			Port:        app.ExposedPort,
			Weight:      1,
		})
	}
	h.router.AddRoute(r.Context(), app, replicas)

	h.logger.Info("App deployed",
		zap.String("app_id", appID),
		zap.String("deployment_id", deployment.ID.String()),
	)

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"message":       "Deployment started",
		"deployment_id": deployment.ID.String(),
		"status":        string(deployment.Status),
		"url":           h.router.GetAppURL(app),
	})
}

// Scale scales an application
func (h *AppHandler) Scale(w http.ResponseWriter, r *http.Request) {
	appID := chi.URLParam(r, "appId")
	app, err := h.getApp(appID)
	if err != nil {
		writeError(w, http.StatusNotFound, "App not found")
		return
	}

	var req ScaleRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	if req.Replicas < 0 || req.Replicas > 10 {
		writeError(w, http.StatusBadRequest, "Replicas must be between 0 and 10")
		return
	}

	if err := h.orchestrator.Scale(r.Context(), app, req.Replicas); err != nil {
		writeError(w, http.StatusInternalServerError, "Scaling failed: "+err.Error())
		return
	}

	h.logger.Info("App scaled",
		zap.String("app_id", appID),
		zap.Int("replicas", req.Replicas),
	)

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"message":  "Scaling complete",
		"replicas": app.Replicas,
	})
}

// Restart restarts an application
func (h *AppHandler) Restart(w http.ResponseWriter, r *http.Request) {
	appID := chi.URLParam(r, "appId")
	app, err := h.getApp(appID)
	if err != nil {
		writeError(w, http.StatusNotFound, "App not found")
		return
	}

	if err := h.orchestrator.Restart(r.Context(), app); err != nil {
		writeError(w, http.StatusInternalServerError, "Restart failed: "+err.Error())
		return
	}

	h.logger.Info("App restarted", zap.String("app_id", appID))
	writeJSON(w, http.StatusOK, map[string]string{
		"message": "App restarted",
	})
}

// Stop stops an application
func (h *AppHandler) Stop(w http.ResponseWriter, r *http.Request) {
	appID := chi.URLParam(r, "appId")
	app, err := h.getApp(appID)
	if err != nil {
		writeError(w, http.StatusNotFound, "App not found")
		return
	}

	if err := h.orchestrator.Stop(r.Context(), app); err != nil {
		writeError(w, http.StatusInternalServerError, "Stop failed: "+err.Error())
		return
	}

	// Remove route
	h.router.RemoveRoute(r.Context(), app.ID)

	h.logger.Info("App stopped", zap.String("app_id", appID))
	writeJSON(w, http.StatusOK, map[string]string{
		"message": "App stopped",
	})
}

// SetEnvVars sets environment variables
func (h *AppHandler) SetEnvVars(w http.ResponseWriter, r *http.Request) {
	appID := chi.URLParam(r, "appId")
	app, err := h.getApp(appID)
	if err != nil {
		writeError(w, http.StatusNotFound, "App not found")
		return
	}

	var envVars map[string]string
	if err := json.NewDecoder(r.Body).Decode(&envVars); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	for k, v := range envVars {
		app.SetEnvVar(k, v)
	}

	h.logger.Info("Env vars updated",
		zap.String("app_id", appID),
		zap.Int("count", len(envVars)),
	)

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"message":  "Environment variables updated",
		"env_vars": app.EnvVars,
	})
}

// DeleteEnvVar deletes an environment variable
func (h *AppHandler) DeleteEnvVar(w http.ResponseWriter, r *http.Request) {
	appID := chi.URLParam(r, "appId")
	app, err := h.getApp(appID)
	if err != nil {
		writeError(w, http.StatusNotFound, "App not found")
		return
	}

	key := chi.URLParam(r, "key")
	if key == "" {
		writeError(w, http.StatusBadRequest, "Key is required")
		return
	}

	app.DeleteEnvVar(key)

	h.logger.Info("Env var deleted",
		zap.String("app_id", appID),
		zap.String("key", key),
	)

	writeJSON(w, http.StatusOK, map[string]string{
		"message": "Environment variable deleted",
	})
}

// Logs streams application logs
func (h *AppHandler) Logs(w http.ResponseWriter, r *http.Request) {
	appID := chi.URLParam(r, "appId")
	app, err := h.getApp(appID)
	if err != nil {
		writeError(w, http.StatusNotFound, "App not found")
		return
	}

	// Get containers
	containerIDs := h.orchestrator.GetAppContainers(app.ID)
	if len(containerIDs) == 0 {
		writeError(w, http.StatusNotFound, "No containers running")
		return
	}

	// Return container info for log streaming
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"app_id":      appID,
		"containers":  containerIDs,
		"log_streams": len(containerIDs),
	})
}

// Helper methods

func (h *AppHandler) getApp(idStr string) (*domain.App, error) {
	id, err := uuid.Parse(idStr)
	if err != nil {
		return nil, fmt.Errorf("invalid app ID format: %w", err)
	}
	app, exists := h.apps[id]
	if !exists {
		return nil, fmt.Errorf("app not found: %s", idStr)
	}
	return app, nil
}

func (h *AppHandler) appToResponse(app *domain.App) AppResponse {
	response := AppResponse{
		ID:             app.ID.String(),
		Name:           app.Name,
		Slug:           app.Slug,
		Description:    app.Description,
		Status:         string(app.Status),
		Replicas:       app.Replicas,
		TargetReplicas: app.TargetReplicas,
		CurrentImageID: app.CurrentImageID,
		EnvVars:        app.EnvVars,
		ExposedPort:    app.ExposedPort,
		MemoryLimit:    app.MemoryLimit,
		CPUQuota:       app.CPUQuota,
		CreatedAt:      app.CreatedAt.Format("2006-01-02T15:04:05Z"),
		UpdatedAt:      app.UpdatedAt.Format("2006-01-02T15:04:05Z"),
	}

	if app.Status == domain.AppStatusRunning {
		response.URL = h.router.GetAppURL(app)
	}

	return response
}

// UpdateAppImage updates an app's current image (called by build handler on success)
func (h *AppHandler) UpdateAppImage(appID string, imageID, imageTag string) {
	id, err := uuid.Parse(appID)
	if err != nil {
		h.logger.Error("UpdateAppImage: invalid app ID", zap.String("app_id", appID))
		return
	}

	app, exists := h.apps[id]
	if !exists {
		h.logger.Warn("UpdateAppImage: app not found", zap.String("app_id", appID))
		return
	}

	app.UpdateImage(imageTag)
	h.logger.Info("App image updated after build",
		zap.String("app_id", appID),
		zap.String("image_tag", imageTag),
	)
}

func slugify(name string) string {
	// Simple slugify - in production use a proper slugify library
	slug := ""
	for _, r := range name {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			slug += string(r)
		} else if r >= 'A' && r <= 'Z' {
			slug += string(r + 32)
		} else if r == ' ' || r == '-' || r == '_' {
			if len(slug) > 0 && slug[len(slug)-1] != '-' {
				slug += "-"
			}
		}
	}
	return slug
}
