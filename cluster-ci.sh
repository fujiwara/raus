#!/bin/bash

set -ex
docker compose up -d --wait

# Wait for cluster to be ready
for i in $(seq 1 30); do
  if docker compose exec redis-node-0 redis-cli -a testpass cluster info | grep -q "cluster_state:ok"; then
    break
  fi
  sleep 1
done

REDIS_URL="rediscluster://:testpass@127.0.0.1:16379" go test -v ./...
docker compose down
