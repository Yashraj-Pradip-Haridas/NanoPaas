package docker

import (
	"context"
	"fmt"
	"io"
	"runtime"
	"sync"
	"time"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/client"
	"github.com/docker/docker/pkg/stdcopy"
	"github.com/docker/go-connections/nat"
	"go.uber.org/zap"
)

// Client wraps the Docker SDK client with high-level operations
type Client struct {
	cli             *client.Client
	logger          *zap.Logger
	containerPrefix string
	defaultNetwork  string
	mu              sync.RWMutex
}

// ContainerInfo holds information about a running container
type ContainerInfo struct {
	ID        string
	Name      string
	Image     string
	Status    string
	State     string
	Ports     map[string]string
	Labels    map[string]string
	CreatedAt time.Time
	IPAddress string
}

// BuildOptions holds options for building an image
type BuildOptions struct {
	ContextPath    string
	DockerfilePath string
	Tags           []string
	BuildArgs      map[string]*string
	NoCache        bool
	Pull           bool
}

// ContainerOptions holds options for creating a container
type ContainerOptions struct {
	Name         string
	Image        string
	Env          []string
	Labels       map[string]string
	ExposedPorts []string
	Memory       int64 // Memory limit in bytes
	CPUQuota     int64 // CPU quota in microseconds
	RestartPolicy string
	NetworkMode  string
	User         string
	ReadOnly     bool
	Privileged   bool
}

// NewClient creates a new Docker client wrapper
func NewClient(host, apiVersion, containerPrefix, defaultNetwork string, logger *zap.Logger) (*Client, error) {
	opts := []client.Opt{
		client.WithAPIVersionNegotiation(),
	}

	if host != "" {
		opts = append(opts, client.WithHost(host))
	} else {
		// Use default host based on OS
		if runtime.GOOS == "windows" {
			opts = append(opts, client.WithHost("npipe:////./pipe/docker_engine"))
		} else {
			opts = append(opts, client.WithHost("unix:///var/run/docker.sock"))
		}
	}

	if apiVersion != "" {
		opts = append(opts, client.WithVersion(apiVersion))
	}

	cli, err := client.NewClientWithOpts(opts...)
	if err != nil {
		return nil, fmt.Errorf("failed to create docker client: %w", err)
	}

	return &Client{
		cli:             cli,
		logger:          logger,
		containerPrefix: containerPrefix,
		defaultNetwork:  defaultNetwork,
	}, nil
}

// Ping checks if the Docker daemon is responsive
func (c *Client) Ping(ctx context.Context) error {
	_, err := c.cli.Ping(ctx)
	if err != nil {
		return fmt.Errorf("docker daemon not responding: %w", err)
	}
	return nil
}

// Info returns Docker system information
func (c *Client) Info(ctx context.Context) (types.Info, error) {
	info, err := c.cli.Info(ctx)
	if err != nil {
		return types.Info{}, fmt.Errorf("failed to get docker info: %w", err)
	}
	return info, nil
}

