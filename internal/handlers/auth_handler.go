package handlers

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"go.uber.org/zap"

	"github.com/nanopaas/nanopaas/internal/domain"
	"github.com/nanopaas/nanopaas/internal/services/auth"
	"github.com/nanopaas/nanopaas/internal/services/github"
)

// AuthHandler handles authentication endpoints
type AuthHandler struct {
	authService   *auth.Service
	githubService *github.Service
	frontendURL   string
	logger        *zap.Logger
}

// NewAuthHandler creates a new auth handler
func NewAuthHandler(authService *auth.Service, githubService *github.Service, frontendURL string, logger *zap.Logger) *AuthHandler {
	return &AuthHandler{
		authService:   authService,
		githubService: githubService,
		frontendURL:   frontendURL,
		logger:        logger,
	}
}

// GitHubLogin redirects to GitHub OAuth
func (h *AuthHandler) GitHubLogin(w http.ResponseWriter, r *http.Request) {
	// Generate state token for CSRF protection
	state := generateState()

	// Store state in cookie (in production, use secure cookie or session)
	http.SetCookie(w, &http.Cookie{
		Name:     "oauth_state",
		Value:    state,
		Path:     "/",
		MaxAge:   600, // 10 minutes
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})

	authURL := h.githubService.GetAuthURL(state)
	http.Redirect(w, r, authURL, http.StatusTemporaryRedirect)
}

// GitHubCallback handles the OAuth callback from GitHub
func (h *AuthHandler) GitHubCallback(w http.ResponseWriter, r *http.Request) {
	code := r.URL.Query().Get("code")
	state := r.URL.Query().Get("state")

	if code == "" {
		h.redirectWithError(w, r, "missing_code", "Authorization code not provided")
		return
	}

	// Verify state
	stateCookie, err := r.Cookie("oauth_state")
	if err != nil || stateCookie.Value != state {
		h.redirectWithError(w, r, "invalid_state", "Invalid state parameter")
		return
	}

	// Clear state cookie
	http.SetCookie(w, &http.Cookie{
		Name:   "oauth_state",
		Value:  "",
		Path:   "/",
		MaxAge: -1,
	})

	// Exchange code for token
	token, err := h.githubService.ExchangeCode(r.Context(), code)
	if err != nil {
		h.logger.Error("Failed to exchange code", zap.Error(err))
		h.redirectWithError(w, r, "exchange_failed", "Failed to authenticate with GitHub")
		return
	}

	// Get GitHub user
	ghUser, err := h.githubService.GetUser(r.Context(), token.AccessToken)
	if err != nil {
		h.logger.Error("Failed to get GitHub user", zap.Error(err))
		h.redirectWithError(w, r, "user_fetch_failed", "Failed to fetch user from GitHub")
		return
	}

	// Authenticate/create user
	user, tokens, err := h.authService.AuthenticateGitHub(
		r.Context(),
		ghUser.ID,
		ghUser.Login,
		ghUser.Email,
		ghUser.Name,
		ghUser.AvatarURL,
		token.AccessToken,
	)
	if err != nil {
		h.logger.Error("Failed to authenticate user", zap.Error(err))
		h.redirectWithError(w, r, "auth_failed", "Failed to authenticate")
		return
	}

	h.logger.Info("User authenticated via GitHub",
		zap.String("user_id", user.ID.String()),
		zap.String("github_login", ghUser.Login),
	)

	// Redirect to frontend with token
	redirectURL := h.frontendURL + "/auth/callback?access_token=" + tokens.AccessToken + "&refresh_token=" + tokens.RefreshToken
	http.Redirect(w, r, redirectURL, http.StatusTemporaryRedirect)
}

