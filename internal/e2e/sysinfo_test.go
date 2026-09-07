package e2e

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// The three machine-facing primitives group C's system-information
// utilities are built on: `uname_field(i)` (one field of the kernel's
// utsname record), `getcwd()` and `cpu_count()`.
//
// Every assertion here compares against the SAME kernel fact through a
// second path, never against "non-empty": sysname is "Linux" on every
// box in this fleet, so a helper that read the neighbouring field would
// sail through a non-empty check. `uname -m` and Go's syscall.Uname are
// that second path.

// unameFieldProbeSource prints the five fields one per line, then the
// two out-of-range indices, which must be empty rather than the
// domainname the record holds at 5.
func unameFieldProbeSource() string {
	return `function main(): i32 {
    var i: i32 = 0;
    while (i < 5) {
        print(uname_field(i));
        i = i + 1;
    }
    print("[" + uname_field(5) + "]");
    print("[" + uname_field(0 - 1) + "]");
    return 0;
}
`
}

// wantMachine is the machine name the leg's execution environment
// reports, which is not always the host's: the arm64 legs run under
// qemu-aarch64 where the host is x86-64, and the emulator answers
// "aarch64" for exactly the field this probe is most likely to get
// wrong. Passing it in keeps the assertion on the real value rather
// than relaxing it to "non-empty".
func checkUnameFieldOutput(t *testing.T, out string, wantMachine string) {
	t.Helper()
	want := hostUtsname(t)
	want[4] = wantMachine
	got := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(got) != 7 {
		t.Fatalf("uname_field probe printed %d lines, want 7:\n%s", len(got), out)
	}
	names := [5]string{"sysname", "nodename", "release", "version", "machine"}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("uname_field(%d) [%s] = %q, want %q", i, names[i], got[i], want[i])
		}
	}
	for i, line := range got[5:] {
		if line != "[]" {
			t.Errorf("uname_field out-of-range probe %d = %s, want []", i, line)
		}
	}
}

func TestX86_64UnameField(t *testing.T) {
	out, code := compileAndRunX86_64(t, unameFieldProbeSource())
	if code != 0 {
		t.Fatalf("probe exited %d:\n%s", code, out)
	}
	checkUnameFieldOutput(t, out, hostUtsname(t)[4])
}

func TestArm64UnameField(t *testing.T) {
	out, code := compileAndRunArm64(t, unameFieldProbeSource())
	if code != 0 {
		t.Fatalf("probe exited %d:\n%s", code, out)
	}
	checkUnameFieldOutput(t, out, "aarch64")
}

// The SSA backend keeps its own helper table, so it is its own leg.
func TestArm64SSAUnameField(t *testing.T) {
	checkUnameFieldOutput(t, compileAndRunArm64SSACapture(t, unameFieldProbeSource()), "aarch64")
}

// getcwdProbeSource prints the working directory the process inherited.
const getcwdProbeSource = `function main(): i32 {
    print(getcwd());
    return 0;
}
`

// The compiled probes run with the test process's own working
// directory, which is this package's source directory.
func hostCwd(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("os.Getwd: %v", err)
	}
	return wd
}

func TestX86_64Getcwd(t *testing.T) {
	out, code := compileAndRunX86_64(t, getcwdProbeSource)
	if want := hostCwd(t); code != 0 || strings.TrimSpace(out) != want {
		t.Errorf("getcwd() = %q (exit %d), want %q", strings.TrimSpace(out), code, want)
	}
}

func TestArm64Getcwd(t *testing.T) {
	out, code := compileAndRunArm64(t, getcwdProbeSource)
	if want := hostCwd(t); code != 0 || strings.TrimSpace(out) != want {
		t.Errorf("getcwd() = %q (exit %d), want %q", strings.TrimSpace(out), code, want)
	}
}

func TestArm64SSAGetcwd(t *testing.T) {
	out := compileAndRunArm64SSACapture(t, getcwdProbeSource)
	if want := hostCwd(t); strings.TrimSpace(out) != want {
		t.Errorf("getcwd() = %q, want %q", strings.TrimSpace(out), want)
	}
}

// cpuCountProbeSource exits with the count, so the assertion is the
// exit status. runtime.NumCPU is the same number through a second path:
// the Go runtime sets it from sched_getaffinity(2) at startup.
const cpuCountProbeSource = `function main(): i32 {
    return cpu_count();
}
`

// wantCPUCount is the count the probe must report. An exit status is one
// byte, so a machine with 256 processing units or more cannot carry the
// answer out — say so rather than compare a truncated number.
func wantCPUCount(t *testing.T) int {
	t.Helper()
	n := runtime.NumCPU()
	if n < 1 || n > 255 {
		t.Skipf("%d CPUs does not fit an exit status; the probe cannot report it", n)
	}
	return n
}

func TestX86_64CPUCount(t *testing.T) {
	want := wantCPUCount(t)
	if _, code := compileAndRunX86_64(t, cpuCountProbeSource); code != want {
		t.Errorf("cpu_count() = %d, want %d", code, want)
	}
}

func TestArm64CPUCount(t *testing.T) {
	want := wantCPUCount(t)
	if _, code := compileAndRunArm64(t, cpuCountProbeSource); code != want {
		t.Errorf("cpu_count() = %d, want %d", code, want)
	}
}

func TestInterpSysinfo(t *testing.T) {
	want := hostUtsname(t)
	src := fmt.Sprintf(`function main(): i32 {
    if (uname_field(0) != %q) { return 1; }
    if (uname_field(4) != %q) { return 2; }
    if (uname_field(5) != "") { return 3; }
    if (getcwd() != %q) { return 4; }
    if (cpu_count() != %d) { return 5; }
    return 0;
}
`, want[0], want[4], hostCwd(t), runtime.NumCPU())
	if code := runInterpExit(t, src); code != 0 {
		t.Errorf("interp sysinfo probe exited %d (the code names the case)", code)
	}
}

// Neither WASI preview has a utsname record, a processor count or a
// current directory, so all three are refused by the target capability
// gate at check time rather than answered with a fiction. E066 names
// the capability and the builtin; the wasm leg of these primitives IS
// that diagnostic.
func TestWasmSysinfoIsRefused(t *testing.T) {
	bin := buildLangBinForCheck(t)
	dir := t.TempDir()
	for _, tc := range []struct{ name, call, capability string }{
		{"uname_field", "uname_field(0).len()", "sysinfo"},
		{"cpu_count", "cpu_count()", "sysinfo"},
		{"getcwd", "getcwd().len()", "cwd"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			src := fmt.Sprintf("function main(): i32 {\n    return %s;\n}\n", tc.call)
			path := filepath.Join(dir, tc.name+".fern")
			if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
				t.Fatalf("write %s: %v", path, err)
			}
			out, err := exec.Command(bin, "-target", "wasm32-wasi", "-o", filepath.Join(dir, tc.name+".wasm"), path).CombinedOutput()
			if err == nil {
				t.Fatalf("%s compiled for wasm32-wasi; it has no answer to give there", tc.name)
			}
			for _, want := range []string{"E066", "`" + tc.capability + "`", "`" + tc.name + "`"} {
				if !bytes.Contains(out, []byte(want)) {
					t.Errorf("rejection does not mention %s:\n%s", want, out)
				}
			}
		})
	}
}
