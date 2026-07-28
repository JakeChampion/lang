// Static position-independent (PIE / ET_DYN) native-backend tests for the
// arm64-android target. A Fern PIE is a static, no-PLT/GOT image laid out
// from a load base of 0; the kernel maps it at an arbitrary base. Every code
// reference is PC-relative (adrp/:lo12:), so it runs unchanged at any base —
// the ONLY load-base-dependent values are the `.quad <symbol>`
// function-pointer slots (function values, vtables), which carry
// R_AARCH64_RELATIVE relocations.
//
// Coverage: the ET_DYN container loads and runs reloc-free programs at a
// shifted base (TestArm64NativePIERelocFree); the assembler emits well-formed
// relocations for function-pointer slots (TestArm64NativePIEEmitsRelocs); and
// the self-relocation prologue (Options.PIE) applies those relocations at
// startup so programs that DO use function values / dyn-trait vtables run as
// PIEs (TestArm64NativePIESelfReloc).
package e2e

import (
	"bytes"
	"debug/elf"
	"os"
	"path/filepath"
	"testing"

	"github.com/jakechampion/lang/internal/checker"
	arm64codegen "github.com/jakechampion/lang/internal/codegen/arm64"
	x86codegen "github.com/jakechampion/lang/internal/codegen/x86_64"
	"github.com/jakechampion/lang/internal/constfold"
	"github.com/jakechampion/lang/internal/modload"
	"github.com/jakechampion/lang/internal/monomorph"
	na "github.com/jakechampion/lang/internal/native/arm64"
	nativeelf "github.com/jakechampion/lang/internal/native/elf"
	nativex86 "github.com/jakechampion/lang/internal/native/x86_64"
)

// TestArm64NativePIERelocFree builds reloc-free programs as static PIEs and
// runs them (natively on arm64, else under qemu-aarch64). No `.quad <symbol>`
// tables means no
// relocations, so the binary is correct at any load base without a
// self-relocation prologue. The exit code proves PC-relative code (adrp +
// :lo12: rodata access, branches, calls) runs at the kernel-chosen base.
func TestArm64NativePIERelocFree(t *testing.T) {
	qemu := arm64QemuOrEmpty(t)
	cases := []struct {
		name string
		src  string
		exit int
		out  string
	}{
		{"exit", `function main(): i32 { return 42; }`, 42, ""},
		{"arith", `function main(): i32 { var x = 6; var y = 7; return x * y; }`, 42, ""},
		{"fib", `function fib(n: i32): i32 { if (n < 2) { return n; } return fib(n-1)+fib(n-2); }
function main(): i32 { return fib(10); }`, 55, ""},
		{"loop", `function main(): i32 { var n = 0; var i = 0; while (i < 42) { n = n + 1; i = i + 1; } return n; }`, 42, ""},
		{"string", `function main(): i32 { print("hello PIE"); return 0; }`, 0, "hello PIE\n"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			asm := compileToArm64Asm(t, c.src)
			text, rodata, relocs, err := na.AssembleProgramPIE(asm, nativeelf.TextVAddrPIE)
			if err != nil {
				t.Fatalf("AssembleProgramPIE: %v", err)
			}
			if len(relocs) != 0 {
				t.Fatalf("expected reloc-free, got %d relocations", len(relocs))
			}
			bin := nativeelf.StaticPieExecutable(text, rodata, toElfRelocs(relocs))
			assertPIELayout(t, bin)

			path := filepath.Join(t.TempDir(), "prog")
			if err := os.WriteFile(path, bin, 0o755); err != nil {
				t.Fatal(err)
			}
			cmd := runArm64Bin(qemu, path)
			out, _ := cmd.CombinedOutput()
			code := cmd.ProcessState.ExitCode()
			if string(out) != c.out || code != c.exit {
				t.Fatalf("PIE run = (%q, %d), want (%q, %d)", out, code, c.out, c.exit)
			}
		})
	}
}

