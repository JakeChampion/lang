package e2ecompiler

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

// A hardened x86-64 program asks the kernel for the shadow stack at
// startup. Where the kernel grants it, an overwritten return address
// faults; where it refuses, the program runs as before and the hijack
// control succeeds.
const shadowStackSrc = `import "std/i32";
import "std/string";

@noinline
function apply(f: (i32) => i32, x: i32): i32 {
  return f(x);
}

@noinline
function twice(x: i32): i32 {
  return x * 2;
}

function main(): i32 {
  let features: string = "";
  match (read_file("/proc/self/status")) {
    Ok(status) => {
      for line in status.split("\n") {
        if (line.starts_with("x86_Thread_features:")) { features = line; }
      }
    },
    Err(_) => {}
  }
  print("apply " + apply(twice, 21).to_string());
  print("features [" + features + "]");
  return 0;
}
`

func TestSelfHostShadowStack(t *testing.T) {
	cli := buildSelfHostCLI(t)
	if len(cli.runner) != 0 {
		t.Skip("the shadow stack request needs native x86-64 Linux")
	}
	gcc, _ := x86_64Tooling(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "main.fern")
	if err := os.WriteFile(src, []byte(shadowStackSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	compile := func(out string, flags ...string) {
		t.Helper()
		args := append(append([]string{}, flags...), "-target", "x86-64-linux", "-o", out, src, cli.stdlib)
		cmd := runX86_64Bin(cli.runner, cli.bin, args...)
		cmd.Env = childEnv()
		if msg, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("compile %v: %v\n%s", flags, err, msg)
		}
	}
	run := func(bin string) (string, *exec.Cmd) {
		t.Helper()
		c := runX86_64Bin(cli.runner, bin)
		out, _ := c.CombinedOutput()
		return string(out), c
	}
	// movl $20481, %edi is ARCH_SHSTK_ENABLE; movl $20483, %edi is ARCH_SHSTK_LOCK.
	enable := []byte{0xbf, 0x01, 0x50, 0x00, 0x00}
	lock := []byte{0xbf, 0x03, 0x50, 0x00, 0x00}
	plain := filepath.Join(dir, "plain")
	hard := filepath.Join(dir, "hard")
	compile(plain)
	compile(hard, "-harden")
	plainImg, err := os.ReadFile(plain)
	if err != nil {
		t.Fatal(err)
	}
	hardImg, err := os.ReadFile(hard)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(plainImg, enable) {
		t.Error("the plain image asks for the shadow stack")
	}
	if !bytes.Contains(hardImg, enable) || !bytes.Contains(hardImg, lock) {
		t.Fatal("the -harden image does not ask for and lock the shadow stack")
	}
	out, c := run(hard)
	if !strings.HasPrefix(out, "apply 42\nfeatures [") || c.ProcessState.ExitCode() != 0 {
		t.Fatalf("the hardened program printed %q with %v", out, c.ProcessState)
	}
	features := strings.TrimSuffix(strings.TrimPrefix(strings.TrimSpace(out), "apply 42\nfeatures ["), "]")
	active := strings.Contains(features, "shstk")
	t.Logf("x86_Thread_features: %q; shadow stack active=%t", features, active)

	asmPath := filepath.Join(dir, "hard.s")
	compile(asmPath, "-harden", "-emit", "asm")
	asm, err := os.ReadFile(asmPath)
	if err != nil {
		t.Fatal(err)
	}
	// Before apply's ret the popped frame leaves the return address at the
	// stack top; the probe points it at code that exits 7.
	start := bytes.Index(asm, []byte("\n__fn_apply.r:\n"))
	if start < 0 {
		t.Fatal("no __fn_apply.r in the emitted text")
	}
	rel := bytes.Index(asm[start:], []byte("\n    ret\n"))
	if rel < 0 {
		t.Fatal("no ret after __fn_apply.r")
	}
	at := start + rel
	hijack := "\n    leaq .Lfern_hijack(%rip), %rsi\n    movq %rsi, (%rsp)\n    ret\n.Lfern_hijack:\n    movl $7, %edi\n    movl $231, %eax\n    syscall\n"
	patched := append(append([]byte{}, asm[:at]...), []byte(hijack)...)
	patched = append(patched, asm[at+len("\n    ret\n"):]...)
	s := filepath.Join(dir, "hijack.s")
	bin := filepath.Join(dir, "hijack")
	if err := os.WriteFile(s, patched, 0o644); err != nil {
		t.Fatal(err)
	}
	if msg, err := exec.Command(gcc, "-static", "-nostdlib", "-no-pie", s, "-o", bin).CombinedOutput(); err != nil {
		t.Fatalf("link: %v\n%s", err, msg)
	}
	c = withoutCoreDump(runX86_64Bin(cli.runner, bin))
	msg, _ := c.CombinedOutput()
	out = string(msg)
	status, _ := c.ProcessState.Sys().(syscall.WaitStatus)
	if active {
		if !status.Signaled() || status.Signal() != syscall.SIGSEGV {
			t.Fatalf("with an active shadow stack the hijacked return got %v, want SIGSEGV\n%s", c.ProcessState, out)
		}
		t.Log("shadow stack fault control passed")
		return
	}
	if c.ProcessState.ExitCode() != 7 {
		t.Fatalf("without a shadow stack the hijack control got %v, want exit 7\n%s", c.ProcessState, out)
	}
	t.Log("this host has no user shadow stack; the hijack control exits 7 and enforcement stays unverified here")
}
