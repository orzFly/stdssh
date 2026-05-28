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
	parent := socketParent()
	if err := os.MkdirAll(parent, 0o700); err != nil {
		return "", nil, fmt.Errorf("agentfwd: mkdir parent: %w", err)
	}
	dir, err := os.MkdirTemp(parent, "agent-")
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
		return filepath.Join(v, "stdssh")
	}
	return filepath.Join(os.TempDir(), "stdssh")
}

type binding struct {
	ln   net.Listener
	conn *ssh.ServerConn
	log  *slog.Logger
	dir  string

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
	ch, reqs, err := b.conn.OpenChannel("auth-agent@openssh.com", nil)
	if err != nil {
		b.log.Debug("auth-agent open rejected", "err", err)
		_ = c.Close()
		return
	}
	go ssh.DiscardRequests(reqs)

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		_, _ = io.Copy(c, ch)
	}()
	go func() {
		defer wg.Done()
		_, _ = io.Copy(ch, c)
	}()
	wg.Wait()
	_ = ch.Close()
	_ = c.Close()
}

func (b *binding) Close() error {
	b.closeOnce.Do(func() {
		err1 := b.ln.Close()
		err2 := os.RemoveAll(b.dir)
		b.closeErr = errors.Join(err1, err2)
	})
	return b.closeErr
}
