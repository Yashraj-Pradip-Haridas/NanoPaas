package builder

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/nanopaas/nanopaas/internal/domain"
	"github.com/nanopaas/nanopaas/internal/infrastructure/docker"
)

// BuilderConfig holds configuration for the builder service
type BuilderConfig struct {
	WorkerCount     int
	WorkDir         string
	MaxBuildTime    time.Duration
	CleanupOnFinish bool
}

// DefaultBuilderConfig returns default configuration
func DefaultBuilderConfig() BuilderConfig {
	return BuilderConfig{
		WorkerCount:     4,
		WorkDir:         os.TempDir(),
		MaxBuildTime:    15 * time.Minute,
		CleanupOnFinish: true,
	}
}

// BuildJob represents a build job in the queue
type BuildJob struct {
	Build       *domain.Build
	AppSlug     string
	SourceData  io.Reader // For gzip source
	SourceURL   string    // For git/url source
	ResultChan  chan BuildResult
	LogCallback func(string)
	OnSuccess   func(imageID, imageTag string) // Called when build succeeds
}

// BuildResult holds the result of a build
type BuildResult struct {
	BuildID  uuid.UUID
	ImageID  string
	ImageTag string
	Error    error
	Duration time.Duration
}

// Builder is the main build service that manages build workers
type Builder struct {
	config       BuilderConfig
	dockerClient *docker.Client
	logger       *zap.Logger

	jobQueue    chan *BuildJob
	workerWg    sync.WaitGroup
	ctx         context.Context
	cancel      context.CancelFunc

	// Active builds tracking
	activeBuilds   map[uuid.UUID]*BuildJob
	activeBuildsMu sync.RWMutex
}

// NewBuilder creates a new Builder service
func NewBuilder(config BuilderConfig, dockerClient *docker.Client, logger *zap.Logger) *Builder {
	ctx, cancel := context.WithCancel(context.Background())

	b := &Builder{
		config:       config,
		dockerClient: dockerClient,
		logger:       logger,
		jobQueue:     make(chan *BuildJob, 100),
		ctx:          ctx,
		cancel:       cancel,
		activeBuilds: make(map[uuid.UUID]*BuildJob),
	}

	// Start workers
	for i := 0; i < config.WorkerCount; i++ {
		b.workerWg.Add(1)
		go b.worker(i)
	}

	logger.Info("Builder service started",
		zap.Int("workers", config.WorkerCount),
		zap.String("work_dir", config.WorkDir),
	)

	return b
}

// SubmitBuild submits a new build job to the queue
func (b *Builder) SubmitBuild(job *BuildJob) error {
	if job.Build == nil {
		return fmt.Errorf("build cannot be nil")
	}

	if job.ResultChan == nil {
		job.ResultChan = make(chan BuildResult, 1)
	}

	// Track active build
	b.activeBuildsMu.Lock()
	b.activeBuilds[job.Build.ID] = job
	b.activeBuildsMu.Unlock()

	// Submit to queue
	select {
	case b.jobQueue <- job:
		b.logger.Info("Build job submitted",
			zap.String("build_id", job.Build.ID.String()),
			zap.String("app", job.AppSlug),
		)
		return nil
	case <-b.ctx.Done():
		return fmt.Errorf("builder is shutting down")
	default:
		return fmt.Errorf("build queue is full")
	}
}

// GetBuildStatus returns the status of an active build
func (b *Builder) GetBuildStatus(buildID uuid.UUID) (*domain.Build, bool) {
	b.activeBuildsMu.RLock()
	defer b.activeBuildsMu.RUnlock()

	job, exists := b.activeBuilds[buildID]
	if !exists {
		return nil, false
	}
	return job.Build, true
}

// CancelBuild attempts to cancel a running build
func (b *Builder) CancelBuild(buildID uuid.UUID) bool {
	b.activeBuildsMu.Lock()
	defer b.activeBuildsMu.Unlock()

	job, exists := b.activeBuilds[buildID]
	if !exists {
		return false
	}

	job.Build.Cancel()
	delete(b.activeBuilds, buildID)
	return true
}

// worker is the build worker goroutine
func (b *Builder) worker(id int) {
	defer b.workerWg.Done()

	b.logger.Debug("Build worker started", zap.Int("worker_id", id))

	for {
		select {
		case job := <-b.jobQueue:
			b.processJob(id, job)
		case <-b.ctx.Done():
			b.logger.Debug("Build worker stopping", zap.Int("worker_id", id))
			return
		}
	}
}

