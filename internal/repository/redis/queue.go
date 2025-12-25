package redis

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
)

// Client wraps the Redis client with application-specific operations
type Client struct {
	rdb    *redis.Client
	logger *zap.Logger
}

// BuildLogEntry represents a log entry for a build
type BuildLogEntry struct {
	Timestamp time.Time `json:"timestamp"`
	Level     string    `json:"level"`
	Message   string    `json:"message"`
}

// QueuedJob represents a job in the build queue
type QueuedJob struct {
	ID        uuid.UUID              `json:"id"`
	Type      string                 `json:"type"`
	Payload   map[string]interface{} `json:"payload"`
	Priority  int                    `json:"priority"`
	CreatedAt time.Time              `json:"created_at"`
}

// NewClient creates a new Redis client
func NewClient(host string, port int, password string, db int, logger *zap.Logger) (*Client, error) {
	rdb := redis.NewClient(&redis.Options{
		Addr:     fmt.Sprintf("%s:%d", host, port),
		Password: password,
		DB:       db,
	})

	// Test connection
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := rdb.Ping(ctx).Err(); err != nil {
		return nil, fmt.Errorf("failed to connect to Redis: %w", err)
	}

	logger.Info("Connected to Redis", zap.String("addr", fmt.Sprintf("%s:%d", host, port)))

	return &Client{
		rdb:    rdb,
		logger: logger,
	}, nil
}

// Close closes the Redis connection
func (c *Client) Close() error {
	return c.rdb.Close()
}

// Ping checks the Redis connection
func (c *Client) Ping(ctx context.Context) error {
	return c.rdb.Ping(ctx).Err()
}

// --- Build Logs ---

// AppendBuildLog appends a log entry to a build's log stream
func (c *Client) AppendBuildLog(ctx context.Context, buildID uuid.UUID, level, message string) error {
	key := fmt.Sprintf("build:logs:%s", buildID.String())

	entry := BuildLogEntry{
		Timestamp: time.Now().UTC(),
		Level:     level,
		Message:   message,
	}

	data, err := json.Marshal(entry)
	if err != nil {
		return fmt.Errorf("failed to marshal log entry: %w", err)
	}

	// Append to list
	if err := c.rdb.RPush(ctx, key, data).Err(); err != nil {
		return fmt.Errorf("failed to append log: %w", err)
	}

	// Publish to subscribers
	pubKey := fmt.Sprintf("build:logs:stream:%s", buildID.String())
	c.rdb.Publish(ctx, pubKey, data)

	return nil
}

// GetBuildLogs retrieves all logs for a build
func (c *Client) GetBuildLogs(ctx context.Context, buildID uuid.UUID, start, stop int64) ([]BuildLogEntry, error) {
	key := fmt.Sprintf("build:logs:%s", buildID.String())

	results, err := c.rdb.LRange(ctx, key, start, stop).Result()
	if err != nil {
		return nil, fmt.Errorf("failed to get logs: %w", err)
	}

	entries := make([]BuildLogEntry, 0, len(results))
	for _, data := range results {
		var entry BuildLogEntry
		if err := json.Unmarshal([]byte(data), &entry); err != nil {
			c.logger.Warn("Failed to unmarshal log entry", zap.Error(err))
			continue
		}
		entries = append(entries, entry)
	}

	return entries, nil
}

// SubscribeBuildLogs subscribes to real-time build logs
func (c *Client) SubscribeBuildLogs(ctx context.Context, buildID uuid.UUID) <-chan string {
	pubKey := fmt.Sprintf("build:logs:stream:%s", buildID.String())
	pubsub := c.rdb.Subscribe(ctx, pubKey)

	ch := make(chan string, 100)

	go func() {
		defer close(ch)
		defer pubsub.Close()

		for {
			select {
			case <-ctx.Done():
				return
			case msg := <-pubsub.Channel():
				select {
				case ch <- msg.Payload:
				default:
					// Drop message if channel is full
				}
			}
		}
	}()

	return ch
}

// DeleteBuildLogs deletes logs for a build
func (c *Client) DeleteBuildLogs(ctx context.Context, buildID uuid.UUID) error {
	key := fmt.Sprintf("build:logs:%s", buildID.String())
	return c.rdb.Del(ctx, key).Err()
}

// SetBuildLogsExpiry sets expiry on build logs
func (c *Client) SetBuildLogsExpiry(ctx context.Context, buildID uuid.UUID, expiry time.Duration) error {
	key := fmt.Sprintf("build:logs:%s", buildID.String())
	return c.rdb.Expire(ctx, key, expiry).Err()
}

// --- Build Queue ---

