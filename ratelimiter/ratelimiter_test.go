package ratelimiter

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/go-redis/redis/v8"
)

func setupTestRedis(t *testing.T) *redis.Client {
	client := redis.NewClient(&redis.Options{
		Addr: "localhost:6379",
		DB:   15,
	})

	ctx := context.Background()
	if err := client.Ping(ctx).Err(); err != nil {
		t.Skipf("Redis not available: %v", err)
	}

	if err := client.FlushDB(ctx).Err(); err != nil {
		t.Fatalf("Failed to flush test DB: %v", err)
	}

	return client
}

func TestConfigValidation(t *testing.T) {
	tests := []struct {
		name    string
		cfg     Config
		wantErr bool
	}{
		{
			name: "valid config",
			cfg: Config{
				RedisAddr: "localhost:6379",
				Rate:      10,
				Burst:     20,
			},
			wantErr: false,
		},
		{
			name: "missing redis addr",
			cfg: Config{
				Rate:  10,
				Burst: 20,
			},
			wantErr: true,
		},
		{
			name: "zero rate",
			cfg: Config{
				RedisAddr: "localhost:6379",
				Rate:      0,
				Burst:     20,
			},
			wantErr: true,
		},
		{
			name: "negative rate",
			cfg: Config{
				RedisAddr: "localhost:6379",
				Rate:      -1,
				Burst:     20,
			},
			wantErr: true,
		},
		{
			name: "zero burst",
			cfg: Config{
				RedisAddr: "localhost:6379",
				Rate:      10,
				Burst:     0,
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.cfg.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestNewRateLimiter(t *testing.T) {
	setupTestRedis(t)

	ctx := context.Background()
	cfg := Config{
		RedisAddr: "localhost:6379",
		RedisDB:   15,
		Rate:      10,
		Burst:     20,
	}

	rl, err := NewRateLimiter(ctx, cfg)
	if err != nil {
		t.Fatalf("NewRateLimiter() error = %v", err)
	}
	defer rl.Close()

	if rl.refillRate != 10 {
		t.Errorf("refillRate = %v, want 10", rl.refillRate)
	}
	if rl.bucketSize != 20 {
		t.Errorf("bucketSize = %v, want 20", rl.bucketSize)
	}
	if rl.keyPrefix != "ratelimiter:" {
		t.Errorf("keyPrefix = %v, want ratelimiter:", rl.keyPrefix)
	}
}

func TestNewRateLimiterCustomPrefix(t *testing.T) {
	setupTestRedis(t)

	ctx := context.Background()
	cfg := Config{
		RedisAddr: "localhost:6379",
		RedisDB:   15,
		KeyPrefix: "custom:",
		Rate:      10,
		Burst:     20,
	}

	rl, err := NewRateLimiter(ctx, cfg)
	if err != nil {
		t.Fatalf("NewRateLimiter() error = %v", err)
	}
	defer rl.Close()

	if rl.keyPrefix != "custom:" {
		t.Errorf("keyPrefix = %v, want custom:", rl.keyPrefix)
	}
}

func TestAllow_Basic(t *testing.T) {
	setupTestRedis(t)

	ctx := context.Background()
	cfg := Config{
		RedisAddr: "localhost:6379",
		RedisDB:   15,
		Rate:      10,
		Burst:     10,
	}

	rl, err := NewRateLimiter(ctx, cfg)
	if err != nil {
		t.Fatalf("NewRateLimiter() error = %v", err)
	}
	defer rl.Close()

	allowed, err := rl.Allow(ctx, "test-key", 1)
	if err != nil {
		t.Fatalf("Allow() error = %v", err)
	}
	if !allowed {
		t.Error("First request should be allowed")
	}
}

func TestAllow_BurstLimit(t *testing.T) {
	setupTestRedis(t)

	ctx := context.Background()
	cfg := Config{
		RedisAddr: "localhost:6379",
		RedisDB:   15,
		Rate:      1,
		Burst:     5,
	}

	rl, err := NewRateLimiter(ctx, cfg)
	if err != nil {
		t.Fatalf("NewRateLimiter() error = %v", err)
	}
	defer rl.Close()

	for i := 0; i < 5; i++ {
		allowed, err := rl.Allow(ctx, "test-burst", 1)
		if err != nil {
			t.Fatalf("Allow() error on request %d: %v", i, err)
		}
		if !allowed {
			t.Errorf("Request %d should be allowed (within burst)", i)
		}
	}

	allowed, err := rl.Allow(ctx, "test-burst", 1)
	if err != nil {
		t.Fatalf("Allow() error = %v", err)
	}
	if allowed {
		t.Error("Request beyond burst should be denied")
	}
}

func TestAllow_Refill(t *testing.T) {
	setupTestRedis(t)

	ctx := context.Background()
	cfg := Config{
		RedisAddr: "localhost:6379",
		RedisDB:   15,
		Rate:      10,
		Burst:     5,
	}

	rl, err := NewRateLimiter(ctx, cfg)
	if err != nil {
		t.Fatalf("NewRateLimiter() error = %v", err)
	}
	defer rl.Close()

	for i := 0; i < 5; i++ {
		allowed, err := rl.Allow(ctx, "test-refill", 1)
		if err != nil {
			t.Fatalf("Allow() error: %v", err)
		}
		if !allowed {
			t.Errorf("Request %d should be allowed", i)
		}
	}

	allowed, err := rl.Allow(ctx, "test-refill", 1)
	if err != nil {
		t.Fatalf("Allow() error = %v", err)
	}
	if allowed {
		t.Error("Request should be denied immediately after burst")
	}

	time.Sleep(200 * time.Millisecond)

	allowed, err = rl.Allow(ctx, "test-refill", 1)
	if err != nil {
		t.Fatalf("Allow() error = %v", err)
	}
	if !allowed {
		t.Error("Request should be allowed after refill period")
	}
}

func TestAllow_MultipleKeys(t *testing.T) {
	setupTestRedis(t)

	ctx := context.Background()
	cfg := Config{
		RedisAddr: "localhost:6379",
		RedisDB:   15,
		Rate:      10,
		Burst:     2,
	}

	rl, err := NewRateLimiter(ctx, cfg)
	if err != nil {
		t.Fatalf("NewRateLimiter() error = %v", err)
	}
	defer rl.Close()

	for i := 0; i < 2; i++ {
		allowed, err := rl.Allow(ctx, "key1", 1)
		if err != nil {
			t.Fatalf("Allow() error = %v", err)
		}
		if !allowed {
			t.Error("key1 should be allowed")
		}
	}

	allowed, err := rl.Allow(ctx, "key1", 1)
	if err != nil {
		t.Fatalf("Allow() error = %v", err)
	}
	if allowed {
		t.Error("key1 should be denied after burst")
	}

	for i := 0; i < 2; i++ {
		allowed, err := rl.Allow(ctx, "key2", 1)
		if err != nil {
			t.Fatalf("Allow() error = %v", err)
		}
		if !allowed {
			t.Error("key2 should be allowed independently")
		}
	}
}

func TestAllow_InvalidTokens(t *testing.T) {
	setupTestRedis(t)

	ctx := context.Background()
	cfg := Config{
		RedisAddr: "localhost:6379",
		RedisDB:   15,
		Rate:      10,
		Burst:     10,
	}

	rl, err := NewRateLimiter(ctx, cfg)
	if err != nil {
		t.Fatalf("NewRateLimiter() error = %v", err)
	}
	defer rl.Close()

	_, err = rl.Allow(ctx, "test", 0)
	if err == nil {
		t.Error("Allow() should return error for 0 tokens")
	}

	_, err = rl.Allow(ctx, "test", -1)
	if err == nil {
		t.Error("Allow() should return error for negative tokens")
	}
}

func TestAllow_Concurrency(t *testing.T) {
	setupTestRedis(t)

	ctx := context.Background()
	cfg := Config{
		RedisAddr: "localhost:6379",
		RedisDB:   15,
		Rate:      100,
		Burst:     50,
	}

	rl, err := NewRateLimiter(ctx, cfg)
	if err != nil {
		t.Fatalf("NewRateLimiter() error = %v", err)
	}
	defer rl.Close()

	const goroutines = 10
	const requestsPerGoroutine = 10

	allowed := make(chan bool, goroutines*requestsPerGoroutine)
	done := make(chan struct{})

	for i := 0; i < goroutines; i++ {
		go func() {
			for j := 0; j < requestsPerGoroutine; j++ {
				ok, err := rl.Allow(ctx, "concurrent-test", 1)
				if err == nil {
					allowed <- ok
				}
			}
			done <- struct{}{}
		}()
	}

	for i := 0; i < goroutines; i++ {
		<-done
	}
	close(allowed)

	allowedCount := 0
	for ok := range allowed {
		if ok {
			allowedCount++
		}
	}

	if allowedCount > 50 {
		t.Errorf("Allowed %d requests, should not exceed burst of 50", allowedCount)
	}
	if allowedCount < 45 {
		t.Errorf("Allowed %d requests, expected around 50", allowedCount)
	}
}

func TestAllow_FractionalTokens(t *testing.T) {
	setupTestRedis(t)

	ctx := context.Background()
	cfg := Config{
		RedisAddr: "localhost:6379",
		RedisDB:   15,
		Rate:      10,
		Burst:     10,
	}

	rl, err := NewRateLimiter(ctx, cfg)
	if err != nil {
		t.Fatalf("NewRateLimiter() error = %v", err)
	}
	defer rl.Close()

	allowed, err := rl.Allow(ctx, "test-fractional", 0.5)
	if err != nil {
		t.Fatalf("Allow() error = %v", err)
	}
	if !allowed {
		t.Error("Request with fractional tokens should be allowed")
	}

	for i := 0; i < 19; i++ {
		allowed, _ := rl.Allow(ctx, "test-fractional", 0.5)
		if !allowed {
			t.Errorf("Request %d should be allowed", i)
		}
	}

	allowed, _ = rl.Allow(ctx, "test-fractional", 0.5)
	if allowed {
		t.Error("Request should be denied after consuming all tokens")
	}
}

func TestAllow_EmptyKey(t *testing.T) {
	setupTestRedis(t)

	ctx := context.Background()
	cfg := Config{
		RedisAddr: "localhost:6379",
		RedisDB:   15,
		Rate:      10,
		Burst:     10,
	}

	rl, err := NewRateLimiter(ctx, cfg)
	if err != nil {
		t.Fatalf("NewRateLimiter() error = %v", err)
	}
	defer rl.Close()

	allowed, err := rl.Allow(ctx, "", 1)
	if err != nil {
		t.Fatalf("Allow() error = %v", err)
	}
	if !allowed {
		t.Error("Empty key should be allowed (valid use case)")
	}
}

func TestAllow_LargeBurst(t *testing.T) {
	setupTestRedis(t)

	ctx := context.Background()
	cfg := Config{
		RedisAddr: "localhost:6379",
		RedisDB:   15,
		Rate:      1000,
		Burst:     10000,
	}

	rl, err := NewRateLimiter(ctx, cfg)
	if err != nil {
		t.Fatalf("NewRateLimiter() error = %v", err)
	}
	defer rl.Close()

	allowed, err := rl.Allow(ctx, "test-large", 5000)
	if err != nil {
		t.Fatalf("Allow() error = %v", err)
	}
	if !allowed {
		t.Error("Large token request should be allowed")
	}

	allowed, err = rl.Allow(ctx, "test-large", 5000)
	if err != nil {
		t.Fatalf("Allow() error = %v", err)
	}
	if !allowed {
		t.Error("Second large token request should be allowed")
	}

	allowed, err = rl.Allow(ctx, "test-large", 1)
	if err != nil {
		t.Fatalf("Allow() error = %v", err)
	}
	if allowed {
		t.Error("Request beyond bucket should be denied")
	}
}

func TestAllow_ContextCancellation(t *testing.T) {
	setupTestRedis(t)

	cfg := Config{
		RedisAddr: "localhost:6379",
		RedisDB:   15,
		Rate:      10,
		Burst:     10,
	}

	ctx := context.Background()
	rl, err := NewRateLimiter(ctx, cfg)
	if err != nil {
		t.Fatalf("NewRateLimiter() error = %v", err)
	}
	defer rl.Close()

	cancelCtx, cancel := context.WithCancel(ctx)
	cancel()

	_, err = rl.Allow(cancelCtx, "test-cancel", 1)
	if err == nil {
		t.Error("Allow() should return error with cancelled context")
	}
}

func TestNewRateLimiter_InvalidConfig(t *testing.T) {
	ctx := context.Background()
	cfg := Config{
		RedisAddr: "localhost:6379",
		RedisDB:   15,
		Rate:      0,
		Burst:     10,
	}

	_, err := NewRateLimiter(ctx, cfg)
	if err == nil {
		t.Error("NewRateLimiter() should return error for invalid config")
	}
}

func TestNewRateLimiter_ConnectionFailure(t *testing.T) {
	ctx := context.Background()
	cfg := Config{
		RedisAddr: "localhost:9999",
		RedisDB:   15,
		Rate:      10,
		Burst:     10,
	}

	_, err := NewRateLimiter(ctx, cfg)
	if err == nil {
		t.Error("NewRateLimiter() should return error for bad connection")
	}
}

func setupBenchmarkRedis(b *testing.B) *RateLimiter {
	client := redis.NewClient(&redis.Options{
		Addr: "localhost:6379",
		DB:   15,
	})

	ctx := context.Background()
	if err := client.Ping(ctx).Err(); err != nil {
		b.Skipf("Redis not available: %v", err)
	}

	if err := client.FlushDB(ctx).Err(); err != nil {
		b.Fatalf("Failed to flush test DB: %v", err)
	}

	cfg := Config{
		RedisAddr: "localhost:6379",
		RedisDB:   15,
		Rate:      1000,
		Burst:     1000,
	}

	rl, err := NewRateLimiter(ctx, cfg)
	if err != nil {
		b.Fatalf("NewRateLimiter() error = %v", err)
	}

	return rl
}

func BenchmarkAllow_Sequential(b *testing.B) {
	rl := setupBenchmarkRedis(b)
	defer rl.Close()

	ctx := context.Background()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		rl.Allow(ctx, "bench-seq", 1)
	}
}

func BenchmarkAllow_Parallel(b *testing.B) {
	rl := setupBenchmarkRedis(b)
	defer rl.Close()

	ctx := context.Background()
	b.ResetTimer()

	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			key := "bench-parallel-" + strconv.Itoa(i%100)
			rl.Allow(ctx, key, 1)
			i++
		}
	})
}

