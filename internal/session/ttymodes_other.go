//go:build !linux

package session

import "os"

// applyModelist on non-Linux platforms is currently a no-op. stdssh's smoke
// matrix runs on Linux; Darwin support can be added by mirroring
// ttymodes_linux.go against the BSD termios layout (Iflag/Oflag/Cflag/Lflag
// are uint64 there and a few opcodes have no equivalent). Until then the PTY
// keeps the kernel's default termios — same as pre-modelist stdssh.
func applyModelist(_ *os.File, _ []byte) error { return nil }
