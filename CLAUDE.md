# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Overview

raus is a Go library that allocates unique machine IDs for Snowflake algorithm implementations using Redis as a coordination backend. It ensures no two service instances share the same machine ID during startup.

## Build & Test

```bash
# Run all tests (requires redis-server binary in PATH; tests auto-start a Redis on port 26379)
go test -v ./...

# Run a single test
go test -v -run TestGet ./...

# Run integration tests with Redis Cluster (requires Docker)
docker compose up --exit-code-from app
# or
./cluster-ci.sh

# Format and fix code (must run before committing)
go fmt ./...
go fix ./...
```

Tests use `github.com/soh335/go-test-redisserver` to spin up a temporary Redis instance automatically. Set `REDIS_URL` env var to use an external Redis instead.

## Architecture

- **raus.go** — Core logic: `Raus` struct, `New()` constructor, `Get()` to acquire a machine ID, `subscribe()` for Pub/Sub discovery + atomic lock via `SET NX`, `publish()` for heartbeat + lock renewal, graceful cleanup on context cancellation.
- **redis.go** — `RedisClient` interface abstracting `redis.Client` and `redis.ClusterClient`, `RedisOptions` struct, `NewClient()` factory. Supports `redis://` and `rediscluster://` URI schemes.
- **raus_test.go** — Unit/integration tests including URI parsing, single-instance Get, and concurrent race test (`TestGetRace`).
- **slog_test.go** — Tests for structured logging integration (`SetDefaultSlogLogger`, `SetSlogLogger`).

## Key Design Details

- Machine ID allocation is a two-phase process: discovery (listen to Pub/Sub for used IDs) then atomic lock (`SET NX` with expiration).
- The `publish()` goroutine maintains a heartbeat (1s interval), refreshes the lock via `GETSET` + `EXPIRE` pipeline, and releases the lock on context cancellation.
- Fatal errors (e.g., UUID collision in `GETSET`) are sent to the error channel and stop the heartbeat goroutine; recoverable errors trigger Redis client reconnection.
- Configurable package-level vars: `MaxCandidate`, `LockExpires`, `SubscribeTimeout`, `CleanupTimeout`.

## CI

- GitHub Actions runs `go test -v ./...` with Redis 7.x.
- A separate `cluster.yml` workflow tests Redis Cluster via Docker Compose.
