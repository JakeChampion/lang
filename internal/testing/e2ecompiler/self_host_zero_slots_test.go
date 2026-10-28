package e2ecompiler

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// zeroSlotsProgram gives mix sixteen locals that start at zero, take values
// in one branch and stay live across a call, so the edge that skips the
// branch stores zero to many adjacent spill slots.
func zeroSlotsProgram() string {
	var b strings.Builder
	b.WriteString("function step(n: i64): i64 {\n  return n * 7 + 1;\n}\n\n")
	b.WriteString("function mix(c: boolean, n: i64): i64 {\n")
	sum := []string{}
	for i := 0; i < 16; i++ {
		fmt.Fprintf(&b, "  let a%d: i64 = 0;\n", i)
		sum = append(sum, fmt.Sprintf("a%d", i))
	}
	b.WriteString("  if (c) {\n    a0 = step(n);\n")
	for i := 1; i < 16; i++ {
		fmt.Fprintf(&b, "    a%d = step(a%d);\n", i, i-1)
	}
	b.WriteString("  }\n  print(\"mixed\");\n  return " + strings.Join(sum, " + ") + ";\n}\n\n")
	b.WriteString("function main(): i32 {\n  let on: i64 = mix(true, 2);\n  let off: i64 = mix(false, 2);\n")
	b.WriteString("  if (on != 84005463384264 || off != 0) {\n    return 1;\n  }\n  return 0;\n}\n")
	return b.String()
}

// Adjacent spill slots that an edge sets to zero share one 16-byte store:
// movups from a cleared %xmm2 on x86-64, stp xzr, xzr on arm64.
func TestSelfHostZeroSlotsPaired(t *testing.T) {
	cli := buildSelfHostCLI(t)
	src := filepath.Join(t.TempDir(), "main.fern")
	if err := os.WriteFile(src, []byte(zeroSlotsProgram()), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ target, pair string }{
		{"x86-64-linux", "movups %xmm2, "},
		{"arm64-linux", "stp xzr, xzr, "},
	} {
		asm := filepath.Join(t.TempDir(), "out.s")
		if out, err := runX86_64Bin(cli.runner, cli.bin, "-target", c.target, "-emit", "asm", "-o", asm, src, cli.stdlib).CombinedOutput(); err != nil {
			t.Fatalf("%s: %v\n%s", c.target, err, out)
		}
		text, err := os.ReadFile(asm)
		if err != nil {
			t.Fatal(err)
		}
		if n := strings.Count(asmFunction(string(text), "__fn_mix:"), c.pair); n < 3 {
			t.Errorf("%s: mix has %d paired zero stores (%q), want at least 3", c.target, n, c.pair)
		}
	}
	bin := cli.x86Binary(t, src, "FERN_STRICT_IR=1")
	run := exec.Command(bin)
	if len(cli.runner) > 0 {
		run = exec.Command(cli.runner[0], append(append([]string{}, cli.runner[1:]...), bin)...)
	}
	if out, err := run.CombinedOutput(); err != nil {
		t.Errorf("x86-64-linux: %v\n%s", err, out)
	}
	if stderr, exit := cli.exitOf(t, zeroSlotsProgram(), "arm64-linux"); exit != 0 {
		t.Errorf("arm64-linux: exit %d\n%s", exit, stderr)
	}
}
