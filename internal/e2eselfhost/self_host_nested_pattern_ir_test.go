package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// Nested match patterns on the self-host compiler (#5353). The self-host
// parser gained the same parse-time desugar as the native front-end
// (#5389): a nested variant sub-pattern (`Some(Ok(n))`) is rewritten into
// a flat outer arm whose body re-matches the payload on an inner `match`
// (desugar_nested_arms in parser.fern), so the checker and codegen see
// only flat arms. These cases route each program through the self-host
// x86-64 driver (asm_run.fern) and assert the compiled binary agrees with
// the interpreter oracle — the desugared Option/Result matches lower via
// whichever path the driver picks (the IR path bails Option/Result
// scrutinees to the AST emitter, which is expected and still correct).
//
// Scope matches the self-host's single-payload variant model: nesting at
// the one payload slot (`Some(Ok(n))`), including deep nesting, flat
// siblings, guards, and the outer-`_` fallthrough.
var selfHostNestedPatternCases = []struct {
	name string
	main string
}{
	{"headline", `function g(o: Option[Result[i32, i32]]): i32 {
    match (o) {
        Some(Ok(n)) => { return n + 100; },
        Some(Err(e)) => { return e; },
        None => { return 0 - 1; },
    }
    return 0 - 2;
}
function main(): i32 { return g(Some(Ok(5))); }`},
	{"flat-sibling", `function g(o: Option[Result[i32, i32]]): i32 {
    match (o) {
        Some(Ok(n)) => { return n + 100; },
        Some(w) => { return 9; },
        None => { return 0 - 1; },
    }
    return 0 - 2;
}
function main(): i32 { return g(Some(Err(3))); }`},
	{"guarded", `function g(o: Option[Result[i32, i32]]): i32 {
    match (o) {
        Some(Ok(n)) when n > 10 => { return n + 100; },
        Some(Ok(n)) => { return n; },
        Some(Err(e)) => { return e; },
        None => { return 0 - 1; },
    }
    return 0 - 2;
}
function main(): i32 { return g(Some(Ok(3))); }`},
	{"double-nest", `function g(o: Result[Result[i32, i32], i32]): i32 {
    match (o) {
        Ok(Ok(n)) => { return n + 50; },
        Ok(Err(e)) => { return e + 20; },
        Err(x) => { return x; },
    }
    return 0 - 2;
}
function main(): i32 { return g(Ok(Err(3))); }`},
	{"wildcard-fallthrough", `function g(o: Option[Result[i32, i32]]): i32 {
    match (o) {
        Some(Ok(n)) => { return n + 100; },
        _ => { return 7; },
    }
    return 0 - 2;
}
function main(): i32 { return g(Some(Err(9))); }`},
	{"expr-form", `function g(o: Option[Result[i32, i32]]): i32 {
    return match (o) {
        Some(Ok(n)) => n + 100,
        Some(Err(e)) => e,
        None => 0 - 1,
    };
}
function main(): i32 { return g(Some(Ok(5))); }`},
}

// TestSelfHostNestedPatternX86_64 builds the self-host x86-64 driver and
// checks each nested-pattern program's compiled exit code against the
// interpreter oracle.
func TestSelfHostNestedPatternX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := writeSelfHostAsmProject(t)
	src, err := os.ReadFile(filepath.Join("../../examples/self_host", "asm_run.fern"))
	if err != nil {
		t.Fatalf("read asm_run.fern: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "asm_run.fern"), src, 0o644); err != nil {
		t.Fatalf("write asm_run.fern: %v", err)
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")

	for _, tc := range selfHostNestedPatternCases {
		t.Run(tc.name, func(t *testing.T) {
			prog := []byte(tc.main + "\n")
			want := interpExit(t, interpBin, string(prog))
			asm := runCapture(t, gcc, runner, driverBin, prog)
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
			if code := cmd.ProcessState.ExitCode(); code != want {
				t.Errorf("%s exited %d, want %d (interp oracle)", tc.name, code, want)
			}
		})
	}
}
