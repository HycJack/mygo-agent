#!/bin/bash
# Import rules from spec/architecture.md. A rule break is a spec break.
set -e
cd "$(dirname "$0")/.."
fail() { echo "LAYER VIOLATION: $1" >&2; exit 1; }
deps() { go list -deps "$1" 2>/dev/null | grep '^mygo-agent/' || true; }

# The harness imports neither the host/UI nor the providers.
for d in $(deps ./internal/harness); do
  case "$d" in
    mygo-agent/internal/app|mygo-agent/internal/providers/*) fail "$d imported by internal/harness" ;;
  esac
done

# The harness adapters (subpackages) import the harness root and cli,
# never the host or the providers.
for d in internal/harness/*/; do
  d="${d%/}"
  for dep in $(deps "./$d"); do
    case "$dep" in
      mygo-agent/internal/app|mygo-agent/internal/providers/*) fail "$dep imported by $d" ;;
    esac
  done
done

# internal/ui imports the harness types and the toolkit, never the host.
for d in $(deps ./internal/ui); do
  case "$d" in
    mygo-agent/internal/app|mygo-agent/internal/providers/*) fail "$d imported by internal/ui" ;;
  esac
done

# Providers import only the harness.
for d in $(deps ./internal/providers/sandbox); do
  case "$d" in
    mygo-agent/internal/providers/sandbox) ;; # itself
    mygo-agent/internal/app|mygo-agent/internal/providers/*) fail "$d imported by providers" ;;
  esac
done
echo "layers ok"
