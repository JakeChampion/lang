package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// Each open records the close count of its number in the handle, so a socket
// program has the table of counts. The table is the only allocation: a socket
// has none of its own.
func TestSelfHostSocketsAllocateOnlyTheCloseTableX86_64(t *testing.T) {
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
	if len(events) != 4 {
		t.Fatalf("want the allocation control, the close table, and the free of each, got:\n%s", out)
	}
	for at := 0; at < 4; at += 2 {
		if events[at].kind != "a" || events[at+1].kind != "f" || events[at].ptr != events[at+1].ptr {
			t.Fatalf("event %d is not an allocation that the next event frees:\n%s", at, out)
		}
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
