package x86_64

import (
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/native/elf"
)

func nops(n int) string { return strings.Repeat("nop\n", n) }

func hexNops(n int) string { return strings.Repeat("90", n) }

// Short-branch relaxation basics, byte-for-byte what GNU as emits for the
// same snippets (captured from as/objdump): in-range jmp/jcc shrink to
// EB ib / 70+cc ib, call never shrinks, and a label defined AT the
// shrinking instruction (self-loop) or at its very next instruction
// (rel 0) lands on the right side of the size change.
func TestRelaxShortBranches(t *testing.T) {
	cases := []struct{ src, want string }{
		{"L:\njmp L", "ebfe"},
		{"L:\njz L", "74fe"},
		{"L:\ncall L", "e8fbffffff"}, // no short call form
		{"jmp L\nret\nL:\nret", "eb01c3c3"},
		{"jne L\nret\nL:\nret", "7501c3c3"},
		{"jmp L\nL:\nret", "eb00c3"},
		// L sits at the start of the second (shrinking) jmp: it must map
		// to that instruction's new offset, not trail the old one.
		{"jmp M\nL: jmp M\nM: ret", "eb02eb00c3"},
		{"f:\ncall f\njmp f\nret", "e8fbffffffebf9c3"},
	}
	for _, c := range cases {
		if got := asm(t, c.src); got != c.want {
			t.Errorf("%-24q = %s, want %s", c.src, got, c.want)
		}
	}
}

// The rel8 range boundaries, exactly: the displacement is relative to the
// END of the SHORT instruction, so +127 forward is 127 bytes between the
// two-byte branch and its target, and -128 backward is 126 filler bytes
// plus the branch itself. One byte more stays rel32. Byte expectations
// captured from GNU as, which picks the same forms.
func TestRelaxRangeBoundaries(t *testing.T) {
	cases := []struct {
		name, src, want string
	}{
		{"fwd +127", "jmp L\n" + nops(127) + "L: ret", "eb7f" + hexNops(127) + "c3"},
		{"fwd +128", "jmp L\n" + nops(128) + "L: ret", "e980000000" + hexNops(128) + "c3"},
		{"back -128 jmp", "L:\n" + nops(126) + "jmp L", hexNops(126) + "eb80"},
		{"back -129 jmp", "L:\n" + nops(127) + "jmp L", hexNops(127) + "e97cffffff"},
		{"back -128 jz", "L:\n" + nops(126) + "jz L", hexNops(126) + "7480"},
		{"back -129 jz", "L:\n" + nops(127) + "jz L", hexNops(127) + "0f847bffffff"},
	}
	for _, c := range cases {
		if got := asm(t, c.src); got != c.want {
			t.Errorf("%s = %s, want %s", c.name, got, c.want)
		}
	}
}

// A branch that is only in range because another branch between it and its
// target shrank: the first jmp spans 129 bytes while the second is long,
// 126 once it shrinks. GNU as converges to the same bytes.
func TestRelaxChainReaction(t *testing.T) {
	src := "jmp L1\n" + nops(124) + "jmp L2\nL2:\nL1: ret"
	want := "eb7e" + hexNops(124) + "eb00c3"
	if got := asm(t, src); got != want {
		t.Errorf("chain = %s, want %s", got, want)
	}
}

// An alignment pad between a branch and its target can make BOTH layouts
// self-consistent: long js (6) + 120 nops ends at 129, pads 15 to 144, disp
// 135 — out of rel8 range, so long looks forced; short js (2) ends at 5,
// pads 3 to 128, disp 123 — in range. Grow-only relaxation must land on the
// short fixpoint, which is the one GNU as emits (found by the gas fuzz
// lane; a shrink-only pass kept the long form).
func TestRelaxAlignmentTwoFixpoints(t *testing.T) {
	src := "A:\nnop\njmp A\njs L\n" + nops(120) + ".p2align 4\nL: ret"
	want := "90ebfd787b" + hexNops(120) + "0f1f00" + "c3"
	if got := asm(t, src); got != want {
		t.Errorf("two-fixpoint alignment = %s, want %s", got, want)
	}
}

// Growth must cascade one branch at a time: with everything short, BOTH
// branches here are out of range (jle by distance, jno by one byte over the
// pad). Growing jle first shifts jno by 4, and the pad before its target
// absorbs the shift — jno lands back in range and must stay short, as GNU
// as emits it. Pinning both against the all-short layout kept jno long
// (found by the gas fuzz lane).
func TestRelaxGrowthCascadeThroughPad(t *testing.T) {
	src := "A:\n" + nops(140) + "jle A\njno L\n" + nops(120) + ".p2align 4\nL: ret"
	want := hexNops(140) + "0f8e6effffff" + "717c" + hexNops(120) + "0f1f4000" + "c3"
	if got := asm(t, src); got != want {
		t.Errorf("growth cascade = %s, want %s", got, want)
	}
}

// A rip-relative data access AFTER a shrunk branch: the ripFixup's at and
// end offsets (and the .rodata base derived from the final .text length)
// must be remapped before the disp32 resolves. Layout: text = short jmp
// (2) + lea (7) + ret (1) = 10 bytes, .rodata at align8(10) = 16, lea ends
// at 9, so disp = 16-9 = 7. (GNU as emits the same code bytes with the
// disp left to the linker.)
func TestRelaxRipRelativeAfterShrink(t *testing.T) {
	src := "jmp L\nL:\nlea rax, [rip + sym]\nret\n.section .rodata\nsym:\n.quad 0"
	want := "eb00" + "488d0507000000" + "c3"
	if got := asm(t, src); got != want {
		t.Errorf("rip after shrink = %s, want %s", got, want)
	}
}

