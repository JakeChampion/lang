package e2ecompiler

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Under -harden a function value points at a stub that starts with the
// landing pad, and every indirect call checks for that pad. The control
// takes the address of a closure and of twice; thrice has the same
// signature as twice and is only called directly.
const cfiSrc = `import "std/i32";

@noinline
function apply(f: (i32) => i32, x: i32): i32 {
  return f(x);
}

@noinline
function twice(x: i32): i32 {
  return x * 2;
}

@noinline
function thrice(x: i32): i32 {
  return x * 3;
}

function main(): i32 {
  let k: i32 = 5;
  let add: (i32) => i32 = (x: i32): i32 => x + k;
  let xs: i32[] = [1, 2, 3].map(twice);
  print(apply(add, 10).to_string() + " " + apply(twice, 7).to_string() + " " + xs[2].to_string() + " " + thrice(1).to_string());
  return 0;
}
`

const cfiWant = "15 14 6 3\n"

const cfiCause = "fern: indirect call to an address that is not a function\n"

func TestSelfHostForwardEdgeCFI(t *testing.T) {
	cli := buildSelfHostCLI(t)
	x86gcc, _ := x86_64Tooling(t)
	arm64gcc, qemu := arm64Tooling(t)
	for _, tc := range []struct {
		target, gcc, entry string
		pad, check         []byte
	}{
		// The check is cmpl $imm32, (%r11) on x86-64 and movz w17, #0x245f on arm64.
		{"x86-64-linux", x86gcc, ".r", []byte{0xf3, 0x0f, 0x1e, 0xfa}, []byte{0x41, 0x81, 0x3b, 0xf3, 0x0f, 0x1e, 0xfa}},
		{"arm64-linux", arm64gcc, ".k", []byte{0x5f, 0x24, 0x03, 0xd5}, []byte{0xf1, 0x8b, 0x84, 0x52}},
	} {
		t.Run(tc.target, func(t *testing.T) {
			dir := t.TempDir()
			src := filepath.Join(dir, "main.fern")
			if err := os.WriteFile(src, []byte(cfiSrc), 0o644); err != nil {
				t.Fatal(err)
			}
			compile := func(out string, flags ...string) {
				t.Helper()
				args := append(append([]string{}, flags...), "-target", tc.target, "-o", out, src, cli.stdlib)
				cmd := runX86_64Bin(cli.runner, cli.bin, args...)
				cmd.Env = childEnv()
				if msg, err := cmd.CombinedOutput(); err != nil {
					t.Fatalf("compile %v: %v\n%s", flags, err, msg)
				}
			}
			run := func(bin string) (string, string, int) {
				t.Helper()
				var c *exec.Cmd
				if tc.target == "arm64-linux" {
					c = runArm64Bin(qemu, bin)
				} else {
					c = runX86_64Bin(cli.runner, bin)
				}
				var stdout, stderr bytes.Buffer
				c.Stdout, c.Stderr = &stdout, &stderr
				_ = c.Run()
				return stdout.String(), stderr.String(), c.ProcessState.ExitCode()
			}
			plain := filepath.Join(dir, "plain")
			hard := filepath.Join(dir, "hard")
			pie := filepath.Join(dir, "hard-pie")
			compile(plain)
			compile(hard, "-harden")
			compile(pie, "-harden", "-pie")
			for _, bin := range []string{plain, hard, pie} {
				if out, errout, code := run(bin); out != cfiWant || code != 0 {
					t.Fatalf("%s printed %q with exit %d (stderr %q), want %q and exit 0", filepath.Base(bin), out, code, errout, cfiWant)
				}
			}
			plainImg, err := os.ReadFile(plain)
			if err != nil {
				t.Fatal(err)
			}
			hardImg, err := os.ReadFile(hard)
			if err != nil {
				t.Fatal(err)
			}
			if n := bytes.Count(plainImg, tc.pad); n != 0 {
				t.Errorf("the plain image carries %d pads, want none", n)
			}
			if bytes.Contains(plainImg, []byte(cfiCause)) {
				t.Error("the plain image carries the CFI cause")
			}
			// The x86-64 check embeds the pad bytes as its immediate, so the
			// stubs are the pads that are not checks.
			pads := bytes.Count(hardImg, tc.pad)
			checks := bytes.Count(hardImg, tc.check)
			if tc.target == "x86-64-linux" {
				pads -= checks
			}
			if pads != 2 {
				t.Errorf("the -harden image carries %d stubs, want one for the closure and one for twice", pads)
			}
			if checks == 0 {
				t.Error("the -harden image carries no indirect-call check")
			}

			asmPath := filepath.Join(dir, "hard.s")
			compile(asmPath, "-harden", "-emit", "asm")
			asm, err := os.ReadFile(asmPath)
			if err != nil {
				t.Fatal(err)
			}
			stub := ".quad .Lfern_cfi___fn_twice$wrap__" + strings.TrimPrefix(tc.entry, ".") + "\n"
			if bytes.Count(asm, []byte(stub)) != 1 {
				t.Fatalf("the emitted text holds %d references to the stub of twice, want one:\n%s", bytes.Count(asm, []byte(stub)), stub)
			}
			link := func(name string, body []byte) string {
				t.Helper()
				s := filepath.Join(dir, name+".s")
				bin := filepath.Join(dir, name)
				if err := os.WriteFile(s, body, 0o644); err != nil {
					t.Fatal(err)
				}
				args := []string{"-static", "-nostdlib", s, "-o", bin}
				if tc.target == "x86-64-linux" {
					args = append([]string{"-no-pie"}, args...)
				}
				if msg, err := exec.Command(tc.gcc, args...).CombinedOutput(); err != nil {
					t.Fatalf("link %s: %v\n%s", name, err, msg)
				}
				return bin
			}
			if out, errout, code := run(link("linked", asm)); out != cfiWant || code != 0 {
				t.Fatalf("the linked control printed %q with exit %d (stderr %q)", out, code, errout)
			}
			for _, forged := range []struct{ name, target string }{
				{"real-entry", "__fn_twice$wrap" + tc.entry},
				{"same-signature", "__fn_thrice" + tc.entry},
				{"data", "__fern_cfi_fail_msg"},
			} {
				patched := bytes.Replace(asm, []byte(stub), []byte(".quad "+forged.target+"\n"), 1)
				out, errout, code := run(link(forged.name, patched))
				if code != 134 || !strings.HasPrefix(errout, cfiCause) {
					t.Errorf("%s: exit %d, stdout %q, stderr %q; want exit 134 with the CFI cause", forged.name, code, out, errout)
				}
			}
		})
	}
}
