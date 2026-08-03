package e2eselfhost

import (
	"bytes"
	"fmt"
	"os/exec"
	"strings"
	"syscall"
	"testing"
)

// TestSelfHostArm64AsmMemoryLinear pins the in-process arm64 assembler's
// memory to LINEAR in the size of the assembled program (#6011).
//
// arm64_gas_program used to hold the .text buffer inside its Arm64Asm /
// Arm64GasProg structs while patching it, so every fixup — the resolve loop,
// the `.ltorg` pool flush, the @PAGE/@PAGEOFF link, and each backward branch
// in the emit loop — read `a.code` while `a` still referenced it. A second
// reference sends `.with` down the copy-on-write path, so each patched
// instruction word cloned the entire buffer: O(code_len^2) arena traffic, and
// `bin/fern-selfhost -target arm64` exhausted the 16 GiB arena (exit 137)
// compiling an empty `main` that imports std/array. Measured before the fix,
// on this driver: 116 KB of GAS text took 149 MB, 446 KB took 2161 MB, and
// 2.7 MB died. After: 5 MB, 42 MB, and 1485 MB.
//
// The gate is peak RSS on ~450 KB of GAS text — a size the old code needed
// 2.1 GB for. The ceiling is deliberately loose (a ~5x margin over the ~42 MB
// this actually costs) so it fails on a return to quadratic growth, not on
// allocator noise. Skipped when the driver has to run under qemu, where peak
// RSS measures the emulator rather than the program.
func TestSelfHostArm64AsmMemoryLinear(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("driver runs under qemu here; peak RSS would measure the emulator")
	}

	dir := t.TempDir()
	copySelfHostDriver(t, dir, "arm64_asm_bench_run.fern")
	bin := buildSelfHostBin(t, gcc, dir, "arm64_asm_bench_run.fern", "arm64_asm_bench")

	const nInsns = 40000
	src, nLits := gasStressProgram(nInsns)
	if len(src) < 400_000 {
		t.Fatalf("stress program is only %d bytes; too small to catch quadratic growth", len(src))
	}

	cmd := exec.Command(bin)
	cmd.Stdin = strings.NewReader(src)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		t.Fatalf("assembler driver failed on %d bytes of GAS text: %v\nstderr: %s", len(src), err, errb.String())
	}

	// Sanity-check the work actually happened before believing the memory
	// number: every instruction is 4 bytes and each `.ltorg`-flushed literal
	// adds 8, plus a little 8-alignment padding per pool.
	var gotText, gotData int
	if _, err := fmt.Sscanf(out.String(), "text=%d data=%d", &gotText, &gotData); err != nil {
		t.Fatalf("unparsable driver output %q: %v", out.String(), err)
	}
	lo, hi := 4*(nInsns+1), 4*(nInsns+1)+8*nLits+8*nLits
	if gotText < lo || gotText > hi {
		t.Errorf("assembled %d bytes of .text, want %d..%d — the assembler dropped or duplicated work", gotText, lo, hi)
	}

	ru, ok := cmd.ProcessState.SysUsage().(*syscall.Rusage)
	if !ok {
		t.Skip("no rusage on this platform")
	}
	peakMB := ru.Maxrss / 1024 // Linux reports Maxrss in KB
	const ceilingMB = 100
	t.Logf("assembled %d bytes of GAS text; peak RSS %d MB", len(src), peakMB)
	if peakMB > ceilingMB {
		t.Errorf("assembler peak RSS %d MB exceeds the %d MB ceiling for %d bytes of GAS text — "+
			"the .text buffer is being copied per patch again (#6011)", peakMB, ceilingMB, len(src))
	}
}

// gasStressProgram builds a GAS program that exercises all four patch paths at
// once: backward branches (patched during emit), forward branches (queued for
// arm64_asm_resolve), `ldr =N` literals flushed by `.ltorg`, and enough plain
// instructions to make the buffer large. Returns the source and the number of
// literal-pool entries it contains.
func gasStressProgram(n int) (string, int) {
	var b strings.Builder
	lits := 0
	b.WriteString("    .text\n_start:\n")
	for i := 0; i < n; i++ {
		switch i % 8 {
		case 0:
			fmt.Fprintf(&b, ".Lback%d:\n    mov x0, #%d\n", i, i%4096)
		case 1:
			fmt.Fprintf(&b, "    b .Lback%d\n", i-1) // backward: patched in place
		case 2:
			fmt.Fprintf(&b, "    b .Lfwd%d\n", i) // forward: queued as a fixup
		case 3:
			fmt.Fprintf(&b, ".Lfwd%d:\n    add x1, x1, #1\n", i-1)
		case 4:
			fmt.Fprintf(&b, "    ldr x2, =%d\n", i+1) // literal-pool load
			lits++
		case 5:
			b.WriteString("    .ltorg\n    sub x1, x1, #1\n")
		default:
			fmt.Fprintf(&b, "    mov x3, #%d\n", i%4096)
		}
	}
	b.WriteString("    ret\n")
	return b.String(), lits
}
