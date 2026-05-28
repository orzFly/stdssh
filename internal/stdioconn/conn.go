// Package stdioconn exposes os.Stdin / os.Stdout as a net.Conn so the SSH
// server transport can be driven over an inherited stdio pair (e.g. kubectl
// exec).
package stdioconn

import (
	"errors"
	"io"
	"net"
	"os"
	"sync"
	"time"
)

type stdioAddr struct{}

func (stdioAddr) Network() string { return "stdio" }
func (stdioAddr) String() string  { return "stdio" }

type Conn struct {
	r io.ReadCloser
	w io.WriteCloser

	closeOnce sync.Once
	closeErr  error
}

// New returns a Conn backed by os.Stdin and os.Stdout.
func New() *Conn {
	return newConn(os.Stdin, os.Stdout)
}

func newConn(r io.ReadCloser, w io.WriteCloser) *Conn {
	return &Conn{r: r, w: w}
}

func (c *Conn) Read(p []byte) (int, error)  { return c.r.Read(p) }
func (c *Conn) Write(p []byte) (int, error) { return c.w.Write(p) }

// Close shuts the write side before the read side so any in-flight protocol
// bytes are flushed out before the peer sees EOF.
func (c *Conn) Close() error {
	c.closeOnce.Do(func() {
		werr := c.w.Close()
		rerr := c.r.Close()
		c.closeErr = errors.Join(werr, rerr)
	})
	return c.closeErr
}

func (c *Conn) LocalAddr() net.Addr  { return stdioAddr{} }
func (c *Conn) RemoteAddr() net.Addr { return stdioAddr{} }

func (c *Conn) SetDeadline(time.Time) error      { return nil }
func (c *Conn) SetReadDeadline(time.Time) error  { return nil }
func (c *Conn) SetWriteDeadline(time.Time) error { return nil }
