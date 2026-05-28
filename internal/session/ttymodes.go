package session

import "encoding/binary"

// RFC 4254 §8 encoded terminal modes. Opcode 0 is TTY_OP_END (stop). Opcodes
// in [1..159] each carry a uint32 argument. Opcodes [160..255] are reserved
// and force parsing to stop (per the RFC). 128 / 129 are ISPEED / OSPEED.
const (
	ttyOpEnd    uint8 = 0
	ttyOpIspeed uint8 = 128
	ttyOpOspeed uint8 = 129
)

// parseModelist walks the encoded-modes blob calling fn for every (opcode,
// value) pair until TTY_OP_END, an unknown high opcode, or a malformed tail.
// It never returns an error: the wire format is best-effort by design — the
// RFC explicitly says the server should ignore opcodes it doesn't understand.
func parseModelist(b []byte, fn func(opcode uint8, value uint32)) {
	for len(b) > 0 {
		op := b[0]
		b = b[1:]
		if op == ttyOpEnd {
			return
		}
		if op >= 160 {
			return
		}
		if len(b) < 4 {
			return
		}
		v := binary.BigEndian.Uint32(b)
		b = b[4:]
		fn(op, v)
	}
}
