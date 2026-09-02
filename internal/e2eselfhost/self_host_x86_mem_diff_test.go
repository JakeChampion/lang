package e2eselfhost

import (
	"fmt"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/native/x86_64"
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
// internal/native/x86_64 is the oracle: its fuzz lane generates these same
// bases against GNU as, so it is pinned here by a different path than the
// self-host's.

// memBases pairs the AT&T and Intel spelling of each base, chosen to cover
// every ModRM/SIB special case and its REX.B twin.
var memBases = []struct{ att, intel string }{
	{"%rax", "rax"}, // plain
	{"%rbx", "rbx"},
	{"%rsp", "rsp"}, // SIB escape
	{"%rbp", "rbp"}, // forced displacement
	{"%rsi", "rsi"},
	{"%rdi", "rdi"},
	{"%r8", "r8"},   // REX.B, plain
	{"%r12", "r12"}, // REX.B + SIB escape
	{"%r13", "r13"}, // REX.B + forced displacement
	{"%r15", "r15"},
}

// memDisps straddle every width boundary the displacement encoding has: the
// absent/zero form, the disp8 range and both of its edges, and the first
// value on each side that needs a disp32.
var memDisps = []int{0, 1, 127, 128, -1, -128, -129, 4096}

// attMem and intelMem render one operand in each dialect.
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

func intelMem(size, base string, disp int, index string, scale int) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s ptr [%s", size, base)
	if index != "" {
		fmt.Fprintf(&b, "+%s*%d", index, scale)
	}
	switch {
	case disp > 0:
		fmt.Fprintf(&b, "+%d", disp)
	case disp < 0:
		fmt.Fprintf(&b, "-%d", -disp)
	}
	b.WriteString("]")
	return b.String()
}

// memFormCases is base x displacement, then base x index x scale, in both
// load and store direction.
func memFormCases() []formCase {
	var out []formCase
	for _, b := range memBases {
		for _, d := range memDisps {
			out = append(out,
				formCase{
					fmt.Sprintf("movq %s, %%rcx", attMem(b.att, d, "", 0)),
					fmt.Sprintf("mov rcx, %s", intelMem("qword", b.intel, d, "", 0)),
				},
				formCase{
					fmt.Sprintf("movq %%rcx, %s", attMem(b.att, d, "", 0)),
					fmt.Sprintf("mov %s, rcx", intelMem("qword", b.intel, d, "", 0)),
				},
				// A second width, so a REX.W hard-coded into the memory
				// path shows up rather than agreeing with itself.
				formCase{
					fmt.Sprintf("movl %s, %%ecx", attMem(b.att, d, "", 0)),
					fmt.Sprintf("mov ecx, %s", intelMem("dword", b.intel, d, "", 0)),
				},
			)
		}
	}
	// The index half. rsp cannot BE an index (rm=100 in the SIB names "no
	// index"), but r12 can, which is the case an assembler that filters the
	// index by register number rather than by name gets wrong.
	for _, b := range []string{"%rax", "%rsp", "%rbp", "%r12", "%r13"} {
		ib := strings.TrimPrefix(b, "%")
		for _, idx := range []string{"%rax", "%rcx", "%r12", "%r15"} {
			ii := strings.TrimPrefix(idx, "%")
			for _, scale := range []int{1, 2, 4, 8} {
				for _, d := range []int{0, 8, -8} {
					out = append(out, formCase{
						fmt.Sprintf("movq %s, %%rdx", attMem(b, d, idx, scale)),
						fmt.Sprintf("mov rdx, %s", intelMem("qword", ib, d, ii, scale)),
					})
				}
			}
		}
	}
	return out
}

// TestSelfHostX86MemFormsMatchNative byte-compares every addressing mode
// through both assemblers. A self-host refusal is a failure, not a skip: a
// refused line is an instruction that would have left the byte stream.
func TestSelfHostX86MemFormsMatchNative(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	bin := buildX86AsmBenchDriver(t, gcc)

	cases := memFormCases()
	if len(cases) < 200 {
		t.Fatalf("the matrix produced only %d cases; it is meant to be a product of bases, displacements and index forms", len(cases))
	}

	for _, c := range cases {
		want, _, err := x86_64.AssembleProgram(c.intel+"\n", 0x400000)
		if err != nil {
			t.Errorf("%q: internal/native/x86_64 rejects it, so it cannot be the oracle for %q: %v", c.intel, c.att, err)
			continue
		}
		out := runX86BenchDriver(t, bin, runner, ".text\n_start:\n    "+c.att+"\n", "-bytes")
		if refused := asmRefusals(out); len(refused) > 0 {
			t.Errorf("%-34q the self-host assembler REFUSES it; native emits % x", c.att, want)
			continue
		}
		var got []byte
		for _, ln := range strings.Split(out, "\n") {
			var idx, val int
			if _, e := fmt.Sscanf(ln, "byte %d %d", &idx, &val); e == nil {
				got = append(got, byte(val))
			}
		}
		if string(got) != string(want) {
			t.Errorf("%-34q self-host % x, internal/native/x86_64 % x", c.att, got, want)
		}
	}
}
