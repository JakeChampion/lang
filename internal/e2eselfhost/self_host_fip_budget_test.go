package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The self-host COMPILE PATH enforces the `fip` / `fbip` allocation budget
// (#9623).
//
// `irfipverify.fern` has implemented E068 since #6639, and until now only the
// diagnostic drivers called it — `fern.fern` ran no IR verification at all. So
// a claim the self-host accepted had been checked for SHAPE (E053) and never
// for what the lowering actually emitted, and an un-paired construction
// compiled silently where native reported E068. That is a missing diagnostic
// rather than a miscompile, which is the right severity to fix it at and the
// wrong one to leave it at.
//
// #9602 is what made it pressing: admitting the constructor shape in every
// tier means bare `fip` now leans on this check too, exactly as `fbip` always
// has.
//
// The check rides `ircore.lower_gated`, the fused gate every whole-program
// emit path already runs, so it reads the very lowering the emit will use and
// costs no second pass. A function carrying neither annotation returns before
// a single op is examined, which is nearly every function in nearly every
// module.
const (
	fipUnpairedFbipSrc = `struct State { count: i32, total: i64 }
fbip function make(n: i32): State {
	return State { count: n, total: 0 as i64 };
}
function main(): i32 { return make(3).count; }
`
	fipUnpairedFipSrc = `struct State { count: i32, total: i64 }
fip function make(n: i32): State {
	return State { count: n, total: 0 as i64 };
}
function main(): i32 { return make(3).count; }
`
	// The rebuild reuses the box it was handed, so the claim holds and the
	// module compiles. Without this case the test would pass just as well
	// against a compiler that refused every constructor, which is the bug
	// #9602 fixed.
	fipPairedSrc = `struct State { count: i32, total: i64 }
fip function bump(own s: State): State {
	return State { ...s, count: s.count + 1 };
}
function main(): i32 { var s: State = bump(State { count: 0, total: 0 as i64 }); return s.count; }
`
	// A graded claim buys exactly the fresh site it names, so the same body
	// the bare claim is refused for is accepted here. This is what keeps the
	// refusal from being a blanket one.
	fipGradedSrc = `struct State { count: i32, total: i64 }
fip(1) function make(n: i32): State {
	return State { count: n, total: 0 as i64 };
}
function main(): i32 { return make(3).count; }
`
	// An `own` struct with an ARRAY field, rebuilt by a spread on two
	// branches. The self-host found no donor for this shape and reported
	// E068 where native pairs both sites (#9700); examples/fip/
	// event_loop_fbip.fern, below, is the program that found it.
	fipArrayFieldSpreadSrc = `struct S { xs: i64[], n: i32 }
fbip function two(own s: S, k: i32): S {
	if (k > 0) { return S { ...s, n: s.n + k }; }
	return S { ...s, n: s.n - 1 };
}
function main(): i32 { var s: S = two(S { xs: [1, 2], n: 0 }, 1); return s.n; }
`
	fipNoClaimSrc = `struct State { count: i32, total: i64 }
function make(n: i32): State {
	return State { count: n, total: 0 as i64 };
}
function main(): i32 { return make(3).count; }
`
	// `xs.map(f)` on an `own` array passes E053 on both compilers (#9733).
	// Native writes it through the donor (R7) and the claim holds there;
	// this compiler has no in-place map, so its E068 says so rather than
	// letting an annotation it cannot honour read as a guarantee. The
	// element is i32 so the same source reaches E068 on the wasm leg too:
	// the wasm route refuses a combinator handed a function over a 64-bit
	// element before any claim is examined (#9838).
	fipOwnedMapSrc = `import "std/array";
fip function dbl(x: i32): i32 { return x * 2; }
fip function twice(own xs: i32[]): i32[] { return xs.map((x: i32): i32 => dbl(x)); }
function main(): i32 { return twice([1, 2]).len(); }
`
)

