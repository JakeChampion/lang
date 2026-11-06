package e2ecompiler

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// A SIGILL or SIGBUS that the kernel raises writes its cause line and a
// backtrace, and the process still ends by that signal. A sent one exits with 128 plus the
// signal number and writes nothing.
func TestSelfHostFatalSignalReport(t *testing.T) {
	cli := buildSelfHostCLI(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "main.fern")
	if err := os.WriteFile(src, []byte("function main(): i32 {\n  print(\"before\");\n  return 7;\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	const mainEntry = "__fn_main.r:\n"
	x86gcc, runner := x86_64Tooling(t)
	armgcc, qemu := arm64Tooling(t)
	// patched links main with each instruction after its label: the register
	// entry of main is "__fn_main.r:\n", before any work.
	patched := func(t *testing.T, target, name string, edits ...[2]string) string {
		t.Helper()
		text, err := os.ReadFile(cli.emit(t, src, target))
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range edits {
			if !bytes.Contains(text, []byte(e[0])) {
				t.Fatalf("no %q in the %s output", e[0], target)
			}
			text = bytes.Replace(text, []byte(e[0]), []byte(e[0]+e[1]), 1)
		}
		asm := filepath.Join(dir, name+".s")
		if err := os.WriteFile(asm, text, 0o644); err != nil {
			t.Fatal(err)
		}
		bin := filepath.Join(dir, name)
		gcc := x86gcc
		if target == "arm64-linux" {
			gcc = armgcc
		}
		if out, err := exec.Command(gcc, "-static", "-nostdlib", "-no-pie", asm, "-o", bin).CombinedOutput(); err != nil {
			t.Fatalf("link %s: %v\n%s", name, err, out)
		}
		return bin
	}
	run := func(target, bin string) *exec.Cmd {
		if target == "arm64-linux" {
			return withoutCoreDump(runArm64Bin(qemu, bin))
		}
		return withoutCoreDump(runX86_64Bin(runner, bin))
	}
	faults := []struct {
		target, name, insn, cause string
		sig                       syscall.Signal
	}{
		{"x86-64-linux", "ud2", "    ud2\n", "fern: illegal instruction\n", syscall.SIGILL},
		{"arm64-linux", "udf", "    udf #0\n", "fern: illegal instruction\n", syscall.SIGILL},
		// The alignment-check flag makes an unaligned load a bus error.
		{"x86-64-linux", "align", "    pushfq\n    orl $0x40000, (%rsp)\n    popfq\n    movq %rsp, %rax\n    orq $1, %rax\n    movq (%rax), %rax\n", "fern: bus error\n", syscall.SIGBUS},
	}
	for _, f := range faults {
		t.Run(f.target+"/"+f.name, func(t *testing.T) {
			if f.sig == syscall.SIGBUS && len(runner) > 0 {
				t.Skip("the alignment-check flag needs a native x86-64 host")
			}
			cmd := run(f.target, patched(t, f.target, f.name, [2]string{mainEntry, f.insn}))
			var stderr bytes.Buffer
			cmd.Stderr = &stderr
			err := cmd.Run()
			if got := signalOf(err); got != f.sig {
				t.Errorf("ended by %v (%v), want %v", got, err, f.sig)
			}
			if want := f.cause + "backtrace:\n  0x"; !strings.HasPrefix(stderr.String(), want) {
				t.Errorf("stderr %q, want it to start with %q", stderr.String(), want)
			}
		})
	}
	// A second fault inside the handler is blocked, so the kernel takes the
	// default action for it at once: no nested handler and no report.
	nested := []struct {
		target, name, fault, anchor, inHandler string
		sig                                    syscall.Signal
	}{
		{"x86-64-linux", "segv-then-ill", "    movq 0, %rax\n", ".Lfern_segv_other:\n", "    ud2\n", syscall.SIGILL},
		{"arm64-linux", "segv-then-ill", "    mov x9, #0\n    ldr x9, [x9]\n", ".Lfern_segv_other:\n", "    udf #0\n", syscall.SIGILL},
		{"x86-64-linux", "ill-then-segv", "    ud2\n", ".Lfern_fatal_signal:\n", "    movq 0, %rax\n", syscall.SIGSEGV},
		{"arm64-linux", "ill-then-segv", "    udf #0\n", ".Lfern_fatal_signal:\n", "    mov x9, #0\n    ldr x9, [x9]\n", syscall.SIGSEGV},
	}
	for _, f := range nested {
		t.Run(f.target+"/"+f.name, func(t *testing.T) {
			cmd := run(f.target, patched(t, f.target, f.name, [2]string{mainEntry, f.fault}, [2]string{f.anchor, f.inHandler}))
			var stderr bytes.Buffer
			cmd.Stderr = &stderr
			err := cmd.Run()
			if got := signalOf(err); got != f.sig || strings.Contains(stderr.String(), "fern: ") {
				t.Errorf("ended by %v (%v), stderr %q; want %v and no report", got, err, stderr.String(), f.sig)
			}
		})
	}
	spin := filepath.Join(dir, "spin.fern")
	if err := os.WriteFile(spin, []byte("function main(): i32 {\n  let i = 0;\n  while (i >= 0) {\n    i = (i + 1) % 1000;\n  }\n  return 0;\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{"x86-64-linux", "arm64-linux"} {
		t.Run(target+"/sent", func(t *testing.T) {
			var cmd *exec.Cmd
			if target == "arm64-linux" {
				cmd = run(target, cli.arm64Binary(t, spin))
			} else {
				bin := filepath.Join(dir, "spin")
				if msg, err := cli.compileCmd(target, bin, spin).CombinedOutput(); err != nil {
					t.Fatalf("compile: %v\n%s", err, msg)
				}
				cmd = run(target, bin)
			}
			var stderr bytes.Buffer
			cmd.Stderr = &stderr
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			time.Sleep(500 * time.Millisecond)
			if err := cmd.Process.Signal(syscall.SIGBUS); err != nil {
				t.Fatal(err)
			}
			_ = cmd.Wait()
			if code := cmd.ProcessState.ExitCode(); code != 128+int(syscall.SIGBUS) || stderr.Len() > 0 {
				t.Errorf("exit %d, stderr %q; want %d and no report", code, stderr.String(), 128+int(syscall.SIGBUS))
			}
		})
	}
}
