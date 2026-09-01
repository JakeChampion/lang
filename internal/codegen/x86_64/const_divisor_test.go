package x86_64

// Shape tests for division by a literal divisor.
//
// The semantics are pinned by conformance/cases/const_divisor_lowering, which
// runs the whole family on every backend. What is checked here is that the
// x86-64 emitter actually takes the specialised path — a lowering that quietly
// stopped firing would still compute the right answers, so a correctness
// corpus cannot notice it.

import (
	"strings"
	"testing"
)

func divBody(t *testing.T, expr string) string {
	t.Helper()
	asm := compileOpts(t, `
@noinline function d(x: i64): i64 { return `+expr+`; }
function main(): i32 { return d(7i64) as i32; }`, Options{})
	body, ok := fnBodyOf(asm, "d")
	if !ok {
		t.Fatal("d not found in emitted asm")
	}
	return body
}

// Both of the generic sequence's guards branch on the divisor, so a literal
// makes them dead. They are the bulk of what the sequence costs.
func TestLiteralDivisorDropsTheGuards(t *testing.T) {
	body := divBody(t, "x % 97i64")
	for _, gone := range []string{"test rcx, rcx", "cmp rcx, -1", ".Ldiv_zero", ".Ldiv_ovf", ".Ldiv_norm"} {
		if strings.Contains(body, gone) {
			t.Errorf("a guard on a compile-time-known divisor survived (%q):\n%s", gone, body)
		}
	}
	if !strings.Contains(body, "idiv") {
		t.Errorf("expected the divide itself to remain:\n%s", body)
	}
}

// A power of two needs no divide at all.
func TestPowerOfTwoDivisorBecomesAShift(t *testing.T) {
	body := divBody(t, "x / 8i64")
	if strings.Contains(body, "idiv") || strings.Contains(body, "div ") {
		t.Errorf("division by 8 still uses a divide instruction:\n%s", body)
	}
	if !strings.Contains(body, "sar rax, 3") {
		t.Errorf("expected a shift by 3:\n%s", body)
	}
	// Signed division rounds toward zero, so the shift has to be biased.
	// Without this pair -1/8 is -1 instead of 0, which is the whole reason
	// the case is not just `sar`.
	if !strings.Contains(body, "sar rcx, 63") || !strings.Contains(body, "shr rcx, 61") {
		t.Errorf("the round-toward-zero bias is missing:\n%s", body)
	}
}

func TestPowerOfTwoRemainderBecomesAMask(t *testing.T) {
	body := divBody(t, "x % 8i64")
	if strings.Contains(body, "idiv") {
		t.Errorf("remainder by 8 still uses a divide instruction:\n%s", body)
	}
	if !strings.Contains(body, "and rcx, -8") {
		t.Errorf("expected the mask that clears the low three bits:\n%s", body)
	}
}

// The degenerate divisors need no arithmetic at all.
func TestDegenerateDivisorsEmitNoDivide(t *testing.T) {
	cases := []struct{ expr, want, avoid string }{
		{"x / 1i64", "", "idiv"},
		{"x % 1i64", "xor eax, eax", "idiv"},
		{"x / 0i64", "xor eax, eax", "idiv"},
		{"x % 0i64", "", "idiv"},
		{"x / -1i64", "neg rax", "idiv"},
		{"x % -1i64", "xor eax, eax", "idiv"},
	}
	for _, c := range cases {
		t.Run(c.expr, func(t *testing.T) {
			body := divBody(t, c.expr)
			if strings.Contains(body, c.avoid) {
				t.Errorf("%s emitted a divide:\n%s", c.expr, body)
			}
			if c.want != "" && !strings.Contains(body, c.want) {
				t.Errorf("%s should emit %q:\n%s", c.expr, c.want, body)
			}
		})
	}
}

// The most negative divisor has no positive magnitude, so the power-of-two
// path must not try to negate it — it belongs on the generic path.
func TestMostNegativeDivisorTakesTheGenericPath(t *testing.T) {
	body := divBody(t, "x / -9223372036854775808i64")
	if !strings.Contains(body, "idiv") {
		t.Errorf("INT64_MIN divisor should use the divide instruction:\n%s", body)
	}
	// Still no guards: it is neither zero nor -1.
	if strings.Contains(body, "test rcx, rcx") {
		t.Errorf("a guard survived on a literal divisor:\n%s", body)
	}
}

func TestConstDivisorHelpers(t *testing.T) {
	for _, v := range []uint64{1, 2, 4, 8, 1024, 1 << 31, 1 << 63} {
		if !isPow2(v) {
			t.Errorf("isPow2(%d) = false", v)
		}
	}
	for _, v := range []uint64{0, 3, 5, 6, 7, 97, (1 << 31) + 1} {
		if isPow2(v) {
			t.Errorf("isPow2(%d) = true", v)
		}
	}
	for v, want := range map[uint64]int{1: 0, 2: 1, 8: 3, 1024: 10, 1 << 31: 31} {
		if got := pow2Shift(v); got != want {
			t.Errorf("pow2Shift(%d) = %d, want %d", v, got, want)
		}
	}
	// Signed stops one short of the sign bit; unsigned may use it.
	if maxDivShift(32, false) != 30 || maxDivShift(32, true) != 31 {
		t.Error("maxDivShift is wrong for 32-bit")
	}
	if maxDivShift(64, false) != 62 || maxDivShift(64, true) != 63 {
		t.Error("maxDivShift is wrong for 64-bit")
	}
	// A 32-bit mask with the top bit set has to be written as imm32.
	if got := immAtWidth(0xFFFFFFFE, 32); got != -2 {
		t.Errorf("immAtWidth(0xFFFFFFFE, 32) = %d, want -2", got)
	}
	if got := immAtWidth(0xFFFFFFFE, 64); got != 0xFFFFFFFE {
		t.Errorf("immAtWidth at 64-bit should pass through, got %d", got)
	}
}

// An i64 literal only has to pay `movabs`'s ten bytes when it actually needs
// all 64 bits. A write to eax zero-extends into rax, so anything that fits in
// uint32 is five bytes and zero is two — and the literal has to be written as
// the signed imm32 with the same bit pattern, because imm32 is a signed field
// and the peephole's constant folds read it back as one.
func TestI64ConstantPicksTheNarrowestForm(t *testing.T) {
	cases := []struct{ lit, want, avoid string }{
		{"0i64", "xor eax, eax", "movabs"},
		{"7i64", "mov eax, 7", "movabs"},
		{"4294967295i64", "mov eax, -1", "movabs"},
		{"4294967296i64", "movabs rax, 4294967296", ""},
		{"-1i64", "movabs rax, -1", ""},
	}
	for _, c := range cases {
		t.Run(c.lit, func(t *testing.T) {
			asm := compileOpts(t, `
@noinline function k(): i64 { return `+c.lit+`; }
function main(): i32 { return k() as i32; }`, Options{})
			body, ok := fnBodyOf(asm, "k")
			if !ok {
				t.Fatal("k not found in emitted asm")
			}
			if !strings.Contains(body, c.want) {
				t.Errorf("expected %q for %s:\n%s", c.want, c.lit, body)
			}
			if c.avoid != "" && strings.Contains(body, c.avoid) {
				t.Errorf("%s should not need %q:\n%s", c.lit, c.avoid, body)
			}
		})
	}
}
