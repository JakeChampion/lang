package e2e

import (
	"bufio"
	"bytes"
	"io"
	"os/exec"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

const signalPreemptionSource = `import "std/i32";
import "std/i64";

function work(round: i32): i32 {
    let parts: string[] = [];
    let i: i32 = 0;
    while (i < 64) {
        parts = parts.append(f"r{round}-{i}");
        i = i + 1;
    }
    let total: i32 = 0;
    for p in parts {
        total = total + p.len();
    }
    return total;
}

function main(): i32 {
    let usr1: i32 = 10;
    if (signal_catch(usr1) != 0) { return 2; }
    print("ready");
    let sum: i64 = 0;
    let taken: i32 = 0;
    let round: i32 = 0;
    while (round < 20000) {
        sum = sum + (work(round) as i64);
        if (signal_taken(usr1)) { taken = taken + 1; }
        round = round + 1;
    }
    print(f"sum={sum}");
    if (taken > 0) {
        print("taken");
    } else {
        print("quiet");
    }
    return 0;
}
`

type preemptionRun struct {
	stdout, stderr string
	code, sent     int
}

func runPreempted(t *testing.T, cmd *exec.Cmd, storm bool) preemptionRun {
	t.Helper()
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	r := bufio.NewReader(out)
	first, _ := r.ReadString('\n')
	if first != "ready\n" {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		t.Fatalf("first line = %q, want %q; stderr: %q", first, "ready\n", stderr.String())
	}
	done := make(chan struct{})
	sent := make(chan int)
	go func() {
		n := 0
		for storm {
			select {
			case <-done:
				sent <- n
				return
			default:
			}
			if cmd.Process.Signal(syscall.SIGUSR1) == nil {
				n++
			}
			time.Sleep(20 * time.Microsecond)
		}
		<-done
		sent <- n
	}()
	rest, _ := io.ReadAll(r)
	_ = cmd.Wait()
	close(done)
	return preemptionRun{stdout: first + string(rest), stderr: stderr.String(), code: cmd.ProcessState.ExitCode(), sent: <-sent}
}

func checkPreemption(t *testing.T, build func() *exec.Cmd) {
	t.Helper()
	quiet := runPreempted(t, build(), false)
	stormy := runPreempted(t, build(), true)
	for _, run := range []struct {
		name string
		r    preemptionRun
	}{{"quiet", quiet}, {"stormy", stormy}} {
		if run.r.code != 0 {
			t.Fatalf("%s run: exit = %d, stdout %q, stderr %q", run.name, run.r.code, run.r.stdout, run.r.stderr)
		}
	}
	if !strings.HasSuffix(quiet.stdout, "quiet\n") {
		t.Fatalf("the quiet run took a signal: %q", quiet.stdout)
	}
	if stormy.sent == 0 || !strings.HasSuffix(stormy.stdout, "taken\n") {
		t.Fatalf("the storm did not reach the program: sent %d, stdout %q", stormy.sent, stormy.stdout)
	}
	if strings.TrimSuffix(quiet.stdout, "quiet\n") != strings.TrimSuffix(stormy.stdout, "taken\n") {
		t.Errorf("the storm changed the output:\nquiet:  %q\nstormy: %q", quiet.stdout, stormy.stdout)
	}
	qa, qf, ql := parseLeakCheckLine(t, quiet.stderr)
	sa, sf, sl := parseLeakCheckLine(t, stormy.stderr)
	if qa != qf || ql != 0 {
		t.Errorf("quiet census: allocs=%d frees=%d live_bytes=%d, want balanced", qa, qf, ql)
	}
	if sa != qa || sf != qf || sl != ql {
		t.Errorf("the storm changed the census: quiet allocs=%d frees=%d live=%d, stormy allocs=%d frees=%d live=%d", qa, qf, ql, sa, sf, sl)
	}
	t.Logf("%d signals sent; %d allocations each run", stormy.sent, qa)
}

func TestX86_64SignalPreemption(t *testing.T) {
	runner := e2eharness.X86_64Runner(t)
	bin := e2eharness.CompileSelfHostSource(t, e2eharness.TargetX86_64Linux, signalPreemptionSource, []string{"FERN_LEAKCHECK=1"})
	checkPreemption(t, func() *exec.Cmd { return runX86_64Bin(runner, bin) })
}

func TestArm64SignalPreemption(t *testing.T) {
	qemu := e2eharness.Arm64Runner(t)
	bin := e2eharness.CompileSelfHostSource(t, e2eharness.TargetArm64Linux, signalPreemptionSource, []string{"FERN_LEAKCHECK=1"})
	checkPreemption(t, func() *exec.Cmd { return runArm64Bin(qemu, bin) })
}
