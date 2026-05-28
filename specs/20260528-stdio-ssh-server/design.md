# Design: stdio-only SSH server

## 1. High-level architecture

SSH is transport-agnostic; `golang.org/x/crypto/ssh.NewServerConn` accepts any
`net.Conn`. We wrap `os.Stdin`/`os.Stdout` into a synthetic `net.Conn` and feed
it directly to the SSH state machine. There is no listener, no `Accept` loop,
no concurrency at the connection level — one process, one connection, lifetime
of the SSH transport equals lifetime of the process.

```
                 +----------------------+        +-----------------------+
ssh client  <==> | kubectl exec stdio   |  <==>  | stdssh                |
                 | (multiplexed frames) |        |  stdin  -> ssh.Conn   |
                 +----------------------+        |  stdout <- ssh.Conn   |
                                                 |  stderr  -> log only  |
                                                 +-----------------------+
```

**Invariant:** `stdout` is sacred. It carries the SSH binary protocol; nothing
else may write to it. All logging goes to `stderr`. (See §6 for enforcement.)

## 2. Components and package layout

```
cmd/stdssh/
  main.go            -- flag parsing, hostkey load, wire-up, run
internal/
  stdioconn/         -- net.Conn façade over os.Stdin/os.Stdout
  hostkey/           -- load from file, derive from seed, generate-and-save
  server/            -- ssh.ServerConfig assembly + channel/request demux loop
  session/           -- "session" channel: exec, shell, pty-req, window-change,
                        env, signal, exit-status, subsystem dispatch
  sftp/              -- subsystem "sftp" handler (delegates to pkg/sftp)
  forward/           -- direct-tcpip (-L), tcpip-forward + forwarded-tcpip (-R)
  agentfwd/          -- auth-agent-req@openssh.com + unix-socket bridge
```

`cmd/stdssh/main.go` stays thin (flag parsing + composition). `internal/*`
packages have no knowledge of each other beyond small interfaces — they are
plugged together in `internal/server`.

## 3. Stdio as `net.Conn`

`internal/stdioconn.Conn` implements `net.Conn`:

| Method | Implementation |
|---|---|
| `Read(p)` | Read from `os.Stdin`. |
| `Write(p)` | Write to `os.Stdout`. |
| `Close()` | Close both; signals EOF to the SSH transport. |
| `LocalAddr` / `RemoteAddr` | Return a dummy `stdioAddr{name: "stdio"}`. |
| `SetDeadline*` | Return `nil` (no-op). `x/crypto/ssh` uses these but tolerates no-op. |

The `Read` side honors EOF correctly: when stdin closes (kubectl exec
disconnect, parent dies), `ServerConn.Wait` returns and the process exits.

## 4. Hostkey

Two mutually-exclusive flags, **at least one required**:

- `--hostkey <path>`: read PEM-encoded private key from disk. If the file
  doesn't exist, generate an ed25519 key, write it (mode `0600`, create parent
  dir if needed), and use it. On subsequent runs the same key is loaded —
  giving a stable host identity as long as the underlying volume persists.
- `--hostkey-seed <string>`: derive a deterministic ed25519 key from the seed.

  ```
  seed32 = HKDF-SHA256(secret=[]byte(seedArg), salt=nil, info="stdssh hostkey ed25519 v1")[:32]
  priv   = ed25519.NewKeyFromSeed(seed32)
  ```

  Same seed → same key → no host-key-mismatch warnings across pod restarts,
  even when no PV is attached. Suggested seed source: pod UID, deployment UID,
  or a per-tenant secret injected via downward API.

If both flags are passed, `main` errors out before opening stdio. If neither,
same — explicit failure beats silent default.

## 5. SSH server config

```go
config := &ssh.ServerConfig{
    NoClientAuth: true,
    ServerVersion: "SSH-2.0-stdssh_" + version, // RFC 4253 banner
}
config.AddHostKey(loadedKey)
```

Algorithms: use library defaults (modern: curve25519, chacha20-poly1305,
ed25519). No custom suite — the SSH client (openssh) negotiates safely.

## 6. Logging discipline

- One small `log/slog` `*slog.Logger` constructed at startup, writing to
  `os.Stderr` only.
- The standard `log` package's default writer is rerouted to stderr at startup
  to catch any stray library logs.
- `fmt.Println` and friends are banned in code review; CI lint can grep for
  them later.

