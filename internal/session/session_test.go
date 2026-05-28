package session

import (
	"errors"
	"os/exec"
	"syscall"
	"testing"
)

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
