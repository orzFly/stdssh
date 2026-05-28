// Package session handles a single SSH "session" channel: env negotiation,
// exec/shell launch, signal forwarding, and (later phases) PTY allocation,
// SFTP subsystem, and agent forwarding.
package session

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"golang.org/x/crypto/ssh"

	"stdssh/internal/agentfwd"
	"stdssh/internal/sftp"
)

// childGraceTimeout is how long cleanup() waits for a SIGHUP'd process group
// to exit before escalating to SIGKILL. Short enough that disconnect cleanup
// isn't perceptibly slow; long enough that well-behaved shells finish writes
// and exit normally.
const childGraceTimeout = 2 * time.Second

type HandlerConfig struct {
	Logger        *slog.Logger
	Conn          *ssh.ServerConn // required when AllowAgentFwd is true
	Shell         string          // override; empty = $SHELL or /bin/sh
	AllowPTY      bool
	AllowSFTP     bool
	AllowAgentFwd bool
}

type Handler struct {
	cfg HandlerConfig
}

func NewHandler(cfg HandlerConfig) *Handler {
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	return &Handler{cfg: cfg}
}

// Serve accepts the session channel and processes its requests until the
// channel or context closes. It returns nil on clean completion.
func (h *Handler) Serve(ctx context.Context, newCh ssh.NewChannel) error {
	ch, reqs, err := newCh.Accept()
	if err != nil {
		return fmt.Errorf("session: accept: %w", err)
	}

	s := &sessionState{
		h:      h,
		ch:     ch,
		log:    h.cfg.Logger,
		env:    map[string]string{},
		ctx:    ctx,
		closed: make(chan struct{}),
	}
	defer s.cleanup()

	for req := range reqs {
		if !s.handle(req) {
			return nil
		}
	}
	return nil
}

type sessionState struct {
	h   *Handler
	ch  ssh.Channel
	log *slog.Logger
	ctx context.Context

	mu        sync.Mutex
	env       map[string]string
	started   bool
	pty       *ptyRequest
	ptyMaster *os.File

	agentSock   string
	agentCloser io.Closer

	closed chan struct{}
	cmd    *exec.Cmd
}

// handle processes a single request. Returns false when the channel is done.
func (s *sessionState) handle(req *ssh.Request) bool {
	switch req.Type {
	case "env":
		var m envRequest
		if err := ssh.Unmarshal(req.Payload, &m); err != nil {
			s.reply(req, false)
			return true
		}
		if envNameAllowed(m.Name) {
			s.mu.Lock()
			s.env[m.Name] = m.Value
			s.mu.Unlock()
		}
		s.reply(req, true)
		return true

	case "shell":
		ok := s.startShell()
		s.reply(req, ok)
		return true

	case "exec":
		var m execRequest
		if err := ssh.Unmarshal(req.Payload, &m); err != nil {
			s.reply(req, false)
			return true
		}
		ok := s.startExec(m.Command)
		s.reply(req, ok)
		return true

	case "signal":
		var m signalRequest
		if err := ssh.Unmarshal(req.Payload, &m); err == nil {
			s.forwardSignal(m.Signal)
		}
		// signal requests never want_reply per RFC 4254 §6.10, but be safe.
		s.reply(req, true)
		return true

	case "pty-req":
		if !s.h.cfg.AllowPTY {
			s.reply(req, false)
			return true
		}
		var m ptyRequest
		if err := ssh.Unmarshal(req.Payload, &m); err != nil {
			s.reply(req, false)
			return true
		}
		s.mu.Lock()
		s.pty = &m
		s.mu.Unlock()
		s.reply(req, true)
		return true

	case "window-change":
		var m windowChangeRequest
		if err := ssh.Unmarshal(req.Payload, &m); err == nil {
			s.handleWindowChange(&m)
		}
		// window-change never wants reply.
		return true

	case "subsystem":
		var m subsystemRequest
		if err := ssh.Unmarshal(req.Payload, &m); err != nil {
			s.reply(req, false)
			return true
		}
		switch m.Name {
		case "sftp":
			if !s.h.cfg.AllowSFTP {
				s.reply(req, false)
				return true
			}
			s.mu.Lock()
			already := s.started
			s.started = true
			s.mu.Unlock()
			if already {
				s.reply(req, false)
				return true
			}
			s.reply(req, true)
			if err := sftp.Serve(s.ctx, s.ch); err != nil {
				s.log.Warn("sftp serve", "err", err)
			}
			_, _ = s.ch.SendRequest("exit-status", false, ssh.Marshal(&exitStatusMessage{Status: 0}))
			_ = s.ch.Close()
			return false
		default:
			s.log.Debug("subsystem rejected", "name", m.Name)
			s.reply(req, false)
			return true
		}

	case "auth-agent-req@openssh.com":
		if !s.h.cfg.AllowAgentFwd || s.h.cfg.Conn == nil {
			s.reply(req, false)
			return true
		}
		// Reject duplicates: a second Bind would orphan the first binding's
		// listener and socket directory, since cleanup only closes the most
		// recently stored agentCloser.
		s.mu.Lock()
		already := s.agentCloser != nil
		s.mu.Unlock()
		if already {
			s.log.Debug("auth-agent-req rejected: already bound")
			s.reply(req, false)
			return true
		}
		sock, closer, err := agentfwd.Bind(s.h.cfg.Conn, s.log)
		if err != nil {
			s.log.Warn("agentfwd bind", "err", err)
			s.reply(req, false)
			return true
		}
		s.mu.Lock()
		// Re-check under the lock: another concurrent request could have
		// snuck a binding in between the first check and Bind().
		if s.agentCloser != nil {
			s.mu.Unlock()
			_ = closer.Close()
			s.reply(req, false)
			return true
		}
		s.agentSock = sock
		s.agentCloser = closer
		s.mu.Unlock()
		s.reply(req, true)
		return true

	default:
		s.log.Debug("session request rejected", "type", req.Type)
		s.reply(req, false)
		return true
	}
}

