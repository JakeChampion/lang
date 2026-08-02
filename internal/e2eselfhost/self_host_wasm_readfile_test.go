package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestSelfHostWasmReadFileLarge guards the self-host wasm read_file against
// the 64 KiB truncation bug: the preview1 reader used a fixed 64 KiB buffer
// and silently stopped there (a full buffer read back 0 bytes, mistaken for
// EOF). The buffer now grows in 64 KiB chunks, so files larger than 64 KiB
// round-trip. The program reads a 200 000-byte file and checks both its
// length and two marker bytes past the 64 KiB boundary.
func TestSelfHostWasmReadFileLarge(t *testing.T) {
	wasmtime, err := exec.LookPath("wasmtime")
	if err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host wasm read_file e2e")
	}
	gcc, runner := x86_64Tooling(t)

	dir := t.TempDir()
	for _, name := range []string{"lexer.fern", "parser.fern", "util.fern", "astwalk.fern", "asmcore.fern", "ir.fern", "irlower.fern", "asm_ir.fern", "wasm_ir.fern", "wasm_run.fern"} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	wasmRun := buildSelfHostBin(t, gcc, dir, "wasm_run.fern", "wasm_run")

	// 200 KB file, '.' filled, with 'A' at offset 70000 and 'B' at 150000
	// (both past the old 64 KiB cap).
	buf := bytes.Repeat([]byte("."), 200000)
	buf[70000] = 'A'
	buf[150000] = 'B'
	if err := os.WriteFile(filepath.Join(dir, "big.txt"), buf, 0o644); err != nil {
		t.Fatalf("write big.txt: %v", err)
	}

	prog := "function main(): i32 {\n" +
		"    match (read_file(\"big.txt\")) {\n" +
		"        Ok(s) => {\n" +
		"            if (s.len() != 200000) { return 1; }\n" +
		"            if (s[70000] as i32 != 65) { return 2; }\n" +
		"            if (s[150000] as i32 != 66) { return 3; }\n" +
		"            return 0;\n" +
		"        },\n" +
		"        Err(e) => { return 9; }\n" +
		"    }\n" +
		"}\n"
	wat := runCapture(t, gcc, runner, wasmRun, []byte(prog))
	if len(wat) == 0 {
		t.Fatal("wasm emitter produced 0 bytes")
	}
	watPath := filepath.Join(dir, "rf.wat")
	if err := os.WriteFile(watPath, wat, 0o644); err != nil {
		t.Fatalf("write wat: %v", err)
	}
	err = exec.Command(wasmtime, "run", "--dir", dir+"::/", watPath).Run()
	code := 0
	if err != nil {
		ee, ok := err.(*exec.ExitError)
		if !ok {
			t.Fatalf("wasmtime run failed (not an exit code): %v", err)
		}
		code = ee.ExitCode()
	}
	if code != 0 {
		t.Fatalf("read_file large round-trip failed at check %d (1=len truncated, 2/3=byte past 64KiB wrong, 9=Err)", code)
	}
}
