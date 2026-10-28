package e2ecompiler

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// A raw free of an address outside the heap arena linked that address into
// the free list, so a later allocation returned it. The free now checks the
// arena bounds and aborts with its cause. The control frees a live block,
// allocates again and reads the recycled block back.
const rawFreeSrc = `function main(): i32 {
  unsafe {
    let p: usize = __alloc(64);
    __store_i64(p, 7i64);
    __free(p, 64);
    let q: usize = __alloc(64);
    print((q == p).to_string());
    __free(0 as usize, 64);
    print("null free is a no-op");
    __free(4096 as usize, 64);
    print("freed a non-heap address");
    return 0;
  }
}
`

func TestSelfHostRawFreeOutsideHeap(t *testing.T) {
	cli := buildSelfHostCLI(t)
	_, qemu := arm64Tooling(t)
	for _, target := range []string{"x86-64-linux", "arm64-linux"} {
		t.Run(target, func(t *testing.T) {
			dir := t.TempDir()
			src := filepath.Join(dir, "main.fern")
			if err := os.WriteFile(src, []byte(rawFreeSrc), 0o644); err != nil {
				t.Fatal(err)
			}
			bin := filepath.Join(dir, "main")
			args := []string{"-target", target, "-o", bin, src, cli.stdlib}
			cmd := runX86_64Bin(cli.runner, cli.bin, args...)
			cmd.Env = childEnv()
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("self-host CLI %v: %v\n%s", args, err, out)
			}
			var c *exec.Cmd
			if target == "arm64-linux" {
				c = runArm64Bin(qemu, bin)
			} else {
				c = runX86_64Bin(cli.runner, bin)
			}
			var stdout, stderr bytes.Buffer
			c.Stdout, c.Stderr = &stdout, &stderr
			_ = c.Run()
			code := c.ProcessState.ExitCode()
			want := "true\nnull free is a no-op\n"
			if code != 134 || stdout.String() != want || !strings.HasPrefix(stderr.String(), "fern: release of memory outside the heap\n") {
				t.Fatalf("exit %d, stdout %q, stderr %q; want 134 after %q with the out-of-heap cause", code, stdout.String(), stderr.String(), want)
			}
		})
	}
}
