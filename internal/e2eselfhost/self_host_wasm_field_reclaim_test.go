package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestSelfHostFieldReclaimWasm — the wasm Perceus slice 1d: the per-type
// $__field_reclaim_<T> body (and the $__fern_snapshot_dec it calls) now REALLY
// reclaim a consume-rebind's superseded boxes, instead of leak-safe
// PASS-THROUGHs that returned `new` and freed nothing. The wasm sibling of the
// register backends' emit_ir_field_reclaim_one + __fern_snapshot_dec and of
// arm64's emit_arm64_field_reclaim_one (slice 1b).
//
// When a struct PARAM is reassigned (`a = T { … }`), the superseded `old`
// struct's rc-array field buffers are freed (cow-guarded ≠ new, snapshot-guarded
// ≠ the caller's borrowed original `snap`) and then `old`'s box itself via
// $__fern_snapshot_dec — which frees only the UNIQUELY-owned case (rc==1), never
// decrementing a shared/borrowed box. The field offset is the IR struct layout
// (8 + i*8), as in struct_drop (slice 1c).
//
// As in slice 1c, the RC-introspection builtins force a module onto the wasm AST
// path, so an IR-routed reclaim test can't use them. Reclaim is proven by a
// memory-pressure differential: a single call whose snapshot param is loop-rebound
// 2M times reclaims every intermediate (memory stays ~bounded under a tight cap)
// with the real bodies, and leaks one box+buffer per rebind — blowing any
// reasonable cap — with the pass-through. The WAT-shape assertion pins the emitted
// real body so a silent reroute to the AST path can't make the gate pass vacuously.
func TestSelfHostFieldReclaimWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping wasm field-reclaim e2e")
	}
	gcc, runner := x86_64Tooling(t)

	dir := t.TempDir()
	copySelfHostDriver(t, dir, "wasm_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_run.fern", "wasm_run")

	// The reclaim churn: one build() call whose snapshot param is rebound 2M
	// times. Bounded reclaim footprint is ~1 MiB; the pass-through leak is one
	// box+buffer per rebind (~2M × ~80 B ≈ 160 MiB), so a regression traps on
	// memory.grow well under the cap.
	const reclaimSrc = "struct Acc { items: i32[] } " +
		"function build(a: Acc, n: i32): Acc { var i: i32 = 0; while (i < n) { a = Acc { items: [i, i, i, i] }; i = i + 1; } return a; } " +
		"function main(): i32 { var seed: Acc = Acc { items: [0] }; var r: Acc = build(seed, 2000000); return r.items[0] - 1999999; }"
	const cap = "16777216" // 16 MiB — ~16× the bounded footprint, ~1/10 the leak

	// The real $__field_reclaim_Acc body opens with the old-is-heap guard then the
	// cow check on field 0 at the IR offset 8 — the pass-through is just
	// `(local.get $new))`. Pinning this guarantees IR routing + the real shape.
	const wantBody = "(func $__field_reclaim_Acc (param $new i32) (param $old i32) (param $snap i32) (result i32)\n" +
		"    (if (i32.ge_u (local.get $old)"

	cases := []struct {
		name string
		src  string
		// run under the memory cap (reclaim differential)?
		capped bool
		// expected exit code
		exit int
	}{
		// RECLAIM: 2M consume-rebind intermediates stay bounded under the cap ⇒
		// field_reclaim + snapshot_dec free each superseded box+buffer.
		{"consume-rebind-reclaim", reclaimSrc, true, 0},
		// SNAPSHOT GUARD: the caller's original `seed` must survive the param
		// rebinds (snapshot_dec never frees `old == snap`; field_reclaim never decs
		// `old.field == snap.field`). Reads seed AFTER the rebind-heavy call: its
		// values must be intact, AND the rebound result must be the last
		// intermediate. (sum 50 - 50) + (4999 - 4999) == 0.
		{"snapshot-guard-caller-intact",
			"struct Acc { items: i32[] } " +
				"function build(a: Acc, n: i32): Acc { var i: i32 = 0; while (i < n) { a = Acc { items: [i, i, i] }; i = i + 1; } return a; } " +
				"function main(): i32 { var seed: Acc = Acc { items: [42, 7, 1] }; var r: Acc = build(seed, 5000); return (seed.items[0] + seed.items[1] + seed.items[2] - 50) + (r.items[0] - 4999); }",
			false, 0},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			wat := runCapture(t, gcc, runner, driverBin, []byte(tc.src))
			if len(wat) == 0 {
				t.Fatal("wasm emitter produced 0 bytes")
			}
			if !strings.Contains(string(wat), wantBody) {
				t.Fatalf("%s: emitted $__field_reclaim body missing the real shape\nwant substring:\n%s\n--- WAT ---\n%s", tc.name, wantBody, wat)
			}
			watPath := filepath.Join(dir, tc.name+".wat")
			if err := os.WriteFile(watPath, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			args := []string{"run"}
			if tc.capped {
				args = append(args, "-W", "max-memory-size="+cap, "-W", "trap-on-grow-failure=y")
			}
			args = append(args, "--dir", dir, watPath)
			cmd := exec.Command("wasmtime", args...)
			_, _ = cmd.Output()
			if code := cmd.ProcessState.ExitCode(); code != tc.exit {
				detail := ""
				if tc.capped {
					detail = " (a trap means the consume-rebind intermediates leaked past the " + cap + "-byte cap — field_reclaim/snapshot_dec did not reclaim)"
				}
				t.Errorf("%s: wasm exited %d, want %d%s\n--- WAT ---\n%s", tc.name, code, tc.exit, detail, wat)
			}
		})
	}
}
