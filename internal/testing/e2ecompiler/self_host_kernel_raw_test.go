package e2ecompiler

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

// The raw-pointer surface (#14256): the image takes a frame and stores and
// loads it through std/raw at every width. Each value's bytes land where the
// little-endian layout puts them, a byte store changes one byte of a word,
// and an address round-trips through store_usize and load_usize.
const rawProbeSrc = `// Stores a frame at every width and reads the bytes back.
function probe(a: usize): string {
  raw.store_u64(a, 0x1122334455667788i64 as u64);
  let first = "u32 low " + (raw.load_u32(a) == 0x55667788 as u32).to_string() + " u16 at 4 " + (raw.load_u16(raw.offset(a, 4 as usize)) == 0x3344 as u32).to_string() + " u8 at 7 " + (raw.load_u8(raw.offset(a, 7 as usize)) == 0x11 as u8).to_string();
  raw.store_u8(raw.offset(a, 1 as usize), 0xff as u8);
  raw.store_u16(raw.offset(a, 8 as usize), 0xbeef as u32);
  raw.store_u32(raw.offset(a, 12 as usize), 0x7eadbeef as u32);
  let second = "u64 after byte store " + (raw.load_u64(a) == 0x112233445566ff88i64 as u64).to_string() + " bytes of u16 " + (raw.load_u8(raw.offset(a, 8 as usize)) == 0xef as u8 && raw.load_u8(raw.offset(a, 9 as usize)) == 0xbe as u8).to_string() + " u64 at 8 " + (raw.load_u64(raw.offset(a, 8 as usize)) == 0x7eadbeef0000beefi64 as u64).to_string();
  raw.store_usize(raw.offset(a, 16 as usize), a);
  return first + "\n" + second + "\nusize round trip " + (raw.load_usize(raw.offset(a, 16 as usize)) == a).to_string() + "\nmmio round trip " + mmio(raw.offset(a, 32 as usize)).to_string();
}

// Stores and loads at every width through the MMIO intrinsics, which the
// mmio capability grants a kernel. The 32-bit load sign-extends.
function mmio(p: usize): boolean {
  unsafe {
    __mmio_store8(p, 200);
    __mmio_store16(p + 2 as usize, 40000);
    __mmio_store32(p + 4 as usize, -5);
    __mmio_store64(p + 8 as usize, 1234567890123i64);
    let widths = __mmio_load8(p) == 200 && __mmio_load16(p + 2 as usize) == 40000 && __mmio_load32(p + 4 as usize) == -5 && __mmio_load64(p + 8 as usize) == 1234567890123i64;
    return widths && __load_u8(p + 2 as usize) == 64 && __load_u8(p + 3 as usize) == 156;
  }
}
`

const rawMainSrc = `import "./board";
import "std/frames";
import "std/option";
import "std/raw";

` + rawProbeSrc + `
function main(): i32 {
  // The frames start above the bootstrap heap's cursor, and end at the stack.
  let top = board.RAM_BASE + board.RAM_SIZE - board.STACK_BYTES;
  let cursor = 0 as usize;
  unsafe {
    cursor = (__heap_mark() as usize) + 65536 as usize;
  }
  let Some(f) = frames.new(cursor, top - cursor) else {
    print("no frames");
    return 1;
  };
  let Some(a) = f.alloc() else {
    print("no frame");
    return 1;
  };
  print(probe(a));
  print("frame back " + f.free(a).to_string());
  return 0;
}
`

const rawX86MainSrc = `import "./board";
import "std/frames";
import "std/option";
import "std/raw";

` + rawProbeSrc + `
function main(): i32 {
  // The frames start above the bootstrap heap's cursor, and end at the stack.
  let top = board.RAM_BASE + board.RAM_SIZE - board.STACK_BYTES;
  let cursor = 0 as usize;
  unsafe {
    cursor = (__heap_mark() as usize) + 65536 as usize;
  }
  let Some(f) = frames.new(cursor, top - cursor) else {
    print("no frames");
    return 1;
  };
  let Some(a) = f.alloc() else {
    print("no frame");
    return 1;
  };
  print(probe(a));
  print("frame back " + f.free(a).to_string());
  return 16;
}
`

