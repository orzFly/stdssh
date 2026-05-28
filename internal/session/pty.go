package session

import (
	"io"
	"os"
	"os/exec"
	"syscall"

	"github.com/creack/pty"
)

// openPTY opens a new pty pair sized per req. The returned slave is what the
// caller passes to attachPTY; the master is what we read/write from the parent
// side. Both must be closed on error before returning.
func openPTY(req *ptyRequest) (master, slave *os.File, err error) {
	master, slave, err = pty.Open()
	if err != nil {
		return nil, nil, err
	}
	if err := pty.Setsize(master, &pty.Winsize{
		Rows: uint16(req.Rows),
		Cols: uint16(req.Columns),
		X:    uint16(req.Width),
		Y:    uint16(req.Height),
	}); err != nil {
		_ = master.Close()
		_ = slave.Close()
		return nil, nil, err
	}
	return master, slave, nil
}

// attachPTY wires cmd to slave (as its controlling tty), starts it, then closes
// slave in the parent and spins up the master<->channel copy goroutines. The
// caller retains ownership of master on error.
func (s *sessionState) attachPTY(cmd *exec.Cmd, master, slave *os.File) error {
	cmd.Stdin = slave
	cmd.Stdout = slave
	cmd.Stderr = slave
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setsid = true
	cmd.SysProcAttr.Setctty = true

	if err := cmd.Start(); err != nil {
		return err
	}
	_ = slave.Close()

	s.mu.Lock()
	s.ptyMaster = master
	s.mu.Unlock()

	go func() { _, _ = io.Copy(master, s.ch) }()
	go func() { _, _ = io.Copy(s.ch, master) }()
	return nil
}

func (s *sessionState) handleWindowChange(req *windowChangeRequest) {
	s.mu.Lock()
	master := s.ptyMaster
	s.mu.Unlock()
	if master == nil {
		return
	}
	_ = pty.Setsize(master, &pty.Winsize{
		Rows: uint16(req.Rows),
		Cols: uint16(req.Columns),
		X:    uint16(req.Width),
		Y:    uint16(req.Height),
	})
}
