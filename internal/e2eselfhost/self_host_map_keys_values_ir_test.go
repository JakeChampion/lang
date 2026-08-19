package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// mapKeysValuesIRCases pin an UNANNOTATED `var x = m.keys()` / `m.values()`
// binding consumed by a `for … in x` loop (or `.len()`) to the self-host IR path
// on x86-64 + wasm. The annotated form (`var x: i32[] = m.keys()`) already routed
// IR — the array-local foreach machinery is complete — but the unannotated var
// binding never marked the slot's array-ness / element type from the map's K/V,
// so it bailed the whole module to the legacy AST emitter. #2691 teaches the
// unannotated-var array inference (and the expr_is_arr_src / expr_is_strarr
// predicates) to recognize map `.keys()`/`.values()` via one map_kv_elem_tag
// gate. Scope: i32/string keys and i32/string values — the 8-byte i64/f64 value
// cases hit a SEPARATE pre-existing op_map_values wasm bug (the annotated form
// mis-sums on wasm too) and stay on AST. Each case is oracle-checked against the
// interpreter; all need `import "core/map";`.
var mapKeysValuesIRCases = []struct {
	name string
	main string
}{
	// i32-keyed map: sum the keys. 10 + 20 = 30.
	{"keys-i32", `import "core/map"; function main(): i32 { var m: Map[i32, i32] = map_new(8); m = m.insert(10, 1); m = m.insert(20, 1); var ks = m.keys(); var s: i32 = 0; for k in ks { s = s + k; } return s; }`},
	// i32 values: sum them. 4 + 6 = 10.
	{"vals-i32", `import "core/map"; function main(): i32 { var m: Map[string, i32] = map_new(8); m = m.insert("a", 4); m = m.insert("b", 6); var vs = m.values(); var s: i32 = 0; for v in vs { s = s + v; } return s; }`},
	// string keys: sum their lengths. len("ab")+len("c") = 3.
	{"keys-str", `import "core/map"; function main(): i32 { var m: Map[string, i32] = map_new(8); m = m.insert("ab", 1); m = m.insert("c", 1); var ks = m.keys(); var s: i32 = 0; for k in ks { s = s + k.len(); } return s; }`},
	// string values: sum their lengths. len("ab")+len("c") = 3.
	{"vals-str", `import "core/map"; function main(): i32 { var m: Map[i32, string] = map_new(8); m = m.insert(1, "ab"); m = m.insert(2, "c"); var vs = m.values(); var s: i32 = 0; for v in vs { s = s + v.len(); } return s; }`},
	// keys() result used by .len() (no foreach). 2 entries.
	{"keys-len", `import "core/map"; function main(): i32 { var m: Map[i32, i32] = map_new(8); m = m.insert(5, 1); m = m.insert(6, 1); var ks = m.keys(); return ks.len(); }`},
	// Regression: the ANNOTATED form was already on the IR path. 42.
	{"annot-i32", `import "core/map"; function main(): i32 { var m: Map[string, i32] = map_new(8); m = m.insert("a", 42); var vs: i32[] = m.values(); var s: i32 = 0; for v in vs { s = s + v; } return s; }`},
}

// TestSelfHostMapKeysValuesIRX86_64 routes each case through the self-hosted
// x86-64 IR driver, oracle-checked, with routing pinned to "ir".
func TestSelfHostMapKeysValuesIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "asm_run.fern", "asm_pathprobe_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")
	probeBin := buildSelfHostBin(t, gcc, dir, "asm_pathprobe_run.fern", "pathprobe")

	for _, tc := range mapKeysValuesIRCases {
		t.Run(tc.name, func(t *testing.T) {
			src := []byte(tc.main + "\n")
			want := interpExit(t, interpBin, string(src))
			path := strings.TrimSpace(string(runCapture(t, gcc, runner, probeBin, src)))
			if path != "ir" {
				t.Fatalf("%s routed through %q path, want \"ir\"", tc.name, path)
			}
			asm := runCapture(t, gcc, runner, driverBin, src)
			if len(asm) == 0 {
				t.Fatal("self-host compiler emitted 0 bytes")
			}
			progBin := buildBin(t, gcc, dir, tc.name, string(asm))
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(progBin)
			} else {
				cmd = exec.Command(runner[0], append(runner[1:], progBin)...)
			}
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != want {
				t.Errorf("%s exited %d, want %d (interp oracle)", tc.name, code, want)
			}
		})
	}
}

// TestSelfHostMapKeysValuesIRWasm runs the same cases through the wasm IR backend.
func TestSelfHostMapKeysValuesIRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host map-keys-values wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")

	for _, tc := range mapKeysValuesIRCases {
		t.Run(tc.name, func(t *testing.T) {
			src := []byte(tc.main + "\n")
			want := interpExit(t, interpBin, string(src))
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(driverBin, "-ir")
			} else {
				cmd = exec.Command(runner[0], append(append(append([]string{}, runner[1:]...), driverBin), "-ir")...)
			}
			cmd.Stdin = bytes.NewReader(src)
			wat, err := cmd.Output()
			if err != nil || len(wat) == 0 {
				t.Fatalf("driver failed for %q: %v", tc.name, err)
			}
			watFile := filepath.Join(dir, "map_keys_values_prog.wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			run := exec.Command("wasmtime", "run", watFile)
			_ = run.Run()
			if run.ProcessState == nil || !run.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.name, wat)
			}
			if code := run.ProcessState.ExitCode(); code != want {
				t.Errorf("map-keys-values wasm IR %q = %d, want %d (interp oracle)", tc.name, code, want)
			}
		})
	}
}
