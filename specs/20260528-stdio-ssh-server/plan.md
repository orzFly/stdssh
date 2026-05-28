# Execution plan

Each phase ends with a verifiable smoke test. Commit at every phase boundary
using the conventional prefix in `AGENTS.md`.

## Phase 0 — dependencies

1. Add deps:
   ```bash
   go get golang.org/x/crypto/ssh
   go get github.com/pkg/sftp
   go get github.com/creack/pty
   go get golang.org/x/sys/unix
   go get golang.org/x/crypto/hkdf
   go mod tidy
   ```
2. Verify: `go build ./...`.
3. Commit: `vendor: add ssh, sftp, pty, hkdf deps`.

## Phase 1 — `internal/stdioconn`

Files: `internal/stdioconn/conn.go`, `internal/stdioconn/conn_test.go`.

1. Implement `Conn` struct with `os.Stdin`/`os.Stdout` (or injected
   `io.ReadCloser`/`io.WriteCloser` for tests).
2. Implement `net.Conn` surface; `LocalAddr`/`RemoteAddr` return
   `stdioAddr{}`; deadlines are no-ops.
3. `Close` closes stdout then stdin (order matters for flushing).
4. Tests: round-trip Read/Write via pipes; Close idempotent; Close on stdin
   side propagates EOF to Read.
5. Verify: `go test ./internal/stdioconn/...`.
6. Commit: `feat(stdioconn): net.Conn over stdin/stdout`.

## Phase 2 — `internal/hostkey`

Files: `internal/hostkey/hostkey.go`, `internal/hostkey/hostkey_test.go`.

1. `FromSeed(seed string) (ssh.Signer, error)`:
   - HKDF-SHA256, info=`"stdssh hostkey ed25519 v1"`, expand 32 bytes.
   - `ed25519.NewKeyFromSeed(...)`, wrap via `ssh.NewSignerFromKey`.
2. `LoadOrCreate(path string) (ssh.Signer, error)`:
   - If file exists: read PEM, parse via `ssh.ParseRawPrivateKey`, wrap.
   - Else: generate ed25519, marshal to PEM (`ssh.MarshalPrivateKey`),
     `os.MkdirAll(dir, 0700)`, `os.WriteFile(path, pem, 0600)`.
3. Tests:
   - `FromSeed("foo")` deterministic across two calls (same public key bytes).
   - `LoadOrCreate` round-trip in a `t.TempDir()`.
   - `LoadOrCreate` rejects world-readable files? (Defer; not in v1.)
4. Verify: `go test ./internal/hostkey/...`.
5. Commit: `feat(hostkey): file + deterministic seed loaders`.

## Phase 3 — minimal `internal/server` + `cmd/stdssh` skeleton

Goal: a process that completes an SSH handshake over stdio, accepts any
client, then closes channels with "session not implemented yet".

1. `internal/server/server.go`:
   - `Config` struct per `api.md`.
   - `Run(ctx, conn, cfg)`:
     - Build `*ssh.ServerConfig` with `NoClientAuth: true` and `cfg.HostKey`.
     - `ssh.NewServerConn(conn, sc)`.
     - Goroutine A: drain `reqs` (reject all for now).
     - Goroutine B: drain `chans`, reject everything with
       `ssh.UnknownChannelType, "not implemented"`.
     - `serverConn.Wait()` and return.
2. `cmd/stdssh/main.go`:
   - Replace `Hello, World` body.
   - `flag` package: `--hostkey`, `--hostkey-seed`, `--log-level`, `--shell`,
     `--no-pty`, `--no-sftp`, `--no-forward`, `--no-agent-forward`,
     `--version`.
   - Validate exactly one of `--hostkey` / `--hostkey-seed`.
   - Build `slog.Logger` to stderr at requested level; redirect `log` package's
     default writer to stderr.
   - Build `server.Config`, call `server.Run(ctx, stdioconn.New(), cfg)`.
   - Wire `os.Interrupt`/`SIGTERM` to cancel ctx.
3. Smoke test:
   ```bash
   go build -o /tmp/stdssh ./cmd/stdssh
   ssh -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null \
       -o ProxyCommand='/tmp/stdssh --hostkey-seed test' fake@fake true 2>&1 | head
   ```
   Expect: handshake completes, then a "channel rejected" / exit-status-127
   style failure from the client. The handshake succeeding is the win.
4. Commit: `feat: minimal stdio ssh server skeleton`.

## Phase 4 — `internal/session` (exec + shell, no PTY)

Files: `internal/session/session.go`, `internal/session/env.go`,
`internal/session/exec.go`.

1. Channel handler routine per spec §7 (without PTY for now):
   - Accept `"session"`.
   - Loop on `ch.Requests`:
     - `env`: filter and accumulate.
     - `shell`: launch `$SHELL -l` with composed env, wire stdin/stdout/stderr
       to the channel; on exit send `exit-status` and close.
     - `exec`: same but `$SHELL -c <payload>`.
     - `signal`: forward to child.
     - else: reply `false`.
