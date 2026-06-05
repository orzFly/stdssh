# syntax=docker/dockerfile:1
#
# Multi-distro smoke image. Pick the runtime base with:
#   --build-arg DISTRO=debian|ubuntu|alpine|arch|fedora
#
# stdssh is installed from the release packages GoReleaser produces (deb/rpm/apk)
# so the smoke suite exercises the same artifact users install. Run
# `goreleaser release --snapshot --clean` first so dist/ holds the packages; the
# build context copies them in. Arch has no nfpm package format, so it falls back
# to the statically-built binary.
ARG DISTRO=debian
# Package arch suffix in the GoReleaser file names (amd64 / arm64). Docker sets
# TARGETARCH automatically; default keeps a plain `docker build` working.
ARG TARGETARCH=amd64

# ---- build stage: static binary, used by the arch fallback --------------------
FROM docker.io/library/golang:1.25-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
RUN CGO_ENABLED=0 go build -trimpath -ldflags='-s -w -X main.version=smoke' \
      -o /out/stdssh ./cmd/stdssh

# ---- runtime variants ---------------------------------------------------------
# Each variant installs openssh + test deps, then installs the stdssh package
# for its family. The package lands the binary at /usr/bin/stdssh.

FROM docker.io/library/debian:bookworm-slim AS runtime-debian
ARG TARGETARCH
ENV DEBIAN_FRONTEND=noninteractive
RUN apt-get update && apt-get install -y --no-install-recommends \
      openssh-client ca-certificates curl netcat-openbsd socat python3 procps rsync \
    && rm -rf /var/lib/apt/lists/*
COPY dist/ /pkgs/
RUN apt-get install -y /pkgs/stdssh_*_linux_${TARGETARCH}.deb

FROM docker.io/library/ubuntu:24.04 AS runtime-ubuntu
ARG TARGETARCH
ENV DEBIAN_FRONTEND=noninteractive
RUN apt-get update && apt-get install -y --no-install-recommends \
      openssh-client ca-certificates curl netcat-openbsd socat python3 procps rsync \
    && rm -rf /var/lib/apt/lists/*
COPY dist/ /pkgs/
RUN apt-get install -y /pkgs/stdssh_*_linux_${TARGETARCH}.deb

FROM docker.io/library/alpine:3 AS runtime-alpine
ARG TARGETARCH
RUN apk add --no-cache \
      bash openssh-client-default openssh-keygen \
      ca-certificates curl socat netcat-openbsd python3 ncurses coreutils \
      diffutils grep sed rsync
COPY dist/ /pkgs/
# Snapshot .apk files are unsigned; --allow-untrusted accepts them.
RUN apk add --no-cache --allow-untrusted /pkgs/stdssh_*_linux_${TARGETARCH}.apk

FROM docker.io/library/archlinux:base AS runtime-arch
RUN pacman -Syu --noconfirm \
      openssh curl openbsd-netcat socat python ncurses ca-certificates \
      diffutils rsync \
    && rm -rf /var/cache/pacman/pkg/* /var/lib/pacman/sync/*
# Arch has no nfpm package format; use the statically-built binary.
COPY --from=build /out/stdssh /usr/bin/stdssh

FROM docker.io/library/fedora:40 AS runtime-fedora
ARG TARGETARCH
RUN dnf -y install \
      openssh-clients ca-certificates curl nmap-ncat socat python3 ncurses \
      procps-ng diffutils rsync \
    && dnf clean all
COPY dist/ /pkgs/
RUN rpm -i /pkgs/stdssh_*_linux_${TARGETARCH}.rpm

# ---- final stage --------------------------------------------------------------
FROM runtime-${DISTRO} AS final
COPY tests/smoke/inside.sh /usr/local/bin/run-smoke
COPY tests/smoke/lib       /smoke/lib
COPY tests/smoke/cases     /smoke/cases

ENV HOME=/root
WORKDIR /smoke
RUN mkdir -p /root/.ssh && chmod 700 /root/.ssh

CMD ["bash", "/usr/local/bin/run-smoke"]
