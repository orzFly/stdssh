package session

import (
	"errors"
	"io"
	"log/slog"
	"os/exec"
	"syscall"
	"testing"
	"time"
)

func newTestLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestSSHSignalToOS(t *testing.T) {
	cases := []struct {
		in   string
		want syscall.Signal
		ok   bool
	}{
		{"INT", syscall.SIGINT, true},
		{"TERM", syscall.SIGTERM, true},
		{"KILL", syscall.SIGKILL, true},
		{"HUP", syscall.SIGHUP, true},
		{"USR1", syscall.SIGUSR1, true},
		{"USR2", syscall.SIGUSR2, true},
		{"QUIT", syscall.SIGQUIT, true},
		{"ABRT", syscall.SIGABRT, true},
		{"ALRM", syscall.SIGALRM, true},
		{"FPE", syscall.SIGFPE, true},
		{"ILL", syscall.SIGILL, true},
		{"PIPE", syscall.SIGPIPE, true},
		{"SEGV", syscall.SIGSEGV, true},

		{"", 0, false},
		{"sigint", 0, false},
		{"STOP", 0, false}, // not in RFC 4254 §6.10
		{"CONT", 0, false},
		{"BOGUS", 0, false},
	}
	for _, tc := range cases {
		got, ok := sshSignalToOS(tc.in)
		if ok != tc.ok || got != tc.want {
			t.Errorf("sshSignalToOS(%q) = (%v, %v), want (%v, %v)", tc.in, got, ok, tc.want, tc.ok)
		}
	}
}

func TestOSSignalToSSH(t *testing.T) {
	cases := []struct {
		in   syscall.Signal
		want string
		ok   bool
	}{
		{syscall.SIGINT, "INT", true},
		{syscall.SIGTERM, "TERM", true},
		{syscall.SIGKILL, "KILL", true},
		{syscall.SIGHUP, "HUP", true},
		{syscall.SIGSEGV, "SEGV", true},
		{syscall.SIGSTOP, "", false},
		{syscall.SIGCONT, "", false},
		{syscall.SIGCHLD, "", false},
	}
	for _, tc := range cases {
		got, ok := osSignalToSSH(tc.in)
		if ok != tc.ok || got != tc.want {
			t.Errorf("osSignalToSSH(%v) = (%q, %v), want (%q, %v)", tc.in, got, ok, tc.want, tc.ok)
		}
	}
}

// signalInfo and the RFC mapping together must NOT produce uint32(-1)
// (= 4294967295) as an exit-status for a signal-killed process. Regression
// guard against the bug where ExitCode()==-1 was sent verbatim.
func TestSignalInfoDetectsKilledProcess(t *testing.T) {
	cmd := exec.Command("/bin/sh", "-c", "kill -TERM $$; sleep 5")
	if err := cmd.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		_ = cmd.Process.Kill()
		t.Fatal("process did not exit in time")
	}

	sig, signum, ok := signalInfo(cmd)
	if !ok {
		t.Fatalf("signalInfo: ok=false, want true for signal-killed process")
	}
	if sig != syscall.SIGTERM {
		t.Errorf("signal = %v, want SIGTERM", sig)
	}
	if signum != int(syscall.SIGTERM) {
		t.Errorf("signum = %d, want %d", signum, int(syscall.SIGTERM))
	}
	name, ok := osSignalToSSH(sig)
	if !ok || name != "TERM" {
		t.Errorf("osSignalToSSH(%v) = (%q, %v), want (TERM, true)", sig, name, ok)
	}
}

