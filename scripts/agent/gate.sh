#!/bin/sh
# Orchestrator merge gate (agent-plan §3): gate.sh WP-NN <branch> [go packages...]
set -e
cd "$(dirname "$0")/../.."
WP=$1; BR=$2; shift 2
git diff --name-only main..."$BR" | scripts/agent/check-ownership "$WP"
git merge --no-ff -q "$BR" -m "Merge $WP

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
if ! { go build ./... 2>&1 | grep -v '^ld: warning\|^#' ; CGO_ENABLED=0 go vet -tags purego ./... && go run ./scripts/layercheck && { [ $# -eq 0 ] || CGO_ENABLED=0 go test -tags purego -count=1 "$@"; }; }; then
  echo "GATE FAILED for $WP: reverting merge"; git reset -q --keep HEAD~1; exit 1
fi
echo "GATE OK: $WP merged"
