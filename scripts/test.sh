#!/usr/bin/env bash
# Pre-deploy regression gate: vet + unit tests (no DB, no network).
# Integration tests (-tags integration) run too when MONGO_TEST_URI is set.
set -euo pipefail

cd "$(dirname "$0")/.."

go vet ./...
go test ./... -count=1

if [ -n "${MONGO_TEST_URI:-}" ]; then
  echo "MONGO_TEST_URI set: running integration tests"
  go vet -tags integration ./...
  go test -tags integration ./... -count=1
fi
