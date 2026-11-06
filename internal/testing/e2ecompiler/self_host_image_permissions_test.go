package e2ecompiler

import (
	"debug/elf"
	"encoding/binary"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
)

// Every image variant must keep code and data apart: one read-execute load
// segment, writable segments that are not executable, and a non-executable
// stack. The constant data leads the data segment under PT_GNU_RELRO, ends
// on a page, and the startup maps it read-only. A write into the code mapping, a write into
// the read-only data mapping and a jump into the data mapping must all fault.
const imagePermSrc = `import "std/string";

@noinline
function twice(x: i32): i32 { return x * 2; }

function main(): i32 {
  let f: (i32) => i32 = twice;
  let zeroes: i32[] = [0, 0, 0, 0];
  print("ok " + f(21 + zeroes[args().len() - 1]).to_string());
  return 0;
}
`

// The program finds its own executable mapping and stores a byte into it.
const codeWriteSrc = `import "std/string";

function main(): i32 {
  unsafe {
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
}
`

// The program finds the read-only mapping of its own data segment and stores
// a byte into it. The code mapping is the only other read-only mapping of the
// image, and it is executable.
var relroWriteSrc = strings.NewReplacer(` r-xp `, ` r--p `, "code-write", "relro-write").Replace(codeWriteSrc)

