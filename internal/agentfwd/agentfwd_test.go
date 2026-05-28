package agentfwd

import (
	"io"
	"log/slog"
	"net"
	"sync"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"

	"stdssh/internal/hostkey"
)

// TestHalfCloseUnblocksOnClientDisconnect is the regression test for the
// "ssh -A hangs after session exit" bug.
//
// Before the fix: handle() ran two unconditional io.Copy goroutines and
// wg.Wait()-ed both. When the unix client (ssh-add) closed its socket,
// io.Copy(ch, c) returned but io.Copy(c, ch) blocked forever on ch.Read()
// because the SSH peer waits for our half-close before closing the channel.
// The handle() goroutine leaked, the auth-agent@openssh.com channel stayed
// open at the SSH connection layer, and the SSH client never disconnected.
//
// After the fix: handle() calls ch.CloseWrite() when the unix-side EOF
// lands; the peer (well-behaved) closes its end; both io.Copy directions
// finish; handle() returns and releases the channel.
//
// External assertion: the peer-side goroutine that reads from the channel
// observes EOF and then a fully-closed channel — which it can only do if
// the server half-closed AND the round-trip completed.
func TestHalfCloseUnblocksOnClientDisconnect(t *testing.T) {
	peerEOF := make(chan struct{}, 1)
	srvConn, cliConn := setupSSHPairWithPeer(t, func(t *testing.T, ch ssh.Channel) {
		defer ch.Close()
		// Echo any inbound bytes back so the test can sync on the round-trip.
		// When the server half-closes, ch.Read returns io.EOF; we then close
		// our end so the server's other io.Copy direction can finish.
		buf := make([]byte, 1024)
		for {
			n, err := ch.Read(buf)
			if n > 0 {
				_, _ = ch.Write(buf[:n])
			}
			if err == io.EOF {
				peerEOF <- struct{}{}
				_ = ch.CloseWrite()
				return
			}
			if err != nil {
				return
			}
		}
	})
	defer srvConn.Close()
	defer cliConn.Close()

	sockPath, closer, err := Bind(srvConn, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("Bind: %v", err)
	}
	defer closer.Close()

	c, err := net.Dial("unix", sockPath)
	if err != nil {
		t.Fatalf("dial agent sock: %v", err)
	}

	// Drive a round-trip so the peer's auth-agent handler is fully running.
	if _, err := c.Write([]byte("ping")); err != nil {
		t.Fatalf("write: %v", err)
	}
	buf := make([]byte, 4)
	if _, err := io.ReadFull(c, buf); err != nil {
		t.Fatalf("read echo: %v", err)
	}

	// Close from the unix-client side. With the bug, the server's
	// io.Copy(c, ch) keeps reading from ch — server NEVER half-closes,
	// so the peer NEVER sees EOF on its ch.Read, and peerEOF stays empty.
	if err := c.Close(); err != nil {
		t.Fatalf("close unix conn: %v", err)
	}

	select {
	case <-peerEOF:
		// Server half-closed; the fix is doing its job.
	case <-time.After(2 * time.Second):
		t.Fatal("server did not half-close the auth-agent channel after the unix client disconnected — the handle() goroutine is leaking the channel")
	}
}

