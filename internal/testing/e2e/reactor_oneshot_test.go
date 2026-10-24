package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestReactorOneShotWatch(t *testing.T) {
	bin := buildFernCLI(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "oneshot.fern")
	prog := `import "std/async";
function main(): i32 {
  let p: Pipe = match (pipe()) {
    Ok(p) => p,
    Err(_) => { return 10; },
  };
  match (p.w.write("x")) { Ok(_) => {}, Err(_) => { return 11; } }
  let d: async.RealDriver = async.real_driver();
  let fd: i32 = p.r.fd;
  if (!d.oneshot()) { return 12; }
  if (d.watch(fd, 1) < 0) { return 13; }
  if (d.wait(4, 0).len() != 2) { return 14; }
  if (d.wait(4, 0).len() != 2) { return 15; }
  if (d.unwatch(fd) < 0) { return 16; }
  if (d.watch(fd, 1 | async.ONESHOT) < 0) { return 17; }
  if (d.wait(4, 0).len() != 2) { return 18; }
  if (d.wait(4, 50).len() != 0) { return 19; }
  if (d.watch(fd, 1 | async.ONESHOT) < 0) { return 20; }
  if (d.wait(4, 0).len() != 2) { return 21; }
  if (d.watch(fd, 1) < 0) { return 22; }
  if (d.wait(4, 0).len() != 2) { return 23; }
  if (d.wait(4, 0).len() != 2) { return 24; }
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
			out := filepath.Join(dir, be.target+"_oneshot.bin")
			if o, err := exec.Command(bin, "-target", be.target, "-o", out, src).CombinedOutput(); err != nil {
				t.Fatalf("build failed: %v\n%s", err, o)
			}
			cmd := be.run(qemu, out)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != 0 {
				t.Errorf("exit = %d: 14/15 a plain watch stopped reporting, 18 a one-shot one never fired, 19 it fired twice, 21 a re-watch did not re-arm it, 23/24 a plain watch after it did not report each time", code)
			}
		})
	}
}
