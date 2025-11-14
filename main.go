package main

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/<your-username>/ratelimiter"
)

// Middleware wraps an HTTP handler with rate limiting.
func RateLimitMiddleware(rl *ratelimiter.RateLimiter, key string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			allowed, err := rl.Allow(r.Context(), key, 1)
			if err != nil {
				http.Error(w, "Internal server error", http.StatusInternalServerError)
				return
			}
			if !allowed {
				http.Error(w, "Rate limit exceeded", http.StatusTooManyRequests)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func main() {
	ctx := context.Background()

	// Configure rate limiter
	cfg := ratelimiter.Config{
		RedisAddr:      "localhost:6379",
		RedisPassword:  "",
		MaxRetries:     3,
		BaseRetryDelay: 100 * time.Millisecond,
		Rate:           10, // 10 tokens per second
		Burst:          20, // Max burst
	}

	rl, err := ratelimiter.NewRateLimiter(ctx, cfg)
	if err != nil {
		fmt.Printf("Failed to initialize rate limiter: %v\n", err)
		return
	}
	defer rl.Close()

	// Example HTTP server with rate limiting
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintln(w, "Hello, world!")
	})

	// Apply rate limiter middleware
	handler := RateLimitMiddleware(rl, "api:global")(mux)
	fmt.Println("Starting server on :8080")
	if err := http.ListenAndServe(":8080", handler); err != nil {
		fmt.Printf("Server failed: %v\n", err)
	}
}