// ListContainers lists all containers matching the prefix
func (c *Client) ListContainers(ctx context.Context, all bool) ([]ContainerInfo, error) {
	filterArgs := filters.NewArgs()
	if c.containerPrefix != "" {
		filterArgs.Add("name", c.containerPrefix+"*")
	}

	containers, err := c.cli.ContainerList(ctx, container.ListOptions{
		All:     all,
		Filters: filterArgs,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to list containers: %w", err)
	}

	result := make([]ContainerInfo, 0, len(containers))
	for _, cont := range containers {
		ports := make(map[string]string)
		for _, p := range cont.Ports {
			if p.PublicPort > 0 {
				ports[fmt.Sprintf("%d/%s", p.PrivatePort, p.Type)] = fmt.Sprintf("%s:%d", p.IP, p.PublicPort)
			}
		}

		var ipAddress string
		if cont.NetworkSettings != nil {
			for _, netw := range cont.NetworkSettings.Networks {
				ipAddress = netw.IPAddress
				break
			}
		}

		name := ""
		if len(cont.Names) > 0 {
			name = cont.Names[0]
			if len(name) > 0 && name[0] == '/' {
				name = name[1:]
			}
		}

		result = append(result, ContainerInfo{
			ID:        cont.ID[:12],
			Name:      name,
			Image:     cont.Image,
			Status:    cont.Status,
			State:     cont.State,
			Ports:     ports,
			Labels:    cont.Labels,
			CreatedAt: time.Unix(cont.Created, 0),
			IPAddress: ipAddress,
		})
	}

	return result, nil
}

// CreateContainer creates a new container with the given options
func (c *Client) CreateContainer(ctx context.Context, opts ContainerOptions) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Build exposed ports and port bindings
	exposedPorts := nat.PortSet{}
	portBindings := nat.PortMap{}

	for _, port := range opts.ExposedPorts {
		natPort, err := nat.NewPort("tcp", port)
		if err != nil {
			return "", fmt.Errorf("invalid port %s: %w", port, err)
		}
		exposedPorts[natPort] = struct{}{}
		portBindings[natPort] = []nat.PortBinding{
			{HostIP: "0.0.0.0", HostPort: ""}, // Auto-assign host port
		}
	}

	// Set default labels
	if opts.Labels == nil {
		opts.Labels = make(map[string]string)
	}
	opts.Labels["managed-by"] = "nanopaas"
	opts.Labels["created-at"] = time.Now().UTC().Format(time.RFC3339)

	// Container configuration
	config := &container.Config{
		Image:        opts.Image,
		Env:          opts.Env,
		Labels:       opts.Labels,
		ExposedPorts: exposedPorts,
		User:         opts.User,
	}

	// Restart policy
	restartPolicy := container.RestartPolicy{}
	switch opts.RestartPolicy {
	case "always":
		restartPolicy = container.RestartPolicy{Name: "always"}
	case "on-failure":
		restartPolicy = container.RestartPolicy{Name: "on-failure", MaximumRetryCount: 5}
	case "unless-stopped":
		restartPolicy = container.RestartPolicy{Name: "unless-stopped"}
	default:
		restartPolicy = container.RestartPolicy{Name: "on-failure", MaximumRetryCount: 3}
	}

	// Host configuration with security constraints
	hostConfig := &container.HostConfig{
		PortBindings:  portBindings,
		RestartPolicy: restartPolicy,
		Resources: container.Resources{
			Memory:   opts.Memory,
			CPUQuota: opts.CPUQuota,
		},
		ReadonlyRootfs: opts.ReadOnly,
		Privileged:     opts.Privileged,
		SecurityOpt:    []string{"no-new-privileges:true"},
		CapDrop:        []string{"ALL"},
		CapAdd:         []string{"NET_BIND_SERVICE"},
	}

	// Network configuration
	networkConfig := &network.NetworkingConfig{}
	if opts.NetworkMode != "" {
		hostConfig.NetworkMode = container.NetworkMode(opts.NetworkMode)
	} else if c.defaultNetwork != "" {
		networkConfig.EndpointsConfig = map[string]*network.EndpointSettings{
			c.defaultNetwork: {},
		}
	}

	containerName := c.containerPrefix + opts.Name

	resp, err := c.cli.ContainerCreate(ctx, config, hostConfig, networkConfig, nil, containerName)
	if err != nil {
		return "", fmt.Errorf("failed to create container: %w", err)
	}

	c.logger.Info("Container created",
		zap.String("id", resp.ID[:12]),
		zap.String("name", containerName),
		zap.String("image", opts.Image),
	)

	return resp.ID, nil
}

// StartContainer starts a container by ID
func (c *Client) StartContainer(ctx context.Context, containerID string) error {
	if err := c.cli.ContainerStart(ctx, containerID, container.StartOptions{}); err != nil {
		return fmt.Errorf("failed to start container %s: %w", containerID[:12], err)
	}
	c.logger.Info("Container started", zap.String("id", containerID[:12]))
	return nil
}

// StopContainer stops a container gracefully
func (c *Client) StopContainer(ctx context.Context, containerID string, timeout *int) error {
	stopOptions := container.StopOptions{}
	if timeout != nil {
		stopOptions.Timeout = timeout
	}

	if err := c.cli.ContainerStop(ctx, containerID, stopOptions); err != nil {
		return fmt.Errorf("failed to stop container %s: %w", containerID[:12], err)
	}
	c.logger.Info("Container stopped", zap.String("id", containerID[:12]))
	return nil
}

