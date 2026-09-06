package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// std/array's projection-extremum verbs `max_by_i32_key[T](xs, key) -> Option[T]`
// and `min_by_i32_key` — the element whose i32 `key(x)` projection is largest /
// smallest (#4416 roadmap #10, the "which record has the max timestamp" case).
// The projection sibling of `sort_by_i32_key`; the closure over a generic `T[]`
// lowers on every native backend and the self-host IR path (same shape as
// `find` / `sort_by`). Empty -> None; ties keep the FIRST extremum.
//
// Each inline case inlines minimal `max_by_i32_key` / `min_by_i32_key` bodies so
// the single-program self-host driver (which resolves no imports) can compile it,
// then encodes the result as a small exit code.
var maxByKeyCases = []struct {
	name string
	main string
	want int
}{
	// keys [30,10,50,20]; max is at index 2 -> tag 3; min at index 1 -> tag 2; 3*10+2 = 32.
	{"max-min", `function max_by_i32_key[T](xs: T[], key: (T) => i32): Option[T] { if (xs.len() == 0) { return None; } var best: T = xs[0]; var bk: i32 = key(best); var i: i32 = 1; while (i < xs.len()) { var k: i32 = key(xs[i]); if (k > bk) { best = xs[i]; bk = k; } i = i + 1; } return Some(best); }
function min_by_i32_key[T](xs: T[], key: (T) => i32): Option[T] { if (xs.len() == 0) { return None; } var best: T = xs[0]; var bk: i32 = key(best); var i: i32 = 1; while (i < xs.len()) { var k: i32 = key(xs[i]); if (k < bk) { best = xs[i]; bk = k; } i = i + 1; } return Some(best); }
struct R { tag: i32, ts: i32 }
function ts(r: R): i32 { return r.ts; }
function pick(o: Option[R]): i32 { match (o) { Some(r) => { return r.tag; }, None => { return 0 - 1; } } }
function main(): i32 { var rs: R[] = [R { tag: 1, ts: 30 }, R { tag: 2, ts: 10 }, R { tag: 3, ts: 50 }, R { tag: 4, ts: 20 }]; return pick(max_by_i32_key(rs, ts)) * 10 + pick(min_by_i32_key(rs, ts)); }`, 32},
	// empty -> None for both; encode as 2 (max None) + 3 (min None) = 5.
	{"empty-none", `function max_by_i32_key[T](xs: T[], key: (T) => i32): Option[T] { if (xs.len() == 0) { return None; } var best: T = xs[0]; var bk: i32 = key(best); var i: i32 = 1; while (i < xs.len()) { var k: i32 = key(xs[i]); if (k > bk) { best = xs[i]; bk = k; } i = i + 1; } return Some(best); }
function min_by_i32_key[T](xs: T[], key: (T) => i32): Option[T] { if (xs.len() == 0) { return None; } var best: T = xs[0]; var bk: i32 = key(best); var i: i32 = 1; while (i < xs.len()) { var k: i32 = key(xs[i]); if (k < bk) { best = xs[i]; bk = k; } i = i + 1; } return Some(best); }
struct R { tag: i32, ts: i32 }
function ts(r: R): i32 { return r.ts; }
function main(): i32 { var e: R[] = []; var r = 0; match (max_by_i32_key(e, ts)) { Some(x) => {}, None => { r = r + 2; } } match (min_by_i32_key(e, ts)) { Some(x) => {}, None => { r = r + 3; } } return r; }`, 5},
	// ties keep the FIRST extremum: two elements share ts 99; max returns the
	// earlier one (tag 10), not tag 11.
	{"ties-first", `function max_by_i32_key[T](xs: T[], key: (T) => i32): Option[T] { if (xs.len() == 0) { return None; } var best: T = xs[0]; var bk: i32 = key(best); var i: i32 = 1; while (i < xs.len()) { var k: i32 = key(xs[i]); if (k > bk) { best = xs[i]; bk = k; } i = i + 1; } return Some(best); }
struct R { tag: i32, ts: i32 }
function ts(r: R): i32 { return r.ts; }
function pick(o: Option[R]): i32 { match (o) { Some(r) => { return r.tag; }, None => { return 0 - 1; } } }
function main(): i32 { var rs: R[] = [R { tag: 10, ts: 99 }, R { tag: 7, ts: 5 }, R { tag: 11, ts: 99 }]; return pick(max_by_i32_key(rs, ts)); }`, 10},
}

