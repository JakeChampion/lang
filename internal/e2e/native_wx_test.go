// W^X native-backend end-to-end tests: build real code-generator output
// through BOTH the original single-segment R+W+X image (AssembleProgram +
// StaticExecutableData) and the W^X two-segment image (AssembleProgramWX +
// StaticExecutableDataWX), run both, and assert identical behaviour. This
// is the behaviour-equivalence gate for the W^X layout cmd/fern now emits:
// the page-aligned data segment (a separate R+W PT_LOAD) must resolve
// adrp/:lo12: (arm64) and rip-relative (x86-64) data references exactly as
// the contiguous layout did — across rodata strings, .quad function-pointer
// tables (closures), heap allocation (maps), and floats.
//
// W^X matters for loaders that reject writable+executable mappings —
// notably Android's SELinux policy — so these targets gain a layout that
// runs there unchanged.
package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/jakechampion/lang/internal/checker"
	x86codegen "github.com/jakechampion/lang/internal/codegen/x86_64"
	"github.com/jakechampion/lang/internal/constfold"
	"github.com/jakechampion/lang/internal/modload"
	"github.com/jakechampion/lang/internal/monomorph"
	na "github.com/jakechampion/lang/internal/native/arm64"
	nativeelf "github.com/jakechampion/lang/internal/native/elf"
	nativex86 "github.com/jakechampion/lang/internal/native/x86_64"
)

// wxCases are programs exercising the data-addressing surface the W^X
// split touches: rodata strings, .quad function-pointer tables (closures),
// heap allocation (maps), and floats. Each is checked for identical
// (stdout, exit) under the contiguous and W^X layouts, and for the
// expected exit code.
var wxCases = []struct {
	name string
	src  string
	exit int
	out  string
}{
	{"exit", `function main(): i32 { return 42; }`, 42, ""},
	{"factorial", `function factorial(n: i32): i32 { if (n == 0) { return 1; } return n * factorial(n - 1); }
function main(): i32 { return factorial(5); }`, 120, ""},
	{"string", `function main(): i32 { print("hello W^X"); return 0; }`, 0, "hello W^X\n"},
	{"concat", `import "std/i32"; function main(): i32 { print("x=" + (42).to_string()); return 0; }`, 0, "x=42\n"},
	{"closure", `function makeAdder(n: i32): (i32) => i32 { function add(x: i32): i32 { return x + n; } return add; }
function main(): i32 { var add5 = makeAdder(5); return add5(37); }`, 42, ""},
	{"map", `import "core/map";
function main(): i32 {
  var m: Map[i32, i32] = map_new(8);
  m = m.insert(7, 40);
  m = m.insert(7, 42);
  match (m.get(7)) { Some(v) => { return v; }, None => { return 3; } }
}`, 42, ""},
	{"float", `function main(): i32 { var a: f64 = 84.0; var b: f64 = 2.0; return ((a / b) as i32); }`, 42, ""},
}

// TestX86_64NativeWX builds each wxCase with the x86-64 code generator and
// runs it through both the contiguous and W^X images, asserting identical
// behaviour and the expected result. On amd64 the binaries run directly;
// elsewhere a qemu-x86_64 would be needed (SKIP — see x86NativeRunner).
func TestX86_64NativeWX(t *testing.T) {
	runner := x86NativeRunner(t) // SKIPs if neither native amd64 nor qemu-x86_64
	for _, c := range wxCases {
		t.Run(c.name, func(t *testing.T) {
			asm := compileToX86Asm(t, c.src)
			contig := buildX86(t, asm, false)
			wx := buildX86(t, asm, true)

			cOut, cCode := runWXBin(t, runner, contig)
			wOut, wCode := runWXBin(t, runner, wx)
			if wOut != cOut || wCode != cCode {
				t.Fatalf("W^X diverged: contiguous=(%q,%d) wx=(%q,%d)", cOut, cCode, wOut, wCode)
			}
			if wCode != c.exit || wOut != c.out {
				t.Fatalf("W^X result = (%q,%d), want (%q,%d)", wOut, wCode, c.out, c.exit)
			}
		})
	}
}

