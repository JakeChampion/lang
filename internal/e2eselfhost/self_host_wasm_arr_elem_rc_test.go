package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestSelfHostRcArrElemWasm proves the array-element recursive release: a
// string-array or struct-array LOCAL, when freed at function exit as the last
// owner, now releases its rc-boxed ELEMENTS (one level) via the new
// $__fern_arr_dec_ptr helper before freeing the buffer — closing the
// pervasive `string[]` / struct-array element leak. An aliased array (rc>1)
// only decrements the buffer; elements live on through the other owner. Every
// case checks the over-release detector stays 0 (the dangerous regression is
// releasing an element a surviving owner still references) and the value is
// correct.
func TestSelfHostRcArrElemWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping wasm arr-elem-rc e2e")
	}
	gcc, runner := x86_64Tooling(t)

	dir := t.TempDir()
	copySelfHostDriver(t, dir, "wasm_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_run.fern", "wasm_run")

	cases := []struct {
		name string
		src  string
		exit int
	}{
		// A string-array of heap strings, freed at exit, releases each string
		// (one level) — value-correct + detector clean.
		{"str-array-elem-released", "function main(): i32 { var a: string = \"a\" + \"x\"; var b: string = \"b\" + \"y\"; var arr: string[] = [a, b]; return arr[0].len() + arr[1].len() + __rc_underflow_count(); }", 4},
		// The dangerous case: an element is ALSO held by a surviving local. The
		// construction-inc + rc==1-guarded element release must balance across
		// both owners (no over-release of the still-referenced string).
		{"str-array-elem-aliased-clean", "function main(): i32 { var s: string = \"ab\" + \"cd\"; var arr: string[] = [s]; return arr[0].len() + s.len() + __rc_underflow_count(); }", 8},
		// Appended (not literal) heap strings are released too (the .append
		// path retains, the exit sweep recursively releases).
		{"str-array-append-released", "function main(): i32 { var arr: string[] = []; var i = 0; while (i < 3) { arr = arr.append(\"x\" + \"y\"); i = i + 1; } return arr.len() + arr[2].len() + __rc_underflow_count(); }", 5},
		// A churn of string-arrays: each iteration builds and drops an array of
		// heap strings; element release reclaims them (no unbounded growth) and
		// the detector stays clean across many cycles.
		{"str-array-churn-clean", "function mk(): i32 { var arr: string[] = [\"a\" + \"b\", \"c\" + \"d\", \"e\" + \"f\"]; return arr[0].len() + arr[1].len() + arr[2].len(); } function main(): i32 { var k = 0; var s = 0; while (k < 50000) { s = mk(); k = k + 1; } return (s % 7) + __rc_underflow_count(); }", 6},
		// A struct-array freed releases each struct box (one level).
		{"struct-array-elem-released", "struct P { x: i32, y: i32 } function main(): i32 { var ps: P[] = [P { x: 1, y: 2 }, P { x: 3, y: 4 }]; return ps[0].x + ps[1].y + __rc_underflow_count(); }", 5},
		// A struct-array where a struct element is also a surviving local: no
		// over-release.
		{"struct-array-elem-aliased-clean", "struct P { x: i32, y: i32 } function main(): i32 { var p = P { x: 5, y: 6 }; var ps: P[] = [p]; return ps[0].x + p.y + __rc_underflow_count(); }", 11},
		// An aliased string-array: both locals own the buffer; the element
		// release fires exactly once (at the last owner), detector clean.
		{"str-array-aliased-buffer-clean", "function main(): i32 { var a: string = \"p\" + \"q\"; var xs: string[] = [a]; var ys: string[] = xs; return ys[0].len() + xs[0].len() + __rc_underflow_count(); }", 4},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			wat := runCapture(t, gcc, runner, driverBin, []byte(tc.src))
			if len(wat) == 0 {
				t.Fatal("wasm emitter produced 0 bytes")
			}
			watPath := filepath.Join(dir, tc.name+".wat")
			if err := os.WriteFile(watPath, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			cmd := exec.Command("wasmtime", "run", "--dir", dir, watPath)
			_, _ = cmd.Output()
			if code := cmd.ProcessState.ExitCode(); code != tc.exit {
				t.Errorf("%s: wasm exited %d, want %d\n--- WAT ---\n%s", tc.name, code, tc.exit, wat)
			}
		})
	}
}
