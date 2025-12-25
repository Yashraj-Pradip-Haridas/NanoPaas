package config

import (
	"os"
	"strconv"
	"strings"
	"time"
)

// Config holds all configuration for NanoPaaS
type Config struct {
	Server   ServerConfig
	Docker   DockerConfig
	Postgres PostgresConfig
	Redis    RedisConfig
	Router   RouterConfig
	GitHub   GitHubConfig
	Auth     AuthConfig
}

// ServerConfig holds HTTP server configuration
type ServerConfig struct {
	Host            string
	Port            int
	ReadTimeout     time.Duration
	WriteTimeout    time.Duration
	ShutdownTimeout time.Duration
}

// DockerConfig holds Docker daemon configuration
type DockerConfig struct {
	Host            string
	APIVersion      string
	TLSVerify       bool
	CertPath        string
	RegistryAuth    string
	DefaultNetwork  string
	ContainerPrefix string
}

// PostgresConfig holds PostgreSQL configuration
type PostgresConfig struct {
	Host     string
	Port     int
	User     string
	Password string
	Database string
	SSLMode  string
	PoolSize int
}

// RedisConfig holds Redis configuration
type RedisConfig struct {
	Host     string
	Port     int
	Password string
	DB       int
}

// RouterConfig holds reverse proxy configuration
type RouterConfig struct {
	Domain      string
	TraefikAPI  string
	ConfigPath  string
	HTTPPort    int
	HTTPSPort   int
	EnableHTTPS bool
}

// GitHubConfig holds GitHub OAuth configuration
type GitHubConfig struct {
	ClientID      string
	ClientSecret  string
	WebhookSecret string
	RedirectURI   string
	Scopes        []string
}

// AuthConfig holds authentication configuration
type AuthConfig struct {
	JWTSecret        string
	JWTExpiry        time.Duration
	JWTRefreshExpiry time.Duration
	FrontendURL      string
	CORSOrigins      []string
}

// Load loads configuration from environment variables with defaults
func Load() *Config {
	return &Config{
		Server: ServerConfig{
			Host:            getEnv("SERVER_HOST", "0.0.0.0"),
			Port:            getEnvInt("SERVER_PORT", 8080),
			ReadTimeout:     getEnvDuration("SERVER_READ_TIMEOUT", 30*time.Second),
			WriteTimeout:    getEnvDuration("SERVER_WRITE_TIMEOUT", 30*time.Second),
			ShutdownTimeout: getEnvDuration("SERVER_SHUTDOWN_TIMEOUT", 15*time.Second),
		},
		Docker: DockerConfig{
			Host:            getEnv("DOCKER_HOST", ""),
			APIVersion:      getEnv("DOCKER_API_VERSION", "1.44"),
			TLSVerify:       getEnvBool("DOCKER_TLS_VERIFY", false),
			CertPath:        getEnv("DOCKER_CERT_PATH", ""),
			RegistryAuth:    getEnv("DOCKER_REGISTRY_AUTH", ""),
			DefaultNetwork:  getEnv("DOCKER_NETWORK", "nanopaas"),
			ContainerPrefix: getEnv("DOCKER_CONTAINER_PREFIX", "nanopaas-"),
		},
		Postgres: PostgresConfig{
			Host:     getEnv("POSTGRES_HOST", "localhost"),
			Port:     getEnvInt("POSTGRES_PORT", 5432),
			User:     getEnv("POSTGRES_USER", "nanopaas"),
			Password: getEnv("POSTGRES_PASSWORD", "nanopaas"),
			Database: getEnv("POSTGRES_DB", "nanopaas"),
			SSLMode:  getEnv("POSTGRES_SSL_MODE", "disable"),
			PoolSize: getEnvInt("POSTGRES_POOL_SIZE", 10),
		},
		Redis: RedisConfig{
			Host:     getEnv("REDIS_HOST", "localhost"),
			Port:     getEnvInt("REDIS_PORT", 6379),
			Password: getEnv("REDIS_PASSWORD", ""),
			DB:       getEnvInt("REDIS_DB", 0),
		},
		Router: RouterConfig{
			Domain:      getEnv("ROUTER_DOMAIN", "localhost"),
			TraefikAPI:  getEnv("TRAEFIK_API", "http://localhost:8081"),
			ConfigPath:  getEnv("TRAEFIK_CONFIG_PATH", "./traefik/dynamic"),
			HTTPPort:    getEnvInt("ROUTER_HTTP_PORT", 80),
			HTTPSPort:   getEnvInt("ROUTER_HTTPS_PORT", 443),
			EnableHTTPS: getEnvBool("ROUTER_ENABLE_HTTPS", false),
		},
		GitHub: GitHubConfig{
			ClientID:      getEnv("GITHUB_CLIENT_ID", ""),
			ClientSecret:  getEnv("GITHUB_CLIENT_SECRET", ""),
			WebhookSecret: getEnv("GITHUB_WEBHOOK_SECRET", ""),
			RedirectURI:   getEnv("GITHUB_REDIRECT_URI", "http://localhost:8080/api/v1/auth/github/callback"),
			Scopes:        []string{"user:email", "repo", "read:org"},
		},
		Auth: AuthConfig{
			JWTSecret:        getEnv("JWT_SECRET", "change-me-in-production"),
			JWTExpiry:        getEnvDuration("JWT_EXPIRY", 24*time.Hour),
			JWTRefreshExpiry: getEnvDuration("JWT_REFRESH_EXPIRY", 168*time.Hour),
			FrontendURL:      getEnv("FRONTEND_URL", "http://localhost:3000"),
			CORSOrigins:      getEnvSlice("CORS_ALLOWED_ORIGINS", []string{"http://localhost:3000", "http://localhost:8080"}),
		},
	}
}

func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

func getEnvInt(key string, defaultValue int) int {
	if value := os.Getenv(key); value != "" {
		if intValue, err := strconv.Atoi(value); err == nil {
			return intValue
		}
	}
	return defaultValue
}

func getEnvBool(key string, defaultValue bool) bool {
	if value := os.Getenv(key); value != "" {
		if boolValue, err := strconv.ParseBool(value); err == nil {
			return boolValue
		}
	}
	return defaultValue
}

func getEnvDuration(key string, defaultValue time.Duration) time.Duration {
	if value := os.Getenv(key); value != "" {
		if duration, err := time.ParseDuration(value); err == nil {
			return duration
		}
	}
	return defaultValue
}

func getEnvSlice(key string, defaultValue []string) []string {
	if value := os.Getenv(key); value != "" {
		parts := strings.Split(value, ",")
		result := make([]string, 0, len(parts))
		for _, part := range parts {
			trimmed := strings.TrimSpace(part)
			if trimmed != "" {
				result = append(result, trimmed)
			}
		}
		return result
	}
	return defaultValue
}