Verbosity flag: `--log-level={error,warn,info,debug}`, default `info`.

## 7. Session channel (`internal/session`)

For each `ssh.NewChannel` of type `"session"`:

1. Accept the channel.
2. Spawn a per-channel goroutine to handle requests serially.
3. Process requests in order:
   - `env`: capture name=value pairs for later `exec`/`shell`/`subsystem` use.
     **Filter**: only allow names matching `^[A-Z_][A-Z0-9_]*$` and reject
     `LD_*`, `DYLD_*`, `PATH`, `HOME`, `USER`, `SHELL` (we control those).
   - `pty-req`: parse term name, dims, modes. Stash a `*ptyReq`; don't allocate
     the actual PTY yet — wait for `shell`/`exec` so we know what to attach to.
   - `window-change`: if a PTY exists, call `pty.Setsize`.
   - `shell`: launch `$SHELL` (fallback `/bin/sh`) `-l`.
   - `exec`: parse the single string payload as a command, run via
     `$SHELL -c "<payload>"` (matches openssh sshd behavior — the client sends
     a shell-quoted blob, the server must shell-parse it).
   - `subsystem`: dispatch on name. `"sftp"` → `internal/sftp`. Unknown →
     reject (`false`).
   - `signal`: forward to the running child (`cmd.Process.Signal`).
   - `exit-status` / `exit-signal`: sent by server when child exits, then close
     channel.

PTY allocation uses `github.com/creack/pty`:
- `pty.Start(cmd)` returns the master file.
- Bidirectional copy: `channel <-> ptyMaster`.
- On `window-change`: `pty.Setsize(master, &pty.Winsize{...})`.
- Set initial modes from the `pty-req` payload (echo, icanon, etc.) via
  `termios` syscalls — `pkg/term/termios` or hand-rolled with `unix.IoctlSetTermios`.

Without PTY:
- `cmd.Stdin = channel`
- `cmd.Stdout = channel`
- `cmd.Stderr = channel.Stderr()` (SSH session has a separate stderr stream).

Environment composition for the child:
- Start from the binary's own `os.Environ()`.
- Set `HOME`, `USER`, `LOGNAME`, `SHELL` from the process's current uid (via
  `os/user.Current`).
- Set `PATH` to a sane default if the binary's `PATH` is empty.
- Layer in filtered `env` requests last.
- If a PTY was allocated, set `TERM` from the `pty-req` term name.
- Chdir the child to `$HOME` if it exists, else stay in cwd.

## 8. SFTP subsystem (`internal/sftp`)

`github.com/pkg/sftp` provides a ready server:

```go
srv, err := sftp.NewServer(channel)  // channel implements io.ReadWriter
srv.Serve()
```

No chroot, no path filtering — the SFTP server inherits process credentials
(typical container `uid`). If the operator wants to restrict, run the
container as a less-privileged user.

Disabled when `--no-sftp` is passed: subsystem request returns `false`.

## 9. Port forwarding (`internal/forward`)

### 9.1 `-L` and explicit `direct-tcpip` (always client-initiated)

Channel type `"direct-tcpip"`. Payload (per RFC 4254 §7.2):
`host:port` to connect to, plus originator info.

Handler:
1. Parse payload.
2. `net.Dial("tcp", host:port)` (or unix for `direct-streamlocal@openssh.com`
   if we extend later — out of scope v1).
3. On dial failure, reject channel with `ssh.ConnectionFailed`.
4. On success, accept channel and proxy bytes both ways until either side
   closes.

No new process, no listener — just an outbound dial. Disabled when
`--no-forward`.

### 9.2 `-R` (server-side listener)

Client sends a global request `"tcpip-forward"` with `bind-addr` + `bind-port`.

Handler:
1. `net.Listen("tcp", bind-addr:bind-port)`. If `bind-port == 0`, use the OS-
   assigned port and return it in the global-request reply payload.
2. Track active listeners in a map keyed by `(bind-addr, bind-port)`.
3. On each `Accept`, open a `"forwarded-tcpip"` channel back to the client
   with the originator's address; proxy bytes both ways.
4. On `"cancel-tcpip-forward"` global request, close the matching listener.
5. On SSH connection close, close all listeners.

This **does** open inbound TCP listeners inside the pod. That is part of the
feature ("-R" semantics) — distinct from a server-side SSH listener. Operators
who don't want it pass `--no-forward`.

