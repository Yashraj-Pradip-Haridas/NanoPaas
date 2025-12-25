package handlers

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"go.uber.org/zap"

	"github.com/nanopaas/nanopaas/internal/domain"
	"github.com/nanopaas/nanopaas/internal/services/builder"
	ws "github.com/nanopaas/nanopaas/pkg/websocket"
)

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin: func(r *http.Request) bool {
		return true // Allow all origins for development
	},
}

// AppUpdater interface for updating app image after build success
type AppUpdater interface {
	UpdateAppImage(appID string, imageID, imageTag string)
}

// BuildHandler handles build-related endpoints
type BuildHandler struct {
	builder    *builder.Builder
	wsHub      *ws.Hub
	logger     *zap.Logger
	appUpdater AppUpdater
}

// CreateBuildRequest represents a request to create a new build
type CreateBuildRequest struct {
	Source         string            `json:"source"` // "gzip", "git", "url"
	SourceURL      string            `json:"source_url,omitempty"`
	GitRef         string            `json:"git_ref,omitempty"`
	DockerfilePath string            `json:"dockerfile_path,omitempty"`
	BuildArgs      map[string]string `json:"build_args,omitempty"`
}

// BuildResponse represents a build in API responses
type BuildResponse struct {
	ID           string            `json:"id"`
	AppID        string            `json:"app_id"`
	Status       string            `json:"status"`
	Source       string            `json:"source"`
	ImageTag     string            `json:"image_tag,omitempty"`
	ImageID      string            `json:"image_id,omitempty"`
	Duration     string            `json:"duration,omitempty"`
	Error        string            `json:"error,omitempty"`
	CreatedAt    string            `json:"created_at"`
	StartedAt    string            `json:"started_at,omitempty"`
	CompletedAt  string            `json:"completed_at,omitempty"`
	WebSocketURL string            `json:"websocket_url,omitempty"`
}

// NewBuildHandler creates a new build handler
func NewBuildHandler(bldr *builder.Builder, wsHub *ws.Hub, logger *zap.Logger) *BuildHandler {
	return &BuildHandler{
		builder: bldr,
		wsHub:   wsHub,
		logger:  logger,
	}
}

// SetAppUpdater sets the app updater for build-to-deploy integration
func (h *BuildHandler) SetAppUpdater(updater AppUpdater) {
	h.appUpdater = updater
}

// Create initiates a new build
func (h *BuildHandler) Create(w http.ResponseWriter, r *http.Request) {
	appID := chi.URLParam(r, "appId")
	if appID == "" {
		writeError(w, http.StatusBadRequest, "App ID is required")
		return
	}

	appUUID, err := uuid.Parse(appID)
	if err != nil {
		writeError(w, http.StatusBadRequest, "Invalid app ID format")
		return
	}

	var req CreateBuildRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	// Validate source type
	var source domain.BuildSource
	switch req.Source {
	case "gzip":
		source = domain.BuildSourceGzip
	case "git":
		source = domain.BuildSourceGit
		if req.SourceURL == "" {
			writeError(w, http.StatusBadRequest, "source_url is required for git builds")
			return
		}
	case "url":
		source = domain.BuildSourceURL
		if req.SourceURL == "" {
			writeError(w, http.StatusBadRequest, "source_url is required for URL builds")
			return
		}
	default:
		writeError(w, http.StatusBadRequest, "Invalid source type. Must be 'gzip', 'git', or 'url'")
		return
	}

	// Create build entity
	build := domain.NewBuild(appUUID, source)
	build.SourceURL = req.SourceURL
	build.GitRef = req.GitRef
	if req.DockerfilePath != "" {
		build.DockerfilePath = req.DockerfilePath
	}
	build.BuildArgs = req.BuildArgs

	// For gzip builds, we expect the source in a follow-up upload
	// For now, create the build record and return the ID
	
	// Generate WebSocket URL for log streaming
	wsURL := fmt.Sprintf("/ws/builds/%s/logs", build.ID.String())

	response := BuildResponse{
		ID:           build.ID.String(),
		AppID:        appID,
		Status:       string(build.Status),
		Source:       string(build.Source),
		CreatedAt:    build.CreatedAt.Format("2006-01-02T15:04:05Z"),
		WebSocketURL: wsURL,
	}

	h.logger.Info("Build created",
		zap.String("build_id", build.ID.String()),
		zap.String("app_id", appID),
		zap.String("source", req.Source),
	)

	writeJSON(w, http.StatusCreated, response)
}