// EnqueueBuild adds a build job to the queue
func (c *Client) EnqueueBuild(ctx context.Context, job QueuedJob) error {
	data, err := json.Marshal(job)
	if err != nil {
		return fmt.Errorf("failed to marshal job: %w", err)
	}

	// Use sorted set with priority as score
	score := float64(job.Priority)*1e12 + float64(job.CreatedAt.UnixNano())
	if err := c.rdb.ZAdd(ctx, "build:queue", redis.Z{
		Score:  score,
		Member: data,
	}).Err(); err != nil {
		return fmt.Errorf("failed to enqueue job: %w", err)
	}

	c.logger.Debug("Job enqueued", zap.String("job_id", job.ID.String()))
	return nil
}

// DequeueBuild removes and returns the next build job from the queue
func (c *Client) DequeueBuild(ctx context.Context) (*QueuedJob, error) {
	// Pop the lowest score (highest priority, oldest)
	results, err := c.rdb.ZPopMin(ctx, "build:queue", 1).Result()
	if err != nil {
		return nil, fmt.Errorf("failed to dequeue job: %w", err)
	}

	if len(results) == 0 {
		return nil, nil // Queue is empty
	}

	var job QueuedJob
	if err := json.Unmarshal([]byte(results[0].Member.(string)), &job); err != nil {
		return nil, fmt.Errorf("failed to unmarshal job: %w", err)
	}

	return &job, nil
}

// QueueLength returns the number of jobs in the build queue
func (c *Client) QueueLength(ctx context.Context) (int64, error) {
	return c.rdb.ZCard(ctx, "build:queue").Result()
}

// --- Deployment Events ---

// PublishDeploymentEvent publishes a deployment event
func (c *Client) PublishDeploymentEvent(ctx context.Context, appID uuid.UUID, event string, data interface{}) error {
	channel := fmt.Sprintf("deployment:events:%s", appID.String())

	payload := map[string]interface{}{
		"event":     event,
		"data":      data,
		"timestamp": time.Now().UTC(),
	}

	jsonData, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal event: %w", err)
	}

	return c.rdb.Publish(ctx, channel, jsonData).Err()
}

// SubscribeDeploymentEvents subscribes to deployment events for an app
func (c *Client) SubscribeDeploymentEvents(ctx context.Context, appID uuid.UUID) <-chan string {
	channel := fmt.Sprintf("deployment:events:%s", appID.String())
	pubsub := c.rdb.Subscribe(ctx, channel)

	ch := make(chan string, 100)

	go func() {
		defer close(ch)
		defer pubsub.Close()

		for {
			select {
			case <-ctx.Done():
				return
			case msg := <-pubsub.Channel():
				select {
				case ch <- msg.Payload:
				default:
				}
			}
		}
	}()

	return ch
}

// --- Distributed Locking ---

// AcquireLock attempts to acquire a distributed lock
func (c *Client) AcquireLock(ctx context.Context, key string, ttl time.Duration) (bool, error) {
	lockKey := fmt.Sprintf("lock:%s", key)
	return c.rdb.SetNX(ctx, lockKey, time.Now().UTC().String(), ttl).Result()
}

// ReleaseLock releases a distributed lock
func (c *Client) ReleaseLock(ctx context.Context, key string) error {
	lockKey := fmt.Sprintf("lock:%s", key)
	return c.rdb.Del(ctx, lockKey).Err()
}

// --- Rate Limiting ---

// CheckRateLimit checks if an action is rate limited
func (c *Client) CheckRateLimit(ctx context.Context, key string, limit int, window time.Duration) (bool, error) {
	rateLimitKey := fmt.Sprintf("ratelimit:%s", key)

	pipe := c.rdb.Pipeline()
	incr := pipe.Incr(ctx, rateLimitKey)
	pipe.Expire(ctx, rateLimitKey, window)
	_, err := pipe.Exec(ctx)
	if err != nil {
		return false, fmt.Errorf("rate limit check failed: %w", err)
	}

	count := incr.Val()
	return count <= int64(limit), nil
}

// --- Caching ---

// SetCache sets a value in cache with expiration
func (c *Client) SetCache(ctx context.Context, key string, value interface{}, expiry time.Duration) error {
	data, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("failed to marshal cache value: %w", err)
	}

	cacheKey := fmt.Sprintf("cache:%s", key)
	return c.rdb.Set(ctx, cacheKey, data, expiry).Err()
}

// GetCache retrieves a value from cache
func (c *Client) GetCache(ctx context.Context, key string, dest interface{}) error {
	cacheKey := fmt.Sprintf("cache:%s", key)
	data, err := c.rdb.Get(ctx, cacheKey).Result()
	if err != nil {
		if err == redis.Nil {
			return fmt.Errorf("cache miss")
		}
		return fmt.Errorf("cache get failed: %w", err)
	}

	return json.Unmarshal([]byte(data), dest)
}

// DeleteCache deletes a value from cache
func (c *Client) DeleteCache(ctx context.Context, key string) error {
	cacheKey := fmt.Sprintf("cache:%s", key)
	return c.rdb.Del(ctx, cacheKey).Err()
}
