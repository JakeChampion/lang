package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// Range match patterns on the self-host compiler (#5355). The self-host
// parser gained the same range-pattern support as the native front-end
// (#5406): a scalar arm `lo..hi` / `lo..=hi` desugars (in build_literal_match,
// at parse time) to a bound test `scrut >= lo && scrut <op> hi` in the
// literal-match if-chain — so the checker and codegen see only ordinary
// comparisons. These cases build the self-host x86-64 driver and assert the
// compiled binary agrees with the interpreter oracle. Scope matches the
// native side: signed-integer scrutinees (unsigned is rejected by the
// checker for now).
var selfHostRangePatternCases = []struct {
	name string
	main string
}{
	{"exclusive", `function cls(x: i32): i32 {
    match (x) {
        1..5 => { return 1; },
        5..10 => { return 2; },
        _ => { return 0; },
    }
    return 0 - 1;
}
function main(): i32 { return cls(3) * 100 + cls(5) * 10 + cls(9); }`},
	{"inclusive", `function cls(x: i32): i32 {
    match (x) {
        1..=5 => { return 1; },
        6..=10 => { return 2; },
        _ => { return 0; },
    }
    return 0 - 1;
}
function main(): i32 { return cls(5) * 100 + cls(10) * 10 + cls(11); }`},
	{"expr-form", `function cls(x: i32): i32 {
    return match (x) { 0..10 => 1, 10..20 => 2, _ => 0 };
}
function main(): i32 { return cls(5) * 100 + cls(15) * 10 + cls(25); }`},
	{"range-literal-mix", `function cls(x: i32): i32 {
    match (x) {
        0 => { return 9; },
        1..10 => { return 1; },
        _ => { return 0; },
    }
    return 0 - 1;
}
function main(): i32 { return cls(0) * 10 + cls(5) + cls(50); }`},
	{"guarded-range", `function cls(x: i32): i32 {
    match (x) {
        1..100 when x % 2 == 0 => { return 1; },
        1..100 => { return 2; },
        _ => { return 0; },
    }
    return 0 - 1;
}
function main(): i32 { return cls(4) * 100 + cls(5) * 10 + cls(200); }`},
}

// TestSelfHostRangePatternX86_64 builds the self-host x86-64 driver and
// checks each range-pattern program's compiled exit code against the
// interpreter oracle.
func TestSelfHostRangePatternX86_64(t *testing.T) {
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

	for _, tc := range selfHostRangePatternCases {
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
