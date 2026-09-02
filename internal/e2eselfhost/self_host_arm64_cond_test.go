package e2eselfhost

import (
	"encoding/binary"
	"strings"
	"testing"

	nativearm64 "github.com/jakechampion/lang/internal/native/arm64"
)

// arm64Conditions is every condition spelling, aliases included. cs/hs and
// cc/lo are the same code; al and nv are distinct codes that both behave as
// "always" at run time but do NOT share an encoding, which is what made
// folding one onto the other invisible (#8075).
var arm64Conditions = []string{
	"eq", "ne", "cs", "hs", "cc", "lo", "mi", "pl",
	"vs", "vc", "hi", "ls", "ge", "lt", "gt", "le", "al", "nv",
}

// TestSelfHostArm64ConditionsMatchNative is the gate the arm64 pair never had.
//
// The self-host decoded conditions with a lookup that returned 0 — `eq` — for
// anything it did not recognise, and none of its five call sites checked. So
// `csel x0, x1, x2, zz` assembled as `csel ..., eq`, and `csel ..., nv`, which
// GNU as encodes 9a82f020, assembled as 9a820020. A valid instruction, a wrong
// condition, and nothing reported: the failure mode that survives every test
// that only asks whether a program builds.
//
// Comparing against the native assembler rather than against pinned bytes is
// deliberate — the native side is itself pinned to GNU as, and this way one
// list of spellings covers both directions at once.
func TestSelfHostArm64ConditionsMatchNative(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	bin := buildAsmBenchDriver(t, gcc)

	for _, cond := range arm64Conditions {
		forms := []string{
			"csel x0, x1, x2, " + cond,
			"csinc x5, x6, x7, " + cond,
			"ccmp x0, x1, #0, " + cond,
			"fcsel d0, d1, d2, " + cond,
		}
		// The inverting aliases take every condition EXCEPT al and nv, which
		// the refusal test below covers. Their encoders flip the condition, so
		// an off-by-one in the table shows up here as the opposite branch.
		if cond != "al" && cond != "nv" {
			forms = append(forms,
				"cset x0, "+cond,
				"csetm x3, "+cond,
				"cinc x0, x1, "+cond,
				"cneg x4, x5, "+cond,
			)
		}
		for _, form := range forms {
			src := ".text\n_start:\n\t" + form + "\n"
			text, _, err := nativearm64.AssembleProgram(src, 0x400000)
			if err != nil {
				t.Errorf("%q: the native assembler rejects it, so it cannot be the oracle: %v", form, err)
				continue
			}
			want := binary.LittleEndian.Uint32(text[len(text)-4:])
			got := assembleSelfHost(t, bin, runner, src)
			if len(got) != 1 || got[0] != want {
				t.Errorf("%q: self-host %08x, internal/native/arm64 %08x", form, got, want)
			}
		}
	}
}

// TestSelfHostArm64BranchConditionsMatchNative covers the one condition-taking
// form that needs a label. It is a zero-distance BACKWARD branch: both
// assemblers must settle that on the short encoding, so the condition field is
// the only thing left that can differ — the same isolation the x86 twin uses.
func TestSelfHostArm64BranchConditionsMatchNative(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	bin := buildAsmBenchDriver(t, gcc)

	for _, cond := range arm64Conditions {
		src := ".text\n_start:\nl0:\n\tb." + cond + " l0\n"
		text, _, err := nativearm64.AssembleProgram(src, 0x400000)
		if err != nil {
			t.Errorf("b.%s: the native assembler rejects it: %v", cond, err)
			continue
		}
		want := binary.LittleEndian.Uint32(text[len(text)-4:])
		got := assembleSelfHost(t, bin, runner, src)
		if len(got) != 1 || got[0] != want {
			t.Errorf("b.%s: self-host %08x, internal/native/arm64 %08x", cond, got, want)
		}
	}
}

// TestSelfHostArm64RefusesBadConditions pins the refusals, since an assembler
// that quietly substitutes a condition passes any test that only compares the
// spellings it does know.
func TestSelfHostArm64RefusesBadConditions(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	bin := buildAsmBenchDriver(t, gcc)

	for _, bad := range []string{
		// Not a condition at all. This used to assemble as `eq`.
		"csel x0, x1, x2, zz",
		"ccmp x0, x1, #0, qq",
		"fcsel d0, d1, d2, xx",
		// AL and NV on the aliases that encode the INVERSE of the written
		// condition; GNU as refuses these by name.
		"cset x0, al", "cset x0, nv",
		"csetm x0, al", "csetm x0, nv",
		"cinc x0, x1, al", "cinv x0, x1, nv", "cneg x0, x1, al",
	} {
		src := ".text\n_start:\n\t" + bad + "\n"
		refused := refusalsFor(t, bin, runner, src)
		if len(refused) == 0 {
			t.Errorf("%q: the self-host assembler accepted it; GNU as refuses it", bad)
			continue
		}
		// The refusal has to name the offending token, or the report says only
		// that some line failed.
		tok := bad[strings.LastIndex(bad, " ")+1:]
		if !strings.Contains(strings.Join(refused, " "), tok) {
			t.Errorf("%q: refused as %v, which does not name %q", bad, refused, tok)
		}
	}
}