func TestSelfHostImagePermissions(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("requires Linux memory protection")
	}
	cli := selfHostCLIForHost(t)
	variants := []struct {
		name       string
		flags      []string
		largePages bool
	}{
		{"plain", nil, false},
		{"pie", []string{"-pie"}, false},
		{"harden", []string{"-harden"}, false},
		{"harden-pie", []string{"-harden", "-pie"}, false},
		{"debug", []string{"-g"}, false},
		{"sanitize", []string{"-sanitize"}, false},
		{"backtrace", []string{"-backtrace"}, false},
		{"pie-64k", []string{"-harden", "-pie"}, true},
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
		{"arm64-android", elf.EM_AARCH64, []byte{0xa8, 0x0b, 0x80, 0xd2, 0x00, 0x00, 0x80, 0xd2, 0x01, 0x00, 0x00, 0xd4}},
	} {
		t.Run(tc.target, func(t *testing.T) {
			var runner []string
			available := false
			for _, target := range cli.targets {
				if target.target == tc.target || (tc.target == "arm64-android" && target.target == "arm64-linux") {
					runner, available = target.runner, true
				}
			}
			if !available {
				t.Skip("target runner not available")
			}
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
			emptySrc := write("empty.fern", "function main(): i32 { return 0; }\n")
			probeSrc := write("probe.fern", codeWriteSrc)
			relroSrc := write("relro.fern", relroWriteSrc)
			compile := func(t *testing.T, src, out string, flags ...string) {
				t.Helper()
				args := append(append([]string{}, flags...), "-target", tc.target, "-o", out, src, cli.stdlib)
				cmd := exec.Command(cli.cli, args...)
				cmd.Env = childEnv()
				if msg, err := cmd.CombinedOutput(); err != nil {
					t.Fatalf("compile %v: %v\n%s", flags, err, msg)
				}
				if err := os.Chmod(out, 0o755); err != nil {
					t.Fatal(err)
				}
			}
			command := func(bin string, override []string) *exec.Cmd {
				selected := runner
				if len(override) != 0 {
					selected = override
				}
				if len(selected) != 0 {
					return exec.Command(selected[0], append(append([]string{}, selected[1:]...), bin)...)
				}
				return exec.Command(bin)
			}
			run := func(bin string, override []string) (string, *exec.Cmd) {
				t.Helper()
				c := command(bin, override)
				out, _ := c.CombinedOutput()
				return string(out), c
			}
			expectSegv := func(t *testing.T, what, bin string, override []string) {
				t.Helper()
				c := withoutCoreDump(command(bin, override))
				out, _ := c.CombinedOutput()
				status, ok := c.ProcessState.Sys().(syscall.WaitStatus)
				if !ok || !status.Signaled() || status.Signal() != syscall.SIGSEGV {
					t.Errorf("%s: want SIGSEGV, got %v\n%s", what, c.ProcessState, out)
				}
			}
			for _, v := range variants {
				// Android always emits PIE through its own executable path.
				if tc.target == "arm64-android" && v.name != "plain" && v.name != "pie-64k" {
					continue
				}
				if tc.target == "arm64-android" {
					v.flags = nil
				}
				t.Run(v.name, func(t *testing.T) {
					var override []string
					if v.largePages {
						if tc.machine != elf.EM_AARCH64 {
							t.Skip("large-page control uses the ARM64 target")
						}
						qemu, err := exec.LookPath("qemu-aarch64")
						if err != nil {
							t.Skip("qemu-aarch64 not on PATH")
						}
						override = []string{qemu, "-p", "65536"}
					}
					empty := filepath.Join(dir, v.name+"-empty")
					compile(t, emptySrc, empty, v.flags...)
					// A -sanitize build prints its leak census at exit; an empty program's census is balanced.
					if out, c := run(empty, override); (out != "" && out != "leakcheck: allocs=0 frees=0 live_bytes=0\n") || c.ProcessState.ExitCode() != 0 {
						t.Fatalf("empty image printed %q with %v", out, c.ProcessState)
					}
					emptyImage, err := elf.Open(empty)
					if err != nil {
						t.Fatal(err)
					}
					for _, p := range emptyImage.Progs {
						if p.Type == elf.PT_GNU_RELRO && p.Memsz != 0 {
							t.Errorf("empty image has %d bytes of constant data", p.Memsz)
						}
					}
					emptyImage.Close()
					bin := filepath.Join(dir, v.name)
					compile(t, mainSrc, bin, v.flags...)
					if out, c := run(bin, override); !strings.HasPrefix(out, "ok 42\n") || c.ProcessState.ExitCode() != 0 {
						t.Fatalf("printed %q with %v", out, c.ProcessState)
					}
					f, err := elf.Open(bin)
					if err != nil {
						t.Fatal(err)
					}
					defer f.Close()
					if tc.target == "arm64-android" && f.Type != elf.ET_DYN {
						t.Fatalf("Android image type %v, want ET_DYN", f.Type)
					}
					if f.Machine != tc.machine {
						t.Fatalf("machine %v, want %v", f.Machine, tc.machine)
					}
					var code, data, relro, readonly *elf.Prog
					stack := false
					for _, p := range f.Progs {
						switch p.Type {
						case elf.PT_GNU_RELRO:
							relro = p
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
							if p.Flags&elf.PF_W != 0 {
								data = p
							}
							if p.Flags == elf.PF_R {
								readonly = p
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
					if relro == nil {
						t.Fatal("no PT_GNU_RELRO")
					}
					if f.Type == elf.ET_EXEC {
						if readonly == nil || relro.Memsz == 0 || relro.Vaddr != readonly.Vaddr || relro.Memsz != readonly.Filesz {
							t.Fatalf("constant data has no matching read-only load segment: %v", readonly)
						}
						pageEnd := (readonly.Vaddr + readonly.Memsz + data.Align - 1) & ^(data.Align - 1)
						if pageEnd > data.Vaddr & ^(data.Align-1) {
							t.Error("read-only and writable virtual pages overlap")
						}
					} else {
						var relocated *elf.Prog
						for _, p := range f.Progs {
							if p.Type == elf.PT_LOAD && p.Vaddr == relro.Vaddr && p.Flags == elf.PF_R|elf.PF_W {
								relocated = p
							}
						}
						if relocated == nil || relocated == data || relro.Memsz == 0 || relro.Memsz != relocated.Memsz || (relro.Vaddr+relro.Memsz)%4096 != 0 {
							t.Fatal("PIE has no separate mapped RELRO range with a page-aligned end")
						}
						if relocated.Memsz <= relocated.Filesz {
							t.Error("PIE does not use mapped zero-fill space after its constants")
						}
						if data.Off != relocated.Off+relocated.Filesz {
							t.Error("PIE stores its virtual constant gap in the file")
						}
						pageEnd := (relocated.Vaddr + relocated.Filesz + data.Align - 1) & ^(data.Align - 1)
						if pageEnd > data.Vaddr & ^(data.Align-1) {
							t.Error("PIE constant bytes and writable globals share virtual pages")
						}
					}

					probe := filepath.Join(dir, "code-write-"+v.name)
					if v.largePages {
						// QEMU can omit file names from large-page mappings.
						// Store to the current instruction, then exit if it succeeds.
						image, err := os.ReadFile(bin)
						if err != nil {
							t.Fatal(err)
						}
						entry := code.Off + f.Entry - code.Vaddr
						words := []uint32{0x10000000, 0x3900001f, 0xd2800060, 0xd2800ba8, 0xd4000001}
						if entry+uint64(len(words)*4) > code.Off+code.Filesz {
							t.Fatal("entry has no space for the code-write probe")
						}
						for i, word := range words {
							binary.LittleEndian.PutUint32(image[entry+uint64(i*4):], word)
						}
						if err := os.WriteFile(probe, image, 0o755); err != nil {
							t.Fatal(err)
						}
					} else {
						compile(t, probeSrc, probe, v.flags...)
					}
					if err := os.Rename(probe, filepath.Join(dir, v.name+"-code-write")); err != nil {
						t.Fatal(err)
					}
					expectSegv(t, "write into code", filepath.Join(dir, v.name+"-code-write"), override)

					relroProbe := filepath.Join(dir, "relro-write-"+v.name)
					selectedRelroSrc := relroSrc
					if v.largePages {
						selectedRelroSrc = write("direct-relro.fern", `function main(): i32 {
  unsafe { let s = "protected constant"; __store_u8(s as usize, 0); }
  return 3;
}`)
					}
					compile(t, selectedRelroSrc, relroProbe, v.flags...)
					if err := os.Rename(relroProbe, filepath.Join(dir, v.name+"-relro-write")); err != nil {
						t.Fatal(err)
					}
					expectSegv(t, "write into the read-only data", filepath.Join(dir, v.name+"-relro-write"), override)

					image, err := os.ReadFile(bin)
					if err != nil {
						t.Fatal(err)
					}
					// A plain program has no writable file bytes, so the probe
					// then executes from the read-only data segment instead.
					target := data
					if target.Filesz < uint64(len(tc.exit)) && readonly != nil {
						target = readonly
					}
					if target.Filesz < uint64(len(tc.exit)) {
						t.Fatalf("data segment holds %d file bytes, fewer than the %d-byte probe", target.Filesz, len(tc.exit))
					}
					copy(image[target.Off:], tc.exit)
					binary.LittleEndian.PutUint64(image[24:32], target.Vaddr)
					nx := filepath.Join(dir, v.name+"-execute-data")
					if err := os.WriteFile(nx, image, 0o755); err != nil {
						t.Fatal(err)
					}
					expectSegv(t, "execute from data", nx, override)
				})
			}
		})
	}
}
