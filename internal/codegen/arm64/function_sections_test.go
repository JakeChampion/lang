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
		want := ".section .text." + fn + ",\"ax\",@progbits"
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
	if strings.Contains(asm, ".section .text.main") {
		t.Error("arm64-darwin (Mach-O) output must NOT emit the ELF .section .text.<name> directive")
	}
}
