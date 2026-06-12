package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// auditStdArrayCases broaden the self-host std/array coverage beyond the
// single gcd_all case in self_host_array_test.go: each compiles the real
// std/array (+ std/sort) bundle with a different reduction `main` through
// the self-hosted compiler and asserts the exit code. Self-host arm of
// the §D std/array audit (docs/FEATURE-AUDIT.md); the native arm is the
// `audit_std_numeric` fixture (all four native backends).
var auditStdArrayCases = []struct {
	name string
	main string
	exit int
}{
	{"sum", `import "./array";
function main(): i32 { var xs: i32[] = [3, 1, 2]; return array.__method_Array_sum(xs); }`, 6},
	{"product", `import "./array";
function main(): i32 { var xs: i32[] = [3, 1, 2]; return array.__method_Array_product(xs); }`, 6},
	{"sorted_asc", `import "./array";
function main(): i32 { var xs: i32[] = [3, 1, 2]; var s: i32[] = array.__method_Array_sorted_asc(xs); return s[0] * 100 + s[1] * 10 + s[2]; }`, 123},
	{"max-some", `import "./array";
function main(): i32 { var xs: i32[] = [3, 1, 2]; match (array.__method_Array_max(xs)) { Some(m) => { return m; }, None => { return 0; } } }`, 3},
}

func stdArrayBundle(t *testing.T, main string) []byte {
	t.Helper()
	a, err := os.ReadFile("../../internal/stdlib/std/array.fern")
	if err != nil {
		t.Fatalf("read array: %v", err)
	}
	srt, err := os.ReadFile("../../internal/stdlib/std/sort.fern")
	if err != nil {
		t.Fatalf("read sort: %v", err)
	}
	var b []byte
	b = append(b, "///MODULE array\n"...)
	b = append(b, a...)
	b = append(b, "\n///MODULE sort\n"...)
	b = append(b, srt...)
	b = append(b, "\n///MODULE main\n"...)
	b = append(b, main...)
	b = append(b, '\n')
	return b
}

// TestSelfHostAuditStdArrayX86_64 compiles each std/array reduction bundle
// through the self-hosted x86-64 driver and asserts the exit code.
func TestSelfHostAuditStdArrayX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	for _, name := range []string{"flatten.fern", "bundle_run.fern"} {
		src, _ := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "bundle_run.fern", "driver")

	for _, tc := range auditStdArrayCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, gcc, runner, driverBin, stdArrayBundle(t, tc.main))
			if len(asm) == 0 {
				t.Fatal("self-host compiler emitted 0 bytes")
			}
			progBin := buildBin(t, gcc, dir, "arr_"+tc.name, string(asm))
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(progBin)
			} else {
				cmd = exec.Command(runner[0], append(runner[1:], progBin)...)
			}
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.exit {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.exit)
			}
		})
	}
}
