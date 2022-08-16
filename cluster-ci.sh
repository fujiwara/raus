#!/bin/bash

set -ex
grep image: compose.yml | sort | uniq | awk '{print $2}' | xargs -I {} docker pull {}
docker compose up -d
sleep 10
docker compose run app bash -c "go test -v ./..."
docker compose down
