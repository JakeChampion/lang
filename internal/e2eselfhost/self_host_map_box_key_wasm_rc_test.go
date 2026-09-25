package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The wasm leg of #9966. wasm does not take the MAPKA: credit — its key column
// is rc-managed per insert, like its value column — so the change there is two
// gates widening from `kis == 1` (a string key) to `kis != 0` (any counted key,
// so a struct or enum box too): the construction-inc in $__fern_map_set, and
// the per-slot key release in $__fern_map_release.
//
// They have to widen TOGETHER, and they sit in different regions of
// wasm_ir.fern, so each direction of a one-sided widening needs an assert:
//
//   - inc alone (release still string-only) leaks a key box per map, which the
//     heap bump across 1000 build-and-drop rounds reads as growth → exit 1;
//   - release alone (insert still string-only) decs a borrowed key nothing
//     inc'd, which __rc_underflow_count() reads as non-zero → exit 99.
//
// Both were measured by reverting each gate in turn against this program.
//
// A BORROWED key is what discriminates, and is why one case covers both: the
// existing struct-key wasm cases construct every key inline, single-run, with
// no heap readout, and would pass under either half alone. Here the key is a
// local the map outlives and which is read back after the insert, so the map
// owes it an inc and the release owes it the matching dec.
func TestSelfHostMapBoxKeyWasmRC(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host box-key map wasm rc e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")

	cases := []struct {
		name string
		src  string
	}{
		// A BORROWED struct key: the local outlives the map and is read after
		// the insert, so the map owes the key an inc and the release owes it a
		// dec. Balanced = flat heap and a zero underflow counter.
		{"key-borrowed",
			`import "core/cmp"; @derive(cmp.Eq, cmp.Hash) struct P { x: i32, y: i32 } function build(n: i32): i32 { var k: P = P { x: n, y: n * 2 }; var m: Map[P, i32] = map_new(4); m = m.insert(k, 7); var got: i32 = m.get_or(k, 0); return got; } function main(): i32 { var acc: i32 = 0; var w: i32 = 0; while (w < 100) { acc = acc + build(w); w = w + 1; } var s1: i32 = (__heap_bump_bytes() as i32); var j: i32 = 0; while (j < 1000) { acc = acc + build(j); j = j + 1; } var s2: i32 = (__heap_bump_bytes() as i32); if (__rc_underflow_count() != 0) { return 99; } if ((s2 - s1) > 4096) { return 1; } if (acc != 7700) { return 88; } return 0; }`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(driverBin, "-ir")
			} else {
				cmd = exec.Command(runner[0], append(append(append([]string{}, runner[1:]...), driverBin), "-ir")...)
			}
			cmd.Stdin = bytes.NewReader([]byte(tc.src))
			wat, err := cmd.Output()
			if err != nil || len(wat) == 0 {
				t.Fatalf("driver failed for %q: %v", tc.name, err)
			}
			if !strings.Contains(string(wat), "$__fern_map_new_struct") {
				t.Fatalf("%q did not reach the struct-key IR path (no $__fern_map_new_struct in WAT)", tc.name)
			}
			watFile := filepath.Join(dir, "boxkey_rc_"+tc.name+".wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			rcmd := exec.Command("wasmtime", "run", watFile)
			_ = rcmd.Run()
			if rcmd.ProcessState == nil || !rcmd.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q", tc.name)
			}
			if got := rcmd.ProcessState.ExitCode(); got != 0 {
				t.Fatalf("%s exited %d, want 0 "+
					"(1 = the key column leaks, an inc with no matching release; "+
					"99 = over-release, a release with no matching inc; "+
					"88 = a key read back wrong)", tc.name, got)
			}
		})
	}
}
