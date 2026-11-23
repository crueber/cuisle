# Cuisle Linux CI/build image (used by .forgejo/workflows/ci.yml; built by ci-image.yml).
# Ubuntu 22.04 base for glibc compatibility of the shipped binary (docs/architecture.md §6.5).
# Versions match ADR 0001: keep WAILS_VERSION identical to go.mod.
FROM ubuntu:22.04

ARG GO_VERSION=1.27.4
ARG NODE_MAJOR=24
ARG TASK_VERSION=v3.54.0
ARG WAILS_VERSION=v3.1.0

ENV DEBIAN_FRONTEND=noninteractive APPIMAGE_EXTRACT_AND_RUN=1 PATH=/usr/local/go/bin:/root/go/bin:$PATH

RUN apt-get update && apt-get install -y --no-install-recommends \
      ca-certificates curl wget git build-essential pkg-config xvfb xauth file \
      libgtk-3-dev libwebkit2gtk-4.1-dev libsoup-3.0-dev \
      libsecret-1-0 gnome-keyring dbus-x11 \
    && curl -fsSL "https://go.dev/dl/go${GO_VERSION}.linux-amd64.tar.gz" | tar -C /usr/local -xz \
    && curl -fsSL "https://deb.nodesource.com/setup_${NODE_MAJOR}.x" | bash - \
    && apt-get install -y --no-install-recommends nodejs \
    && go install "github.com/go-task/task/v3/cmd/task@${TASK_VERSION}" \
    && go install -tags gtk3 "github.com/wailsapp/wails/v3/cmd/wails3@${WAILS_VERSION}" \
    && rm -rf /var/lib/apt/lists/*

# Playwright's Chromium for the mock-mode perf/a11y suite (installed lazily by the suite if missing).
WORKDIR /src