func TestSelfHostCompilePathEnforcesFipBudget(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("CLI driver test runs only natively (argv paths)")
	}
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "fern.fern")
	fernBin := buildSelfHostBin(t, gcc, dir, "fern.fern", "fern")

	stdlibRoot, err := filepath.Abs("../../internal/stdlib")
	if err != nil {
		t.Fatalf("abs stdlib root: %v", err)
	}

	compile := func(t *testing.T, target, src string) (string, int) {
		t.Helper()
		caseDir := t.TempDir()
		srcPath := filepath.Join(caseDir, "main.fern")
		if err := os.WriteFile(srcPath, []byte(src), 0o644); err != nil {
			t.Fatalf("write src: %v", err)
		}
		cmd := exec.Command(fernBin, "-target", target,
			"-o", filepath.Join(caseDir, "out"), srcPath, stdlibRoot)
		out, _ := cmd.CombinedOutput()
		return string(out), cmd.ProcessState.ExitCode()
	}

	// One emitter per target, one hook each, and the refusal is a compile-time
	// diagnostic — so the target is what varies and nothing here runs the
	// output. Without the arm64 and wasm legs, deleting either emitter's hook
	// left CI green (#9655).
	for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
		t.Run(target, func(t *testing.T) {
			for _, tc := range []struct{ name, src, kw string }{
				{"unpaired fbip", fipUnpairedFbipSrc, "`fbip` function"},
				{"unpaired fip", fipUnpairedFipSrc, "`fip` function"},
				{"owned map without an in-place shape", fipOwnedMapSrc, "no in-place map"},
			} {
				t.Run(tc.name, func(t *testing.T) {
					out, code := compile(t, target, tc.src)
					if code == 0 {
						t.Fatalf("compiled an un-paired construction, want E068\n%s", out)
					}
					if !strings.Contains(out, "E068") {
						t.Errorf("refusal is not E068:\n%s", out)
					}
					// The keyword must be the one the author wrote: a `fip` body
					// told it violated `fbip` names a claim it did not make.
					if !strings.Contains(out, tc.kw) {
						t.Errorf("refusal does not name %s:\n%s", tc.kw, out)
					}
					// And it must name the construct, not just the function, or
					// the author has nothing to act on in a body with several.
					if !strings.Contains(out, "un-reused allocation site") {
						t.Errorf("refusal does not name the un-paired site:\n%s", out)
					}
				})
			}

			eventLoop, err := os.ReadFile("../../examples/fip/event_loop_fbip.fern")
			if err != nil {
				t.Fatalf("read event_loop_fbip: %v", err)
			}
			for _, tc := range []struct{ name, src string }{
				{"paired rebuild", fipPairedSrc},
				{"paired array-field spread", fipArrayFieldSpreadSrc},
				{"event_loop_fbip", string(eventLoop)},
				{"graded claim", fipGradedSrc},
				// Guard against the check becoming a blanket refusal of
				// anything that allocates: an unannotated function may
				// construct freely.
				{"no claim, no budget", fipNoClaimSrc},
			} {
				t.Run(tc.name, func(t *testing.T) {
					out, code := compile(t, target, tc.src)
					if code != 0 {
						t.Fatalf("a claim that holds was refused (exit %d):\n%s", code, out)
					}
					if strings.Contains(out, "E068") {
						t.Errorf("E068 reported for a claim that holds:\n%s", out)
					}
				})
			}
		})
	}
}

// The per-unit and per-module emit paths re-lower outside `lower_gated`, so
// they held a claim to no budget at all until the check moved to the emit
// entries (#9655). These are the drivers the staged and bootstrap flows use,
// which is exactly where an unverified claim would go unnoticed.
func TestSelfHostPerUnitEmitEnforcesFipBudget(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostFiles(t, dir, "asm_arm64_ir.fern", "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "airun")

	// `-ir-unit entry|lib` is asm_ir.emit_module_ir_unit, the per-module emit
	// with no gate pass in front of it; the source arrives on stdin.
	for _, unit := range []string{"entry", "lib"} {
		t.Run(unit, func(t *testing.T) {
			var cmd *exec.Cmd
			args := []string{"-ir-unit", unit}
			if len(runner) == 0 {
				cmd = exec.Command(driverBin, args...)
			} else {
				cmd = exec.Command(runner[0], append(append(append([]string{}, runner[1:]...), driverBin), args...)...)
			}
			cmd.Stdin = bytes.NewReader([]byte(fipUnpairedFbipSrc))
			out, _ := cmd.CombinedOutput()
			if !strings.Contains(string(out), "E068") {
				t.Errorf("-ir-unit %s accepted an un-paired `fbip` claim (exit %d):\n%s",
					unit, cmd.ProcessState.ExitCode(), out)
			}
		})
	}
}

// A routing probe lowers a module only to answer a question about it, so it
// must not be killed by a claim it was merely asked about. Hooking the check
// into `lower_all_gated` made `wasm_run -decide` exit(1) on a module it was
// asked to describe, where the same driver's emit path is what should refuse
// it (#9655). Both halves are asserted here: one driver, one program, two
// invocations that must answer differently.
func TestSelfHostRoutingProbeAnswersWhereEmitRefuses(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "wasm_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_run.fern", "wasm_run")

	run := func(t *testing.T, args ...string) (string, int) {
		t.Helper()
		var cmd *exec.Cmd
		if len(runner) == 0 {
			cmd = exec.Command(driverBin, args...)
		} else {
			cmd = exec.Command(runner[0], append(append(append([]string{}, runner[1:]...), driverBin), args...)...)
		}
		cmd.Stdin = bytes.NewReader([]byte(fipUnpairedFbipSrc))
		out, _ := cmd.CombinedOutput()
		return string(out), cmd.ProcessState.ExitCode()
	}

	out, code := run(t, "-decide")
	if code != 0 {
		t.Errorf("the routing probe exited %d instead of answering:\n%s", code, out)
	}
	if strings.Contains(out, "E068") {
		t.Errorf("the routing probe refused instead of answering:\n%s", out)
	}
	if v := strings.TrimSpace(out); v != "ir" && v != "refused" {
		t.Errorf("the routing probe printed no verdict:\n%s", out)
	}

	out, code = run(t)
	if code == 0 {
		t.Errorf("the wasm emit path compiled an un-paired `fbip` claim:\n%s", out)
	}
	if !strings.Contains(out, "E068") {
		t.Errorf("the wasm emit path's refusal is not E068:\n%s", out)
	}
}