var rawWantLines = []string{
	"u32 low true u16 at 4 true u8 at 7 true\n",
	"u64 after byte store true bytes of u16 true u64 at 8 true\n",
	"usize round trip true\n",
	"mmio round trip true\n",
	"frame back true\n",
}

func TestSelfHostKernelRawOnQemu(t *testing.T) {
	qemu := e2eharness.QemuSystem(t, "qemu-system-aarch64")
	cli := buildSelfHostCLI(t)
	src := writeTickProgram(t, dmaBoardSrc, rawMainSrc)
	for _, c := range []struct {
		name string
		env  []string
	}{{"plain", nil}, {"leakcheck", []string{"FERN_LEAKCHECK=1"}}} {
		t.Run(c.name, func(t *testing.T) {
			out := filepath.Join(t.TempDir(), "kernel.elf")
			cmd := cli.compileCmd("arm64-kernel", out, src, "-ram", kernelRAM)
			cmd.Env = append(os.Environ(), c.env...)
			if msg, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("build: %v\n%s", err, msg)
			}
			console, err := bootQemu(t, qemu, qemuVirtArgs(out)...)
			if err != nil {
				t.Fatalf("qemu: %v; console:\n%s", err, console)
			}
			for _, want := range rawWantLines {
				if !strings.Contains(string(console), want) {
					t.Errorf("console:\n%s\nwant %q", console, want)
				}
			}
			if c.env != nil {
				wantCensus(t, console)
			}
		})
	}
}

// main returns 16, so qemu exits with 33 from isa-debug-exit.
func TestSelfHostKernelX86RawOnQemu(t *testing.T) {
	qemu := e2eharness.QemuSystem(t, "qemu-system-x86_64")
	cli := buildSelfHostCLI(t)
	src := writeTickProgram(t, dmaX86BoardSrc, rawX86MainSrc)
	for _, c := range []struct {
		name string
		env  []string
	}{{"plain", nil}, {"leakcheck", []string{"FERN_LEAKCHECK=1"}}} {
		t.Run(c.name, func(t *testing.T) {
			out := filepath.Join(t.TempDir(), "kernel.elf")
			cmd := cli.compileCmd("x86-64-kernel", out, src, "-ram", kernelX86RAM)
			cmd.Env = append(os.Environ(), c.env...)
			if msg, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("build: %v\n%s", err, msg)
			}
			console, err := bootQemu(t, qemu, "-M", "microvm", "-cpu", "max", "-m", "128M",
				"-nodefaults", "-no-user-config", "-nographic", "-no-reboot", "-serial", "stdio",
				"-device", "isa-debug-exit,iobase=0xf4,iosize=4", "-kernel", out, "-append", "fern=pvh")
			var exit *exec.ExitError
			if !errors.As(err, &exit) || exit.ExitCode() != 33 {
				t.Fatalf("qemu: %v, want exit status 33 from isa-debug-exit; console:\n%s", err, console)
			}
			for _, want := range rawWantLines {
				if !strings.Contains(string(console), want) {
					t.Errorf("console:\n%s\nwant %q", console, want)
				}
			}
			if c.env != nil {
				wantCensus(t, console)
			}
		})
	}
}

