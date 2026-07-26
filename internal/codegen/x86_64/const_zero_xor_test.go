package x86_64

import (
	"strings"
	"testing"
)

// Literal zero materialises as `xor eax, eax` (2 bytes) rather than
// `mov eax, 0` (5 bytes) — #4380 lever 2. Zero is pervasive (every `0` in
// source, every zero-init, every implicit `return 0`), so the 3 bytes
// compound: measured ~1% of emitted code across the examples (regex_captures
// 139,296 -> 137,772 text bytes, i.e. ~508 zero sites).
//
// `xor` clobbers FLAGS where `mov` does not. That is safe because FLAGS are
// never live across an IR-op boundary here: each flag producer is emitted with
// its consumer inside one op's expansion (`cmp` + `setcc` back to back), and
// the cmp/branch fusion peephole only rewrites an already-adjacent pair — so a
// const materialisation, being its own IR op, can never sit between a
// flag-setter and its reader. The e2e + differential suites are the empirical
// backstop for that invariant.
func TestConstZeroUsesXor(t *testing.T) {
	asm := compile(t, `function f(): i32 { return 0; }
function main(): i32 { return f(); }`)
	body := fnBody(t, asm, "f")
	if !strings.Contains(body, "xor eax, eax") {
		t.Errorf("literal 0 should materialise as `xor eax, eax`, got:\n%s", body)
	}
	if strings.Contains(body, "mov eax, 0\n") {
		t.Errorf("literal 0 still emits the 5-byte `mov eax, 0`:\n%s", body)
	}
}

// Non-zero immediates keep `mov` — `xor` only helps for zero, and rewriting
// a non-zero constant would be wrong.
func TestConstNonZeroKeepsMov(t *testing.T) {
	asm := compile(t, `function f(): i32 { return 7; }
function main(): i32 { return f(); }`)
	body := fnBody(t, asm, "f")
	if !strings.Contains(body, "mov eax, 7") {
		t.Errorf("literal 7 should still use `mov eax, 7`, got:\n%s", body)
	}
}

// Negative immediates keep `mov` too (the assembler takes a negative imm32
// directly, and the value is not zero).
func TestConstNegativeKeepsMov(t *testing.T) {
	asm := compile(t, `function f(): i32 { return 0 - 3; }
function main(): i32 { return f(); }`)
	body := fnBody(t, asm, "f")
	if !strings.Contains(body, "mov eax, 3") && !strings.Contains(body, "mov eax, -3") {
		t.Errorf("negative literal should still use `mov`, got:\n%s", body)
	}
}
