package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestSelfHostSocketCloseWithoutObserverAllocatesNothingX86_64(t *testing.T) {
	cli := buildSelfHostCLI(t)
	src := filepath.Join(t.TempDir(), "main.fern")
	const program = `function main(): i32 {
  unsafe {
    let p = __alloc(8);
    __free(p, 8);
  }
  for i in 0..100 {
    let s = tcp_listen(0);
    if (s.fd < 0) { return 1; }
    if (tcp_close(s) != 0) { return 2; }
  }
  return 0;
}`
	if err := os.WriteFile(src, []byte(program), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := runX86_64Bin(cli.runner, cli.x86Binary(t, src, "FERN_RC_TRACE=1"))
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("run: %v\n%s", err, out)
	}
	events, _ := parseHev(t, string(out))
	if len(events) != 2 || events[0].kind != "a" || events[1].kind != "f" || events[0].ptr != events[1].ptr {
		t.Fatalf("want only the allocation control and its free, got:\n%s", out)
	}
}

func TestSelfHostSocketGenerationSurvivesTableGrowth(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
		t.Run(target, func(t *testing.T) {
			src := filepath.Join(t.TempDir(), "main.fern")
			const program = `function main(): i32 {
  let s = tcp_listen(0);
  if (s.fd < 0) { return 1; }
  let fd = s.fd;
  unsafe {
    let before = __task_fd_gen(fd);
    for i in 1..512 { __task_fd_gen(i); }
    if (__task_fd_gen(fd) != before) { return 2; }
    if (tcp_close(s) != 0) { return 3; }
    if (__task_fd_gen(fd) != before + 1) { return 4; }
    for i in 512..1024 { __task_fd_gen(i); }
    if (__task_fd_gen(fd) != before + 1) { return 5; }
  }
  return 0;
}`
			if err := os.WriteFile(src, []byte(program), 0o644); err != nil {
				t.Fatal(err)
			}
			var cmd *exec.Cmd
			switch target {
			case "arm64-linux":
				_, qemu := arm64Tooling(t)
				cmd = runArm64Bin(qemu, cli.arm64Binary(t, src))
			case "wasm32-wasi":
				cmd = exec.Command("wasmtime", "run", "-S", "inherit-network", cli.wasmComponent(t, src))
			default:
				cmd = runX86_64Bin(cli.runner, cli.x86Binary(t, src))
			}
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("run: %v\n%s", err, out)
			}
		})
	}
}