func (s *sessionState) reply(req *ssh.Request, ok bool) {
	if req.WantReply {
		_ = req.Reply(ok, nil)
	}
}

func (s *sessionState) startShell() bool {
	return s.start(nil)
}

func (s *sessionState) startExec(command string) bool {
	return s.start([]string{"-c", command})
}

// start launches the shell (or shell -c <command>) wired to the SSH channel.
// extraArgs nil ⇒ login shell.
func (s *sessionState) start(extraArgs []string) bool {
	s.mu.Lock()
	if s.started {
		s.mu.Unlock()
		return false
	}
	s.started = true
	envSnap := snapshotEnv(s.env)
	ptyReq := s.pty
	agentSock := s.agentSock
	s.mu.Unlock()

	shell := s.resolveShell()
	base := filepath.Base(shell)

	cmd := exec.CommandContext(s.ctx, shell)
	// Match OpenSSH session.c argv conventions:
	//   * login shell: argv[0] = "-<basename>" (the leading '-' is the
	//     portable login-shell signal recognised by bash/zsh/dash/csh, more
	//     universal than `-l`).
	//   * command:     argv[0] = "<basename>", then the verbatim flags.
	// cmd.Path remains the absolute shell path so the kernel finds the
	// binary; only argv[0] is cosmetic / login-detection.
	if extraArgs == nil {
		cmd.Args = []string{"-" + base}
	} else {
		cmd.Args = append([]string{base}, extraArgs...)
	}
	// Default Cancel is Process.Kill (SIGKILL to the immediate pid), which
	// races with cleanup() and orphans grandchildren — the pgroup is left
	// intact because nothing signals it. Override to SIGHUP the whole pgroup
	// instead; cleanup() still owns final escalation to SIGKILL via
	// killChildGroup, but by then either path has already signaled the tree.
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return os.ErrProcessDone
		}
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGHUP)
	}

	// Allocate the PTY before composing env so SSH_TTY can include the slave
	// device path. On any failure between here and attachPTY, the master/slave
	// pair must be closed.
	var ptyMaster, ptySlave *os.File
	if ptyReq != nil {
		m, sv, err := openPTY(ptyReq)
		if err != nil {
			s.log.Warn("pty open failed", "err", err)
			return false
		}
		ptyMaster, ptySlave = m, sv
		// Apply the client-supplied termios (intr / erase / IUTF8 / etc.) to
		// the slave before the child inherits it. Best effort: a malformed
		// modelist or unsupported opcode shouldn't tank the session.
		if err := applyModelist(ptySlave, []byte(ptyReq.Modelist)); err != nil {
			s.log.Debug("applyModelist failed", "err", err)
		}
	}

	env := composeEnv(envSnap, shell)
	if ptyReq != nil && ptyReq.Term != "" {
		env = append(env, "TERM="+ptyReq.Term)
	}
	if agentSock != "" {
		env = append(env, "SSH_AUTH_SOCK="+agentSock)
	}
	// stdssh has no underlying socket and intentionally cannot be exposed via
	// inetd/socket activation (no auth in this layer). Use the same UNKNOWN /
	// 65535 placeholders OpenSSH uses for non-socket transports (packet.c,
	// ssh_remote_ipaddr fallback) so remote shells still see "I am in ssh".
	env = append(env,
		"SSH_CLIENT=UNKNOWN 65535 65535",
		"SSH_CONNECTION=UNKNOWN 65535 UNKNOWN 65535",
	)
	if ptySlave != nil {
		env = append(env, "SSH_TTY="+ptySlave.Name())
	}
	cmd.Env = env
	cmd.Dir = homeDir()

	if ptyReq != nil {
		if err := s.attachPTY(cmd, ptyMaster, ptySlave); err != nil {
			_ = ptyMaster.Close()
			_ = ptySlave.Close()
			s.log.Warn("pty start failed", "shell", shell, "err", err)
			return false
		}
	} else {
		cmd.Stdin = s.ch
		cmd.Stdout = s.ch
		cmd.Stderr = s.ch.Stderr()
		// Put the child in its own process group so cleanup can kill any
		// grandchildren it spawned (kill(-pgid, sig)). The PTY path already
		// gets an isolated session/pgroup from creack/pty via Setsid.
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		if err := cmd.Start(); err != nil {
			s.log.Warn("exec start failed", "shell", shell, "err", err)
			return false
		}
	}

	s.mu.Lock()
	s.cmd = cmd
	s.mu.Unlock()
	go s.waitAndExit(cmd)
	return true
}

