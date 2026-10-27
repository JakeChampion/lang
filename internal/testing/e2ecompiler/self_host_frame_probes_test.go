package e2ecompiler

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"testing"
)

// bigFrameSrc keeps 600 call results live across 600 calls, so the register
// allocator spills most of them and big's frame is larger than one page.
// big recurses with work after the call, so the frames stack up and a
// patched frame can walk the stack down to its guard. With 600 levels the
// sum is 360000 plus the 361200 the levels add on the way back.
func bigFrameSrc() string {
	var b strings.Builder
	b.WriteString("@noinline\nfunction f(n: i32): i32 { return n * 2 + 1; }\n@noinline\nfunction big(depth: i32): i32 {\n")
	for i := 0; i < 600; i++ {
		fmt.Fprintf(&b, "  let v%d: i32 = f(%d);\n", i, i)
	}
	b.WriteString("  let total: i32 = v0")
	for i := 1; i < 600; i++ {
		fmt.Fprintf(&b, " + v%d", i)
	}
	b.WriteString(";\n  if (depth > 0) { let r: i32 = big(depth - 1); return r + f(depth); }\n  return total;\n}\n")
	b.WriteString("function main(): i32 { print(big(args().len() + 599).to_string()); return 0; }\n")
	return b.String()
}

var (
	x86ProbeRe   = regexp.MustCompile(`(?m)^    movq \$(\d+), %r11\n(\.L\w+):\n    subq \$4096, %rsp\n    movq \$0, \(%rsp\)\n    subq \$4096, %r11\n    cmpq \$4095, %r11\n    ja \.L\w+\n    subq %r11, %rsp\n`)
	arm64ProbeRe = regexp.MustCompile(`(?m)^    movz x16, #(\d+)\n(\.L\w+):\n    sub sp, sp, #4096\n    str xzr, \[sp, #0\]\n    sub x16, x16, #4096\n    cmp x16, #4095\n    b\.hi \.L\w+\n    sub sp, sp, x16\n`)
)

// widenFrame sets big's probed frame to 2 MiB, twice the guard, so the
// recursion reaches the guard with a frame that could step over it.
func widenFrame(t *testing.T, asm, target string) string {
	t.Helper()
	body := asmFuncBody(t, asm, "__fn_big")
	re, wide := x86ProbeRe, "    movq $2097152, %r11\n"
	if target == "arm64-linux" {
		re, wide = arm64ProbeRe, "    movz x16, #0\n    movk x16, #32, lsl #16\n"
	}
	loc := re.FindStringSubmatchIndex(body)
	if loc == nil {
		t.Fatalf("big has no page probe loop on %s:\n%s", target, body)
	}
	head := body[:loc[2]]
	line := strings.LastIndex(head, "\n") + 1
	end := strings.Index(body[loc[2]:], "\n") + loc[2] + 1
	patched := body[:line] + wide + body[end:]
	return strings.Replace(asm, body, patched, 1)
}

func TestSelfHostFrameProbes(t *testing.T) {
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
			src := filepath.Join(dir, "big.fern")
			if err := os.WriteFile(src, []byte(bigFrameSrc()), 0o644); err != nil {
				t.Fatal(err)
			}
			asmPath := filepath.Join(dir, "big.s")
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
			}{{"control", false}, {"frame-past-guard", true}} {
				t.Run(tc.name, func(t *testing.T) {
					text := string(asm)
					if tc.patched {
						text = widenFrame(t, text, target)
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
					var stdout, stderr bytes.Buffer
					c.Stdout, c.Stderr = &stdout, &stderr
					_ = c.Run()
					code := c.ProcessState.ExitCode()
					signalled := false
					if ws, ok := c.ProcessState.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
						signalled = true
					}
					if tc.patched {
						if code != 134 || signalled || !strings.HasPrefix(stderr.String(), "fern: stack overflow\n") {
							t.Fatalf("exit %d, signalled %t, want 134 with the stack overflow cause\nstderr:\n%s", code, signalled, stderr.String())
						}
						return
					}
					if code != 0 || stdout.String() != "721200\n" {
						t.Fatalf("control: exit %d, stdout %q, stderr %q; want 721200", code, stdout.String(), stderr.String())
					}
				})
			}
		})
	}
}
