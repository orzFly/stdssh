// Package server assembles the SSH server config and drives the channel /
// global-request demux loop over a single net.Conn (typically stdio).
package server

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"sync"

	"golang.org/x/crypto/ssh"

	"stdssh/internal/forward"
	"stdssh/internal/session"
)

type Config struct {
	HostKey       ssh.Signer
	Logger        *slog.Logger
	ServerVersion string // e.g. "SSH-2.0-stdssh_0.1.0"

	Shell         string
	AllowPTY      bool
	AllowSFTP     bool
	AllowForward  bool
	AllowAgentFwd bool
}

// Run drives an SSH server session on conn until either side closes.
// Returns nil on clean disconnect.
func Run(ctx context.Context, conn net.Conn, cfg Config) error {
	if cfg.HostKey == nil {
		return fmt.Errorf("server: nil host key")
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}

	sc := &ssh.ServerConfig{
		NoClientAuth:  true,
		ServerVersion: cfg.ServerVersion,
	}
	sc.AddHostKey(cfg.HostKey)

	srvConn, chans, reqs, err := ssh.NewServerConn(conn, sc)
	if err != nil {
		return fmt.Errorf("server: handshake: %w", err)
	}

	cfg.Logger.Debug("ssh handshake completed",
		"client_version", string(srvConn.ClientVersion()))

	// Per-connection context: cancelled when the SSH transport closes (or the
	// root context does). Session goroutines observe this through their exec
	// children so a remote disconnect actually tears the children down,
	// rather than leaving them running indefinitely.
	connCtx, cancelConn := context.WithCancel(ctx)
	defer cancelConn()

	sessHandler := session.NewHandler(session.HandlerConfig{
		Logger:        cfg.Logger,
		Conn:          srvConn,
		Shell:         cfg.Shell,
		AllowPTY:      cfg.AllowPTY,
		AllowSFTP:     cfg.AllowSFTP,
		AllowAgentFwd: cfg.AllowAgentFwd,
	})

	var fwdMgr *forward.Manager
	if cfg.AllowForward {
		fwdMgr = forward.NewManager(srvConn, cfg.Logger)
		defer fwdMgr.Close()
	}

	var wg sync.WaitGroup
	go drainGlobalRequests(cfg.Logger, reqs, fwdMgr)
	wg.Add(1)
	go func() {
		defer wg.Done()
		dispatchChannels(connCtx, cfg.Logger, chans, sessHandler, cfg.AllowForward)
	}()

	waitErr := make(chan error, 1)
	go func() { waitErr <- srvConn.Wait() }()

	select {
	case <-ctx.Done():
		// Close srvConn to unblock the Wait() goroutine; this is the only
		// place we close it. On the waitErr branch the peer or the protocol
		// has already torn down the transport.
		_ = srvConn.Close()
		<-waitErr
	case err := <-waitErr:
		if err != nil && err.Error() != "EOF" {
			cfg.Logger.Debug("ssh wait returned", "err", err)
		}
	}
	// Transport is down — cancel connCtx before draining session goroutines
	// so their cleanup paths see context cancellation and don't sit waiting.
	cancelConn()
	wg.Wait()
	return nil
}

func drainGlobalRequests(log *slog.Logger, reqs <-chan *ssh.Request, fwd *forward.Manager) {
	for req := range reqs {
		if fwd != nil && fwd.HandleRequest(req) {
			continue
		}
		log.Debug("global request rejected", "type", req.Type)
		if req.WantReply {
			_ = req.Reply(false, nil)
		}
	}
}

func dispatchChannels(ctx context.Context, log *slog.Logger, chans <-chan ssh.NewChannel, sess *session.Handler, allowForward bool) {
	var wg sync.WaitGroup
	for newCh := range chans {
		newCh := newCh
		switch newCh.ChannelType() {
		case "session":
			wg.Add(1)
			go func() {
				defer wg.Done()
				if err := sess.Serve(ctx, newCh); err != nil {
					log.Warn("session handler error", "err", err)
				}
			}()
		case "direct-tcpip":
			if !allowForward {
				_ = newCh.Reject(ssh.Prohibited, "forwarding disabled")
				continue
			}
			wg.Add(1)
			go func() {
				defer wg.Done()
				if err := forward.HandleDirect(ctx, newCh, log); err != nil {
					log.Warn("direct-tcpip error", "err", err)
				}
			}()
		default:
			log.Debug("channel rejected", "type", newCh.ChannelType())
			_ = newCh.Reject(ssh.UnknownChannelType, "not implemented")
		}
	}
	wg.Wait()
}
