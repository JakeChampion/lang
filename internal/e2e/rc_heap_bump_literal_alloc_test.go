package e2e

import (
	"testing"

	"github.com/jakechampion/lang/internal/ast"
)

// Literal-sized buffer reclamation (scalar-arg taint, docs/RC-PERCEUS-PLAN.md).
// rhsTainted used to taint EVERY NumberLit (it fell through to the conservative
// default), so a fresh owned buffer whose only "borrowed" input is a literal
// size arg — `var b: u8[] = __alloc_u8(8)` — read as ineligible and was not
// reclaimed at its last reference. NumberLit/FloatLit/BoolLit are now untainted
// (they alias nothing), so such a pure temp reclaims, lowering the steady-state
// heap high-water of hot scratch-buffer code (e.g. int_to_string_radix's
// 33-byte `digits` reached via `(n).to_hex()`). The bump is bounded either way
// (the freelist already caps it), so this asserts the high-water is STABLE
// across a 10x iteration count — a reclaimed temp keeps it flat.
//
// Two guards keep this safe and are exercised elsewhere:
//   - a buffer cast to a raw integer (`buf as usize`) is escape-tainted in
//     computeFreeEligible, so int_to_string's scratch stays protected (heavy
//     coverage via every `(n).to_string()` / radix path);
//   - random_bytes' result is a fresh rc=1 u8[] box, untainted like
//     __alloc_u8's — guarded by the TestX/Arm64/WASM RandomBytes tests.
// The scalar-BINARY arg case is deliberately left tainted: untainting it
// over-released int_to_string_radix's result buffer (see TestToRgbHexNoOverRelease).

func literalAllocBumpSrc(n string) string {
	return `function main(): i32 {
    var before: i32 = (__heap_bump_bytes() as i32);
    var i: i32 = 0;
    var acc: i32 = 0;
    while (i < ` + n + `) {
        var b: u8[] = __alloc_u8(8);
        b = b.with(0, (i % 200) as u8);
        b = b.with(1, ((i + 1) % 200) as u8);
        acc = acc + (b[0] as i32) + (b[1] as i32);
        i = i + 1;
    }
    if (acc < 0) { return -1; }
    return (__heap_bump_bytes() as i32) - before;
}`
}

func TestX86_64LiteralAllocReclaim(t *testing.T) {
	if s, l := mustRunX86_64FreeOn(t, literalAllocBumpSrc("5000")), mustRunX86_64FreeOn(t, literalAllocBumpSrc("50000")); s != l {
		t.Errorf("literal-sized temp bump should be bounded: N=5000 -> %d, N=50000 -> %d", s, l)
	}
}

func TestArm64LiteralAllocReclaim(t *testing.T) {
	if s, l := mustRunArm64FreeOn(t, literalAllocBumpSrc("5000")), mustRunArm64FreeOn(t, literalAllocBumpSrc("50000")); s != l {
		t.Errorf("literal-sized temp bump should be bounded: N=5000 -> %d, N=50000 -> %d", s, l)
	}
}

func TestWASMLiteralAllocReclaim(t *testing.T) {
	prev := ast.RcFreeEnabled
	ast.RcFreeEnabled = true
	defer func() { ast.RcFreeEnabled = prev }()
	s, l := runWasm(t, literalAllocBumpSrc("5000")), runWasm(t, literalAllocBumpSrc("50000"))
	if s != l {
		t.Errorf("literal-sized temp bump should be bounded: N=5000 -> %d, N=50000 -> %d", s, l)
	}
	if s == 0 {
		t.Errorf("wasm heap-allocates; expected a non-zero bounded high-water, got 0")
	}
}

// Over-release regression: int_to_string_radix (reached via (n).to_hex() →
// (n).to_rgb_hex()) builds a result buffer sized by a scalar BINARY (`k + 1`).
// Untainting scalar binaries made that buffer eligible and the exit sweep
// over-released it under free-on, so to_rgb_hex returned the wrong hex for
// values whose component layout triggered the freelist reuse (e.g. 65280 →
// "#00ff00"). Locks the value-correctness on all native backends with free on.
const toRgbHexOverRelease = `import "std/i32";
function main(): i32 {
    if ((0).to_rgb_hex() != "#000000") { return 1; }
    if ((255).to_rgb_hex() != "#0000ff") { return 2; }
    if ((65280).to_rgb_hex() != "#00ff00") { return 3; }
    if ((16711680).to_rgb_hex() != "#ff0000") { return 4; }
    if ((16777215).to_rgb_hex() != "#ffffff") { return 5; }
    return 0;
}`

func TestToRgbHexNoOverRelease(t *testing.T) {
	// Per-backend sub-tests: the arm64 leg is unreachable behind the x86 one
	// on any host missing a toolchain, because the gate inside each runner is
	// a t.Skip that ends the whole function.
	t.Run("x86_64", func(t *testing.T) {
		if _, code := compileAndRunX86_64FreeOn(t, toRgbHexOverRelease); code != 0 {
			t.Errorf("x86_64: to_rgb_hex over-release/value error, code=%d", code)
		}
	})
	t.Run("arm64-linux", func(t *testing.T) {
		if _, code := compileAndRunArm64FreeOn(t, toRgbHexOverRelease); code != 0 {
			t.Errorf("arm64: to_rgb_hex over-release/value error, code=%d", code)
		}
	})
}
