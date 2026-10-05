package e2eselfhost

import (
	"fmt"
	"testing"
)

// The addressing-mode differential.
//
// #8083's form matrix is a product of mnemonics, operand forms and widths,
// but it fixes every memory operand at `(%rbx)` — one base register, no
// displacement, no index. That leaves the part of x86 addressing where the
// encoding is NOT a function of the register number:
//
//   - rsp (rm=100) means "a SIB byte follows", so a base of rsp cannot be
//     encoded in ModRM alone and needs a SIB with no index.
//   - rbp (mod=00, rm=101) means "disp32", so a base of rbp with a ZERO
//     displacement must be written mod=01 with an explicit disp8 of 0.
//   - r12 and r13 repeat both quirks through REX.B, which is the half that
//     gets missed: an assembler can special-case rsp and rbp by name and
//     still mis-encode their extended twins.
//
// Getting any of these wrong produces a well-formed instruction addressing
// the wrong memory — the #8083 failure mode, where `movzwq %cx, %rdx`
// assembled to a load from `(%rdi)`. Nothing reports it.
//
// GNU as is the oracle.

// memBases covers every ModRM/SIB special case and its REX.B twin.
var memBases = []string{
	"%rax", // plain
	"%rbx",
	"%rsp", // SIB escape
	"%rbp", // forced displacement
	"%rsi",
	"%rdi",
	"%r8",  // REX.B, plain
	"%r12", // REX.B + SIB escape
	"%r13", // REX.B + forced displacement
	"%r15",
}

// memDisps straddle every width boundary the displacement encoding has: the
// absent/zero form, the disp8 range and both of its edges, and the first
// value on each side that needs a disp32.
var memDisps = []int{0, 1, 127, 128, -1, -128, -129, 4096}

// attMem renders one memory operand.
func attMem(base string, disp int, index string, scale int) string {
	d := ""
	if disp != 0 {
		d = fmt.Sprint(disp)
	}
	if index == "" {
		return fmt.Sprintf("%s(%s)", d, base)
	}
	return fmt.Sprintf("%s(%s,%s,%d)", d, base, index, scale)
}

// memFormCases is base x displacement, then base x index x scale, in both
// load and store direction.
func memFormCases() []string {
	var out []string
	for _, b := range memBases {
		for _, d := range memDisps {
			out = append(out,
				fmt.Sprintf("movq %s, %%rcx", attMem(b, d, "", 0)),
				fmt.Sprintf("movq %%rcx, %s", attMem(b, d, "", 0)),
				// A second width, so a REX.W hard-coded into the memory
				// path shows up rather than agreeing with itself.
				fmt.Sprintf("movl %s, %%ecx", attMem(b, d, "", 0)),
			)
		}
	}
	// The index half. rsp cannot BE an index (rm=100 in the SIB names "no
	// index"), but r12 can, which is the case an assembler that filters the
	// index by register number rather than by name gets wrong.
	for _, b := range []string{"%rax", "%rsp", "%rbp", "%r12", "%r13"} {
		for _, idx := range []string{"%rax", "%rcx", "%r12", "%r15"} {
			for _, scale := range []int{1, 2, 4, 8} {
				for _, d := range []int{0, 8, -8} {
					out = append(out, fmt.Sprintf("movq %s, %%rdx", attMem(b, d, idx, scale)))
				}
			}
		}
	}
	return out
}

// TestSelfHostX86MemFormsMatchGas byte-compares every addressing mode
// against GNU as. A self-host refusal is a failure, not a skip: a refused
// line is an instruction that would have left the byte stream.
func TestSelfHostX86MemFormsMatchGas(t *testing.T) {
	cases := memFormCases()
	if len(cases) < 200 {
		t.Fatalf("the matrix produced only %d cases; it is meant to be a product of bases, displacements and index forms", len(cases))
	}
	compareFormCases(t, cases)
}
