#!/usr/bin/env bash
set -euo pipefail

readonly packages=(
  ./internal/domain
  ./internal/central
  ./internal/central/bootstrap
  ./internal/central/reconcile
)
readonly cover_packages=./internal/domain,./internal/central,./internal/central/bootstrap,./internal/central/reconcile
profile="$(mktemp "${TMPDIR:-/tmp}/cineko-central-coverage.XXXXXX")"
trap 'rm -f "$profile"' EXIT

GOWORK=off go test -race -covermode=atomic -coverpkg="$cover_packages" -coverprofile="$profile" "${packages[@]}"
coverage="$(GOWORK=off go tool cover -func="$profile" | awk '/^total:/ {gsub(/%/, "", $3); print $3}')"
if [[ "$coverage" != "100.0" ]]; then
  printf 'Central unit coverage must be 100.0%%; got %s%%\n' "$coverage" >&2
  GOWORK=off go tool cover -func="$profile" | awk '$3 != "100.0%"'
  exit 1
fi
printf 'Central unit coverage: %s%%\n' "$coverage"
