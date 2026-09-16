package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// `-backend flat` names the stack-machine emitter rather than describing it.
// Without a name for it, a comparison against "the shipping backend" has to
// omit the flag and take whatever the default is — which is how every SSA
// differential is written, and why the default-backend flip would leave them
// comparing the SSA emitter against itself while still reporting a comparison
// (#4112).
//
// The name has to select the same emitter the default does, so the test is
// that the two builds are byte-identical.
func TestBackendFlatIsTheDefaultEmitter(t *testing.T) {
	for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
		t.Run(target, func(t *testing.T) {
			dir := t.TempDir()
			src := filepath.Join(dir, "p.fern")
			const prog = `function fib(n: i32): i32 { if (n < 2) { return n; } return fib(n - 1) + fib(n - 2); }
function main(): i32 { return fib(9); }`
			if err := os.WriteFile(src, []byte(prog), 0o644); err != nil {
				t.Fatal(err)
			}
			build := func(backend, name string) []byte {
				t.Helper()
				out := filepath.Join(dir, name)
				if code, err := run(src, out, target, backend, "", "", false, true, "", false, false, false, nil, false, "", false, nil); err != nil || code != 0 {
					t.Fatalf("build with -backend %q: code=%d err=%v", backend, code, err)
				}
				b, err := os.ReadFile(out)
				if err != nil {
					t.Fatal(err)
				}
				return b
			}
			if dflt, flat := build("", "dflt"), build("flat", "flat"); !bytes.Equal(dflt, flat) {
				t.Errorf("`-backend flat` produced %d bytes and the default %d: it is supposed to name the same emitter, not a second one", len(flat), len(dflt))
			}
		})
	}
}

// A name nothing implements is an error, not a silent fallback to the default.
func TestUnknownBackendIsRefused(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "p.fern")
	if err := os.WriteFile(src, []byte("function main(): i32 { return 0; }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	code, err := run(src, filepath.Join(dir, "p"), "x86-64-linux", "stack", "", "", false, true, "", false, false, false, nil, false, "", false, nil)
	if err == nil || code == 0 {
		t.Fatalf("-backend stack built something instead of being refused: code=%d err=%v", code, err)
	}
}
