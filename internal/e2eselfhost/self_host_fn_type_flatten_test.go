package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The annotation-driven fn-value lift (#5834's family: an fn-typed tuple
// element, an Option/Result payload, a declared fn return) reads the DECLARED
// TYPE to rule out the const reading a bare zero-arg name would otherwise get.
// Every one of those decisions is made on a type SPELLING, and the module
// flattener rewrites that spelling before the lift sees it.
//
// It rewrote it wrongly. parse_type_ref gives a function type an EMPTY base
// with args = the parameters plus the result last — structurally the same shape
// a generic application has — and rewrite_type_name's generic branch claimed it,
// re-spelling `() => i32` as `[i32]`. With no arrow left, the annotation no
// longer said "fn value": `Option[() => i32] = Some(a1)` arrived as
// `Option[[i32]]`, the payload was CALLED where the variant is built, and the
// match arm then called its i32 result. SIGSEGV, against 3 from both native and
// the interpreter (#7959). `Result[() => i32, string]` did not even lower.
//
// tupleFnZeroArgCases already covers those shapes — but only through the
// stdin driver (`asm_ir_run.fern`), which parses one source and never flattens,
// so the whole corpus ran on the one path that could not see this. This leg runs
// the SAME corpus through the full `fern.fern` CLI, which flattens; that is the
// path every real program takes and the one the bug lived on.
//
// The unit-level half — that rewrite_type_name round-trips a fn spelling and
// rewrites its parameters and result — is asserted in flatten.fern's own
// self-test (returns 205-212, TestSelfHostFlattenX86_64).

// fnTypeTupleCheckerGap names the corpus rows this leg cannot run, and why. The
// self-host CHECKER refuses an annotated fn-typed TUPLE outright — `var t:
// ((() => i32), i32) = (a1, 4)` draws E003 "cannot assign tuple to variable of
// type tuple" where native's `-check` accepts it — because the parser coarsens a
// tuple element containing an arrow to the bare tag "fn" and no type resolver
// has an arm for that tag, so the DECLARED element reads `unknown` while the
// init types correctly as a callable. Resolving the tag to the opaque TypeFunc
// (the shape a "fn"-coarsened struct field and parameter already resolve to)
// clears E003 and lands the case in the #4346 representability bucket instead:
// the coarsened spelling records no RESULT type either, so `t.0()` still has no
// type. Closing it needs the element's return spelling carried past the
// coarsening, which is checker/parser work of its own — #7961.
//
// The stdin driver these rows are also run through (TestSelfHostTupleFnZeroArgIR*)
// does not type-check, which is why they pass there. Every row without a
// fn-typed tuple annotation — the Option/Result payloads, the fn return, the
// user-enum field, and all the const-read regressions — runs here.
var fnTypeTupleCheckerGap = map[string]bool{
	"tuple-zeroarg":          true,
	"tuple-in-array-zeroarg": true,
	"tuple-two-fn-elems":     true,
	"tuple-onearg":           true,
	"tuple-lambda":           true,
	"const-and-fn-mixed":     true,
}

// fnTypeCrossModuleCases add what a single source cannot reach: the fn type and
// the function it names come from DIFFERENT modules, so the spelling being
// rewritten is also the spelling being mangled.
var fnTypeCrossModuleCases = []struct {
	name string
	lib  string
	main string
	exit int
}{
	{
		// The #7959 shape with the payload's function imported: the annotation is
		// rewritten AND the bare name mangles to `lib__three`.
		name: "xmod-option-zeroarg-payload",
		lib:  "pub function three(): i32 { return 3; }",
		main: "import \"lib\";\nfunction main(): i32 {\n    var o: Option[() => i32] = Some(lib.three);\n    match (o) { Some(f) => { return f() + 4; }, None => { return 0; } }\n    return 9;\n}",
		exit: 7,
	},
	{
		// A fn type naming an imported STRUCT in parameter and result position —
		// the recursion has to mangle `lib.P` inside the arrow form, which the
		// old generic branch dropped along with the arrow.
		name: "xmod-fn-type-struct-positions",
		lib:  "pub struct P { a: i32 }\npub function mk(): P { return P { a: 3 }; }\npub function take(p: P): i32 { return p.a * 2; }",
		main: "import \"lib\";\nfunction main(): i32 {\n    var f: () => lib.P = lib.mk;\n    var g: (lib.P) => i32 = lib.take;\n    return g(f());\n}",
		exit: 6,
	},
	{
		// Result's SECOND type argument, cross-module: `Err` indexes arg 1, and
		// the arrow has to survive in that position too.
		name: "xmod-result-err-zeroarg",
		lib:  "pub function five(): i32 { return 5; }",
		main: "import \"lib\";\nfunction main(): i32 {\n    var r: Result[string, () => i32] = Err(lib.five);\n    match (r) { Ok(s) => { return 1; }, Err(f) => { return f(); } }\n    return 9;\n}",
		exit: 5,
	},
}

// TestSelfHostFnTypeFlattenX86_64 runs the annotated fn-value corpus through the
// flattening CLI on x86-64 — the leg the bug was measured on.
func TestSelfHostFnTypeFlattenX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "fern.fern")
	fernBin := buildSelfHostBin(t, gcc, dir, "fern.fern", "fern")
	stdlibRoot, err := filepath.Abs("../../internal/stdlib")
	if err != nil {
		t.Fatalf("abs stdlib root: %v", err)
	}

	for _, tc := range tupleFnZeroArgCases {
		if fnTypeTupleCheckerGap[tc.name] {
			continue
		}
		t.Run(tc.name, func(t *testing.T) {
			proj := t.TempDir()
			mustWriteFile(t, filepath.Join(proj, "main.fern"), tc.src)
			got := compileLinkRunX86_64(t, gcc, runner, fernBin, stdlibRoot, proj, "main.fern")
			if got != tc.exit {
				t.Errorf("%s = %d, want %d — the flattened annotation must still name a fn type", tc.name, got, tc.exit)
			}
		})
	}

	for _, tc := range fnTypeCrossModuleCases {
		t.Run(tc.name, func(t *testing.T) {
			proj := t.TempDir()
			mustWriteFile(t, filepath.Join(proj, "lib.fern"), tc.lib)
			mustWriteFile(t, filepath.Join(proj, "main.fern"), tc.main)
			got := compileLinkRunX86_64(t, gcc, runner, fernBin, stdlibRoot, proj, "main.fern")
			if got != tc.exit {
				t.Errorf("%s = %d, want %d", tc.name, got, tc.exit)
			}
		})
	}
}

