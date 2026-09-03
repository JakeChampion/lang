package arm64

import (
	"regexp"
	"strings"
	"testing"
)

// Compare-and-branch fusion (#4378, arm64 mirror of the x86-64 slice): an
// integer comparison whose 0/1 result flows straight into the OpIf / OpBrIf
// that follows it (through zero or more OpNots) must emit `cmp; b.cond` rather
// than materialising the boolean with `cset` and re-testing it with cbz/cbnz.
// These pin both the SHAPE (the fused `cmp; b.cond` appears; the `cset` is
// gone) and, via the correctness matrix in the e2e suite, that the chosen
// condition code is right for every comparison / signedness / negation combo.
//
// The fused branch is a single `b.cond` that reaches its target directly (see
// condBranchFarCC), so `if (a < b)` shows `b.ge .LifElse` — the branch fires
// when the condition is FALSE and control leaves for the else arm. A function
// too large for b.cond's ±1MB reach has its branches expanded back into the
// inverted-test-over-`b` trampoline afterwards (reachCheckCondBranches).

// fnBody returns the emitted lines of the Fern function `name` (between its
// label and its `.size` directive), for shape assertions. `name` is the source
// name; the emitted symbol is mangled. On Darwin there is no `.size`
// directive; the Linux emit path (the default here) always has one.
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
	// if (a < b) — signed. The branch reaches the else-label when a >= b.
	asm := compile(t, `@noinline function f(a: i32, b: i32): i32 { if (a < b) { return 1; } return 2; }
function main(): i32 { return f(1, 2); }`, Options{})
	body := fnBody(t, asm, "f")

	// The fused form: a `cmp` immediately followed by a conditional branch.
	if !regexp.MustCompile(`(?m)^\s*cmp w1, w0\n\s*b\.ge `).MatchString(body) {
		t.Errorf("expected fused `cmp w1, w0; b.ge` for `if (a < b)`, got:\n%s", body)
	}
	// One branch instruction, not an inverted test over an unconditional `b`.
	if regexp.MustCompile(`(?m)^\s*b\.\w+ \.LbrFar`).MatchString(body) {
		t.Errorf("a function this small must branch directly, not via a trampoline:\n%s", body)
	}
	// The un-fused materialisation must be gone from this function.
	if strings.Contains(body, "cset") {
		t.Errorf("un-fused `cset` still present in fused `if (a < b)`:\n%s", body)
	}
}

func TestCmpBranchFusionUnsignedMnemonic(t *testing.T) {
	// Unsigned `<` must fuse to the unsigned condition family (lo/hs), not
	// the signed lt/ge — the two disagree across the sign boundary and a
	// wrong pick silently miscompiles u32/u64.
	asm := compile(t, `@noinline function f(a: u32, b: u32): i32 { if (a < b) { return 1; } return 2; }
function main(): i32 { return f(1, 2); }`, Options{})
	body := fnBody(t, asm, "f")
	if !strings.Contains(body, "b.hs") {
		t.Errorf("unsigned `if (a < b)` should fuse to the unsigned `b.hs`, got:\n%s", body)
	}
	if strings.Contains(body, "b.ge") {
		t.Errorf("unsigned `if (a < b)` wrongly used the signed `b.ge`:\n%s", body)
	}
}

func TestCmpBranchFusionNegation(t *testing.T) {
	// `if (!(a < b))` folds the OpNot into the branch: the body runs on
	// a >= b, so the branch that reaches the else-label fires when a < b —
	// `b.lt`, the opposite parity of the un-negated case.
	asm := compile(t, `@noinline function f(a: i32, b: i32): i32 { if (!(a < b)) { return 1; } return 2; }
function main(): i32 { return f(1, 2); }`, Options{})
	body := fnBody(t, asm, "f")
	if !regexp.MustCompile(`(?m)^\s*cmp w1, w0\n\s*b\.lt `).MatchString(body) {
		t.Errorf("expected fused `cmp w1, w0; b.lt` for `if (!(a < b))`, got:\n%s", body)
	}
}

func TestCmpBranchFusionWhile(t *testing.T) {
	// A `while (i < n)` loop guard is an OpBrIf-shaped consumer; it must
	// fuse too (the hot path the #4378 benchmark measured).
	asm := compile(t, `@noinline function f(n: i32): i32 { var i: i32 = 0; while (i < n) { i = i + 1; } return i; }
function main(): i32 { return f(3); }`, Options{})
	body := fnBody(t, asm, "f")
	if strings.Contains(body, "cset") {
		t.Errorf("while-loop guard did not fuse (`cset` present):\n%s", body)
	}
}

// A boolean that is not a comparison's result reaches the branch through
// OpNot, which tryFuseCmpBranch does not cover: the run of OpNots has no
// comparison in front of it. tryFuseNotBranch folds that run into the
// branch's own zero test, leaving `cmp w0, #0 / cset w0, eq` out entirely.
// The answers that fold produces are pinned by TestNotBranchFusionMatrix in
// internal/e2e, on both native backends.

// notBranchProg puts a call result — not a comparison — behind the `!`.
const notBranchProg = `@noinline function flag(x: i32): boolean { return x > 2; }
@noinline function f(x: i32): i32 { var b: boolean = flag(x); if (!b) { return 10; } return 20; }
@noinline function g(x: i32): i32 { var b: boolean = flag(x); var i: i32 = 0; while (!b) { i = i + 1; b = true; } return i; }
@noinline function h(x: i32): i32 { var b: boolean = flag(x); if (!!b) { return 30; } return 40; }
function main(): i32 { return f(1) + g(1) + h(1); }`

func TestNotBranchFusionShape(t *testing.T) {
	asm := compile(t, notBranchProg, Options{})

	// `if (!b)` leaves for the else arm when !b is false, i.e. when b holds.
	body := fnBody(t, asm, "f")
	if !regexp.MustCompile(`(?m)^\s*cbnz w0, \.LifElse`).MatchString(body) {
		t.Errorf("expected fused `cbnz` for `if (!b)`, got:\n%s", body)
	}
	if strings.Contains(body, "cset") {
		t.Errorf("un-fused `cset` still present in `if (!b)`:\n%s", body)
	}

	// An even run of OpNots is the identity, so the polarity comes back.
	body = fnBody(t, asm, "h")
	if !regexp.MustCompile(`(?m)^\s*cbz w0, \.LifElse`).MatchString(body) {
		t.Errorf("expected fused `cbz` for `if (!!b)`, got:\n%s", body)
	}

	// A `while (!b)` guard is the OpBrIf-shaped consumer.
	body = fnBody(t, asm, "g")
	if strings.Contains(body, "cset") {
		t.Errorf("while-loop guard did not fuse (`cset` present):\n%s", body)
	}
}
