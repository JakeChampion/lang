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

// A second raw free of the block at the head of its list aborts. The control
// frees two blocks of one class in turn, which the check must allow.
func rawDoubleFreeSrc(size string) string {
	return `function main(): i32 {
  unsafe {
    let a: usize = __alloc(` + size + `);
    let b: usize = __alloc(` + size + `);
    __free(a, ` + size + `);
    __free(b, ` + size + `);
    let c: usize = __alloc(` + size + `);
    let d: usize = __alloc(` + size + `);
    print(((c == a || c == b) && (d == a || d == b) && c != d).to_string());
    __free(c, ` + size + `);
    __free(d, ` + size + `);
    print("two frees of one class");
    __free(d, ` + size + `);
    print("freed a block twice");
    return 0;
  }
}
`
}

func runRawFreeProbe(t *testing.T, cli *selfHostCLI, qemu, target, source string) (stdout, stderr string, code int) {
	t.Helper()
	dir := t.TempDir()
	src := filepath.Join(dir, "main.fern")
	if err := os.WriteFile(src, []byte(source), 0o644); err != nil {
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
	var out, errb bytes.Buffer
	c.Stdout, c.Stderr = &out, &errb
	_ = c.Run()
	return out.String(), errb.String(), c.ProcessState.ExitCode()
}

func TestSelfHostRawFreeOutsideHeap(t *testing.T) {
	cli := buildSelfHostCLI(t)
	_, qemu := arm64Tooling(t)
	for _, target := range []string{"x86-64-linux", "arm64-linux"} {
		t.Run(target, func(t *testing.T) {
			stdout, stderr, code := runRawFreeProbe(t, cli, qemu, target, rawFreeSrc)
			want := "true\nnull free is a no-op\n"
			if code != 134 || stdout != want || !strings.HasPrefix(stderr, "fern: release of memory outside the heap\n") {
				t.Fatalf("exit %d, stdout %q, stderr %q; want 134 after %q with the out-of-heap cause", code, stdout, stderr, want)
			}
		})
	}
}

func TestSelfHostRawDoubleFree(t *testing.T) {
	cli := buildSelfHostCLI(t)
	_, qemu := arm64Tooling(t)
	for _, target := range []string{"x86-64-linux", "arm64-linux"} {
		for _, size := range []string{"64", "1048576"} {
			t.Run(target+"/"+size, func(t *testing.T) {
				stdout, stderr, code := runRawFreeProbe(t, cli, qemu, target, rawDoubleFreeSrc(size))
				want := "true\ntwo frees of one class\n"
				if code != 134 || stdout != want || !strings.HasPrefix(stderr, "fern: release of memory that is already free\n") {
					t.Fatalf("exit %d, stdout %q, stderr %q; want 134 after %q with the double-free cause", code, stdout, stderr, want)
				}
			})
		}
	}
}
