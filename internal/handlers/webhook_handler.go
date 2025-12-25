package handlers

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/nanopaas/nanopaas/internal/domain"
	"github.com/nanopaas/nanopaas/internal/repository/postgres"
	"github.com/nanopaas/nanopaas/internal/services/builder"
)

// WebhookHandler handles GitHub webhook events
type WebhookHandler struct {
	appRepo     *postgres.AppRepository
	buildRepo   *postgres.BuildRepository
	builder     *builder.Builder
	webhookSecret string
	logger      *zap.Logger
}

// NewWebhookHandler creates a new webhook handler
func NewWebhookHandler(
	appRepo *postgres.AppRepository,
	buildRepo *postgres.BuildRepository,
	builder *builder.Builder,
	webhookSecret string,
	logger *zap.Logger,
) *WebhookHandler {
	return &WebhookHandler{
		appRepo:       appRepo,
		buildRepo:     buildRepo,
		builder:       builder,
		webhookSecret: webhookSecret,
		logger:        logger,
	}
}

// GitHubPushEvent represents a GitHub push webhook payload
type GitHubPushEvent struct {
	Ref        string `json:"ref"`
	Before     string `json:"before"`
	After      string `json:"after"`
	Repository struct {
		ID       int64  `json:"id"`
		Name     string `json:"name"`
		FullName string `json:"full_name"`
		CloneURL string `json:"clone_url"`
		SSHURL   string `json:"ssh_url"`
		Private  bool   `json:"private"`
	} `json:"repository"`
	Pusher struct {
		Name  string `json:"name"`
		Email string `json:"email"`
	} `json:"pusher"`
	Sender struct {
		ID    int64  `json:"id"`
		Login string `json:"login"`
	} `json:"sender"`
	Commits []struct {
		ID      string `json:"id"`
		Message string `json:"message"`
		Author  struct {
			Name  string `json:"name"`
			Email string `json:"email"`
		} `json:"author"`
	} `json:"commits"`
	HeadCommit struct {
		ID      string `json:"id"`
		Message string `json:"message"`
	} `json:"head_commit"`
}

// HandleGitHub handles incoming GitHub webhooks
func (h *WebhookHandler) HandleGitHub(w http.ResponseWriter, r *http.Request) {
	// Read body
	body, err := io.ReadAll(r.Body)
	if err != nil {
		h.logger.Error("Failed to read webhook body", zap.Error(err))
		writeError(w, http.StatusBadRequest, "Failed to read request body")
		return
	}

	// Verify signature
	if h.webhookSecret != "" {
		signature := r.Header.Get("X-Hub-Signature-256")
		if !h.verifySignature(body, signature) {
			h.logger.Warn("Invalid webhook signature")
			writeError(w, http.StatusUnauthorized, "Invalid signature")
			return
		}
	}

	// Get event type
	eventType := r.Header.Get("X-GitHub-Event")
	deliveryID := r.Header.Get("X-GitHub-Delivery")

	h.logger.Info("Received GitHub webhook",
		zap.String("event", eventType),
		zap.String("delivery_id", deliveryID),
	)

	switch eventType {
	case "push":
		h.handlePushEvent(w, body)
	case "pull_request":
		h.handlePullRequestEvent(w, body)
	case "ping":
		h.handlePingEvent(w, body)
	default:
		h.logger.Debug("Ignoring unsupported webhook event", zap.String("event", eventType))
		writeJSON(w, http.StatusOK, map[string]string{"message": "Event ignored"})
	}
}

