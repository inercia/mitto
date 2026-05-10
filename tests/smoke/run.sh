#!/bin/bash
# Smoke test orchestrator — run from project root or tests/smoke/
# Cross-compiles binaries, builds Docker image, runs CLI + Playwright tests
set -e

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"
cd "$PROJECT_ROOT"

SMOKE_PORT="${SMOKE_PORT:-8089}"
COMPOSE_FILE="tests/smoke/docker-compose.yml"

cleanup() {
    echo ""
    echo "=== Cleanup ==="
    docker compose -f "$COMPOSE_FILE" down 2>/dev/null || true
}
trap cleanup EXIT

echo "=== Step 1: Cross-compile for Linux ==="
mkdir -p tests/smoke/.build
# Detect host architecture (Apple Silicon → arm64, x86 → amd64)
SMOKE_GOARCH=$(uname -m | sed 's/x86_64/amd64/' | sed 's/aarch64/arm64/')
echo "Target architecture: linux/${SMOKE_GOARCH}"
GOOS=linux GOARCH="${SMOKE_GOARCH}" go build -o tests/smoke/.build/mitto ./cmd/mitto
GOOS=linux GOARCH="${SMOKE_GOARCH}" go build -o tests/smoke/.build/mock-acp-server ./tests/mocks/acp-server
echo "✅ Binaries compiled"

echo ""
echo "=== Step 2: Build Docker image ==="
docker compose -f "$COMPOSE_FILE" build
echo "✅ Docker image built"

echo ""
echo "=== Step 3: Run CLI smoke tests ==="
docker compose -f "$COMPOSE_FILE" run --rm mitto /home/mitto/smoke-test.sh
echo "✅ CLI smoke tests passed"

echo ""
echo "=== Step 4: Start container for Playwright ==="
docker compose -f "$COMPOSE_FILE" up -d

echo "Waiting for Mitto to be healthy..."
for i in $(seq 1 30); do
    if curl -sf "http://localhost:${SMOKE_PORT}/mitto/api/health" >/dev/null 2>&1; then
        echo "✅ Mitto is healthy"
        break
    fi
    if [ "$i" -eq 30 ]; then
        echo "❌ Mitto failed to become healthy within 30 seconds"
        docker compose -f "$COMPOSE_FILE" logs
        exit 1
    fi
    sleep 1
done

echo ""
echo "=== Step 5: Run Playwright tests ==="
MITTO_TEST_URL="http://localhost:${SMOKE_PORT}" \
MITTO_EXTERNAL_SERVER=1 \
    npx playwright test --config=tests/ui/playwright.config.ts
echo "✅ Playwright tests passed"

echo ""
echo "=== All smoke tests passed! ==="
