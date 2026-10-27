package e2ecompiler

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"testing"
)

// A frame pointer that points above the application stack ends the walk at
// the stack top, so the report keeps its cause line and exit status instead
// of faulting inside the walker.
// mid and main keep work after their calls, so neither call is a tail jump.
// On x86-64 neither needs a frame pointer, so the walk from boom starts at
// the frame pointer mid passed through: the corrupted one in the patched
// build, the entry frame's in the control.
const corruptFrameSrc = `@noinline
function boom(xs: i32[], i: i32): i32 { return xs[i]; }
@noinline
function mid(n: i32): i32 { let r: i32 = boom([1, 2, 3], n + 7); return r + 1; }
function main(): i32 { let r: i32 = mid(args().len()); return r - 1; }
`

var corruptFrameReport = regexp.MustCompile(`^fern: array index out of range\nbacktrace:\n(  0x[0-9a-f]{16}\n){0,1}$`)

// corruptFrameBeforeCall inserts an instruction that sets the frame pointer
// to an aligned address above every user mapping, just before mid calls boom.
func corruptFrameBeforeCall(t *testing.T, asm, target string) string {
	t.Helper()
	call, clobber := "call __fn_boom", "    movq $0x800000000000, %rbp\n"
	if target == "arm64-linux" {
		call, clobber = "bl __fn_boom", "    movz x29, #0x8000, lsl #32\n"
	}
	start := strings.Index(asm, "\n__fn_mid:")
	if start < 0 {
		t.Fatalf("no __fn_mid in the emitted assembly")
	}
	at := strings.Index(asm[start:], call)
	if at < 0 {
		t.Fatalf("mid does not call boom in the emitted assembly:\n%s", asmFuncBody(t, asm, "__fn_mid"))
	}
	at += start
	line := strings.LastIndex(asm[:at], "\n") + 1
	return asm[:line] + clobber + asm[line:]
}

func TestSelfHostAbortBacktraceCorruptFrame(t *testing.T) {
	cli := buildSelfHostCLI(t)
	x86gcc, _ := x86_64Tooling(t)
	for _, target := range []string{"x86-64-linux", "arm64-linux"} {
		t.Run(target, func(t *testing.T) {
			gcc := x86gcc
			var run func(bin string) *exec.Cmd
			if target == "arm64-linux" {
				arm64gcc, qemu := arm64Tooling(t)
				gcc = arm64gcc
				run = func(bin string) *exec.Cmd { return runArm64Bin(qemu, bin) }
			} else {
				run = func(bin string) *exec.Cmd { return runX86_64Bin(cli.runner, bin) }
			}
			dir := t.TempDir()
			src := filepath.Join(dir, "corrupt.fern")
			if err := os.WriteFile(src, []byte(corruptFrameSrc), 0o644); err != nil {
				t.Fatal(err)
			}
			asmPath := filepath.Join(dir, "corrupt.s")
			args := []string{"-emit", "asm", "-target", target, "-o", asmPath, src, cli.stdlib}
			cmd := runX86_64Bin(cli.runner, cli.bin, args...)
			cmd.Env = childEnv()
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("self-host CLI %v: %v\n%s", args, err, out)
			}
			asm, err := os.ReadFile(asmPath)
			if err != nil {
				t.Fatal(err)
			}
			for _, tc := range []struct {
				name    string
				patched bool
			}{{"control", false}, {"corrupt-frame", true}} {
				t.Run(tc.name, func(t *testing.T) {
					text := string(asm)
					if tc.patched {
						text = corruptFrameBeforeCall(t, text, target)
					}
					path := filepath.Join(dir, tc.name+".s")
					if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
						t.Fatal(err)
					}
					bin := filepath.Join(dir, tc.name)
					if out, err := exec.Command(gcc, "-static", "-nostdlib", "-no-pie", path, "-o", bin).CombinedOutput(); err != nil {
						t.Fatalf("link: %v\n%s", err, out)
					}
					c := run(bin)
					var stderr bytes.Buffer
					c.Stderr = &stderr
					_ = c.Run()
					code := c.ProcessState.ExitCode()
					signalled := false
					if ws, ok := c.ProcessState.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
						signalled = true
					}
					if code != 134 || signalled {
						t.Fatalf("exit %d, signalled %t, want 134 from the report path\nstderr:\n%s", code, signalled, stderr.String())
					}
					frames := abortFrameRe.FindAllString(stderr.String(), -1)
					if tc.patched {
						if !corruptFrameReport.MatchString(stderr.String()) {
							t.Errorf("the walk did not stop at the stack top:\n%s", stderr.String())
						}
					} else if len(frames) < 1 {
						t.Errorf("control printed no frame:\n%s", stderr.String())
					}
				})
			}
		})
	}
}
