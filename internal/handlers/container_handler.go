package handlers

import (
	"encoding/json"
	"io"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"go.uber.org/zap"

	"github.com/nanopaas/nanopaas/internal/infrastructure/docker"
)

// ContainerHandler handles container management endpoints
type ContainerHandler struct {
	dockerClient *docker.Client
	logger       *zap.Logger
}

// CreateContainerRequest represents a request to create a container
type CreateContainerRequest struct {
	Name          string            `json:"name"`
	Image         string            `json:"image"`
	Env           map[string]string `json:"env,omitempty"`
	Labels        map[string]string `json:"labels,omitempty"`
	ExposedPorts  []string          `json:"exposed_ports,omitempty"`
	MemoryLimit   int64             `json:"memory_limit,omitempty"`
	CPUQuota      int64             `json:"cpu_quota,omitempty"`
	RestartPolicy string            `json:"restart_policy,omitempty"`
}

// ContainerResponse represents a container in API responses
type ContainerResponse struct {
	ID        string            `json:"id"`
	Name      string            `json:"name"`
	Image     string            `json:"image"`
	Status    string            `json:"status"`
	State     string            `json:"state"`
	Ports     map[string]string `json:"ports,omitempty"`
	Labels    map[string]string `json:"labels,omitempty"`
	CreatedAt string            `json:"created_at"`
	IPAddress string            `json:"ip_address,omitempty"`
}

// NewContainerHandler creates a new container handler
func NewContainerHandler(dockerClient *docker.Client, logger *zap.Logger) *ContainerHandler {
	return &ContainerHandler{
		dockerClient: dockerClient,
		logger:       logger,
	}
}

// List returns all containers
func (h *ContainerHandler) List(w http.ResponseWriter, r *http.Request) {
	all := r.URL.Query().Get("all") == "true"

	containers, err := h.dockerClient.ListContainers(r.Context(), all)
	if err != nil {
		h.logger.Error("Failed to list containers", zap.Error(err))
		writeError(w, http.StatusInternalServerError, "Failed to list containers")
		return
	}

	response := make([]ContainerResponse, 0, len(containers))
	for _, c := range containers {
		response = append(response, ContainerResponse{
			ID:        c.ID,
			Name:      c.Name,
			Image:     c.Image,
			Status:    c.Status,
			State:     c.State,
			Ports:     c.Ports,
			Labels:    c.Labels,
			CreatedAt: c.CreatedAt.Format("2006-01-02T15:04:05Z"),
			IPAddress: c.IPAddress,
		})
	}

	writeJSON(w, http.StatusOK, response)
}

// Create creates a new container
func (h *ContainerHandler) Create(w http.ResponseWriter, r *http.Request) {
	var req CreateContainerRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	if req.Name == "" {
		writeError(w, http.StatusBadRequest, "Container name is required")
		return
	}

	if req.Image == "" {
		writeError(w, http.StatusBadRequest, "Image is required")
		return
	}

	// Convert env map to slice
	envSlice := make([]string, 0, len(req.Env))
	for k, v := range req.Env {
		envSlice = append(envSlice, k+"="+v)
	}

	// Set defaults
	if req.MemoryLimit == 0 {
		req.MemoryLimit = 512 * 1024 * 1024 // 512MB
	}
	if req.CPUQuota == 0 {
		req.CPUQuota = 50000 // 50% CPU
	}
	if len(req.ExposedPorts) == 0 {
		req.ExposedPorts = []string{"8080"}
	}

	opts := docker.ContainerOptions{
		Name:          req.Name,
		Image:         req.Image,
		Env:           envSlice,
		Labels:        req.Labels,
		ExposedPorts:  req.ExposedPorts,
		Memory:        req.MemoryLimit,
		CPUQuota:      req.CPUQuota,
		RestartPolicy: req.RestartPolicy,
		User:          "1000:1000", // Non-root user
		ReadOnly:      false,
		Privileged:    false,
	}

	containerID, err := h.dockerClient.CreateContainer(r.Context(), opts)
	if err != nil {
		h.logger.Error("Failed to create container", zap.Error(err))
		writeError(w, http.StatusInternalServerError, "Failed to create container: "+err.Error())
		return
	}

	h.logger.Info("Container created",
		zap.String("id", containerID[:12]),
		zap.String("name", req.Name),
	)

	writeJSON(w, http.StatusCreated, map[string]string{
		"id":      containerID[:12],
		"name":    req.Name,
		"message": "Container created successfully",
	})
}

