# API: CLI and internal package surface

## 1. CLI flags

```
stdssh [flags]

Hostkey (exactly one required):
  --hostkey <path>            PEM-encoded private key. Created on first use
                              (ed25519, mode 0600) if the file doesn't exist.
  --hostkey-seed <string>     Derive an ed25519 hostkey deterministically from
                              this seed (HKDF-SHA256, info="stdssh hostkey
                              ed25519 v1"). Same seed → same key.

Feature toggles (all features on by default):
  --no-sftp                   Reject the "sftp" subsystem request.
  --no-forward                Reject direct-tcpip channels and tcpip-forward
                              global requests (disables -L, -R, -D).
  --no-agent-forward          Reject auth-agent-req@openssh.com channel
                              requests.
  --no-pty                    Reject pty-req requests (exec/shell still work
                              without a TTY).

Other:
  --log-level <level>         error | warn | info | debug. Default: info.
                              Logs go to stderr only.
  --shell <path>              Override $SHELL for exec/shell sessions.
  --version                   Print version and exit.
  --help                      Print this message and exit.

Errors:
  exit 2  bad flag combination (e.g. both --hostkey and --hostkey-seed,
          or neither)
  exit 1  runtime failure (hostkey load, SSH handshake, etc.)
  exit 0  clean disconnect from peer
```

There is **no** `--listen`, `--port`, `--bind` flag. The binary speaks SSH on
stdin/stdout exclusively.

## 2. Exit behavior

- Process lifetime equals SSH transport lifetime.
- Clean client disconnect → exit 0.
- Stdin EOF before handshake completes → exit 1 with a stderr log line.
- Panic in any handler → log to stderr, exit 1 (no recover at the top — let
  the runtime print the trace, since kubectl will capture stderr).

## 3. Internal package APIs

These are not exported beyond the module. Listed for reviewer orientation.

### `internal/stdioconn`

```go
package stdioconn

// New returns a net.Conn whose Read pulls from os.Stdin and Write pushes to
// os.Stdout. Close shuts both streams (stdout first, then stdin).
// LocalAddr and RemoteAddr return a sentinel Addr with Network()=="stdio".
// SetDeadline / SetReadDeadline / SetWriteDeadline are no-ops returning nil.
func New() net.Conn
```

### `internal/hostkey`

```go
package hostkey

// LoadOrCreate reads a PEM private key from path, or generates an ed25519 key
// and writes it (mode 0600, creating parent dir with mode 0700) if missing.
func LoadOrCreate(path string) (ssh.Signer, error)

// FromSeed derives a deterministic ed25519 signer via HKDF-SHA256 with the
// fixed info label "stdssh hostkey ed25519 v1".
func FromSeed(seed string) (ssh.Signer, error)
```

### `internal/server`

```go
package server

type Config struct {
    HostKey         ssh.Signer
    Logger          *slog.Logger
    Shell           string // override $SHELL; empty = use $SHELL or /bin/sh
    AllowPTY        bool
    AllowSFTP       bool
    AllowForward    bool
    AllowAgentFwd   bool
}

// Run drives an SSH server session on the given net.Conn until either side
// closes. It returns nil on clean disconnect.
func Run(ctx context.Context, c net.Conn, cfg Config) error
```

### `internal/session`

```go
package session

// Handler owns one "session" channel: accepts it, then services env/pty-req/
// shell/exec/subsystem/signal/window-change until the channel closes.
type Handler struct { ... }

func NewHandler(cfg HandlerConfig) *Handler
func (h *Handler) Serve(ctx context.Context, newCh ssh.NewChannel) error
```

### `internal/sftp`

```go
package sftp

// Serve runs a pkg/sftp server bound to the given ssh.Channel and returns
// when the channel closes.
func Serve(ctx context.Context, ch ssh.Channel) error
```

### `internal/forward`

```go
package forward

// HandleDirect handles a "direct-tcpip" channel: dials the requested target
// and proxies bytes.
func HandleDirect(ctx context.Context, newCh ssh.NewChannel, logger *slog.Logger) error

// Manager tracks active -R listeners. NewManager returns a manager scoped to
// one ssh.ServerConn.
type Manager struct { ... }

func NewManager(conn *ssh.ServerConn, logger *slog.Logger) *Manager
func (m *Manager) HandleGlobal(req *ssh.Request) // tcpip-forward / cancel-tcpip-forward
func (m *Manager) Close() error                  // stop all listeners
```

### `internal/agentfwd`

```go
package agentfwd

// Bind creates a per-session unix socket and starts an Accept loop that opens
// "auth-agent@openssh.com" channels back through conn for each accepted
// connection. Returns the SSH_AUTH_SOCK path the child should see, and a
// Closer that tears down the socket + goroutine on session end.
func Bind(ctx context.Context, conn *ssh.ServerConn, logger *slog.Logger) (sockPath string, closer io.Closer, err error)
```
