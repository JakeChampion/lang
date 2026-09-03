package e2e

import (
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// The `-target x86-64-linux -backend ssa` path: the SSA register-allocated
// x86-64 emitter, reachable from the CLI.
//
// It was not reachable before. `internal/codegen/x86_64ssa` has had `Emit`,
// `EmitAsm` and a module-level `EmitAsmModule` for a while, with their own
// tests — but nothing selected them, so the only pipeline the CLI could run for
// x86-64 was the stack machine. That is #6979's complaint (a code-size harness
// measuring a pipeline the CLI never runs) and it is why
// docs/SSA-CUTOVER-PLAN.md's readiness table lists x86_64ssa as "unreachable
// from the CLI".
//
// Three properties, because the backend's contract has three parts.
//
//   - It AGREES with the shipping backend. A register allocator that got the
//     answer wrong would be worse than no register allocator.
//   - It is SMALLER. The whole point of #4112 is that the stack machine spills
//     every operand; if the allocated path did not emit less, there would be
//     nothing to cut over to.
//   - Outside its coverage it REFUSES CLEANLY. The subset is documented and
//     deliberate, so an unsupported construct must be a diagnostic and a
//     non-zero exit, never a miscompile and never a crash. That property is
//     what makes the subset safe to widen incrementally.
func TestX86_64SSABackendCLI(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("x86-64 backends are not exercised on windows")
	}
	if runtime.GOARCH != "amd64" {
		t.Skip("x86-64 binaries are run natively by this test; not an amd64 host")
	}
	fern := buildFernCLI(t)

	// Covered shapes: arithmetic, a cross-function call, a loop, an array index.
	// Each must compile through BOTH backends and give the same answer.
	for _, c := range []struct {
		name string
		src  string
	}{
		{"arith-and-call", `function add3(a: i32, b: i32, c: i32): i32 { return a + b * c; }
function main(): i32 { return add3(2, 3, 4) - 14; }
`},
		{"loop-accumulate", `function main(): i32 {
    var t: i32 = 0;
    var i: i32 = 0;
    while (i < 10) { t = t + i; i = i + 1; }
    return t - 45;
}
`},
		{"array-index", `function main(): i32 {
    var xs: i32[] = [3, 1, 4, 1, 5];
    var t: i32 = 0;
    var i: i32 = 0;
    while (i < xs.len()) { t = t + xs[i]; i = i + 1; }
    return t - 14;
}
`},
		// Payloadless enum variants: each is a pointer to a shared .rodata cell
		// holding the tag, so the match reads [ptr+0] the same way it reads a heap
		// box.
		{"enum-match", `enum Shape { Circle, Square }
function main(): i32 {
    var s: Shape = Shape.Square;
    match (s) { Shape.Circle => { return 1; }, Shape.Square => { return 0; } }
}
`},
		// Calls inside a loop, which is where the allocator pays most: it has no
		// call-clobber awareness, so it saves EVERY caller-saved allocatable
		// register that could hold a live-across-call value, around every call —
		// and a match brings calls with it, dropping its scrutinee through
		// __fern_rc_inc / __fern_rc_dec. That traffic (34 push/pop against the
		// stack machine's 6) used to swallow the whole win on this program and
		// leave the allocated path larger. It no longer does — 195 instructions
		// against 199 — so the smaller-than assertion below covers the shape
		// that was hardest for it. The margin is four instructions because both
		// emitters inline or call the same rc guards; a guard improvement that
		// reaches only one of them moves this case, which is what it is for.
		{"call-heavy-loop-match", `enum Shape { Circle, Square, Triangle }
function pick(n: i32): Shape {
    if (n == 0) { return Shape.Circle; }
    if (n == 1) { return Shape.Square; }
    return Shape.Triangle;
}
function main(): i32 {
    var t: i32 = 0;
    var i: i32 = 0;
    while (i < 3) {
        match (pick(i)) {
            Shape.Circle => { t = t + 1; },
            Shape.Square => { t = t + 10; },
            Shape.Triangle => { t = t + 100; }
        }
        i = i + 1;
    }
    return t - 111;
}
`},
	} {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			src := mustWrite(t, dir, "prog.fern", c.src)

			baseBin := filepath.Join(dir, "base")
			if out, err := exec.Command(fern, "-target", "x86-64-linux", "-o", baseBin, src).CombinedOutput(); err != nil {
				t.Fatalf("shipping backend failed to build: %v\n%s", err, out)
			}
			ssaBin := filepath.Join(dir, "ssa")
			if out, err := exec.Command(fern, "-target", "x86-64-linux", "-backend", "ssa", "-o", ssaBin, src).CombinedOutput(); err != nil {
				t.Fatalf("-backend ssa failed to build a covered program: %v\n%s", err, out)
			}

			baseOut, baseCode := runBin(exec.Command(baseBin), "")
			ssaOut, ssaCode := runBin(exec.Command(ssaBin), "")
			if baseCode != ssaCode || baseOut != ssaOut {
				t.Errorf("backends disagree: shipping exit=%d out=%q, ssa exit=%d out=%q",
					baseCode, baseOut, ssaCode, ssaOut)
			}

			// And the allocated path must be the smaller one. Both emitters
			// write GAS text to stdout when no -o is given, so this counts the
			// same thing on both sides — indented lines, one per instruction.
			baseAsm := mustEmitAsm(t, fern, src, false)
			ssaAsm := mustEmitAsm(t, fern, src, true)
			baseN, ssaN := countAsmInstructions(baseAsm), countAsmInstructions(ssaAsm)
			if ssaN >= baseN {
				t.Errorf("SSA emitted %d instructions against the stack machine's %d — "+
					"the register allocator is supposed to delete the operand-stack traffic (#4112)", ssaN, baseN)
			}
		})
	}

	// The coverage endpoints. Two shapes are outside the subset for different
	// reasons, and BOTH must refuse with a diagnostic naming the backend — that
	// is the property that makes the subset safe to widen one slice at a time.
	//
	//   - A float reinterpret is refused during instruction selection, by name.
	//   - A program needing a runtime helper the emitter has no body for used to
	//     get all the way to the assembler and die on `undefined label
	//     "fn___fern_drop_arr_str"`, which names neither the backend nor the
	//     coverage gap. checkNoDanglingCalls now catches it at emit time.
	for _, c := range []struct {
		name string
		src  string
		want string // a fragment the diagnostic must carry
	}{
		// f64_bits, on a value the constant folder cannot see through — a literal
		// argument is folded in the IR and never reaches the backend at all.
		{"float-reinterpret", `function widen(n: i32): f64 { return n as f64 + 0.5; }
function main(): i32 {
    var b: i64 = f64_bits(widen(3));
    return (b % 7) as i32;
}
`, "reinterpret_f64_to_i64"},
		{"missing-runtime-helper", `function main(): i32 {
    var xs: string[] = ["a", "b"];
    return xs.len() as i32;
}
`, "call target(s) the module never defines"},
	} {
		t.Run("uncovered-refuses-cleanly/"+c.name, func(t *testing.T) {
			dir := t.TempDir()
			src := mustWrite(t, dir, "prog.fern", c.src)
			out, err := exec.Command(fern, "-target", "x86-64-linux", "-backend", "ssa", "-o", filepath.Join(dir, "out"), src).CombinedOutput()
			if err == nil {
				t.Skipf("%s now compiles through x86-64 SSA — fold this case into the covered table above", c.name)
			}
			if !strings.Contains(string(out), "x86-64/ssa:") {
				t.Errorf("an uncovered construct must refuse with a diagnostic naming the backend, got:\n%s", out)
			}
			if !strings.Contains(string(out), c.want) {
				t.Errorf("the diagnostic must say what is missing (%q), got:\n%s", c.want, out)
			}
			if strings.Contains(string(out), "undefined label") {
				t.Errorf("a coverage gap must be refused by the backend, not discovered by the assembler:\n%s", out)
			}
			if strings.Contains(string(out), "signal:") || strings.Contains(string(out), "panic:") {
				t.Errorf("an uncovered construct must REFUSE, not crash:\n%s", out)
			}
		})
	}
}

// mustEmitAsm returns the GAS text a backend writes to stdout for src. Both the
// shipping x86-64 emitter and the SSA one write assembly when no -o is given.
func mustEmitAsm(t *testing.T, fern, src string, ssa bool) string {
	t.Helper()
	args := []string{"-target", "x86-64-linux"}
	if ssa {
		args = append(args, "-backend", "ssa")
	}
	args = append(args, src)
	out, err := exec.Command(fern, args...).Output()
	if err != nil {
		t.Fatalf("emit asm (ssa=%v): %v", ssa, err)
	}
	return string(out)
}

// countAsmInstructions counts indented lines — one per machine instruction,
// with labels and directives at column 0. The same measure scripts/perf-bench
// and scripts/perf-bench-selfhost use, so the numbers are comparable to the
// checked-in baselines.
func countAsmInstructions(asm string) int {
	n := 0
	for _, line := range strings.Split(asm, "\n") {
		if line != "" && (line[0] == ' ' || line[0] == '\t') {
			n++
		}
	}
	return n
}
