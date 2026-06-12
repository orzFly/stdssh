# syntax=docker/dockerfile:1
#
# Multi-distro smoke image. Pick the runtime base with:
#   --build-arg DISTRO=debian|ubuntu|alpine|arch|fedora
#
# stdssh comes from one of two sources, picked with --build-arg STDSSH_FROM=...:
#   binary  (default) static binary built in the build stage below; needs nothing
#           beyond the build context, so plain `docker build` / run.sh just work.
#   package release package GoReleaser produces (deb/rpm/apk), so the suite
#           exercises the same artifact users install. Run
#           `goreleaser release --snapshot --clean` first so dist/ holds the
#           packages. Arch has no nfpm package format and falls back to the
#           static binary.
ARG DISTRO=debian
ARG STDSSH_FROM=binary
# Package arch suffix in the GoReleaser file names (amd64 / arm64). Docker sets
# TARGETARCH automatically; default keeps a plain `docker build` working.
ARG TARGETARCH=amd64

# ---- build stage: static binary, shared across all distros --------------------
FROM docker.io/library/golang:1.25-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
RUN CGO_ENABLED=0 go build -trimpath -ldflags='-s -w -X main.version=smoke' \
      -o /out/stdssh ./cmd/stdssh

# ---- runtime variants: openssh + test deps only -------------------------------
FROM docker.io/library/debian:bookworm-slim AS runtime-debian
ENV DEBIAN_FRONTEND=noninteractive
RUN apt-get update && apt-get install -y --no-install-recommends \
      openssh-client ca-certificates curl netcat-openbsd socat python3 procps rsync \
    && rm -rf /var/lib/apt/lists/*

FROM docker.io/library/ubuntu:24.04 AS runtime-ubuntu
ENV DEBIAN_FRONTEND=noninteractive
RUN apt-get update && apt-get install -y --no-install-recommends \
      openssh-client ca-certificates curl netcat-openbsd socat python3 procps rsync \
    && rm -rf /var/lib/apt/lists/*

FROM docker.io/library/alpine:3 AS runtime-alpine
RUN apk add --no-cache \
      bash openssh-client-default openssh-keygen \
      ca-certificates curl socat netcat-openbsd python3 ncurses coreutils \
      diffutils grep sed rsync

FROM docker.io/library/archlinux:base AS runtime-arch
RUN pacman -Syu --noconfirm \
      openssh curl openbsd-netcat socat python ncurses ca-certificates \
      diffutils rsync \
    && rm -rf /var/cache/pacman/pkg/* /var/lib/pacman/sync/*

FROM docker.io/library/fedora:40 AS runtime-fedora
RUN dnf -y install \
      openssh-clients ca-certificates curl nmap-ncat socat python3 ncurses \
      procps-ng diffutils rsync \
    && dnf clean all

# ---- package install variants: stdssh from the dist/ release packages ---------
# The package lands the binary at /usr/bin/stdssh. Only built when
# STDSSH_FROM=package, so dist/ may be absent from the context otherwise.
FROM runtime-debian AS package-debian
ARG TARGETARCH
COPY dist/ /pkgs/
RUN apt-get install -y /pkgs/stdssh_*_linux_${TARGETARCH}.deb

FROM runtime-ubuntu AS package-ubuntu
ARG TARGETARCH
COPY dist/ /pkgs/
RUN apt-get install -y /pkgs/stdssh_*_linux_${TARGETARCH}.deb

FROM runtime-alpine AS package-alpine
ARG TARGETARCH
COPY dist/ /pkgs/
# Snapshot .apk files are unsigned; --allow-untrusted accepts them.
RUN apk add --no-cache --allow-untrusted /pkgs/stdssh_*_linux_${TARGETARCH}.apk

# Arch has no nfpm package format; package mode falls back to the static binary.
FROM runtime-arch AS package-arch
COPY --from=build /out/stdssh /usr/bin/stdssh

FROM runtime-fedora AS package-fedora
ARG TARGETARCH
COPY dist/ /pkgs/
RUN rpm -i /pkgs/stdssh_*_linux_${TARGETARCH}.rpm

# ---- install variants, selected by STDSSH_FROM --------------------------------
FROM runtime-${DISTRO} AS binary-install
COPY --from=build /out/stdssh /usr/local/bin/stdssh

FROM package-${DISTRO} AS package-install

# ---- final stage --------------------------------------------------------------
FROM ${STDSSH_FROM}-install AS final
COPY tests/smoke/inside.sh /usr/local/bin/run-smoke
COPY tests/smoke/lib       /smoke/lib
COPY tests/smoke/cases     /smoke/cases

ENV HOME=/root
WORKDIR /smoke
RUN mkdir -p /root/.ssh && chmod 700 /root/.ssh

CMD ["bash", "/usr/local/bin/run-smoke"]
