package e2eselfhost

import (
	"fmt"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/native/arm64"
)

// The arm64 operand-form differential (#7903 phase 5).
//
// The x86-64 side has three of these — the ALU/shift/extend form product, the
// addressing-mode product, and the mov/push/pop/branch product — and between
// them they found eleven defects, nine of them silent. arm64 had one: the
// load/store offset differential (#8088). Every defect any of them found came
// from a family with no table on at least one side, or from a boundary no
// probe sat on, and arm64's non-memory families are exactly that: dispatched
// by hand on both sides with nothing comparing the two form by form.
//
// AArch64 hides more encoding choices behind one written syntax than x86 does,
// which is what makes the product worth building rather than sampling:
//
//   - `add x1, x2, x3` is a shifted-register add, but `add sp, sp, x3` is an
//     EXTENDED-register one — the operand decides the encoding, not the
//     mnemonic.
//   - `add x1, x2, #4096` cannot use imm12 unshifted, so it becomes
//     `#1, lsl #12`; the assembler picks, and picking wrong is a valid
//     instruction adding a different number.
//   - `and x1, x2, #0xff` is a bitmask immediate — N:immr:imms, the most
//     error-prone field in the ISA — while `and x1, x2, x3` is a register form.
//   - `mov x1, #imm` is movz, movn, or an orr-immediate depending on the value,
//     the same selection class that hid the x86 movq immediate bug.
//   - `lsl x1, x2, #3` is a ubfm alias whose two fields are both derived from
//     the shift amount, and `lsl x1, x2, x3` is a different instruction.
//
// internal/native/arm64 is the oracle, as in the memory differential: it is
// pinned to aarch64-linux-gnu-as, and it reaches its answers by a different
// path than the self-host does.

