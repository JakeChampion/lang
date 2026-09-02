package arm64

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
// not calls. Both object formats.
func TestFernRuntimeHelperEmittedOnceUnderFnName(t *testing.T) {
	for _, darwin := range []bool{false, true} {
		asm := compile(t, readFileProgram, Options{Darwin: darwin})
		sym := AsmFnName("__fern_utf8_valid")
		if n := strings.Count(asm, "\n"+sym+":"); n != 1 {
			t.Errorf("darwin=%v: %s defined %d times, want 1", darwin, sym, n)
		}
		if !strings.Contains(asm, "bl "+sym) {
			t.Errorf("darwin=%v: __fern_read_file does not call %s", darwin, sym)
		}
		if strings.Contains(asm, "\n__fern_utf8_valid:") {
			t.Errorf("darwin=%v: a hand-written __fern_utf8_valid is emitted beside the Fern one", darwin)
		}
		if !strings.Contains(asm, "ldrb w0, [x0]") {
			t.Errorf("darwin=%v: the helper's byte loads are not lowered inline", darwin)
		}
		if strings.Contains(asm, "__load_u8") {
			t.Errorf("darwin=%v: __load_u8 is still a symbol", darwin)
		}
	}
}

// A Fern helper nothing needs costs nothing.
func TestFernRuntimeHelperGatedOnUse(t *testing.T) {
	asm := compile(t, `function main(): i32 { return 0; }`, Options{})
	if strings.Contains(asm, "fern_utf8_valid") {
		t.Error("the helper is emitted for a program that never reads a file")
	}
}
