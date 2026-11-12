package e2ecompiler

import (
	"os/exec"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

// A view that is stored in a variable of an outer block must not view the
// storage of an inner block (#14991). Both checkers give the same verdict.
func viewStoreCases(t *testing.T, bin string, runner []string, dir string) {
	t.Helper()
	const decls = `struct H { v: [i32] }
function first(xs: i32[]): [i32] { return xs[0:1]; }
function fill[T](v: T, acc: T[]): T[] { return acc.append(v); }
function gather[T](v: T): Option[T[]] { let none: T[] = []; return Some(fill(v, none)); }
`
	cases := []struct {
		name, body string
		reject     bool
	}{
		{"loop-local", `let last: [i32] = [0][:]; for k in 0..4 { let tmp: i32[] = [k, k + 1]; last = tmp[0:2]; } return last[0];`, true},
		{"temporary", `let last: [i32] = [0][:]; for k in 0..4 { last = [k, k + 1][0:2]; } return last[0];`, true},
		{"option", `let o: Option[[i32]] = None; if (n > 0) { let tmp: i32[] = [1, 2, 3]; o = Some(tmp[0:2]); } return n;`, true},
		{"append", `let views: [i32][] = []; let k: i32 = 0; while (k < 4) { let tmp: i32[] = [k, k + 1]; views = views.append(tmp[0:2]); k = k + 1; } return views.len();`, true},
		{"with", `let views: [i32][] = [[0][:]]; if (n > 0) { let tmp: i32[] = [1, 2]; views = views.with(0, tmp[0:2]); } return views.len();`, true},
		{"struct-field", `let h: H = H { v: [0][:] }; if (n > 0) { let tmp: i32[] = [1, 2]; h = H { v: tmp[0:2] }; } return h.v[0];`, true},
		{"inner-view", `let last: [i32] = [0][:]; for k in 0..4 { let tmp: i32[] = [k, k + 1]; let inner: [i32] = tmp[0:2]; last = inner; } return last[0];`, true},
		{"two-blocks-down", `let last: [i32] = [0][:]; for k in 0..4 { let tmp: i32[] = [k, k + 1]; if (k > 1) { last = tmp[0:2]; } } return last[0];`, true},
		{"middle-block-target", `let n2 = n; for k in 0..4 { let last: [i32] = [0][:]; if (k > 1) { let tmp: i32[] = [k, k + 1]; last = tmp[0:2]; } n2 = n2 + last[0]; } return n2;`, true},
		{"parameter-target", `if (n > 0) { let tmp: i32[] = [1, 2]; p = tmp[0:2]; } return p[0];`, true},
		{"outer-storage", `let base: i32[] = [5, 6, 7, 8]; let last: [i32] = base[0:1]; for k in 0..3 { last = base[k:k + 2]; } return last[0];`, false},
		{"outer-storage-append", `let base: i32[] = [5, 6, 7, 8]; let views: [i32][] = []; for k in 0..3 { views = views.append(base[k:k + 1]); } return views.len();`, false},
		{"inner-view-of-outer-storage", `let base: i32[] = [5, 6, 7, 8]; let last: [i32] = base[0:1]; for k in 0..3 { let inner: [i32] = base[k:k + 2]; last = inner; } return last[0];`, false},
		{"one-block", `let v: [i32] = [0][:]; let tmp: i32[] = [1, 2, 3]; v = tmp[1:3]; return v[0];`, false},
		{"one-inner-block", `let n2 = n; for k in 0..4 { let v: [i32] = [0][:]; let tmp: i32[] = [k, k + 1]; v = tmp[0:2]; n2 = n2 + v[0]; } return n2;`, false},
		{"temporary-in-one-block", `let v: [i32] = [0][:]; v = [4, 5, 6][1:3]; return v[0];`, false},
		{"parameter-storage", `let last: [i32] = xs[0:1]; for k in 0..2 { last = xs[k:k + 1]; } return last[0];`, false},
		{"call-on-parameter-storage", `let last: [i32] = xs[0:1]; for k in 0..2 { last = first(xs); } return last[0];`, false},
		{"scalar-of-inner-storage", `let total: i32[] = []; for k in 0..4 { let tmp: i32[] = [k, k + 1]; total = total.append(tmp[0:2].len()); } return total.len();`, false},
		{"owned-copy", `let rows: i32[][] = []; for k in 0..4 { let tmp: i32[] = [k, k + 1]; rows = rows.append(tmp); } return rows.len();`, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			source := decls + "function work(n: i32, xs: i32[], p: [i32]): i32 { " + tc.body + " } function main(): i32 { return 0; }"
			check := func(label string, ds []driverDiag) {
				t.Helper()
				if !tc.reject && len(ds) == 0 {
					return
				}
				if tc.reject {
					for _, d := range ds {
						if d.code == "E063" {
							return
						}
					}
				}
				t.Fatalf("%s: reject=%v, diagnostics=%v", label, tc.reject, ds)
			}
			check("Go", goCheckerDiags(t, dir, source))
			command := exec.Command(bin)
			if len(runner) > 0 {
				command = exec.Command(runner[0], append(append([]string{}, runner[1:]...), bin)...)
			}
			command.Stdin = strings.NewReader(source)
			check("self-host", driverDiags(runCheckerDriver(t, command, tc.name)))
		})
	}
}

func TestSelfHostViewStoreX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/checker_codes_run.fern")
	bin := buildSelfHostBin(t, gcc, dir, "drivers/checker_codes_run.fern", "view_store_checker")
	viewStoreCases(t, bin, runner, dir)
}

func TestSelfHostViewStoreWasm(t *testing.T) {
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/checker_codes_run.fern")
	bin := buildSelfHostBinFor(t, dir, "drivers/checker_codes_run.fern", "view_store_checker.wasm", e2eharness.TargetWasm32Wasi)
	viewStoreCases(t, bin, []string{e2eharness.Wasmtime(t), "run"}, dir)
}
