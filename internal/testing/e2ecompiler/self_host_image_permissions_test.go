package e2ecompiler

import (
	"debug/elf"
	"encoding/binary"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

// Every image variant must keep code and data apart: one read-execute load
// segment, writable segments that are not executable, and a non-executable
// stack. A write into the code mapping and a jump into the data mapping
// must both fault.
const imagePermSrc = `import "std/string";

@noinline
function twice(x: i32): i32 { return x * 2; }

function main(): i32 {
  let f: (i32) => i32 = twice;
  print("ok " + f(21).to_string());
  return 0;
}
`

// The program finds its own executable mapping and stores a byte into it.
const codeWriteSrc = `import "std/string";

function main(): i32 {
  match (read_file("/proc/self/maps")) {
    Ok(maps) => {
      for line in maps.split("\n") {
        if (line.contains(" r-xp ") && line.contains("code-write")) {
          let hex: string = line.split("-")[0];
          let at: i64 = 0i64;
          for c in hex.bytes() {
            let d: i64 = 0i64;
            if (c >= b'a') { d = (c - b'a') as i64 + 10i64; } else { d = (c - b'0') as i64; }
            at = at * 16i64 + d;
          }
          print("writing");
          __store_u8(at as usize, 0);
          print("survived");
          return 3;
        }
      }
      print("no mapping");
      return 2;
    },
    Err(_) => { print("no maps"); return 2; }
  }
}
`

func TestSelfHostImagePermissions(t *testing.T) {
	cli := buildSelfHostCLI(t)
	_, qemu := arm64Tooling(t)
	if len(cli.runner) != 0 {
		t.Skip("requires native x86-64 Linux memory protection")
	}
	variants := []struct {
		name  string
		flags []string
	}{
		{"plain", nil},
		{"pie", []string{"-pie"}},
		{"harden", []string{"-harden"}},
		{"harden-pie", []string{"-harden", "-pie"}},
		{"debug", []string{"-g"}},
		{"sanitize", []string{"-sanitize"}},
		{"backtrace", []string{"-backtrace"}},
	}
	for _, tc := range []struct {
		target  string
		machine elf.Machine
		exit    []byte
	}{
		// mov eax, 60; xor edi, edi; syscall
		{"x86-64-linux", elf.EM_X86_64, []byte{0xb8, 60, 0, 0, 0, 0x31, 0xff, 0x0f, 0x05}},
		// mov x8, #93; mov x0, #0; svc #0
		{"arm64-linux", elf.EM_AARCH64, []byte{0xa8, 0x0b, 0x80, 0xd2, 0x00, 0x00, 0x80, 0xd2, 0x01, 0x00, 0x00, 0xd4}},
	} {
		t.Run(tc.target, func(t *testing.T) {
			dir := t.TempDir()
			write := func(name, src string) string {
				t.Helper()
				path := filepath.Join(dir, name)
				if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
					t.Fatal(err)
				}
				return path
			}
			mainSrc := write("main.fern", imagePermSrc)
			probeSrc := write("probe.fern", codeWriteSrc)
			compile := func(src, out string, flags ...string) {
				t.Helper()
				args := append(append([]string{}, flags...), "-target", tc.target, "-o", out, src, cli.stdlib)
				cmd := runX86_64Bin(cli.runner, cli.bin, args...)
				cmd.Env = childEnv()
				if msg, err := cmd.CombinedOutput(); err != nil {
					t.Fatalf("compile %v: %v\n%s", flags, err, msg)
				}
				if err := os.Chmod(out, 0o755); err != nil {
					t.Fatal(err)
				}
			}
			run := func(bin string) (string, *exec.Cmd) {
				t.Helper()
				var c *exec.Cmd
				if tc.target == "arm64-linux" {
					c = runArm64Bin(qemu, bin)
				} else {
					c = runX86_64Bin(cli.runner, bin)
				}
				out, _ := c.CombinedOutput()
				return string(out), c
			}
			expectSegv := func(what, bin string) {
				t.Helper()
				out, c := run(bin)
				status, ok := c.ProcessState.Sys().(syscall.WaitStatus)
				if !ok || !status.Signaled() || status.Signal() != syscall.SIGSEGV {
					t.Errorf("%s: want SIGSEGV, got %v\n%s", what, c.ProcessState, out)
				}
			}
			for _, v := range variants {
				t.Run(v.name, func(t *testing.T) {
					bin := filepath.Join(dir, v.name)
					compile(mainSrc, bin, v.flags...)
					if out, c := run(bin); !strings.HasPrefix(out, "ok 42\n") || c.ProcessState.ExitCode() != 0 {
						t.Fatalf("printed %q with %v", out, c.ProcessState)
					}
					f, err := elf.Open(bin)
					if err != nil {
						t.Fatal(err)
					}
					defer f.Close()
					if f.Machine != tc.machine {
						t.Fatalf("machine %v, want %v", f.Machine, tc.machine)
					}
					var code, data *elf.Prog
					stack := false
					for _, p := range f.Progs {
						switch p.Type {
						case elf.PT_GNU_STACK:
							stack = true
							if p.Flags&elf.PF_X != 0 {
								t.Error("PT_GNU_STACK asks for an executable stack")
							}
						case elf.PT_LOAD:
							if p.Flags&elf.PF_W != 0 && p.Flags&elf.PF_X != 0 {
								t.Errorf("load segment at %#x is writable and executable", p.Vaddr)
							}
							if p.Flags&elf.PF_X != 0 {
								if code != nil {
									t.Errorf("a second executable load segment at %#x", p.Vaddr)
								}
								code = p
							}
							if p.Flags&elf.PF_W != 0 && data == nil {
								data = p
							}
						}
					}
					if !stack {
						t.Error("no PT_GNU_STACK")
					}
					if code == nil || data == nil {
						t.Fatalf("code %v data %v", code != nil, data != nil)
					}
					if code.Vaddr+code.Memsz > data.Vaddr {
						t.Errorf("code [%#x,%#x) overlaps data at %#x", code.Vaddr, code.Vaddr+code.Memsz, data.Vaddr)
					}

					probe := filepath.Join(dir, "code-write-"+v.name)
					compile(probeSrc, probe, v.flags...)
					if err := os.Rename(probe, filepath.Join(dir, v.name+"-code-write")); err != nil {
						t.Fatal(err)
					}
					expectSegv("write into code", filepath.Join(dir, v.name+"-code-write"))

					image, err := os.ReadFile(bin)
					if err != nil {
						t.Fatal(err)
					}
					if data.Filesz < uint64(len(tc.exit)) {
						t.Fatalf("data segment holds %d file bytes, fewer than the %d-byte probe", data.Filesz, len(tc.exit))
					}
					copy(image[data.Off:], tc.exit)
					binary.LittleEndian.PutUint64(image[24:32], data.Vaddr)
					nx := filepath.Join(dir, v.name+"-execute-data")
					if err := os.WriteFile(nx, image, 0o755); err != nil {
						t.Fatal(err)
					}
					expectSegv("execute from data", nx)
				})
			}
		})
	}
}
