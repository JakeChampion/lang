package e2ecompiler

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestSelfHostWasmCacheServesNonUTF8Literal pins that a wasm unit object whose
// string table holds a literal that is not UTF-8 is served from the cache. The
// object stores literals verbatim, and the cache read it back with read_file,
// which refuses invalid UTF-8, so the unit read as a miss every time: the
// whole-compiler sharded link failed with "no cached object" once the lexer
// carried a `\xff` test literal.
func TestSelfHostWasmCacheServesNonUTF8Literal(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/wasm_modload_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/wasm_modload_run.fern", "wasm_modload_run")

	proj := t.TempDir()
	cacheDir := filepath.Join(proj, "cache")
	if err := os.Mkdir(cacheDir, 0o755); err != nil {
		t.Fatal(err)
	}
	entry := filepath.Join(proj, "entry.fern")
	if err := os.WriteFile(entry, []byte("function main(): i32 {\n  let s: string = \"a\\xffb\";\n  return s.len();\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	emit := func() string {
		t.Helper()
		args := []string{entry, "-per-module-emit", "0", "-cache-dir", cacheDir}
		var cmd *exec.Cmd
		if len(runner) == 0 {
			cmd = exec.Command(driverBin, args...)
		} else {
			cmd = exec.Command(runner[0], append(append(append([]string{}, runner[1:]...), driverBin), args...)...)
		}
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		if err := cmd.Run(); err != nil {
			t.Fatalf("emit failed: %v\n%s", err, stderr.String())
		}
		return stderr.String()
	}
	if first := emit(); !strings.Contains(first, "cache-miss") {
		t.Fatalf("a cold cache did not miss:\n%s", first)
	}
	if second := emit(); !strings.Contains(second, "cache-hit") {
		t.Errorf("the unit just written was not served from the cache:\n%s", second)
	}
}
