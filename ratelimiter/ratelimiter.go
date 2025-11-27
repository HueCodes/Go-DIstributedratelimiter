package ratelimiter

import (
	"context"
	"fmt"
	"time"

	"github.com/go-redis/redis/v8"
)

// Config holds the configuration for the rate limiter.
type Config struct {
	RedisAddr      string
	RedisPassword  string
	RedisDB        int
	KeyPrefix      string
	Rate           float64       // Tokens per second (refill rate)
	Burst          float64       // Max tokens in bucket (bucket size)
	KeyTTL         time.Duration // TTL for bucket keys (cleanup inactive keys)
}

// Validate checks if the config is valid.
func (cfg Config) Validate() error {
	if cfg.RedisAddr == "" {
		return fmt.Errorf("RedisAddr is required")
	}
	if cfg.Rate <= 0 {
		return fmt.Errorf("Rate must be > 0, got: %f", cfg.Rate)
	}
	if cfg.Burst <= 0 {
		return fmt.Errorf("Burst must be > 0, got: %f", cfg.Burst)
	}
	return nil
}

// RateLimiter implements a distributed token bucket rate limiter using Redis.
type RateLimiter struct {
	client     *redis.Client
	bucketSize float64
	refillRate float64
	keyPrefix  string
	keyTTL     time.Duration
	script     *redis.Script
}

// Lua script for atomic token bucket operations.
// This eliminates the need for locking and reduces round trips to 1.
const luaScript = `
local key = KEYS[1]
local now = tonumber(ARGV[1])
local rate = tonumber(ARGV[2])
local burst = tonumber(ARGV[3])
local tokens_requested = tonumber(ARGV[4])
local ttl = tonumber(ARGV[5])

local bucket = redis.call('HMGET', key, 'tokens', 'last')
local tokens = tonumber(bucket[1])
local last = tonumber(bucket[2])

if tokens == nil then
    tokens = burst
    last = now
end

local elapsed = math.max(0, now - last)
local current_tokens = math.min(burst, tokens + (elapsed / 1e9) * rate)

if current_tokens >= tokens_requested then
    current_tokens = current_tokens - tokens_requested
    redis.call('HSET', key, 'tokens', tostring(current_tokens), 'last', tostring(now))
    redis.call('EXPIRE', key, ttl)
    return 1
else
    redis.call('HSET', key, 'tokens', tostring(current_tokens), 'last', tostring(now))
    redis.call('EXPIRE', key, ttl)
    return 0
end
`

// NewRateLimiter creates a new distributed rate limiter from a Config.
func NewRateLimiter(ctx context.Context, cfg Config) (*RateLimiter, error) {
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("invalid config: %w", err)
	}

	client := redis.NewClient(&redis.Options{
		Addr:     cfg.RedisAddr,
		Password: cfg.RedisPassword,
		DB:       cfg.RedisDB,
	})

	if err := client.Ping(ctx).Err(); err != nil {
		return nil, fmt.Errorf("failed to connect to Redis: %w", err)
	}

	keyPrefix := cfg.KeyPrefix
	if keyPrefix == "" {
		keyPrefix = "ratelimiter:"
	}

	keyTTL := cfg.KeyTTL
	if keyTTL == 0 {
		keyTTL = 1 * time.Hour
	}

	return &RateLimiter{
		client:     client,
		bucketSize: cfg.Burst,
		refillRate: cfg.Rate,
		keyPrefix:  keyPrefix,
		keyTTL:     keyTTL,
		script:     redis.NewScript(luaScript),
	}, nil
}

// Close closes the Redis client connection.
func (rl *RateLimiter) Close() error {
	return rl.client.Close()
}

// Allow checks if a request is allowed for the given key.
// It atomically updates the token bucket and returns true if tokens are available.
func (rl *RateLimiter) Allow(ctx context.Context, key string, tokens float64) (bool, error) {
	if tokens <= 0 {
		return false, fmt.Errorf("tokens must be > 0, got: %f", tokens)
	}

	dataKey := rl.keyPrefix + key
	now := time.Now().UnixNano()
	ttlSeconds := int(rl.keyTTL.Seconds())

	result, err := rl.script.Run(
		ctx,
		rl.client,
		[]string{dataKey},
		now,
		rl.refillRate,
		rl.bucketSize,
		tokens,
		ttlSeconds,
	).Result()

	if err != nil {
		return false, fmt.Errorf("failed to execute rate limit check: %w", err)
	}

	allowed, ok := result.(int64)
	if !ok {
		return false, fmt.Errorf("unexpected script result type: %T", result)
	}

	return allowed == 1, nil
}
