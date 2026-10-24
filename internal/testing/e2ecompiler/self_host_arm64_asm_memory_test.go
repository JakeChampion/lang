package e2ecompiler

import (
	"bytes"
	"fmt"
	"os/exec"
	"strings"
	"testing"
)

func TestSelfHostArm64AsmMemoryLinear(t *testing.T) {
	gcc, runner := x86_64Tooling(t)

	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/arm64_asm_bench_run.fern")
	bin := buildSelfHostBin(t, gcc, dir, "drivers/arm64_asm_bench_run.fern", "arm64_asm_bench")

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

func gasStressProgram(n int) (string, int) {
	var b strings.Builder
	lits := 0
	b.WriteString("    .text\n_start:\n")
	for i := 0; i < n; i++ {
		switch i % 8 {
		case 0:
			fmt.Fprintf(&b, ".Lback%d:\n    mov x0, #%d\n", i, i%4096)
		case 1:
			fmt.Fprintf(&b, "    b .Lback%d\n", i-1)
		case 2:
			fmt.Fprintf(&b, "    b .Lfwd%d\n", i)
		case 3:
			fmt.Fprintf(&b, ".Lfwd%d:\n    add x1, x1, #1\n", i-1)
		case 4:
			fmt.Fprintf(&b, "    ldr x2, =%d\n", i+1)
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
