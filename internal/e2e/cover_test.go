package e2e

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/ast"
	"github.com/jakechampion/lang/internal/checker"
	arm64codegen "github.com/jakechampion/lang/internal/codegen/arm64"
	"github.com/jakechampion/lang/internal/codegen/x86_64"
	"github.com/jakechampion/lang/internal/constfold"
	"github.com/jakechampion/lang/internal/modload"
	"github.com/jakechampion/lang/internal/monomorph"
)

// --- Line coverage, `fern -cover` (#5548) ------------------------------
//
// ast.CoverEnabled instruments every executable source line with a counter
// and dumps the whole table to stderr at BOTH exit seams (the _start
// epilogue and the exit() builtin's __fern_exit):
//
//	fern-cover: <file>:<line> <count>
//
// The distinction these pin is the one the feature exists for: a line the
// run never reached reports 0 while a line it reached reports its hit
// count, on both natives, with the program's own stdout and exit code
// untouched. A report that said 0 everywhere, or omitted the zeros
// entirely, would look like a working coverage tool and measure nothing.

// coverSrc is one taken branch, one untaken branch, one loop body run
// three times, and one function nothing calls.
const coverSrc = `function classify(n: i32): i32 {
    if (n > 10) {
        return 1;
    }
    return 2;
}
function never_called(n: i32): i32 {
    return n * 3;
}
function main(): i32 {
    var i: i32 = 0;
    while (i < 3) {
        i = i + 1;
    }
    print("ran");
    return classify(1) - 2;
}
`

