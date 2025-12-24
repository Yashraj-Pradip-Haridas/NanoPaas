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
	"go.uber.org/zap"

	"github.com/nanopaas/nanopaas/internal/config"
	"github.com/nanopaas/nanopaas/internal/handlers"
	"github.com/nanopaas/nanopaas/internal/infrastructure/docker"
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

	// Initialize router
	r := chi.NewRouter()

	// Middleware
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	r.Use(middleware.Timeout(60 * time.Second))

	// CORS middleware
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Access-Control-Allow-Origin", "*")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS, PATCH")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
			if r.Method == "OPTIONS" {
				w.WriteHeader(http.StatusOK)
				return
			}
			next.ServeHTTP(w, r)
		})
	})

	// Initialize handlers
	healthHandler := handlers.NewHealthHandler(dockerClient, logger)
	containerHandler := handlers.NewContainerHandler(dockerClient, logger)

	// Health routes
	r.Get("/health", healthHandler.Health)
	r.Get("/health/docker", healthHandler.DockerHealth)
	r.Get("/ready", healthHandler.Ready)

	// API v1 routes
	r.Route("/api/v1", func(r chi.Router) {
		// Container management
		r.Route("/containers", func(r chi.Router) {
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

	// Graceful shutdown
	done := make(chan struct{})
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		<-quit
		logger.Info("Shutting down server...")

		ctx, cancel := context.WithTimeout(context.Background(), cfg.Server.ShutdownTimeout)
		defer cancel()

		if err := server.Shutdown(ctx); err != nil {
			logger.Error("Server shutdown error", zap.Error(err))
		}

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
