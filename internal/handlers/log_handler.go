package handlers

import (
	"context"
	"io"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"go.uber.org/zap"

	"github.com/nanopaas/nanopaas/internal/infrastructure/docker"
	ws "github.com/nanopaas/nanopaas/pkg/websocket"
)

var logUpgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin: func(r *http.Request) bool {
		return true // Allow all origins for development
	},
}

// LogHandler handles log streaming endpoints
type LogHandler struct {
	dockerClient *docker.Client
	wsHub        *ws.Hub
	logger       *zap.Logger
}

// NewLogHandler creates a new log handler
func NewLogHandler(dockerClient *docker.Client, wsHub *ws.Hub, logger *zap.Logger) *LogHandler {
	return &LogHandler{
		dockerClient: dockerClient,
		wsHub:        wsHub,
		logger:       logger,
	}
}

// GetAppLogs returns recent logs for an app (HTTP)
func (h *LogHandler) GetAppLogs(w http.ResponseWriter, r *http.Request) {
	appID := chi.URLParam(r, "appId")
	if appID == "" {
		writeError(w, http.StatusBadRequest, "App ID required")
		return
	}

	// Get query parameters
	tail := r.URL.Query().Get("tail")
	if tail == "" {
		tail = "100"
	}

	// Find containers for this app
	allContainers, err := h.dockerClient.ListContainers(r.Context(), true)
	if err != nil {
		h.logger.Error("Failed to list containers", zap.Error(err))
		writeError(w, http.StatusInternalServerError, "Failed to list containers")
		return
	}

	// Filter by app ID label
	var containers []docker.ContainerInfo
	for _, c := range allContainers {
		if c.Labels["nanopaas.app.id"] == appID {
			containers = append(containers, c)
		}
	}

	if len(containers) == 0 {
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"logs":       []string{},
			"containers": 0,
			"message":    "No running containers",
		})
		return
	}

	// Collect logs from all containers
	var allLogs []string
	for _, container := range containers {
		logs, err := h.getContainerLogs(r.Context(), container.ID, tail)
		if err != nil {
			h.logger.Warn("Failed to get logs for container",
				zap.String("container_id", container.ID),
				zap.Error(err),
			)
			continue
		}
		allLogs = append(allLogs, logs...)
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"logs":       allLogs,
		"containers": len(containers),
		"tail":       tail,
	})
}

// StreamAppLogs streams logs via WebSocket
func (h *LogHandler) StreamAppLogs(w http.ResponseWriter, r *http.Request) {
	appID := chi.URLParam(r, "appId")
	if appID == "" {
		http.Error(w, "App ID required", http.StatusBadRequest)
		return
	}

	// Upgrade to WebSocket
	conn, err := logUpgrader.Upgrade(w, r, nil)
	if err != nil {
		h.logger.Error("WebSocket upgrade failed", zap.Error(err))
		return
	}
	defer conn.Close()

	// Find containers for this app
	allContainers, err := h.dockerClient.ListContainers(r.Context(), true)
	if err != nil {
		h.logger.Error("Failed to list containers", zap.Error(err))
		conn.WriteJSON(map[string]string{"error": "Failed to list containers"})
		return
	}

	// Filter by app ID label
	var containers []docker.ContainerInfo
	for _, c := range allContainers {
		if c.Labels["nanopaas.app.id"] == appID {
			containers = append(containers, c)
		}
	}

	if len(containers) == 0 {
		conn.WriteJSON(map[string]string{"message": "No running containers"})
		return
	}

	// Create context that cancels when connection closes
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	// Start log streaming for each container
	for _, container := range containers {
		go h.streamContainerLogs(ctx, conn, container.ID, appID)
	}

	// Keep connection alive and handle incoming messages
	for {
		_, _, err := conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				h.logger.Debug("WebSocket closed", zap.Error(err))
			}
			break
		}
	}
}

// StreamContainerLogs streams logs for a specific container
func (h *LogHandler) StreamContainerLogs(w http.ResponseWriter, r *http.Request) {
	containerID := chi.URLParam(r, "containerId")
	if containerID == "" {
		http.Error(w, "Container ID required", http.StatusBadRequest)
		return
	}

	// Upgrade to WebSocket
	conn, err := logUpgrader.Upgrade(w, r, nil)
	if err != nil {
		h.logger.Error("WebSocket upgrade failed", zap.Error(err))
		return
	}
	defer conn.Close()

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	h.streamContainerLogs(ctx, conn, containerID, "")
}