// emitCover compiles src with ast.CoverEnabled toggled per the flag,
// returning the asm text and the entry file's path (the coverage report
// names it, so a test that asserts on lines has to know it).
func emitCover(t *testing.T, backend, src string, cover bool) (string, string) {
	t.Helper()
	dir := t.TempDir()
	srcPath := filepath.Join(dir, "main.fern")
	if err := os.WriteFile(srcPath, []byte(src), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	prog, _, err := modload.Load(srcPath)
	if err != nil {
		t.Fatalf("modload: %v", err)
	}
	if err := constfold.Fold(prog, nil); err != nil {
		t.Fatalf("constfold: %v", err)
	}
	info, err := checker.Check(prog)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if err := monomorph.Run(prog, info); err != nil {
		t.Fatalf("monomorph: %v", err)
	}
	prev := ast.CoverEnabled
	t.Cleanup(func() { ast.CoverEnabled = prev })
	ast.CoverEnabled = cover
	var asm string
	var emitErr error
	if backend == "arm64-linux" {
		asm, emitErr = arm64codegen.Emit(prog, info)
	} else {
		asm, emitErr = x86_64.Emit(prog, info)
	}
	ast.CoverEnabled = prev
	if emitErr != nil {
		t.Fatalf("%s emit: %v", backend, emitErr)
	}
	return asm, srcPath
}

// runCoverX86_64 compiles src flag-on and runs it, returning stdout,
// stderr, the exit code, and the entry path. The report contract is
// "stderr only, stdout untouched", so combined output won't do.
func runCoverX86_64(t *testing.T, src string) (string, string, int, string) {
	t.Helper()
	gcc, runner := x86_64Tooling(t)
	asm, srcPath := emitCover(t, "x86_64", src, true)
	dir := t.TempDir()
	asmPath := filepath.Join(dir, "prog.s")
	binPath := filepath.Join(dir, "prog")
	if err := os.WriteFile(asmPath, []byte(asm), 0o644); err != nil {
		t.Fatalf("write asm: %v", err)
	}
	if out, err := exec.Command(gcc, "-static", "-nostdlib", "-no-pie", asmPath, "-o", binPath).CombinedOutput(); err != nil {
		t.Fatalf("gcc: %v\n%s", err, out)
	}
	var cmd *exec.Cmd
	if len(runner) == 0 {
		cmd = exec.Command(binPath)
	} else {
		cmd = exec.Command(runner[0], append(append([]string{}, runner[1:]...), binPath)...)
	}
	stdout, stderr, code := runSplit(t, cmd)
	return stdout, stderr, code, srcPath
}

// runCoverArm64 is the arm64 sibling (qemu; SKIPs without the aarch64
// toolchain — rides CI).
func runCoverArm64(t *testing.T, src string) (string, string, int, string) {
	t.Helper()
	gcc, qemu := arm64Tooling(t)
	asm, srcPath := emitCover(t, "arm64-linux", src, true)
	dir := t.TempDir()
	asmPath := filepath.Join(dir, "prog.s")
	binPath := filepath.Join(dir, "prog")
	if err := os.WriteFile(asmPath, []byte(asm), 0o644); err != nil {
		t.Fatalf("write asm: %v", err)
	}
	if out, err := exec.Command(gcc, "-static", "-nostdlib", asmPath, "-o", binPath).CombinedOutput(); err != nil {
		t.Fatalf("gcc: %v\n%s", err, out)
	}
	stdout, stderr, code := runSplit(t, runArm64Bin(qemu, binPath))
	return stdout, stderr, code, srcPath
}

var coverLineRe = regexp.MustCompile(`^` + regexp.QuoteMeta(ast.CoverLinePrefix) + `(.*):(\d+) (\d+)$`)

// parseCoverLines folds a run's stderr into line → hit count, and fails
// if a report line names a file other than the entry.
func parseCoverLines(t *testing.T, stderr, srcPath string) map[int]uint64 {
	t.Helper()
	hits := map[int]uint64{}
	for _, raw := range strings.Split(stderr, "\n") {
		if !strings.HasPrefix(raw, ast.CoverLinePrefix) {
			continue
		}
		m := coverLineRe.FindStringSubmatch(raw)
		if m == nil {
			t.Fatalf("unparseable report line %q", raw)
		}
		if m[1] != srcPath {
			t.Fatalf("report line %q names %q, want the entry %q", raw, m[1], srcPath)
		}
		line, err := strconv.Atoi(m[2])
		if err != nil {
			t.Fatalf("report line %q: bad line number: %v", raw, err)
		}
		n, err := strconv.ParseUint(m[3], 10, 64)
		if err != nil {
			t.Fatalf("report line %q: bad count: %v", raw, err)
		}
		if _, dup := hits[line]; dup {
			t.Errorf("line %d reported twice — one counter per line is the whole denominator", line)
		}
		hits[line] = n
	}
	return hits
}

// assertCoverReport is the shared body of the two backends' checks: the
// counts the coverSrc program's control flow forces, plus the "stdout and
// exit code untouched" half of the contract.
func assertCoverReport(t *testing.T, stdout, stderr string, code int, srcPath string) {
	t.Helper()
	if stdout != "ran\n" {
		t.Errorf("stdout = %q, want %q — the report goes to stderr only", stdout, "ran\n")
	}
	if code != 0 {
		t.Errorf("exit code = %d, want 0 — instrumentation must not change the program's result", code)
	}
	hits := parseCoverLines(t, stderr, srcPath)
	if len(hits) == 0 {
		t.Fatalf("no report lines in stderr:\n%s", stderr)
	}
	// Line 3 is `return 1;`, guarded by `n > 10` on a call with n == 1.
	if got, ok := hits[3]; !ok {
		t.Errorf("line 3 (the untaken `return 1;`) has no row — an unreached line must still be counted, as 0")
	} else if got != 0 {
		t.Errorf("line 3 (untaken branch) reports %d hits, want 0", got)
	}
	// Line 8 is `return n * 3;` inside never_called.
	if got, ok := hits[8]; !ok {
		t.Errorf("line 8 (inside a function nothing calls) has no row — never called is exactly what a report must say")
	} else if got != 0 {
		t.Errorf("line 8 (never-called function body) reports %d hits, want 0", got)
	}
	// Line 5 is `return 2;`, reached once through classify(1).
	if got := hits[5]; got != 1 {
		t.Errorf("line 5 (the taken `return 2;`) reports %d hits, want 1", got)
	}
	// Line 13 is `i = i + 1;`, the body of a three-iteration loop. A
	// counter that only recorded "reached" rather than "how often" would
	// report 1 here, which is what makes this the hit-count assertion.
	if got := hits[13]; got != 3 {
		t.Errorf("line 13 (a loop body run three times) reports %d hits, want 3", got)
	}
}

func TestCoverReportX86_64(t *testing.T) {
	stdout, stderr, code, srcPath := runCoverX86_64(t, coverSrc)
	assertCoverReport(t, stdout, stderr, code, srcPath)
}

func TestCoverReportArm64(t *testing.T) {
	stdout, stderr, code, srcPath := runCoverArm64(t, coverSrc)
	assertCoverReport(t, stdout, stderr, code, srcPath)
}

// exit() leaves through __fern_exit, not the _start epilogue. Report there
// too, or a program that ends with an explicit exit — every CLI, and the
// TAP runner — measures nothing.
const coverExitSrc = `function main(): i32 {
    print("bye");
    exit(3);
    return 0;
}
`

func TestCoverReportRunsAtTheExitBuiltinX86_64(t *testing.T) {
	stdout, stderr, code, srcPath := runCoverX86_64(t, coverExitSrc)
	if stdout != "bye\n" {
		t.Errorf("stdout = %q, want %q", stdout, "bye\n")
	}
	if code != 3 {
		t.Errorf("exit code = %d, want 3 — the report must not eat exit()'s status", code)
	}
	hits := parseCoverLines(t, stderr, srcPath)
	if got := hits[2]; got != 1 {
		t.Errorf("line 2 reports %d hits, want 1; stderr:\n%s", got, stderr)
	}
	// Line 4 is after the exit() call and is never reached.
	if got, ok := hits[4]; ok && got != 0 {
		t.Errorf("line 4 (past exit()) reports %d hits, want 0", got)
	}
}

func TestCoverReportRunsAtTheExitBuiltinArm64(t *testing.T) {
	stdout, stderr, code, srcPath := runCoverArm64(t, coverExitSrc)
	if stdout != "bye\n" {
		t.Errorf("stdout = %q, want %q", stdout, "bye\n")
	}
	if code != 3 {
		t.Errorf("exit code = %d, want 3 — the report must not eat exit()'s status", code)
	}
	hits := parseCoverLines(t, stderr, srcPath)
	if got := hits[2]; got != 1 {
		t.Errorf("line 2 reports %d hits, want 1; stderr:\n%s", got, stderr)
	}
	if got, ok := hits[4]; ok && got != 0 {
		t.Errorf("line 4 (past exit()) reports %d hits, want 0", got)
	}
}

// The cheap proxy for "an ordinary build is byte-identical to one from a
// compiler without the feature": a flag-off build mentions no coverage
// symbol at all, on either native.
func TestCoverFlagOffEmitsNoCoverageSymbols(t *testing.T) {
	for _, backend := range []string{"x86_64", "arm64-linux"} {
		asm, _ := emitCover(t, backend, coverSrc, false)
		for _, sym := range []string{"__fern_cov_counters", "__fern_cov_table", "__fern_cov_report", ast.CoverLinePrefix} {
			if strings.Contains(asm, sym) {
				t.Errorf("%s: flag-off build mentions %q", backend, sym)
			}
		}
	}
}

// And the flag-on build carries all three — a gate that only checked the
// off side would pass on a feature that never emitted anything.
func TestCoverFlagOnEmitsCoverageSymbols(t *testing.T) {
	for _, backend := range []string{"x86_64", "arm64-linux"} {
		asm, srcPath := emitCover(t, backend, coverSrc, true)
		for _, sym := range []string{"__fern_cov_counters", "__fern_cov_table", "__fern_cov_report"} {
			if !strings.Contains(asm, sym) {
				t.Errorf("%s: flag-on build is missing %q", backend, sym)
			}
		}
		// The report text is baked into .rodata, so the untaken branch's
		// line is nameable in the asm itself.
		if want := fmt.Sprintf("%s%s:3 ", ast.CoverLinePrefix, srcPath); !strings.Contains(asm, want) {
			t.Errorf("%s: flag-on build has no report literal for line 3 (%q)", backend, want)
		}
	}
}

// --- Branch coverage, slice 3 (#5548) ----------------------------------
//
// The whole point is the case line coverage reports as covered while an
// edge never ran. coverBranchSrc is built around three of them: a
// one-armed `if` whose condition is always false (no `else` line exists
// to report 0), a `while` whose body never runs (the `while` line itself
// still reports a hit), and an `&&` whose right operand is never reached
// (both operands sit on the condition's line).

const coverBranchSrc = `function classify(n: i32): i32 {
    if (n > 10) {
        return 1;
    }
    return 2;
}
function guard(a: i32, b: i32): i32 {
    if (a > 0 && b > 0) { return 1; }
    return 0;
}
function main(): i32 {
    var i: i32 = 0;
    while (i < 3) { i = i + 1; }
    while (i > 99) { i = i + 1; }
    print("ran");
    return classify(1) + guard(0, 5) - 2;
}
`

// coverEdges is one conditional's two counters as the report states them.
type coverEdges struct{ eval, taken uint64 }

var coverBranchRe = regexp.MustCompile(`^` + regexp.QuoteMeta(ast.CoverBranchPrefix) + `(.*):(\d+):(\d+) ([ET]) (\d+)$`)

// parseCoverBranches folds a run's stderr into (line, col) → edges, and
// fails if a branch row names a file other than the entry.
func parseCoverBranches(t *testing.T, stderr, srcPath string) map[[2]int]*coverEdges {
	t.Helper()
	out := map[[2]int]*coverEdges{}
	for _, raw := range strings.Split(stderr, "\n") {
		if !strings.HasPrefix(raw, ast.CoverBranchPrefix) {
			continue
		}
		m := coverBranchRe.FindStringSubmatch(raw)
		if m == nil {
			t.Fatalf("unparseable branch row %q", raw)
		}
		if m[1] != srcPath {
			t.Fatalf("branch row %q names %q, want the entry %q", raw, m[1], srcPath)
		}
		line, _ := strconv.Atoi(m[2])
		col, _ := strconv.Atoi(m[3])
		n, err := strconv.ParseUint(m[5], 10, 64)
		if err != nil {
			t.Fatalf("branch row %q: bad count: %v", raw, err)
		}
		k := [2]int{line, col}
		e := out[k]
		if e == nil {
			e = &coverEdges{}
			out[k] = e
		}
		if m[4] == "E" {
			e.eval = n
		} else {
			e.taken = n
		}
	}
	return out
}

// edgesAt returns the conditional recorded at a line, failing when the
// line holds none or several — the tests below name a line each.
func edgesAt(t *testing.T, edges map[[2]int]*coverEdges, line int) *coverEdges {
	t.Helper()
	var found []*coverEdges
	for k, e := range edges {
		if k[0] == line {
			found = append(found, e)
		}
	}
	if len(found) != 1 {
		t.Fatalf("line %d has %d conditionals, want 1", line, len(found))
	}
	return found[0]
}

// assertCoverBranchReport is the shared body of the two backends' checks.
func assertCoverBranchReport(t *testing.T, stdout, stderr string, code int, srcPath string) {
	t.Helper()
	if stdout != "ran\n" {
		t.Errorf("stdout = %q, want %q — the report goes to stderr only", stdout, "ran\n")
	}
	if code != 0 {
		t.Errorf("exit code = %d, want 0", code)
	}
	hits := parseCoverLines(t, stderr, srcPath)
	edges := parseCoverBranches(t, stderr, srcPath)
	if len(edges) == 0 {
		t.Fatalf("no branch rows in stderr:\n%s", stderr)
	}

	// Line 2's `if (n > 10)` runs once with n == 1, so the true edge
	// never fires. There is no `else` in the source, so nothing in the
	// LINE report can say this — which is the whole reason the pair
	// exists.
	if e := edgesAt(t, edges, 2); e.eval != 1 || e.taken != 0 {
		t.Errorf("line 2 `if`: eval=%d taken=%d, want 1 and 0", e.eval, e.taken)
	}

	// Line 13's `while (i < 3)` evaluates four times and enters three.
	// A counter that recorded only "reached" would report 1 and 1.
	if e := edgesAt(t, edges, 13); e.eval != 4 || e.taken != 3 {
		t.Errorf("line 13 `while`: eval=%d taken=%d, want 4 and 3", e.eval, e.taken)
	}

	// Line 14's `while (i > 99)` never enters its body — and the LINE
	// counter for 14 still reports a hit. That contrast is the feature.
	if e := edgesAt(t, edges, 14); e.eval != 1 || e.taken != 0 {
		t.Errorf("line 14 `while`: eval=%d taken=%d, want 1 and 0", e.eval, e.taken)
	}
	if got := hits[14]; got == 0 {
		t.Errorf("line 14's LINE counter reports %d — the test's premise is that the line runs while its body does not", got)
	}

	// Line 8 holds two conditionals: the `if` and the `&&`. guard(0, 5)
	// makes `a > 0` false, so the `&&` short-circuits and its right
	// operand never runs — invisible to line coverage, since both
	// operands are on line 8.
	var onLine8 []*coverEdges
	for k, e := range edges {
		if k[0] == 8 {
			onLine8 = append(onLine8, e)
		}
	}
	if len(onLine8) != 2 {
		t.Fatalf("line 8 has %d conditionals, want 2 (the `if` and the `&&`)", len(onLine8))
	}
	for _, e := range onLine8 {
		if e.eval != 1 || e.taken != 0 {
			t.Errorf("line 8 conditional: eval=%d taken=%d, want 1 and 0", e.eval, e.taken)
		}
	}
}

func TestCoverBranchReportX86_64(t *testing.T) {
	stdout, stderr, code, srcPath := runCoverX86_64(t, coverBranchSrc)
	assertCoverBranchReport(t, stdout, stderr, code, srcPath)
}

func TestCoverBranchReportArm64(t *testing.T) {
	stdout, stderr, code, srcPath := runCoverArm64(t, coverBranchSrc)
	assertCoverBranchReport(t, stdout, stderr, code, srcPath)
}

// A program's coverage output must not depend on which native built it —
// the reader is one parser, and a divergence here would be invisible
// until someone compared two runs.
func TestCoverBranchReportIdenticalAcrossNatives(t *testing.T) {
	_, x86Err, _, x86Path := runCoverX86_64(t, coverBranchSrc)
	_, armErr, _, armPath := runCoverArm64(t, coverBranchSrc)
	// The two runs compile in different temp dirs, so normalise the path
	// before comparing — everything else must match byte for byte.
	got := strings.ReplaceAll(armErr, armPath, "ENTRY")
	want := strings.ReplaceAll(x86Err, x86Path, "ENTRY")
	if got != want {
		t.Errorf("arm64 report differs from x86-64:\narm64:\n%s\nx86-64:\n%s", got, want)
	}
}
