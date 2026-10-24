package e2ecompiler

import (
	"strings"
	"testing"
)

var arm64Conditions = []string{
	"eq", "ne", "cs", "hs", "cc", "lo", "mi", "pl",
	"vs", "vc", "hi", "ls", "ge", "lt", "gt", "le", "al", "nv",
}

func TestSelfHostArm64ConditionsMatchGas(t *testing.T) {
	var cases []string
	for _, cond := range arm64Conditions {
		cases = append(cases,
			"csel x0, x1, x2, "+cond,
			"csinc x5, x6, x7, "+cond,
			"ccmp x0, x1, #0, "+cond,
			"fcsel d0, d1, d2, "+cond,
		)
		if cond != "al" && cond != "nv" {
			cases = append(cases,
				"cset x0, "+cond,
				"csetm x3, "+cond,
				"cinc x0, x1, "+cond,
				"cneg x4, x5, "+cond,
			)
		}
		cases = append(cases, "l0:\n\tb."+cond+" l0")
	}
	compareArm64Cases(t, cases)
}

func TestSelfHostArm64RefusesBadConditions(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	bin := buildAsmBenchDriver(t, gcc)

	for _, bad := range []string{
		"csel x0, x1, x2, zz",
		"ccmp x0, x1, #0, qq",
		"fcsel d0, d1, d2, xx",
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
		tok := bad[strings.LastIndex(bad, " ")+1:]
		if !strings.Contains(strings.Join(refused, " "), tok) {
			t.Errorf("%q: refused as %v, which does not name %q", bad, refused, tok)
		}
	}
}
