package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// intBinopReassignCases pin the string-freshness collector against integer
// arithmetic. str_local_binding_is_fresh admits every `+` on the strength of
// the binding being a string, and collect_fresh_string_in_stmt used to admit a
// `var` on that predicate alone — so `var e: i32 = ae + 1` was credited as a
// fresh string, and the later `e = eb` between two such locals took the
// string alias-reassign retain: a __fern_rc_inc on an integer, which faults
// for every value but the immortal sentinel -1. coreutils/lib/ld's `add`
// computes exactly that shape (a value's exponent plus its trailing zeros,
// then `if (eb < e) { e = eb; }`), which crashed the self-host numfmt on
// every fractional input.
//
// Each program exits 0 when the arithmetic is right; a surviving retain is a
// SIGSEGV (exit 139), never a wrong answer, so the exit code is the whole
// verdict.
var intBinopReassignCases = []struct {
	name string
	src  string
}{
	{"i32-plus-const", `
function probe(ae: i32, be: i32): i32 {
    var e: i32 = ae + 1;
    var eb: i32 = be + 1;
    if (eb < e) {
        e = eb;
    }
    return e;
}
function main(): i32 {
    if (probe(0 - 63, 0 - 65) != 0 - 64) { return 1; }
    if (probe(10, 20) != 11) { return 2; }
    return 0;
}`},
	{"field-plus-call", `
struct V { neg: boolean, kind: i32, hi: u64, lo: u64, e: i32 }
function tz(hi: u64, lo: u64): i32 {
    if (lo != 0 as u64) {
        return __ctz64(lo);
    }
    return 64 + __ctz64(hi);
}
function probe(a: V, b: V): i32 {
    var e: i32 = a.e + tz(a.hi, a.lo);
    var eb: i32 = b.e + tz(b.hi, b.lo);
    if (eb < e) {
        e = eb;
    }
    return e;
}
function main(): i32 {
    var a: V = V { neg: false, kind: 0, hi: 0 as u64, lo: 9223372036854775808 as u64, e: 0 - 63 };
    var b: V = V { neg: false, kind: 0, hi: 0 as u64, lo: 9223372036854775808 as u64, e: 0 - 65 };
    if (probe(a, b) != 0 - 2) { return 1; }
    return 0;
}`},
	// The string concat the credit exists for still takes it, and the alias
	// reassign between two of them still balances: no leak, no over-release.
	{"string-concat-still-credited", `
function main(): i32 {
    var s: string = "ab" + "cd";
    var t: string = "x" + "y";
    if (s.len() < 10) {
        t = s;
    }
    if (t != "abcd") { return 1; }
    if (__rc_underflow_count() != 0) { return 99; }
    return 0;
}`},
}

func TestSelfHostIntBinopReassignX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	src, err := os.ReadFile("../../examples/self_host/asm_run.fern")
	if err != nil {
		t.Fatalf("read asm_run.fern: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "asm_run.fern"), src, 0o644); err != nil {
		t.Fatalf("write asm_run.fern: %v", err)
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")

	for _, tc := range intBinopReassignCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, gcc, runner, driverBin, []byte(tc.src+"\n"))
			if len(asm) == 0 {
				t.Fatal("self-host compiler emitted 0 bytes")
			}
			bin := buildBin(t, gcc, dir, tc.name, string(asm))
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(bin)
			} else {
				cmd = exec.Command(runner[0], append(runner[1:], bin)...)
			}
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != 0 {
				t.Errorf("%s = %d, want 0 (139 = the integer local took a string retain; 99 = over-release)", tc.name, code)
			}
		})
	}
}