// A hosted target does not grant the privileged capability, so a program that
// reads raw memory through std/raw fails with E066 naming the intrinsic, in
// the self-host compiler and in the Go checker alike.
func TestSelfHostRawMemoryNeedsAKernelTarget(t *testing.T) {
	cli := buildSelfHostCLI(t)
	src := filepath.Join(t.TempDir(), "entry.fern")
	const program = `import "std/raw";

function main(): i32 {
  return raw.load_u32(4096 as usize) as i32;
}
`
	if err := os.WriteFile(src, []byte(program), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{"x86-64-linux", "arm64-linux", "arm64-darwin", "wasm32-wasi"} {
		out, err := cli.compileCmd(target, filepath.Join(t.TempDir(), "program"), src).CombinedOutput()
		if err == nil || !strings.Contains(string(out), "E066") || !strings.Contains(string(out), "__mem_load32") {
			t.Errorf("%s: want E066 naming __mem_load32, got err=%v\n%s", target, err, out)
		}
	}
	oracle, err := exec.Command(buildFernCLIBin(t), "-check", "-target", "x86-64-linux", src).CombinedOutput()
	if err == nil || !strings.Contains(string(oracle), "E066") || !strings.Contains(string(oracle), "__mem_load32") {
		t.Errorf("Go checker: want E066 naming __mem_load32, got err=%v\n%s", err, oracle)
	}
	if out, err := exec.Command(buildFernCLIBin(t), "-check", "-target", "arm64-kernel", src).CombinedOutput(); err != nil {
		t.Errorf("Go checker on arm64-kernel: %v\n%s", err, out)
	}
}

// A dependency reaches the privileged capability through std/raw, so it needs
// the privileged grant in its fern.toml entry. Both compilers report E070
// without it and build with it.
func TestSelfHostRawMemoryNeedsThePrivilegedGrant(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("package capability differential runs only natively (argv paths)")
	}
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "fern.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "fern.fern", "fern")
	oracleBin := buildFernCLIBin(t)
	stdlib, err := filepath.Abs(filepath.Join("..", "..", "stdlib"))
	if err != nil {
		t.Fatalf("stdlib path: %v", err)
	}
	const readsRaw = "import \"std/raw\";\npub function save(s: string): i32 {\n  return raw.load_u32(4096 as usize) as i32 + s.len();\n}\n"
	const denied = "error helper privileged __mem_load32 save → load_u32 → __mem_load32 lib.fern:2:5"
	for _, c := range []struct {
		name      string
		depEntry  string
		wantBuild bool
		wantE070  string
	}{
		{"granted", "helper = { path = \"../helper\", capabilities = [\"privileged\"] }", true, ""},
		{"empty-grant", "helper = { path = \"../helper\", capabilities = [] }", false, denied},
	} {
		t.Run(c.name, func(t *testing.T) {
			proj := t.TempDir()
			entry := writeCapsProject(t, proj, c.depEntry, readsRaw)

			oracleCmd := exec.Command(oracleBin, "-check", "-target", "arm64-kernel", entry)
			oracleOut, _ := oracleCmd.CombinedOutput()
			oracleBuilt := oracleCmd.ProcessState.ExitCode() == 0

			shCmd := exec.Command(driverBin, "-target", "arm64-kernel", "-ram", kernelRAM, "-o", filepath.Join(proj, "kernel.elf"), entry, stdlib)
			shOut, _ := shCmd.CombinedOutput()
			shBuilt := shCmd.ProcessState.ExitCode() == 0

			if oracleBuilt != c.wantBuild {
				t.Fatalf("Go built = %v, want %v\n%s", oracleBuilt, c.wantBuild, oracleOut)
			}
			if shBuilt != oracleBuilt {
				t.Errorf("Go built = %v, self-host built = %v\n--- Go ---\n%s\n--- self-host ---\n%s",
					oracleBuilt, shBuilt, oracleOut, shOut)
			}
			want := capsDiagnostics(string(oracleOut))
			got := capsDiagnostics(string(shOut))
			if strings.Join(want, "; ") != c.wantE070 {
				t.Errorf("Go's E070 = %v, want %q\n%s", want, c.wantE070, oracleOut)
			}
			if strings.Join(want, "; ") != strings.Join(got, "; ") {
				t.Errorf("capability diagnostics differ:\nGo        %v\nself-host %v\n--- Go ---\n%s\n--- self-host ---\n%s",
					want, got, oracleOut, shOut)
			}
		})
	}
}