// RestartContainer restarts a container
func (c *Client) RestartContainer(ctx context.Context, containerID string, timeout *int) error {
	stopOptions := container.StopOptions{}
	if timeout != nil {
		stopOptions.Timeout = timeout
	}

	if err := c.cli.ContainerRestart(ctx, containerID, stopOptions); err != nil {
		return fmt.Errorf("failed to restart container %s: %w", containerID[:12], err)
	}
	c.logger.Info("Container restarted", zap.String("id", containerID[:12]))
	return nil
}

// RemoveContainer removes a container
func (c *Client) RemoveContainer(ctx context.Context, containerID string, force bool) error {
	if err := c.cli.ContainerRemove(ctx, containerID, container.RemoveOptions{
		Force:         force,
		RemoveVolumes: true,
	}); err != nil {
		return fmt.Errorf("failed to remove container %s: %w", containerID[:12], err)
	}
	c.logger.Info("Container removed", zap.String("id", containerID[:12]))
	return nil
}

// InspectContainer returns detailed information about a container
func (c *Client) InspectContainer(ctx context.Context, containerID string) (types.ContainerJSON, error) {
	info, err := c.cli.ContainerInspect(ctx, containerID)
	if err != nil {
		return types.ContainerJSON{}, fmt.Errorf("failed to inspect container %s: %w", containerID[:12], err)
	}
	return info, nil
}

// GetContainerLogs streams container logs
func (c *Client) GetContainerLogs(ctx context.Context, containerID string, follow bool, tail string) (io.ReadCloser, error) {
	options := container.LogsOptions{
		ShowStdout: true,
		ShowStderr: true,
		Follow:     follow,
		Timestamps: true,
		Tail:       tail,
	}

	logs, err := c.cli.ContainerLogs(ctx, containerID, options)
	if err != nil {
		return nil, fmt.Errorf("failed to get logs for container %s: %w", containerID[:12], err)
	}
	return logs, nil
}

// StreamContainerLogs streams container logs to stdout and stderr writers
func (c *Client) StreamContainerLogs(ctx context.Context, containerID string, stdout, stderr io.Writer) error {
	logs, err := c.GetContainerLogs(ctx, containerID, true, "100")
	if err != nil {
		return err
	}
	defer logs.Close()

	_, err = stdcopy.StdCopy(stdout, stderr, logs)
	return err
}

// BuildImage builds a Docker image from a build context
func (c *Client) BuildImage(ctx context.Context, buildContext io.Reader, opts BuildOptions) (string, error) {
	buildOptions := types.ImageBuildOptions{
		Tags:       opts.Tags,
		Dockerfile: opts.DockerfilePath,
		BuildArgs:  opts.BuildArgs,
		NoCache:    opts.NoCache,
		PullParent: opts.Pull,
		Remove:     true,
		Labels: map[string]string{
			"built-by": "nanopaas",
			"built-at": time.Now().UTC().Format(time.RFC3339),
		},
	}

	resp, err := c.cli.ImageBuild(ctx, buildContext, buildOptions)
	if err != nil {
		return "", fmt.Errorf("failed to build image: %w", err)
	}
	defer resp.Body.Close()

	// Read and discard the build output (in production, stream to WebSocket)
	_, err = io.Copy(io.Discard, resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read build output: %w", err)
	}

	if len(opts.Tags) > 0 {
		c.logger.Info("Image built", zap.String("tag", opts.Tags[0]))
		return opts.Tags[0], nil
	}
	return "", nil
}

