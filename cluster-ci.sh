#!/bin/bash

set -ex
grep image: compose.yml | sort | uniq | awk '{print $2}' | xargs -I {} docker pull {}
docker compose up --exit-code-from app
