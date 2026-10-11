package e2ecompiler

import (
	"os/exec"
	"testing"
)

// TestSelfHostWasmLocals pins the wasm backend's local coalescing
// (compiler/wasmlocals.fern, #12078) through the wasm_locals_run driver, which
// builds an op list per case and prints the coalesced ops, the local count and
// the i64 and f64 locals. The refusals are the cases that matter: each is a
// slot whose value a sharing local would overwrite.
func TestSelfHostWasmLocals(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("wasm_locals_run driver runs natively; skipping under an exec runner")
	}
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/wasm_locals_run.fern")
	bin := buildSelfHostBin(t, gcc, dir, "drivers/wasm_locals_run.fern", "wasm_locals_run")

	const want = "" +
		// Three statement temporaries share one local after the param; the
		// slot nothing names takes none.
		"straight_line: const_i32 1 ; store_local 1 ; load_local 1 ; store_local 0 ; const_i32 2 ; store_local 1 ; load_local 1 ; store_local 0 ; const_i32 3 ; store_local 1 ; load_local 1 ; load_local 0 ; add ; return | n_locals=2 i64=[] f64=[]\n" +
		// Slot 2 reads wasm's zero before its store: a local of its own (0),
		// apart from the one slot 1 is done with (1).
		"read_before_store: const_i32 1 ; store_local 1 ; load_local 1 ; drop ; load_local 0 ; const_i32 5 ; add ; store_local 0 ; load_local 0 ; return | n_locals=2 i64=[] f64=[]\n" +
		// Stored only inside the if and read after it: apart.
		"store_in_if_read_after: const_i32 1 ; store_local 1 ; load_local 1 ; if ; const_i32 7 ; store_local 0 ; end ; load_local 0 ; return | n_locals=2 i64=[] f64=[]\n" +
		// Slot 1 lives around the back edge, so the body's temporary takes a
		// second local; the slot after the loop reuses the first.
		"live_around_back_edge: const_i32 4 ; store_local 0 ; block ; loop ; load_local 0 ; const_i32 1 ; add ; store_local 1 ; load_local 1 ; drop ; const_i32 0 ; brif 0 ; end ; end ; const_i32 9 ; store_local 0 ; load_local 0 ; return | n_locals=2 i64=[] f64=[]\n" +
		// Stored at the top of each iteration: shares like straight-line code.
		"stored_each_iteration: block ; loop ; const_i32 1 ; store_local 0 ; load_local 0 ; drop ; const_i32 2 ; store_local 0 ; load_local 0 ; brif 0 ; end ; end ; const_i32 0 ; return | n_locals=1 i64=[] f64=[]\n" +
		// i32, i64, f64, then i32 again, which reuses the first local.
		"types_apart: const_i32 1 ; store_local 0 ; load_local 0 ; drop ; const_i32 2 ; store_local 1 ; load_local 1 ; drop ; const_i32 3 ; store_local 2 ; load_local 2 ; drop ; const_i32 4 ; store_local 0 ; load_local 0 ; return | n_locals=3 i64=[1] f64=[2]\n" +
		// dyn_dispatch's run stays consecutive (1, 2) and its base follows it;
		// the temporaries share local 3.
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