// processJob processes a single build job
func (b *Builder) processJob(workerID int, job *BuildJob) {
	startTime := time.Now()
	build := job.Build

	b.logger.Info("Processing build",
		zap.Int("worker", workerID),
		zap.String("build_id", build.ID.String()),
		zap.String("source", string(build.Source)),
	)

	// Mark build as running
	build.Start()

	// Create build context with timeout
	ctx, cancel := context.WithTimeout(b.ctx, b.config.MaxBuildTime)
	defer cancel()

	// Log callback helper
	log := func(msg string) {
		if job.LogCallback != nil {
			job.LogCallback(msg)
		}
		b.logger.Debug("Build log", zap.String("build_id", build.ID.String()), zap.String("msg", msg))
	}

	log(fmt.Sprintf("[NanoPaaS] Build %s started\n", build.ID.String()[:8]))

	// Prepare build directory
	buildDir, err := b.prepareBuildDir(job, log)
	if err != nil {
		b.finishBuild(job, "", "", err, time.Since(startTime))
		return
	}

	if b.config.CleanupOnFinish {
		defer os.RemoveAll(buildDir)
	}

	// Detect Dockerfile
	dockerfilePath, err := b.detectDockerfile(buildDir, log)
	if err != nil {
		b.finishBuild(job, "", "", err, time.Since(startTime))
		return
	}

	// Generate image tag
	imageTag := build.GenerateImageTag(job.AppSlug)
	log(fmt.Sprintf("[NanoPaaS] Building image: %s\n", imageTag))

	// Build the image
	imageID, err := b.buildImage(ctx, buildDir, dockerfilePath, imageTag, job.LogCallback)
	if err != nil {
		b.finishBuild(job, "", "", err, time.Since(startTime))
		return
	}

	log(fmt.Sprintf("[NanoPaaS] Build completed successfully in %s\n", time.Since(startTime)))
	b.finishBuild(job, imageID, imageTag, nil, time.Since(startTime))
}

// prepareBuildDir prepares the build directory from the source
func (b *Builder) prepareBuildDir(job *BuildJob, log func(string)) (string, error) {
	// Create unique build directory
	buildDir := filepath.Join(b.config.WorkDir, "nanopaas-build-"+job.Build.ID.String()[:8])
	if err := os.MkdirAll(buildDir, 0755); err != nil {
		return "", fmt.Errorf("failed to create build directory: %w", err)
	}

	switch job.Build.Source {
	case domain.BuildSourceGzip:
		log("[NanoPaaS] Extracting gzipped source...\n")
		if err := b.extractGzip(job.SourceData, buildDir); err != nil {
			return "", fmt.Errorf("failed to extract source: %w", err)
		}

	case domain.BuildSourceGit:
		log(fmt.Sprintf("[NanoPaaS] Cloning repository: %s\n", job.SourceURL))
		if err := b.cloneGitRepo(job.SourceURL, job.Build.GitRef, buildDir); err != nil {
			return "", fmt.Errorf("failed to clone repository: %w", err)
		}

	case domain.BuildSourceURL:
		log(fmt.Sprintf("[NanoPaaS] Downloading source from: %s\n", job.SourceURL))
		if err := b.downloadSource(job.SourceURL, buildDir); err != nil {
			return "", fmt.Errorf("failed to download source: %w", err)
		}

	default:
		return "", fmt.Errorf("unsupported source type: %s", job.Build.Source)
	}

	return buildDir, nil
}

// extractGzip extracts a gzipped tar archive to the destination
func (b *Builder) extractGzip(reader io.Reader, destDir string) error {
	gzr, err := gzip.NewReader(reader)
	if err != nil {
		return fmt.Errorf("failed to create gzip reader: %w", err)
	}
	defer gzr.Close()

	tr := tar.NewReader(gzr)

	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("error reading tar: %w", err)
		}

		// Prevent path traversal attacks
		target := filepath.Join(destDir, header.Name)
		if !strings.HasPrefix(target, filepath.Clean(destDir)+string(os.PathSeparator)) {
			return fmt.Errorf("invalid file path: %s", header.Name)
		}

		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0755); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
				return err
			}
			f, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, os.FileMode(header.Mode))
			if err != nil {
				return err
			}
			if _, err := io.Copy(f, tr); err != nil {
				f.Close()
				return err
			}
			f.Close()
		}
	}

	return nil
}

// cloneGitRepo clones a git repository
func (b *Builder) cloneGitRepo(url, ref, destDir string) error {
	args := []string{"clone", "--depth", "1"}
	if ref != "" {
		args = append(args, "--branch", ref)
	}
	args = append(args, url, destDir)

	cmd := exec.Command("git", args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git clone failed: %s: %w", string(output), err)
	}

	return nil
}

