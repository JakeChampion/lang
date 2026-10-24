package e2ecompiler

import (
	"fmt"
	"strings"
	"testing"
)

func litPoolFixture(nFuncs, nVars int) (src string, wantExit int) {
	var b strings.Builder
	sum := 0
	for f := 0; f < nFuncs; f++ {
		fmt.Fprintf(&b, "function pool_fn%d(p: i32): i32 {\n    let s = 0;\n", f)
		acc := 0
		for i := 0; i < nVars; i++ {
			k := 1000000 + f*nVars + i
			fmt.Fprintf(&b, "    s = s + (p | %d);\n", k)
			acc += 1 | k
		}
		b.WriteString("    return s % 7;\n}\n\n")
		sum += acc % 7
	}
	b.WriteString("function main(): i32 {\n    let s = 0;\n")
	for f := 0; f < nFuncs; f++ {
		fmt.Fprintf(&b, "    s = s + pool_fn%d(1);\n", f)
	}
	b.WriteString("    return s % 251;\n}\n")
	return b.String(), sum % 251
}

func TestSelfHostArm64LitPoolRange(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	_, x86runner, driverBin := buildModloadArm64DriverX86(t)

	const nFuncs, nVars = 400, 190
	src, wantExit := litPoolFixture(nFuncs, nVars)
	asm, progDir := compileFilesModload(t, x86runner, driverBin,
		map[string]string{"main.fern": src}, "-target", "arm64-linux")
	if len(asm) == 0 {
		t.Fatal("self-host arm64 compiler emitted 0 bytes for the literal-pool fixture")
	}

	words := 0
	for _, line := range strings.Split(asm, "\n") {
		if strings.HasPrefix(line, "    ") && !strings.HasPrefix(strings.TrimSpace(line), ".") {
			words++
		}
	}
	words += 2 * strings.Count(asm, ", =")
	const ldrLiteralReach = 262143
	if words <= ldrLiteralReach {
		t.Fatalf("fixture emits %d words of .text, inside the %d-word LDR-literal reach — "+
			"it no longer exercises the pool-distance bug; raise nFuncs/nVars", words, ldrLiteralReach)
	}
	t.Logf("fixture .text = %d words, %.1f%% past the %d-word LDR-literal reach",
		words, 100*float64(words-ldrLiteralReach)/float64(ldrLiteralReach), ldrLiteralReach)
	if n := strings.Count(asm, "\n    .ltorg\n"); n < nFuncs {
		t.Errorf("emitted asm has %d `.ltorg` flushes for %d functions — "+
			"asm_arm64_ir must flush the literal pool at every function boundary", n, nFuncs)
	}

	bin := buildBin(t, arm64gcc, progDir, "litpool", asm)
	cmd := runArm64Bin(qemu, bin)
	_ = cmd.Run()
	if cmd.ProcessState == nil || !cmd.ProcessState.Exited() {
		t.Fatal("literal-pool fixture did not exit normally")
	}
	if got := cmd.ProcessState.ExitCode(); got != wantExit {
		t.Errorf("literal-pool fixture exited %d, want %d — "+
			"a wrapped imm19 loads from the wrong address instead of failing to link", got, wantExit)
	}
}
