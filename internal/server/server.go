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
	defer srvConn.Close()

	cfg.Logger.Info("ssh handshake completed",
		"client_version", string(srvConn.ClientVersion()))

	sessHandler := session.NewHandler(session.HandlerConfig{
		Logger:        cfg.Logger,
		Shell:         cfg.Shell,
		AllowPTY:      cfg.AllowPTY,
		AllowSFTP:     cfg.AllowSFTP,
		AllowAgentFwd: cfg.AllowAgentFwd,
	})

	var wg sync.WaitGroup
	go drainGlobalRequests(cfg.Logger, reqs)
	wg.Add(1)
	go func() {
		defer wg.Done()
		dispatchChannels(ctx, cfg.Logger, chans, sessHandler)
	}()

	waitErr := make(chan error, 1)
	go func() { waitErr <- srvConn.Wait() }()

	select {
	case <-ctx.Done():
		srvConn.Close()
		<-waitErr
	case err := <-waitErr:
		if err != nil && err.Error() != "EOF" {
			cfg.Logger.Debug("ssh wait returned", "err", err)
		}
	}
	wg.Wait()
	return nil
}

func drainGlobalRequests(log *slog.Logger, reqs <-chan *ssh.Request) {
	for req := range reqs {
		log.Debug("global request rejected (skeleton)", "type", req.Type)
		if req.WantReply {
			_ = req.Reply(false, nil)
		}
	}
}

func dispatchChannels(ctx context.Context, log *slog.Logger, chans <-chan ssh.NewChannel, sess *session.Handler) {
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
		default:
			log.Debug("channel rejected", "type", newCh.ChannelType())
			_ = newCh.Reject(ssh.UnknownChannelType, "not implemented")
		}
	}
	wg.Wait()
}