2. Env composer: process env + uid-derived `HOME`/`USER`/`LOGNAME`/`SHELL` +
   client env (filtered).
3. Wire into `internal/server` channel demux.
4. Smoke test:
   ```bash
   ssh ... 'echo hello && uname -a'
   ssh ... 'cat > /tmp/x' < /etc/hostname
   ```
5. Commit: `feat(session): exec and shell handlers without pty`.

## Phase 5 — PTY + window-change

Files: `internal/session/pty.go`.

1. Capture `pty-req` payload (term, cols, rows, modes blob).
2. On `shell`/`exec` *with* a stashed `pty-req`: use `pty.Start(cmd)`; copy
   `channel ↔ master`; set `TERM` env from req; apply termios modes via
   `unix.IoctlSetTermios` (best-effort; ignore unknown opcodes).
3. `window-change`: `pty.Setsize(master, &pty.Winsize{Rows, Cols, X, Y})`.
4. `--no-pty`: reply `false` to `pty-req`.
5. Smoke test:
   ```bash
   ssh -tt ... 'tput cols; tput lines'
   # in a real terminal, resize the window mid-session and check `tput cols`.
   ```
6. Commit: `feat(session): pty allocation and resize`.

## Phase 6 — SFTP subsystem

Files: `internal/sftp/sftp.go`.

1. `Serve(ctx, ch)`: `srv, _ := sftp.NewServer(ch); srv.Serve()`; close on
   ctx cancel.
2. In session handler, on `subsystem "sftp"` (and not `--no-sftp`): accept the
   request, hand the channel to `sftp.Serve`.
3. Smoke test:
   ```bash
   sftp -o ProxyCommand=... fake@fake <<<'ls /etc'
   scp -o ProxyCommand=... fake@fake:/etc/hostname /tmp/h
   ```
4. Commit: `feat(sftp): subsystem handler`.

## Phase 7 — `direct-tcpip` (`-L`, `-D`)

Files: `internal/forward/direct.go`.

1. Handler per spec §9.1: parse payload, `net.Dial`, accept channel, proxy
   bytes both ways until close.
2. Hook into `internal/server` channel demux for type `"direct-tcpip"`.
3. `--no-forward`: reject the channel with
   `ssh.Prohibited, "forwarding disabled"`.
4. Smoke test:
   ```bash
   # In the pod (or local proxycommand): a known reachable target.
   ssh -N -L 18080:127.0.0.1:80 -o ProxyCommand=... fake@fake &
   curl -sv http://127.0.0.1:18080/  # should reach the pod's :80
   kill %1
   ```
5. Commit: `feat(forward): -L / direct-tcpip`.

## Phase 8 — `-R` (tcpip-forward + forwarded-tcpip)

Files: `internal/forward/listener.go`.

1. `Manager` per spec §9.2. Listener map keyed by `bind-addr|port`.
2. Wire into `internal/server` global-request dispatch.
3. On `serverConn.Wait()` return: `mgr.Close()` to drop listeners.
4. Smoke test:
   ```bash
   ssh -N -R 18181:127.0.0.1:9999 -o ProxyCommand=... fake@fake &
   # in another shell, run on the local side: `nc -l 127.0.0.1 9999`
   # in the pod: `curl http://127.0.0.1:18181` — should reach local nc
   kill %1
   ```
5. Commit: `feat(forward): -R / tcpip-forward`.

## Phase 9 — agent forwarding

Files: `internal/agentfwd/agentfwd.go`.

1. `Bind` per spec §10: pick socket path, `net.Listen("unix", ...)`,
   accept-loop opens `"auth-agent@openssh.com"` channels back to the client.
2. In session handler, on `auth-agent-req@openssh.com` (and not
   `--no-agent-forward`): call `Bind`, add `SSH_AUTH_SOCK=<path>` to the env
   composer, defer cleanup on session close.
3. Smoke test:
   ```bash
   eval "$(ssh-agent -s)"; ssh-add ~/.ssh/id_ed25519
   ssh -A -o ProxyCommand=... fake@fake 'ssh-add -l'
   ```
4. Commit: `feat(agentfwd): SSH_AUTH_SOCK bridge`.

## Phase 10 — polish & docs

1. `--version` returns `git describe`-derived string injected via
   `-ldflags="-X main.version=..."`.
2. Update `AGENTS.md` if anything has shifted.
3. Add `README.md` with one paragraph + the two invocation examples from
   `proposal.md`.
4. `gofmt -w . && go vet ./... && go test ./...` clean.
5. Final commit: `docs: README and version stamping`.

## Out of scope (defer to a future spec)

- `direct-streamlocal@openssh.com` (`-L` over unix sockets).
- `streamlocal-forward@openssh.com` (`-R` over unix sockets).
- X11 forwarding.
- Keepalive global requests (the SSH client drives keepalive in our use case).
- Connection multiplexing / control sockets.
- A managed agent (vs forwarding) inside the pod.
