package server

import (
	"context"
	"net"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"

	"stdssh/internal/hostkey"
)

func setupPair(t *testing.T, cfg Config) (*ssh.Client, <-chan error) {
	t.Helper()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })

	serverDone := make(chan error, 1)
	go func() {
		raw, err := ln.Accept()
		if err != nil {
			serverDone <- err
			return
		}
		serverDone <- Run(context.Background(), raw, cfg)
	}()

	raw, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	cliConn, chans, reqs, err := ssh.NewClientConn(raw, raw.RemoteAddr().String(), &ssh.ClientConfig{
		User:            "test",
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
	})
	if err != nil {
		t.Fatal(err)
	}
	client := ssh.NewClient(cliConn, chans, reqs)
	t.Cleanup(func() { client.Close() })
	return client, serverDone
}

func testConfig(t *testing.T) Config {
	t.Helper()
	signer, err := hostkey.FromSeed("server-test")
	if err != nil {
		t.Fatal(err)
	}
	return Config{
		HostKey:      signer,
		AllowPTY:     true,
		AllowSFTP:    true,
		AllowForward: true,
	}
}

func TestRunExec(t *testing.T) {
	client, serverDone := setupPair(t, testConfig(t))

	sess, err := client.NewSession()
	if err != nil {
		t.Fatal(err)
	}
	out, err := sess.Output("echo hello")
	if err != nil {
		t.Fatalf("exec: %v", err)
	}
	if got := strings.TrimSpace(string(out)); got != "hello" {
		t.Errorf("output = %q, want hello", got)
	}

	_ = client.Close()
	if err := <-serverDone; err != nil {
		t.Errorf("server: %v", err)
	}
}

func TestRunExitStatus(t *testing.T) {
	client, serverDone := setupPair(t, testConfig(t))

	sess, err := client.NewSession()
	if err != nil {
		t.Fatal(err)
	}
	err = sess.Run("exit 42")
	if err == nil {
		t.Fatal("expected error for exit 42")
	}
	if exitErr, ok := err.(*ssh.ExitError); !ok || exitErr.ExitStatus() != 42 {
		t.Errorf("exit status: got %v, want 42", err)
	}

	_ = client.Close()
	if err := <-serverDone; err != nil {
		t.Errorf("server: %v", err)
	}
}

func TestRunContextCancel(t *testing.T) {
	signer, err := hostkey.FromSeed("server-test-cancel")
	if err != nil {
		t.Fatal(err)
	}
	cfg := Config{
		HostKey:      signer,
		AllowPTY:     true,
		AllowForward: true,
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	ctx, cancel := context.WithCancel(context.Background())
	serverDone := make(chan error, 1)
	go func() {
		raw, err := ln.Accept()
		if err != nil {
			serverDone <- err
			return
		}
		serverDone <- Run(ctx, raw, cfg)
	}()

	raw, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	cliConn, chans, reqs, err := ssh.NewClientConn(raw, raw.RemoteAddr().String(), &ssh.ClientConfig{
		User:            "test",
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
	})
	if err != nil {
		t.Fatal(err)
	}
	client := ssh.NewClient(cliConn, chans, reqs)
	defer client.Close()

	cancel()

	select {
	case err := <-serverDone:
		if err != nil {
			t.Errorf("server: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("server did not shut down after context cancel")
	}
}