// Upload handles source code upload for gzip builds
func (h *BuildHandler) Upload(w http.ResponseWriter, r *http.Request) {
	buildID := chi.URLParam(r, "buildId")
	if buildID == "" {
		writeError(w, http.StatusBadRequest, "Build ID is required")
		return
	}

	buildUUID, err := uuid.Parse(buildID)
	if err != nil {
		writeError(w, http.StatusBadRequest, "Invalid build ID format")
		return
	}

	// Limit upload size to 100MB
	r.Body = http.MaxBytesReader(w, r.Body, 100*1024*1024)

	// Read multipart form
	if err := r.ParseMultipartForm(100 << 20); err != nil {
		writeError(w, http.StatusBadRequest, "Failed to parse multipart form")
		return
	}

	file, _, err := r.FormFile("source")
	if err != nil {
		writeError(w, http.StatusBadRequest, "Source file is required")
		return
	}
	defer file.Close()

	appSlug := r.FormValue("app_slug")
	if appSlug == "" {
		appSlug = "app"
	}

	// Create build entity (in production, retrieve from database)
	appUUID := uuid.New() // Placeholder
	build := domain.NewBuild(appUUID, domain.BuildSourceGzip)
	build.ID = buildUUID

	// Create result channel
	resultChan := make(chan builder.BuildResult, 1)

	// Create log callback that broadcasts to WebSocket
	logTopic := fmt.Sprintf("build:%s", buildID)
	logCallback := func(msg string) {
		h.wsHub.BroadcastString(logTopic, "log", msg)
	}

	// Submit build job
	job := &builder.BuildJob{
		Build:       build,
		AppSlug:     appSlug,
		SourceData:  file,
		ResultChan:  resultChan,
		LogCallback: logCallback,
	}

	if err := h.builder.SubmitBuild(job); err != nil {
		writeError(w, http.StatusServiceUnavailable, "Build queue is full")
		return
	}

	h.logger.Info("Build source uploaded",
		zap.String("build_id", buildID),
		zap.String("app_slug", appSlug),
	)

	writeJSON(w, http.StatusAccepted, map[string]string{
		"message":       "Build started",
		"build_id":      buildID,
		"websocket_url": fmt.Sprintf("/ws/builds/%s/logs", buildID),
	})
}

// Get returns build status
func (h *BuildHandler) Get(w http.ResponseWriter, r *http.Request) {
	buildID := chi.URLParam(r, "buildId")
	if buildID == "" {
		writeError(w, http.StatusBadRequest, "Build ID is required")
		return
	}

	buildUUID, err := uuid.Parse(buildID)
	if err != nil {
		writeError(w, http.StatusBadRequest, "Invalid build ID format")
		return
	}

	// Check active builds first
	build, found := h.builder.GetBuildStatus(buildUUID)
	if !found {
		// In production, check database
		writeError(w, http.StatusNotFound, "Build not found")
		return
	}

	response := BuildResponse{
		ID:        build.ID.String(),
		AppID:     build.AppID.String(),
		Status:    string(build.Status),
		Source:    string(build.Source),
		ImageTag:  build.ImageTag,
		ImageID:   build.ImageID,
		CreatedAt: build.CreatedAt.Format("2006-01-02T15:04:05Z"),
	}

	if build.StartedAt != nil {
		response.StartedAt = build.StartedAt.Format("2006-01-02T15:04:05Z")
	}
	if build.CompletedAt != nil {
		response.CompletedAt = build.CompletedAt.Format("2006-01-02T15:04:05Z")
	}
	if build.Duration() > 0 {
		response.Duration = build.Duration().String()
	}
	if build.ErrorMessage != "" {
		response.Error = build.ErrorMessage
	}

	writeJSON(w, http.StatusOK, response)
}

// Cancel cancels a running build
func (h *BuildHandler) Cancel(w http.ResponseWriter, r *http.Request) {
	buildID := chi.URLParam(r, "buildId")
	if buildID == "" {
		writeError(w, http.StatusBadRequest, "Build ID is required")
		return
	}

	buildUUID, err := uuid.Parse(buildID)
	if err != nil {
		writeError(w, http.StatusBadRequest, "Invalid build ID format")
		return
	}

	if h.builder.CancelBuild(buildUUID) {
		h.logger.Info("Build cancelled", zap.String("build_id", buildID))
		writeJSON(w, http.StatusOK, map[string]string{
			"message": "Build cancelled",
		})
	} else {
		writeError(w, http.StatusNotFound, "Build not found or already completed")
	}
}

