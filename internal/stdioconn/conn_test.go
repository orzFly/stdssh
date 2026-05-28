package stdioconn

import (
	"errors"
	"io"
	"testing"
	"time"
)

func TestRoundTrip(t *testing.T) {
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	c := newConn(inR, outW)

	go func() {
		_, _ = inW.Write([]byte("ping"))
		_ = inW.Close()
	}()

	buf := make([]byte, 4)
	if _, err := io.ReadFull(c, buf); err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(buf) != "ping" {
		t.Fatalf("got %q want ping", buf)
	}

	go func() {
		got, _ := io.ReadAll(outR)
		if string(got) != "pong" {
			t.Errorf("downstream got %q want pong", got)
		}
	}()
	if _, err := c.Write([]byte("pong")); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := c.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
}

func TestCloseIdempotent(t *testing.T) {
	inR, _ := io.Pipe()
	_, outW := io.Pipe()
	c := newConn(inR, outW)

	if err := c.Close(); err != nil {
		t.Fatalf("first close: %v", err)
	}
	if err := c.Close(); err != nil {
		t.Fatalf("second close: %v", err)
	}
}

func TestReadPropagatesEOF(t *testing.T) {
	inR, inW := io.Pipe()
	_, outW := io.Pipe()
	c := newConn(inR, outW)

	_ = inW.Close()
	if _, err := c.Read(make([]byte, 1)); !errors.Is(err, io.EOF) {
		t.Fatalf("want EOF, got %v", err)
	}
}

func TestDeadlinesNoOp(t *testing.T) {
	c := newConn(io.NopCloser(nil), nopWriter{})
	if err := c.SetDeadline(time.Time{}); err != nil {
		t.Fatalf("SetDeadline: %v", err)
	}
	if err := c.SetReadDeadline(time.Time{}); err != nil {
		t.Fatalf("SetReadDeadline: %v", err)
	}
	if err := c.SetWriteDeadline(time.Time{}); err != nil {
		t.Fatalf("SetWriteDeadline: %v", err)
	}
}

type nopWriter struct{}

func (nopWriter) Write(p []byte) (int, error) { return len(p), nil }
func (nopWriter) Close() error                { return nil }
