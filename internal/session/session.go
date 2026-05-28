// Package session handles a single SSH "session" channel: env negotiation,
// exec/shell launch, signal forwarding, and (later phases) PTY allocation,
// SFTP subsystem, and agent forwarding.
package session

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"os/user"
	"sync"
	"syscall"

	"golang.org/x/crypto/ssh"
)

type HandlerConfig struct {
	Logger        *slog.Logger
	Shell         string // override; empty = $SHELL or /bin/sh
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

	case "auth-agent-req@openssh.com", "subsystem":
		// Filled in by later phases. For now reject quietly.
		s.reply(req, false)
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
// extraArgs nil ⇒ login shell (`-l`).
func (s *sessionState) start(extraArgs []string) bool {
	s.mu.Lock()
	if s.started {
		s.mu.Unlock()
		return false
	}
	s.started = true
	envSnap := snapshotEnv(s.env)
	ptyReq := s.pty
	s.mu.Unlock()

	shell := s.resolveShell()
	args := extraArgs
	if args == nil {
		args = []string{"-l"}
	}

	cmd := exec.CommandContext(s.ctx, shell, args...)
	env := composeEnv(envSnap, shell)
	if ptyReq != nil && ptyReq.Term != "" {
		env = append(env, "TERM="+ptyReq.Term)
	}
	cmd.Env = env
	cmd.Dir = homeDir()

	if ptyReq != nil {
		if err := s.startWithPTY(cmd, ptyReq); err != nil {
			s.log.Warn("pty start failed", "shell", shell, "err", err)
			return false
		}
	} else {
		cmd.Stdin = s.ch
		cmd.Stdout = s.ch
		cmd.Stderr = s.ch.Stderr()
		if err := cmd.Start(); err != nil {
			s.log.Warn("exec start failed", "shell", shell, "err", err)
			return false
		}
	}

	s.cmd = cmd
	go s.waitAndExit(cmd)
	return true
}

func (s *sessionState) waitAndExit(cmd *exec.Cmd) {
	err := cmd.Wait()
	status := exitStatus(err)
	s.log.Debug("child exited", "status", status, "err", err)
	_, _ = s.ch.SendRequest("exit-status", false, ssh.Marshal(&exitStatusMessage{Status: uint32(status)}))
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
	_ = cmd.Process.Signal(sig)
}

func (s *sessionState) cleanup() {
	if s.cmd != nil && s.cmd.Process != nil {
		select {
		case <-s.closed:
		default:
			_ = s.cmd.Process.Signal(syscall.SIGHUP)
		}
	}
	s.mu.Lock()
	master := s.ptyMaster
	s.mu.Unlock()
	if master != nil {
		_ = master.Close()
	}
	_ = s.ch.Close()
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
