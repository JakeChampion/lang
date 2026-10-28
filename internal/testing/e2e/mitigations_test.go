//go:build linux

package e2e

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"syscall"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

const mitigationsReportSrc = `import "std/mitigations";
function main(): i32 {
  for row in mitigations.report() { print(row.line()); }
  return 0;
}
`

var mitigationRowRe = regexp.MustCompile(`^(\w+) cpu=(yes|no|unknown) kernel=(yes|no|unknown) process=(yes|no|unknown)$`)

// hostX86Rows computes the x86-64 report from the same files the module
// reads, in Go, for the process that runs this test. The kernel line is the
// same for every process, and neither this test binary nor the Fern program
// enables a CET feature, so the rows must agree.
func hostX86Rows(t *testing.T) []string {
	t.Helper()
	state := func(b bool) string {
		if b {
			return "yes"
		}
		return "no"
	}
	listed := func(line, flag string) bool {
		_, rest, ok := strings.Cut(line, ":")
		if !ok {
			return false
		}
		for _, tok := range strings.Fields(rest) {
			if tok == flag {
				return true
			}
		}
		return false
	}
	cpuinfo, err := os.ReadFile("/proc/cpuinfo")
	if err != nil {
		t.Fatal(err)
	}
	var flags string
	for _, line := range strings.Split(string(cpuinfo), "\n") {
		if strings.HasPrefix(line, "flags") {
			flags = line
			break
		}
	}
	if flags == "" {
		t.Fatal("no flags line in /proc/cpuinfo")
	}
	status, err := os.ReadFile("/proc/self/status")
	if err != nil {
		t.Fatal(err)
	}
	kernel, features := false, ""
	for _, line := range strings.Split(string(status), "\n") {
		if strings.HasPrefix(line, "x86_Thread_features:") {
			kernel, features = true, line
			break
		}
	}
	ibt := state(listed(features, "ibt"))
	return []string{
		"shstk cpu=" + state(listed(flags, "user_shstk")) + " kernel=" + state(kernel) + " process=" + state(listed(features, "shstk")),
		"ibt cpu=" + state(listed(flags, "ibt")) + " kernel=" + ibt + " process=" + ibt,
	}
}

func TestSelfHostMitigationsReport(t *testing.T) {
	if runtime.GOARCH != "amd64" && runtime.GOARCH != "arm64" {
		t.Skip("the report runs on x86-64 and arm64 Linux hosts")
	}
	run := func(t *testing.T, cmd *exec.Cmd) (string, string, int) {
		t.Helper()
		var stdout, stderr bytes.Buffer
		cmd.Stdout, cmd.Stderr = &stdout, &stderr
		if err := cmd.Run(); cmd.ProcessState == nil {
			t.Fatalf("run: %v\n%s", err, stderr.String())
		}
		return stdout.String(), stderr.String(), cmd.ProcessState.ExitCode()
	}
	rows := func(t *testing.T, out string, want []string) {
		t.Helper()
		lines := strings.Split(strings.TrimSuffix(out, "\n"), "\n")
		if len(lines) != len(want) {
			t.Fatalf("report printed %d rows, want %d:\n%s", len(lines), len(want), out)
		}
		for i, line := range lines {
			m := mitigationRowRe.FindStringSubmatch(line)
			if m == nil || m[1] != want[i] {
				t.Errorf("row %d = %q, want the %s row with three known states", i, line, want[i])
			}
		}
	}
	host := e2eharness.HostSelfHostTarget()
	t.Run(host, func(t *testing.T) {
		bin := e2eharness.CompileSelfHostSource(t, host, mitigationsReportSrc, nil)
		out, errout, code := run(t, exec.Command(bin))
		if code != 0 || errout != "" {
			t.Fatalf("exit=%d stdout=%q stderr=%q", code, out, errout)
		}
		t.Logf("%s report:\n%s", host, out)
		if runtime.GOARCH == "amd64" {
			if want := strings.Join(hostX86Rows(t), "\n") + "\n"; out != want {
				t.Fatalf("native report:\n%swant the rows Go reads from the same files:\n%s", out, want)
			}
		} else {
			rows(t, out, []string{"pac", "bti", "mte"})
		}
		t.Run("oracle", func(t *testing.T) {
			src := filepath.Join(t.TempDir(), "main.fern")
			if err := os.WriteFile(src, []byte(mitigationsReportSrc), 0o644); err != nil {
				t.Fatal(err)
			}
			oout, oerr, ocode := run(t, exec.Command(buildLangBinForInterp(t), "-interp", src))
			if ocode != 0 || oerr != "" || oout != out {
				t.Fatalf("oracle: exit=%d stdout=%q stderr=%q; the native report is %q", ocode, oout, oerr, out)
			}
		})
		t.Run("unreadable-sources", func(t *testing.T) {
			// With openat refused, every file-backed cell is unknown; the
			// arm64 process cells come from prctl and stay as they are.
			nr := uintptr(syscall.SYS_OPENAT)
			dout, derr, dcode := run(t, underSeccompErrno(t, t.TempDir(), syscall.EACCES, []uintptr{nr}, bin))
			if dcode != 0 || derr != "" {
				t.Fatalf("denied: exit=%d stdout=%q stderr=%q", dcode, dout, derr)
			}
			for _, line := range strings.Split(strings.TrimSuffix(dout, "\n"), "\n") {
				m := mitigationRowRe.FindStringSubmatch(line)
				if m == nil || m[2] != "unknown" || m[3] != "unknown" {
					t.Errorf("denied row %q: the cpu and kernel cells must be unknown", line)
				}
				if runtime.GOARCH == "amd64" && m != nil && m[4] != "unknown" {
					t.Errorf("denied row %q: the process cell comes from a file on x86-64", line)
				}
			}
			if strings.Count(dout, "\n") != strings.Count(out, "\n") {
				t.Errorf("denied report has %d rows, the control %d", strings.Count(dout, "\n"), strings.Count(out, "\n"))
			}
		})
	})
	if runtime.GOARCH != "amd64" {
		return
	}
	t.Run("arm64-linux", func(t *testing.T) {
		qemu := e2eharness.Arm64Runner(t)
		if qemu == "" {
			t.Skip("no arm64 runner")
		}
		bin := e2eharness.CompileSelfHostSource(t, "arm64-linux", mitigationsReportSrc, nil)
		out, errout, code := run(t, exec.Command(qemu, bin))
		if code != 0 || errout != "" {
			t.Fatalf("exit=%d stdout=%q stderr=%q", code, out, errout)
		}
		t.Logf("arm64-linux under QEMU:\n%s", out)
		rows(t, out, []string{"pac", "bti", "mte"})
		// QEMU user mode serves the guest's auxv and cpuinfo itself, so the
		// cpu and kernel cells are known even on a foreign host.
		for _, line := range strings.Split(strings.TrimSuffix(out, "\n"), "\n") {
			if m := mitigationRowRe.FindStringSubmatch(line); m != nil && (m[2] == "unknown" || m[3] == "unknown") {
				t.Errorf("row %q: QEMU serves the auxv and the cpuinfo, so the cpu and kernel cells are known", line)
			}
		}
	})
}