func BenchmarkAllow_HighContention(b *testing.B) {
	rl := setupBenchmarkRedis(b)
	defer rl.Close()

	ctx := context.Background()
	b.ResetTimer()

	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			rl.Allow(ctx, "single-key", 1)
		}
	})
}

func BenchmarkAllow_LowContention(b *testing.B) {
	rl := setupBenchmarkRedis(b)
	defer rl.Close()

	ctx := context.Background()
	b.ResetTimer()

	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			key := "bench-low-" + strconv.Itoa(i)
			rl.Allow(ctx, key, 1)
			i++
		}
	})
}

func BenchmarkAllow_Denied(b *testing.B) {
	client := redis.NewClient(&redis.Options{
		Addr: "localhost:6379",
		DB:   15,
	})
	defer client.Close()

	ctx := context.Background()
	if err := client.Ping(ctx).Err(); err != nil {
		b.Skipf("Redis not available: %v", err)
	}

	if err := client.FlushDB(ctx).Err(); err != nil {
		b.Fatalf("Failed to flush test DB: %v", err)
	}

	cfg := Config{
		RedisAddr: "localhost:6379",
		RedisDB:   15,
		Rate:      1,
		Burst:     1,
	}

	rl, err := NewRateLimiter(ctx, cfg)
	if err != nil {
		b.Fatalf("NewRateLimiter() error = %v", err)
	}
	defer rl.Close()

	rl.Allow(ctx, "bench-denied", 1)

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		rl.Allow(ctx, "bench-denied", 1)
	}
}

func BenchmarkAllow_FractionalTokens(b *testing.B) {
	rl := setupBenchmarkRedis(b)
	defer rl.Close()

	ctx := context.Background()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		rl.Allow(ctx, "bench-frac", 0.1)
	}
}

func BenchmarkConfigValidation(b *testing.B) {
	cfg := Config{
		RedisAddr: "localhost:6379",
		Rate:      10,
		Burst:     20,
	}

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		cfg.Validate()
	}
}
