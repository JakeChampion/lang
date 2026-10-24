package e2ecompiler

import (
	"os/exec"
	"testing"
)

func TestSelfHostWasmLocals(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("wasm_locals_run driver runs natively; skipping under an exec runner")
	}
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/wasm_locals_run.fern")
	bin := buildSelfHostBin(t, gcc, dir, "drivers/wasm_locals_run.fern", "wasm_locals_run")

	const want = "" +
		"straight_line: const_i32 1 ; store_local 1 ; load_local 1 ; store_local 0 ; const_i32 2 ; store_local 1 ; load_local 1 ; store_local 0 ; const_i32 3 ; store_local 1 ; load_local 1 ; load_local 0 ; add ; return | n_locals=2 i64=[] f64=[]\n" +
		"read_before_store: const_i32 1 ; store_local 1 ; load_local 1 ; drop ; load_local 0 ; const_i32 5 ; add ; store_local 0 ; load_local 0 ; return | n_locals=2 i64=[] f64=[]\n" +
		"store_in_if_read_after: const_i32 1 ; store_local 1 ; load_local 1 ; if ; const_i32 7 ; store_local 0 ; end ; load_local 0 ; return | n_locals=2 i64=[] f64=[]\n" +
		"live_around_back_edge: const_i32 4 ; store_local 0 ; block ; loop ; load_local 0 ; const_i32 1 ; add ; store_local 1 ; load_local 1 ; drop ; const_i32 0 ; brif 0 ; end ; end ; const_i32 9 ; store_local 0 ; load_local 0 ; return | n_locals=2 i64=[] f64=[]\n" +
		"stored_each_iteration: block ; loop ; const_i32 1 ; store_local 0 ; load_local 0 ; drop ; const_i32 2 ; store_local 0 ; load_local 0 ; brif 0 ; end ; end ; const_i32 0 ; return | n_locals=1 i64=[] f64=[]\n" +
		"types_apart: const_i32 1 ; store_local 0 ; load_local 0 ; drop ; const_i32 2 ; store_local 1 ; load_local 1 ; drop ; const_i32 3 ; store_local 2 ; load_local 2 ; drop ; const_i32 4 ; store_local 0 ; load_local 0 ; return | n_locals=3 i64=[1] f64=[2]\n" +
		"dyn_dispatch_run: const_i32 1 ; store_local 3 ; load_local 3 ; store_local 0 ; const_i32 5 ; store_local 1 ; const_i32 6 ; store_local 2 ; dyn_dispatch from 1 ; store_local 3 ; load_local 3 ; return | n_locals=4 i64=[] f64=[]\n" + "\n"

	cmd := exec.Command(bin)
	out, _ := cmd.Output()
	if cmd.ProcessState == nil || cmd.ProcessState.ExitCode() != 0 {
		t.Fatalf("wasm_locals_run did not exit 0")
	}
	if got := string(out); got != want {
		t.Errorf("coalescing report mismatch:\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}