// arm64FormCases builds the product. Each entry is one instruction; both
// assemblers must produce the same word.
func arm64FormCases() []string {
	var out []string
	add := func(fs string, args ...any) { out = append(out, fmt.Sprintf(fs, args...)) }

	// The two register widths, with their zero register and a second operand.
	widths := []struct{ d, n, m, zr string }{
		{"x1", "x2", "x3", "xzr"},
		{"w1", "w2", "w3", "wzr"},
	}

	// --- the add/sub group: register, shifted register, imm12, shifted imm12.
	for _, w := range widths {
		for _, m := range []string{"add", "adds", "sub", "subs"} {
			add("%s %s, %s, %s", m, w.d, w.n, w.m)
			for _, sh := range []string{"lsl", "lsr", "asr"} {
				for _, amt := range []int{1, 4, 31} {
					add("%s %s, %s, %s, %s #%d", m, w.d, w.n, w.m, sh, amt)
				}
			}
			// imm12 and its boundaries: the last unshifted value, the first
			// that needs the lsl #12 form, and the top of the shifted range.
			// 0xFFF000 is the largest encodable immediate at all — anything
			// above it, or between two multiples of 4096, needs a materialise
			// (see TestSelfHostArm64RefusesUnencodableForms).
			for _, imm := range []int{0, 1, 255, 4095, 4096, 8192, 16773120} {
				add("%s %s, %s, #%d", m, w.d, w.n, imm)
			}
		}
	}
	// The zero-register destinations these alias to, and the sp forms, which
	// take the extended-register encoding rather than the shifted-register one.
	add("cmp x2, x3")
	add("cmn x2, x3")
	add("cmp w2, w3")
	// imm12 is unsigned, so a negative is the opposite mnemonic carrying the
	// magnitude (`add #-16` is `sub #16`, `cmp` becomes `cmn`), and an
	// explicit `, lsl #12` names the shift instead of leaving it derived —
	// `#0, lsl #12` keeps sh=1 rather than collapsing to the unshifted form.
	for _, m := range []string{"add", "sub", "adds", "subs"} {
		for _, imm := range []string{"-1", "-16", "-4095", "-4096", "-16773120"} {
			add("%s x1, x2, #%s", m, imm)
			add("%s w1, w2, #%s", m, imm)
		}
		add("%s x1, x2, #1, lsl #12", m)
		add("%s x1, x2, #0, lsl #12", m)
		add("%s w1, w2, #4095, lsl #12", m)
		add("%s x1, x2, #7, lsl #0", m)
		add("%s x1, x2, #-1, lsl #12", m)
	}
	add("cmp x1, #-16")
	add("cmn x1, #-16")
	add("cmp w1, #-4096")
	add("cmn w1, #-1")
	add("add sp, sp, #-16")
	add("sub sp, sp, #-16")
	add("add sp, sp, #-4096")
	add("cmp x2, #4095")
	add("cmp x2, #4096")
	add("cmn w2, #1")
	add("add sp, sp, #16")
	add("sub sp, sp, #16")
	add("add x1, sp, #32")
	add("mov x1, sp")
	add("mov sp, x1")

	// --- the logical group: register, shifted register, bitmask immediate.
	for _, w := range widths {
		for _, m := range []string{"and", "orr", "eor"} {
			add("%s %s, %s, %s", m, w.d, w.n, w.m)
			for _, amt := range []int{1, 8} {
				add("%s %s, %s, %s, lsl #%d", m, w.d, w.n, w.m, amt)
			}
		}
	}
	// Bitmask immediates: runs anchored at bit 0, runs rotated off it, the
	// all-ones-but-one shapes, and the repeating patterns only the smaller
	// element sizes reach. The rotated and repeating ones are what a 32-bit
	// immediate can only express by being replicated into 64 bits first
	// (#8138); passing the 64-bit fields through instead refused them.
	for _, imm := range []string{"0x1", "0x3", "0xff", "0xffff", "0xfffe",
		"0xf0f0f0f0", "0x1010101", "0x8000000f", "0xfffffffe", "0x3fffc000"} {
		add("and w1, w2, #%s", imm)
		add("orr w1, w2, #%s", imm)
		add("eor w1, w2, #%s", imm)
	}
	for _, imm := range []string{"0x1", "0xff", "0xffff", "0xffffffff",
		"0xf0f0f0f0f0f0f0f0", "0x5555555555555555", "0xfffffffffffffffe",
		"0x0000ffff0000ffff", "0xff00ff00ff00ff00"} {
		add("and x1, x2, #%s", imm)
		add("orr x1, x2, #%s", imm)
		add("eor x1, x2, #%s", imm)
	}
	// ands and its tst alias take the same immediate through a different emit
	// path, so they need their own rows: covering only and/orr/eor left the
	// flag-setting sibling parsing at 32 bits and encoding a truncated mask
	// while the shared vet accepted the full-width value.
	for _, imm := range []string{"0xff", "0xfffe", "0xf0f0f0f0"} {
		add("ands w1, w2, #%s", imm)
		add("tst w1, #%s", imm)
	}
	for _, imm := range []string{"0xff", "0x5555555555555555", "0xfffffffffffffffe"} {
		add("ands x1, x2, #%s", imm)
		add("tst x1, #%s", imm)
	}
	add("ands x1, x2, x3")
	add("ands w1, w2, w3")
	add("tst x1, x2")

	// --- the move-wide family and the immediate selection above it.
	for _, sh := range []int{0, 16, 32, 48} {
		add("movz x1, #%d, lsl #%d", 0x1234, sh)
		add("movk x1, #%d, lsl #%d", 0x1234, sh)
		add("movn x1, #%d, lsl #%d", 0x1234, sh)
	}
	for _, sh := range []int{0, 16} {
		add("movz w1, #%d, lsl #%d", 0x1234, sh)
		add("movk w1, #%d, lsl #%d", 0x1234, sh)
	}
	// `mov reg, #imm` — the assembler chooses movz, movn or an orr-immediate.
	// 4294967295 is absent on purpose: it needs two mov-wide chunks, so gas
	// and native build it with an orr-bitmask, which this assembler does not
	// reach for. It is pinned as a refusal instead — a safe one.
	for _, imm := range []string{"0", "1", "65535", "65536", "-1", "-2"} {
		add("mov x1, #%s", imm)
	}
	for _, imm := range []string{"0", "1", "65535", "65536", "-1"} {
		add("mov w1, #%s", imm)
	}
	add("mov x1, x2")
	add("mov w1, w2")

	// --- the bitfield aliases: every one derives its fields from the operand.
	for _, w := range widths {
		hi := 63
		if w.d[0] == 'w' {
			hi = 31
		}
		for _, m := range []string{"lsl", "lsr", "asr", "ror"} {
			for _, amt := range []int{0, 1, 7, hi} {
				add("%s %s, %s, #%d", m, w.d, w.n, amt)
			}
			// The register-operand form is a different instruction sharing
			// the spelling.
			add("%s %s, %s, %s", m, w.d, w.n, w.m)
		}
		for _, f := range [][2]int{{0, 1}, {0, 8}, {4, 4}, {8, 8}} {
			add("ubfx %s, %s, #%d, #%d", w.d, w.n, f[0], f[1])
			add("sbfx %s, %s, #%d, #%d", w.d, w.n, f[0], f[1])
		}
	}
	add("extr x1, x2, x3, #1")
	add("extr x1, x2, x3, #63")
	add("extr w1, w2, w3, #1")

	// --- the extends, which are bitfield aliases too.
	for _, m := range []string{"sxtb", "sxth", "uxtb", "uxth"} {
		add("%s w1, w2", m)
	}
	for _, m := range []string{"sxtb", "sxth", "sxtw"} {
		add("%s x1, w2", m)
	}

	// --- multiply, divide, and the count/reverse unaries.
	for _, w := range widths {
		add("mul %s, %s, %s", w.d, w.n, w.m)
		add("msub %s, %s, %s, %s", w.d, w.n, w.m, w.d)
		add("sdiv %s, %s, %s", w.d, w.n, w.m)
		add("udiv %s, %s, %s", w.d, w.n, w.m)
		add("neg %s, %s", w.d, w.n)
		add("clz %s, %s", w.d, w.n)
		add("cls %s, %s", w.d, w.n)
		add("rbit %s, %s", w.d, w.n)
		add("rev %s, %s", w.d, w.n)
		add("rev16 %s, %s", w.d, w.n)
	}
	add("rev32 x1, x2")

	// --- the conditional selects, over every condition spelling.
	for _, c := range []string{"eq", "ne", "cs", "hs", "cc", "lo", "mi", "pl",
		"vs", "vc", "hi", "ls", "ge", "lt", "gt", "le"} {
		add("csel x1, x2, x3, %s", c)
		add("csel w1, w2, w3, %s", c)
		add("cset x1, %s", c)
		add("cset w1, %s", c)
	}
	return out
}

