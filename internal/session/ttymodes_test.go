package session

import (
	"encoding/binary"
	"reflect"
	"testing"
)

// helper: build an encoded-modes blob from (opcode, value) pairs and an
// optional trailing TTY_OP_END.
func encModes(pairs []struct {
	op uint8
	v  uint32
}, terminate bool) []byte {
	out := make([]byte, 0, len(pairs)*5+1)
	for _, p := range pairs {
		out = append(out, p.op)
		var u [4]byte
		binary.BigEndian.PutUint32(u[:], p.v)
		out = append(out, u[:]...)
	}
	if terminate {
		out = append(out, ttyOpEnd)
	}
	return out
}

type modeRec struct {
	op uint8
	v  uint32
}

func collect(b []byte) []modeRec {
	var out []modeRec
	parseModelist(b, func(op uint8, v uint32) {
		out = append(out, modeRec{op, v})
	})
	return out
}

func TestParseModelistEmpty(t *testing.T) {
	if got := collect(nil); len(got) != 0 {
		t.Errorf("nil blob produced %v entries, want none", got)
	}
	if got := collect([]byte{}); len(got) != 0 {
		t.Errorf("empty blob produced %v entries, want none", got)
	}
}

func TestParseModelistTerminatedByOpEnd(t *testing.T) {
	in := encModes([]struct {
		op uint8
		v  uint32
	}{
		{1, 0x03},     // VINTR = ^C
		{36, 1},       // ICRNL on
		{53, 0},       // ECHO off
		{128, 38400},  // ISPEED
	}, true)
	// Append garbage after TTY_OP_END — must NOT be parsed.
	in = append(in, 0xFF, 0xAA, 0x55)
	got := collect(in)
	want := []modeRec{{1, 0x03}, {36, 1}, {53, 0}, {128, 38400}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v\nwant %v", got, want)
	}
}

func TestParseModelistStopsOnHighOpcode(t *testing.T) {
	in := encModes([]struct {
		op uint8
		v  uint32
	}{
		{1, 0x03},
	}, false)
	// 160 is reserved per RFC 4254 §8 — parser must stop.
	in = append(in, 160, 0, 0, 0, 99) // 99 = junk that must not be consumed
	got := collect(in)
	want := []modeRec{{1, 0x03}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v\nwant %v", got, want)
	}
}

func TestParseModelistStopsOnTruncatedValue(t *testing.T) {
	// opcode 7 + only 2 bytes of its u32 = malformed tail. Earlier entries
	// must still be reported; parser must not panic.
	in := []byte{
		1, 0, 0, 0, 0x03,
		7, 0xDE, 0xAD,
	}
	got := collect(in)
	want := []modeRec{{1, 0x03}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v\nwant %v", got, want)
	}
}

func TestParseModelistNoTerminator(t *testing.T) {
	// A blob without TTY_OP_END is still well-formed if it ends on a u32
	// boundary; parser keeps going until the buffer is exhausted.
	in := encModes([]struct {
		op uint8
		v  uint32
	}{
		{30, 1},
		{42, 1},
	}, false)
	got := collect(in)
	want := []modeRec{{30, 1}, {42, 1}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v\nwant %v", got, want)
	}
}
