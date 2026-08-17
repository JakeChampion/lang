package e2eselfhost

import (
	"fmt"
	"strings"
	"testing"
)

// litPoolFixture builds a program whose arm64 .text exceeds the LDR-literal
// reach. Every `var v = <7-digit constant>` lowers to `ldr x0, =N` — a load
// from the assembler's literal pool, reached by a signed 19-bit word offset,
// i.e. ±1 MB. asm_arm64_ir emits a `.ltorg` at each function's `ret` so each
// pool sits beside the loads that use it; with no flush the whole module
// shares one pool at the end of .text and the early functions' loads cannot
// reach it.
//
// nFuncs*nVars is sized so the emitted .text lands well past the reach, with
// enough margin that the fixture keeps failing as codegen shifts.
//
// "~30% past" was the original margin and it did not survive one codegen
// improvement: the arm64 push/pop peephole cut 18% of emitted lines and dropped
// this fixture to 237,639 words, inside the 262,143-word reach, at which point
// it was measuring nothing. The margin is wider now, and the test logs its word
// count on success so the drift is visible before it becomes a failure rather
// than after.
func litPoolFixture(nFuncs, nVars int) (src string, wantExit int) {
	var b strings.Builder
	sum := 0
	for f := 0; f < nFuncs; f++ {
		// `pool_fnN`, not `fN`: `f32` and `f64` are type keywords, and a
		// function so named is a parse error rather than a link failure.
		fmt.Fprintf(&b, "function pool_fn%d(): i32 {\n", f)
		for i := 0; i < nVars; i++ {
			// Each constant is distinct so the assembler cannot dedupe the
			// pool down to a handful of entries.
			fmt.Fprintf(&b, "    var v%d = %d;\n", i, 1000000+f*nVars+i)
		}
		b.WriteString("    return v0 % 7;\n}\n\n")
		sum += (1000000 + f*nVars) % 7
	}
	b.WriteString("function main(): i32 {\n    var s = 0;\n")
	for f := 0; f < nFuncs; f++ {
		fmt.Fprintf(&b, "    s = s + pool_fn%d();\n", f)
	}
	b.WriteString("    return s % 251;\n}\n")
	return b.String(), sum % 251
}

// TestSelfHostArm64LitPoolRange is the regression gate for the missing
// literal-pool flush in asm_arm64_ir.fern.
//
// The arm64 IR emitter routes EVERY integer constant through `ldr xN, =V`,
// but emitted no `.ltorg`, so GNU as had to place one module-wide pool at the
// end of .text. That works only while .text stays inside the LDR-literal
// reach. It had stopped being true by a hair: measured on the commit before
// the fix, the checker driver's .text was 1,043,608 bytes with its first
// literal load 1,043,492 bytes from the pool — 5,080 bytes (~1,270
// instructions) under the 1,048,572-byte limit. Any change that grew the
// emitted code slightly broke the link with ~40 "pc-relative load offset out
// of range" errors, which is how it surfaced (on #6105's inline rc traffic)
// as a mysterious failure of a PR that had not touched arm64 at all.
//
// TestSelfHostCheckerDriverArm64 is the real-world instance of this, but it
// sits right at the boundary and says nothing about the margin. This test
// pins the invariant with room to spare: a program deliberately past the
// reach must assemble, link, and compute the right answer. The run matters as
// much as the link — an out-of-range offset that a laxer assembler masks into
// 19 bits produces a load from the wrong address rather than an error.
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

	// The fixture is only a gate while it actually outruns the reach. Size
	// .text in words, toolchain-free: one word per emitted instruction
	// (indented, non-directive lines) plus two per literal, since each `ldr
	// =V` also costs an 8-byte pool entry. The pool entries have to be counted
	// — with no `.ltorg` they all pile up at the end of .text, so entry k sits
	// 8k bytes BEYOND the last instruction, and it is the far end of that pile
	// that the early loads cannot reach. Instructions alone put this fixture
	// at 972 KB, apparently inside the limit, while the unflushed build in
	// fact fails to assemble at 1.33 MB.
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
	// Logged on success too: this fixture went vacuous once because the only
	// way to see the number was to make it fail.
	t.Logf("fixture .text = %d words, %.1f%% past the %d-word LDR-literal reach",
		words, 100*float64(words-ldrLiteralReach)/float64(ldrLiteralReach), ldrLiteralReach)
	if n := strings.Count(asm, "\n    .ltorg\n"); n < nFuncs {
		t.Errorf("emitted asm has %d `.ltorg` flushes for %d functions — "+
			"asm_arm64_ir must flush the literal pool at every function boundary", n, nFuncs)
	}

	// Assemble + link (this is where the bug reported itself) and run.
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
