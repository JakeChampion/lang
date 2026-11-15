package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// The function has more live parameters than the allocator has registers
// that a division does not write. Its unused parameters stay in a register
// until the entry moves end.
const paramRegisterProg = `import "std/i32";

@noinline
function pick(xs: i32[], ys: i32[], bs: u8[], v: [i32], sv: [u8], a: i32, b: i32): i32 {
    let acc: i32 = 0;
    if ((a / 4) as u32 < xs.len() as u32) {
        acc = acc + xs[a / 4];
    }
    return acc + xs.len() + (bs.len() << 8);
}

function main(): i32 {
    let xs: i32[] = [10, 20, 30];
    let ys: i32[] = [1];
    let bs: u8[] = [7, 8];
    print(pick(xs, ys, bs, xs[0:1], bs[0:1], 8, 0).to_string());
    print(pick(xs, ys, bs, xs[0:1], bs[0:1], 0 - 8, 0).to_string());
    return 0;
}
`

// Two parameters do not get one register. With one register for two, the
// entry of the function has two moves to one destination, and the compiler
// did not end.
func TestSelfHostParametersDoNotShareARegister(t *testing.T) {
	cli := buildSelfHostCLI(t)
	src := filepath.Join(t.TempDir(), "main.fern")
	if err := os.WriteFile(src, []byte(paramRegisterProg), 0o644); err != nil {
		t.Fatal(err)
	}
	const want = "545\n515\n"
	_, qemu := arm64Tooling(t)
	for target, cmd := range map[string]*exec.Cmd{
		"x86-64-linux": runX86_64Bin(cli.runner, cli.x86Binary(t, src)),
		"arm64-linux":  runArm64Bin(qemu, cli.arm64Binary(t, src)),
	} {
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("%s: %v", target, err)
		}
		if string(out) != want {
			t.Errorf("%s: stdout %q, want %q", target, out, want)
		}
	}
}

// The guard is false at compile time, so `ws` is `xs` on each path and the
// release of `ws` tests `xs != xs`.
const selfCompareReleaseProg = `import "std/i32";

@noinline
function count(xs: i32[]): i32 {
    let ws: i32[] = xs;
    if ((0 - 8) / 2 > 0) {
        ws = ws.with(0, 0);
    }
    return ws.len();
}

function main(): i32 {
    let xs: i32[] = [1, 2, 3];
    print(count(xs).to_string());
    return 0;
}
`

// The certifier takes a comparison of a value with itself as decided. It
// refused this function for the release behind `xs != xs`, which does not run.
func TestSelfHostSelfComparedReleaseCertifies(t *testing.T) {
	cli := buildSelfHostCLI(t)
	src := filepath.Join(t.TempDir(), "main.fern")
	if err := os.WriteFile(src, []byte(selfCompareReleaseProg), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := runX86_64Bin(cli.runner, cli.x86Binary(t, src)).Output()
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != "3\n" {
		t.Errorf("stdout %q, want %q", out, "3\n")
	}
}
