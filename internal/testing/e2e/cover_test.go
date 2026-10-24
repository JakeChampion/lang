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

	"github.com/jakechampion/lang/internal/syntax/ast"
	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

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
    let i: i32 = 0;
    while (i < 3) {
        i = i + 1;
    }
    print("ran");
    return classify(1) - 2;
}
`

func coverCompile(t *testing.T, target, src string, cover bool, out string, args ...string) string {
	t.Helper()
	srcPath := filepath.Join(t.TempDir(), "main.fern")
	if err := os.WriteFile(srcPath, []byte(src), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	argv := []string{"-target", target}
	if cover {
		argv = append(argv, "-cover")
	}
	argv = append(append(argv, args...), "-o", out, srcPath, e2eharness.SelfHostStdlibRoot(t))
	cmd := exec.Command(e2eharness.SelfHostCLI(t), argv...)
	cmd.Env = e2eharness.ChildEnv()
	if msg, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("self-host %v: %v\n%s", argv, err, msg)
	}
	return srcPath
}

func emitCover(t *testing.T, target, src string, cover bool) (string, string) {
	t.Helper()
	asmPath := filepath.Join(t.TempDir(), "prog.s")
	srcPath := coverCompile(t, target, src, cover, asmPath, "-emit", "asm")
	asm, err := os.ReadFile(asmPath)
	if err != nil {
		t.Fatalf("read asm: %v", err)
	}
	return string(asm), srcPath
}

func runCoverX86_64(t *testing.T, src string) (string, string, int, string) {
	t.Helper()
	_, runner := x86_64Tooling(t)
	binPath := filepath.Join(t.TempDir(), "prog")
	srcPath := coverCompile(t, e2eharness.TargetX86_64Linux, src, true, binPath)
	var cmd *exec.Cmd
	if len(runner) == 0 {
		cmd = exec.Command(binPath)
	} else {
		cmd = exec.Command(runner[0], append(append([]string{}, runner[1:]...), binPath)...)
	}
	stdout, stderr, code := runSplit(t, cmd)
	return stdout, stderr, code, srcPath
}

func runCoverArm64(t *testing.T, src string) (string, string, int, string) {
	t.Helper()
	_, qemu := arm64Tooling(t)
	binPath := filepath.Join(t.TempDir(), "prog")
	srcPath := coverCompile(t, e2eharness.TargetArm64Linux, src, true, binPath)
	stdout, stderr, code := runSplit(t, runArm64Bin(qemu, binPath))
	return stdout, stderr, code, srcPath
}

var coverLineRe = regexp.MustCompile(`^` + regexp.QuoteMeta(ast.CoverLinePrefix) + `(.*):(\d+) (\d+)$`)

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
	if got, ok := hits[3]; !ok {
		t.Errorf("line 3 (the untaken `return 1;`) has no row — an unreached line must still be counted, as 0")
	} else if got != 0 {
		t.Errorf("line 3 (untaken branch) reports %d hits, want 0", got)
	}
	if got, ok := hits[8]; !ok {
		t.Errorf("line 8 (inside a function nothing calls) has no row — never called is exactly what a report must say")
	} else if got != 0 {
		t.Errorf("line 8 (never-called function body) reports %d hits, want 0", got)
	}
	if got := hits[5]; got != 1 {
		t.Errorf("line 5 (the taken `return 2;`) reports %d hits, want 1", got)
	}
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

func TestCoverFlagOffEmitsNoCoverageSymbols(t *testing.T) {
	for _, target := range []string{e2eharness.TargetX86_64Linux, e2eharness.TargetArm64Linux} {
		asm, _ := emitCover(t, target, coverSrc, false)
		for _, sym := range []string{"__fern_cov_counters", "__fern_cov_table", "__fern_cov_report", ast.CoverLinePrefix} {
			if strings.Contains(asm, sym) {
				t.Errorf("%s: flag-off build mentions %q", target, sym)
			}
		}
	}
}

func TestCoverFlagOnEmitsCoverageSymbols(t *testing.T) {
	for _, target := range []string{e2eharness.TargetX86_64Linux, e2eharness.TargetArm64Linux} {
		asm, srcPath := emitCover(t, target, coverSrc, true)
		for _, sym := range []string{"__fern_cov_counters", "__fern_cov_table", "__fern_cov_report"} {
			if !strings.Contains(asm, sym) {
				t.Errorf("%s: flag-on build is missing %q", target, sym)
			}
		}
		if want := fmt.Sprintf("%s%s:3 ", ast.CoverLinePrefix, srcPath); !strings.Contains(asm, want) {
			t.Errorf("%s: flag-on build has no report literal for line 3 (%q)", target, want)
		}
	}
}

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
    let i: i32 = 0;
    while (i < 3) { i = i + 1; }
    while (i > 99) { i = i + 1; }
    print("ran");
    return classify(1) + guard(0, 5) - 2;
}
`

type coverEdges struct{ eval, taken uint64 }

var coverBranchRe = regexp.MustCompile(`^` + regexp.QuoteMeta(ast.CoverBranchPrefix) + `(.*):(\d+):(\d+) ([ET]) (\d+)$`)

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

	if e := edgesAt(t, edges, 2); e.eval != 1 || e.taken != 0 {
		t.Errorf("line 2 `if`: eval=%d taken=%d, want 1 and 0", e.eval, e.taken)
	}

	if e := edgesAt(t, edges, 13); e.eval != 4 || e.taken != 3 {
		t.Errorf("line 13 `while`: eval=%d taken=%d, want 4 and 3", e.eval, e.taken)
	}

	if e := edgesAt(t, edges, 14); e.eval != 1 || e.taken != 0 {
		t.Errorf("line 14 `while`: eval=%d taken=%d, want 1 and 0", e.eval, e.taken)
	}
	if got := hits[14]; got == 0 {
		t.Errorf("line 14's LINE counter reports %d — the test's premise is that the line runs while its body does not", got)
	}

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

func TestCoverBranchReportIdenticalAcrossNatives(t *testing.T) {
	_, x86Err, _, x86Path := runCoverX86_64(t, coverBranchSrc)
	_, armErr, _, armPath := runCoverArm64(t, coverBranchSrc)
	got := strings.ReplaceAll(armErr, armPath, "ENTRY")
	want := strings.ReplaceAll(x86Err, x86Path, "ENTRY")
	if got != want {
		t.Errorf("arm64 report differs from x86-64:\narm64:\n%s\nx86-64:\n%s", got, want)
	}
}
