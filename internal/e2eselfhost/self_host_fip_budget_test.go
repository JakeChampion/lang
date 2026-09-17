package e2eselfhost

import (
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

	compile := func(t *testing.T, src string) (string, int) {
		t.Helper()
		caseDir := t.TempDir()
		srcPath := filepath.Join(caseDir, "main.fern")
		if err := os.WriteFile(srcPath, []byte(src), 0o644); err != nil {
			t.Fatalf("write src: %v", err)
		}
		cmd := exec.Command(fernBin, "-target", "x86-64-linux",
			"-o", filepath.Join(caseDir, "out"), srcPath, stdlibRoot)
		out, _ := cmd.CombinedOutput()
		return string(out), cmd.ProcessState.ExitCode()
	}

	for _, tc := range []struct{ name, src, kw string }{
		{"unpaired fbip", fipUnpairedFbipSrc, "`fbip` function"},
		{"unpaired fip", fipUnpairedFipSrc, "`fip` function"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out, code := compile(t, tc.src)
			if code == 0 {
				t.Fatalf("compiled an un-paired construction, want E068\n%s", out)
			}
			if !strings.Contains(out, "E068") {
				t.Errorf("refusal is not E068:\n%s", out)
			}
			// The keyword must be the one the author wrote: a `fip` body told
			// it violated `fbip` names a claim it did not make.
			if !strings.Contains(out, tc.kw) {
				t.Errorf("refusal does not name %s:\n%s", tc.kw, out)
			}
			// And it must name the construct, not just the function, or the
			// author has nothing to act on in a body with several.
			if !strings.Contains(out, "un-reused allocation site") {
				t.Errorf("refusal does not name the un-paired site:\n%s", out)
			}
		})
	}

	for _, tc := range []struct{ name, src string }{
		{"paired rebuild", fipPairedSrc},
		{"graded claim", fipGradedSrc},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out, code := compile(t, tc.src)
			if code != 0 {
				t.Fatalf("a claim that holds was refused (exit %d):\n%s", code, out)
			}
			if strings.Contains(out, "E068") {
				t.Errorf("E068 reported for a claim that holds:\n%s", out)
			}
		})
	}

	// Guard against the check becoming a blanket refusal of anything that
	// allocates: an unannotated function may construct freely.
	t.Run("no claim, no budget", func(t *testing.T) {
		out, code := compile(t, `struct State { count: i32, total: i64 }
function make(n: i32): State {
	return State { count: n, total: 0 as i64 };
}
function main(): i32 { return make(3).count; }
`)
		if code != 0 {
			t.Fatalf("an unannotated function was held to a budget (exit %d):\n%s", code, out)
		}
	})
}
