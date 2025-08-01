# raus

[![GoDoc](https://godoc.org/github.com/fujiwara/raus?status.svg)](https://godoc.org/github.com/fujiwara/raus)
[![Go Report Card](https://goreportcard.com/badge/github.com/fujiwara/raus)](https://goreportcard.com/report/github.com/fujiwara/raus)
[![MIT License](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)

**Ranged unique ID supplier** - A distributed machine ID generator for Snowflake algorithm implementations using Redis as coordination backend.

## Overview

raus provides unique machine ID allocation for distributed ID generators like the Snowflake algorithm. It ensures no machine ID conflicts across multiple instances during startup, making it essential for implementing distributed unique ID generation systems in microservices and containerized environments.

The primary use case is allocating unique machine IDs (typically 0-1023 for 10-bit machine ID) that Snowflake algorithm implementations require to generate globally unique IDs across distributed systems.

## Features

- **Machine ID Allocation**: Safe unique machine ID assignment for Snowflake implementations
- **Redis Integration**: Works with both Redis and Redis Cluster for coordination
- **Range-based Assignment**: Allocate machine IDs within specified ranges (e.g., 0-1023)
- **Real-time Coordination**: Uses Redis Pub/Sub for instance communication
- **Startup-time Assignment**: Optimized for one-time ID allocation during service startup
- **Automatic Cleanup**: Graceful lock release on shutdown
- **RESP3 Support**: Compatible with latest Redis protocol

## Installation

```bash
go get github.com/fujiwara/raus
```

**Requirements:**
- Go 1.23 or later
- Redis 7.x or later (Redis 6.x also supported)

## Quick Start

### Basic Machine ID Allocation

```go
package main

import (
    "context"
    "log"
    "time"

    "github.com/fujiwara/raus"
)

func main() {
    // Allocate machine ID for Snowflake (0-1023 range for 10-bit machine ID)
    r, err := raus.New("redis://localhost:6379", 0, 1023)
    if err != nil {
        log.Fatal(err)
    }

    // Get unique machine ID during startup
    ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
    defer cancel()

    machineID, errCh, err := r.Get(ctx)
    if err != nil {
        log.Fatal(err)
    }

    log.Printf("Allocated machine ID: %d", machineID)

    // Monitor for errors in background
    go func() {
        for err := range errCh {
            log.Printf("Runtime error: %v", err)
            // Handle Redis connectivity issues, etc.
        }
    }()

    // Use machine ID in your Snowflake implementation
    snowflake := NewSnowflake(machineID)
    
    // Run your application...
    runApplication(snowflake)

    // Cleanup on shutdown
    cancel()
}
```

### Integration with Snowflake Algorithm

```go
type SnowflakeGenerator struct {
    machineID uint64
    sequence  uint64
    lastTime  int64
}

func NewSnowflake(machineID uint) *SnowflakeGenerator {
    return &SnowflakeGenerator{
        machineID: uint64(machineID),
    }
}

func (s *SnowflakeGenerator) NextID() uint64 {
    // Implement Snowflake algorithm using s.machineID
    // Format: timestamp(41) + machineID(10) + sequence(12)
    // ...
}

func main() {
    // Get machine ID from raus
    r, _ := raus.New("redis://localhost:6379", 0, 1023)
    machineID, _, _ := r.Get(context.Background())
    
    // Initialize Snowflake with allocated machine ID
    snowflake := NewSnowflake(machineID)
    
    // Generate unique IDs
    id1 := snowflake.NextID()
    id2 := snowflake.NextID()
}
```

## Configuration

### Redis URI Format

raus supports flexible Redis URI formats for coordination:

```go
// Standard Redis
raus.New("redis://localhost:6379", 0, 1023)

// Redis with authentication
raus.New("redis://user:password@redis.example.com:6379", 0, 1023)

// Redis with custom namespace (for multi-environment)
raus.New("redis://localhost:6379?ns=production", 0, 1023)

// Redis Cluster for high availability
raus.New("rediscluster://cluster.example.com:6379", 0, 1023)

// Redis Cluster with authentication and namespace
raus.New("rediscluster://user:pass@cluster.example.com:6379?ns=prod", 0, 1023)
```

### Machine ID Ranges

Common Snowflake configurations:

```go
// Standard 10-bit machine ID (Twitter Snowflake)
raus.New(redisURI, 0, 1023)  // 2^10 - 1 = 1023

// 8-bit machine ID for smaller deployments
raus.New(redisURI, 0, 255)   // 2^8 - 1 = 255

// 12-bit machine ID for larger deployments
raus.New(redisURI, 0, 4095)  // 2^12 - 1 = 4095

// Custom range for specific deployment
raus.New(redisURI, 100, 199) // Reserved range for specific service
```

## How It Works

The machine ID allocation process follows these steps:

1. **Discovery Phase**: Listen to Redis Pub/Sub channel to discover other instances' machine IDs
2. **Candidate Selection**: Choose from available machine IDs not used by other instances
3. **Atomic Assignment**: Use Redis `SET NX` to atomically claim a machine ID
4. **Heartbeat Broadcasting**: Continuously publish assigned machine ID to coordinate with other instances
5. **Graceful Release**: Release machine ID lock on service shutdown

## Architecture

```
┌─────────────────┐    ┌─────────────────┐    ┌─────────────────┐
│   Service A     │    │   Service B     │    │   Service C     │
│ MachineID: 0    │    │ MachineID: 1    │    │ MachineID: 2    │
│                 │    │                 │    │                 │
│ Snowflake IDs:  │    │ Snowflake IDs:  │    │ Snowflake IDs:  │
│ 12345...000     │    │ 12345...001     │    │ 12345...002     │
│ 12345...000     │    │ 12345...001     │    │ 12345...002     │
└─────────┬───────┘    └─────────┬───────┘    └─────────┬───────┘
          │                      │                      │
          └──────────────────────┼──────────────────────┘
                                 │
                        ┌────────▼────────┐
                        │     Redis       │
                        │                 │
                        │ • Machine ID    │
                        │   Coordination  │
                        │ • Pub/Sub       │
                        │ • Atomic Locks  │
                        └─────────────────┘
```

## Advanced Usage

### Startup Coordination in Container Environments

```go
func initializeMachineID() uint {
    // Wait for Redis availability
    r, err := raus.New(os.Getenv("REDIS_URL"), 0, 1023)
    if err != nil {
        log.Fatal("Failed to connect to Redis:", err)
    }

    // Use longer timeout for container startup scenarios
    ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
    defer cancel()

    machineID, errCh, err := r.Get(ctx)
    if err != nil {
        log.Fatal("Failed to allocate machine ID:", err)
    }

    // Background monitoring for Redis connectivity
    go func() {
        for err := range errCh {
            log.Printf("Machine ID coordination error: %v", err)
            // Consider alerting or graceful shutdown on persistent errors
        }
    }()

    return machineID
}
```

### Multi-Environment Support

```go
// Development environment (smaller range)
devRaus, _ := raus.New("redis://dev-redis:6379?ns=dev", 0, 63)

// Staging environment  
stagingRaus, _ := raus.New("redis://staging-redis:6379?ns=staging", 64, 127)

// Production environment (larger range)
prodRaus, _ := raus.New("redis://prod-redis:6379?ns=prod", 128, 1023)
```

### Configuration Tuning

```go
// Adjust timing parameters before calling New()
raus.MaxCandidate = 50              // More candidates for large deployments
raus.LockExpires = 60 * time.Second // Longer locks for slow startups
raus.SubscribeTimeout = 10 * time.Second // Longer discovery phase
raus.CleanupTimeout = 30 * time.Second   // Cleanup timeout
```

## Use Cases

### Primary Use Cases (Snowflake Algorithm)
- **Distributed ID Generation**: Machine ID allocation for Snowflake implementations
- **Microservices ID Systems**: Unique identifier generation across services
- **Event Sourcing**: Ordered ID generation with embedded machine identification
- **Database Sharding**: Shard-aware ID generation with machine component

### Secondary Use Cases
- **Load Balancer Configuration**: Backend server identification
- **Container Orchestration**: Pod/container unique identification  
- **Multi-tenant Systems**: Tenant-specific ID generation coordination
- **Distributed Caching**: Cache partition assignment

## Comparison with Alternatives

### When to Use raus

**Choose raus when you need:**
- Machine ID coordination for Snowflake algorithm
- Guaranteed non-overlapping ID ranges across instances
- Redis infrastructure already available
- Startup-time ID allocation (not runtime generation)

### Alternative Approaches

| Approach | Use Case | Pros | Cons |
|----------|----------|------|------|
| **raus** | Snowflake machine ID | Guaranteed uniqueness, Redis coordination | Redis dependency, startup complexity |
| **Static Configuration** | Fixed deployments | Simple, no dependencies | Manual management, scaling issues |
| **UUID v1 MAC** | MAC-based machine ID | Hardware-based uniqueness | Privacy concerns, virtual environments |
| **IP-based** | Network-based ID | Automatic from infrastructure | Network changes break IDs |

## Testing

```bash
# Run unit tests
go test -v

# Run integration tests with Redis Cluster
docker compose up --exit-code-from app

# Or use the cluster CI script
./cluster-ci.sh
```

## Production Considerations

### High Availability
- Use Redis Cluster or Redis Sentinel for Redis HA
- Configure appropriate timeouts for your startup SLA
- Monitor machine ID allocation success rates

### Monitoring
- Track machine ID allocation time during startup
- Monitor Redis connectivity from application instances
- Alert on machine ID allocation failures

### Scaling
- Plan machine ID ranges based on maximum expected instances
- Consider namespace separation for different environments
- Reserve ranges for different services or deployment patterns

## Compatibility

- **Go**: 1.23+
- **Redis**: 7.x (recommended), 6.x supported
- **Protocol**: RESP3 (default), RESP2 compatible
- **Clustering**: Redis Cluster supported
- **Snowflake**: Compatible with standard Snowflake algorithm implementations

## Contributing

1. Fork the repository
2. Create a feature branch
3. Add tests for your changes
4. Ensure all tests pass
5. Submit a pull request

## License

MIT License

Copyright (c) 2017 FUJIWARA Shunichiro

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
SOFTWARE.