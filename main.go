package main

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/HueCodes/Go-DIstributedratelimiter/ratelimiter"
)

// KeyExtractor is a function that extracts a rate limiting key from an HTTP request.
type KeyExtractor func(*http.Request) string

// ClientIPKeyExtractor extracts the client IP from the request.
// Checks X-Forwarded-For header first, falls back to RemoteAddr.
func ClientIPKeyExtractor(r *http.Request) string {
	forwarded := r.Header.Get("X-Forwarded-For")
	if forwarded != "" {
		parts := strings.Split(forwarded, ",")
		return strings.TrimSpace(parts[0])
	}
	ip := r.RemoteAddr
	if colon := strings.LastIndex(ip, ":"); colon != -1 {
		ip = ip[:colon]
	}
	return ip
}

// HeaderKeyExtractor returns a KeyExtractor that uses a specific header value.
func HeaderKeyExtractor(headerName string) KeyExtractor {
	return func(r *http.Request) string {
		return r.Header.Get(headerName)
	}
}

// PathKeyExtractor extracts the request path as the key.
func PathKeyExtractor(r *http.Request) string {
	return r.URL.Path
}

// RateLimitMiddleware wraps an HTTP handler with rate limiting.
// The keyExtractor function determines how to extract the rate limiting key from each request.
func RateLimitMiddleware(rl *ratelimiter.RateLimiter, keyExtractor KeyExtractor) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			key := keyExtractor(r)
			if key == "" {
				http.Error(w, "Unable to determine rate limit key", http.StatusBadRequest)
				return
			}

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

	cfg := ratelimiter.Config{
		RedisAddr:     "localhost:6379",
		RedisPassword: "",
		RedisDB:       0,
		KeyPrefix:     "ratelimiter:",
		Rate:          10,
		Burst:         20,
		KeyTTL:        1 * time.Hour,
	}

	rl, err := ratelimiter.NewRateLimiter(ctx, cfg)
	if err != nil {
		fmt.Printf("Failed to initialize rate limiter: %v\n", err)
		return
	}
	defer rl.Close()

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintln(w, "Hello, world!")
	})

	// Use ClientIPKeyExtractor for IP-based rate limiting
	handler := RateLimitMiddleware(rl, ClientIPKeyExtractor)(mux)
	fmt.Println("Starting server on :8080")
	if err := http.ListenAndServe(":8080", handler); err != nil {
		fmt.Printf("Server failed: %v\n", err)
	}
}
