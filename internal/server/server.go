// Package server assembles the SSH server config and drives the channel /
// global-request demux loop over a single net.Conn (typically stdio).
package server

import (
	"context"
	"fmt"
	"log/slog"
	"net"

	"golang.org/x/crypto/ssh"
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

	go drainGlobalRequests(cfg.Logger, reqs)
	go drainChannels(cfg.Logger, chans)

	waitErr := make(chan error, 1)
	go func() { waitErr <- srvConn.Wait() }()

	select {
	case <-ctx.Done():
		srvConn.Close()
		<-waitErr
		return nil
	case err := <-waitErr:
		if err != nil && err.Error() != "EOF" {
			cfg.Logger.Debug("ssh wait returned", "err", err)
		}
		return nil
	}
}

func drainGlobalRequests(log *slog.Logger, reqs <-chan *ssh.Request) {
	for req := range reqs {
		log.Debug("global request rejected (skeleton)", "type", req.Type)
		if req.WantReply {
			_ = req.Reply(false, nil)
		}
	}
}

func drainChannels(log *slog.Logger, chans <-chan ssh.NewChannel) {
	for newCh := range chans {
		log.Debug("channel rejected (skeleton)", "type", newCh.ChannelType())
		_ = newCh.Reject(ssh.UnknownChannelType, "not implemented")
	}
}