// TestNativeArrayMaxByKey runs the inline programs on interp / x86-64 / wasm.
func TestNativeArrayMaxByKey(t *testing.T) {
	for _, tc := range maxByKeyCases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			p := filepath.Join(dir, "main.fern")
			if err := os.WriteFile(p, []byte(tc.main+"\n"), 0o644); err != nil {
				t.Fatalf("write: %v", err)
			}
			if _, code := runFixtureInterp(t, p, ""); code != tc.want {
				t.Errorf("%s interp = %d, want %d", tc.name, code, tc.want)
			}
			if _, code := runFixtureX86_64(t, p, ""); code != tc.want {
				t.Errorf("%s x86-64 = %d, want %d", tc.name, code, tc.want)
			}
			if code := runWasm(t, tc.main+"\n"); code != tc.want {
				t.Errorf("%s wasm = %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}

// TestNativeArrayMaxByKeyModule exercises the shipped `import "std/array"` bodies
// over a struct array keyed by an i32 field, incl. a negated-key inversion and
// the empty case.
func TestNativeArrayMaxByKeyModule(t *testing.T) {
	src := `import "std/array" as arr;
struct Rec { id: i32, ts: i32 }
function ts_of(r: Rec): i32 { return r.ts; }
function main(): i32 {
    var r = 0;
    var rs: Rec[] = [Rec { id: 1, ts: 30 }, Rec { id: 2, ts: 10 }, Rec { id: 3, ts: 50 }, Rec { id: 4, ts: 20 }];
    match (arr.max_by_i32_key(rs, ts_of)) { Some(x) => { if (x.id == 3) { r = r + 1; } }, None => {} }
    match (arr.min_by_i32_key(rs, ts_of)) { Some(x) => { if (x.id == 2) { r = r + 2; } }, None => {} }
    match (arr.min_by_i32_key(rs, (x: Rec): i32 => { return 0 - x.ts; })) { Some(x) => { if (x.id == 3) { r = r + 4; } }, None => {} }
    var e: Rec[] = [];
    match (arr.max_by_i32_key(e, ts_of)) { Some(x) => {}, None => { r = r + 8; } }
    return r;
}
`
	p := writeIterProg(t, src)
	if _, code := runFixtureInterp(t, p, ""); code != 15 {
		t.Errorf("max_by_key module interp = %d, want 15", code)
	}
	if _, code := runFixtureX86_64(t, p, ""); code != 15 {
		t.Errorf("max_by_key module x86-64 = %d, want 15", code)
	}
	if code := runWasm(t, src); code != 15 {
		t.Errorf("max_by_key module wasm = %d, want 15", code)
	}
}

// TestSelfHostArrayMaxByKeyIRX86_64 drives the inline cases through the
// self-hosted x86-64 compiler (asm_run), oracle-checking the exit code — pinning
// that the projection-extremum closures lower + run on the self-host IR path.
func TestSelfHostArrayMaxByKeyIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	src, err := os.ReadFile(filepath.Join("../../examples/self_host", "asm_run.fern"))
	if err != nil {
		t.Fatalf("read asm_run.fern: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "asm_run.fern"), src, 0o644); err != nil {
		t.Fatalf("write asm_run.fern: %v", err)
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")

	for _, tc := range maxByKeyCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, gcc, runner, driverBin, []byte(tc.main+"\n"))
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
				t.Errorf("%s self-host x86-64 = %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}