func (s *sessionState) waitAndExit(cmd *exec.Cmd) {
	err := cmd.Wait()
	if sig, signum, ok := signalInfo(cmd); ok {
		if name, ok := osSignalToSSH(sig); ok {
			s.log.Debug("child killed", "signal", name, "err", err)
			_, _ = s.ch.SendRequest("exit-signal", false, ssh.Marshal(&exitSignalMessage{
				Signal:     name,
				CoreDumped: coreDumped(cmd),
			}))
		} else {
			// Signal not in RFC 4254's set (e.g. SIGSTOP/SIGCONT — unlikely to
			// have killed the process, but be defensive). Report POSIX-style
			// 128+signum as exit-status rather than uint32(-1).
			status := 128 + int(signum)
			s.log.Debug("child killed by non-RFC signal", "signum", signum, "status", status)
			_, _ = s.ch.SendRequest("exit-status", false, ssh.Marshal(&exitStatusMessage{Status: uint32(status)}))
		}
	} else {
		status := exitStatus(err)
		s.log.Debug("child exited", "status", status, "err", err)
		_, _ = s.ch.SendRequest("exit-status", false, ssh.Marshal(&exitStatusMessage{Status: uint32(status)}))
	}
	_ = s.ch.Close()
	close(s.closed)
}

func (s *sessionState) forwardSignal(name string) {
	s.mu.Lock()
	cmd := s.cmd
	s.mu.Unlock()
	if cmd == nil || cmd.Process == nil {
		return
	}
	sig, ok := sshSignalToOS(name)
	if !ok {
		return
	}
	// Signal the whole session pgroup, matching OpenSSH (session.c killpg).
	// Both start paths arrange pgid == child.Pid (PTY: Setsid; non-PTY: Setpgid),
	// so kill(-pid, sig) reaches the shell *and* its descendants (e.g. a
	// background `sleep` still under the same session) without escaping into
	// stdssh's own process group.
	_ = syscall.Kill(-cmd.Process.Pid, sig)
}

func (s *sessionState) cleanup() {
	s.mu.Lock()
	cmd := s.cmd
	master := s.ptyMaster
	agentCloser := s.agentCloser
	s.agentCloser = nil
	s.mu.Unlock()

	killChildGroup(s.log, cmd, s.closed, childGraceTimeout)
	if master != nil {
		_ = master.Close()
	}
	if agentCloser != nil {
		_ = agentCloser.Close()
	}
	_ = s.ch.Close()
}

