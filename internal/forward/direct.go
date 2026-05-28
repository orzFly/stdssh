// Package forward implements port forwarding channels and global requests:
// direct-tcpip (-L / -D / -W), tcpip-forward (-R), and forwarded-tcpip.
package forward

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"strconv"
	"sync"

	"golang.org/x/crypto/ssh"
)

// directTCPIPPayload is the RFC 4254 §7.2 channel-open data for "direct-tcpip".
type directTCPIPPayload struct {
	HostToConnect       string
	PortToConnect       uint32
	OriginatorIPAddress string
	OriginatorPort      uint32
}

// HandleDirect handles a "direct-tcpip" channel-open: dials the requested
// target and proxies bytes between the SSH channel and the dialed conn.
func HandleDirect(ctx context.Context, newCh ssh.NewChannel, log *slog.Logger) error {
	var p directTCPIPPayload
	if err := ssh.Unmarshal(newCh.ExtraData(), &p); err != nil {
		_ = newCh.Reject(ssh.Prohibited, "bad direct-tcpip payload")
		return fmt.Errorf("direct-tcpip: parse payload: %w", err)
	}

	target := net.JoinHostPort(p.HostToConnect, strconv.FormatUint(uint64(p.PortToConnect), 10))
	dialer := net.Dialer{}
	tcp, err := dialer.DialContext(ctx, "tcp", target)
	if err != nil {
		log.Debug("direct-tcpip dial failed", "target", target, "err", err)
		_ = newCh.Reject(ssh.ConnectionFailed, err.Error())
		return nil
	}

	ch, reqs, err := newCh.Accept()
	if err != nil {
		tcp.Close()
		return fmt.Errorf("direct-tcpip: accept: %w", err)
	}
	go ssh.DiscardRequests(reqs)

	log.Debug("direct-tcpip established", "target", target,
		"from", net.JoinHostPort(p.OriginatorIPAddress, strconv.FormatUint(uint64(p.OriginatorPort), 10)))

	proxy(ch, tcp)
	return nil
}

// proxy bidirectionally copies between an ssh.Channel and a net.Conn,
// returning when either side closes.
func proxy(ch ssh.Channel, tcp net.Conn) {
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		_, _ = io.Copy(tcp, ch)
		if c, ok := tcp.(interface{ CloseWrite() error }); ok {
			_ = c.CloseWrite()
		}
	}()
	go func() {
		defer wg.Done()
		_, _ = io.Copy(ch, tcp)
		_ = ch.CloseWrite()
	}()
	wg.Wait()
	_ = ch.Close()
	_ = tcp.Close()
}
