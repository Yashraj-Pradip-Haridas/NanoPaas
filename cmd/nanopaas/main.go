package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"

	"github.com/nanopaas/nanopaas/internal/config"
	"github.com/nanopaas/nanopaas/internal/handlers"
	"github.com/nanopaas/nanopaas/internal/infrastructure/docker"
	"github.com/nanopaas/nanopaas/internal/repository/postgres"
	"github.com/nanopaas/nanopaas/internal/services/auth"
	"github.com/nanopaas/nanopaas/internal/services/builder"
	"github.com/nanopaas/nanopaas/internal/services/github"
	"github.com/nanopaas/nanopaas/internal/services/orchestrator"
	"github.com/nanopaas/nanopaas/internal/services/router"
	ws "github.com/nanopaas/nanopaas/pkg/websocket"
)

func main() {
	// Initialize logger
	logger, err := zap.NewProduction()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to initialize logger: %v\n", err)
		os.Exit(1)
	}
	defer logger.Sync()

	// Load configuration
	cfg := config.Load()

	logger.Info("Starting NanoPaaS",
		zap.String("host", cfg.Server.Host),
		zap.Int("port", cfg.Server.Port),
	)

	// Initialize Docker client
	dockerClient, err := docker.NewClient(
		cfg.Docker.Host,
		cfg.Docker.APIVersion,
		cfg.Docker.ContainerPrefix,
		cfg.Docker.DefaultNetwork,
		logger,
	)
	if err != nil {
		logger.Fatal("Failed to create Docker client", zap.Error(err))
	}
	defer dockerClient.Close()

	// Verify Docker connection
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	if err := dockerClient.Ping(ctx); err != nil {
		cancel()
		logger.Fatal("Failed to connect to Docker daemon", zap.Error(err))
	}
	cancel()
	logger.Info("Connected to Docker daemon")

	// Ensure the NanoPaaS network exists
	ctx, cancel = context.WithTimeout(context.Background(), 30*time.Second)
	if err := dockerClient.EnsureNetwork(ctx); err != nil {
		cancel()
		logger.Warn("Failed to ensure Docker network", zap.Error(err))
	} else {
		logger.Info("Docker network ready", zap.String("network", cfg.Docker.DefaultNetwork))
	}
	cancel()

	// Initialize PostgreSQL connection pool
	dbURL := fmt.Sprintf("postgres://%s:%s@%s:%d/%s?sslmode=%s",
		cfg.Postgres.User,
		cfg.Postgres.Password,
		cfg.Postgres.Host,
		cfg.Postgres.Port,
		cfg.Postgres.Database,
		cfg.Postgres.SSLMode,
	)

	poolConfig, err := pgxpool.ParseConfig(dbURL)
	if err != nil {
		logger.Fatal("Failed to parse database config", zap.Error(err))
	}
	poolConfig.MaxConns = int32(cfg.Postgres.PoolSize)

	dbPool, err := pgxpool.NewWithConfig(context.Background(), poolConfig)
	if err != nil {
		logger.Fatal("Failed to create database pool", zap.Error(err))
	}
	defer dbPool.Close()

	// Verify database connection
	ctx, cancel = context.WithTimeout(context.Background(), 5*time.Second)
	if err := dbPool.Ping(ctx); err != nil {
		cancel()
		logger.Fatal("Failed to connect to database", zap.Error(err))
	}
	cancel()
	logger.Info("Connected to PostgreSQL")

	// Initialize repositories
	userRepo := postgres.NewUserRepository(dbPool, logger)
	// Note: App repository will be used when we switch to persistent storage
	// appRepo := postgres.NewAppRepository(dbPool, logger)

	// Initialize GitHub service
	githubService := github.NewService(github.Config{
		ClientID:      cfg.GitHub.ClientID,
		ClientSecret:  cfg.GitHub.ClientSecret,
		WebhookSecret: cfg.GitHub.WebhookSecret,
		RedirectURI:   cfg.GitHub.RedirectURI,
		Scopes:        cfg.GitHub.Scopes,
	}, logger)

	// Initialize auth service
	authService := auth.NewService(auth.Config{
		JWTSecret:        cfg.Auth.JWTSecret,
		JWTExpiry:        cfg.Auth.JWTExpiry,
		JWTRefreshExpiry: cfg.Auth.JWTRefreshExpiry,
	}, userRepo, logger)

	// Initialize orchestrator for container lifecycle management
	orch := orchestrator.NewOrchestrator(
		orchestrator.DefaultOrchestratorConfig(),
		dockerClient,
		logger,
	)
	defer orch.Shutdown()
	logger.Info("Orchestrator initialized")

	// Initialize builder service for Docker image builds
	builderService := builder.NewBuilder(
		builder.DefaultBuilderConfig(),
		dockerClient,
		logger,
	)
	defer builderService.Shutdown()
	logger.Info("Builder service initialized")

	// Initialize Traefik router for dynamic routing
	traefikRouter, err := router.NewTraefikRouter(router.RouterConfig{
		Domain:      cfg.Router.Domain,
		ConfigPath:  cfg.Router.ConfigPath,
		HTTPPort:    cfg.Router.HTTPPort,
		HTTPSPort:   cfg.Router.HTTPSPort,
		EnableHTTPS: cfg.Router.EnableHTTPS,
	}, logger)
	if err != nil {
		logger.Fatal("Failed to initialize Traefik router", zap.Error(err))
	}
	logger.Info("Traefik router initialized")

	// Initialize WebSocket hub for real-time log streaming
	wsHub := ws.NewHub(logger)
	go wsHub.Run()
	logger.Info("WebSocket hub initialized")

	// Initialize HTTP router
	r := chi.NewRouter()

	// Middleware
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	r.Use(middleware.Timeout(60 * time.Second))

	// CORS middleware with configurable origins
	r.Use(corsMiddleware(cfg.Auth.CORSOrigins))

	// Initialize repositories
	appRepo := postgres.NewAppRepository(dbPool, logger)
	buildRepo := postgres.NewBuildRepository(dbPool, logger)

	// Initialize handlers
	healthHandler := handlers.NewHealthHandler(dockerClient, logger)
	containerHandler := handlers.NewContainerHandler(dockerClient, logger)
	authHandler := handlers.NewAuthHandler(authService, githubService, cfg.Auth.FrontendURL, logger)
	githubHandler := handlers.NewGitHubHandler(githubService, logger)
	appHandler := handlers.NewAppHandler(orch, traefikRouter, logger)
	buildHandler := handlers.NewBuildHandler(builderService, wsHub, logger)
	buildHandler.SetAppUpdater(appHandler) // Connect build completion to app updates
	metricsHandler := handlers.NewMetricsHandler(dockerClient, orch, builderService, wsHub, logger)
	logHandler := handlers.NewLogHandler(dockerClient, wsHub, logger)
	webhookHandler := handlers.NewWebhookHandler(appRepo, buildRepo, builderService, cfg.GitHub.WebhookSecret, logger)

	// Health routes
	r.Get("/health", healthHandler.Health)
	r.Get("/health/docker", healthHandler.DockerHealth)
	r.Get("/ready", healthHandler.Ready)

	// Metrics routes (public for Prometheus scraping)
	r.Get("/metrics", metricsHandler.Metrics)
	r.Get("/api/v1/stats", metricsHandler.Stats)

	// Webhook routes (public with signature verification)
	r.Post("/webhooks/github", webhookHandler.HandleGitHub)
	r.Post("/api/v1/webhooks/github/{appId}", webhookHandler.HandleGitHubForApp)

	// WebSocket routes
	r.Get("/ws/apps/{appId}/logs", logHandler.StreamAppLogs)
	r.Get("/ws/containers/{containerId}/logs", logHandler.StreamContainerLogs)
	r.Get("/ws/builds/{buildId}/logs", logHandler.StreamBuildLogs)

	// API v1 routes
	r.Route("/api/v1", func(r chi.Router) {
		// Auth routes (public)
		r.Route("/auth", func(r chi.Router) {
			r.Get("/github", authHandler.GitHubLogin)
			r.Get("/github/callback", authHandler.GitHubCallback)
			r.Post("/refresh", authHandler.RefreshToken)
			r.Post("/logout", authHandler.Logout)

			// Protected auth routes
			r.Group(func(r chi.Router) {
				r.Use(handlers.AuthMiddleware(authService))
				r.Get("/me", authHandler.GetCurrentUser)
			})
		})

		// GitHub routes (protected)
		r.Route("/github", func(r chi.Router) {
			r.Use(handlers.AuthMiddleware(authService))
			r.Get("/repos", githubHandler.ListRepositories)
			r.Get("/repos/{owner}/{repo}", githubHandler.GetRepository)
			r.Get("/repos/{owner}/{repo}/branches", githubHandler.ListBranches)
			r.Post("/webhooks", githubHandler.CreateWebhook)
			r.Delete("/webhooks/{owner}/{repo}/{webhookId}", githubHandler.DeleteWebhook)
		})

		// Apps routes (protected)
		r.Route("/apps", func(r chi.Router) {
			r.Use(handlers.AuthMiddleware(authService))
			r.Get("/", appHandler.List)
			r.Post("/", appHandler.Create)
			r.Get("/{appId}", appHandler.Get)
			r.Put("/{appId}", appHandler.Update)
			r.Delete("/{appId}", appHandler.Delete)
			r.Post("/{appId}/deploy", appHandler.Deploy)
			r.Post("/{appId}/scale", appHandler.Scale)
			r.Post("/{appId}/restart", appHandler.Restart)
			r.Post("/{appId}/stop", appHandler.Stop)
			r.Put("/{appId}/env", appHandler.SetEnvVars)
			r.Delete("/{appId}/env/{key}", appHandler.DeleteEnvVar)
			r.Get("/{appId}/logs", logHandler.GetAppLogs)

			// Build routes within apps
			r.Post("/{appId}/builds", buildHandler.Create)
			r.Post("/{appId}/builds/git", buildHandler.StartBuildFromGit)
			r.Get("/{appId}/builds/{buildId}", buildHandler.Get)
			r.Post("/{appId}/builds/{buildId}/cancel", buildHandler.Cancel)
			r.Get("/{appId}/builds/{buildId}/logs", logHandler.GetBuildLogs)
		})

		// Container management (protected)
		r.Route("/containers", func(r chi.Router) {
			r.Use(handlers.AuthMiddleware(authService))
			r.Get("/", containerHandler.List)
			r.Post("/", containerHandler.Create)
			r.Get("/{id}", containerHandler.Get)
			r.Delete("/{id}", containerHandler.Delete)
			r.Post("/{id}/start", containerHandler.Start)
			r.Post("/{id}/stop", containerHandler.Stop)
			r.Post("/{id}/restart", containerHandler.Restart)
			r.Get("/{id}/logs", containerHandler.Logs)
		})
	})

	// Create server
	server := &http.Server{
		Addr:         fmt.Sprintf("%s:%d", cfg.Server.Host, cfg.Server.Port),
		Handler:      r,
		ReadTimeout:  cfg.Server.ReadTimeout,
		WriteTimeout: cfg.Server.WriteTimeout,
	}

	// Graceful shutdown with resource cleanup
	done := make(chan struct{})
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		<-quit
		logger.Info("Initiating graceful shutdown...")

		// Create shutdown context with timeout
		ctx, cancel := context.WithTimeout(context.Background(), cfg.Server.ShutdownTimeout)
		defer cancel()

		// 1. Stop accepting new HTTP requests
		if err := server.Shutdown(ctx); err != nil {
			logger.Error("HTTP server shutdown error", zap.Error(err))
		} else {
			logger.Info("HTTP server stopped gracefully")
		}

		// 2. Stop the builder service (wait for in-progress builds)
		logger.Info("Stopping builder service...")
		builderService.Stop()
		logger.Info("Builder service stopped")

		// 3. Stop WebSocket hub
		logger.Info("Stopping WebSocket hub...")
		wsHub.Stop()
		logger.Info("WebSocket hub stopped")

		// 4. Close database connection pool
		logger.Info("Closing database connections...")
		dbPool.Close()
		logger.Info("Database connections closed")

		// 5. Close Docker client
		logger.Info("Closing Docker client...")
		if err := dockerClient.Close(); err != nil {
			logger.Error("Docker client close error", zap.Error(err))
		} else {
			logger.Info("Docker client closed")
		}

		logger.Info("Graceful shutdown complete")
		close(done)
	}()

	// Start server
	logger.Info("Server listening", zap.String("addr", server.Addr))
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		logger.Fatal("Server failed", zap.Error(err))
	}

	<-done
	logger.Info("Server stopped")
}

// corsMiddleware creates a CORS middleware with the specified allowed origins
func corsMiddleware(allowedOrigins []string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin := r.Header.Get("Origin")

			// Check if origin is allowed
			allowed := false
			for _, o := range allowedOrigins {
				if o == "*" || o == origin {
					allowed = true
					break
				}
			}

			if allowed {
				w.Header().Set("Access-Control-Allow-Origin", origin)
			} else if len(allowedOrigins) > 0 {
				w.Header().Set("Access-Control-Allow-Origin", allowedOrigins[0])
			}

			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS, PATCH")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
			w.Header().Set("Access-Control-Allow-Credentials", "true")

			if r.Method == "OPTIONS" {
				w.WriteHeader(http.StatusOK)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}