// downloadSource downloads source from a URL (placeholder for HTTP download + extraction)
func (b *Builder) downloadSource(url, destDir string) error {
	// This would download and extract from URL
	// For now, return not implemented
	return fmt.Errorf("URL source download not yet implemented")
}

// detectDockerfile finds the Dockerfile in the build directory
func (b *Builder) detectDockerfile(buildDir string, log func(string)) (string, error) {
	// Check for Dockerfile in common locations
	candidates := []string{
		"Dockerfile",
		"dockerfile",
		"Dockerfile.prod",
		"Dockerfile.production",
	}

	for _, candidate := range candidates {
		path := filepath.Join(buildDir, candidate)
		if _, err := os.Stat(path); err == nil {
			log(fmt.Sprintf("[NanoPaaS] Found Dockerfile: %s\n", candidate))
			return candidate, nil
		}
	}

	// Check if there's a buildpack.toml for CNB
	buildpackPath := filepath.Join(buildDir, "project.toml")
	if _, err := os.Stat(buildpackPath); err == nil {
		log("[NanoPaaS] Found project.toml - Cloud Native Buildpacks could be used\n")
		// For now, we'll generate a simple Dockerfile
	}

	// Try to auto-detect and generate Dockerfile
	dockerfile, err := b.generateDockerfile(buildDir, log)
	if err != nil {
		return "", fmt.Errorf("no Dockerfile found and auto-detection failed: %w", err)
	}

	// Write generated Dockerfile
	dockerfilePath := filepath.Join(buildDir, "Dockerfile")
	if err := os.WriteFile(dockerfilePath, []byte(dockerfile), 0644); err != nil {
		return "", fmt.Errorf("failed to write generated Dockerfile: %w", err)
	}

	log("[NanoPaaS] Generated Dockerfile based on project detection\n")
	return "Dockerfile", nil
}

// generateDockerfile attempts to auto-generate a Dockerfile based on project structure
func (b *Builder) generateDockerfile(buildDir string, log func(string)) (string, error) {
	// Check for Python
	if _, err := os.Stat(filepath.Join(buildDir, "requirements.txt")); err == nil {
		log("[NanoPaaS] Detected Python project\n")
		return b.generatePythonDockerfile(buildDir), nil
	}

	// Check for Node.js
	if _, err := os.Stat(filepath.Join(buildDir, "package.json")); err == nil {
		log("[NanoPaaS] Detected Node.js project\n")
		return b.generateNodeDockerfile(buildDir), nil
	}

	// Check for Go
	if _, err := os.Stat(filepath.Join(buildDir, "go.mod")); err == nil {
		log("[NanoPaaS] Detected Go project\n")
		return b.generateGoDockerfile(buildDir), nil
	}

	// Check for Ruby
	if _, err := os.Stat(filepath.Join(buildDir, "Gemfile")); err == nil {
		log("[NanoPaaS] Detected Ruby project\n")
		return b.generateRubyDockerfile(buildDir), nil
	}

	return "", fmt.Errorf("unable to detect project type")
}

// generatePythonDockerfile generates a Dockerfile for Python projects
func (b *Builder) generatePythonDockerfile(buildDir string) string {
	return `FROM python:3.11-slim

WORKDIR /app

# Install dependencies
COPY requirements.txt .
RUN pip install --no-cache-dir -r requirements.txt

# Copy application
COPY . .

# Create non-root user
RUN useradd -m -u 1000 appuser && chown -R appuser:appuser /app
USER appuser

EXPOSE 8080

CMD ["python", "app.py"]
`
}

// generateNodeDockerfile generates a Dockerfile for Node.js projects
func (b *Builder) generateNodeDockerfile(buildDir string) string {
	return `FROM node:20-alpine

WORKDIR /app

# Install dependencies
COPY package*.json ./
RUN npm ci --only=production

# Copy application
COPY . .

# Create non-root user
RUN adduser -D -u 1000 appuser && chown -R appuser:appuser /app
USER appuser

EXPOSE 8080

CMD ["node", "index.js"]
`
}

// generateGoDockerfile generates a Dockerfile for Go projects
func (b *Builder) generateGoDockerfile(buildDir string) string {
	return `FROM golang:1.22-alpine AS builder

WORKDIR /app
COPY go.* ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o /app/main .

FROM alpine:latest
RUN apk --no-cache add ca-certificates
RUN adduser -D -u 1000 appuser

WORKDIR /app
COPY --from=builder /app/main .
RUN chown appuser:appuser /app/main

USER appuser
EXPOSE 8080
CMD ["./main"]
`
}