// RefreshToken refreshes the access token
func (h *AuthHandler) RefreshToken(w http.ResponseWriter, r *http.Request) {
	var req struct {
		RefreshToken string `json:"refresh_token"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	tokens, err := h.authService.RefreshTokens(r.Context(), req.RefreshToken)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "Invalid refresh token")
		return
	}

	writeJSON(w, http.StatusOK, tokens)
}

// GetCurrentUser returns the current authenticated user
func (h *AuthHandler) GetCurrentUser(w http.ResponseWriter, r *http.Request) {
	user := GetUserFromContext(r.Context())
	if user == nil {
		writeError(w, http.StatusUnauthorized, "Not authenticated")
		return
	}

	writeJSON(w, http.StatusOK, user)
}

// Logout logs out the user
func (h *AuthHandler) Logout(w http.ResponseWriter, r *http.Request) {
	// In a real implementation, you might want to:
	// - Invalidate the token in a blacklist
	// - Clear any server-side session

	writeJSON(w, http.StatusOK, map[string]string{
		"message": "Logged out successfully",
	})
}

// redirectWithError redirects to frontend with error
func (h *AuthHandler) redirectWithError(w http.ResponseWriter, r *http.Request, code, message string) {
	redirectURL := h.frontendURL + "/auth/error?error=" + code + "&message=" + message
	http.Redirect(w, r, redirectURL, http.StatusTemporaryRedirect)
}

// generateState generates a random state string
func generateState() string {
	bytes := make([]byte, 16)
	rand.Read(bytes)
	return hex.EncodeToString(bytes)
}

// AuthMiddleware validates JWT tokens
func AuthMiddleware(authService *auth.Service) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			authHeader := r.Header.Get("Authorization")
			if authHeader == "" {
				writeError(w, http.StatusUnauthorized, "Missing authorization header")
				return
			}

			parts := strings.Split(authHeader, " ")
			if len(parts) != 2 || strings.ToLower(parts[0]) != "bearer" {
				writeError(w, http.StatusUnauthorized, "Invalid authorization header format")
				return
			}

			user, err := authService.GetUserFromToken(r.Context(), parts[1])
			if err != nil {
				writeError(w, http.StatusUnauthorized, "Invalid or expired token")
				return
			}

			// Add user to context
			ctx := SetUserInContext(r.Context(), user)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// OptionalAuthMiddleware validates JWT tokens but doesn't require them
func OptionalAuthMiddleware(authService *auth.Service) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			authHeader := r.Header.Get("Authorization")
			if authHeader != "" {
				parts := strings.Split(authHeader, " ")
				if len(parts) == 2 && strings.ToLower(parts[0]) == "bearer" {
					user, err := authService.GetUserFromToken(r.Context(), parts[1])
					if err == nil {
						ctx := SetUserInContext(r.Context(), user)
						r = r.WithContext(ctx)
					}
				}
			}
			next.ServeHTTP(w, r)
		})
	}
}

// GitHubHandler handles GitHub-related endpoints
type GitHubHandler struct {
	githubService *github.Service
	logger        *zap.Logger
}

// NewGitHubHandler creates a new GitHub handler
func NewGitHubHandler(githubService *github.Service, logger *zap.Logger) *GitHubHandler {
	return &GitHubHandler{
		githubService: githubService,
		logger:        logger,
	}
}

// ListRepositories lists user's GitHub repositories
func (h *GitHubHandler) ListRepositories(w http.ResponseWriter, r *http.Request) {
	user := GetUserFromContext(r.Context())
	if user == nil || user.GitHubToken == "" {
		writeError(w, http.StatusUnauthorized, "GitHub not connected")
		return
	}

	page := 1
	perPage := 30

	repos, err := h.githubService.ListRepositories(r.Context(), user.GitHubToken, page, perPage)
	if err != nil {
		h.logger.Error("Failed to list repositories", zap.Error(err))
		writeError(w, http.StatusInternalServerError, "Failed to fetch repositories")
		return
	}

	writeJSON(w, http.StatusOK, repos)
}

// GetRepository gets a specific repository
func (h *GitHubHandler) GetRepository(w http.ResponseWriter, r *http.Request) {
	user := GetUserFromContext(r.Context())
	if user == nil || user.GitHubToken == "" {
		writeError(w, http.StatusUnauthorized, "GitHub not connected")
		return
	}

	owner := chi.URLParam(r, "owner")
	repo := chi.URLParam(r, "repo")

	repository, err := h.githubService.GetRepository(r.Context(), user.GitHubToken, owner, repo)
	if err != nil {
		h.logger.Error("Failed to get repository", zap.Error(err))
		writeError(w, http.StatusInternalServerError, "Failed to fetch repository")
		return
	}

	writeJSON(w, http.StatusOK, repository)
}

// Context helpers
type contextKey string

const userContextKey contextKey = "user"

// SetUserInContext adds user to context
func SetUserInContext(ctx context.Context, user *domain.User) context.Context {
	return context.WithValue(ctx, userContextKey, user)
}

// GetUserFromContext extracts user from context
func GetUserFromContext(ctx context.Context) *domain.User {
	user, ok := ctx.Value(userContextKey).(*domain.User)
	if !ok {
		return nil
	}
	return user
}
