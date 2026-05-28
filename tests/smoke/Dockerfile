# syntax=docker/dockerfile:1
#
# Multi-distro smoke image. Pick the runtime base with:
#   --build-arg DISTRO=debian|ubuntu|alpine|arch|fedora
ARG DISTRO=debian

# ---- build stage: static binary, shared across all distros --------------------
FROM docker.io/library/golang:1.25-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
RUN CGO_ENABLED=0 go build -trimpath -ldflags='-s -w -X main.version=smoke' \
      -o /out/stdssh ./cmd/stdssh

# ---- runtime variants ---------------------------------------------------------
FROM docker.io/library/debian:bookworm-slim AS runtime-debian
ENV DEBIAN_FRONTEND=noninteractive
RUN apt-get update && apt-get install -y --no-install-recommends \
      openssh-client ca-certificates curl netcat-openbsd socat python3 procps \
    && rm -rf /var/lib/apt/lists/*

FROM docker.io/library/ubuntu:24.04 AS runtime-ubuntu
ENV DEBIAN_FRONTEND=noninteractive
RUN apt-get update && apt-get install -y --no-install-recommends \
      openssh-client ca-certificates curl netcat-openbsd socat python3 procps \
    && rm -rf /var/lib/apt/lists/*

FROM docker.io/library/alpine:3 AS runtime-alpine
RUN apk add --no-cache \
      bash openssh-client-default openssh-keygen \
      ca-certificates curl socat netcat-openbsd python3 ncurses coreutils \
      diffutils grep sed

FROM docker.io/library/archlinux:base AS runtime-arch
RUN pacman -Syu --noconfirm \
      openssh curl openbsd-netcat socat python ncurses ca-certificates \
      diffutils \
    && rm -rf /var/cache/pacman/pkg/* /var/lib/pacman/sync/*

FROM docker.io/library/fedora:40 AS runtime-fedora
RUN dnf -y install \
      openssh-clients ca-certificates curl nmap-ncat socat python3 ncurses \
      procps-ng diffutils \
    && dnf clean all

# ---- final stage --------------------------------------------------------------
FROM runtime-${DISTRO} AS final
COPY --from=build /out/stdssh /usr/local/bin/stdssh
COPY tests/smoke/inside.sh /usr/local/bin/run-smoke
COPY tests/smoke/lib       /smoke/lib
COPY tests/smoke/cases     /smoke/cases

ENV HOME=/root
WORKDIR /smoke
RUN mkdir -p /root/.ssh && chmod 700 /root/.ssh

CMD ["bash", "/usr/local/bin/run-smoke"]
