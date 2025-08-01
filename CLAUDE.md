# raus - Ranged unique ID supplier for Snowflake Algorithm

## Overview

**raus** is a specialized Go library designed for **Snowflake algorithm implementations** that provides distributed machine ID allocation using Redis as coordination backend. Its primary purpose is to ensure unique machine ID assignment across multiple service instances during startup, which is essential for Snowflake-based distributed ID generation systems.

## Architecture

### Core Components

- **raus.go**: Main library logic for machine ID allocation
  - `Raus` struct: Core machine ID allocator
  - `Get()`: Acquires unique machine ID within specified range
  - Redis Pub/Sub for inter-instance coordination
  - Atomic locking mechanism to prevent machine ID conflicts

- **redis.go**: Redis connection and client management
  - Supports both regular Redis and Redis Cluster
  - URI-based configuration (`redis://`, `rediscluster://`)

### Key Features

1. **Machine ID Allocation**: Safely allocates non-overlapping machine IDs for Snowflake algorithm
2. **Redis Coordination**: Uses Redis as distributed coordination backend
3. **Startup-time Assignment**: Optimized for one-time allocation during service startup
4. **Range-based Configuration**: Flexible machine ID ranges (e.g., 0-1023 for 10-bit)
5. **Pub/Sub Communication**: Real-time coordination between service instances
6. **Graceful Cleanup**: Automatic lock release on service shutdown

## Primary Use Case: Snowflake Algorithm Support

### Snowflake Algorithm Requirements

The Snowflake algorithm generates unique 64-bit IDs with the following structure:
```
┌─────────────────────────────────────────────────────────────────────┐
│ Timestamp (41 bits) │ Machine ID (10 bits) │ Sequence (12 bits)   │
└─────────────────────────────────────────────────────────────────────┘
```

**Machine ID Requirements:**
- Must be unique across all instances
- Typically 0-1023 range for 10-bit machine ID
- Assigned once during service startup
- Consistent throughout service lifetime

### How raus Solves This

raus provides the **Machine ID component** by:
1. **Coordinated Assignment**: Ensures no two instances get the same machine ID
2. **Range Management**: Supports various bit-width configurations (8-bit, 10-bit, 12-bit)
3. **Startup Coordination**: Handles concurrent service startup scenarios
4. **Environment Separation**: Namespace support for dev/staging/prod environments

## Usage Patterns

### Basic Snowflake Integration

```go
// 1. Allocate machine ID during service startup
r, err := raus.New("redis://localhost:6379", 0, 1023)
machineID, errCh, err := r.Get(ctx)

// 2. Initialize Snowflake generator with allocated machine ID
snowflake := NewSnowflakeGenerator(machineID)

// 3. Generate unique IDs throughout service lifetime
id1 := snowflake.NextID() // e.g., 123456789012345678
id2 := snowflake.NextID() // e.g., 123456789012345679
```

### Machine ID Range Configuration

```go
// Standard 10-bit machine ID (supports up to 1024 instances)
raus.New(redisURI, 0, 1023)  // 2^10 - 1

// 8-bit machine ID (smaller deployments, up to 256 instances)
raus.New(redisURI, 0, 255)   // 2^8 - 1

// 12-bit machine ID (larger deployments, up to 4096 instances)
raus.New(redisURI, 0, 4095)  // 2^12 - 1

// Environment-specific ranges
raus.New("redis://localhost:6379?ns=prod", 0, 511)     // Production
raus.New("redis://localhost:6379?ns=staging", 512, 767) // Staging
raus.New("redis://localhost:6379?ns=dev", 768, 1023)   // Development
```

## Implementation Details

### Machine ID Allocation Process

1. **Discovery Phase** (3-second timeout):
   - Subscribe to Redis Pub/Sub channel
   - Listen for other instances' machine ID announcements
   - Build map of currently used machine IDs

2. **Candidate Selection**:
   - Generate list of available machine IDs from range
   - Exclude IDs already claimed by other instances
   - Randomly select from available candidates (up to MaxCandidate)

3. **Atomic Assignment**:
   - Use Redis `SET NX` (set if not exists) to claim machine ID
   - Lock expires after LockExpires duration (default: 60 seconds)
   - Retry with different candidate if lock acquisition fails

4. **Heartbeat Maintenance**:
   - Continuously publish machine ID every second
   - Refresh lock expiration with `GETSET` + `EXPIRE`
   - Monitor for fatal errors (e.g., UUID collisions)

5. **Graceful Cleanup**:
   - Release lock on context cancellation
   - Clean shutdown removes machine ID from coordination

### Error Handling