// BuildImageWithLogs builds an image and streams logs via a callback
func (c *Client) BuildImageWithLogs(ctx context.Context, buildContext io.Reader, opts BuildOptions, logCallback func(string)) (string, error) {
	buildOptions := types.ImageBuildOptions{
		Tags:       opts.Tags,
		Dockerfile: opts.DockerfilePath,
		BuildArgs:  opts.BuildArgs,
		NoCache:    opts.NoCache,
		PullParent: opts.Pull,
		Remove:     true,
		Labels: map[string]string{
			"built-by": "nanopaas",
			"built-at": time.Now().UTC().Format(time.RFC3339),
		},
	}

	resp, err := c.cli.ImageBuild(ctx, buildContext, buildOptions)
	if err != nil {
		return "", fmt.Errorf("failed to build image: %w", err)
	}
	defer resp.Body.Close()

	// Stream build output line by line
	buf := make([]byte, 4096)
	for {
		n, readErr := resp.Body.Read(buf)
		if n > 0 && logCallback != nil {
			logCallback(string(buf[:n]))
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return "", fmt.Errorf("error reading build output: %w", readErr)
		}
	}

	if len(opts.Tags) > 0 {
		c.logger.Info("Image built successfully", zap.String("tag", opts.Tags[0]))
		return opts.Tags[0], nil
	}
	return "", nil
}

// PullImage pulls an image from a registry
func (c *Client) PullImage(ctx context.Context, imageName string) error {
	reader, err := c.cli.ImagePull(ctx, imageName, image.PullOptions{})
	if err != nil {
		return fmt.Errorf("failed to pull image %s: %w", imageName, err)
	}
	defer reader.Close()

	// Consume the output
	_, err = io.Copy(io.Discard, reader)
	if err != nil {
		return fmt.Errorf("error reading pull output: %w", err)
	}

	c.logger.Info("Image pulled", zap.String("image", imageName))
	return nil
}

// RemoveImage removes an image
func (c *Client) RemoveImage(ctx context.Context, imageID string, force bool) error {
	_, err := c.cli.ImageRemove(ctx, imageID, image.RemoveOptions{
		Force:         force,
		PruneChildren: true,
	})
	if err != nil {
		return fmt.Errorf("failed to remove image %s: %w", imageID, err)
	}
	c.logger.Info("Image removed", zap.String("image", imageID))
	return nil
}

// ListImages lists all NanoPaaS-managed images
func (c *Client) ListImages(ctx context.Context) ([]image.Summary, error) {
	filterArgs := filters.NewArgs()
	filterArgs.Add("label", "built-by=nanopaas")

	images, err := c.cli.ImageList(ctx, image.ListOptions{
		All:     false,
		Filters: filterArgs,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to list images: %w", err)
	}
	return images, nil
}

// EnsureNetwork creates the default network if it doesn't exist
func (c *Client) EnsureNetwork(ctx context.Context) error {
	if c.defaultNetwork == "" {
		return nil
	}

	networks, err := c.cli.NetworkList(ctx, network.ListOptions{
		Filters: filters.NewArgs(filters.Arg("name", c.defaultNetwork)),
	})
	if err != nil {
		return fmt.Errorf("failed to list networks: %w", err)
	}

	if len(networks) > 0 {
		c.logger.Debug("Network already exists", zap.String("network", c.defaultNetwork))
		return nil
	}

	_, err = c.cli.NetworkCreate(ctx, c.defaultNetwork, network.CreateOptions{
		Driver: "bridge",
		Labels: map[string]string{
			"managed-by": "nanopaas",
		},
	})
	if err != nil {
		return fmt.Errorf("failed to create network: %w", err)
	}

	c.logger.Info("Network created", zap.String("network", c.defaultNetwork))
	return nil
}

// WaitForContainer waits for a container to reach a certain state
func (c *Client) WaitForContainer(ctx context.Context, containerID string, condition container.WaitCondition) error {
	statusCh, errCh := c.cli.ContainerWait(ctx, containerID, condition)
	select {
	case err := <-errCh:
		return fmt.Errorf("error waiting for container: %w", err)
	case status := <-statusCh:
		if status.StatusCode != 0 {
			return fmt.Errorf("container exited with status %d", status.StatusCode)
		}
	case <-ctx.Done():
		return ctx.Err()
	}
	return nil
}

// HealthCheck checks if a container is healthy
func (c *Client) HealthCheck(ctx context.Context, containerID string) (bool, error) {
	info, err := c.InspectContainer(ctx, containerID)
	if err != nil {
		return false, err
	}

	if info.State == nil {
		return false, nil
	}

	if info.State.Health != nil {
		return info.State.Health.Status == "healthy", nil
	}

	return info.State.Running, nil
}

// Close closes the Docker client
func (c *Client) Close() error {
	return c.cli.Close()
}
