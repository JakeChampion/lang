package e2eselfhost

import (
	"strings"
	"testing"
)

// arm64Conditions is every condition spelling, aliases included. cs/hs and
// cc/lo are the same code; al and nv are distinct codes that both behave as
// "always" at run time but do NOT share an encoding, which is what made
// folding one onto the other invisible (#8075).
var arm64Conditions = []string{
	"eq", "ne", "cs", "hs", "cc", "lo", "mi", "pl",
	"vs", "vc", "hi", "ls", "ge", "lt", "gt", "le", "al", "nv",
}

// TestSelfHostArm64ConditionsMatchGas is the condition-field gate.
//
// The self-host decoded conditions with a lookup that returned 0 — `eq` — for
// anything it did not recognise, and none of its five call sites checked. So
// `csel x0, x1, x2, zz` assembled as `csel ..., eq`, and `csel ..., nv`, which
// GNU as encodes 9a82f020, assembled as 9a820020. A valid instruction, a wrong
// condition, and nothing reported: the failure mode that survives every test
// that only asks whether a program builds.
func TestSelfHostArm64ConditionsMatchGas(t *testing.T) {
	var cases []string
	for _, cond := range arm64Conditions {
		cases = append(cases,
			"csel x0, x1, x2, "+cond,
			"csinc x5, x6, x7, "+cond,
			"ccmp x0, x1, #0, "+cond,
			"fcsel d0, d1, d2, "+cond,
		)
		// The inverting aliases take every condition EXCEPT al and nv, which
		// the refusal test below covers. Their encoders flip the condition, so
		// an off-by-one in the table shows up here as the opposite branch.
		if cond != "al" && cond != "nv" {
			cases = append(cases,
				"cset x0, "+cond,
				"csetm x3, "+cond,
				"cinc x0, x1, "+cond,
				"cneg x4, x5, "+cond,
			)
		}
		// The one condition-taking form that needs a label: a zero-distance
		// BACKWARD branch, which settles on the short encoding, so the
		// condition field is the only thing left that can differ.
		cases = append(cases, "l0:\n\tb."+cond+" l0")
	}
	compareArm64Cases(t, cases)
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
