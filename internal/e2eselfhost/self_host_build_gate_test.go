package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestSelfHostBuildGateX86_64 pins the invariant #6961 restored: the self-host
// CLI's COMPILE path rejects a program its own checker rejects. Before it the
// gate ran over six codes, so `-check` reported E003 on a source that
// `-target` then compiled to a working binary, and every checker rule ported
// for parity stayed reachable only through `-check`.
//
// Both directions are the test. The gate is an exclusion list
// (`is_partial_checker_gap_code` in checker.fern), so a case either names a
// code that must now reject the build, or one of the partial-port rules that
// must NOT — a valid program drawing one of those still has to compile, which
// is the failure mode that kept the gate at six codes in the first place. A
// change that widens the exclusion list silently is what the second group
// catches; one that narrows it too far is what the first group catches.
func TestSelfHostBuildGateX86_64(t *testing.T) {
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

	cases := []struct {
		name string
		src  string
		// wantDiag non-empty ⇒ the compile must fail with it on stderr.
		// Empty ⇒ the compile must succeed, whatever `-check` says.
		wantDiag string
	}{
		{
			// The issue's own repro: assignment type error, reported by both
			// checkers, compiled clean by the self-host until the gate widened.
			name:     "assign-mismatch-E003",
			src:      "function main(): i32 { var s: string = 5; return 0; }\n",
			wantDiag: "error[E003]",
		},
		{
			name:     "wildcard-arm-not-last-E026",
			src:      "enum O { Sm(i32), Nn }\nfunction main(): i32 { var o: O = O.Nn; match (o) { _ => { return 1; }, Nn => { return 3; } } }\n",
			wantDiag: "error[E026]",
		},
		{
			name:     "variant-covered-twice-E028",
			src:      "enum O { Sm(i32), Nn }\nfunction main(): i32 { var o: O = O.Sm(1); match (o) { Sm(a) => { return a; }, Sm(b) => { return b; }, Nn => { return 3; } } }\n",
			wantDiag: "error[E028]",
		},
		{
			// One of the six codes that gated before this change, so the
			// widening cannot be read as having replaced the old set.
			name:     "field-assign-E048",
			src:      "struct P { x: i32 }\nfunction main(): i32 { var p: P = P { x: 1 }; p.x = 5; return p.x; }\n",
			wantDiag: "error[E048]",
		},
		{
			// A valid i64 program compiles. This drew a spurious E043 when the
			// checker ignored integer width, which is why E043 was excluded;
			// #7011 closed that, and both checkers are now silent here. The
			// case stays as the regression guard for the width rule.
			name:     "i64-program-compiles",
			src:      "import \"std/i64\";\nfunction main(): i32 { var a: i64 = 9i64; var b: i64 = 3i64; return (a / b) as i32; }\n",
			wantDiag: "",
		},
		{
			// E064 is excluded: the partial checker does not know every stdlib
			// type, so "unknown type" fires on valid imports — including on the
			// compiler's own sources, which is where gating it breaks the
			// fixpoint.
			name:     "excluded-unknown-stdlib-type-E064",
			src:      "import \"std/io\";\nfunction main(): i32 { var r: i32 = 0; return r; }\n",
			wantDiag: "",
		},
		{
			// The uncoded #4346 hint (`error[type]`) is a statement about what
			// this checker can model, not about the program, so it must never
			// reject a build. `is_diagnostic_code` is what keeps it out.
			name:     "uncoded-partial-checker-hint-does-not-gate",
			src:      "enum W { Wrap(i32), Er2 }\nfunction main(): i32 { var w: W = W.Er2; match (w) { Wrap(Er2) => { return 1; }, Er2 => { return 2; } } }\n",
			wantDiag: "",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			progDir := t.TempDir()
			prog := filepath.Join(progDir, "prog.fern")
			if err := os.WriteFile(prog, []byte(tc.src), 0o644); err != nil {
				t.Fatalf("write prog: %v", err)
			}
			out := filepath.Join(progDir, "prog.bin")
			cmd := exec.Command(fernBin, "-target", "x86-64-linux", "-o", out, prog, stdlibRoot)
			stderr, _ := cmd.CombinedOutput()
			code := cmd.ProcessState.ExitCode()

			if tc.wantDiag == "" {
				if code != 0 {
					t.Fatalf("valid program rejected by the build gate: exit=%d\n%s", code, stderr)
				}
				return
			}
			if code == 0 {
				t.Fatalf("ill-typed program compiled clean (exit 0); wanted %s.\n"+
					"The compile path is not gating on this code — see #6961.", tc.wantDiag)
			}
			if !strings.Contains(string(stderr), tc.wantDiag) {
				t.Errorf("exit=%d but stderr missing %q\ngot: %s", code, tc.wantDiag, stderr)
			}
		})
	}
}

// TestSelfHostBuildGateMatchesCheckX86_64 states the property behind the case
// list above: for any program, if `-check` reports a gating code then
// `-target` must refuse to build it. #6961 was exactly this property failing —
// the two modes ran the same checker and disagreed about what it meant.
func TestSelfHostBuildGateMatchesCheckX86_64(t *testing.T) {
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

	// Each source draws a gating diagnostic under `-check`. The property is
	// that the compile path agrees; the codes themselves are pinned above.
	//
	// Every source here must be one the GATE stops. An undefined call, say,
	// would pass this test without the gate existing at all — IR lowering
	// rejects it on its own — so it would prove nothing.
	srcs := []string{
		"function main(): i32 { var s: string = 5; return 0; }\n",
		"enum O { Sm(i32), Nn }\nfunction main(): i32 { var o: O = O.Sm(1); match (o) { Sm(a) => { return a; }, Sm(b) => { return b; }, Nn => { return 3; } } }\n",
		"struct P { x: i32 }\nfunction main(): i32 { var p: P = P { x: 1 }; p.x = 5; return p.x; }\n",
		"enum O { Sm(i32), Nn }\nfunction main(): i32 { var o: O = O.Nn; match (o) { _ => { return 1; }, Nn => { return 3; } } }\n",
	}
	for i, src := range srcs {
		progDir := t.TempDir()
		prog := filepath.Join(progDir, "prog.fern")
		if err := os.WriteFile(prog, []byte(src), 0o644); err != nil {
			t.Fatalf("write prog: %v", err)
		}
		checkCmd := exec.Command(fernBin, "-check", prog, stdlibRoot)
		checkOut, _ := checkCmd.CombinedOutput()
		if checkCmd.ProcessState.ExitCode() == 0 {
			t.Errorf("case %d: -check accepted a program the corpus says it rejects:\n%s", i, src)
			continue
		}
		buildCmd := exec.Command(fernBin, "-target", "x86-64-linux",
			"-o", filepath.Join(progDir, "prog.bin"), prog, stdlibRoot)
		buildOut, _ := buildCmd.CombinedOutput()
		if buildCmd.ProcessState.ExitCode() == 0 {
			t.Errorf("case %d: -check rejected but -target built it (#6961).\nsrc: %s-check said: %s\n-target said: %s",
				i, src, checkOut, buildOut)
		}
	}
}
