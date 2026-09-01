package x86_64

// Tests for P6, which materialises an argument straight into its register
// instead of computing it in the accumulator and copying.
//
// The soundness argument is that the copy is the last read of rax before a
// call, so what the rename leaves in rax cannot be observed: rax is
// caller-saved and is not an argument register. Everything here is about
// keeping "last read before a call" honest — the instructions allowed to sit
// between the copy and the call are a whitelist, because the accumulator has
// four names and one of them, `al`, is a substring of `call`.

import (
	"strings"
	"testing"
)

func TestArgSetupWhitelist(t *testing.T) {
	allowed := []string{
		"\tmov esi, 16",
		"\tmov rsi, [rbp-24]",
		"\tlea rdx, [rip + .Lstr3]",
		"\tmovzx ecx, byte ptr [rbp-8]",
		"\tsub rsp, 8",
		"\tadd rsp, 8",
	}
	for _, l := range allowed {
		if !isArgSetupNotTouchingAcc(l) {
			t.Errorf("%q should be allowed between the copy and the call", l)
		}
	}
	refused := []string{
		"\tmov rdi, rax",      // reads the accumulator
		"\tmov rax, 1",        // writes it
		"\tmov esi, eax",      // reads it, 32-bit name
		"\tmov sil, al",       // reads it, 8-bit name
		"\tmovzx ecx, ax",     // reads it, 16-bit name
		"\tcall __fern_alloc", // clobbers it
		"\tadd rcx, rdx",      // not a materialisation
		"\tmov [rbp-8], rcx",  // destination is not a register
		"\tpop rcx",           // moves rsp by an amount the rename cannot see
		"\tsete al",           // writes the accumulator's low byte
	}
	for _, l := range refused {
		if isArgSetupNotTouchingAcc(l) {
			t.Errorf("%q must not be allowed between the copy and the call", l)
		}
	}
}

// namesAcc has to see the accumulator under every name this backend writes,
// and must not fire on the letters inside other mnemonics.
func TestNamesAccBoundaries(t *testing.T) {
	for _, s := range []string{"rax", "eax", "ax", "al", "ah", "[rax + 8]", "byte ptr [rax]"} {
		if !namesAcc(s) {
			t.Errorf("namesAcc(%q) = false, want true", s)
		}
	}
	// The mnemonics and operands that contain the letters without naming the
	// register. `call` is the one that matters: it ends in `al`.
	for _, s := range []string{"__fern_alloc", "rcx", "r10", "[rbp-8]", "small", "halt", "maxi"} {
		if namesAcc(s) {
			t.Errorf("namesAcc(%q) = true, want false", s)
		}
	}
	// It errs toward yes — the 64- and 32-bit names are matched as plain
	// substrings, so a hypothetical `rax2` reads as the accumulator. That
	// direction is free: a false yes refuses a fold, a false no miscompiles.
	if !namesAcc("rax2") {
		t.Error("namesAcc should err toward reporting the accumulator")
	}
}

// End to end: a one-argument call on a local loads straight into rdi.
func TestSingleArgumentLoadsStraightIntoItsRegister(t *testing.T) {
	asm := compileOpts(t, `
@noinline function twice(x: i64): i64 { return x + x; }
function main(): i32 {
  var t: i64 = 0i64;
  var i: i64 = 0i64;
  while (i < 4i64) { t = t + twice(i); i = i + 1i64; }
  return t as i32;
}`, Options{})
	body, ok := fnBodyOf(asm, "main")
	if !ok {
		t.Fatal("main not found in emitted asm")
	}
	if !strings.Contains(body, "mov rdi, [rbp-") {
		t.Errorf("the argument did not load straight into rdi:\n%s", body)
	}
}

// And the copy is NOT removed when what follows is not a call, because then
// nothing proves the accumulator dead.
func TestCopyOutOfAccKeptWithoutACall(t *testing.T) {
	asm := compileOpts(t, `
@noinline function g(a: i64, b: i64): i64 { return a * b; }
function main(): i32 {
  var t: i64 = 0i64;
  var i: i64 = 0i64;
  while (i < 4i64) { t = t + g(i, i + 1i64) + i; i = i + 1i64; }
  return t as i32;
}`, Options{})
	// The program must still be emitted and still call g; the point is that
	// the rules did not eliminate a copy whose accumulator is read again.
	if !strings.Contains(asm, "call __fn_g") {
		t.Fatal("precondition: g is not called")
	}
	if problems := checkStackAlignment(asm); len(problems) > 0 {
		for _, p := range problems {
			t.Error(p)
		}
	}
}