// TestArm64NativePIEEmitsRelocs proves the relocation machinery: a program
// that uses a named function as a value builds a static closure-pair cell
// (`__closure_cell_<fn>: .quad <fn>`) — an absolute function pointer that
// must yield an R_AARCH64_RELATIVE relocation (Addend = the fn's
// base-relative address, Offset = the cell slot in the data segment).
// (Nested closures, by contrast, materialise the fn address PC-relatively
// via adrp/:lo12: and need no relocation.) This checks the relocations are
// present and well-formed; TestArm64NativePIESelfReloc covers running them.
func TestArm64NativePIEEmitsRelocs(t *testing.T) {
	src := `function apply(f: (i32) => i32, x: i32): i32 { return f(x); }
function dbl(x: i32): i32 { return x * 2; }
function main(): i32 { return apply(dbl, 21); }`
	asm := compileToArm64Asm(t, src)
	text, rodata, relocs, err := na.AssembleProgramPIE(asm, nativeelf.TextVAddrPIE)
	if err != nil {
		t.Fatalf("AssembleProgramPIE: %v", err)
	}
	if len(relocs) == 0 {
		t.Fatalf("closure program produced no relocations; expected >= 1")
	}
	// Each reloc Offset must land inside the data segment (>= the page after
	// .text) and the binary must still be a well-formed ET_DYN.
	dataStart := pageUpTest(uint64(64 + 3*56 + len(text)))
	for i, r := range relocs {
		if r.Offset < dataStart {
			t.Errorf("reloc[%d].Offset %#x is before data segment start %#x", i, r.Offset, dataStart)
		}
	}
	bin := nativeelf.StaticPieExecutable(text, rodata, toElfRelocs(relocs))
	assertPIELayout(t, bin)
}

// assertPIELayout checks the container is a position-independent ELF: ET_DYN,
// a PT_DYNAMIC segment, and two PT_LOAD segments neither of which is W+X.
func assertPIELayout(t *testing.T, bin []byte) {
	t.Helper()
	f, err := elf.NewFile(bytes.NewReader(bin))
	if err != nil {
		t.Fatalf("not a parseable ELF: %v", err)
	}
	if f.Type != elf.ET_DYN {
		t.Errorf("e_type = %v, want ET_DYN", f.Type)
	}
	loads, dyn := 0, false
	for _, p := range f.Progs {
		switch p.Type {
		case elf.PT_LOAD:
			loads++
			if p.Flags&elf.PF_W != 0 && p.Flags&elf.PF_X != 0 {
				t.Errorf("PT_LOAD is W+X (%v) — not W^X", p.Flags)
			}
		case elf.PT_DYNAMIC:
			dyn = true
		}
	}
	if loads != 2 {
		t.Errorf("got %d PT_LOAD segments, want 2", loads)
	}
	if !dyn {
		t.Errorf("no PT_DYNAMIC segment")
	}
}

// TestArm64NativePIESelfReloc is the stage-3 gate: programs that DO carry
// relocations (function values → __closure_cell_<fn>: .quad <fn>) are
// compiled with the PIE self-relocation prologue (Options.PIE) and run as
// static PIEs (natively on arm64, else under qemu-aarch64). The prologue
// applies .rela.dyn at startup,
// so the function-pointer slots hold the correct runtime address despite the
// kernel-chosen load base. Reloc-free programs are included to prove the
// prologue's empty-loop path is harmless.
func TestArm64NativePIESelfReloc(t *testing.T) {
	qemu := arm64QemuOrEmpty(t)
	cases := []struct {
		name      string
		src       string
		exit      int
		wantReloc bool // expect >= 1 relocation (function-pointer slot)
	}{
		{"exit_relocfree", `function main(): i32 { return 42; }`, 42, false},
		{"funcvalue", `function apply(f: (i32) => i32, x: i32): i32 { return f(x); }
function dbl(x: i32): i32 { return x * 2; }
function main(): i32 { return apply(dbl, 21); }`, 42, true},
		{"funcvalue_multi", `function apply(f: (i32) => i32, x: i32): i32 { return f(x); }
function dbl(x: i32): i32 { return x * 2; }
function inc(x: i32): i32 { return x + 1; }
function main(): i32 { return apply(dbl, 20) + apply(inc, 1); }`, 42, true},
		{"dyntrait", `trait Shape { function area(self: Self): i32; }
struct Sq { s: i32 }
impl Shape for Sq { function area(self: Self): i32 { return self.s * self.s; } }
function main(): i32 { var d: dyn Shape = Sq { s: 7 }; return d.area(); }`, 49, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			asm := compileToArm64AsmPIE(t, c.src)
			text, rodata, relocs, err := na.AssembleProgramPIE(asm, nativeelf.TextVAddrPIE)
			if err != nil {
				t.Fatalf("AssembleProgramPIE: %v", err)
			}
			if c.wantReloc && len(relocs) == 0 {
				t.Fatalf("expected relocations, got none")
			}
			if !c.wantReloc && len(relocs) != 0 {
				t.Fatalf("expected reloc-free, got %d", len(relocs))
			}
			bin := nativeelf.StaticPieExecutable(text, rodata, toElfRelocs(relocs))
			assertPIELayout(t, bin)
			path := filepath.Join(t.TempDir(), "prog")
			if err := os.WriteFile(path, bin, 0o755); err != nil {
				t.Fatal(err)
			}
			cmd := runArm64Bin(qemu, path)
			out, _ := cmd.CombinedOutput()
			if code := cmd.ProcessState.ExitCode(); code != c.exit {
				t.Fatalf("PIE self-reloc run exit = %d, want %d (out=%q)", code, c.exit, out)
			}
		})
	}
}