// generateRubyDockerfile generates a Dockerfile for Ruby projects
func (b *Builder) generateRubyDockerfile(buildDir string) string {
	return `FROM ruby:3.2-slim

WORKDIR /app

# Install dependencies
COPY Gemfile* ./
RUN bundle install --without development test

# Copy application
COPY . .

# Create non-root user
RUN useradd -m -u 1000 appuser && chown -R appuser:appuser /app
USER appuser

EXPOSE 8080

CMD ["ruby", "app.rb"]
`
}

// buildImage builds a Docker image from the build directory
func (b *Builder) buildImage(ctx context.Context, buildDir, dockerfilePath, imageTag string, logCallback func(string)) (string, error) {
	// Create tar archive of build context
	tarPath := buildDir + ".tar"
	if err := b.createTarArchive(buildDir, tarPath); err != nil {
		return "", fmt.Errorf("failed to create build context: %w", err)
	}
	defer os.Remove(tarPath)

	// Open tar file
	tarFile, err := os.Open(tarPath)
	if err != nil {
		return "", fmt.Errorf("failed to open build context: %w", err)
	}
	defer tarFile.Close()

	// Build options
	opts := docker.BuildOptions{
		Tags:           []string{imageTag},
		DockerfilePath: dockerfilePath,
		NoCache:        false,
		Pull:           true,
	}

	// Build with log streaming
	imageID, err := b.dockerClient.BuildImageWithLogs(ctx, tarFile, opts, logCallback)
	if err != nil {
		return "", fmt.Errorf("docker build failed: %w", err)
	}

	return imageID, nil
}

// createTarArchive creates a tar archive of a directory
func (b *Builder) createTarArchive(srcDir, destPath string) error {
	tarFile, err := os.Create(destPath)
	if err != nil {
		return err
	}
	defer tarFile.Close()

	tw := tar.NewWriter(tarFile)
	defer tw.Close()

	return filepath.Walk(srcDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		// Skip the tar file itself
		if path == destPath {
			return nil
		}

		// Create tar header
		header, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return err
		}

		// Use relative path
		relPath, err := filepath.Rel(srcDir, path)
		if err != nil {
			return err
		}
		header.Name = relPath

		if err := tw.WriteHeader(header); err != nil {
			return err
		}

		// Write file content
		if !info.IsDir() {
			file, err := os.Open(path)
			if err != nil {
				return err
			}
			defer file.Close()
			_, err = io.Copy(tw, file)
			return err
		}

		return nil
	})
}

// finishBuild completes a build and sends the result
func (b *Builder) finishBuild(job *BuildJob, imageID, imageTag string, err error, duration time.Duration) {
	build := job.Build

	if err != nil {
		build.Fail(err)
		b.logger.Error("Build failed",
			zap.String("build_id", build.ID.String()),
			zap.Error(err),
			zap.Duration("duration", duration),
		)
	} else {
		build.Succeed(imageID, imageTag)
		b.logger.Info("Build succeeded",
			zap.String("build_id", build.ID.String()),
			zap.String("image", imageTag),
			zap.Duration("duration", duration),
		)
		// Call OnSuccess callback if provided
		if job.OnSuccess != nil {
			go job.OnSuccess(imageID, imageTag)
		}
	}

	// Remove from active builds
	b.activeBuildsMu.Lock()
	delete(b.activeBuilds, build.ID)
	b.activeBuildsMu.Unlock()

	// Send result
	result := BuildResult{
		BuildID:  build.ID,
		ImageID:  imageID,
		ImageTag: imageTag,
		Error:    err,
		Duration: duration,
	}

	select {
	case job.ResultChan <- result:
	default:
		b.logger.Warn("Result channel full, dropping result",
			zap.String("build_id", build.ID.String()),
		)
	}
}

// Shutdown gracefully shuts down the builder
func (b *Builder) Shutdown() {
	b.logger.Info("Shutting down builder service...")
	b.cancel()
	b.workerWg.Wait()
	close(b.jobQueue)
	b.logger.Info("Builder service stopped")
}

// ActiveBuildCount returns the number of active builds
func (b *Builder) ActiveBuildCount() int {
	b.activeBuildsMu.RLock()
	defer b.activeBuildsMu.RUnlock()
	return len(b.activeBuilds)
}

// QueueLength returns the current queue length
func (b *Builder) QueueLength() int {
	return len(b.jobQueue)
}
