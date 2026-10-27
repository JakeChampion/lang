package e2ecompiler

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// A freed block above one gigabyte returns to the large free list, so the
// second allocation of the same size reuses it instead of advancing the bump.
const allocReuseAboveGigabyteSrc = `import "std/i64";
function touch(n: i32): i32 {
    let b: u8[] = __alloc_u8(n);
    return b.len();
}
function main(): i32 {
    let before: i64 = __heap_bump_bytes();
    let a: i32 = touch(1100000000);
    let b: i32 = touch(1100000000);
    print((__heap_bump_bytes() - before).to_string());
    return 0;
}
`

// A request whose bump sum wraps the address space aborts with the
// allocation size cause instead of returning a pointer below the heap. No
// checked entry passes such a size, so the probe calls the allocator from
// patched assembly with a request of minus eight bytes.
const allocWrappedBumpSrc = `@noinline
function keep(n: i32): i32 { let b: u8[] = __alloc_u8(n + 3); return b.len(); }
function main(): i32 { return keep(args().len()) - 4; }
`

func wrappedAllocCall(target string) string {
	if target == "arm64-linux" {
		return "    mov x0, #-8\n    bl __fern_alloc\n"
	}
	return "    movq $-8, %rdi\n    call __fern_alloc\n"
}

func runAllocProbe(t *testing.T, cli *selfHostCLI, target, src string) (string, string, int) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "probe.fern")
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	var cmd *exec.Cmd
	if target == "arm64-linux" {
		_, qemu := arm64Tooling(t)
		cmd = runArm64Bin(qemu, cli.arm64Binary(t, path))
	} else {
		bin := filepath.Join(t.TempDir(), "prog")
		if msg, err := cli.compileCmd(target, bin, path).CombinedOutput(); err != nil {
			t.Fatalf("compile: %v\n%s", err, msg)
		}
		if err := os.Chmod(bin, 0o755); err != nil {
			t.Fatal(err)
		}
		cmd = runX86_64Bin(cli.runner, bin)
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	_ = cmd.Run()
	return stdout.String(), stderr.String(), cmd.ProcessState.ExitCode()
}

func TestSelfHostAllocReuseAboveOneGigabyte(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, target := range []string{"x86-64-linux", "arm64-linux"} {
		t.Run(target, func(t *testing.T) {
			out, stderr, code := runAllocProbe(t, cli, target, allocReuseAboveGigabyteSrc)
			if code != 0 {
				t.Fatalf("exit %d\nstderr: %s", code, stderr)
			}
			grown, err := strconv.ParseInt(strings.TrimSpace(out), 10, 64)
			if err != nil {
				t.Fatalf("stdout %q is not the bump growth", out)
			}
			if grown >= 2*1100000000 {
				t.Errorf("two 1.1 GB allocations grew the bump by %d bytes: the first block was not reused", grown)
			}
		})
	}
}

func TestSelfHostAllocRefusesWrappedBump(t *testing.T) {
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
			src := filepath.Join(dir, "wrap.fern")
			if err := os.WriteFile(src, []byte(allocWrappedBumpSrc), 0o644); err != nil {
				t.Fatal(err)
			}
			asmPath := filepath.Join(dir, "wrap.s")
			args := []string{"-emit", "asm", "-target", target, "-o", asmPath, src, cli.stdlib}
			cmd := runX86_64Bin(cli.runner, cli.bin, args...)
			cmd.Env = childEnv()
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("self-host CLI %v: %v\n%s", args, err, out)
			}
			raw, err := os.ReadFile(asmPath)
			if err != nil {
				t.Fatal(err)
			}
			text := string(raw)
			label := "\n__fn_keep.r:\n"
			at := strings.Index(text, label)
			if at < 0 {
				t.Fatalf("no __fn_keep.r in the emitted assembly")
			}
			at += len(label)
			text = text[:at] + wrappedAllocCall(target) + text[at:]
			patched := filepath.Join(dir, "wrap_patched.s")
			if err := os.WriteFile(patched, []byte(text), 0o644); err != nil {
				t.Fatal(err)
			}
			bin := filepath.Join(dir, "wrap")
			if out, err := exec.Command(gcc, "-static", "-nostdlib", "-no-pie", patched, "-o", bin).CombinedOutput(); err != nil {
				t.Fatalf("link: %v\n%s", err, out)
			}
			c := run(bin)
			var stderr bytes.Buffer
			c.Stderr = &stderr
			_ = c.Run()
			code := c.ProcessState.ExitCode()
			if code != 134 || !strings.Contains(stderr.String(), "fern: allocation size out of range") {
				t.Fatalf("exit %d, stderr %q; want 134 with the allocation size cause", code, stderr.String())
			}
		})
	}
}
