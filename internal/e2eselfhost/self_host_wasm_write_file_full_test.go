package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// A write that fails is an error on wasm too. The preview 1
// `$__fern_write_file` dropped fd_write's errno and wrote once, so a full
// device answered Ok and a short write left a short file. Preopening /dev
// hands the guest /dev/full, whose every write fails with ENOSPC; native and
// the interpreter answer "err" to this program run from /dev.
const selfHostWriteFileFullWasmSource = `function main(): i32 {
    match (write_file("full", "hello")) {
        Ok(_) => { print("ok"); return 1; },
        Err(e) => { print("err"); return 0; }
    }
    return 2;
}
`

func TestSelfHostWasmWriteFileReportsFullDevice(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("/dev/full is a Linux device")
	}
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH")
	}
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("the CLI driver takes host filesystem paths as argv")
	}
	stdlibRoot, err := filepath.Abs("../../internal/stdlib")
	if err != nil {
		t.Fatal(err)
	}
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "fern.fern")
	fernBin := buildSelfHostBin(t, gcc, dir, "fern.fern", "fern")

	work := t.TempDir()
	src := filepath.Join(work, "main.fern")
	if err := os.WriteFile(src, []byte(selfHostWriteFileFullWasmSource), 0o644); err != nil {
		t.Fatal(err)
	}
	wat := filepath.Join(work, "main.wat")
	compile := exec.Command(fernBin, "-target", "wasm32-wasi", "-emit", "asm", "-o", wat, src, stdlibRoot)
	var stderr strings.Builder
	compile.Stderr = &stderr
	if err := compile.Run(); err != nil {
		t.Fatalf("compile: %v\n%s", err, stderr.String())
	}
	out, code := runBin(exec.Command("wasmtime", "run", "--dir", "/dev::/", wat), "")
	if code != 0 || out != "err\n" {
		t.Fatalf("answered %q exit %d, want %q exit 0", out, code, "err\n")
	}
}