// TestSelfHostArm64FormsMatchNative byte-compares every case through both
// assemblers. A self-host refusal is a failure, not a skip: a refused line is
// an instruction that would have left the word stream.
func TestSelfHostArm64FormsMatchNative(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	bin := buildAsmBenchDriver(t, gcc)

	cases := arm64FormCases()
	if len(cases) < 300 {
		t.Fatalf("the matrix produced only %d cases; it is meant to be a product of families, forms and widths", len(cases))
	}

	// The oracle, one line at a time so a native refusal names its own line
	// rather than poisoning the whole batch.
	want := make([]uint32, 0, len(cases))
	kept := make([]string, 0, len(cases))
	for _, c := range cases {
		b, _, err := arm64.AssembleProgram(c+"\n", 0x400000)
		if err != nil {
			t.Errorf("%-40s internal/native/arm64 rejects it, so it cannot be the oracle: %v", c, err)
			continue
		}
		if len(b) != 4 {
			t.Errorf("%-40s native emitted %d bytes, want one word", c, len(b))
			continue
		}
		want = append(want, uint32(b[0])|uint32(b[1])<<8|uint32(b[2])<<16|uint32(b[3])<<24)
		kept = append(kept, c)
	}

	// One line per driver run. A batch would be faster, but a refusal in a
	// batch reports a tag and not the line that produced it, and which FORM
	// was refused is the whole finding.
	for i, c := range kept {
		if refused := refusalsFor(t, bin, runner, ".text\n_start:\n    "+c+"\n"); len(refused) > 0 {
			t.Errorf("%-40s the self-host assembler REFUSES it (%s); native emits %08x",
				c, strings.Join(refused, ", "), want[i])
			continue
		}
		got := assembleSelfHost(t, bin, runner, ".text\n_start:\n    "+c+"\n")
		if len(got) != 1 {
			t.Errorf("%-40s the self-host assembler produced %d words, want one", c, len(got))
			continue
		}
		if got[0] != want[i] {
			t.Errorf("%-40s self-host %08x, internal/native/arm64 %08x", c, got[0], want[i])
		}
	}
}

