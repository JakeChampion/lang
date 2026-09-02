package e2eselfhost

import (
	"fmt"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/native/arm64"
)

// The arm64 load/store offset differential.
//
// AArch64 has two immediate-offset encodings for the same written syntax,
// and the assembler picks between them from the offset itself:
//
//	LDR  imm12, UNSIGNED and SCALED by the access size — reaches 4095*size,
//	     but only offsets that are a multiple of size.
//	LDUR imm9, SIGNED and UNSCALED — any alignment, but only -256..255.
//
// So `ldr x1, [x2, #8]` is an LDR and `ldr x1, [x2, #4]` is an LDUR, from
// alignment alone; `[x2, #255]` is an LDUR and `[x2, #256]` an LDR, from
// range. Both are spelled `ldr`, and both encode to a valid instruction, so
// choosing the wrong one does not fail — it addresses the wrong byte. An
// assembler that always scales silently divides an unaligned offset; one
// that never scales silently truncates a large one.
//
// The existing self-host arm64 tests probe offsets -16, -8, 0, 1, 4, 5, 6,
// 8, 16 and 255. Not one of the selection boundaries is among them, and
// the largest is 255 — the last value before the rule changes.
//
// internal/native/arm64 is the oracle. It is the right one: it gets all
// eleven boundaries right, checked directly against
// aarch64-linux-gnu-as + objdump, and it reaches them by a different path
// than the self-host does.

// memSize pairs a load/store mnemonic with the access size that scales its
// imm12 — the number the whole selection rule turns on.
var arm64MemForms = []struct {
	load, store string
	reg         string
	size        int
}{
	{"ldrb", "strb", "w1", 1},
	{"ldrh", "strh", "w1", 2},
	{"ldr", "str", "w1", 4},
	{"ldr", "str", "x1", 8},
	// The sign-extending loads have no store sibling; the store column
	// repeats the load so the product stays rectangular and every case is
	// still a real instruction.
	{"ldrsb", "ldrsb", "w1", 1},
	{"ldrsh", "ldrsh", "w1", 2},
	{"ldrsb", "ldrsb", "x1", 1},
	{"ldrsw", "ldrsw", "x1", 4},
}

// arm64MemOffsets straddles every boundary of the two encodings: zero, an
// aligned and an unaligned small offset, both edges of LDUR's signed range,
// the first offset past it (which must therefore be scaled), and the top of
// LDR's scaled range.
func arm64MemOffsets(size int) []int {
	offs := []int{0, 1, size, size + 1, 8, 255, 256, 257, -1, -8, -255, -256}
	// The scaled ceiling, and the last scaled offset below it.
	offs = append(offs, 4095*size, 4094*size)
	// A large offset that is NOT a multiple of the size cannot be scaled and
	// is out of LDUR's range, so it is unencodable in one instruction; those
	// are filtered by arm64MemEncodable rather than listed here.
	if size > 1 {
		offs = append(offs, 4095*size-1)
	}
	return offs
}

// arm64MemEncodable reports whether one instruction can carry the offset:
// LDUR's signed 9-bit range, or LDR's scaled unsigned 12-bit one.
func arm64MemEncodable(off, size int) bool {
	if off >= -256 && off <= 255 {
		return true
	}
	return off >= 0 && off%size == 0 && off/size <= 4095
}

// arm64MemCases is the product of form, base register and offset, in both
// directions.
func arm64MemCases() []string {
	var out []string
	for _, f := range arm64MemForms {
		for _, base := range []string{"x2", "x28", "sp"} {
			for _, off := range arm64MemOffsets(f.size) {
				if !arm64MemEncodable(off, f.size) {
					continue
				}
				out = append(out,
					fmt.Sprintf("%s %s, [%s, #%d]", f.load, f.reg, base, off),
					fmt.Sprintf("%s %s, [%s, #%d]", f.store, f.reg, base, off),
				)
			}
		}
	}
	return out
}

// TestSelfHostArm64MemOffsetsMatchNative byte-compares every encodable
// (form, base, offset) through both assemblers.
func TestSelfHostArm64MemOffsetsMatchNative(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	bin := buildAsmBenchDriver(t, gcc)

	cases := arm64MemCases()
	if len(cases) < 200 {
		t.Fatalf("the matrix produced only %d cases; it is meant to be a product of forms, bases and offsets", len(cases))
	}

	// The oracle, one line at a time so a native refusal names its own line.
	want := make([]uint32, 0, len(cases))
	kept := make([]string, 0, len(cases))
	for _, c := range cases {
		b, _, err := arm64.AssembleProgram(c+"\n", 0x400000)
		if err != nil {
			t.Errorf("%-32s internal/native/arm64 rejects it, so it cannot be the oracle: %v", c, err)
			continue
		}
		if len(b) != 4 {
			t.Errorf("%-32s native emitted %d bytes, want one word", c, len(b))
			continue
		}
		want = append(want, uint32(b[0])|uint32(b[1])<<8|uint32(b[2])<<16|uint32(b[3])<<24)
		kept = append(kept, c)
	}

	var prog strings.Builder
	prog.WriteString(".text\n_start:\n")
	for _, c := range kept {
		prog.WriteString("    " + c + "\n")
	}
	got := assembleSelfHost(t, bin, runner, prog.String())
	if len(got) != len(kept) {
		t.Fatalf("the self-host assembler produced %d words for %d instructions — it dropped or split one", len(got), len(kept))
	}
	for i, c := range kept {
		if got[i] != want[i] {
			t.Errorf("%-32s self-host %08x, internal/native/arm64 %08x", c, got[i], want[i])
		}
	}
}
