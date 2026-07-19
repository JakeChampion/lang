package x86_64

import "testing"

// asmFnName escapes function names that collide with an x86 register
// mnemonic so the emitted `.globl` / `call` / `.quad` tokens are not
// mis-parsed as registers. Register names (case-insensitively) get the
// collision-proof `$fn` suffix; everything else — the vast majority,
// including every `__fern_*` runtime helper — passes through unchanged.
func TestAsmFnNameEscapesRegisterNames(t *testing.T) {
	escaped := []string{"ch", "cl", "al", "ah", "si", "di", "sp", "bp", "ax", "cx", "dx", "bx",
		"rax", "rsp", "eax", "r8", "r15", "spl", "xmm0", "xmm15", "st", "CH", "AX"}
	for _, n := range escaped {
		if got := asmFnName(n); got != n+"$fn" {
			t.Errorf("asmFnName(%q) = %q, want %q", n, got, n+"$fn")
		}
	}
	unchanged := []string{"main", "chr", "eq", "len", "foo", "add", "sub", "mov",
		"__fern_alloc", "__method_MapIter_key", "si_", "channel", "axis", "r16", "xmm16"}
	for _, n := range unchanged {
		if got := asmFnName(n); got != n {
			t.Errorf("asmFnName(%q) = %q, want it unchanged", n, got)
		}
	}
}
