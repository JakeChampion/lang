package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// A FRESHLY CONSTRUCTED map argument strands its box on every insert, in both
// columns (#9984).
//
// op_map_set's width word carries two bits that tell $__fern_map_set to TAKE an
// argument's single ref instead of retaining it: vconsume for the value,
// kconsume for the key. Each asked a narrower question than it meant — the key
// asked only whether the argument was a fresh STRING, so no constructed box
// ever set it; the value knew struct and array literals but not a variant
// construction. Either way the retain went unmatched: rc 1 → 2 on insert, one
// dec at release, and nothing else naming the box.
//
// A BORROWED argument is even, because the source local's own sweep supplies
// the second dec — which is why TestSelfHostMapBoxKeyWasmRC passes and why the
// existing struct fixtures, all inline constructions with no heap readout,
// never showed it. Freshness is the whole discriminator, so every case here
// constructs its argument in the insert and reads the heap across 1000 rounds.
func TestSelfHostMapFreshBoxArgWasmRC(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host fresh box-arg map wasm rc e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")

	const probe = `function main(): i32 { var acc: i32 = 0; var w: i32 = 0; while (w < 100) { acc = acc + build(w); w = w + 1; } ` +
		`var s1: i32 = (__heap_bump_bytes() as i32); var j: i32 = 0; while (j < 1000) { acc = acc + build(j); j = j + 1; } ` +
		`var s2: i32 = (__heap_bump_bytes() as i32); if (__rc_underflow_count() != 0) { return 99; } if ((s2 - s1) > 4096) { return 1; } ` +
		`if (acc != 1100) { return 88; } return 0; }`

	cases := []struct {
		name string
		src  string
	}{
		// A struct literal in the insert: map_arg_binding_is_fresh's "b" kind
		// answers for it, is_fresh_str_temp does not.
		{"struct-literal-key",
			`import "core/cmp"; @derive(cmp.Eq, cmp.Hash) struct P { x: i32, y: i32 } ` +
				`function build(n: i32): i32 { var m: Map[P, i32] = map_new(4); m = m.insert(P { x: n, y: n * 2 }, 7); return m.len(); } ` + probe},
		// A variant construction is a fresh box exactly as a struct literal is,
		// and reads as an ordinary call — so it exercises the other arm of the
		// freshness test.
		{"variant-key",
			`import "core/cmp"; @derive(cmp.Eq, cmp.Hash) enum Tag { Lo(i32), Hi(i32) } ` +
				`function build(n: i32): i32 { var m: Map[Tag, i32] = map_new(4); m = m.insert(Tag.Lo(n), 7); return m.len(); } ` + probe},
		// The VALUE column asks the same question, and knew struct literals but
		// not variant constructions: this one leaked where its struct twin
		// below was already flat, which is what says the two columns want one
		// predicate rather than two that drift.
		{"variant-value",
			`import "core/cmp"; @derive(cmp.Eq, cmp.Hash) enum Tag { Lo(i32), Hi(i32) } ` +
				`function build(n: i32): i32 { var m: Map[i32, Tag] = map_new(4); m = m.insert(n, Tag.Lo(n)); return m.len(); } ` + probe},
		{"struct-value",
			`import "core/cmp"; @derive(cmp.Eq, cmp.Hash) struct P { x: i32, y: i32 } ` +
				`function build(n: i32): i32 { var m: Map[i32, P] = map_new(4); m = m.insert(n, P { x: n, y: n * 2 }); return m.len(); } ` + probe},
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
			if strings.HasSuffix(tc.name, "-key") && !strings.Contains(string(wat), "$__fern_map_new_struct") {
				t.Fatalf("%q did not reach the struct-key IR path (no $__fern_map_new_struct in WAT)", tc.name)
			}
			watFile := filepath.Join(dir, "fresharg_rc_"+tc.name+".wat")
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
					"(1 = the fresh box leaks, a retain with no matching release; "+
					"99 = over-release, a dec with no matching inc; "+
					"88 = a key read back wrong)", tc.name, got)
			}
		})
	}
}