// HandleGitHubForApp handles webhooks for a specific app
func (h *WebhookHandler) HandleGitHubForApp(w http.ResponseWriter, r *http.Request) {
	appID := chi.URLParam(r, "appId")
	appUUID, err := uuid.Parse(appID)
	if err != nil {
		writeError(w, http.StatusBadRequest, "Invalid app ID")
		return
	}

	// Read body
	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeError(w, http.StatusBadRequest, "Failed to read request body")
		return
	}

	// Verify signature
	if h.webhookSecret != "" {
		signature := r.Header.Get("X-Hub-Signature-256")
		if !h.verifySignature(body, signature) {
			writeError(w, http.StatusUnauthorized, "Invalid signature")
			return
		}
	}

	eventType := r.Header.Get("X-GitHub-Event")

	if eventType == "push" {
		var event GitHubPushEvent
		if err := json.Unmarshal(body, &event); err != nil {
			writeError(w, http.StatusBadRequest, "Invalid payload")
			return
		}

		// Get app
		app, err := h.appRepo.GetByID(r.Context(), appUUID)
		if err != nil || app == nil {
			writeError(w, http.StatusNotFound, "App not found")
			return
		}

		// Check if auto-deploy is enabled
		if !app.AutoDeploy {
			h.logger.Debug("Auto-deploy disabled for app", zap.String("app_id", appID))
			writeJSON(w, http.StatusOK, map[string]string{"message": "Auto-deploy disabled"})
			return
		}

		// Check branch
		branch := strings.TrimPrefix(event.Ref, "refs/heads/")
		if app.GitBranch != "" && app.GitBranch != branch {
			h.logger.Debug("Push to non-tracked branch",
				zap.String("pushed_branch", branch),
				zap.String("tracked_branch", app.GitBranch),
			)
			writeJSON(w, http.StatusOK, map[string]string{"message": "Branch not tracked"})
			return
		}

		// Trigger build
		build := domain.NewBuild(app.ID, domain.BuildSourceGit)
		build.SourceURL = event.Repository.CloneURL
		build.GitRef = branch

		if err := h.buildRepo.Create(r.Context(), build); err != nil {
			h.logger.Error("Failed to create build", zap.Error(err))
			writeError(w, http.StatusInternalServerError, "Failed to create build")
			return
		}

		// Submit to builder
		resultChan := make(chan builder.BuildResult, 1)
		job := &builder.BuildJob{
			Build:      build,
			AppSlug:    app.Slug,
			SourceURL:  event.Repository.CloneURL,
			ResultChan: resultChan,
		}

		if err := h.builder.SubmitBuild(job); err != nil {
			h.logger.Error("Failed to submit build", zap.Error(err))
			writeError(w, http.StatusServiceUnavailable, "Build queue full")
			return
		}

		h.logger.Info("Auto-deploy triggered",
			zap.String("app_id", appID),
			zap.String("build_id", build.ID.String()),
			zap.String("commit", event.HeadCommit.ID[:8]),
		)

		writeJSON(w, http.StatusAccepted, map[string]interface{}{
			"message":  "Build triggered",
			"build_id": build.ID.String(),
			"commit":   event.HeadCommit.ID,
		})
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"message": "Event processed"})
}

func (h *WebhookHandler) handlePushEvent(w http.ResponseWriter, body []byte) {
	var event GitHubPushEvent
	if err := json.Unmarshal(body, &event); err != nil {
		h.logger.Error("Failed to parse push event", zap.Error(err))
		writeError(w, http.StatusBadRequest, "Invalid payload")
		return
	}

	h.logger.Info("Push event received",
		zap.String("repo", event.Repository.FullName),
		zap.String("ref", event.Ref),
		zap.String("pusher", event.Pusher.Name),
	)

	// Find apps tracking this repository
	// In production, query database for apps with matching git_repo_url
	
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"message":    "Push event received",
		"repository": event.Repository.FullName,
		"ref":        event.Ref,
	})
}

func (h *WebhookHandler) handlePullRequestEvent(w http.ResponseWriter, body []byte) {
	var event struct {
		Action      string `json:"action"`
		Number      int    `json:"number"`
		PullRequest struct {
			Title string `json:"title"`
			Head  struct {
				Ref string `json:"ref"`
				SHA string `json:"sha"`
			} `json:"head"`
			Base struct {
				Ref string `json:"ref"`
			} `json:"base"`
		} `json:"pull_request"`
		Repository struct {
			FullName string `json:"full_name"`
			CloneURL string `json:"clone_url"`
		} `json:"repository"`
	}

	if err := json.Unmarshal(body, &event); err != nil {
		h.logger.Error("Failed to parse pull request event", zap.Error(err))
		writeError(w, http.StatusBadRequest, "Invalid payload")
		return
	}

	h.logger.Info("Pull request event received",
		zap.String("repo", event.Repository.FullName),
		zap.String("action", event.Action),
		zap.Int("pr_number", event.Number),
	)

	// For preview deployments, trigger on opened/synchronize actions
	if event.Action == "opened" || event.Action == "synchronize" {
		// TODO: Implement preview deployments
		h.logger.Debug("Could trigger preview deployment",
			zap.Int("pr_number", event.Number),
			zap.String("branch", event.PullRequest.Head.Ref),
		)
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"message":   "Pull request event received",
		"action":    event.Action,
		"pr_number": event.Number,
	})
}

func (h *WebhookHandler) handlePingEvent(w http.ResponseWriter, body []byte) {
	var event struct {
		Zen    string `json:"zen"`
		HookID int64  `json:"hook_id"`
	}

	if err := json.Unmarshal(body, &event); err != nil {
		h.logger.Error("Failed to parse ping event", zap.Error(err))
		writeError(w, http.StatusBadRequest, "Invalid payload")
		return
	}

	h.logger.Info("Ping event received",
		zap.Int64("hook_id", event.HookID),
		zap.String("zen", event.Zen),
	)

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"message": "pong",
		"zen":     event.Zen,
	})
}

func (h *WebhookHandler) verifySignature(payload []byte, signature string) bool {
	if signature == "" {
		return false
	}

	// Remove "sha256=" prefix
	signature = strings.TrimPrefix(signature, "sha256=")

	// Calculate expected signature
	mac := hmac.New(sha256.New, []byte(h.webhookSecret))
	mac.Write(payload)
	expectedMAC := hex.EncodeToString(mac.Sum(nil))

	return hmac.Equal([]byte(expectedMAC), []byte(signature))
}
