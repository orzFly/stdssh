//go:build linux

package session

import (
	"os"

	"golang.org/x/sys/unix"
)

// Opcode → c_cc[] index. Matches OpenSSH's ttymodes.h. Indices come from
// golang.org/x/sys/unix so they track the kernel's actual termios layout.
var ccOpcodes = map[uint8]int{
	1:  unix.VINTR,
	2:  unix.VQUIT,
	3:  unix.VERASE,
	4:  unix.VKILL,
	5:  unix.VEOF,
	6:  unix.VEOL,
	7:  unix.VEOL2,
	8:  unix.VSTART,
	9:  unix.VSTOP,
	10: unix.VSUSP,
	12: unix.VREPRINT,
	13: unix.VWERASE,
	14: unix.VLNEXT,
	18: unix.VDISCARD,
}

// Opcode → bitmask for c_iflag / c_lflag / c_oflag / c_cflag. Values are
// platform termios constants (uint32 on Linux). 0 (= no bit) is reserved
// internally to mean "absent"; on Linux every flag has at least one bit set
// so this is safe.
var iflagOpcodes = map[uint8]uint32{
	30: unix.IGNPAR,
	31: unix.PARMRK,
	32: unix.INPCK,
	33: unix.ISTRIP,
	34: unix.INLCR,
	35: unix.IGNCR,
	36: unix.ICRNL,
	37: unix.IUCLC,
	38: unix.IXON,
	39: unix.IXANY,
	40: unix.IXOFF,
	41: unix.IMAXBEL,
	42: unix.IUTF8,
}

var lflagOpcodes = map[uint8]uint32{
	50: unix.ISIG,
	51: unix.ICANON,
	52: unix.XCASE,
	53: unix.ECHO,
	54: unix.ECHOE,
	55: unix.ECHOK,
	56: unix.ECHONL,
	57: unix.NOFLSH,
	58: unix.TOSTOP,
	59: unix.IEXTEN,
	60: unix.ECHOCTL,
	61: unix.ECHOKE,
	62: unix.PENDIN,
}

var oflagOpcodes = map[uint8]uint32{
	70: unix.OPOST,
	71: unix.OLCUC,
	72: unix.ONLCR,
	73: unix.OCRNL,
	74: unix.ONOCR,
	75: unix.ONLRET,
}

var cflagOpcodes = map[uint8]uint32{
	90: unix.CS7,
	91: unix.CS8,
	92: unix.PARENB,
	93: unix.PARODD,
}

// applyModelist reads the slave's current termios, overlays the SSH-encoded
// modes from the client, and writes it back via TCSETS (~ tcsetattr TCSANOW).
// Baud opcodes are accepted but not applied — PTYs ignore line speed, and
// translating the numeric baud through Bxxx constants in Go is fiddly with no
// observable benefit.
func applyModelist(slave *os.File, modes []byte) error {
	if len(modes) == 0 {
		return nil
	}
	fd := int(slave.Fd())
	t, err := unix.IoctlGetTermios(fd, unix.TCGETS)
	if err != nil {
		return err
	}
	parseModelist(modes, func(op uint8, v uint32) {
		if op == ttyOpIspeed || op == ttyOpOspeed {
			return
		}
		if idx, ok := ccOpcodes[op]; ok {
			if idx >= 0 && idx < len(t.Cc) {
				t.Cc[idx] = byte(v)
			}
			return
		}
		if bit, ok := iflagOpcodes[op]; ok {
			if v != 0 {
				t.Iflag |= bit
			} else {
				t.Iflag &^= bit
			}
			return
		}
		if bit, ok := lflagOpcodes[op]; ok {
			if v != 0 {
				t.Lflag |= bit
			} else {
				t.Lflag &^= bit
			}
			return
		}
		if bit, ok := oflagOpcodes[op]; ok {
			if v != 0 {
				t.Oflag |= bit
			} else {
				t.Oflag &^= bit
			}
			return
		}
		if bit, ok := cflagOpcodes[op]; ok {
			if v != 0 {
				t.Cflag |= bit
			} else {
				t.Cflag &^= bit
			}
			return
		}
		// Unknown opcode — already consumed its u32; nothing to do.
	})
	return unix.IoctlSetTermios(fd, unix.TCSETS, t)
}
