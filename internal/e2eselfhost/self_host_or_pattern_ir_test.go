package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// Literal or-patterns on the self-host compiler (#5355): `match x { 1 | 2
// | 3 => … }`. The self-host literal-match arm loop now splits on `|` at a
// precedence above bitwise-or (parse_lit_alts), so `1 | 2` reads as "1 or
// 2", not the value 3 — matching the native front-end. Each alternative
// becomes its own literal arm sharing the guard + body, expanded through
// the existing build_literal_match if-chain. Ranges combine with `|` too
// (`1..5 | 10..15`). These cases build the self-host x86-64 driver and
// oracle-check the compiled binary against the interpreter.
var selfHostOrPatternCases = []struct {
	name string
	main string
}{
	{"literal-or", `function f(x: i32): i32 {
    match (x) {
        1 | 2 | 3 => { return 9; },
        _ => { return 0; },
    }
    return 0 - 1;
}
function main(): i32 { return f(1) * 1000 + f(2) * 100 + f(3) * 10 + f(5); }`},
	{"literal-or-expr", `function f(x: i32): i32 {
    return match (x) { 1 | 2 => 7, 3 | 4 => 8, _ => 0 };
}
function main(): i32 { return f(2) * 100 + f(4) * 10 + f(9); }`},
	{"string-or", `function f(s: string): i32 {
    match (s) {
        "a" | "b" | "c" => { return 5; },
        _ => { return 0; },
    }
    return 0 - 1;
}
function main(): i32 { return f("b") * 10 + f("z"); }`},
	{"or-with-guard", `function f(x: i32): i32 {
    match (x) {
        1 | 2 | 3 when x > 1 => { return 9; },
        _ => { return 0; },
    }
    return 0 - 1;
}
function main(): i32 { return f(1) * 100 + f(2) * 10 + f(5); }`},
	{"range-or", `function f(x: i32): i32 {
    match (x) {
        1..5 | 10..15 => { return 1; },
        _ => { return 0; },
    }
    return 0 - 1;
}
function main(): i32 { return f(3) * 100 + f(12) * 10 + f(7); }`},
}

func TestSelfHostOrPatternX86_64(t *testing.T) {
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

	for _, tc := range selfHostOrPatternCases {
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
