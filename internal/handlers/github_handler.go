package handlers

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
	"go.uber.org/zap"
)

// Note: GitHubHandler and NewGitHubHandler are defined in auth_handler.go
// This file adds additional GitHub routes to the existing GitHubHandler

// ListBranches lists branches for a repository
func (h *GitHubHandler) ListBranches(w http.ResponseWriter, r *http.Request) {
	owner := chi.URLParam(r, "owner")
	repo := chi.URLParam(r, "repo")

	if owner == "" || repo == "" {
		writeError(w, http.StatusBadRequest, "Owner and repo are required")
		return
	}

	user := GetUserFromContext(r.Context())
	if user == nil || user.GitHubToken == "" {
		writeError(w, http.StatusUnauthorized, "GitHub access token required")
		return
	}

	branches, err := h.githubService.ListBranches(r.Context(), user.GitHubToken, owner, repo)
	if err != nil {
		h.logger.Error("Failed to list branches",
			zap.String("owner", owner),
			zap.String("repo", repo),
			zap.Error(err),
		)
		writeError(w, http.StatusInternalServerError, "Failed to list branches")
		return
	}

	writeJSON(w, http.StatusOK, branches)
}

// WebhookRequest represents a request to create a webhook
type WebhookRequest struct {
	Owner  string   `json:"owner"`
	Repo   string   `json:"repo"`
	Events []string `json:"events"`
	URL    string   `json:"url"`
}

// CreateWebhook creates a GitHub webhook
func (h *GitHubHandler) CreateWebhook(w http.ResponseWriter, r *http.Request) {
	var req WebhookRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	if req.Owner == "" || req.Repo == "" || req.URL == "" {
		writeError(w, http.StatusBadRequest, "Owner, repo, and URL are required")
		return
	}

	user := GetUserFromContext(r.Context())
	if user == nil || user.GitHubToken == "" {
		writeError(w, http.StatusUnauthorized, "GitHub access token required")
		return
	}

	err := h.githubService.CreateWebhook(r.Context(), user.GitHubToken, req.Owner, req.Repo, req.URL)
	if err != nil {
		h.logger.Error("Failed to create webhook",
			zap.String("owner", req.Owner),
			zap.String("repo", req.Repo),
			zap.Error(err),
		)
		writeError(w, http.StatusInternalServerError, "Failed to create webhook")
		return
	}

	h.logger.Info("Webhook created",
		zap.String("owner", req.Owner),
		zap.String("repo", req.Repo),
	)

	writeJSON(w, http.StatusCreated, map[string]string{
		"message": "Webhook created successfully",
	})
}

// DeleteWebhook deletes a GitHub webhook
func (h *GitHubHandler) DeleteWebhook(w http.ResponseWriter, r *http.Request) {
	owner := chi.URLParam(r, "owner")
	repo := chi.URLParam(r, "repo")
	webhookID := chi.URLParam(r, "webhookId")

	if owner == "" || repo == "" || webhookID == "" {
		writeError(w, http.StatusBadRequest, "Owner, repo, and webhookId are required")
		return
	}

	// Note: DeleteWebhook is not implemented in the GitHub service yet
	// This is a placeholder that returns success
	h.logger.Warn("DeleteWebhook called but not fully implemented",
		zap.String("owner", owner),
		zap.String("repo", repo),
		zap.String("webhook_id", webhookID),
	)

	writeJSON(w, http.StatusOK, map[string]string{"message": "Webhook deletion not implemented"})
}

// Ensure github_handler.go extends the GitHubHandler from auth_handler.go
var _ interface {
	ListBranches(w http.ResponseWriter, r *http.Request)
	CreateWebhook(w http.ResponseWriter, r *http.Request)
	DeleteWebhook(w http.ResponseWriter, r *http.Request)
} = (*GitHubHandler)(nil)
