package e2ecompiler

import (
	"bytes"
	"debug/elf"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

// The program touches every static kind a relocation must move: string
// literal boxes, a constant aggregate, a function value, a trait object
// table, an enum shape compared in a match, and a runtime-module error
// path with its own literals. It prints the image's own text mapping so a
// test can see the base the kernel chose.
const pieSrc = `import "std/string";
struct P { a: i32, b: i32 }
trait Shape { function area(self: Self): i32; }
struct Sq { s: i32 }
impl Shape for Sq { function area(self: Self): i32 { return self.s * self.s; } }
enum Kind { Circle(i32), Square(i32, i32), Empty }
function dbl(x: i32): i32 { return x * 2; }
@noinline function pick(): (i32) => i32 { return dbl; }
@noinline function ks(): string[] { return ["ab", "c,d"]; }
@noinline function mk(): P { return P { a: 5, b: 9 }; }
@noinline function kind(k: i32): Kind {
  if (k == 1) { return Kind.Circle(5); }
  if (k == 2) { return Kind.Square(2, 3); }
  return Kind.Empty;
}
function main(): i32 {
  let s: string = "hello, " + "world!";
  let k: string[] = ks();
  let d: dyn Shape = Sq { s: 7 };
  let f: (i32) => i32 = pick();
  let p: P = mk();
  print(s + " " + k[1] + " " + d.area().to_string() + " " + f(p.a + p.b).to_string());
  match (kind(args().len())) {
    Kind.Circle(r) => { print("circle " + r.to_string()); },
    Kind.Square(a, b) => { print("square " + (a * b).to_string()); },
    Kind.Empty => { print("empty"); }
  }
  match (read_file("/nonexistent/fern-pie")) {
    Ok(_) => { print("read?"); },
    Err(e) => {
      match (e) {
        NotFound(path) => { print("not found " + path); },
        _ => { print("other " + e.message()); }
      }
    }
  }
  match (read_file("/proc/self/maps")) {
    Ok(m) => {
      for line in m.split("\n") {
        if (line.contains("r-xp") && line.contains("/pie-")) { print("text " + line.split("-")[0]); }
      }
    },
    Err(_) => { print("no maps"); }
  }
  return 0;
}
`

const pieWant = "hello, world! c,d 49 28\ncircle 5\nnot found /nonexistent/fern-pie\n"

// pieLayout checks the headers a position-independent executable must carry:
// ET_DYN at base 0, no interpreter, a non-executable stack, and no segment
// that is both writable and executable.
func pieLayout(t *testing.T, bin string, machine elf.Machine) {
	t.Helper()
	f, err := elf.Open(bin)
	if err != nil {
		t.Fatalf("open %s: %v", bin, err)
	}
	defer f.Close()
	if f.Type != elf.ET_DYN || f.Machine != machine {
		t.Fatalf("%s: type %v machine %v, want ET_DYN %v", bin, f.Type, f.Machine, machine)
	}
	var loads []*elf.Prog
	var relro *elf.Prog
	stack := false
	for _, p := range f.Progs {
		switch p.Type {
		case elf.PT_INTERP, elf.PT_DYNAMIC:
			t.Errorf("%s: a static image carries %v", bin, p.Type)
		case elf.PT_GNU_STACK:
			stack = true
			if p.Flags&elf.PF_X != 0 {
				t.Errorf("%s: the stack is executable", bin)
			}
		case elf.PT_GNU_RELRO:
			relro = p
		case elf.PT_LOAD:
			if len(loads) == 0 && p.Vaddr != 0 {
				t.Errorf("%s: the first load maps at %#x, want 0", bin, p.Vaddr)
			}
			if p.Flags&elf.PF_W != 0 && p.Flags&elf.PF_X != 0 {
				t.Errorf("%s: a load segment is writable and executable", bin)
			}
			loads = append(loads, p)
		}
	}
	if len(loads) != 3 || !stack {
		t.Fatalf("%s: %d load segments, stack header %t; want 3 and true", bin, len(loads), stack)
	}
	constants, globals := loads[1], loads[2]
	if constants.Flags != elf.PF_R|elf.PF_W || globals.Flags != elf.PF_R|elf.PF_W {
		t.Errorf("%s: constants and globals must start writable and non-executable", bin)
	}
	if relro == nil || relro.Vaddr != constants.Vaddr || relro.Off != constants.Off || relro.Memsz != constants.Memsz || relro.Memsz == 0 {
		t.Fatalf("%s: no matching constant load and protected range", bin)
	}
	if constants.Filesz == 0 || constants.Memsz <= constants.Filesz || constants.Vaddr+constants.Memsz > globals.Vaddr {
		t.Errorf("%s: constants have no separate zero-fill space before globals", bin)
	}
	if globals.Off != constants.Off+constants.Filesz {
		t.Errorf("%s: the image stores the virtual constant gap in the file", bin)
	}
}

// The test runs the host's own compiler, so it runs on an arm64 host too.
// Each target runs natively where it can, and under an emulator otherwise.
func TestSelfHostPIE(t *testing.T) {
	cli := e2eharness.SelfHostCLI(t)
	stdlib := e2eharness.SelfHostStdlibRoot(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "main.fern")
	if err := os.WriteFile(src, []byte(pieSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	compile := func(t *testing.T, out string, flags ...string) (string, int) {
		t.Helper()
		args := append(append([]string{}, flags...), "-o", out, src, stdlib)
		cmd := exec.Command(cli, args...)
		cmd.Env = childEnv()
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		_ = cmd.Run()
		return stderr.String(), cmd.ProcessState.ExitCode()
	}
	run := func(t *testing.T, runner []string, bin string) string {
		t.Helper()
		c := runX86_64Bin(runner, bin)
		var stdout, stderr bytes.Buffer
		c.Stdout, c.Stderr = &stdout, &stderr
		if err := c.Run(); err != nil {
			t.Fatalf("%s: %v\nstdout: %s\nstderr: %s", bin, err, stdout.String(), stderr.String())
		}
		if !strings.HasPrefix(stdout.String(), pieWant) {
			t.Fatalf("%s printed %q, want the prefix %q", bin, stdout.String(), pieWant)
		}
		return strings.TrimPrefix(strings.TrimSpace(strings.TrimPrefix(stdout.String(), pieWant)), "text ")
	}
	for _, tc := range []struct {
		target  string
		machine elf.Machine
	}{{"x86-64-linux", elf.EM_X86_64}, {"arm64-linux", elf.EM_AARCH64}} {
		t.Run(tc.target, func(t *testing.T) {
			var runner []string
			if tc.target == "arm64-linux" {
				if qemu := e2eharness.Arm64Runner(t); qemu != "" {
					runner = []string{qemu}
				}
			} else {
				runner = e2eharness.X86_64Runner(t)
			}
			bin := filepath.Join(dir, "pie-"+tc.target)
			if errout, code := compile(t, bin, "-pie", "-target", tc.target); code != 0 {
				t.Fatalf("compile -pie: exit %d\n%s", code, errout)
			}
			pieLayout(t, bin, tc.machine)
			base1 := run(t, runner, bin)
			if base1 == "" || base1 == "00400000" {
				t.Fatalf("the text mapping is at %q, want a base the loader chose", base1)
			}
			t.Logf("%s text mapping at %s", tc.target, base1)
			if len(runner) > 0 {
				t.Logf("%s runs under an emulator here, so the kernel's randomization is not observed", tc.target)
			} else if policy, _ := os.ReadFile("/proc/sys/kernel/randomize_va_space"); strings.TrimSpace(string(policy)) == "0" {
				t.Logf("randomize_va_space is 0 on this host, so the second run cannot show another base")
			} else if base2 := run(t, runner, bin); base2 == base1 {
				t.Errorf("two runs mapped the text at %s both times; the kernel did not randomize the base", base1)
			} else {
				t.Logf("%s: the kernel randomized the base (native): %s, then %s", tc.target, base1, base2)
			}
			hardened := filepath.Join(dir, "pie-harden-"+tc.target)
			if errout, code := compile(t, hardened, "-pie", "-harden", "-target", tc.target); code != 0 {
				t.Fatalf("compile -pie -harden: exit %d\n%s", code, errout)
			}
			pieLayout(t, hardened, tc.machine)
			run(t, runner, hardened)
			symbols := filepath.Join(dir, "pie-g-"+tc.target)
			if errout, code := compile(t, symbols, "-pie", "-g", "-target", tc.target); code != 0 {
				t.Fatalf("compile -pie -g: exit %d\n%s", code, errout)
			}
			run(t, runner, symbols)
			f, err := elf.Open(symbols)
			if err != nil {
				t.Fatal(err)
			}
			syms, err := f.Symbols()
			f.Close()
			if err != nil {
				t.Fatalf("symbols of the -g image: %v", err)
			}
			found := false
			for _, sym := range syms {
				if sym.Name == "__fn_main" && sym.Value != 0 {
					found = true
				}
			}
			if !found {
				t.Errorf("the -pie -g image has no __fn_main symbol")
			}
		})
	}
	t.Run("refusals", func(t *testing.T) {
		for _, tc := range []struct {
			flags []string
			want  string
		}{
			{[]string{"-pie", "-shared", "-target", "x86-64-linux"}, "fern: -pie is only supported for -target arm64-linux and x86-64-linux executables (got \"x86-64-linux\" with -shared)"},
			{[]string{"-pie", "-target", "wasm32-wasi"}, "fern: -pie is only supported for -target arm64-linux and x86-64-linux executables (got \"wasm32-wasi\")"},
			{[]string{"-pie", "-target", "arm64-darwin"}, "fern: -pie is only supported for -target arm64-linux and x86-64-linux executables (got \"arm64-darwin\")"},
		} {
			errout, code := compile(t, filepath.Join(t.TempDir(), "out"), tc.flags...)
			if code != 2 || !strings.HasPrefix(errout, tc.want) {
				t.Errorf("%v: exit %d, stderr %q; want 2 and %q", tc.flags, code, errout, tc.want)
			}
		}
	})
}
