package e2ecompiler

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const arm64Arch = ".arch armv8.2-a+crypto"

// Each arm64 assembly file names the baseline in its first line, so an
// external assembler accepts the LSE, CRC and crypto instructions with no
// flag. The internal assembler accepts that line and refuses each other
// architecture.
func TestSelfHostArm64AsmNamesTheBaseline(t *testing.T) {
	cli := buildSelfHostCLI(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "main.fern")
	if err := os.WriteFile(src, []byte(atomicsSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{"arm64-linux", "arm64-darwin", "arm64-android"} {
		asm, err := os.ReadFile(cli.emit(t, src, target))
		if err != nil {
			t.Fatal(err)
		}
		first, _, _ := strings.Cut(string(asm), "\n")
		if strings.TrimSpace(first) != arm64Arch {
			t.Errorf("%s: the first line is %q, want %q", target, first, arm64Arch)
		}
		if strings.Contains(string(asm), ".arch_extension") {
			t.Errorf("%s: the assembly has an .arch_extension line", target)
		}
	}

	gcc, runner := x86_64Tooling(t)
	copySelfHostDriver(t, dir, "drivers/asm_refusals_run.fern")
	driver := buildSelfHostBin(t, gcc, dir, "drivers/asm_refusals_run.fern", "asm_refusals_run")
	for _, c := range []struct{ line, refusal string }{
		{arm64Arch, ""},
		{".arch armv8-a", ".arch armv8-a: the arm64 targets assemble " + arm64Arch + " alone\n"},
		{".arch armv9-a", ".arch armv9-a: the arm64 targets assemble " + arm64Arch + " alone\n"},
		{".arch_extension lse", ".arch_extension lse: the arm64 targets assemble " + arm64Arch + " alone\n"},
	} {
		text := "    " + c.line + "\n    .text\nf:\n    ldaddal x1, x0, [x2]\n    ret\n"
		out, err := runX86_64Bin(runner, driver, "arm64", mustWrite(t, dir, "arch.s", text)).Output()
		if err != nil {
			t.Fatalf("%s: run: %v", c.line, err)
		}
		if string(out) != c.refusal {
			t.Errorf("%s: refusals %q, want %q", c.line, out, c.refusal)
		}
	}
}