// TestSelfHostArm64RefusesUnencodableForms pins the one shape gas encodes and
// this assembler does not: a mov immediate needing two mov-wide chunks, which
// gas builds with an orr-bitmask rather than a mov. Refusing is safe — the
// driver checks p.unknown and declines to write the image — and refusing is
// what it does now; before #7903 phase 5 it encoded `movn x1, #0` and loaded
// -1 instead.
//
// The row asserts native still ENCODES it, so it fails the day the gap closes
// rather than outliving it.
func TestSelfHostArm64RefusesUnencodableForms(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	bin := buildAsmBenchDriver(t, gcc)

	lines := []string{"mov x1, #4294967295"}
	for _, ln := range lines {
		if _, _, err := arm64.AssembleProgram(ln+"\n", 0x400000); err != nil {
			t.Errorf("%-40s internal/native/arm64 refuses it too, so this row does not "+
				"describe a self-host gap — check it: %v", ln, err)
		}
	}
	checkRefusedSelfHost(t, bin, runner, lines)
}

// TestSelfHostArm64RefusesUnencodableImmediates pins the other direction: an
// immediate NEITHER assembler can encode must be refused by both, not masked
// into a different number by one of them.
//
// imm12 is twelve unsigned bits, optionally shifted left twelve, so it holds
// 0..4095 and the multiples of 4096 up to 0xFFF000 — nothing between two
// multiples above 4095, and nothing past the ceiling. The self-host masked
// every one of these to twelve bits and encoded it anyway, which is how
// `sub sp, sp, #4096` became `sub sp, sp, #0`: a function needing a frame
// that large allocated none at all, and nothing failed at assemble time.
func TestSelfHostArm64RefusesUnencodableImmediates(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	bin := buildAsmBenchDriver(t, gcc)

	lines := []string{
		"add x1, x2, #4097",      // between two multiples of 4096
		"sub x1, x2, #8191",      // likewise
		"sub sp, sp, #16777215",  // past the shifted ceiling
		"cmp x2, #5000",          // the cmp alias takes the same field
		"cmn w2, #4097",          // and cmn
		"adds x1, x2, #16777216", // 0x1000000: one past 0xFFF000
		"add x1, x2, #-4097",     // negative, and the magnitude does not fit
		"cmn x1, #-16777215",     // likewise through the alias
		// An explicit shift operand names the field directly, so only the
		// bare twelve bits are reachable and only lsl #0 / lsl #12 spell it.
		"add x1, x2, #4096, lsl #12",
		"add x1, x2, #-4096, lsl #12",
		"add x1, x2, #5, lsr #12",
		"add x1, x2, #5, lsl #13",
		// A logical immediate wider than the register it applies to. The low
		// half of each IS a valid bitmask, so a width check that is not there
		// encodes that instead of refusing — a different mask, silently.
		"and w1, w2, #0x1000000ff",
		"orr w1, w2, #0x300000001",
	}
	for _, ln := range lines {
		if _, _, err := arm64.AssembleProgram(ln+"\n", 0x400000); err == nil {
			t.Errorf("%-40s internal/native/arm64 ACCEPTS it, so it is encodable after all "+
				"and the self-host should encode it rather than refuse", ln)
		}
	}
	checkRefusedSelfHost(t, bin, runner, lines)
}
