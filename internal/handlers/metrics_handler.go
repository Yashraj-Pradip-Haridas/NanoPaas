package handlers

import (
	"net/http"
	"runtime"
	"time"

	"go.uber.org/zap"

	"github.com/nanopaas/nanopaas/internal/infrastructure/docker"
	"github.com/nanopaas/nanopaas/internal/services/builder"
	"github.com/nanopaas/nanopaas/internal/services/orchestrator"
	ws "github.com/nanopaas/nanopaas/pkg/websocket"
)

// MetricsHandler handles Prometheus-compatible metrics endpoints
type MetricsHandler struct {
	dockerClient *docker.Client
	orchestrator *orchestrator.Orchestrator
	builder      *builder.Builder
	wsHub        *ws.Hub
	logger       *zap.Logger
	startTime    time.Time
}

// NewMetricsHandler creates a new metrics handler
func NewMetricsHandler(
	dockerClient *docker.Client,
	orchestrator *orchestrator.Orchestrator,
	builder *builder.Builder,
	wsHub *ws.Hub,
	logger *zap.Logger,
) *MetricsHandler {
	return &MetricsHandler{
		dockerClient: dockerClient,
		orchestrator: orchestrator,
		builder:      builder,
		wsHub:        wsHub,
		logger:       logger,
		startTime:    time.Now(),
	}
}

// Metrics returns Prometheus-compatible metrics
func (h *MetricsHandler) Metrics(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")

	// Collect metrics
	var m runtime.MemStats
	runtime.ReadMemStats(&m)

	uptime := time.Since(h.startTime).Seconds()
	activeBuilds := 0
	buildQueueLen := 0
	wsClients := 0
	deployments := 0

	if h.builder != nil {
		activeBuilds = h.builder.ActiveBuildCount()
		buildQueueLen = h.builder.QueueLength()
	}

	if h.wsHub != nil {
		wsClients = h.wsHub.ClientCount()
	}

	if h.orchestrator != nil {
		deployments = len(h.orchestrator.ListDeployments())
	}

	// Write Prometheus format metrics
	metrics := []struct {
		name  string
		help  string
		mtype string
		value interface{}
	}{
		{"nanopaas_uptime_seconds", "Time in seconds since the server started", "gauge", uptime},
		{"nanopaas_goroutines", "Number of active goroutines", "gauge", runtime.NumGoroutine()},
		{"nanopaas_memory_alloc_bytes", "Current memory allocation in bytes", "gauge", m.Alloc},
		{"nanopaas_memory_sys_bytes", "Total memory obtained from system", "gauge", m.Sys},
		{"nanopaas_gc_runs_total", "Total number of GC runs", "counter", m.NumGC},
		{"nanopaas_builds_active", "Number of active builds", "gauge", activeBuilds},
		{"nanopaas_builds_queue_length", "Number of builds in queue", "gauge", buildQueueLen},
		{"nanopaas_websocket_clients", "Number of connected WebSocket clients", "gauge", wsClients},
		{"nanopaas_deployments_active", "Number of active deployments", "gauge", deployments},
	}

	for _, metric := range metrics {
		// HELP line
		w.Write([]byte("# HELP " + metric.name + " " + metric.help + "\n"))
		// TYPE line
		w.Write([]byte("# TYPE " + metric.name + " " + metric.mtype + "\n"))
		// Value line
		switch v := metric.value.(type) {
		case int:
			w.Write([]byte(metric.name + " " + itoa(v) + "\n"))
		case int64:
			w.Write([]byte(metric.name + " " + itoa64(v) + "\n"))
		case uint32:
			w.Write([]byte(metric.name + " " + itoa(int(v)) + "\n"))
		case uint64:
			w.Write([]byte(metric.name + " " + itoa64(int64(v)) + "\n"))
		case float64:
			w.Write([]byte(metric.name + " " + ftoa(v) + "\n"))
		}
	}
}

// Stats returns JSON-formatted stats (for dashboard)
func (h *MetricsHandler) Stats(w http.ResponseWriter, r *http.Request) {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)

	activeBuilds := 0
	buildQueueLen := 0
	wsClients := 0
	deployments := 0

	if h.builder != nil {
		activeBuilds = h.builder.ActiveBuildCount()
		buildQueueLen = h.builder.QueueLength()
	}

	if h.wsHub != nil {
		wsClients = h.wsHub.ClientCount()
	}

	if h.orchestrator != nil {
		deployments = len(h.orchestrator.ListDeployments())
	}

	stats := map[string]interface{}{
		"uptime_seconds":    time.Since(h.startTime).Seconds(),
		"uptime_human":      time.Since(h.startTime).String(),
		"goroutines":        runtime.NumGoroutine(),
		"memory_alloc_mb":   float64(m.Alloc) / 1024 / 1024,
		"memory_sys_mb":     float64(m.Sys) / 1024 / 1024,
		"gc_runs":           m.NumGC,
		"builds_active":     activeBuilds,
		"builds_queued":     buildQueueLen,
		"websocket_clients": wsClients,
		"deployments":       deployments,
		"go_version":        runtime.Version(),
		"num_cpu":           runtime.NumCPU(),
	}

	writeJSON(w, http.StatusOK, stats)
}

// Helper functions for formatting
func itoa(i int) string {
	return itoa64(int64(i))
}

func itoa64(i int64) string {
	if i == 0 {
		return "0"
	}
	
	negative := i < 0
	if negative {
		i = -i
	}
	
	var buf [20]byte
	pos := len(buf)
	for i > 0 {
		pos--
		buf[pos] = byte('0' + i%10)
		i /= 10
	}
	
	if negative {
		pos--
		buf[pos] = '-'
	}
	
	return string(buf[pos:])
}

func ftoa(f float64) string {
	// Simple float to string with 2 decimal places
	if f == 0 {
		return "0"
	}
	
	negative := f < 0
	if negative {
		f = -f
	}
	
	intPart := int64(f)
	decPart := int64((f - float64(intPart)) * 100)
	
	result := itoa64(intPart) + "." + padLeft(itoa64(decPart), 2, '0')
	
	if negative {
		result = "-" + result
	}
	
	return result
}

func padLeft(s string, length int, pad byte) string {
	for len(s) < length {
		s = string(pad) + s
	}
	return s
}