// StreamLogs handles WebSocket connection for log streaming
func (h *BuildHandler) StreamLogs(w http.ResponseWriter, r *http.Request) {
	buildID := chi.URLParam(r, "buildId")
	if buildID == "" {
		http.Error(w, "Build ID is required", http.StatusBadRequest)
		return
	}

	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		h.logger.Error("WebSocket upgrade failed", zap.Error(err))
		return
	}

	// Create WebSocket client
	client := ws.NewClient(h.wsHub, conn)
	h.wsHub.Register(client)

	// Subscribe to build logs
	logTopic := fmt.Sprintf("build:%s", buildID)
	h.wsHub.Subscribe(client, logTopic)

	h.logger.Debug("WebSocket client connected for build logs",
		zap.String("build_id", buildID),
		zap.String("client_id", client.ID.String()),
	)

	// Start pumps
	go client.WritePump()
	go client.ReadPump()
}

// Stats returns builder statistics
func (h *BuildHandler) Stats(w http.ResponseWriter, r *http.Request) {
	stats := map[string]interface{}{
		"active_builds": h.builder.ActiveBuildCount(),
		"queue_length":  h.builder.QueueLength(),
		"ws_clients":    h.wsHub.ClientCount(),
	}

	writeJSON(w, http.StatusOK, stats)
}

// StartBuildFromGit initiates a build from a Git repository
func (h *BuildHandler) StartBuildFromGit(w http.ResponseWriter, r *http.Request) {
	appID := chi.URLParam(r, "appId")
	if appID == "" {
		writeError(w, http.StatusBadRequest, "App ID is required")
		return
	}

	appUUID, err := uuid.Parse(appID)
	if err != nil {
		writeError(w, http.StatusBadRequest, "Invalid app ID format")
		return
	}

	var req struct {
		RepoURL string `json:"repo_url"`
		Branch  string `json:"branch"`
		AppSlug string `json:"app_slug"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	if req.RepoURL == "" {
		writeError(w, http.StatusBadRequest, "repo_url is required")
		return
	}

	if req.AppSlug == "" {
		req.AppSlug = "app"
	}

	// Create build entity
	build := domain.NewBuild(appUUID, domain.BuildSourceGit)
	build.SourceURL = req.RepoURL
	build.GitRef = req.Branch

	// Create result channel
	resultChan := make(chan builder.BuildResult, 1)

	// Create log callback
	logTopic := fmt.Sprintf("build:%s", build.ID.String())
	logCallback := func(msg string) {
		h.wsHub.BroadcastString(logTopic, "log", msg)
	}

	// Submit build job
	job := &builder.BuildJob{
		Build:       build,
		AppSlug:     req.AppSlug,
		SourceURL:   req.RepoURL,
		ResultChan:  resultChan,
		LogCallback: logCallback,
		OnSuccess: func(imageID, imageTag string) {
			if h.appUpdater != nil {
				h.appUpdater.UpdateAppImage(appID, imageID, imageTag)
			}
		},
	}

	if err := h.builder.SubmitBuild(job); err != nil {
		writeError(w, http.StatusServiceUnavailable, "Build queue is full: "+err.Error())
		return
	}

	h.logger.Info("Git build started",
		zap.String("build_id", build.ID.String()),
		zap.String("repo", req.RepoURL),
		zap.String("branch", req.Branch),
	)

	// Wait for result (with timeout) or return immediately
	// For async, we return immediately
	response := BuildResponse{
		ID:           build.ID.String(),
		AppID:        appID,
		Status:       string(build.Status),
		Source:       string(build.Source),
		CreatedAt:    build.CreatedAt.Format("2006-01-02T15:04:05Z"),
		WebSocketURL: fmt.Sprintf("/ws/builds/%s/logs", build.ID.String()),
	}

	writeJSON(w, http.StatusAccepted, response)
}

// HealthCheck placeholder for builder health
func (h *BuildHandler) HealthCheck(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"status":        "ok",
		"active_builds": h.builder.ActiveBuildCount(),
		"queue_length":  h.builder.QueueLength(),
	})
}

// Helper to read request body as reader (for streaming uploads)
func getBodyReader(r *http.Request) io.Reader {
	return r.Body
}
