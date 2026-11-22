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
	cases := []string{
		"1:\n\ttbz w0, #31, 1b", "1:\n\ttbnz xzr, #0x3f, 1b",
		"1:\n\ttbz x0, #010, 1b", "1:\n\ttbz w0, #0b101, 1b",
		"br xzr", "br lr", "blr fp", "blr x0",
		"1:\n\tcbnz wzr, 1b", "1:\n\tbeq 1b",
	}
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

func TestSelfHostArm64RefusesBadBranches(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	bin := buildAsmBenchDriver(t, gcc)
	checkRefusedSelfHost(t, bin, runner, []string{
		"b.zz _start", "b.eq _start, x0", "bl _start, x1",
		"cbnz sp, _start", "cbz d0, _start", "cbz x0, _start, w1",
		"tbz w0, #32, _start", "tbnz x0, #64, _start",
		"tbz x0, #-1, _start", "tbz x0, #0xZZ, _start",
		"tbz x0, #4294967296, _start", "tbz s0, #1, _start",
		"br", "br w3", "br d0", "blr", "blr sp", "blr x0, x1",
		"b", "cbz x0", "tbz x0, #1",
	})
}