// TestCloseForceTerminatesInFlightHandlers verifies the backup safety net:
// if a peer is misbehaving (never half-closes back, never closes the
// channel), binding.Close() must still tear down any in-flight auth-agent
// channels so the SSH connection can shut down promptly.
//
// External assertion: a peer-side goroutine sitting in ch.Read() observes
// io.EOF (or another error) only when the channel is forcibly closed.
func TestCloseForceTerminatesInFlightHandlers(t *testing.T) {
	peerAccepted := make(chan struct{}, 1)
	peerRead := make(chan error, 1)
	srvConn, cliConn := setupSSHPairWithPeer(t, func(t *testing.T, ch ssh.Channel) {
		peerAccepted <- struct{}{}
		// Read once but never write or close — model a stuck peer.
		buf := make([]byte, 1024)
		_, err := ch.Read(buf)
		peerRead <- err
	})
	defer srvConn.Close()
	defer cliConn.Close()

	sockPath, closer, err := Bind(srvConn, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("Bind: %v", err)
	}

	c, err := net.Dial("unix", sockPath)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer c.Close()

	select {
	case <-peerAccepted:
	case <-time.After(2 * time.Second):
		t.Fatal("peer never accepted the auth-agent channel")
	}

	// Close while a handler is in-flight. The misbehaving peer will never
	// react to our half-close, so only force-teardown can release the
	// channel — manifested as the peer's ch.Read() returning io.EOF.
	done := make(chan error, 1)
	go func() { done <- closer.Close() }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("binding.Close() hung — force-teardown of in-flight handlers regressed")
	}

	select {
	case err := <-peerRead:
		if err != io.EOF {
			t.Logf("peer ch.Read returned %v (acceptable: forced close), wanted io.EOF", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("peer's ch.Read never returned — Close() did not force the channel closed")
	}
}

func setupSSHPairWithPeer(t *testing.T, peerHandler func(*testing.T, ssh.Channel)) (*ssh.ServerConn, ssh.Conn) {
	t.Helper()

	signer, err := hostkey.FromSeed("agentfwd-test")
	if err != nil {
		t.Fatalf("hostkey: %v", err)
	}

	// Use a TCP loopback instead of net.Pipe: the SSH version exchange
	// writes its banner before reading the peer's, and net.Pipe is
	// unbuffered, so two synchronous writes would deadlock.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()

	serverCfg := &ssh.ServerConfig{NoClientAuth: true}
	serverCfg.AddHostKey(signer)

	type srvResult struct {
		conn  *ssh.ServerConn
		chans <-chan ssh.NewChannel
		reqs  <-chan *ssh.Request
		err   error
	}
	srvCh := make(chan srvResult, 1)
	go func() {
		raw, err := ln.Accept()
		if err != nil {
			srvCh <- srvResult{err: err}
			return
		}
		c, chans, reqs, err := ssh.NewServerConn(raw, serverCfg)
		srvCh <- srvResult{c, chans, reqs, err}
	}()

	cliCfg := &ssh.ClientConfig{
		User:            "test",
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
	}

	type cliResult struct {
		conn  ssh.Conn
		chans <-chan ssh.NewChannel
		reqs  <-chan *ssh.Request
		err   error
	}
	cliCh := make(chan cliResult, 1)
	go func() {
		raw, err := net.Dial("tcp", ln.Addr().String())
		if err != nil {
			cliCh <- cliResult{err: err}
			return
		}
		c, chans, reqs, err := ssh.NewClientConn(raw, raw.RemoteAddr().String(), cliCfg)
		cliCh <- cliResult{c, chans, reqs, err}
	}()

	srv := <-srvCh
	if srv.err != nil {
		t.Fatalf("server handshake: %v", srv.err)
	}
	cli := <-cliCh
	if cli.err != nil {
		t.Fatalf("client handshake: %v", cli.err)
	}

	go ssh.DiscardRequests(srv.reqs)
	go ssh.DiscardRequests(cli.reqs)
	go func() {
		for nc := range srv.chans {
			_ = nc.Reject(ssh.UnknownChannelType, "no channels in test")
		}
	}()

	var wg sync.WaitGroup
	go func() {
		for newCh := range cli.chans {
			if newCh.ChannelType() != "auth-agent@openssh.com" {
				_ = newCh.Reject(ssh.UnknownChannelType, "")
				continue
			}
			ch, reqs, err := newCh.Accept()
			if err != nil {
				continue
			}
			go ssh.DiscardRequests(reqs)
			wg.Add(1)
			go func() {
				defer wg.Done()
				peerHandler(t, ch)
			}()
		}
	}()

	t.Cleanup(func() {
		_ = srv.conn.Close()
		_ = cli.conn.Close()
	})

	return srv.conn, cli.conn
}
