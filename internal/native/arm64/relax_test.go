package arm64

import (
	"testing"

	"github.com/jakechampion/lang/internal/native/elf"
)

// assembleShortRelax parses gas source, shrinks the conditional-branch
// span, and lays it out at the W^X text address. Shrinking is what keeps
// these tests to a few hundred instructions instead of the 1 MB the
// architectural imm19 limit would demand.
func assembleShortRelax(t *testing.T, src string, reach int) (*Assembler, []uint32) {
	t.Helper()
	a, err := ParseProgram(src)
	if err != nil {
		t.Fatalf("ParseProgram: %v", err)
	}
	a.relaxReach = reach
	if _, _, err := a.BytesProgramWX(elf.TextVAddrWX); err != nil {
		t.Fatalf("BytesProgramWX: %v", err)
	}
	return a, a.insns
}

// A conditional branch that cannot reach its target is inverted and made
// to hop over an unconditional `b` that can. A veneer cannot help here —
// the conditional itself is what will not encode — so before this the
// assembler simply refused the program.
func TestFarConditionalBranchIsRelaxed(t *testing.T) {
	src := "_start:\n\tcmp x0, #0\n\tb.eq far\n" + nops(200) + "far:\n\tret\n"
	a, insns := assembleShortRelax(t, src, 64)

	at := a.labels["_start"] + 1 // the cmp, then the branch
	if got := insns[at] & 0xff000010; got != 0x54000000 {
		t.Fatalf("instruction at the branch is 0x%08x, not a b.cond", insns[at])
	}
	if cond := insns[at] & 0xf; cond != 1 { // eq = 0, ne = 1
		t.Errorf("condition is %d, want ne (1) — the branch was not inverted", cond)
	}
	if off := int32(insns[at]<<8) >> 13; off != 2 {
		t.Errorf("inverted branch skips %d instructions, want 2 (over the `b`)", off)
	}
	if got := insns[at+1] & 0xfc000000; got != 0x14000000 {
		t.Errorf("instruction after the conditional is 0x%08x, want an unconditional b", insns[at+1])
	}
	// The spliced `b` must land on the original target.
	if want, got := a.labels["far"], at+1+int(int32(insns[at+1]<<6)>>6); got != want {
		t.Errorf("the b targets instruction %d, want %d (far)", got, want)
	}
}

// cbz/cbnz and tbz/tbnz invert into each other; b.cond flips its
// condition. Each pairing has to survive the round trip.
func TestEveryConditionalFormInverts(t *testing.T) {
	for _, c := range []struct {
		branch string
		want   uint32 // the opcode bits the inverted form must carry
		mask   uint32
	}{
		{"b.eq far", 0x54000001, 0xff00001f},
		{"b.lt far", 0x5400000a, 0xff00001f}, // lt = 11 (b), ge = 10 (a)
		{"cbz x3, far", 0xb5000003, 0xff00001f},
		{"cbnz x3, far", 0xb4000003, 0xff00001f},
		{"cbz w3, far", 0x35000003, 0xff00001f},
		{"tbz x3, #5, far", 0x37000003, 0xff00001f},  // inverts to tbnz
		{"tbnz x3, #5, far", 0x36000003, 0xff00001f}, // and back
	} {
		src := "_start:\n\t" + c.branch + "\n" + nops(200) + "far:\n\tret\n"
		a, insns := assembleShortRelax(t, src, 64)
		at := a.labels["_start"]
		if got := insns[at] & c.mask; got != c.want&c.mask {
			t.Errorf("%s inverted to 0x%08x (masked 0x%08x), want masked 0x%08x", c.branch, insns[at], got, c.want&c.mask)
		}
		if got := insns[at+1] & 0xfc000000; got != 0x14000000 {
			t.Errorf("%s: instruction after it is 0x%08x, want an unconditional b", c.branch, insns[at+1])
		}
	}
}

// A conditional that already reaches is left exactly as written: no
// inversion, no extra instruction.
func TestNearConditionalBranchIsUntouched(t *testing.T) {
	src := "_start:\n\tb.eq near\n" + nops(4) + "near:\n\tret\n"
	a, insns := assembleShortRelax(t, src, 64)
	at := a.labels["_start"]
	if cond := insns[at] & 0xf; cond != 0 {
		t.Errorf("condition is %d, want eq (0) — a reachable branch was relaxed", cond)
	}
	if off := int32(insns[at]<<8) >> 13; off != 5 {
		t.Errorf("branch offset is %d, want 5 (straight to near)", off)
	}
}

// tbz/tbnz reach only ±32 KB against the others' ±1 MB, so a program can
// need one relaxed while the b.cond beside it still encodes.
func TestOnlyTheBranchThatCannotReachIsRelaxed(t *testing.T) {
	src := "_start:\n\tb.eq mid\n\ttbz x1, #0, mid\n" + nops(60) + "mid:\n\tret\n"
	a, err := ParseProgram(src)
	if err != nil {
		t.Fatalf("ParseProgram: %v", err)
	}
	// imm14's real span is a sixteenth of imm19's; mirror that ratio.
	a.relaxReach = 1 << 18
	before := len(a.insns)
	if _, _, err := a.BytesProgramWX(elf.TextVAddrWX); err != nil {
		t.Fatalf("BytesProgramWX: %v", err)
	}
	if len(a.insns) != before {
		t.Errorf("program grew from %d to %d instructions; both branches reach", before, len(a.insns))
	}
}

// Relaxation splices instructions in, which moves every label after the
// splice — including the ones other branches and the `adrp`/`:lo12:`
// symbol references point at. Getting that remap wrong is silent, so
// pin it: a program with a far conditional AND a far call must have
// both land correctly.
func TestRelaxationRemapsTheLabelsAfterIt(t *testing.T) {
	src := "_start:\n\tb.eq far\n" + nops(200) + "far:\n\tadrp x0, tail\n\tadd x0, x0, #:lo12:tail\n" +
		nops(20) + "tail:\n\tret\n"
	a, insns := assembleShortRelax(t, src, 64)

	far := a.labels["far"]
	if got := insns[a.labels["_start"]+1] & 0xfc000000; got != 0x14000000 {
		t.Fatal("the conditional was not relaxed, so this proves nothing")
	}
	// far's own instruction must still be the adrp.
	if got := insns[far] & 0x9f000000; got != 0x90000000 {
		t.Errorf("instruction at far is 0x%08x, want an adrp — the label did not move with its code", insns[far])
	}
	if got := insns[a.labels["tail"]]; got != 0xd65f03c0 {
		t.Errorf("instruction at tail is 0x%08x, want ret — the label did not move with its code", got)
	}
}
