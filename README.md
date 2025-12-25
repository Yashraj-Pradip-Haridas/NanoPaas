# NanoPaaS - Lightweight PaaS Platform

A production-grade Platform as a Service (PaaS) that mimics Heroku's core functionality. Built with Go for high concurrency, Docker for container orchestration, and Traefik for dynamic routing.

## Features

- 🚀 **One-command deployments** from Git or source archives
- 📦 **Auto-Dockerfile generation** for Python, Node.js, Go, Ruby
- 🔄 **Horizontal scaling** with a single API call
- 🌐 **Dynamic subdomain routing** via Traefik
- 🔒 **Secure by default** - containers run with minimal privileges
- 📊 **Real-time build logs** via WebSocket
- ⚡ **Automatic rollback** on deployment failure

## Quick Start

### Prerequisites

- Docker & Docker Compose
- Go 1.22+ (for development)

### Run with Docker Compose

```bash
# Start all services
docker-compose up -d

# View logs
docker-compose logs -f nanopaas
```

### Deploy Your First App

```bash
# Create an app
curl -X POST http://localhost:8080/api/v1/apps \
  -H "Content-Type: application/json" \
  -d '{"name": "my-api", "slug": "my-api"}'

# Build from Git
curl -X POST http://localhost:8080/api/v1/apps/{app-id}/builds/git \
  -H "Content-Type: application/json" \
  -d '{"repo_url": "https://github.com/user/repo", "branch": "main"}'

# Deploy
curl -X POST http://localhost:8080/api/v1/apps/{app-id}/deploy \
  -H "Content-Type: application/json" \
  -d '{"image_id": "nanopaas/my-api:abc123", "replicas": 2}'

# Access your app
curl http://my-api.localhost
```

## API Reference

### Apps

| Endpoint | Method | Description |
|----------|--------|-------------|
| `/api/v1/apps` | GET | List all apps |
| `/api/v1/apps` | POST | Create app |
| `/api/v1/apps/{id}` | GET | Get app |
| `/api/v1/apps/{id}` | PUT | Update app |
| `/api/v1/apps/{id}` | DELETE | Delete app |
| `/api/v1/apps/{id}/deploy` | POST | Deploy app |
| `/api/v1/apps/{id}/scale` | POST | Scale app |
| `/api/v1/apps/{id}/restart` | POST | Restart app |
| `/api/v1/apps/{id}/stop` | POST | Stop app |
| `/api/v1/apps/{id}/env` | POST | Set env vars |

### Builds

| Endpoint | Method | Description |
|----------|--------|-------------|
| `/api/v1/apps/{id}/builds` | POST | Create build |
| `/api/v1/apps/{id}/builds/git` | POST | Build from Git |
| `/api/v1/builds/{id}` | GET | Get build status |
| `/api/v1/builds/{id}/upload` | POST | Upload source |
| `/api/v1/builds/{id}/cancel` | POST | Cancel build |
| `/ws/builds/{id}/logs` | WS | Stream build logs |

### Containers

| Endpoint | Method | Description |
|----------|--------|-------------|
| `/api/v1/containers` | GET | List containers |
| `/api/v1/containers/{id}` | GET | Get container |
| `/api/v1/containers/{id}/start` | POST | Start container |
| `/api/v1/containers/{id}/stop` | POST | Stop container |
| `/api/v1/containers/{id}/logs` | GET | Get logs |

## Architecture

```
┌─────────────────────────────────────────────────────────────┐
│                         Traefik                              │
│                    (Reverse Proxy :80)                       │
└─────────────────────────────────────────────────────────────┘
                              │
          ┌───────────────────┼───────────────────┐
          ▼                   ▼                   ▼
   ┌───────────┐       ┌───────────┐       ┌───────────┐
   │ app1.localhost │  │ app2.localhost │  │ app3.localhost │
   │ (Container) │     │ (Container) │     │ (Container) │
   └───────────┘       └───────────┘       └───────────┘
          ▲                   ▲                   ▲
          └───────────────────┼───────────────────┘
                              │
┌─────────────────────────────────────────────────────────────┐
│                      NanoPaaS API                            │
│  ┌─────────┐  ┌─────────────┐  ┌────────────┐  ┌─────────┐  │
│  │ Builder │  │ Orchestrator│  │   Router   │  │ Handler │  │
│  └─────────┘  └─────────────┘  └────────────┘  └─────────┘  │
└─────────────────────────────────────────────────────────────┘
          │                   │                   │
          ▼                   ▼                   ▼
   ┌───────────┐       ┌───────────┐       ┌───────────┐
   │   Redis   │       │ PostgreSQL│       │   Docker  │
   │  (Queue)  │       │  (State)  │       │  (Socket) │
   └───────────┘       └───────────┘       └───────────┘
```

## Configuration

Environment variables:

| Variable | Default | Description |
|----------|---------|-------------|
| `SERVER_PORT` | 8080 | API server port |
| `DOCKER_HOST` | auto | Docker socket path |
| `POSTGRES_HOST` | localhost | PostgreSQL host |
| `REDIS_HOST` | localhost | Redis host |
| `ROUTER_DOMAIN` | localhost | Base domain for apps |

## Development

```bash
# Install dependencies
go mod download

# Run locally
go run ./cmd/nanopaas

# Run tests
go test ./... -v

# Build binary
go build -o nanopaas ./cmd/nanopaas
```

## License

MIT
