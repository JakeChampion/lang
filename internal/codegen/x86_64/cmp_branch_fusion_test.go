package x86_64

import (
	"regexp"
	"strings"
	"testing"
)

// Compare-and-branch fusion (#4378): an integer comparison whose 0/1
// result flows straight into the OpIf / OpBrIf that follows it (through
// zero or more OpNots) must emit `cmp; jcc` rather than materialising
// the boolean with setcc/movzx and re-testing it. These pin both the
// SHAPE (the fused sequence appears; the setcc/movzx/test chain does
// not) and, via the correctness matrix in the e2e suite, that the
// chosen jcc mnemonic is right for every comparison / signedness /
// negation combination.

// fnBody returns the emitted lines of the Fern function `name` (between its
// label and its `.size` directive), for shape assertions. `name` is the
// source name; the emitted symbol is mangled.
func fnBody(t *testing.T, asm, name string) string {
	t.Helper()
	sym := AsmFnName(name)
	start := strings.Index(asm, "\n"+sym+":\n")
	if start < 0 {
		t.Fatalf("function %q not found in asm", name)
	}
	rest := asm[start+1:]
	end := strings.Index(rest, ".size "+sym)
	if end < 0 {
		end = len(rest)
	}
	return rest[:end]
}

func TestCmpBranchFusionShape(t *testing.T) {
	// if (a < b) — signed: jump to the else-label when a >= b, i.e. jge.
	asm := compile(t, `@noinline function f(a: i32, b: i32): i32 { if (a < b) { return 1; } return 2; }
function main(): i32 { return f(1, 2); }`)
	body := fnBody(t, asm, "f")

	// The fused form: a `cmp` immediately followed by a conditional jump.
	if !regexp.MustCompile(`(?m)^\s*cmp e?ax, e?cx\n\s*jge \.LifElse`).MatchString(body) {
		t.Errorf("expected fused `cmp; jge` for `if (a < b)`, got:\n%s", body)
	}
	// The un-fused materialisation must be gone from this function.
	for _, dead := range []string{"setl", "setz", "movzx eax, al"} {
		if strings.Contains(body, dead) {
			t.Errorf("un-fused instruction %q still present in fused `if (a < b)`:\n%s", dead, body)
		}
	}
}

func TestCmpBranchFusionUnsignedMnemonic(t *testing.T) {
	// Unsigned `<` must fuse to the below/above family (jae as the
	// jump-to-else), not the signed jge — the two disagree across the
	// sign boundary and a wrong pick silently miscompiles u32/u64.
	asm := compile(t, `@noinline function f(a: u32, b: u32): i32 { if (a < b) { return 1; } return 2; }
function main(): i32 { return f(1, 2); }`)
	body := fnBody(t, asm, "f")
	if !strings.Contains(body, "jae") {
		t.Errorf("unsigned `if (a < b)` should fuse to `jae`, got:\n%s", body)
	}
	if strings.Contains(body, "jge") {
		t.Errorf("unsigned `if (a < b)` wrongly used the signed `jge`:\n%s", body)
	}
}

func TestCmpBranchFusionNegation(t *testing.T) {
	// `if (!(a < b))` folds the OpNot into the jump: jump to else when
	// the *un-negated* condition holds — i.e. when a < b (jl), so the
	// body runs on a >= b.
	asm := compile(t, `@noinline function f(a: i32, b: i32): i32 { if (!(a < b)) { return 1; } return 2; }
function main(): i32 { return f(1, 2); }`)
	body := fnBody(t, asm, "f")
	if !regexp.MustCompile(`(?m)^\s*cmp e?ax, e?cx\n\s*jl \.LifElse`).MatchString(body) {
		t.Errorf("expected fused `cmp; jl` for `if (!(a < b))`, got:\n%s", body)
	}
}

func TestCmpBranchFusionWhile(t *testing.T) {
	// A `while (i < n)` loop guard is an OpBrIf-shaped consumer; it must
	// fuse too (the hot path the #4378 benchmark measured).
	asm := compile(t, `@noinline function f(n: i32): i32 { var i: i32 = 0; while (i < n) { i = i + 1; } return i; }
function main(): i32 { return f(3); }`)
	body := fnBody(t, asm, "f")
	if strings.Count(body, "setl")+strings.Count(body, "movzx eax, al") > 0 {
		t.Errorf("while-loop guard did not fuse (setcc/movzx present):\n%s", body)
	}
}
