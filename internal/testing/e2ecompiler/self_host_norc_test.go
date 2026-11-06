package e2ecompiler

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

// The attribute changes no code: a @norc function compiles and runs like any
// other function.
const norcGoodSrc = `@norc
function mix(x: i32, y: i32): i32 {
  let t = 0;
  let i = 0;
  while (i < y) {
    t += x;
    i += 1;
  }
  return t;
}

@norc
function peek(p: usize): i32 {
  unsafe {
    __fence();
    return __mmio_load32(p);
  }
}

function main(): i32 {
  unsafe {
    let p: usize = __alloc(8);
    __mmio_store32(p, 2);
    let r = mix(20, peek(p)) + peek(p);
    __free(p, 8);
    return r;
  }
}
`

const norcBadSrc = `function helper(x: i32): i32 {
  return x + 1;
}

@norc
function f(s: string, n: i32): i32 {
  let xs: i32[] = [1, 2];
  let t = "hi";
  let u = helper(n);
  while (u < n) {
    u += 1;
  }
  return u + xs.len();
}

function main(): i32 {
  return f("a", 1);
}
`

func TestSelfHostNoRCRuns(t *testing.T) {
	cli := buildSelfHostCLI(t)
	src := filepath.Join(t.TempDir(), "good.fern")
	if err := os.WriteFile(src, []byte(norcGoodSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Run("x86-64-linux", func(t *testing.T) {
		cmd := runX86_64Bin(cli.runner, cli.x86Binary(t, src))
		out, _ := cmd.CombinedOutput()
		if code := cmd.ProcessState.ExitCode(); code != 42 {
			t.Errorf("exit %d, want 42\n%s", code, out)
		}
	})
	t.Run("arm64-linux", func(t *testing.T) {
		qemu := e2eharness.Arm64Runner(t)
		cmd := e2eharness.RunArm64Bin(qemu, cli.arm64Binary(t, src))
		out, _ := cmd.CombinedOutput()
		if code := cmd.ProcessState.ExitCode(); code != 42 {
			t.Errorf("exit %d, want 42\n%s", code, out)
		}
	})
}

func TestSelfHostNoRCRefusesEachBreach(t *testing.T) {
	cli := buildSelfHostCLI(t)
	src := filepath.Join(t.TempDir(), "bad.fern")
	if err := os.WriteFile(src, []byte(norcBadSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := runX86_64Bin(cli.runner, cli.bin, "-check", src, cli.stdlib).CombinedOutput()
	if err == nil {
		t.Fatal("fern -check accepted a @norc function that allocates")
	}
	for _, want := range []string{
		"bad.fern:6:12: error[E102]: `@norc` function \"f\" takes \"s\" of type string, which is not a scalar",
		"bad.fern:7:3: error[E102]: `@norc` function \"f\" declares \"xs\" of type i32[], which is not a scalar",
		"bad.fern:7:19: error[E102]: `@norc` function \"f\" may not use an array literal",
		"bad.fern:8:11: error[E102]: `@norc` function \"f\" may not use a string",
		"bad.fern:9:17: error[E102]: `@norc` function \"f\" may call only `@norc` functions and the raw memory intrinsics, not \"helper\"",
		"bad.fern:13:20: error[E102]: `@norc` function \"f\" may not call a method or a function value",
	} {
		if !strings.Contains(string(out), want) {
			t.Errorf("no %q in:\n%s", want, out)
		}
	}
}
