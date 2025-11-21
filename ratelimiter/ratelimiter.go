package ratelimiter

import (
	"context"
	"fmt"
	"math"
	"strconv"
	"time"

	"github.com/go-redis/redis/v8"
)

// Config holds the configuration for the rate limiter.
type Config struct {
	RedisAddr      string
	RedisPassword  string
	MaxRetries     int
	BaseRetryDelay time.Duration
	Rate           float64 // Tokens per second (refill rate)
	Burst          float64 // Max tokens in bucket (bucket size)
}

// RateLimiter implements a distributed token bucket rate limiter using Redis.
type RateLimiter struct {
	client     *redis.Client
	bucketSize float64       // Max tokens in bucket
	refillRate float64       // Tokens added per second
	keyPrefix  string        // Prefix for Redis keys
	lockTTL    time.Duration // TTL for locks to prevent deadlocks
}

// NewRateLimiter creates a new distributed rate limiter from a Config.
func NewRateLimiter(ctx context.Context, cfg Config) (*RateLimiter, error) {
	client := redis.NewClient(&redis.Options{
		Addr:     cfg.RedisAddr,
		Password: cfg.RedisPassword,
		DB:       0,
	})

	// Test connection
	if err := client.Ping(ctx).Err(); err != nil {
		return nil, fmt.Errorf("failed to connect to Redis: %w", err)
	}

	return &RateLimiter{
		client:     client,
		bucketSize: cfg.Burst,
		refillRate: cfg.Rate,
		keyPrefix:  "ratelimiter:",
		lockTTL:    1 * time.Second,
	}, nil
}

// Close closes the Redis client connection.
func (rl *RateLimiter) Close() error {
	return rl.client.Close()
}

// Allow checks if a request is allowed for the given key.
func (rl *RateLimiter) Allow(ctx context.Context, key string, tokens float64) (bool, error) {
	dataKey := rl.keyPrefix + key
	lockKey := dataKey + ":lock"

	// Generate unique lock value to prevent accidentally deleting another client's lock
	lockValue := fmt.Sprintf("%d", time.Now().UnixNano())

	// Retry lock acquisition with exponential backoff
	maxRetries := 3
	retryDelay := 10 * time.Millisecond

	var locked bool
	var err error
	for i := 0; i < maxRetries; i++ {
		locked, err = rl.client.SetNX(ctx, lockKey, lockValue, rl.lockTTL).Result()
		if err != nil {
			return false, fmt.Errorf("failed to acquire lock: %w", err)
		}
		if locked {
			break
		}
		time.Sleep(retryDelay)
		retryDelay *= 2
	}

	if !locked {
		return false, fmt.Errorf("failed to acquire lock after retries")
	}

	// Release lock only if we still own it (using Lua script for atomicity)
	defer func() {
		script := `
            if redis.call("get", KEYS[1]) == ARGV[1] then
                return redis.call("del", KEYS[1])
            else
                return 0
            end
        `
		rl.client.Eval(ctx, script, []string{lockKey}, lockValue)
	}()

	// Fetch current state (tokens and last refill time as Unix nano)
	fields, err := rl.client.HMGet(ctx, dataKey, "tokens", "last").Result()
	if err != nil && err != redis.Nil {
		return false, fmt.Errorf("failed to fetch bucket: %w", err)
	}

	var tokensInBucket float64
	var last int64
	if err == redis.Nil || len(fields) < 2 || fields[0] == nil {
		// Initialize bucket
		tokensInBucket = rl.bucketSize
		last = time.Now().UnixNano()
	} else {
		tokensStr, ok := fields[0].(string)
		if !ok {
			return false, fmt.Errorf("invalid tokens field")
		}
		tokensInBucket, err = strconv.ParseFloat(tokensStr, 64)
		if err != nil {
			return false, fmt.Errorf("invalid tokens value: %w", err)
		}

		lastStr, ok := fields[1].(string)
		if !ok {
			return false, fmt.Errorf("invalid last field")
		}
		last, err = strconv.ParseInt(lastStr, 10, 64)
		if err != nil {
			return false, fmt.Errorf("invalid last value: %w", err)
		}
	}

	// Refill tokens based on elapsed time
	now := time.Now().UnixNano()
	elapsedSeconds := float64(now-last) / 1e9
	currentTokens := tokensInBucket + (elapsedSeconds * rl.refillRate)
	if currentTokens > rl.bucketSize {
		currentTokens = rl.bucketSize
	}

	// Check if we have enough tokens
	if currentTokens < tokens {
		// Save last time even if not consuming (for accurate refill)
		_, err = rl.client.HMSet(ctx, dataKey, "tokens", fmt.Sprintf("%.2f", currentTokens), "last", fmt.Sprintf("%d", now)).Result()
		if err != nil {
			return false, fmt.Errorf("failed to save bucket: %w", err)
		}
		return false, nil
	}

	// Consume requested tokens
	currentTokens = math.Max(0, currentTokens-tokens)

	// Save updated state
	_, err = rl.client.HMSet(ctx, dataKey, "tokens", fmt.Sprintf("%.2f", currentTokens), "last", fmt.Sprintf("%d", now)).Result()
	if err != nil {
		return false, fmt.Errorf("failed to save bucket: %w", err)
	}

	return true, nil
}
