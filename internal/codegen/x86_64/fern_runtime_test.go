package x86_64

import (
	"strings"
	"testing"
)

const readFileProgram = `function main(): i32 {
    return match (read_file("x")) { Ok(s) => s.len(), Err(_) => 1 };
}`

// A runtime helper written in Fern (internal/fernrt) is emitted through
// emitFunc under its mangled name — once, with no hand-written twin beside
// it — and the hand-written body that needs it calls that name. The raw
// pokes the helper is written on are instructions at their call sites,
// not calls.
func TestFernRuntimeHelperEmittedOnceUnderFnName(t *testing.T) {
	asm := compile(t, readFileProgram)
	sym := AsmFnName("__fern_utf8_valid")
	if n := strings.Count(asm, "\n"+sym+":"); n != 1 {
		t.Errorf("%s defined %d times, want 1", sym, n)
	}
	if !strings.Contains(asm, "call "+sym) {
		t.Errorf("__fern_read_file does not call %s", sym)
	}
	if strings.Contains(asm, "\n__fern_utf8_valid:") {
		t.Error("a hand-written __fern_utf8_valid is emitted beside the Fern one")
	}
	if !strings.Contains(asm, "movzx eax, byte ptr [rax]") {
		t.Error("the helper's byte loads are not lowered inline")
	}
	if strings.Contains(asm, "__load_u8") {
		t.Error("__load_u8 is still a symbol")
	}
}

// A Fern helper nothing needs costs nothing.
func TestFernRuntimeHelperGatedOnUse(t *testing.T) {
	asm := compile(t, `function main(): i32 { return 0; }`)
	if strings.Contains(asm, "fern_utf8_valid") {
		t.Error("the helper is emitted for a program that never reads a file")
	}
}
