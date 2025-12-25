package router

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"text/template"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/nanopaas/nanopaas/internal/domain"
)

// RouterConfig holds router configuration
type RouterConfig struct {
	Domain          string
	ConfigPath      string
	HTTPPort        int
	HTTPSPort       int
	EnableHTTPS     bool
	CertResolver    string
	EntryPoints     []string
	RefreshInterval time.Duration
}

// DefaultRouterConfig returns default router configuration
func DefaultRouterConfig() RouterConfig {
	return RouterConfig{
		Domain:          "localhost",
		ConfigPath:      "./traefik/dynamic",
		HTTPPort:        80,
		HTTPSPort:       443,
		EnableHTTPS:     false,
		CertResolver:    "letsencrypt",
		EntryPoints:     []string{"web"},
		RefreshInterval: 5 * time.Second,
	}
}

// Route represents a routing rule for an app
type Route struct {
	AppID       uuid.UUID
	AppSlug     string
	Subdomain   string
	ServiceName string
	Port        int
	Replicas    []Replica
	EnableHTTPS bool
	Headers     map[string]string
	Middleware  []string
}

// Replica represents a backend replica
type Replica struct {
	ContainerID string
	IPAddress   string
	Port        int
	Weight      int
}

// TraefikRouter manages Traefik dynamic configuration
type TraefikRouter struct {
	config RouterConfig
	logger *zap.Logger

	// Active routes
	routes   map[uuid.UUID]*Route
	routesMu sync.RWMutex

	// File watcher context
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// NewTraefikRouter creates a new Traefik router
func NewTraefikRouter(config RouterConfig, logger *zap.Logger) (*TraefikRouter, error) {
	// Ensure config directory exists
	if err := os.MkdirAll(config.ConfigPath, 0755); err != nil {
		return nil, fmt.Errorf("failed to create config directory: %w", err)
	}

	ctx, cancel := context.WithCancel(context.Background())

	r := &TraefikRouter{
		config: config,
		logger: logger,
		routes: make(map[uuid.UUID]*Route),
		ctx:    ctx,
		cancel: cancel,
	}

	logger.Info("Traefik router initialized",
		zap.String("domain", config.Domain),
		zap.String("config_path", config.ConfigPath),
	)

	return r, nil
}

// AddRoute adds or updates a route for an app
func (r *TraefikRouter) AddRoute(ctx context.Context, app *domain.App, replicas []Replica) error {
	route := &Route{
		AppID:       app.ID,
		AppSlug:     app.Slug,
		Subdomain:   app.Subdomain,
		ServiceName: app.Slug,
		Port:        app.ExposedPort,
		Replicas:    replicas,
		EnableHTTPS: r.config.EnableHTTPS,
		Headers: map[string]string{
			"X-NanoPaaS-App": app.Slug,
		},
		Middleware: []string{},
	}

	r.routesMu.Lock()
	r.routes[app.ID] = route
	r.routesMu.Unlock()

	// Generate and write config
	if err := r.generateConfig(); err != nil {
		return fmt.Errorf("failed to generate config: %w", err)
	}

	r.logger.Info("Route added",
		zap.String("app_id", app.ID.String()),
		zap.String("subdomain", app.Subdomain+"."+r.config.Domain),
		zap.Int("replicas", len(replicas)),
	)

	return nil
}

// RemoveRoute removes a route for an app
func (r *TraefikRouter) RemoveRoute(ctx context.Context, appID uuid.UUID) error {
	r.routesMu.Lock()
	delete(r.routes, appID)
	r.routesMu.Unlock()

	// Regenerate config
	if err := r.generateConfig(); err != nil {
		return fmt.Errorf("failed to generate config: %w", err)
	}

	r.logger.Info("Route removed", zap.String("app_id", appID.String()))
	return nil
}

// UpdateReplicas updates the replicas for a route
func (r *TraefikRouter) UpdateReplicas(ctx context.Context, appID uuid.UUID, replicas []Replica) error {
	r.routesMu.Lock()
	route, exists := r.routes[appID]
	if !exists {
		r.routesMu.Unlock()
		return fmt.Errorf("route not found for app %s", appID)
	}
	route.Replicas = replicas
	r.routesMu.Unlock()

	// Regenerate config
	if err := r.generateConfig(); err != nil {
		return fmt.Errorf("failed to generate config: %w", err)
	}

	r.logger.Debug("Replicas updated",
		zap.String("app_id", appID.String()),
		zap.Int("count", len(replicas)),
	)

	return nil
}

// GetRoute returns a route by app ID
func (r *TraefikRouter) GetRoute(appID uuid.UUID) (*Route, bool) {
	r.routesMu.RLock()
	defer r.routesMu.RUnlock()
	route, exists := r.routes[appID]
	return route, exists
}

// ListRoutes returns all active routes
func (r *TraefikRouter) ListRoutes() []*Route {
	r.routesMu.RLock()
	defer r.routesMu.RUnlock()

	routes := make([]*Route, 0, len(r.routes))
	for _, route := range r.routes {
		routes = append(routes, route)
	}
	return routes
}

// generateConfig generates the Traefik dynamic configuration file
func (r *TraefikRouter) generateConfig() error {
	r.routesMu.RLock()
	routes := make([]*Route, 0, len(r.routes))
	for _, route := range r.routes {
		routes = append(routes, route)
	}
	r.routesMu.RUnlock()

	// Write to file
	configPath := filepath.Join(r.config.ConfigPath, "dynamic.yml")

	// Generate YAML config
	yamlConfig := r.convertToYAML(routes)

	if err := os.WriteFile(configPath, []byte(yamlConfig), 0644); err != nil {
		return fmt.Errorf("failed to write config: %w", err)
	}

	r.logger.Debug("Config generated", zap.String("path", configPath))
	return nil
}

// buildTraefikConfig builds the Traefik configuration structure
func (r *TraefikRouter) buildTraefikConfig(routes []*Route) map[string]interface{} {
	routers := make(map[string]interface{})
	services := make(map[string]interface{})
	middlewares := make(map[string]interface{})

	for _, route := range routes {
		// Router
		routerName := route.AppSlug + "-router"
		routeRule := fmt.Sprintf("Host(`%s.%s`)", route.Subdomain, r.config.Domain)

		router := map[string]interface{}{
			"rule":        routeRule,
			"service":     route.ServiceName,
			"entryPoints": r.config.EntryPoints,
		}

		if route.EnableHTTPS && r.config.CertResolver != "" {
			router["tls"] = map[string]interface{}{
				"certResolver": r.config.CertResolver,
			}
		}

		if len(route.Middleware) > 0 {
			router["middlewares"] = route.Middleware
		}

		routers[routerName] = router

		// Service with load balancer
		servers := make([]map[string]interface{}, 0, len(route.Replicas))
		for _, replica := range route.Replicas {
			servers = append(servers, map[string]interface{}{
				"url": fmt.Sprintf("http://%s:%d", replica.IPAddress, replica.Port),
			})
		}

		services[route.ServiceName] = map[string]interface{}{
			"loadBalancer": map[string]interface{}{
				"servers": servers,
				"healthCheck": map[string]interface{}{
					"path":     "/health",
					"interval": "10s",
					"timeout":  "3s",
				},
			},
		}

		// Custom headers middleware
		middlewareName := route.AppSlug + "-headers"
		middlewares[middlewareName] = map[string]interface{}{
			"headers": map[string]interface{}{
				"customRequestHeaders":  route.Headers,
				"customResponseHeaders": map[string]string{
					"X-Powered-By": "NanoPaaS",
				},
			},
		}
	}

	return map[string]interface{}{
		"http": map[string]interface{}{
			"routers":     routers,
			"services":    services,
			"middlewares": middlewares,
		},
	}
}

// convertToYAML converts routes to YAML format
func (r *TraefikRouter) convertToYAML(routes []*Route) string {
	tmpl := `http:
  routers:
{{- range . }}
    {{ .AppSlug }}-router:
      rule: "Host(` + "`" + `{{ .Subdomain }}.{{ $.Domain }}` + "`" + `)"
      service: {{ .ServiceName }}
      entryPoints:
        - web
{{- if .EnableHTTPS }}
      tls:
        certResolver: letsencrypt
{{- end }}
{{- end }}

  services:
{{- range . }}
    {{ .ServiceName }}:
      loadBalancer:
        servers:
{{- range .Replicas }}
          - url: "http://{{ .IPAddress }}:{{ .Port }}"
{{- end }}
        healthCheck:
          path: /health
          interval: 10s
          timeout: 3s
{{- end }}

  middlewares:
{{- range . }}
    {{ .AppSlug }}-headers:
      headers:
        customRequestHeaders:
          X-NanoPaaS-App: "{{ .AppSlug }}"
        customResponseHeaders:
          X-Powered-By: "NanoPaaS"
{{- end }}
`

	t, err := template.New("traefik").Parse(tmpl)
	if err != nil {
		r.logger.Error("Failed to parse template", zap.Error(err))
		return ""
	}

	data := struct {
		Domain string
		Routes []*Route
	}{
		Domain: r.config.Domain,
		Routes: routes,
	}

	var result string
	// Simple approach - just build the YAML manually
	result = "http:\n"
	result += "  routers:\n"

	for _, route := range routes {
		result += fmt.Sprintf("    %s-router:\n", route.AppSlug)
		result += fmt.Sprintf("      rule: \"Host(`%s.%s`)\"\n", route.Subdomain, r.config.Domain)
		result += fmt.Sprintf("      service: %s\n", route.ServiceName)
		result += "      entryPoints:\n"
		result += "        - web\n"
		if route.EnableHTTPS {
			result += "      tls:\n"
			result += "        certResolver: letsencrypt\n"
		}
	}

	result += "\n  services:\n"
	for _, route := range routes {
		result += fmt.Sprintf("    %s:\n", route.ServiceName)
		result += "      loadBalancer:\n"
		result += "        servers:\n"
		for _, replica := range route.Replicas {
			result += fmt.Sprintf("          - url: \"http://%s:%d\"\n", replica.IPAddress, replica.Port)
		}
		result += "        healthCheck:\n"
		result += "          path: /health\n"
		result += "          interval: 10s\n"
		result += "          timeout: 3s\n"
	}

	result += "\n  middlewares:\n"
	for _, route := range routes {
		result += fmt.Sprintf("    %s-headers:\n", route.AppSlug)
		result += "      headers:\n"
		result += "        customRequestHeaders:\n"
		result += fmt.Sprintf("          X-NanoPaaS-App: \"%s\"\n", route.AppSlug)
		result += "        customResponseHeaders:\n"
		result += "          X-Powered-By: \"NanoPaaS\"\n"
	}

	_ = t // Template is defined but we use manual approach for simplicity
	_ = data

	return result
}

// GetAppURL returns the URL for an app
func (r *TraefikRouter) GetAppURL(app *domain.App) string {
	scheme := "http"
	port := r.config.HTTPPort

	if r.config.EnableHTTPS {
		scheme = "https"
		port = r.config.HTTPSPort
	}

	if port == 80 || port == 443 {
		return fmt.Sprintf("%s://%s.%s", scheme, app.Subdomain, r.config.Domain)
	}
	return fmt.Sprintf("%s://%s.%s:%d", scheme, app.Subdomain, r.config.Domain, port)
}

// GenerateTraefikStaticConfig generates the static Traefik configuration
func (r *TraefikRouter) GenerateTraefikStaticConfig() string {
	return fmt.Sprintf(`
api:
  dashboard: true
  insecure: true

entryPoints:
  web:
    address: ":%d"
  websecure:
    address: ":%d"

providers:
  file:
    directory: "%s"
    watch: true

log:
  level: INFO

accessLog: {}
`, r.config.HTTPPort, r.config.HTTPSPort, r.config.ConfigPath)
}

// Shutdown stops the router
func (r *TraefikRouter) Shutdown() {
	r.logger.Info("Shutting down router...")
	r.cancel()
	r.wg.Wait()
	r.logger.Info("Router stopped")
}
