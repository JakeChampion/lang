// `eprint` writes a line and its newline in one write, so processes sharing
// a stderr never land a write inside each other's line: a write of at most
// PIPE_BUF bytes to a pipe is atomic, and the serve supervisor's workers log
// to the one stderr they inherit. The probe is that shape without the
// sockets: four forked workers each printing a thousand lines, read back
// through a pipe, every line whole and every worker's count exact.
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

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

const (
	eprintWorkers        = 4
	eprintLinesPerWorker = 1000
	eprintPayloadBytes   = 100
)

// eprintWorkersSource forks the workers; each prints its lines and exits,
// and the parent exits 0 once every worker has.
func eprintWorkersSource() string {
	return fmt.Sprintf(`import "core/int";

function main(): i32 {
    let pay: string = "";
    let j: i32 = 0;
    while (j < %d) {
        pay = pay + "x";
        j = j + 1;
    }
    let pids: i32[] = [];
    let w: i32 = 0;
    while (w < %d) {
        let pid: i32 = proc_fork();
        if (pid < 0) {
            return 20;
        }
        if (pid == 0) {
            let line: string = "worker " + int.int_to_string(w) + " " + pay;
            let i: i32 = 0;
            while (i < %d) {
                eprint(line);
                i = i + 1;
            }
            exit(0);
        }
        pids = pids.append(pid);
        w = w + 1;
    }
    let k: i32 = 0;
    while (k < pids.len()) {
        if (proc_waitpid(pids[k]) != 0) {
            return 21;
        }
        k = k + 1;
    }
    return 0;
}
`, eprintPayloadBytes, eprintWorkers, eprintLinesPerWorker)
}

// checkWholeLines fails on any line that is not one worker's whole line,
// and on a worker whose lines were not all seen.
func checkWholeLines(t *testing.T, stderr string) {
	t.Helper()
	pay := strings.Repeat("x", eprintPayloadBytes)
	counts := make([]int, eprintWorkers)
	body := strings.TrimSuffix(stderr, "\n")
	for n, ln := range strings.Split(body, "\n") {
		matched := false
		for w := range counts {
			if ln == fmt.Sprintf("worker %d %s", w, pay) {
				counts[w]++
				matched = true
				break
			}
		}
		if !matched {
			t.Fatalf("line %d is not one worker's whole line: %q", n+1, ln)
		}
	}
	for w, c := range counts {
		if c != eprintLinesPerWorker {
			t.Errorf("worker %d: %d lines, want %d", w, c, eprintLinesPerWorker)
		}
	}
}

func runEprintWorkers(t *testing.T, cmd *exec.Cmd) {
	t.Helper()
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	cmd.Stdout = nil
	if err := cmd.Run(); err != nil {
		t.Fatalf("run: %v (the exit code names the step; see eprintWorkersSource)\n%s", err, stderr.String())
	}
	checkWholeLines(t, stderr.String())
}

func TestX86_64EprintWholeLines(t *testing.T) {
	_, runner := x86_64Tooling(t)
	bin := e2eharness.CompileSelfHostSource(t, e2eharness.TargetX86_64Linux, eprintWorkersSource(), nil)
	runEprintWorkers(t, e2eharness.RunX86_64Bin(runner, bin))
}

func TestArm64EprintWholeLines(t *testing.T) {
	bin, qemu := e2eharness.CompileArm64Bin(t, eprintWorkersSource())
	runEprintWorkers(t, e2eharness.RunArm64Bin(qemu, bin))
}

// TestArm64DarwinEprintWholeLines is the same probe through the Darwin
// writev(2). Off Apple Silicon the build is what is checked.
func TestArm64DarwinEprintWholeLines(t *testing.T) {
	bin := buildFernCLI(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "eprint_workers.fern")
	if err := os.WriteFile(src, []byte(eprintWorkersSource()), 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "eprint_workers")
	if o, err := exec.Command(bin, "-target", "arm64-darwin", "-o", out, src).CombinedOutput(); err != nil {
		t.Fatalf("native arm64-darwin build failed: %v\n%s", err, o)
	}
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		return
	}
	runEprintWorkers(t, exec.Command(out))
}
