package arm64

import (
	"strings"
	"testing"
)

// These tests pin WHICH Mach-O section the arm64-darwin dialect places
// pointer-bearing static data in. Closure-pair cells (`__closure_cell_*`,
// OpConstFunc) and `dyn` trait vtables (`__vtable_*`) both hold absolute
// `.quad <function>` pointers; Apple's ld64 refuses absolute relocations
// anywhere in the __TEXT segment ("ld: Found illegal text-relocations"),
// so on darwin they must live in `__DATA,__const` (read-only after dyld
// relocation — where clang puts C++ vtables too). ELF `.rodata` has no
// such restriction, so the Linux dialect keeps them there.
//
// A link test can't guard this portably: lld's Mach-O port ACCEPTS the
// illegal placement that real ld64 rejects, so the Linux cross-link in
// TestArm64DarwinBuilds passed while the macOS runner failed (the #5052
// map regression — core/map's keyed-family adapter fns materialised
// `__closure_cell___map_*` cells in every map-using program). Hence this
// textual assertion on the emitted section directives.

// sectionOfLabel walks asm tracking the active `.section` directive and
// returns the section in force where `label:` is defined ("" if the label
// never appears). A bare `.text` line also switches sections.
func sectionOfLabel(asm, label string) string {
	section := ""
	for _, raw := range strings.Split(asm, "\n") {
		line := strings.TrimSpace(raw)
		if strings.HasPrefix(line, ".section ") {
			section = strings.TrimSpace(strings.TrimPrefix(line, ".section "))
		} else if line == ".text" {
			section = ".text"
		} else if line == label+":" {
			return section
		}
	}
	return ""
}

// constFuncSrc materialises a named function as a VALUE, forcing an
// OpConstFunc static closure-pair cell (`__closure_cell_dbl`).
const constFuncSrc = `function dbl(x: i32): i32 { return x * 2; }
function main(): i32 { var f: (i32) => i32 = dbl; return f(21); }`

// dynTraitSrc dispatches through a `dyn` trait, forcing a static vtable
// cell (`__vtable_Error_NotFound`).
const dynTraitSrc = `trait Error { function message(self: Self): string; }
struct NotFound { what: string }
impl Error for NotFound { function message(self: Self): string { return self.what; } }
function main(): i32 {
    var e: dyn Error = NotFound { what: "ab" } as dyn Error;
    return e.message().len();
}`

func TestArm64DarwinClosureCellSection(t *testing.T) {
	asm := compile(t, constFuncSrc, Options{Darwin: true})
	// The darwin dialect L-prefixes closure-cell labels (closureCellSym):
	// an assembler-local label doesn't start a new ld64/ld-prime ATOM, so
	// the cell stays glued to the anonymous rc header emitted just before
	// it (the #5056 atom-split fix). The section guard below is unchanged.
	got := sectionOfLabel(asm, "L__closure_cell_dbl")
	if got == "" {
		t.Fatal("__closure_cell_dbl not emitted; test can't guard its section")
	}
	if got != "__DATA,__const" {
		t.Errorf("arm64-darwin closure cell sits in %q; its absolute .quad fn pointer is an illegal text-relocation outside __DATA,__const (ld64 rejects the link)", got)
	}
}

func TestArm64DarwinDynVtableSection(t *testing.T) {
	asm := compile(t, dynTraitSrc, Options{Darwin: true})
	got := sectionOfLabel(asm, "__vtable_Error_NotFound")
	if got == "" {
		t.Fatal("__vtable_Error_NotFound not emitted; test can't guard its section")
	}
	if got != "__DATA,__const" {
		t.Errorf("arm64-darwin dyn vtable sits in %q; its absolute .quad method pointers are illegal text-relocations outside __DATA,__const (ld64 rejects the link)", got)
	}
}

// The ELF dialect keeps both cell kinds in .rodata — the darwin gating
// must not leak Mach-O section names into the Linux target.
func TestArm64ELFPointerCellSections(t *testing.T) {
	for _, tc := range []struct{ name, src, label string }{
		{"closure-cell", constFuncSrc, "__closure_cell_dbl"},
		{"dyn-vtable", dynTraitSrc, "__vtable_Error_NotFound"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			asm := compile(t, tc.src, Options{})
			got := sectionOfLabel(asm, tc.label)
			if got != ".rodata" {
				t.Errorf("ELF arm64 %s sits in %q, want .rodata", tc.label, got)
			}
		})
	}
}
