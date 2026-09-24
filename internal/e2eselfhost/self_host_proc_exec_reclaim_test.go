package e2eselfhost

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// proc_exec and proc_exec_as copy the path and every vector string into
// NUL-terminated blocks for execve, and returned from a failed exec without
// giving any of them back: a caller trying candidate paths leaked a path, a
// vector and a copy per string on every miss (#8837). Twenty misses of each,
// then a subprocess, whose own scratch was already returned, to prove the
// shared copy routine still builds a working argv.
const procExecReclaimSrc = `function main(): i32 {
    var i: i32 = 0;
    while (i < 20) {
        if (proc_exec("/nonexistent/x", ["a", "bb"]) >= 0) { return 99; }
        if (proc_exec_as("/nonexistent/y", ["y", "c"], ["K=V", "L=W"]) >= 0) { return 98; }
        i = i + 1;
    }
    var r = subprocess("echo", ["hello", "world"], "");
    return r.stdout.len();
}
`

const procExecReclaimWant = len("hello world\n")

func writeProcExecSrc(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "proc_exec.fern")
	if err := os.WriteFile(path, []byte(procExecReclaimSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestSelfHostProcExecReclaimX86_64(t *testing.T) {
	cli := buildSelfHostCLI(t)
	src := writeProcExecSrc(t)
	for _, mode := range []string{"FERN_LEAKCHECK=1", "FERN_SANITIZE=1"} {
		t.Run(strings.TrimSuffix(mode, "=1"), func(t *testing.T) {
			stderr, exit := runWithStdin(t, cli.runner, cli.x86Binary(t, src, mode), nil)
			if exit != procExecReclaimWant || strings.Contains(stderr, "fern-sanitizer:") {
				t.Fatalf("exit = %d, want %d, and the sanitizer silent\n%s", exit, procExecReclaimWant, stderr)
			}
			assertBalancedCensus(t, stderr)
		})
	}
}

func TestSelfHostProcExecReclaimArm64(t *testing.T) {
	armgcc, qemu := arm64Tooling(t)
	cli := buildSelfHostCLI(t)
	asm, err := os.ReadFile(cli.emit(t, writeProcExecSrc(t), "arm64-linux", "FERN_LEAKCHECK=1"))
	if err != nil {
		t.Fatal(err)
	}
	cmd := runArm64Bin(qemu, buildBinArm64(t, armgcc, t.TempDir(), "proc_exec", string(asm)))
	var eb strings.Builder
	cmd.Stderr = &eb
	_ = cmd.Run()
	if code := cmd.ProcessState.ExitCode(); code != procExecReclaimWant {
		t.Fatalf("exit = %d, want %d\n%s", code, procExecReclaimWant, eb.String())
	}
	assertBalancedCensus(t, eb.String())
}
