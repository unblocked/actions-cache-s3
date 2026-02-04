#!/bin/bash
set -e

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

# Get the directory where this script is located
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"

echo -e "${YELLOW}=== S3 Cache Action Tests ===${NC}"

# Try to find docker in common locations
if command -v docker &> /dev/null; then
    DOCKER_CMD="docker"
elif [ -x "/Applications/Docker.app/Contents/Resources/bin/docker" ]; then
    export PATH="/Applications/Docker.app/Contents/Resources/bin:$PATH"
    DOCKER_CMD="docker"
elif [ -x "/usr/local/bin/docker" ]; then
    DOCKER_CMD="/usr/local/bin/docker"
else
    echo -e "${YELLOW}Docker not found. Running unit tests only (no S3 integration tests).${NC}"
    echo ""
    echo "Running unit tests..."
    cd "$PROJECT_ROOT"
    go test -v ./src -run "TestZip|TestGetReadableBytes|TestOptimalPartSize"
    exit 0
fi

# Verify docker is working
if ! $DOCKER_CMD info &> /dev/null; then
    echo -e "${YELLOW}Docker is not running. Running unit tests only (no S3 integration tests).${NC}"
    echo ""
    echo "Running unit tests..."
    cd "$PROJECT_ROOT"
    go test -v ./src -run "TestZip|TestGetReadableBytes|TestOptimalPartSize"
    exit 0
fi

# Function to clean up
cleanup() {
    if [ "${KEEP_MINIO:-}" != "true" ]; then
        echo ""
        echo -e "${YELLOW}Stopping MinIO...${NC}"
        docker compose -f "$PROJECT_ROOT/docker-compose.test.yml" down -v > /dev/null 2>&1 || true
    fi
}

# Check if MinIO is already running
MINIO_RUNNING=$(docker compose -f "$PROJECT_ROOT/docker-compose.test.yml" ps -q minio 2>/dev/null || true)

if [ -z "$MINIO_RUNNING" ]; then
    echo -e "${YELLOW}Starting MinIO with docker compose...${NC}"

    # Start MinIO and wait for it to be ready
    docker compose -f "$PROJECT_ROOT/docker-compose.test.yml" up -d

    echo "Waiting for MinIO to be ready..."
    # Wait for minio-init to complete (it creates the bucket)
    docker compose -f "$PROJECT_ROOT/docker-compose.test.yml" logs -f minio-init 2>&1 | grep -q "Bucket created successfully" || sleep 5

    STARTED_MINIO=true

    # Set up cleanup trap
    trap cleanup EXIT
else
    echo -e "${GREEN}MinIO already running${NC}"
    STARTED_MINIO=false
fi

echo ""
echo -e "${YELLOW}Running all tests...${NC}"
echo ""

cd "$PROJECT_ROOT"

# Run tests with verbose output
go test -v ./src -count=1

TEST_EXIT=$?

if [ $TEST_EXIT -eq 0 ]; then
    echo ""
    echo -e "${GREEN}✓ All tests passed!${NC}"
else
    echo ""
    echo -e "${RED}✗ Some tests failed${NC}"
    exit $TEST_EXIT
fi