- **Recoverable Errors**: Network issues, temporary Redis unavailability
- **Fatal Errors**: UUID collisions (extremely rare), lock acquisition failures
- **Context Cancellation**: Triggers cleanup and lock release

### Configuration Parameters

```go
var (
    MaxCandidate     = 10              // Maximum candidate IDs to consider
    LockExpires      = 60 * time.Second // Lock expiration time
    SubscribeTimeout = 3 * time.Second  // Pub/Sub listening timeout
    CleanupTimeout   = 30 * time.Second // Cleanup operation timeout
)
```

## Deployment Scenarios

### Container Orchestration (Kubernetes)

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: snowflake-service
spec:
  replicas: 10  # Each pod gets unique machine ID from 0-1023 range
  template:
    spec:
      containers:
      - name: app
        env:
        - name: REDIS_URL
          value: "redis://redis-cluster:6379?ns=production"
        - name: MACHINE_ID_MIN
          value: "0"
        - name: MACHINE_ID_MAX
          value: "1023"
```

### Multi-Environment Setup

- **Development**: `redis://dev-redis:6379?ns=dev` (range: 0-63)
- **Staging**: `redis://staging-redis:6379?ns=staging` (range: 64-127)  
- **Production**: `redis://prod-redis:6379?ns=prod` (range: 128-1023)

### High Availability

- **Redis Cluster**: Use `rediscluster://` for Redis coordination HA
- **Redis Sentinel**: Automatic failover for Redis coordination
- **Startup Timeouts**: Configure appropriate timeouts for deployment SLA

## Performance Characteristics

### Startup Performance
- **Allocation Time**: Typically 3-5 seconds (discovery + assignment)
- **Concurrent Startup**: Handles multiple instances starting simultaneously
- **Retry Logic**: Automatic retry on temporary failures

### Runtime Performance
- **Memory Usage**: Minimal (single goroutine + small state)
- **Network Traffic**: Heartbeat every 1 second + Pub/Sub messages
- **CPU Usage**: Very low (periodic Redis operations only)

### Scaling Limits
- **Instance Count**: Limited by machine ID range (e.g., 1024 for 10-bit)
- **Redis Load**: Scales with number of instances (O(n) pub/sub messages)
- **Startup Contention**: May increase with concurrent startups

## Dependencies

```go require
github.com/redis/go-redis/v9 v9.11.0    // Redis client
github.com/google/uuid v1.6.0           // UUID generation for instance identification
```

## Use Cases Beyond Snowflake

While primarily designed for Snowflake algorithm, raus can be used for:

- **Database Sharding**: Shard ID assignment for distributed databases
- **Load Balancer Backends**: Unique backend server identification
- **Message Queue Partitioning**: Consumer group coordination
- **Distributed Cache**: Cache partition assignment
- **Container Orchestration**: Pod/container unique identification

## Comparison with Alternatives

| Approach | Pros | Cons | Best For |
|----------|------|------|----------|
| **raus** | Guaranteed uniqueness, dynamic allocation | Redis dependency, startup latency | Dynamic deployments, cloud environments |
| **Static Config** | Simple, no dependencies | Manual management, scaling limitations | Fixed deployments, small scale |
| **MAC Address** | Hardware-based uniqueness | Privacy issues, virtual environments | Physical servers, legacy systems |
| **IP-based** | Network-derived | IP changes break consistency | Stable network environments |
| **Hostname Hash** | Simple derivation | Hash collisions possible | Development, non-critical systems |

## Security Considerations

- **Redis Access Control**: Use authentication and network isolation
- **Namespace Isolation**: Separate environments with different namespaces
- **Network Security**: Secure Redis communication with TLS
- **Machine ID Exposure**: Consider if machine IDs need to be kept private

## Monitoring and Observability

### Key Metrics
- Machine ID allocation success rate
- Machine ID allocation duration
- Redis connectivity status
- Active machine ID count per namespace

### Logging
- Machine ID allocation events
- Redis connectivity issues
- Lock acquisition failures
- Graceful shutdown events

### Alerting
- Machine ID allocation failures
- Redis coordination failures
- Machine ID range exhaustion

## Go Version Compatibility

Requires Go 1.23 or later for:
- Latest error handling with `fmt.Errorf` and `%w` verb
- Context handling improvements
- Performance optimizations
- Security updates

## Redis Compatibility

- **Redis 7.x**: Recommended (full RESP3 support)
- **Redis 6.x**: Supported (RESP3 experimental)
- **Redis Cluster**: Full support for high availability
- **Redis Sentinel**: Supported for automatic failover

raus uses RESP3 protocol by default but can fall back to RESP2 for compatibility.