// Static position-independent (PIE / ET_DYN) native-backend tests — stage 1
// of the arm64-android target. A Fern PIE is a static, no-PLT/GOT image
// laid out from a load base of 0; the kernel maps it at an arbitrary base.
// Every code reference is PC-relative (adrp/:lo12:), so it runs unchanged at
// any base — the ONLY load-base-dependent values are the `.quad <symbol>`
// function-pointer slots (vtables, closures), which carry R_AARCH64_RELATIVE
// relocations.
//
// This stage validates: (a) the ET_DYN container loads and runs reloc-free
// programs at a shifted base under qemu, and (b) the assembler emits the
// right relocations for programs that DO use function-pointer tables.
// Applying those relocations at startup (the self-relocation prologue) is a
// later slice; until then, closure/vtable programs are checked for the
// presence of relocations but not executed.
package e2e

import (
	"bytes"
	"debug/elf"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	na "github.com/jakechampion/lang/internal/native/arm64"
	nativeelf "github.com/jakechampion/lang/internal/native/elf"
)

// TestArm64NativePIERelocFree builds reloc-free programs as static PIEs and
// runs them under qemu-aarch64. No `.quad <symbol>` tables means no
// relocations, so the binary is correct at any load base without a
// self-relocation prologue. The exit code proves PC-relative code (adrp +
// :lo12: rodata access, branches, calls) runs at the kernel-chosen base.
func TestArm64NativePIERelocFree(t *testing.T) {
	qemu := ""
	for _, c := range []string{"qemu-aarch64", "qemu-aarch64-static"} {
		if p, err := exec.LookPath(c); err == nil {
			qemu = p
			break
		}
	}
	if qemu == "" {
		t.Skip("qemu-aarch64 not on PATH")
	}
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
			cmd := exec.Command(qemu, path)
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
// via adrp/:lo12: and need no relocation.) Executing a program with relocs
// needs the self-relocation prologue (a later slice), so this only checks
// the relocations are present and well-formed.
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
	dataStart := pageUpTest(uint64(64+3*56+len(text)))
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

func toElfRelocs(rs []na.Reloc) []nativeelf.Reloc {
	out := make([]nativeelf.Reloc, len(rs))
	for i, r := range rs {
		out[i] = nativeelf.Reloc{Offset: r.Offset, Addend: r.Addend}
	}
	return out
}

func pageUpTest(v uint64) uint64 { return (v + 0xffff) &^ 0xffff }
