package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// A match expression whose arms produce a struct, an enum, an array or a
// tuple (#8777). The desugar carries the arm value through a value local the
// parser declares as `0`; the checker re-declares that local from the arm
// type — or, when the arm type is an enum with no literal spelling, from the
// destination's annotation.
var matchExprValueLocalCases = []struct {
	name string
	src  string
}{
	{"struct_arm", `struct P { x: i32 }
function main(): i32 {
    var t: (i32, i32) = (7, 2);
    var p: P = match (t) { (a, b) => P { x: a } };
    return p.x;
}`}, // 7
	{"struct_nested_if_arms", `struct P { x: i32, name: string }
function main(): i32 {
    var t: (i32, i32) = (7, 2);
    var p: P = match (t) { (a, b) => P { x: a + b, name: "q" } };
    var q: P = match (t) { (a, b) => { if (a > b) { P { x: 1, name: "big" } } else { P { x: 2, name: "small" } } } };
    return p.x + q.x + q.name.len();
}`}, // 13
	{"option_via_destination", `function main(): i32 {
    var t: (i32, i32) = (7, 2);
    var o: Option[i32] = match (t) { (a, b) => Some(a + b) };
    match (o) { Some(v) => { return v; }, None => { return 50; } }
}`}, // 9
	{"result_struct_payload", `struct Q { n: i32 }
function main(): i32 {
    var t: (i32, i32) = (7, 2);
    var r: Result[Q, string] = match (t) { (a, b) => { if (a > b) { Ok(Q { n: a }) } else { Err("no") } } };
    match (r) { Ok(q) => { return q.n + 10; }, Err(_) => { return 51; } }
}`}, // 17
	{"array_arm", `function main(): i32 {
    var t: (i32, i32) = (7, 2);
    var xs: i32[] = match (t) { (a, b) => [a, b, 1] };
    return xs.len() * 10 + xs[0];
}`}, // 37
	{"tuple_arm", `function main(): i32 {
    var t: (i32, i32) = (7, 2);
    var u: (i32, i32) = match (t) { (a, b) => (b, a) };
    return u.0 * 10 + u.1;
}`}, // 27
}

func runMatchExprValueLocalX86_64(t *testing.T, tc struct{ name, src string }, gcc string, runner []string, fernBin, interpBin, stdlibRoot string) {
	want := interpExit(t, interpBin, tc.src)
	proj := t.TempDir()
	mainPath := filepath.Join(proj, "main.fern")
	if err := os.WriteFile(mainPath, []byte(tc.src), 0o644); err != nil {
		t.Fatalf("write main.fern: %v", err)
	}
	asmPath := filepath.Join(proj, "out.s")
	if out, cerr := runX86_64Bin(runner, fernBin, "-target", "x86-64-linux", "-emit", "asm", mainPath, stdlibRoot, "-o", asmPath).CombinedOutput(); cerr != nil {
		t.Fatalf("compile: %v (%s)", cerr, out)
	}
	binPath := filepath.Join(proj, "out.bin")
	if out, lerr := exec.Command(gcc, "-nostdlib", "-static", "-o", binPath, asmPath).CombinedOutput(); lerr != nil {
		t.Fatalf("link: %v (%s)", lerr, out)
	}
	rcmd := runX86_64Bin(runner, binPath)
	_ = rcmd.Run()
	if got := rcmd.ProcessState.ExitCode(); got != want {
		t.Errorf("%s = %d, want %d (interp oracle) — a match-expression value local must be declared from the arm type or the destination annotation", tc.name, got, want)
	}
}

// TestSelfHostMatchExprValueLocalX86_64 — the x86-64 leg; fern.fern is the
// driver because the re-declaration lives in the checker.
func TestSelfHostMatchExprValueLocalX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "fern.fern")
	fernBin := buildSelfHostBin(t, gcc, dir, "fern.fern", "fern")
	stdlibRoot, err := filepath.Abs("../../internal/stdlib")
	if err != nil {
		t.Fatalf("abs stdlib root: %v", err)
	}
	for _, tc := range matchExprValueLocalCases {
		t.Run(tc.name, func(t *testing.T) {
			runMatchExprValueLocalX86_64(t, tc, gcc, runner, fernBin, interpBin, stdlibRoot)
		})
	}
}

// TestSelfHostMatchExprValueLocalWasm is the wasm leg: the local's declaration
// decides the slot, and wasm validates types, so a wrong slot is a module-level
// rejection rather than a wrong answer.
func TestSelfHostMatchExprValueLocalWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping match-expression value-local wasm cases")
	}
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "fern.fern")
	fernBin := buildSelfHostBin(t, gcc, dir, "fern.fern", "fern")
	stdlibRoot, err := filepath.Abs("../../internal/stdlib")
	if err != nil {
		t.Fatalf("abs stdlib root: %v", err)
	}
	for _, tc := range matchExprValueLocalCases {
		t.Run(tc.name, func(t *testing.T) {
			want := interpExit(t, interpBin, tc.src)
			proj := t.TempDir()
			mainPath := filepath.Join(proj, "main.fern")
			if err := os.WriteFile(mainPath, []byte(tc.src), 0o644); err != nil {
				t.Fatalf("write main.fern: %v", err)
			}
			outWat := filepath.Join(proj, "out.wat")
			var stderr strings.Builder
			cmd := runX86_64Bin(runner, fernBin, "-target", "wasm32-wasi", "-emit", "asm", mainPath, stdlibRoot, "-o", outWat)
			cmd.Stderr = &stderr
			if cerr := cmd.Run(); cerr != nil {
				t.Fatalf("compile: %v (%s)", cerr, stderr.String())
			}
			rcmd := exec.Command("wasmtime", "run", outWat)
			_ = rcmd.Run()
			if rcmd.ProcessState == nil || !rcmd.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q", tc.name)
			}
			if got := rcmd.ProcessState.ExitCode(); got != want {
				t.Errorf("%s = %d, want %d (interp oracle) — a match-expression value local must be declared from the arm type or the destination annotation", tc.name, got, want)
			}
		})
	}
}
