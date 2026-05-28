package forward

import (
	"net"
	"testing"
)

func TestListenerKeyDistinguishesAddrAndPort(t *testing.T) {
	a := listenerKey("127.0.0.1", 8080)
	b := listenerKey("127.0.0.1", 8081)
	c := listenerKey("0.0.0.0", 8080)

	if a == b {
		t.Error("same addr, different port produced same key")
	}
	if a == c {
		t.Error("different addr, same port produced same key")
	}
	if got := listenerKey("127.0.0.1", 8080); got != a {
		t.Errorf("listenerKey not deterministic: %q vs %q", got, a)
	}
}

func TestSplitHostPortTCP(t *testing.T) {
	a := &net.TCPAddr{IP: net.IPv4(192, 168, 1, 7), Port: 4242}
	host, port := splitHostPort(a)
	if host != "192.168.1.7" || port != 4242 {
		t.Errorf("got (%q, %d), want (192.168.1.7, 4242)", host, port)
	}
}

func TestSplitHostPortUnix(t *testing.T) {
	a := &net.UnixAddr{Name: "/tmp/x.sock", Net: "unix"}
	host, port := splitHostPort(a)
	// Unix addresses don't fit host:port; the function falls back to addr
	// string + port 0.
	if port != 0 {
		t.Errorf("unix addr port = %d, want 0", port)
	}
	if host == "" {
		t.Error("unix addr host is empty")
	}
}