// Get returns container details
func (h *ContainerHandler) Get(w http.ResponseWriter, r *http.Request) {
	containerID := chi.URLParam(r, "id")
	if containerID == "" {
		writeError(w, http.StatusBadRequest, "Container ID is required")
		return
	}

	info, err := h.dockerClient.InspectContainer(r.Context(), containerID)
	if err != nil {
		h.logger.Error("Failed to inspect container", zap.Error(err), zap.String("id", containerID))
		writeError(w, http.StatusNotFound, "Container not found")
		return
	}

	response := map[string]interface{}{
		"id":         info.ID[:12],
		"name":       info.Name,
		"image":      info.Image,
		"state":      info.State.Status,
		"created_at": info.Created,
		"config": map[string]interface{}{
			"env":    info.Config.Env,
			"labels": info.Config.Labels,
		},
	}

	if info.NetworkSettings != nil && len(info.NetworkSettings.Networks) > 0 {
		networks := make(map[string]string)
		for name, network := range info.NetworkSettings.Networks {
			networks[name] = network.IPAddress
		}
		response["networks"] = networks
	}

	writeJSON(w, http.StatusOK, response)
}

// Delete removes a container
func (h *ContainerHandler) Delete(w http.ResponseWriter, r *http.Request) {
	containerID := chi.URLParam(r, "id")
	if containerID == "" {
		writeError(w, http.StatusBadRequest, "Container ID is required")
		return
	}

	force := r.URL.Query().Get("force") == "true"

	if err := h.dockerClient.RemoveContainer(r.Context(), containerID, force); err != nil {
		h.logger.Error("Failed to remove container", zap.Error(err), zap.String("id", containerID))
		writeError(w, http.StatusInternalServerError, "Failed to remove container")
		return
	}

	h.logger.Info("Container removed", zap.String("id", containerID))
	writeJSON(w, http.StatusOK, map[string]string{
		"message": "Container removed successfully",
	})
}

// Start starts a container
func (h *ContainerHandler) Start(w http.ResponseWriter, r *http.Request) {
	containerID := chi.URLParam(r, "id")
	if containerID == "" {
		writeError(w, http.StatusBadRequest, "Container ID is required")
		return
	}

	if err := h.dockerClient.StartContainer(r.Context(), containerID); err != nil {
		h.logger.Error("Failed to start container", zap.Error(err), zap.String("id", containerID))
		writeError(w, http.StatusInternalServerError, "Failed to start container")
		return
	}

	h.logger.Info("Container started", zap.String("id", containerID))
	writeJSON(w, http.StatusOK, map[string]string{
		"message": "Container started successfully",
	})
}

// Stop stops a container
func (h *ContainerHandler) Stop(w http.ResponseWriter, r *http.Request) {
	containerID := chi.URLParam(r, "id")
	if containerID == "" {
		writeError(w, http.StatusBadRequest, "Container ID is required")
		return
	}

	timeout := 30
	if t := r.URL.Query().Get("timeout"); t != "" {
		if parsed, err := strconv.Atoi(t); err == nil {
			timeout = parsed
		}
	}

	if err := h.dockerClient.StopContainer(r.Context(), containerID, &timeout); err != nil {
		h.logger.Error("Failed to stop container", zap.Error(err), zap.String("id", containerID))
		writeError(w, http.StatusInternalServerError, "Failed to stop container")
		return
	}

	h.logger.Info("Container stopped", zap.String("id", containerID))
	writeJSON(w, http.StatusOK, map[string]string{
		"message": "Container stopped successfully",
	})
}

// Restart restarts a container
func (h *ContainerHandler) Restart(w http.ResponseWriter, r *http.Request) {
	containerID := chi.URLParam(r, "id")
	if containerID == "" {
		writeError(w, http.StatusBadRequest, "Container ID is required")
		return
	}

	timeout := 30
	if t := r.URL.Query().Get("timeout"); t != "" {
		if parsed, err := strconv.Atoi(t); err == nil {
			timeout = parsed
		}
	}

	if err := h.dockerClient.RestartContainer(r.Context(), containerID, &timeout); err != nil {
		h.logger.Error("Failed to restart container", zap.Error(err), zap.String("id", containerID))
		writeError(w, http.StatusInternalServerError, "Failed to restart container")
		return
	}

	h.logger.Info("Container restarted", zap.String("id", containerID))
	writeJSON(w, http.StatusOK, map[string]string{
		"message": "Container restarted successfully",
	})
}

// Logs streams container logs
func (h *ContainerHandler) Logs(w http.ResponseWriter, r *http.Request) {
	containerID := chi.URLParam(r, "id")
	if containerID == "" {
		writeError(w, http.StatusBadRequest, "Container ID is required")
		return
	}

	follow := r.URL.Query().Get("follow") == "true"
	tail := r.URL.Query().Get("tail")
	if tail == "" {
		tail = "100"
	}

	logs, err := h.dockerClient.GetContainerLogs(r.Context(), containerID, follow, tail)
	if err != nil {
		h.logger.Error("Failed to get logs", zap.Error(err), zap.String("id", containerID))
		writeError(w, http.StatusInternalServerError, "Failed to get logs")
		return
	}
	defer logs.Close()

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	if follow {
		w.Header().Set("Transfer-Encoding", "chunked")
	}

	// Stream logs to response
	io.Copy(w, logs)
}

// Helper functions
func writeJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{
		"error": message,
	})
}
