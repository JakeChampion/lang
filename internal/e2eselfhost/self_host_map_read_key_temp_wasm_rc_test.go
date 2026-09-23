package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// A map READ takes its key, probes with it, and releases nothing (#10071).
//
// That is right for a BORROWED key — the source local's own sweep owns it —
// but a freshly constructed key has no other owner, so every call strands it.
// The probe helpers ($__fern_map_find / _get / _has / _get_or) touch no
// refcount by design; the release has to come from the lowering, and only
// `get` emitted one, for strings alone.
//
// Each arm is paired with the same program keyed by a surviving local. The
// control is what makes a failure mean "the fresh key leaked" rather than
// "this shape leaks" — an Option box or a get_or default temp would show in
// both.
//
// Only BOX keys are asserted. Two neighbouring shapes leak with a borrowed key
// too, so a fresh-key case over them would be measuring someone else's bug:
// `without` (#9970, a deleted entry's key and value are never released) and
// every string-keyed program (whatever owns that column's keys). They are left
// out rather than asserted loosely — a case whose control is red cannot say
// what made it red.
func TestSelfHostMapReadKeyTempWasmRC(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host map read-key rc e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")

	const probe = `function main(): i32 { var acc: i32 = 0; var w: i32 = 0; while (w < 100) { acc = acc + build(w); w = w + 1; } ` +
		`var s1: i32 = (__heap_bump_bytes() as i32); var j: i32 = 0; while (j < 1000) { acc = acc + build(j); j = j + 1; } ` +
		`var s2: i32 = (__heap_bump_bytes() as i32); if (__rc_underflow() != 0) { return 99; } if ((s2 - s1) > 4096) { return 1; } return 0; }`
	const box = `import "core/cmp"; @derive(cmp.Eq, cmp.Hash) struct P { x: i32, y: i32 } `

	cases := []struct {
		name string
		src  string
	}{
		{"box-has-fresh", box + `function build(n: i32): i32 { var m: Map[P, i32] = map_new(4); var k: P = P { x: n, y: n * 2 }; m = m.insert(k, 7); if (m.has(P { x: n, y: n * 2 })) { return 0; } return 0; } ` + probe},
		{"box-has-borrowed", box + `function build(n: i32): i32 { var m: Map[P, i32] = map_new(4); var k: P = P { x: n, y: n * 2 }; m = m.insert(k, 7); if (m.has(k)) { return 0; } return 0; } ` + probe},
		{"box-get_or-fresh", box + `function build(n: i32): i32 { var m: Map[P, i32] = map_new(4); var k: P = P { x: n, y: n * 2 }; m = m.insert(k, 7); return m.get_or(P { x: n, y: n * 2 }, 0) - 7; } ` + probe},
		{"box-get_or-borrowed", box + `function build(n: i32): i32 { var m: Map[P, i32] = map_new(4); var k: P = P { x: n, y: n * 2 }; m = m.insert(k, 7); return m.get_or(k, 0) - 7; } ` + probe},
		// A variant key reaches the same arms through the other half of the
		// freshness predicate.
		{"variant-get_or-fresh", `import "core/cmp"; @derive(cmp.Eq, cmp.Hash) enum Tag { Lo(i32), Hi(i32) } ` +
			`function build(n: i32): i32 { var m: Map[Tag, i32] = map_new(4); var k: Tag = Tag.Lo(n); m = m.insert(k, 7); return m.get_or(Tag.Lo(n), 0) - 7; } ` + probe},
		{"variant-get_or-borrowed", `import "core/cmp"; @derive(cmp.Eq, cmp.Hash) enum Tag { Lo(i32), Hi(i32) } ` +
			`function build(n: i32): i32 { var m: Map[Tag, i32] = map_new(4); var k: Tag = Tag.Lo(n); m = m.insert(k, 7); return m.get_or(k, 0) - 7; } ` + probe},
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
			watFile := filepath.Join(dir, "readkey_"+tc.name+".wat")
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
					"(1 = the key temp leaks, no release for a key nothing else owns; "+
					"99 = over-release, a release of a key the frame still owns)", tc.name, got)
			}
		})
	}
}
