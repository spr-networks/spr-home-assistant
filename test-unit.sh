#!/bin/sh
# Run the ha_sync unit tests in a container.
docker build -f Dockerfile.test -t ha_sync-test .
docker run --name ha_sync-test --rm ha_sync-test \
    /bin/bash -c "cd /code && go test -vet=off -v"