// TestSelfHostFnTypeFlattenWasm is the wasm leg. The fix is in the shared
// flattener, so every backend picks it up; before it, this corpus trapped here
// (exit 134) exactly as the register backends SIGSEGV'd.
func TestSelfHostFnTypeFlattenWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping the fn-type flatten wasm leg")
	}
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "fern.fern")
	fernBin := buildSelfHostBin(t, gcc, dir, "fern.fern", "fern")
	stdlibRoot, err := filepath.Abs("../../internal/stdlib")
	if err != nil {
		t.Fatalf("abs stdlib root: %v", err)
	}

	for _, tc := range tupleFnZeroArgCases {
		if fnTypeTupleCheckerGap[tc.name] {
			continue
		}
		t.Run(tc.name, func(t *testing.T) {
			proj := t.TempDir()
			mustWriteFile(t, filepath.Join(proj, "main.fern"), tc.src)
			if got := compileRunWasm(t, runner, fernBin, stdlibRoot, proj, "main.fern"); got != tc.exit {
				t.Errorf("%s = %d, want %d", tc.name, got, tc.exit)
			}
		})
	}

	for _, tc := range fnTypeCrossModuleCases {
		t.Run(tc.name, func(t *testing.T) {
			proj := t.TempDir()
			mustWriteFile(t, filepath.Join(proj, "lib.fern"), tc.lib)
			mustWriteFile(t, filepath.Join(proj, "main.fern"), tc.main)
			if got := compileRunWasm(t, runner, fernBin, stdlibRoot, proj, "main.fern"); got != tc.exit {
				t.Errorf("%s = %d, want %d", tc.name, got, tc.exit)
			}
		})
	}
}

func mustWriteFile(t *testing.T, path, src string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// compileLinkRunX86_64 compiles proj/entry with the self-host CLI, links the asm
// with gcc and returns the program's exit code.
func compileLinkRunX86_64(t *testing.T, gcc string, runner []string, fernBin, stdlibRoot, proj, entry string) int {
	t.Helper()
	asmPath := filepath.Join(proj, "out.s")
	cmd := runX86_64Bin(runner, fernBin, "-target", "x86-64-linux", "-emit", "asm", filepath.Join(proj, entry), stdlibRoot, "-o", asmPath)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("compile: %v (%s)", err, out)
	}
	binPath := filepath.Join(proj, "out.bin")
	if out, err := exec.Command(gcc, "-nostdlib", "-static", "-o", binPath, asmPath).CombinedOutput(); err != nil {
		t.Fatalf("link: %v (%s)", err, out)
	}
	var run *exec.Cmd
	if len(runner) == 0 {
		run = exec.Command(binPath)
	} else {
		run = exec.Command(runner[0], append(runner[1:], binPath)...)
	}
	_ = run.Run()
	return run.ProcessState.ExitCode()
}

// compileRunWasm is the same through the wasm backend, run under wasmtime.
func compileRunWasm(t *testing.T, runner []string, fernBin, stdlibRoot, proj, entry string) int {
	t.Helper()
	watPath := filepath.Join(proj, "out.wat")
	var stderr strings.Builder
	cmd := runX86_64Bin(runner, fernBin, "-target", "wasm32-wasi", "-emit", "asm", filepath.Join(proj, entry), stdlibRoot, "-o", watPath)
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("compile: %v (%s)", err, stderr.String())
	}
	run := exec.Command("wasmtime", "run", watPath)
	_ = run.Run()
	if run.ProcessState == nil || !run.ProcessState.Exited() {
		t.Fatalf("wasmtime did not exit normally")
	}
	return run.ProcessState.ExitCode()
}
