package arm64

import (
	"encoding/binary"
	"testing"
)

// assembleShortLitReach parses gas source, shrinks the ldr-literal span, and
// lays it out at the W^X text address. Shrinking is what keeps these tests to a
// few hundred instructions instead of the megabyte imm19 really reaches.
func assembleShortLitReach(t *testing.T, src string, reach int) (*Assembler, []uint32) {
	t.Helper()
	a, err := ParseProgram(src)
	if err != nil {
		t.Fatalf("ParseProgram: %v", err)
	}
	a.relaxReach = reach
	if _, _, err := bytesWX(a); err != nil {
		t.Fatalf("BytesProgramWX: %v", err)
	}
	return a, a.insns
}

// A literal load whose pool sits past its reach used to be refused outright,
// which is where a 26 MB self-host module stopped. The value is re-homed into a
// pool spliced in near the load instead.
func TestFarLiteralGetsAPoolWithinReach(t *testing.T) {
	src := "_start:\n\tldr x3, =0x1122334455667788\n" + nops(300) + "\tret\n"
	a, insns := assembleShortLitReach(t, src, 64)

	at := a.labels["_start"]
	if got := insns[at] & 0xff000000; got != 0x58000000 {
		t.Fatalf("instruction at the load is 0x%08x, not an ldr-literal", insns[at])
	}
	// imm19 is bits 23:5, signed, in instructions.
	off := int32(insns[at]<<8) >> 13
	if off < -64 || off >= 64 {
		t.Errorf("the load reaches %d instructions, past the %d span it was given", off, 64)
	}
	pool := at + int(off)
	got := uint64(insns[pool]) | uint64(insns[pool+1])<<32
	if got != 0x1122334455667788 {
		t.Errorf("the pool at %d holds %#x, want %#x", pool, got, uint64(0x1122334455667788))
	}
	if pool%2 != 0 {
		t.Errorf("a wide literal landed at instruction %d, which is not 8-byte aligned", pool)
	}
}

// Two loads of the same value near each other share one pool entry rather than
// each planting their own.
func TestNearbyLoadsOfOneValueShareAPoolEntry(t *testing.T) {
	src := "_start:\n\tldr x3, =0xdeadbeefcafef00d\n\tldr x4, =0xdeadbeefcafef00d\n" + nops(300) + "\tret\n"
	a, insns := assembleShortLitReach(t, src, 64)
	start := a.labels["_start"]
	poolOf := func(at int) int { return at + int(int32(insns[at]<<8)>>13) }
	if x, y := poolOf(start), poolOf(start+1); x != y {
		t.Errorf("two loads of one value point at %d and %d, want the same entry", x, y)
	}
}

// A load whose pool is already in reach must be left exactly as it was: no
// island, no duplicate literal.
func TestNearLiteralIsUntouched(t *testing.T) {
	src := "_start:\n\tldr x3, =7\n\tret\n\t.ltorg\n"
	before, err := ParseProgram(src)
	if err != nil {
		t.Fatalf("ParseProgram: %v", err)
	}
	before.relaxReach = 64
	if _, _, err := bytesWX(before); err != nil {
		t.Fatalf("BytesProgramWX: %v", err)
	}
	// ldr, ret, then the two words of the 64-bit literal — nothing spliced in.
	if n := len(before.insns); n != 4 {
		t.Errorf("a program with a reachable literal assembled to %d instructions, want 4", n)
	}
}

// The pool is data sitting in the instruction stream, so control has to go
// around it: the island is headed by a branch past its own end.
func TestPoolIslandIsHoppedOver(t *testing.T) {
	src := "_start:\n\tldr x3, =0x1122334455667788\n" + nops(300) + "\tret\n"
	a, insns := assembleShortLitReach(t, src, 64)
	start := a.labels["_start"]
	pool := start + int(int32(insns[start]<<8)>>13)
	// The island starts with its hop: b, pad, then the wide entry.
	hop := pool - 2
	if got := insns[hop] & 0xfc000000; got != 0x14000000 {
		t.Fatalf("instruction at %d is 0x%08x, want the island's hop-over b", hop, insns[hop])
	}
	dest := hop + int(int32(insns[hop]<<6)>>6)
	if dest <= pool+1 {
		t.Errorf("the hop lands at %d, inside its own pool (entry at %d..%d)", dest, pool, pool+1)
	}
}

// Whatever the assembler emits has to be a program that runs, so the words
// around a spliced pool must still decode as the code that was written.
func TestCodeAroundAPoolStillAssembles(t *testing.T) {
	src := "_start:\n\tldr x3, =0x1122334455667788\n\tmov x0, #7\n" + nops(300) + "\tmov x1, #9\n\tret\n"
	a, insns := assembleShortLitReach(t, src, 64)
	start := a.labels["_start"]
	want, err := Assemble("\tmov x0, #7\n")
	if err != nil {
		t.Fatal(err)
	}
	if got := insns[start+1]; got != binary.LittleEndian.Uint32(want) {
		t.Errorf("the instruction after the load is 0x%08x, want the mov 0x%08x", got, binary.LittleEndian.Uint32(want))
	}
}
