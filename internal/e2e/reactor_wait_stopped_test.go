package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// A process stopped and continued while it waits on the reactor sees its
// epoll_wait fail with EINTR, which the kernel does not restart even with
// no signal handler installed. RealDriver.wait waits again for what is left
// of its timeout rather than answering no events early (#11646).
func TestReactorWaitSurvivesStopContinue(t *testing.T) {
	bin := buildFernCLI(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "stopped.fern")
	prog := `import "std/async";
function main(): i32 {
  let d: async.RealDriver = async.real_driver();
  let start: i64 = monotonic_ns();
  let got: i32[] = d.wait(1, 1500);
  let ms: i64 = (monotonic_ns() - start) / (1000000 as i64);
  if (got.len() != 0) {
    return 2;
  }
  if (ms < (1400 as i64)) {
    return 1;
  }
  return 0;
}
`
	if err := os.WriteFile(src, []byte(prog), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	for _, be := range nativeBackends() {
		be := be
		t.Run(be.target, func(t *testing.T) {
			qemu := be.qemu(t)
			out := filepath.Join(dir, be.target+"_stopped.bin")
			if o, err := exec.Command(bin, "-target", be.target, "-o", out, src).CombinedOutput(); err != nil {
				t.Fatalf("build failed: %v\n%s", err, o)
			}
			cmd := be.run(qemu, out)
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			time.Sleep(300 * time.Millisecond)
			_ = cmd.Process.Signal(syscall.SIGSTOP)
			time.Sleep(100 * time.Millisecond)
			_ = cmd.Process.Signal(syscall.SIGCONT)
			_ = cmd.Wait()
			switch code := cmd.ProcessState.ExitCode(); code {
			case 0:
			case 1:
				t.Errorf("the wait ended before its 1500 ms timeout after a stop and continue")
			case 2:
				t.Errorf("the wait reported events on an empty reactor")
			default:
				t.Errorf("exit = %d", code)
			}
		})
	}
}
