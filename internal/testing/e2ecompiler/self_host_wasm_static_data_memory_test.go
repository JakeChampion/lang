package e2ecompiler

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

const (
	bigLiteralBytes = 32 * 1024
	bigLiteralCount = 40
)

func bigLiteralProgram() string {
	var b strings.Builder
	b.WriteString("function main(): i32 {\n  let n: i32 = 0;\n")
	for i := 0; i < bigLiteralCount; i++ {
		fmt.Fprintf(&b, "  let s%d: string = \"%04d%s\";\n", i, i, strings.Repeat("x", bigLiteralBytes-4))
		fmt.Fprintf(&b, "  n = n + s%d.len();\n", i)
	}
	fmt.Fprintf(&b, "  return n / %d;\n}\n", bigLiteralBytes)
	return b.String()
}

var memoryDeclRe = regexp.MustCompile(`\(memory \(export "memory"\) (\d+)\)`)

func TestSelfHostWasmMemoryCoversStaticData(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host wasm static-data memory e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/wasm_ir_run.fern", "driver")

	src := bigLiteralProgram()
	var cmd *exec.Cmd
	if len(runner) == 0 {
		cmd = exec.Command(driverBin)
	} else {
		cmd = exec.Command(runner[0], append(append([]string{}, runner[1:]...), driverBin)...)
	}
	cmd.Stdin = bytes.NewReader([]byte(src))
	wat, err := cmd.Output()
	if err != nil || len(wat) == 0 {
		t.Fatalf("wasm IR driver failed: %v", err)
	}

	m := memoryDeclRe.FindSubmatch(wat)
	if m == nil {
		t.Fatal("emitted module has no `(memory (export \"memory\") N)` declaration")
	}
	pages, err := strconv.Atoi(string(m[1]))
	if err != nil {
		t.Fatalf("unreadable page count %q: %v", m[1], err)
	}
	if wantAtLeast := bigLiteralCount * bigLiteralBytes / (64 * 1024); pages <= wantAtLeast {
		t.Errorf("declared %d pages for ~%d KiB of literals; the data alone needs more than %d",
			pages, bigLiteralCount*bigLiteralBytes/1024, wantAtLeast)
	}

	watFile := filepath.Join(dir, "bigdata.wat")
	if err := os.WriteFile(watFile, wat, 0o644); err != nil {
		t.Fatalf("write wat: %v", err)
	}
	run := exec.Command("wasmtime", "run", watFile)
	var errb bytes.Buffer
	run.Stderr = &errb
	_ = run.Run()
	if run.ProcessState == nil || !run.ProcessState.Exited() {
		t.Fatalf("wasmtime did not exit normally:\n%s", errb.String())
	}
	if code := run.ProcessState.ExitCode(); code != bigLiteralCount {
		t.Errorf("exit %d, want %d — the program sums its literals' lengths, so a wrong answer means the data moved and a trap means the module could not be instantiated\n%s",
			code, bigLiteralCount, errb.String())
	}
}
