package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// Cell primitives on the self-host WASM **IR** driver (#5510). The existing
// Cell coverage (`TestSelfHostWasmRun`'s cell-* cases) drives wasm_run.fern,
// the AST driver; this pins the wasm_ir_run.fern path, which is the one #5510
// is about.
//
// irlower lowers a Cell as a one-element array — `cell_new(v)` → `[v]`,
// `c.get()` → `c[0]`, `c.set(x)` → `c[0] = x` — so it rides the array
// machinery every backend already has rather than needing three dedicated
// wasm ops. These cases exist to pin that, since the desugar is the only thing
// standing between the wasm IR path and a `$cell_new` link failure.
var cellWasmIRCases = []struct {
	name string
	src  string
	want int
}{
	{"get", `function main(): i32 { var c: Cell[i32] = cell_new(7); return c.get(); }`, 7},
	{"set", `function main(): i32 { var c: Cell[i32] = cell_new(1); c.set(9); return c.get(); }`, 9},
	{"read-modify-write", `function main(): i32 { var c: Cell[i32] = cell_new(0); c.set(c.get() + 5); c.set(c.get() * 2); return c.get(); }`, 10},
	// A Cell passed to a function is shared, not copied — the mutation is
	// visible to the caller. This is the whole point of the type.
	{"shared-through-call", `function bump(c: Cell[i32]): void { c.set(c.get() + 1); } function main(): i32 { var c: Cell[i32] = cell_new(10); bump(c); bump(c); bump(c); return c.get(); }`, 13},
	// A Cell living in a struct field: the receiver is a field access rather
	// than a bare ident.
	{"struct-field", `struct Box { c: Cell[i32] } function main(): i32 { var b: Box = Box { c: cell_new(5) }; b.c.set(b.c.get() + 1); return b.c.get(); }`, 6},
}

// TestSelfHostCellWasmIR runs each case through the self-hosted wasm IR driver
// and checks it against the native backend as the oracle.
func TestSelfHostCellWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host Cell wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")

	for _, tc := range cellWasmIRCases {
		t.Run(tc.name, func(t *testing.T) {
			// Native cross-check first: if the oracle disagrees the case is
			// wrong, not the backend.
			if _, code := compileAndRunX86_64(t, tc.src+"\n"); code != tc.want {
				t.Fatalf("native x86-64 exited %d, want %d", code, tc.want)
			}
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(driverBin, "-ir")
			} else {
				cmd = exec.Command(runner[0], append(append(append([]string{}, runner[1:]...), driverBin), "-ir")...)
			}
			cmd.Stdin = bytes.NewReader([]byte(tc.src + "\n"))
			wat, err := cmd.Output()
			if err != nil || len(wat) == 0 {
				t.Fatalf("wasm IR driver failed for %q: %v", tc.src, err)
			}
			watFile := filepath.Join(dir, "cell_"+tc.name+".wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			run := exec.Command("wasmtime", "run", watFile)
			_ = run.Run()
			if run.ProcessState == nil || !run.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.src, wat)
			}
			if code := run.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("Cell wasm IR %q = %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}
