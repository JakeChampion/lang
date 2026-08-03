package e2eselfhost

import (
	"bytes"
	"fmt"
	"os/exec"
	"strings"
	"testing"
)

// TestSelfHostArm64AsmMemoryLinear pins the in-process arm64 assembler's
// memory to LINEAR in the size of the assembled program (#6011).
//
// arm64_gas_program used to hold the .text buffer inside its Arm64Asm /
// Arm64GasProg structs while patching it, so every fixup read `a.code` while
// `a` still referenced it. A second reference sends `.with` down the
// copy-on-write path, so each patched instruction word cloned the whole
// buffer: O(code_len^2) arena traffic, and `bin/fern-selfhost -target arm64`
// exhausted the 16 GiB arena (exit 137) compiling an empty `main` that
// imports std/array.
//
// The gate is the ARENA HIGH-WATER MARK (__heap_bump_bytes — total bytes the
// allocator handed out fresh, i.e. everything the freelist could not
// recycle), NOT peak RSS. RSS is not portable here: a first touch anywhere in
// the 16 GiB MAP_NORESERVE arena maps a 2 MB huge page under THP=always, so
// the same binary on the same input measured 43 MB on a madvise host and
// 552 MB on an always host. The bump counter is exact and host-independent.
//
// Measured on ~900 KB of stress input: 18 MB now. Before the fix the same
// driver needed 238 MB at HALF this size and exhausted the arena at this one.
func TestSelfHostArm64AsmMemoryLinear(t *testing.T) {
	gcc, runner := x86_64Tooling(t)

	dir := t.TempDir()
	copySelfHostDriver(t, dir, "arm64_asm_bench_run.fern")
	bin := buildSelfHostBin(t, gcc, dir, "arm64_asm_bench_run.fern", "arm64_asm_bench")

	const nInsns = 40000
	src, nLits := gasStressProgram(nInsns)
	if len(src) < 800_000 {
		t.Fatalf("stress program is only %d bytes; too small to catch quadratic growth", len(src))
	}

	var cmd *exec.Cmd
	if len(runner) == 0 {
		cmd = exec.Command(bin)
	} else {
		cmd = exec.Command(runner[0], append(append([]string{}, runner[1:]...), bin)...)
	}
	cmd.Stdin = strings.NewReader(src)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		t.Fatalf("assembler driver failed on %d bytes of GAS text: %v\nstderr: %s", len(src), err, errb.String())
	}

	var gotText, gotData, bump int
	if _, err := fmt.Sscanf(out.String(), "text=%d data=%d bump=%d", &gotText, &gotData, &bump); err != nil {
		t.Fatalf("unparsable driver output %q: %v", out.String(), err)
	}

	// Check the work actually happened before believing the memory number:
	// every instruction is 4 bytes and each `.ltorg`-flushed literal adds 8,
	// plus a little 8-alignment padding per pool.
	lo, hi := 4*(nInsns+1), 4*(nInsns+1)+16*nLits
	if gotText < lo || gotText > hi {
		t.Errorf("assembled %d bytes of .text, want %d..%d — the assembler dropped or duplicated work", gotText, lo, hi)
	}

	const ceilingBytes = 64 << 20
	t.Logf("assembled %d bytes of GAS text; arena high-water %d bytes", len(src), bump)
	if bump > ceilingBytes {
		t.Errorf("assembler allocated %d bytes for %d bytes of GAS text, over the %d-byte ceiling — "+
			"the .text buffer is being copied per patch again (#6011)", bump, len(src), ceilingBytes)
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
