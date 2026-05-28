# Proposal: stdio-only SSH server for `kubectl exec`

## Original requirement

> 我想要写一个 SSH server，without any auth & pam & network socket。It only
> accepts transport over stdio, to be used in `kubectl exec`. So others can run
>
> ```
> ssh -o ProxyCommand="kubectl exec <pod> -- mysshserver" fake@fake
> ```
>
> to use any development tool that supports SSH.

## Decisions captured during intake

| Question | Answer |
|---|---|
| Language / library | Go + `golang.org/x/crypto/ssh` |
| Must-have features | SFTP subsystem; `exec` + `shell` + PTY; port forwarding (`-L` / `-R` / `direct-tcpip`); agent forwarding |
| Hostkey handling | Persist on disk in the pod (rootless), **or** derive deterministically from a seed passed via argv |
| `--listen :2222` mode for local dev | **Rejected.** "Never support socket listening — it opens a big hole in security." The binary is stdio-only, full stop. |
| Default hostkey path when neither flag is given | **None.** Operator must pass `--hostkey` or `--hostkey-seed` explicitly. |
| Forwarding & SFTP defaults | All on by default; opt-out via `--no-forward`, `--no-agent-forward`, `--no-sftp`. |

## Non-goals

- No TCP/Unix listening socket for accepting SSH connections.
- No password / publickey / keyboard-interactive / PAM authentication. The server accepts any user, any credentials (the SSH client is gated by `kubectl exec` RBAC).
- No X11 forwarding.
- No multi-connection handling — one process per SSH session, lifetime tied to stdio.

## Usage shape

```
# Production (via kubectl exec):
ssh -o ProxyCommand="kubectl exec <pod> -c <container> -i -- stdssh --hostkey-seed <pod-uid>" fake@fake

# Local development (ProxyCommand spawns the binary directly):
ssh -o ProxyCommand="./stdssh --hostkey ./dev_hostkey" fake@fake
```

No `--listen` flag exists in either case.
