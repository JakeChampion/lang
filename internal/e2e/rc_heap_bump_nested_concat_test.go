package e2e

import (
	"testing"

	"github.com/jakechampion/lang/internal/ast"
)

// Nested-concat temporary reclamation (RC-Perceus, statement-temporary
// mechanism). Single-level statement temporaries already reclaim, but a
// CHAINED / parenthesised concat `a + b + c` (= `(a + b) + c`) orphaned
// each inner intermediate: OpStrConcat copies the inner `(a + b)` buffer's
// bytes but never freed it, so a chain leaked one buffer per join
// (`(a + b + a).len()` in a loop grew 192288 B → 1632288 B, unbounded).
// The concat lowering now stashes an operand that is itself an owned
// string temp (isOwnedStringTemp: a sub-concat or string slice), uses it
// for the concat, then dec's it (ABI-correct: __fern_str_dec two-word /
// __fern_rc_dec x86_64). It recurses — each level frees its own immediate
// intermediate — so a whole left-/right-nested chain reclaims. Borrowed
// operands (idents / literals) are not stashed (decing would free a live
// value).

func nestedConcatBumpSrc(n string) string {
	return `function main(): i32 {
    var before: i32 = (__heap_bump_bytes() as i32);
    var a: string = "longer_string_one_here";
    var b: string = "longer_string_two_here";
    var i: i32 = 0;
    var acc: i32 = 0;
    while (i < ` + n + `) {
        acc = acc + (a + b + a + b).len();
        i = i + 1;
    }
    return ((__heap_bump_bytes() as i32) - before) + (acc - acc);
}`
}

// Deep chain: value-correctness (length + content) + 0 over-release.
const nestedConcatUnderflowSrc = `function main(): i32 {
    var a: string = "ab";
    var b: string = "cd";
    var c: string = "ef";
    var i: i32 = 0;
    var acc: i32 = 0;
    while (i < 200) {
        var s: string = a + b + c + a; // "abcdefab", len 8
        acc = acc + s.len();
        i = i + 1;
    }
    if (acc != 1600) { return 999; } // 8 * 200
    var t: string = a + b + c;       // content check
    if (t != "abcdef") { return 888; }
    return __rc_underflow_count();
}`

func TestX86_64NestedConcatReclaim(t *testing.T) {
	small := mustRunX86_64FreeOn(t, nestedConcatBumpSrc("5000"))
	large := mustRunX86_64FreeOn(t, nestedConcatBumpSrc("50000"))
	if small != large {
		t.Errorf("nested-concat bump should be bounded: N=5000 -> %d, N=50000 -> %d", small, large)
	}
	if _, code := compileAndRunX86_64FreeOn(t, nestedConcatUnderflowSrc); code != 0 {
		t.Errorf("nested-concat reclaim: code=%d (999=len, 888=content, >0=over-release)", code)
	}
}

func TestArm64NestedConcatReclaim(t *testing.T) {
	small := mustRunArm64FreeOn(t, nestedConcatBumpSrc("5000"))
	large := mustRunArm64FreeOn(t, nestedConcatBumpSrc("50000"))
	if small != large {
		t.Errorf("nested-concat bump should be bounded: N=5000 -> %d, N=50000 -> %d", small, large)
	}
	if _, code := compileAndRunArm64FreeOn(t, nestedConcatUnderflowSrc); code != 0 {
		t.Errorf("nested-concat reclaim: code=%d", code)
	}
}

func TestWASMNestedConcatReclaim(t *testing.T) {
	prev := ast.RcFreeEnabled
	ast.RcFreeEnabled = true
	defer func() { ast.RcFreeEnabled = prev }()
	small := runWasm(t, nestedConcatBumpSrc("5000"))
	large := runWasm(t, nestedConcatBumpSrc("50000"))
	if small != large {
		t.Errorf("nested-concat bump should be bounded: N=5000 -> %d, N=50000 -> %d", small, large)
	}
	if small == 0 {
		t.Errorf("wasm two-word strings heap-allocate; expected a non-zero bounded high-water, got 0")
	}
	if got := runWasm(t, nestedConcatUnderflowSrc); got != 0 {
		t.Errorf("nested-concat reclaim: got %d", got)
	}
}
