// Package sftp serves the SFTP subsystem on an ssh.Channel by delegating to
// github.com/pkg/sftp.
package sftp

import (
	"context"
	"fmt"
	"io"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
)

// Serve runs an SFTP server bound to ch. It returns when the client closes
// the channel or ctx is cancelled. The channel is not closed by Serve; the
// caller owns lifecycle (so it can send exit-status before closing).
func Serve(ctx context.Context, ch ssh.Channel) error {
	srv, err := sftp.NewServer(nopCloser{ch})
	if err != nil {
		return fmt.Errorf("sftp: new server: %w", err)
	}

	done := make(chan error, 1)
	go func() { done <- srv.Serve() }()

	select {
	case err := <-done:
		_ = srv.Close()
		if err != nil && err != io.EOF {
			return fmt.Errorf("sftp: serve: %w", err)
		}
		return nil
	case <-ctx.Done():
		_ = srv.Close()
		<-done
		return nil
	}
}

type nopCloser struct {
	ssh.Channel
}

func (nopCloser) Close() error { return nil }
