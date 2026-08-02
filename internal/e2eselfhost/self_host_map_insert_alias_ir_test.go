package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// mapInsertAliasIRCases exercise the self-reassign `m = m.insert(k, v)` through
// the self-host IR path when the map `m` has a lasting LOCAL alias (#3633 — the
// map sibling of the array `.with` fix #3599).
//
// The builtin op_map_set mutates the map's parallel keys[]/values[] in place,
// which is unsound once `m` is aliased (`var n = m`): the in-place write mutates
// the buffer `n` still references, so `n` observes the change. The interpreter
// and the native (Perceus) backend both copy-on-write and leave `n` unchanged.
// The fix detects the alias at lower_func time (aliased_array_names_of, shared
// with #3599) and routes the aliased self-reassign through a map clone
// (lower_map_clone_insert: fresh map_new + a copy loop over keys()/values(),
// mutate the sole-owned clone) instead of the in-place store. The unaliased
// "no-alias" case still takes the in-place path.
//
// Programs use the `Map {}` literal + bare map builtins (no `import "core/map"`)
// like the other self-host map IR tests; `want` values are verified against the
// native x86-64 backend and the interpreter. Each is pinned to the "ir" path.
var mapInsertAliasIRCases = []struct {
	name string
	main string
	want int
}{
	// The minimal repro: overwrite an existing key while an alias is live. The
	// in-place mutation made n[1]==99 too (99+99=198); copy-on-write keeps n[1]==10.
	{"overwrite", `var m: Map[i32, i32] = Map {}; m = m.insert(1, 10); var n = m; m = m.insert(1, 99); return m.get_or(1, 0) + n.get_or(1, 0);`, 109},
	// Insert a NEW key while an alias is live: n must not gain key 2.
	{"new-key", `var m: Map[i32, i32] = Map {}; m = m.insert(1, 10); var n = m; m = m.insert(2, 20); return m.get_or(2, 0) + n.get_or(2, 0);`, 20},
	// String-keyed map: the clone copies string-pointer key/value slots correctly.
	{"string-key", `var m: Map[string, i32] = Map {}; m = m.insert("a", 5); var n = m; m = m.insert("a", 9); return m.get_or("a", 0) + n.get_or("a", 0);`, 14},
	// REGRESSION: no alias — the in-place fast path must still apply and be correct.
	{"no-alias", `var m: Map[i32, i32] = Map {}; m = m.insert(1, 10); m = m.insert(1, 99); return m.get_or(1, 0);`, 99},
}

func mapInsertAliasIRSrc(mainBody string) string {
	return "function main(): i32 { " + mainBody + " }\n"
}

// TestSelfHostMapInsertAliasIRX86_64 routes each case through the self-hosted
// x86-64 IR driver, pinned to the "ir" path.
func TestSelfHostMapInsertAliasIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "asm_run.fern", "asm_pathprobe_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")
	probeBin := buildSelfHostBin(t, gcc, dir, "asm_pathprobe_run.fern", "pathprobe")

	for _, tc := range mapInsertAliasIRCases {
		t.Run(tc.name, func(t *testing.T) {
			src := []byte(mapInsertAliasIRSrc(tc.main))
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
			if code := cmd.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}

// TestSelfHostMapInsertAliasIRWasm runs the same cases through the wasm IR backend.
func TestSelfHostMapInsertAliasIRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host map-insert-alias wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	for _, name := range []string{
		"util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern",
		"ir.fern", "irlower.fern", "asm_ir.fern", "wasm_ir.fern", "wasm_ir_run.fern",
	} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")

	for _, tc := range mapInsertAliasIRCases {
		t.Run(tc.name, func(t *testing.T) {
			src := []byte(mapInsertAliasIRSrc(tc.main))
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
			watFile := filepath.Join(dir, "mapinsertalias_prog.wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			run := exec.Command("wasmtime", "run", watFile)
			_ = run.Run()
			if run.ProcessState == nil || !run.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.name, wat)
			}
			if code := run.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("map-insert-alias wasm IR %q = %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}