### 9.3 `-D` (SOCKS, client-side)

Pure client feature, handled via `direct-tcpip` channels — no server change
needed.

## 10. Agent forwarding (`internal/agentfwd`)

Client sends channel request `"auth-agent-req@openssh.com"` on a session
channel. On accept:

1. Create a unique unix socket under `$XDG_RUNTIME_DIR/stdssh/<random>/agent.sock`
   (fallback `/tmp/stdssh-<random>/agent.sock`, dir mode `0700`, socket mode
   `0600`).
2. Set `SSH_AUTH_SOCK=<that path>` in the child env composition (§7).
3. Spawn a goroutine: `Accept` on the socket; for each accepted connection,
   open a `"auth-agent@openssh.com"` channel back to the client and proxy bytes
   both ways.
4. On session channel close: close listener, remove socket file, remove parent dir.

Disabled when `--no-agent-forward`.

## 11. Concurrency model

- Main goroutine: `ssh.NewServerConn(stdio, config)` → returns `serverConn`,
  `chans`, `reqs`.
- Goroutine A: range over `reqs`, dispatch global requests (forwarding,
  keepalive).
- Goroutine B: range over `chans`, dispatch channel-open to per-channel
  handlers.
- Per channel: one goroutine running `ssh.Channel.Requests` consumer plus
  worker goroutines for I/O copy.
- Process lifetime: wait on `serverConn.Wait()`; when it returns, cancel a
  root `context.Context`, give in-flight handlers a short grace period
  (~2s), then exit.

## 12. Dependencies (third-party)

| Module | Why |
|---|---|
| `golang.org/x/crypto/ssh` | SSH protocol implementation. |
| `github.com/pkg/sftp` | RFC-compliant SFTP server we'd otherwise reimplement. |
| `github.com/creack/pty` | Cross-platform PTY allocation; openssh-equivalent on Linux. |
| `golang.org/x/sys` | Termios for PTY modes; ioctl. |
| `golang.org/x/crypto/hkdf` | Deterministic hostkey derivation. |

No external auth, no PAM, no PAM-shim, no glibc requirement (CGO_ENABLED=0
target — `creack/pty` is pure-Go on Linux).

## 13. Build & ship

- `CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' ./cmd/stdssh`.
- Single static binary, scratch-friendly. Drop into any container image:

  ```
  FROM scratch
  COPY stdssh /stdssh
  ENTRYPOINT ["/stdssh"]
  ```

  (Realistically it'll be added as a sidecar binary inside an existing image,
  not its own image — but it being scratch-clean makes that easy.)

## 14. Risks and open mitigations

1. **kubectl exec frame interleaving.** kubectl's SPDY/websocket multiplexes
   stdin/stdout/stderr; if a buggy version emits stderr bytes on stdout, the
   SSH stream corrupts. Mitigation: document `-c <container>` and `kubectl
   --v=6` for diagnosis; we cannot defend against a misbehaving kubectl.
2. **Half-close handling.** `os.Stdin` doesn't naturally signal `CloseWrite`
   to the peer. The SSH transport's own close path handles this — but we must
   ensure `stdioconn.Close` closes stdout *before* stdin to flush in-flight
   protocol messages.
3. **PTY modes.** Translating SSH terminal mode opcodes (RFC 4254 §8) to
   `termios` is fiddly. v1 sets a sensible default (raw-ish, ICANON off when
   the client says so) and ignores opcodes we don't recognize.
4. **`-R` listener cleanup.** A crashing client must not leak listeners. The
   goroutine watching `serverConn.Wait()` closes the listener map on exit
   (§11).
5. **No rate limiting / no resource caps.** A misbehaving client can open
   many channels. Acceptable — the binary is gated by `kubectl exec` RBAC, and
   the pod's cgroup limits CPU/memory.

## 15. Testing strategy

- Unit tests per package where pure-Go logic is involved (`stdioconn`,
  `hostkey` seed derivation determinism).
- Integration smoke tests in `tests/` driving the binary via `ssh -o
  ProxyCommand=./stdssh fake@fake <cmd>` — exercises `exec`, `shell` (with
  `-tt`), `sftp`, `scp`, `-L`, `-R`, `-A`. These are shell-script tests run
  manually in dev; not wired into CI in v1.
- No fuzz tests in v1.
