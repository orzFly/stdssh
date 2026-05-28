# stdssh

`stdssh` is an SSH server that speaks the SSH protocol over its inherited
stdin/stdout — no listening socket, no auth, no PAM. It is meant to be invoked
through `kubectl exec` (or any other stdio pipe) so an existing `ssh` client
can drive a remote pod with all the usual amenities: `exec`, interactive shell
with PTY, SFTP, `-L` / `-R` port forwarding, and SSH agent forwarding. Access
is gated entirely by whatever already controls the underlying stdio (e.g.
Kubernetes RBAC on `pods/exec`).

## Usage

Through `kubectl exec`:

```bash
ssh \
  -o ProxyCommand="kubectl exec <pod> -c <container> -i -- stdssh --hostkey-seed <pod-uid>" \
  fake@fake
```

Local development (no kubectl needed — `ssh`'s `ProxyCommand` always uses
stdio, so it can spawn the binary directly):

```bash
ssh -o ProxyCommand="./stdssh --hostkey ./dev_hostkey" fake@fake
```

## Flags

| Flag | Meaning |
|---|---|
| `--hostkey <path>` | PEM private key. Generated (ed25519, mode 0600) on first use if the file does not exist. |
| `--hostkey-seed <string>` | Derive a deterministic ed25519 hostkey from this seed. |
| `--shell <path>` | Override `$SHELL` for `exec` / `shell` sessions. |
| `--log-level error\|warn\|info\|debug` | Stderr log level (default `warn`). |
| `--no-pty` | Reject `pty-req`. |
| `--no-sftp` | Reject the `sftp` subsystem. |
| `--no-forward` | Reject `direct-tcpip` and `tcpip-forward` (disables `-L`, `-R`, `-D`). |
| `--max-forwards <n>` | Maximum concurrent `-R` listeners (`0` = unlimited, default). |
| `--forward-allow <CIDRs>` | Comma-separated CIDRs for allowed `-L` destinations (default: all). |
| `--forward-deny <CIDRs>` | Comma-separated CIDRs to deny for `-L` destinations (takes precedence over allow). |
| `--no-agent-forward` | Reject `auth-agent-req@openssh.com`. |
| `--version` | Print version and exit. |

Exactly one of `--hostkey` or `--hostkey-seed` is required. There is no
`--listen` flag and there will not be one — the binary refuses to bind a
network socket for SSH traffic.

## Build

```bash
go build -trimpath -ldflags "-s -w -X main.version=$(git describe --always --dirty)" ./cmd/stdssh
```

CGO is not required; the result is a single static binary suitable for
`FROM scratch` images or sidecar copy into existing pod images.

## Design

See `specs/20260528-stdio-ssh-server/` — `design.md` for architecture,
`api.md` for the CLI / package surface, `plan.md` for the phased build order.
