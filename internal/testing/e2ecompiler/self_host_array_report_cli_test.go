package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

func arrayReportCLI(t *testing.T, src string, env []string, args ...string) (string, string, int) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "main.fern")
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(e2eharness.SelfHostCLI(t), append(args, path, e2eharness.SelfHostStdlibRoot(t))...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), env...)
	var stdout, stderr strings.Builder
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	_ = cmd.Run()
	if cmd.ProcessState == nil || !cmd.ProcessState.Exited() {
		t.Fatalf("fern %s did not exit normally\n%s", strings.Join(args, " "), stderr.String())
	}
	return stdout.String(), stderr.String(), cmd.ProcessState.ExitCode()
}

var arrayReportCLICases = []struct {
	name string
	src  string
	want []string
}{
	{"chain", `import "std/array";
function run(xs: i64[]): i64 {
  let out: Option[i64] = xs
    .map((x: i64): i64 => x + (1 as i64))
    .filter((x: i64): boolean => x > (0 as i64))
    .reduce((a: i64, b: i64): i64 => a + b);
  match (out) { Some(v) => { return v; }, None => { return 0 as i64; } }
}
function main(): i32 { return run([1 as i64, 2 as i64]) as i32; }
`, []string{
		"array report: primary Fern compiler, typed semantic fusion",
		"run: map -> filter -> reduce; one traversal",
		"reason=fused storage=no-intermediate-arrays",
		"typed semantic fusion decisions: 1",
	}},
	{"empty", "function main(): i32 { return 0; }\n", []string{
		"typed semantic fusion decisions: 0",
	}},
	{"ndarray products", `import "std/ndarray";
function mul(x: i64, y: i64): i64 { return x * y; }
function add(x: i64, y: i64): i64 { return x + y; }
function main(): i32 {
  let v: ndarray.NdArray[i64] = ndarray.from_flat([1 as i64, 2 as i64], [2]);
  return v.inner(v, 0 as i64, mul, add).get_unchecked([]) as i32;
}
`, []string{
		"array report: primary Fern compiler, ndarray kernels",
		"main: inner; layout=packed; rhs-layout=packed; multiply=mul$wrap; element=add$wrap",
		"ndarray kernels decisions: 1",
	}},
	{"ndarray axis", `import "std/ndarray";
function add(x: i64, y: i64): i64 { return x + y; }
function main(): i32 {
  let m: ndarray.NdArray[i64] = ndarray.from_flat([1 as i64, 2 as i64, 3 as i64, 4 as i64], [2, 2]);
  return m.reduce_axis(1, 0 as i64, add).get_unchecked([0]) as i32;
}
`, []string{
		"main: reduce_axis; layout=packed; axis=1; element=add$wrap",
	}},
	{"structural map key", `import "core/map";
import "std/array";
function main(): i32 {
  let a: Map[(string, i32), i32] = map_new(8);
  a = a.insert(("k", 1), 2);
  let xs: i64[] = [1 as i64, 2 as i64];
  let ys: i64[] = xs.map((v: i64): i64 => v * (2 as i64));
  return ys.len() + a.get_or(("k", 1), 0);
}
`, []string{
		"main: map; not fused",
		"map storage decisions: 1",
	}},
}

func TestSelfHostArrayReportCLI(t *testing.T) {
	for _, tc := range arrayReportCLICases {
		t.Run(tc.name, func(t *testing.T) {
			report, stderr, code := arrayReportCLI(t, tc.src, nil, "-array-report")
			if code != 0 || stderr != "" {
				t.Fatalf("fern -array-report: exit %d, stderr:\n%s", code, stderr)
			}
			if n := strings.Count(report, "typed semantic fusion decisions:"); n != 1 {
				t.Errorf("report holds %d fusion summaries, want the program's one:\n%s", n, report)
			}
			for _, want := range tc.want {
				if !strings.Contains(report, want) {
					t.Errorf("report does not mention %q:\n%s", want, report)
				}
			}
			out := filepath.Join(t.TempDir(), "prog")
			compiled, viaEnv, code := arrayReportCLI(t, tc.src, []string{"FERN_ARRAY_REPORT=1"}, "-target", "arm64-linux", "-o", out)
			if code != 0 || compiled != "" {
				t.Fatalf("FERN_ARRAY_REPORT=1 compile: exit %d, stdout:\n%s\nstderr:\n%s", code, compiled, viaEnv)
			}
			if report != viaEnv {
				t.Errorf("-array-report and FERN_ARRAY_REPORT=1 disagree\n-array-report:\n%s\nFERN_ARRAY_REPORT=1:\n%s", report, viaEnv)
			}
			if entries, _ := os.ReadDir(filepath.Dir(out)); len(entries) != 1 {
				t.Errorf("the FERN_ARRAY_REPORT=1 compile wrote %d files, want the one binary", len(entries))
			}
		})
	}
}

func TestSelfHostArrayReportCLITargetAndEnv(t *testing.T) {
	src := arrayReportCLICases[0].src
	report, stderr, code := arrayReportCLI(t, src, []string{"FERN_ARRAY_REPORT=1"}, "-array-report", "-target", "x86-64-linux")
	if code != 0 || stderr != "" {
		t.Fatalf("fern -array-report -target x86-64-linux: exit %d, stderr:\n%s", code, stderr)
	}
	if !strings.Contains(report, "run: map -> filter -> reduce; one traversal") {
		t.Errorf("x86-64-linux report does not name the fused chain:\n%s", report)
	}
}

func TestSelfHostArrayReportCLIRefusals(t *testing.T) {
	stdout, stderr, code := arrayReportCLI(t, "function main(): i32 { return true; }\n", nil, "-array-report")
	if code == 0 || !strings.Contains(stderr, "E0") || strings.Contains(stdout, "decisions:") {
		t.Errorf("ill-typed program: exit %d, stdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	for _, args := range [][]string{{"-array-report", "-o", "prog"}, {"-array-report", "-run"}} {
		stdout, stderr, code := arrayReportCLI(t, "function main(): i32 { return 0; }\n", nil, args...)
		want := "fern: -array-report prints the report and writes no output, so it takes neither -o nor -run\n"
		if code != 2 || stdout != "" || stderr != want {
			t.Errorf("fern %s: exit %d, stdout %q, stderr %q; want exit 2 and %q", strings.Join(args, " "), code, stdout, stderr, want)
		}
	}
}
