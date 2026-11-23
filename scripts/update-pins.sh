#!/usr/bin/env bash
# update-pins.sh - pin the container images used by .forgejo/workflows to their current digests.
#
#   ./scripts/update-pins.sh --dry-run   print the diff, change nothing (safe anywhere)
#   ./scripts/update-pins.sh             rewrite the image: lines in place
#
# Each `image: <repo>:<tag>` line for a known image becomes `image: <repo>@sha256:<digest>`, and the trailing
# "pin by digest" comment is dropped. The digest is read from the registry (HEAD on the manifest, the
# Docker-Content-Digest header), so no docker, crane or skopeo is needed; curl is.
#
# Auth: quay.io pulls are anonymous (a pull token is fetched). The Cuisle CI registry (github.com) uses
# REGISTRY_USER and REGISTRY_TOKEN when they are set, read from the environment and passed to curl on stdin
# (never on the command line). Run this by hand; it is never run automatically.
#
# EXIT  0 ok | 2 usage | 3 one or more digests could not be resolved (nothing was written for those images)
set -Eeuo pipefail

DRY_RUN=0
case "${1:-}" in
  --dry-run) DRY_RUN=1 ;;
  "") ;;
  -h|--help) sed -n '2,15p' "$0" | sed 's/^# \{0,1\}//'; exit 0 ;;
  *) echo "unknown argument: $1" >&2; exit 2 ;;
esac

ROOT=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)
cd "$ROOT"

# Images referenced by the workflows. Keep in step with build/ci/linux.Dockerfile and ci-image.yml.
IMAGES=(
  "quay.io/buildah/stable:latest"
  "github.com/crueber/cuisle-ci:latest"
)
WORKFLOWS=(.forgejo/workflows/ci.yml .forgejo/workflows/ci-image.yml)
MEDIA='application/vnd.oci.image.index.v1+json,application/vnd.docker.distribution.manifest.list.v2+json,application/vnd.oci.image.manifest.v1+json,application/vnd.docker.distribution.manifest.v2+json'

# digest_of <host>/<repo>:<tag>  -> prints sha256:<hex>, or returns 1
digest_of() {
  local ref=$1 host rest repo tag token="" url digest
  host=${ref%%/*}
  rest=${ref#*/}
  repo=${rest%:*}
  tag=${rest##*:}
  url="https://${host}/v2/${repo}/manifests/${tag}"

  if [ "$host" = "quay.io" ]; then
    token=$(curl -fsSL "https://quay.io/v2/auth?service=quay.io&scope=repository:${repo}:pull" 2>/dev/null \
      | sed -n 's/.*"token" *: *"\([^"]*\)".*/\1/p' | head -n1) || true
    [ -n "$token" ] || { echo "  quay.io did not issue a pull token for $repo" >&2; return 1; }
    digest=$(curl -fsSI -H "Accept: $MEDIA" -H "Authorization: Bearer $token" "$url" 2>/dev/null \
      | tr -d '\r' | sed -n 's/^[Dd]ocker-[Cc]ontent-[Dd]igest: *//p' | head -n1) || true
  elif [ -n "${REGISTRY_USER:-}" ] && [ -n "${REGISTRY_TOKEN:-}" ]; then
    digest=$(printf 'user = "%s:%s"\n' "$REGISTRY_USER" "$REGISTRY_TOKEN" \
      | curl -K - -fsSI -H "Accept: $MEDIA" "$url" 2>/dev/null \
      | tr -d '\r' | sed -n 's/^[Dd]ocker-[Cc]ontent-[Dd]igest: *//p' | head -n1) || true
  else
    digest=$(curl -fsSI -H "Accept: $MEDIA" "$url" 2>/dev/null \
      | tr -d '\r' | sed -n 's/^[Dd]ocker-[Cc]ontent-[Dd]igest: *//p' | head -n1) || true
  fi

  if ! printf '%s' "$digest" | grep -Eq '^sha256:[0-9a-f]{64}$'; then
    echo "  could not resolve a digest for $ref (is it published yet? private registry needs REGISTRY_USER/REGISTRY_TOKEN)" >&2
    return 1
  fi
  printf '%s\n' "$digest"
}

# escape regex metacharacters in an image repo for sed
re_escape() { printf '%s' "$1" | sed 's/[.[\*^$/]/\\&/g'; }

status=0
TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT

# 1. resolve every digest first, so a partial failure never leaves a half-pinned set
declare -a PINNED=()
for img in "${IMAGES[@]}"; do
  repo=${img%:*}
  if d=$(digest_of "$img"); then
    PINNED+=("$repo=$d")
    echo "resolved $repo -> $d"
  else
    status=3
  fi
done

# 2. rewrite each workflow into a temp file, then show or install the change
for wf in "${WORKFLOWS[@]}"; do
  [ -f "$wf" ] || { echo "missing $wf" >&2; status=3; continue; }
  out="$TMP/$(basename "$wf")"
  cp "$wf" "$out"
  for pin in "${PINNED[@]}"; do
    repo=${pin%%=*}
    digest=${pin#*=}
    esc=$(re_escape "$repo")
    # matches a tag (repo:tag) or an existing pin (repo@sha256:...), so a re-run moves the pin forward
    sed -E -i.bak "s%^([[:space:]]*image:[[:space:]]*)${esc}(:[A-Za-z0-9._-]+|@sha256:[0-9a-f]{64})([[:space:]]*#.*)?[[:space:]]*\$%\1${repo}@${digest}%" "$out"
    rm -f "$out.bak"
  done
  if diff -u "$wf" "$out" > "$TMP/$(basename "$wf").diff"; then
    echo "$wf: no change"
    continue
  fi
  cat "$TMP/$(basename "$wf").diff"
  if [ $DRY_RUN -eq 0 ]; then
    cp "$out" "$wf"
    echo "$wf: updated"
  fi
done

if [ $DRY_RUN -eq 1 ]; then
  echo "dry run: nothing written"
fi
exit $status