func (h *LogHandler) streamContainerLogs(ctx context.Context, conn *websocket.Conn, containerID, appID string) {
	reader, err := h.dockerClient.GetContainerLogs(ctx, containerID, true, "50")
	if err != nil {
		h.logger.Error("Failed to get container logs",
			zap.String("container_id", containerID),
			zap.Error(err),
		)
		conn.WriteJSON(map[string]string{"error": "Failed to stream logs"})
		return
	}
	defer reader.Close()

	buf := make([]byte, 8*1024)
	for {
		select {
		case <-ctx.Done():
			return
		default:
			n, err := reader.Read(buf)
			if err != nil {
				if err != io.EOF {
					h.logger.Debug("Log stream ended",
						zap.String("container_id", containerID),
						zap.Error(err),
					)
				}
				return
			}

			if n > 0 {
				// Docker log format: first 8 bytes are header
				// We skip header for simple text output
				var content string
				if n > 8 {
					content = string(buf[8:n])
				} else {
					content = string(buf[:n])
				}

				shortID := containerID
				if len(containerID) > 12 {
					shortID = containerID[:12]
				}

				message := map[string]interface{}{
					"type":         "log",
					"container_id": shortID,
					"content":      content,
					"timestamp":    time.Now().UTC().Format(time.RFC3339),
				}

				if appID != "" {
					message["app_id"] = appID
				}

				if err := conn.WriteJSON(message); err != nil {
					h.logger.Debug("Failed to write log message", zap.Error(err))
					return
				}
			}
		}
	}
}

func (h *LogHandler) getContainerLogs(ctx context.Context, containerID, tail string) ([]string, error) {
	reader, err := h.dockerClient.GetContainerLogs(ctx, containerID, false, tail)
	if err != nil {
		return nil, err
	}
	defer reader.Close()

	content, err := io.ReadAll(reader)
	if err != nil {
		return nil, err
	}

	// Parse log lines (Docker multiplexed stream format)
	var logs []string
	for len(content) > 8 {
		// Header: 8 bytes [STREAM_TYPE, 0, 0, 0, SIZE1, SIZE2, SIZE3, SIZE4]
		size := int(content[4])<<24 | int(content[5])<<16 | int(content[6])<<8 | int(content[7])
		if size <= 0 || len(content) < 8+size {
			break
		}
		logs = append(logs, string(content[8:8+size]))
		content = content[8+size:]
	}

	return logs, nil
}

// GetBuildLogs returns logs for a build
func (h *LogHandler) GetBuildLogs(w http.ResponseWriter, r *http.Request) {
	buildID := chi.URLParam(r, "buildId")
	if buildID == "" {
		writeError(w, http.StatusBadRequest, "Build ID required")
		return
	}

	// Parse UUID
	_, err := uuid.Parse(buildID)
	if err != nil {
		writeError(w, http.StatusBadRequest, "Invalid build ID")
		return
	}

	// In production, fetch from build_logs table
	// For now, return empty logs
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"build_id": buildID,
		"logs":     []string{},
		"message":  "Build logs available via WebSocket during build",
	})
}

// StreamBuildLogs streams build logs via WebSocket
func (h *LogHandler) StreamBuildLogs(w http.ResponseWriter, r *http.Request) {
	buildID := chi.URLParam(r, "buildId")
	if buildID == "" {
		http.Error(w, "Build ID required", http.StatusBadRequest)
		return
	}

	// Upgrade to WebSocket
	conn, err := logUpgrader.Upgrade(w, r, nil)
	if err != nil {
		h.logger.Error("WebSocket upgrade failed", zap.Error(err))
		return
	}

	// Create WebSocket client and subscribe to build logs
	client := ws.NewClient(h.wsHub, conn)
	h.wsHub.Register(client)

	// Subscribe to build logs topic
	logTopic := "build:" + buildID
	h.wsHub.Subscribe(client, logTopic)

	h.logger.Debug("Client subscribed to build logs",
		zap.String("build_id", buildID),
		zap.String("client_id", client.ID.String()),
	)

	// Start pumps
	go client.WritePump()
	go client.ReadPump()
}
