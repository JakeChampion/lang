package arm64

import (
	"strings"
	"testing"
)

// TestArm64FunctionSectionsELF pins the per-function `.section .text.<name>`
// emission on the Linux/ELF path.
//
// AArch64 `bl` / `R_AARCH64_CALL26` reaches only ±128 MiB. GNU `ld` auto-inserts
// long-branch veneers BETWEEN input sections but NOT within a single one, so a
// monolithic `.text` larger than 128 MiB fails to link with `relocation truncated
// to fit` — which the ~133 MB self-host compiler binary hit (the arm64 `.text`
// wall). Emitting each function into its own `.section .text.<name>` (the
// `-ffunction-sections` shape) lets `ld` veneer every cross-function call,
// lifting the effective limit to the ±4 GiB ADRP range. This test fails loudly if
// that emission regresses to a single `.text`.
func TestArm64FunctionSectionsELF(t *testing.T) {
	src := `function helper(n: i32): i32 { return n + 1; }
function main(): i32 { return helper(41); }`
	asm := compile(t, src, Options{})

	for _, fn := range []string{"main", "helper"} {
		want := ".section .text." + AsmFnName(fn) + ",\"ax\",@progbits"
		if !strings.Contains(asm, want) {
			t.Errorf("ELF arm64 output must emit %q so ld can veneer cross-function "+
				"calls past the ±128 MiB bl range (the self-host .text wall); not found", want)
		}
	}
}

// TestArm64FunctionSectionsDarwinUnaffected guards that the ELF-only
// per-function-section directive is NOT emitted on the arm64-darwin (Mach-O)
// path — its sections are `__TEXT,__text` and clang+lld already inserts
// range-extension thunks within a section, so the ELF `.section .text.<name>`
// flag/`@progbits` form would be invalid there.
func TestArm64FunctionSectionsDarwinUnaffected(t *testing.T) {
	src := `function main(): i32 { return 0; }`
	asm := compile(t, src, Options{Darwin: true})
	if strings.Contains(asm, ".section .text."+AsmFnName("main")) {
		t.Error("arm64-darwin (Mach-O) output must NOT emit the ELF .section .text.<name> directive")
	}
}

// strbufSrc is a minimal program that pulls in the global string-builder
// runtime (__fern_strbuf_reset / _append / _take) so the emitters that back
// them are exercised.
const strbufSrc = `function main(): i32 {
	strbuf_reset();
	strbuf_append("hello, ");
	strbuf_append("world");
	var s: string = strbuf_take();
	return s.len();
}`

// TestArm64StrBufRuntimeDarwinDialect guards the strbuf runtime's Mach-O
// dialect. The helper was added ELF-only: it hand-emitted `.section .bss` /
// `.section .text` and GNU `adrp`/`add ..., :lo12:sym` addressing, which
// clang's Mach-O assembler rejects (`unexpected token in .section` /
// `ADR/ADRP relocations must be GOT relative`) — link-failing the whole
// arm64-darwin self-host CLI build. On the Darwin path it must use
// `.section __DATA,__bss`, a plain `.text` switch-back, and `@PAGE`/
// `@PAGEOFF` addressing for its BSS symbols.
func TestArm64StrBufRuntimeDarwinDialect(t *testing.T) {
	asm := compile(t, strbufSrc, Options{Darwin: true})

	// Sanity: the strbuf runtime was actually emitted.
	if !strings.Contains(asm, "__fern_strbuf_reset") {
		t.Fatal("strbuf runtime not emitted; test can't guard its dialect")
	}
	for _, bad := range []string{":lo12:__fern_strbuf_len", ":lo12:__fern_strbuf_data", ".section .bss"} {
		if strings.Contains(asm, bad) {
			t.Errorf("arm64-darwin strbuf runtime must not emit ELF-only %q (clang's Mach-O assembler rejects it)", bad)
		}
	}
	for _, want := range []string{"__fern_strbuf_len@PAGE", "__fern_strbuf_data@PAGE", ".section __DATA,__bss"} {
		if !strings.Contains(asm, want) {
			t.Errorf("arm64-darwin strbuf runtime must emit Mach-O form %q", want)
		}
	}
}

// TestArm64StrBufRuntimeELFDialect pins that the ELF path keeps the GNU
// dialect (the darwin gating must not flip the default target).
func TestArm64StrBufRuntimeELFDialect(t *testing.T) {
	asm := compile(t, strbufSrc, Options{})
	if !strings.Contains(asm, ":lo12:__fern_strbuf_len") {
		t.Error("ELF arm64 strbuf runtime must use GNU :lo12: addressing")
	}
	if strings.Contains(asm, "__fern_strbuf_len@PAGE") {
		t.Error("ELF arm64 strbuf runtime must not emit Mach-O @PAGE addressing")
	}
}
