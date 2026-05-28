package session

import (
	"io"
	"os/exec"

	"github.com/creack/pty"
)

// startWithPTY launches cmd attached to a new PTY, sized from req, and copies
// bytes between the channel and the master fd until the command exits or the
// channel closes. The returned *os.File is the master (so window-change can
// resize it via pty.Setsize); callers must close it when done.
func (s *sessionState) startWithPTY(cmd *exec.Cmd, req *ptyRequest) error {
	ws := &pty.Winsize{
		Rows: uint16(req.Rows),
		Cols: uint16(req.Columns),
		X:    uint16(req.Width),
		Y:    uint16(req.Height),
	}
	master, err := pty.StartWithSize(cmd, ws)
	if err != nil {
		return err
	}
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
