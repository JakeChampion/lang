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

// keep holds twelve call results live across calls, so it has a frame with
// saved registers and spill slots on both targets. With one argument the
// sum is 246.
const canarySrc = `@noinline
function f(n: i32): i32 { return n * 3 + 1; }
@noinline
function keep(n: i32): i32 {
  let a: i32 = f(n);
  let b: i32 = f(n + 1);
  let c: i32 = f(n + 2);
  let d: i32 = f(n + 3);
  let e: i32 = f(n + 4);
  let g: i32 = f(n + 5);
  let h: i32 = f(n + 6);
  let i: i32 = f(n + 7);
  let j: i32 = f(n + 8);
  let k: i32 = f(n + 9);
  let l: i32 = f(n + 10);
  let m: i32 = f(n + 11);
  return a + b + c + d + e + g + h + i + j + k + l + m;
}
function main(): i32 { print(keep(args().len()).to_string()); return 0; }
`

var (
	x86CanaryStoreRe   = regexp.MustCompile(`(?m)^    movq __fern_canary\(%rip\), %r11\n    movq %r11, -(\d+)\(%rbp\)\n`)
	x86CanaryCheckRe   = regexp.MustCompile(`(?m)^    movq -(\d+)\(%rbp\), %rsi\n    cmpq __fern_canary\(%rip\), %rsi\n    jne __fern_canary_fail\n`)
	arm64CanaryStoreRe = regexp.MustCompile(`(?m)^    adrp x17, __fern_canary\n    ldr x17, \[x17, :lo12:__fern_canary\]\n    str x17, \[x29, #-(\d+)\]\n`)
	arm64CanaryCheckRe = regexp.MustCompile(`(?m)^    ldr x17, \[x29, #-(\d+)\]\n    adrp x16, __fern_canary\n    ldr x16, \[x16, :lo12:__fern_canary\]\n    cmp x16, x17\n    b\.ne __fern_canary_fail\n`)
)

// smashCanary overwrites keep's canary slot right after the prologue stores
// it, as a write past a frame box would.
func smashCanary(t *testing.T, asm, target string) string {
	t.Helper()
	body := asmFuncBody(t, asm, "__fn_keep")
	store, check := x86CanaryStoreRe, x86CanaryCheckRe
	if target == "arm64-linux" {
		store, check = arm64CanaryStoreRe, arm64CanaryCheckRe
	}
	m := store.FindStringSubmatchIndex(body)
	if m == nil {
		t.Fatalf("keep stores no canary on %s:\n%s", target, body)
	}
	c := check.FindStringSubmatch(body)
	if c == nil || c[1] != body[m[2]:m[3]] {
		t.Fatalf("keep does not check the canary slot it stores on %s:\n%s", target, body)
	}
	smash := "    movq $0, -" + c[1] + "(%rbp)\n"
	if target == "arm64-linux" {
		smash = "    str xzr, [x29, #-" + c[1] + "]\n"
	}
	patched := body[:m[1]] + smash + body[m[1]:]
	return strings.Replace(asm, body, patched, 1)
}

func TestSelfHostStackCanary(t *testing.T) {
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
			src := filepath.Join(dir, "keep.fern")
			if err := os.WriteFile(src, []byte(canarySrc), 0o644); err != nil {
				t.Fatal(err)
			}
			emit := func(name string, flags ...string) string {
				t.Helper()
				out := filepath.Join(dir, name)
				args := append(append([]string{}, flags...), "-target", target, "-o", out, src, cli.stdlib)
				cmd := runX86_64Bin(cli.runner, cli.bin, args...)
				cmd.Env = childEnv()
				if text, err := cmd.CombinedOutput(); err != nil {
					t.Fatalf("self-host CLI %v: %v\n%s", args, err, text)
				}
				return out
			}
			plain, err := os.ReadFile(emit("plain.s", "-emit", "asm"))
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(plain), "__fern_canary") {
				t.Fatalf("a build without -harden references the canary")
			}
			hardened, err := os.ReadFile(emit("harden.s", "-harden", "-emit", "asm"))
			if err != nil {
				t.Fatal(err)
			}
			execute := func(bin string) (string, string, int, bool) {
				t.Helper()
				c := run(bin)
				var stdout, stderr bytes.Buffer
				c.Stdout, c.Stderr = &stdout, &stderr
				_ = c.Run()
				signalled := false
				if ws, ok := c.ProcessState.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
					signalled = true
				}
				return stdout.String(), stderr.String(), c.ProcessState.ExitCode(), signalled
			}
			t.Run("image", func(t *testing.T) {
				bin := emit("harden-image", "-harden")
				if out, errout, code, sig := execute(bin); code != 0 || sig || out != "246\n" {
					t.Fatalf("hardened image: exit %d, signalled %t, stdout %q, stderr %q", code, sig, out, errout)
				}
			})
			for _, tc := range []struct {
				name    string
				patched bool
			}{{"control", false}, {"smashed", true}} {
				t.Run(tc.name, func(t *testing.T) {
					text := string(hardened)
					if tc.patched {
						text = smashCanary(t, text, target)
					}
					path := filepath.Join(dir, tc.name+".s")
					if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
						t.Fatal(err)
					}
					bin := filepath.Join(dir, tc.name)
					if out, err := exec.Command(gcc, "-static", "-nostdlib", "-no-pie", path, "-o", bin).CombinedOutput(); err != nil {
						t.Fatalf("link: %v\n%s", err, out)
					}
					out, errout, code, sig := execute(bin)
					if tc.patched {
						if code != 134 || sig || errout != "fern: stack canary mismatch\n" || out != "" {
							t.Fatalf("smashed canary: exit %d, signalled %t, stdout %q, stderr %q; want 134 and the mismatch cause", code, sig, out, errout)
						}
						return
					}
					if code != 0 || sig || out != "246\n" {
						t.Fatalf("control: exit %d, signalled %t, stdout %q, stderr %q; want 246", code, sig, out, errout)
					}
				})
			}
		})
	}
}
