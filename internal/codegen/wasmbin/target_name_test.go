package wasmbin

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// A `target_os()` / `target_arch()` the front end did not fold is answered
// by this backend's own target, wasm32-wasi.
func TestUnfoldedTargetCallsNameThisTarget(t *testing.T) {
	bin, err := buildFromSource(t, `function main(): i32 {
    print(target_arch());
    print(target_os());
    return 0;
}`)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH")
	}
	p := filepath.Join(t.TempDir(), "prog.wasm")
	if err := os.WriteFile(p, bin, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	cmd := exec.Command("wasmtime", "run", "--invoke", "main", p)
	var so, se bytes.Buffer
	cmd.Stdout = &so
	cmd.Stderr = &se
	if err := cmd.Run(); err != nil {
		t.Fatalf("wasmtime: %v\nstderr:%s", err, se.String())
	}
	if got, want := so.String(), "wasm32\nwasi\n0\n"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
}