// TestX86_64NativePIESelfReloc is the x86-64 counterpart of
// TestArm64NativePIESelfReloc: programs are compiled as static PIEs with the
// self-relocation prologue (Options.PIE) and run (natively on amd64, else
// under qemu-x86_64). rip-relative code is already position-independent; the
// prologue applies the .rela.dyn entries for the `.quad <symbol>` slots.
func TestX86_64NativePIESelfReloc(t *testing.T) {
	runner := x86NativeRunner(t) // SKIPs if neither native amd64 nor qemu-x86_64
	cases := []struct {
		name      string
		src       string
		exit      int
		wantReloc bool
	}{
		{"exit_relocfree", `function main(): i32 { return 42; }`, 42, false},
		{"funcvalue", `function apply(f: (i32) => i32, x: i32): i32 { return f(x); }
function dbl(x: i32): i32 { return x * 2; }
function main(): i32 { return apply(dbl, 21); }`, 42, true},
		{"dyntrait", `trait Shape { function area(self: Self): i32; }
struct Sq { s: i32 }
impl Shape for Sq { function area(self: Self): i32 { return self.s * self.s; } }
function main(): i32 { var d: dyn Shape = Sq { s: 7 }; return d.area(); }`, 49, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			asm := compileToX86AsmPIE(t, c.src)
			text, rodata, relocs, err := nativex86.AssembleProgramPIE(asm, nativeelf.TextVAddrPIE)
			if err != nil {
				t.Fatalf("AssembleProgramPIE: %v", err)
			}
			if c.wantReloc && len(relocs) == 0 {
				t.Fatalf("expected relocations, got none")
			}
			if !c.wantReloc && len(relocs) != 0 {
				t.Fatalf("expected reloc-free, got %d", len(relocs))
			}
			bin := nativeelf.StaticPieExecutableX86(text, rodata, toElfRelocsX86(relocs))
			assertPIELayout(t, bin)
			path := filepath.Join(t.TempDir(), "prog")
			if err := os.WriteFile(path, bin, 0o755); err != nil {
				t.Fatal(err)
			}
			out, code := runWXBin(t, runner, path)
			if code != c.exit {
				t.Fatalf("PIE self-reloc run exit = %d, want %d (out=%q)", code, c.exit, out)
			}
		})
	}
}

// compileToX86AsmPIE compiles src to x86-64 assembly with the PIE
// self-relocation prologue enabled (Options.PIE).
func compileToX86AsmPIE(t *testing.T, src string) string {
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
	asm, err := x86codegen.EmitWithOptions(prog, info, x86codegen.Options{PIE: true})
	if err != nil {
		t.Fatalf("emit: %v", err)
	}
	return asm
}

func toElfRelocsX86(rs []nativex86.Reloc) []nativeelf.Reloc {
	out := make([]nativeelf.Reloc, len(rs))
	for i, r := range rs {
		out[i] = nativeelf.Reloc{Offset: r.Offset, Addend: r.Addend}
	}
	return out
}

// compileToArm64AsmPIE compiles src to arm64 assembly with the PIE
// self-relocation prologue enabled (Options.PIE).
func compileToArm64AsmPIE(t *testing.T, src string) string {
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
	asm, err := arm64codegen.EmitWithOptions(prog, info, arm64codegen.Options{PIE: true})
	if err != nil {
		t.Fatalf("emit: %v", err)
	}
	return asm
}

func toElfRelocs(rs []na.Reloc) []nativeelf.Reloc {
	out := make([]nativeelf.Reloc, len(rs))
	for i, r := range rs {
		out[i] = nativeelf.Reloc{Offset: r.Offset, Addend: r.Addend}
	}
	return out
}

func pageUpTest(v uint64) uint64 { return (v + 0xffff) &^ 0xffff }
