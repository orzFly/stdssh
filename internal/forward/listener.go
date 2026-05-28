package forward

import (
	"encoding/binary"
	"errors"
	"log/slog"
	"net"
	"strconv"
	"sync"

	"golang.org/x/crypto/ssh"
)

// tcpipForwardPayload is the RFC 4254 §7.1 global-request data for
// "tcpip-forward" and "cancel-tcpip-forward".
type tcpipForwardPayload struct {
	BindAddr string
	BindPort uint32
}

// forwardedTCPIPPayload is the RFC 4254 §7.2 channel-open data for
// "forwarded-tcpip".
type forwardedTCPIPPayload struct {
	BindAddr            string
	BindPort            uint32
	OriginatorIPAddress string
	OriginatorPort      uint32
}

// Manager tracks the active -R listeners for one ssh.ServerConn.
type Manager struct {
	conn         *ssh.ServerConn
	log          *slog.Logger
	maxListeners int
	gatewayPorts bool

	mu        sync.Mutex
	listeners map[string]net.Listener
	closed    bool
}

// NewManager returns a Manager bound to the given ServerConn. Call Close to
// stop all listeners. With gatewayPorts=false (the OpenSSH default) any
// wildcard or non-loopback bind address requested by the client is forced to
// 127.0.0.1 so `-R 8080:...` does not silently expose a port to the network.
func NewManager(conn *ssh.ServerConn, log *slog.Logger, maxListeners int, gatewayPorts bool) *Manager {
	return &Manager{
		conn:         conn,
		log:          log,
		maxListeners: maxListeners,
		gatewayPorts: gatewayPorts,
		listeners:    map[string]net.Listener{},
	}
}

// rewriteListenAddr applies the GatewayPorts policy. Mirrors OpenSSH's
// channel_fwd_bind_addr (channels.c): with GatewayPorts=no, only explicit
// 127.0.0.1 / ::1 (and the resolvable "localhost") are honoured verbatim;
// everything else collapses to v4 loopback. With GatewayPorts=yes the
// wildcard tokens "" and "*" pass through as the dual-stack wildcard and
// literal addresses are used as-is.
func rewriteListenAddr(req string, gatewayPorts bool) string {
	if gatewayPorts {
		switch req {
		case "", "*":
			return ""
		}
		return req
	}
	switch req {
	case "127.0.0.1", "::1", "localhost":
		return req
	default:
		return "127.0.0.1"
	}
}

// HandleRequest dispatches tcpip-forward / cancel-tcpip-forward. Returns true
// if the request type was recognized.
func (m *Manager) HandleRequest(req *ssh.Request) bool {
	switch req.Type {
	case "tcpip-forward":
		m.handleForward(req)
		return true
	case "cancel-tcpip-forward":
		m.handleCancel(req)
		return true
	}
	return false
}

func (m *Manager) handleForward(req *ssh.Request) {
	var p tcpipForwardPayload
	if err := ssh.Unmarshal(req.Payload, &p); err != nil {
		_ = req.Reply(false, nil)
		return
	}
	bindHost := rewriteListenAddr(p.BindAddr, m.gatewayPorts)
	if bindHost != p.BindAddr {
		m.log.Debug("tcpip-forward bind address rewritten by GatewayPorts policy",
			"requested", p.BindAddr, "actual", bindHost)
	}
	addr := net.JoinHostPort(bindHost, strconv.FormatUint(uint64(p.BindPort), 10))
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		m.log.Warn("tcpip-forward listen failed", "addr", addr, "err", err)
		_ = req.Reply(false, nil)
		return
	}

	tcpAddr, ok := ln.Addr().(*net.TCPAddr)
	if !ok {
		_ = ln.Close()
		_ = req.Reply(false, nil)
		return
	}
	assignedPort := uint32(tcpAddr.Port)

	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		_ = ln.Close()
		_ = req.Reply(false, nil)
		return
	}
	if m.maxListeners > 0 && len(m.listeners) >= m.maxListeners {
		m.mu.Unlock()
		_ = ln.Close()
		m.log.Warn("tcpip-forward rejected: listener limit reached", "max", m.maxListeners)
		_ = req.Reply(false, nil)
		return
	}
	key := listenerKey(p.BindAddr, assignedPort)
	m.listeners[key] = ln
	m.mu.Unlock()

	reply := make([]byte, 4)
	binary.BigEndian.PutUint32(reply, assignedPort)
	_ = req.Reply(true, reply)

	m.log.Debug("tcpip-forward listening", "addr", ln.Addr().String())
	go m.acceptLoop(ln, p.BindAddr, assignedPort)
}

func (m *Manager) handleCancel(req *ssh.Request) {
	var p tcpipForwardPayload
	if err := ssh.Unmarshal(req.Payload, &p); err != nil {
		_ = req.Reply(false, nil)
		return
	}
	key := listenerKey(p.BindAddr, p.BindPort)
	m.mu.Lock()
	ln, ok := m.listeners[key]
	delete(m.listeners, key)
	m.mu.Unlock()
	if !ok {
		_ = req.Reply(false, nil)
		return
	}
	_ = ln.Close()
	_ = req.Reply(true, nil)
}

func (m *Manager) acceptLoop(ln net.Listener, bindAddr string, bindPort uint32) {
	defer ln.Close()
	for {
		c, err := ln.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return
			}
			m.log.Debug("tcpip-forward accept", "err", err)
			return
		}
		go m.handleAccepted(c, bindAddr, bindPort)
	}
}

func (m *Manager) handleAccepted(c net.Conn, bindAddr string, bindPort uint32) {
	origHost, origPort := splitHostPort(c.RemoteAddr())
	payload := ssh.Marshal(&forwardedTCPIPPayload{
		BindAddr:            bindAddr,
		BindPort:            bindPort,
		OriginatorIPAddress: origHost,
		OriginatorPort:      uint32(origPort),
	})
	ch, reqs, err := m.conn.OpenChannel("forwarded-tcpip", payload)
	if err != nil {
		m.log.Debug("forwarded-tcpip open rejected", "err", err)
		_ = c.Close()
		return
	}
	go ssh.DiscardRequests(reqs)
	proxy(ch, c)
}

// Close stops all listeners and prevents new ones from being registered.
func (m *Manager) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.closed = true
	for k, ln := range m.listeners {
		_ = ln.Close()
		delete(m.listeners, k)
	}
	return nil
}

func listenerKey(addr string, port uint32) string {
	return addr + "|" + strconv.FormatUint(uint64(port), 10)
}

func splitHostPort(a net.Addr) (string, int) {
	if ta, ok := a.(*net.TCPAddr); ok {
		return ta.IP.String(), ta.Port
	}
	host, port, err := net.SplitHostPort(a.String())
	if err != nil {
		return a.String(), 0
	}
	p, _ := strconv.Atoi(port)
	return host, p
}
