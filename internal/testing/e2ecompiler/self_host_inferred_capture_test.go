package e2ecompiler

import (
	"os/exec"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

// A closure assigns a capture whose `let` has no written type. E049 uses the
// inferred type, so both checkers give the same verdict (#15439).
func inferredCaptureCases(t *testing.T, bin string, runner []string, dir string) {
	t.Helper()
	const decls = `struct N { name: string, n: i32 }
function make(): N { return N { name: "a", n: 1 }; }
function pair(): (i32, string) { return (1, "x"); }
function same[T](v: T): T { return v; }
function count(xs: i32[]): i32 { return xs.len(); }
`
	cases := []struct {
		name, body string
		reject     bool
	}{
		{"record-from-call", `let g = make(); let f = (): void => { g = make(); }; f();`, true},
		{"string-concat", `let s = a + "b"; let f = (): void => { s = s + "x"; }; f();`, true},
		{"len", `let n = xs.len(); let f = (): void => { n = n + 1; }; f();`, false},
		{"array-method", `let ys = xs.append(1); let f = (): void => { ys = ys.append(2); }; f();`, true},
		{"tuple-from-call", `let p = pair(); let f = (): void => { p = pair(); }; f();`, true},
		{"option", `let o = Some(make()); let f = (): void => { o = None; }; f();`, true},
		{"loop-string-field", `for x in ns { let cur = x.name; let f = (): void => { cur = cur + "!"; }; f(); }`, true},
		{"loop-scalar-field", `for x in ns { let k = x.n; let f = (): void => { k = k + 1; }; f(); }`, false},
		{"comparison", `let b = xs.len() > 2; let f = (): void => { b = false; }; f();`, false},
		{"float", `let d = 1.5 * 2.0; let f = (): void => { d = d + 1.0; }; f();`, false},
		{"generic-record", `let t = same(make()); let f = (): void => { t = make(); }; f();`, true},
		{"generic-scalar", `let u = same(3); let f = (): void => { u = u + 1; }; f();`, false},
		{"scalar-from-call", `let c = count(xs); let f = (): void => { c = c + 1; }; f();`, false},
		{"nested-block", `if (xs.len() > 0) { let g = make(); let f = (): void => { g = make(); }; f(); }`, true},
		{"view", `let w = xs[0:1]; let f = (): void => { w = xs[0:1]; }; f();`, true},
		{"cast", `let h = a.len() as i64; let f = (): void => { h = h + 1i64; }; f();`, false},
		{"byte", `let ch = a[0]; let f = (): void => { ch = a[0]; }; f();`, false},
		{"string-field", `let m = make().name; let f = (): void => { m = "z"; }; f();`, true},
		{"scalar-field", `let z = make().n; let f = (): void => { z = 2; }; f();`, false},
		{"match-value", `let q = match (xs.len()) { 0 => make(), _ => make() }; let f = (): void => { q = make(); }; f();`, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			source := decls + "function work(xs: i32[], a: string, ns: N[]): i32 { " + tc.body + " return 0; } function main(): i32 { return 0; }"
			check := func(label string, ds []driverDiag) {
				t.Helper()
				found := false
				for _, d := range ds {
					if d.code == "E049" {
						found = true
					}
				}
				if found != tc.reject {
					t.Fatalf("%s: reject=%v, diagnostics=%v", label, tc.reject, ds)
				}
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

func TestSelfHostInferredCaptureX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/checker_codes_run.fern")
	bin := buildSelfHostBin(t, gcc, dir, "drivers/checker_codes_run.fern", "inferred_capture_checker")
	inferredCaptureCases(t, bin, runner, dir)
}

func TestSelfHostInferredCaptureWasm(t *testing.T) {
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/checker_codes_run.fern")
	bin := buildSelfHostBinFor(t, dir, "drivers/checker_codes_run.fern", "inferred_capture_checker.wasm", e2eharness.TargetWasm32Wasi)
	inferredCaptureCases(t, bin, []string{e2eharness.Wasmtime(t), "run"}, dir)
}
