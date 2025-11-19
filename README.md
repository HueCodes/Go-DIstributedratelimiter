# Go-DIstributedratelimiter

A distributed rate limiter implementation in Go, designed to handle high traffic loads in distributed systems.

## Overview

This repository provides a robust and efficient distributed rate limiting solution built in Go. It enables consistent rate limiting across multiple instances or services by leveraging a shared backend (typically Redis), preventing any single node from exceeding defined request thresholds.

Ideal for microservices architectures, API gateways, or any high-concurrency application requiring fair usage enforcement in a clustered environment.

## Features

- Distributed consistency across multiple application instances
- Configurable rate limits (requests per second/minute/hour)
- Support for key-based limiting (e.g., per IP, user ID, API key)
- High performance with minimal overhead
- Easy integration into existing Go services

## Requirements

- Go 1.21 or later
- Redis server (or compatible cluster)

## Installation

```bash
go get github.com/HueCodes/Go-DIstributedratelimiter
```

## Usage

Import the package in your Go application:

```go
import "github.com/HueCodes/Go-DIstributedratelimiter"
```

Basic example (detailed usage and configuration options are provided in the source code and examples directory):

```go
// Initialize the rate limiter with Redis connection and limits
limiter := NewDistributedRateLimiter(redisClient, Config{
    Rate:  100,              // requests
    Per:   time.Minute,     // time window
    Burst: 50,               // optional burst allowance
})

// Check if a request is allowed for a specific key
allowed, err := limiter.Allow(context.Background(), "client-ip-192.168.1.1")
if err != nil {
    // handle error
}
if !allowed {
    // reject request: too many requests
}
```

For complete examples, including HTTP middleware integration, refer to the `examples/` directory.

## Configuration

The rate limiter supports various algorithms and tuning parameters. See the documentation in the code or examples for advanced configuration options such as:

- Token bucket or sliding window algorithms
- Custom key prefixes
- Cleanup intervals
- Error handling strategies

## License

This project is licensed under the MIT License. See the LICENSE file for details.

## Contributing

Contributions are welcome. Please open an issue or submit a pull request for any improvements or bug fixes.