// startGroupedSleeper launches a shell that, depending on trapHUP, either
// exits cleanly on SIGHUP or ignores it and sleeps. Returns the cmd and a
// channel closed when the child is reaped. Forces a fresh process group so
// killChildGroup's kill(-pid, …) targets only this child.
func startGroupedSleeper(t *testing.T, trapHUP bool) (*exec.Cmd, <-chan struct{}) {
	t.Helper()
	// In the ignore-HUP path we set the shell's HUP disposition to SIG_IGN
	// and then exec into sleep — POSIX preserves SIG_IGN across exec, so the
	// resulting process truly ignores HUP. Without `exec`, the forked sleep
	// would still get the default HUP handler and die immediately.
	//
	// The "READY\n" handshake closes a race where the test could send HUP
	// before the shell finished installing the trap — in that window HUP
	// would terminate the shell at its default disposition.
	script := "echo READY; exec sleep 30"
	if !trapHUP {
		script = "trap '' HUP; echo READY; exec sleep 30"
	}
	cmd := exec.Command("/bin/sh", "-c", script)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("StdoutPipe: %v", err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	closed := make(chan struct{})
	go func() {
		_ = cmd.Wait()
		close(closed)
	}()

	ready := make([]byte, 6)
	if _, err := io.ReadFull(stdout, ready); err != nil {
		t.Fatalf("waiting for READY: %v", err)
	}
	return cmd, closed
}

// SIGHUP reaches the whole process group: a child that exits on SIGHUP gets
// reaped well within the grace window, no SIGKILL escalation.
func TestKillChildGroupGracefulHUP(t *testing.T) {
	cmd, closed := startGroupedSleeper(t, true)
	defer func() {
		// belt-and-suspenders
		if cmd.ProcessState == nil {
			_ = cmd.Process.Kill()
		}
	}()

	start := time.Now()
	killChildGroup(newTestLogger(), cmd, closed, 2*time.Second)
	elapsed := time.Since(start)

	if elapsed > 2*time.Second {
		t.Errorf("killChildGroup took %v, expected well under grace (child traps HUP and exits)", elapsed)
	}
	if cmd.ProcessState == nil {
		t.Fatal("ProcessState nil — child not reaped")
	}
	sig, _, ok := signalInfo(cmd)
	if !ok || sig != syscall.SIGHUP {
		t.Errorf("expected child to die from SIGHUP, got signalInfo=(%v, ok=%v)", sig, ok)
	}
}

// Regression for the original cleanup bug: if a child ignores SIGHUP,
// killChildGroup must escalate to SIGKILL within ~grace, not block forever.
func TestKillChildGroupEscalatesToKILL(t *testing.T) {
	cmd, closed := startGroupedSleeper(t, false)
	defer func() {
		if cmd.ProcessState == nil {
			_ = cmd.Process.Kill()
		}
	}()

	grace := 200 * time.Millisecond
	start := time.Now()
	killChildGroup(newTestLogger(), cmd, closed, grace)
	elapsed := time.Since(start)

	if elapsed < grace {
		t.Errorf("killChildGroup returned in %v, expected at least grace=%v", elapsed, grace)
	}
	if elapsed > grace+2*time.Second {
		t.Errorf("killChildGroup took %v, SIGKILL escalation too slow", elapsed)
	}
	if cmd.ProcessState == nil {
		t.Fatal("ProcessState nil — child not reaped")
	}
	sig, _, ok := signalInfo(cmd)
	if !ok || sig != syscall.SIGKILL {
		t.Errorf("expected child to die from SIGKILL, got signalInfo=(%v, ok=%v)", sig, ok)
	}
}

// killChildGroup must reap grandchildren too: that's the whole reason for
// using kill(-pgid) over kill(pid). Spawn a shell that backgrounds a sleeper
// and exits; the sleeper inherits the pgroup and must also die.
func TestKillChildGroupReachesGrandchildren(t *testing.T) {
	// Parent shell prints the grandchild pid, then exits, leaving the
	// grandchild in the same pgroup but no longer a direct child.
	cmd := exec.Command("/bin/sh", "-c", "sleep 30 & echo $! ; wait")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	closed := make(chan struct{})
	go func() {
		_ = cmd.Wait()
		close(closed)
	}()

	// Read the grandchild pid (one line, then "wait" blocks).
	buf := make([]byte, 32)
	n, err := stdout.Read(buf)
	if err != nil || n == 0 {
		t.Fatalf("read grandchild pid: n=%d err=%v", n, err)
	}
	var grandPid int
	for _, c := range buf[:n] {
		if c < '0' || c > '9' {
			break
		}
		grandPid = grandPid*10 + int(c-'0')
	}
	if grandPid == 0 {
		t.Fatalf("could not parse grandchild pid from %q", string(buf[:n]))
	}

	killChildGroup(newTestLogger(), cmd, closed, 200*time.Millisecond)

	// After killChildGroup, signalling 0 to the grandchild should fail with
	// ESRCH if it's truly gone. Give the kernel a tick to reap.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if err := syscall.Kill(grandPid, 0); err != nil {
			return // grandchild gone — good
		}
		time.Sleep(20 * time.Millisecond)
	}
	// Cleanup leak if test failed.
	_ = syscall.Kill(grandPid, syscall.SIGKILL)
	t.Errorf("grandchild pid %d still alive after killChildGroup", grandPid)
}

func TestExitStatus(t *testing.T) {
	if got := exitStatus(nil); got != 0 {
		t.Errorf("exitStatus(nil) = %d, want 0", got)
	}

	cmd := exec.Command("/bin/sh", "-c", "exit 42")
	err := cmd.Run()
	if got := exitStatus(err); got != 42 {
		t.Errorf("exitStatus(exit-42) = %d, want 42", got)
	}

	if got := exitStatus(errors.New("non-exit error")); got != 1 {
		t.Errorf("exitStatus(non-exit) = %d, want 1", got)
	}
}
