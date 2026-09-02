package e2eselfhost

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

// The self-host wasm backend declared `(memory (export "memory") 16)` — a
// hardcoded 16 pages, 1 MiB — however much static data the module carried.
//
// Data segments are written at instantiation and the two freelist arrays are
// written by __fern_free, and neither grows memory: only __fern_alloc does, off
// $heap. So a module whose literals push $heap past the declared minimum traps
// on load, before a single instruction of the program runs, with "out of bounds
// memory access" and nothing pointing at the size.
//
// Every driver stayed under 1 MiB of literals, so nothing hit it until
// playground_run.fern carried the stdlib as `-embed` assets: 1.9 MB of data
// needing 30 pages, a module that validated and could not be instantiated.
// This reproduces it without -embed, from literals alone.

const (
	// Distinct 32 KiB literals. Distinct matters: identical ones dedupe to a
	// single data segment and the module stays small.
	bigLiteralBytes = 32 * 1024
	// 40 of them is ~1.3 MB of data, needing 21 pages against the old 16. The
	// count is the assertion: below ~32 the module fits and the bug is
	// invisible, which is why every existing driver missed it.
	bigLiteralCount = 40
)

// bigLiteralProgram returns a program whose string literals exceed one page
// budget, summing their lengths so the exit code proves the data survived the
// move rather than merely that the module loaded.
func bigLiteralProgram() string {
	var b strings.Builder
	b.WriteString("function main(): i32 {\n  var n: i32 = 0;\n")
	for i := 0; i < bigLiteralCount; i++ {
		fmt.Fprintf(&b, "  var s%d: string = \"%04d%s\";\n", i, i, strings.Repeat("x", bigLiteralBytes-4))
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
	copySelfHostDriver(t, dir, "wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")

	src := bigLiteralProgram()
	var cmd *exec.Cmd
	if len(runner) == 0 {
		cmd = exec.Command(driverBin, "-ir")
	} else {
		cmd = exec.Command(runner[0], append(append(append([]string{}, runner[1:]...), driverBin), "-ir")...)
	}
	cmd.Stdin = bytes.NewReader([]byte(src))
	wat, err := cmd.Output()
	if err != nil || len(wat) == 0 {
		t.Fatalf("wasm IR driver failed: %v", err)
	}

	// The declared minimum, read back so a failure says "16 pages" rather than
	// only "it trapped".
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
