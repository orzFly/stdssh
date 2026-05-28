// Package agentfwd handles SSH agent forwarding: it creates a per-session
// unix socket the child process can use as SSH_AUTH_SOCK, and bridges each
// accepted connection to an "auth-agent@openssh.com" channel back to the
// SSH client.
package agentfwd

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"sync"

	"golang.org/x/crypto/ssh"
)

// Bind sets up the agent-forwarding socket and starts the accept loop.
// Returns the socket path (to be exported as SSH_AUTH_SOCK in the child) and a
// Closer that shuts the listener and removes the socket directory.
func Bind(conn *ssh.ServerConn, log *slog.Logger) (string, io.Closer, error) {
	dir, err := os.MkdirTemp(socketParent(), "stdssh-agent-")
	if err != nil {
		return "", nil, fmt.Errorf("agentfwd: mkdtemp: %w", err)
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		_ = os.RemoveAll(dir)
		return "", nil, fmt.Errorf("agentfwd: chmod dir: %w", err)
	}
	sockPath := filepath.Join(dir, "agent.sock")
	ln, err := net.Listen("unix", sockPath)
	if err != nil {
		_ = os.RemoveAll(dir)
		return "", nil, fmt.Errorf("agentfwd: listen: %w", err)
	}
	if err := os.Chmod(sockPath, 0o600); err != nil {
		log.Warn("agentfwd: chmod socket", "err", err)
	}

	b := &binding{
		ln:   ln,
		conn: conn,
		log:  log,
		dir:  dir,
	}
	go b.acceptLoop()
	return sockPath, b, nil
}

func socketParent() string {
	if v := os.Getenv("XDG_RUNTIME_DIR"); v != "" {
		return v
	}
	return os.TempDir()
}

type binding struct {
	ln   net.Listener
	conn *ssh.ServerConn
	log  *slog.Logger
	dir  string

	mu     sync.Mutex
	closed bool
	chs    map[ssh.Channel]struct{}

	closeOnce sync.Once
	closeErr  error
}

func (b *binding) acceptLoop() {
	for {
		c, err := b.ln.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return
			}
			b.log.Debug("agentfwd accept", "err", err)
			return
		}
		go b.handle(c)
	}
}

func (b *binding) handle(c net.Conn) {
	defer c.Close()
	ch, reqs, err := b.conn.OpenChannel("auth-agent@openssh.com", nil)
	if err != nil {
		b.log.Debug("auth-agent open rejected", "err", err)
		return
	}

	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		_ = ch.Close()
		return
	}
	if b.chs == nil {
		b.chs = map[ssh.Channel]struct{}{}
	}
	b.chs[ch] = struct{}{}
	b.mu.Unlock()

	defer func() {
		b.mu.Lock()
		delete(b.chs, ch)
		b.mu.Unlock()
		_ = ch.Close()
	}()

	go ssh.DiscardRequests(reqs)

	// Half-close pattern: when one direction finishes, signal EOF on the
	// other end so the peer knows to close too. Without this, ssh-add closes
	// its unix socket but the SSH peer keeps the auth-agent channel open,
	// blocking io.Copy(c, ch) and leaking the channel — which in turn keeps
	// the whole SSH connection alive after the session exits.
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		_, _ = io.Copy(c, ch)
		if cw, ok := c.(interface{ CloseWrite() error }); ok {
			_ = cw.CloseWrite()
		}
	}()
	go func() {
		defer wg.Done()
		_, _ = io.Copy(ch, c)
		_ = ch.CloseWrite()
	}()
	wg.Wait()
}

func (b *binding) Close() error {
	b.closeOnce.Do(func() {
		b.mu.Lock()
		b.closed = true
		chs := b.chs
		b.chs = nil
		b.mu.Unlock()

		err1 := b.ln.Close()
		// Forcibly close any in-flight auth-agent channels: if a peer never
		// responded to our CloseWrite (or a connection is mid-transfer when
		// the session ends), this unblocks the handle() goroutines and
		// releases the underlying SSH channels so the client can disconnect.
		for ch := range chs {
			_ = ch.Close()
		}
		err2 := os.RemoveAll(b.dir)
		b.closeErr = errors.Join(err1, err2)
	})
	return b.closeErr
}