// killChildGroup tears down a running session child. If the child is already
// reaped (closed is closed), it's a no-op. Otherwise it SIGHUPs the process
// group, waits up to grace for the child to exit, and escalates to SIGKILL.
// Returns only when the child has been reaped.
//
// Both session start paths arrange for pgid == child.Pid (PTY: Setsid via
// creack/pty; non-PTY: Setpgid above), so kill(-pid, …) targets only this
// session's tree — not the stdssh server's own process group.
func killChildGroup(log *slog.Logger, cmd *exec.Cmd, closed <-chan struct{}, grace time.Duration) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	select {
	case <-closed:
		return
	default:
	}
	pid := cmd.Process.Pid
	_ = syscall.Kill(-pid, syscall.SIGHUP)
	select {
	case <-closed:
		return
	case <-time.After(grace):
	}
	log.Warn("child did not exit on SIGHUP; escalating to SIGKILL", "pid", pid)
	_ = syscall.Kill(-pid, syscall.SIGKILL)
	<-closed
}

func (s *sessionState) resolveShell() string {
	if s.h.cfg.Shell != "" {
		return s.h.cfg.Shell
	}
	if v := os.Getenv("SHELL"); v != "" {
		return v
	}
	return "/bin/sh"
}

func homeDir() string {
	if u, err := user.Current(); err == nil && u.HomeDir != "" {
		if _, err := os.Stat(u.HomeDir); err == nil {
			return u.HomeDir
		}
	}
	if v := os.Getenv("HOME"); v != "" {
		if _, err := os.Stat(v); err == nil {
			return v
		}
	}
	return ""
}

func snapshotEnv(m map[string]string) map[string]string {
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

func exitStatus(err error) int {
	if err == nil {
		return 0
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return ee.ExitCode()
	}
	return 1
}

// signalInfo reports whether the process died from a signal and, if so,
// returns the OS signal value. The signum is returned separately so callers
// can fall back to POSIX 128+signum when the signal isn't in RFC 4254's set.
func signalInfo(cmd *exec.Cmd) (syscall.Signal, int, bool) {
	if cmd.ProcessState == nil {
		return 0, 0, false
	}
	ws, ok := cmd.ProcessState.Sys().(syscall.WaitStatus)
	if !ok || !ws.Signaled() {
		return 0, 0, false
	}
	sig := ws.Signal()
	return sig, int(sig), true
}

func coreDumped(cmd *exec.Cmd) bool {
	if cmd.ProcessState == nil {
		return false
	}
	ws, ok := cmd.ProcessState.Sys().(syscall.WaitStatus)
	if !ok {
		return false
	}
	return ws.CoreDump()
}

// osSignalToSSH is the inverse of sshSignalToOS: it maps an OS signal to the
// RFC 4254 §6.10 name (without the "SIG" prefix). Returns false for signals
// the RFC does not list (e.g. SIGSTOP, SIGCONT) — those are reported as
// generic exit-status to avoid sending an out-of-spec name.
func osSignalToSSH(sig syscall.Signal) (string, bool) {
	switch sig {
	case syscall.SIGABRT:
		return "ABRT", true
	case syscall.SIGALRM:
		return "ALRM", true
	case syscall.SIGFPE:
		return "FPE", true
	case syscall.SIGHUP:
		return "HUP", true
	case syscall.SIGILL:
		return "ILL", true
	case syscall.SIGINT:
		return "INT", true
	case syscall.SIGKILL:
		return "KILL", true
	case syscall.SIGPIPE:
		return "PIPE", true
	case syscall.SIGQUIT:
		return "QUIT", true
	case syscall.SIGSEGV:
		return "SEGV", true
	case syscall.SIGTERM:
		return "TERM", true
	case syscall.SIGUSR1:
		return "USR1", true
	case syscall.SIGUSR2:
		return "USR2", true
	}
	return "", false
}

// RFC 4254 §6.10 signal names → syscall signals.
func sshSignalToOS(name string) (syscall.Signal, bool) {
	switch name {
	case "ABRT":
		return syscall.SIGABRT, true
	case "ALRM":
		return syscall.SIGALRM, true
	case "FPE":
		return syscall.SIGFPE, true
	case "HUP":
		return syscall.SIGHUP, true
	case "ILL":
		return syscall.SIGILL, true
	case "INT":
		return syscall.SIGINT, true
	case "KILL":
		return syscall.SIGKILL, true
	case "PIPE":
		return syscall.SIGPIPE, true
	case "QUIT":
		return syscall.SIGQUIT, true
	case "SEGV":
		return syscall.SIGSEGV, true
	case "TERM":
		return syscall.SIGTERM, true
	case "USR1":
		return syscall.SIGUSR1, true
	case "USR2":
		return syscall.SIGUSR2, true
	}
	return 0, false
}
