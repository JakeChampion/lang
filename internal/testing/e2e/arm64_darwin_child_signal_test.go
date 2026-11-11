package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

// Signal 17 is the child-exit signal in the runtime convention. Darwin gives
// it the number 20, and reaps each child itself when the signal is ignored.
// The supervisor of std/serve depends on both the event and the exit status.
func TestArm64DarwinWatchChildSignal(t *testing.T) {
	bin := buildFernCLI(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "child.fern")
	prog := `import "std/async";
import "std/time";

function main(): i32 {
  let drv: async.RealDriver = async.real_driver();
  if (drv.watch_signal(17) < 0) { return 10; }
  let pid: i32 = proc_fork();
  if (pid < 0) { return 11; }
  if (pid == 0) { exit(7); }
  let seen: boolean = false;
  let tries: i32 = 0;
  while (!seen && tries < 50) {
    let events: i32[] = drv.wait(8, Some(time.duration_millis(100)));
    let at: i32 = 0;
    while (at + 1 < events.len()) {
      if (events[at] == 0 - 17) { seen = true; }
      at = at + 2;
    }
    tries = tries + 1;
  }
  if (!seen) { return 12; }
  if (proc_waitpid_nohang(pid) != 7) { return 13; }
  if (drv.unwatch_signal(17) < 0) { return 14; }
  drv.close();
  return 0;
}
`
	if err := os.WriteFile(src, []byte(prog), 0o644); err != nil {
		t.Fatal(err)
	}
	host := e2eharness.HostSelfHostTarget()
	targets := []string{"arm64-darwin"}
	if host != "" && host != "arm64-darwin" {
		targets = append(targets, host)
	}
	for _, target := range targets {
		out := filepath.Join(dir, target)
		if o, err := exec.Command(bin, "-target", target, "-o", out, src).CombinedOutput(); err != nil {
			t.Fatalf("%s build failed: %v\n%s", target, err, o)
		}
		if target != host {
			continue
		}
		cmd := exec.Command(out)
		_ = cmd.Run()
		if ps := cmd.ProcessState; ps == nil || ps.ExitCode() != 0 {
			t.Fatalf("%s: the program exited %v, want 0", target, ps)
		}
	}
}