// The other side of the empty-pad tie: a rip-relative instruction whose
// END shares an offset with an originally empty pad stays BEFORE the pad
// when it grows. Layout: short jmp (2) + lea ending at 9, .balign 4 pad
// grows 0 to 3, ret at 12, .rodata at align8(13) = 16, so disp = 16-9 = 7
// — not 4, which is what resolving against the post-pad offset would
// give. (gas emits the same code bytes with the disp left to the linker.)
func TestRelaxRipEndAtEmptyPad(t *testing.T) {
	src := "jmp L\nL: lea rax, [rip + sym]\n.balign 4\nM: ret\n.section .rodata\nsym:\n.quad 0"
	want := "eb00" + "488d0507000000" + "0f1f00" + "c3"
	if got := asm(t, src); got != want {
		t.Errorf("rip end at empty pad = %s, want %s", got, want)
	}
}

// .loc rows and the .symtab label map are remapped onto the relaxed
// layout: the row recorded at the long jcc's follower (offset 6) must land
// at the short form's follower (offset 2).
func TestRelaxLocRowsAndSyms(t *testing.T) {
	src := ".loc 1 5\njz L\n.loc 1 7\nL: ret"
	text, _, syms, rows, err := AssembleProgramWXSyms(src, elf.SegmentAddrsWXX86)
	if err != nil {
		t.Fatalf("AssembleProgramWXSyms: %v", err)
	}
	if want := []byte{0x74, 0x00, 0xC3}; string(text) != string(want) {
		t.Fatalf("text = %x, want %x", text, want)
	}
	if got := syms["L"]; got != elf.TextVAddrWX+2 {
		t.Errorf("sym L = %#x, want %#x", got, elf.TextVAddrWX+2)
	}
	wantRows := []LineRow{{Offset: 0, File: 1, Line: 5, IsStmt: true}, {Offset: 2, File: 1, Line: 7, IsStmt: true}}
	if len(rows) != len(wantRows) || rows[0] != wantRows[0] || rows[1] != wantRows[1] {
		t.Errorf("locRows = %v, want %v", rows, wantRows)
	}
}

// Alignment pads between a shrinking branch and its label are re-sized so
// the label stays aligned; a pad can GROW (within [0, align-1]) as code
// shrinks, including from a width max-skip had suppressed. And when that
// growth would push the branch back out of rel8 range, the branch stays
// rel32 — at offset 5+127=132 the .balign 4 pad is 0 and the gap is
// exactly 127, but shrinking would move the pad to 129, grow it to 3, and
// leave a disp of 130. All three expectations are byte-for-byte GNU as.
func TestRelaxAlignmentInteraction(t *testing.T) {
	cases := []struct {
		name, src, want string
	}{
		{"pad recomputed", "jmp L\n.p2align 3\nL: ret", "eb06" + "660f1f440000" + "c3"},
		{"pad grows past max-skip", "jmp L\n.balign 4,,2\nL: ret", "eb02" + "6690" + "c3"},
		{"pad growth keeps branch long", "jmp L\n" + nops(127) + ".balign 4\nL: ret",
			"e97f000000" + hexNops(127) + "c3"},
		// An ORIGINALLY EMPTY pad shares its offset with the label after
		// it; when shrinkage grows the pad (0 to 3 here), the label must
		// land after the new NOPs, at the aligned offset — GNU as agrees.
		{"label after grown empty pad", "jmp L\nret\nret\nret\n.balign 8\nL: ret",
			"eb06" + "c3c3c3" + "0f1f00" + "c3"},
	}
	for _, c := range cases {
		got := asm(t, c.src)
		if got != c.want {
			t.Errorf("%s = %s, want %s", c.name, got, c.want)
		}
	}
	// The aligned label really is aligned in the relaxed layout.
	text, _, err := AssembleProgram("jmp L\n.p2align 3\nL: ret", elf.TextVAddr)
	if err != nil {
		t.Fatal(err)
	}
	if len(text) != 9 || text[8] != 0xC3 {
		t.Errorf("ret not at aligned offset 8: % x", text)
	}
}

// Whole-snippet parity with GNU as on branchy code (expectations captured
// from as/objdump — the live oracle for these shapes is the "branches" /
// "branch_alignment" cases in TestAssembleAgainstGNUAs).
func TestRelaxGasSnippets(t *testing.T) {
	cases := []struct {
		name, src, want string
	}{
		{"count loop",
			"f:\nxor eax, eax\nmov ecx, 10\nL1:\ninc eax\ndec ecx\njnz L1\ncmp eax, 10\njne L2\nret\nL2:\nud2",
			"31c0b90a000000ffc0ffc975fa83f80a7501c30f0b"},
		{"aligned loop",
			"f:\nxor eax, eax\n.p2align 4\nL1:\ninc eax\ncmp eax, 100\njl L1\nret",
			"31c0" + "66662e0f1f8400000000000f1f00" + "ffc083f8647cf9c3"},
	}
	for _, c := range cases {
		if got := asm(t, c.src); got != c.want {
			t.Errorf("%s = %s, want %s", c.name, got, c.want)
		}
	}
}
