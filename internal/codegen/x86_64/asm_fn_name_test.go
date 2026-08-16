package x86_64

import "testing"

// AsmFnName escapes function names GNU `as` reserves in Intel syntax so the
// emitted `.globl` / `call` / `.quad` tokens are not mis-parsed. Reserved
// names (case-insensitively) get the collision-proof `$fn` suffix; everything
// else — the vast majority, including every `__fern_*` runtime helper — passes
// through unchanged.
func TestAsmFnNameEscapesReservedNames(t *testing.T) {
	escaped := []string{
		// General-purpose registers, all four widths, plus the high bytes.
		"ch", "cl", "al", "ah", "si", "di", "sp", "bp", "ax", "cx", "dx", "bx",
		"rax", "rsp", "eax", "r8", "r15", "r15d", "r15w", "r15b", "spl",
		// APX extended GPRs (binutils >= 2.42): `call r16` assembles clean as
		// a REX2 indirect call, so a miss here is a silent SIGSEGV.
		"r16", "r31", "r16d", "r31d", "r16w", "r31w", "r16b", "r31b",
		// Vector, mask, MMX, bounds and tile registers.
		"xmm0", "xmm15", "xmm16", "xmm31", "ymm0", "ymm31", "zmm0", "zmm31",
		"mm0", "mm7", "k0", "k7", "bnd0", "bnd3", "tmm0", "tmm7",
		// Control, debug, segment; x87 stack top; instruction pointer.
		"cr0", "cr8", "cr15", "dr0", "dr7", "dr15",
		"cs", "ds", "es", "ss", "fs", "gs", "st", "rip", "eip",
		// Intel-syntax operand keywords.
		"byte", "word", "dword", "qword", "tbyte", "fword", "oword",
		"xmmword", "ymmword", "zmmword", "offset", "short", "near", "far", "flat",
		// Intel-syntax expression operators.
		"and", "or", "xor", "not", "shl", "shr", "mod", "eq", "ne", "lt", "le", "gt", "ge",
		// The assembler folds case, so the escape must too.
		"CH", "AX", "CS", "Qword", "MOD",
	}
	for _, n := range escaped {
		if got := AsmFnName(n); got != n+"$fn" {
			t.Errorf("AsmFnName(%q) = %q, want %q", n, got, n+"$fn")
		}
	}
	unchanged := []string{"main", "chr", "len", "foo", "add", "sub", "mov",
		"__fern_alloc", "__method_MapIter_key", "si_", "channel", "axis",
		// Verified NOT to collide on binutils 2.42 — escaping these would be
		// churn, and the comment on x86AsmReserved says why each is exempt.
		"st0", "st7", "ip", "tr0", "tr7", "riz", "eiz", "ptr",
		"r0", "r7", "mm8", "k8", "bnd4", "tmm8", "xmm32", "ymm32", "zmm32",
		"cr16", "dr16", "rflags", "eflags",
	}
	for _, n := range unchanged {
		if got := AsmFnName(n); got != n {
			t.Errorf("AsmFnName(%q) = %q, want it unchanged", n, got)
		}
	}
}