// TestArm64NativeWX is the arm64 counterpart of TestX86_64NativeWX. Runs the
// binaries natively on an arm64 host, under qemu-aarch64 elsewhere.
func TestArm64NativeWX(t *testing.T) {
	qemu := arm64QemuOrEmpty(t)
	var runner []string
	if qemu != "" {
		runner = []string{qemu}
	}
	for _, c := range wxCases {
		t.Run(c.name, func(t *testing.T) {
			asm := compileToArm64Asm(t, c.src)
			contig := buildArm64(t, asm, false)
			wx := buildArm64(t, asm, true)

			cOut, cCode := runWXBin(t, runner, contig)
			wOut, wCode := runWXBin(t, runner, wx)
			if wOut != cOut || wCode != cCode {
				t.Fatalf("W^X diverged: contiguous=(%q,%d) wx=(%q,%d)", cOut, cCode, wOut, wCode)
			}
			if wCode != c.exit || wOut != c.out {
				t.Fatalf("W^X result = (%q,%d), want (%q,%d)", wOut, wCode, c.out, c.exit)
			}
		})
	}
}

// buildX86 assembles asm and writes a runnable ELF, using the W^X
// two-segment layout when wx is set and the contiguous R+W+X layout
// otherwise. Returns the binary path.
func buildX86(t *testing.T, asm string, wx bool) string {
	t.Helper()
	var text, rodata []byte
	var err error
	var bin []byte
	if wx {
		text, rodata, err = nativex86.AssembleProgramWX(asm, nativeelf.TextVAddrWX)
		if err == nil {
			bin = nativeelf.StaticExecutableDataX86WX(text, rodata)
		}
	} else {
		text, rodata, err = nativex86.AssembleProgram(asm, nativeelf.TextVAddr)
		if err == nil {
			bin = nativeelf.StaticExecutableDataX86(text, rodata)
		}
	}
	if err != nil {
		t.Fatalf("assemble (wx=%v): %v\n--- asm ---\n%s", wx, err, asm)
	}
	path := filepath.Join(t.TempDir(), "prog")
	if err := os.WriteFile(path, bin, 0o755); err != nil {
		t.Fatalf("write bin: %v", err)
	}
	return path
}

// buildArm64 is the arm64 counterpart of buildX86.
func buildArm64(t *testing.T, asm string, wx bool) string {
	t.Helper()
	var text, rodata []byte
	var err error
	var bin []byte
	if wx {
		text, rodata, err = na.AssembleProgramWX(asm, nativeelf.TextVAddrWX)
		if err == nil {
			bin = nativeelf.StaticExecutableDataWX(text, rodata)
		}
	} else {
		text, rodata, err = na.AssembleProgram(asm, nativeelf.TextVAddr)
		if err == nil {
			bin = nativeelf.StaticExecutableData(text, rodata)
		}
	}
	if err != nil {
		t.Fatalf("assemble (wx=%v): %v\n--- asm ---\n%s", wx, err, asm)
	}
	path := filepath.Join(t.TempDir(), "prog")
	if err := os.WriteFile(path, bin, 0o755); err != nil {
		t.Fatalf("write bin: %v", err)
	}
	return path
}

// runWXBin runs binPath (directly, or under runner[0] when cross-emulated)
// and returns its combined output and exit code.
func runWXBin(t *testing.T, runner []string, binPath string) (string, int) {
	t.Helper()
	var cmd *exec.Cmd
	if len(runner) == 0 {
		cmd = exec.Command(binPath)
	} else {
		cmd = exec.Command(runner[0], binPath)
	}
	out, _ := cmd.CombinedOutput()
	return string(out), cmd.ProcessState.ExitCode()
}

// compileToX86Asm compiles src to x86-64 assembly with the real code
// generator (the x86 counterpart of compileToArm64Asm).
func compileToX86Asm(t *testing.T, src string) string {
	return compileToX86AsmExports(t, src, nil)
}

// compileToX86AsmExports is compileToX86Asm with extra tree-shake roots
// (Options.Exports) so functions the program never calls itself survive —
// e.g. a `-shared` .so export.
func compileToX86AsmExports(t *testing.T, src string, exports []string) string {
	t.Helper()
	dir := t.TempDir()
	srcPath := filepath.Join(dir, "main.fern")
	if err := os.WriteFile(srcPath, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	prog, _, err := modload.Load(srcPath)
	if err != nil {
		t.Fatalf("modload: %v", err)
	}
	if err := constfold.Fold(prog); err != nil {
		t.Fatalf("constfold: %v", err)
	}
	info, err := checker.Check(prog)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if err := monomorph.Run(prog, info); err != nil {
		t.Fatalf("monomorph: %v", err)
	}
	asm, err := x86codegen.EmitWithOptions(prog, info, x86codegen.Options{Exports: exports})
	if err != nil {
		t.Fatalf("x86_64 emit: %v", err)
	}
	return asm
}
