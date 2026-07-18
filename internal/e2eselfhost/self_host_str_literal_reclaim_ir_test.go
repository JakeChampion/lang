package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// Heap-bump FIXPOINTS for the self-host x86-64 IR path's LITERAL string box
// reclaim (#4357's literal-box slice). `var p: string = "...";` allocates a
// fresh 16-byte box per execution whose DATA is .rodata; the box leaked at
// scope exit (24 B/call with header, even for an UNUSED local), and
// `return "lit";` leaked the same box in every caller binding — both were
// excluded from the fresh-string classes outright. Three changes close it:
//
//   - collect_fresh_string_in_stmt admits literal inits to the "STR:" local
//     class (exit-sweep / loop-rebind free via __fern_str_free, whose
//     heap-base guard no-ops on the static data — the accumulator-init
//     contract);
//   - body_has_nonfresh_str_return admits direct literal returns, so a
//     literal-returning function joins str_fresh_ret and its caller bindings
//     (and .len() receivers, #5146) reclaim;
//   - expr_unsafe_for treats a DIRECT bare-ident binary operand as a BORROW
//     (binaries produce scalars or fresh strings; operands are only read), so
//     a literal local consumed by a return-position concat — the SSA-emit
//     WAT-template builder shape — keeps its credit.
//
// A bare-ident string return (`var p = "lit"; return p;`) is still
// conservatively non-fresh (needs per-callee move-out analysis) and keeps its
// safe-leak.
var strLiteralReclaimIRCases = []struct {
	name  string
	src   func(n string) string
	fixed bool
	want  int
}{
	// The SSA-emit shape: a literal local read by a return-position concat.
	{name: "concat-operand-local", src: func(n string) string {
		return `function f(k: i32): string {
    var p: string = "abcdefgh";
    return "(func x" + p + " (param i32)" + p + " i32.add)" + p;
}
function main(): i32 {
    var before: i32 = __heap_bump_bytes();
    var i: i32 = 0;
    var acc: i32 = 0;
    while (i < ` + n + `) { var s: string = f(i); acc = acc + s.len(); i = i + 1; }
    if (acc != ` + n + ` * 52) { return 121; }
    var g: i32 = __heap_bump_bytes() - before;
    if (g > 900) { return 119; }
    return g / 8;
}`
	}},
	// An UNUSED literal local — the purest form of the box leak.
	{name: "unused-local", src: func(n string) string {
		return `function f(k: i32): i32 {
    var p: string = "never-read-literal";
    return k * 2;
}
function main(): i32 {
    var before: i32 = __heap_bump_bytes();
    var i: i32 = 0;
    var acc: i32 = 0;
    while (i < ` + n + `) { acc = acc + f(i); i = i + 1; }
    if (acc != ` + n + ` * (` + n + ` - 1)) { return 121; }
    var g: i32 = __heap_bump_bytes() - before;
    if (g > 900) { return 119; }
    return g / 8;
}`
	}},
	// A literal RETURN: the callee's box moves to the caller binding, which
	// now reclaims it (f joins str_fresh_ret).
	{name: "literal-return", src: func(n string) string {
		return `function f(k: i32): string {
    return "a-literal-return-payload";
}
function main(): i32 {
    var before: i32 = __heap_bump_bytes();
    var i: i32 = 0;
    var acc: i32 = 0;
    while (i < ` + n + `) { var s: string = f(i); acc = acc + s.len(); i = i + 1; }
    if (acc != ` + n + ` * 24) { return 121; }
    var g: i32 = __heap_bump_bytes() - before;
    if (g > 900) { return 119; }
    return g / 8;
}`
	}},
	// Aliasing stays excluded: `var q = p` disqualifies p (bare-ident init is
	// still an escape), and the borrowed concat reads leave p/q value-intact.
	{name: "alias-negative", fixed: true, want: 0, src: func(string) string {
		return `function main(): i32 {
    var p: string = "shared-literal-content";
    var q: string = p;
    var i: i32 = 0;
    var acc: i32 = 0;
    while (i < 2000) {
        var t: string = "x-" + p + "-y";
        acc = acc + t.len() + q.len();
        i = i + 1;
    }
    if (acc != 2000 * (26 + 22)) { return 121; }
    if (p.len() != 22 || q.len() != 22) { return 122; }
    return 0;
}`
	}},
}

// TestSelfHostStrLiteralReclaimIRX86_64 runs the shapes through the
// self-hosted x86-64 IR driver (asm_run). Fixpoint cases assert growth(N=50)
// == growth(N=5000), non-zero, under the leak guard; the fixed case asserts
// its exact exit (121/122 = value corruption, 119 = leak guard).
func TestSelfHostStrLiteralReclaimIRX86_64(t *testing.T) {
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

	sh := func(t *testing.T, tag, prog string) int {
		t.Helper()
		asm := runCapture(t, gcc, runner, driverBin, []byte(prog+"\n"))
		if len(asm) == 0 {
			t.Fatalf("%s: self-host compiler emitted 0 bytes", tag)
		}
		progBin := buildBin(t, gcc, dir, tag, string(asm))
		var cmd *exec.Cmd
		if len(runner) == 0 {
			cmd = exec.Command(progBin)
		} else {
			cmd = exec.Command(runner[0], append(runner[1:], progBin)...)
		}
		_ = cmd.Run()
		return cmd.ProcessState.ExitCode()
	}

	for _, tc := range strLiteralReclaimIRCases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.fixed {
				if code := sh(t, tc.name, tc.src("")); code != tc.want {
					t.Errorf("%s: exited %d, want %d (121/122=value corruption, 119=leak guard)", tc.name, code, tc.want)
				}
				return
			}
			small := sh(t, tc.name+"-50", tc.src("50"))
			large := sh(t, tc.name+"-5000", tc.src("5000"))
			if small != large {
				t.Errorf("%s: high-water not bounded (N=50 -> %d, N=5000 -> %d)", tc.name, small, large)
			}
			if small >= 119 {
				t.Errorf("%s: leak guard tripped (%d)", tc.name, small)
			}
		})
	}
}
