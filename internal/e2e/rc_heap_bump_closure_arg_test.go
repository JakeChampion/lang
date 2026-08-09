package e2e

import (
	"testing"

	"github.com/jakechampion/lang/internal/ast"
)

// A fresh collection handed to a CLOSURE call is reclaimed (#6460).
//
// The stage-(b) arg-temp reclaim only ever ran for a call to a NAMED function
// (`calleeIsFunc && !calleeIsLocal`), so the identical argument passed through
// a function-typed local or param was never released — `frees=0`, none at all,
// not one short. Passing a freshly built collection to a callback is how you
// use an iterator body, a comparator, a visitor or a middleware handler.
//
// The reclaim is gated on a CONCRETE SCALAR result, exactly as the direct path
// is. That restriction is what carries the safety and it is the one whose
// widening to pointer results segfaulted the differential oracle before, so it
// stays as narrow here; the two extra routes a closure could take are both
// closed by the checker (E049 makes a reference-typed capture read-only, and
// `own` cannot appear on a function VALUE's parameter since ast.FuncType
// carries a bare []Type).
//
// All three compiled backends: the temps are stashed in typed scratch slots,
// so a two-word ABI that stores or reloads one at the wrong width fails here.
const closureArgChurnSrc = `import "std/i32";
import "std/string";

function each(f: (i32[]) => i32, g: (string) => i32, n: i32): i32 {
    var t: i32 = 0;
    var i: i32 = 0;
    while (i < n) {
        t = t + f([1, 2, i]) + g("key-that-is-past-sso-" + i.to_string());
        i = i + 1;
    }
    return t;
}

function churn(n: i32): i32 {
    var h: (i32[]) => i32 = (xs: i32[]) => xs.len() + xs[2];
    var s: (string) => i32 = (k: string) => k.len();
    var t: i32 = each(h, s, n);
    if (t <= 0) { return 99; }
    if ((__heap_bump_bytes() as i32) < 65536) { return 0; }
    return 1;
}

function main(): i32 { return churn(20000); }`

func TestX86_64ClosureCallArgRecycles(t *testing.T) {
	if _, code := compileAndRunX86_64FreeOn(t, closureArgChurnSrc); code != 0 {
		t.Errorf("closure-call argument churn: got exit %d, want 0 (heap bump < 64 KiB — a fresh argument to a closure call must be released after the call)", code)
	}
}

func TestArm64ClosureCallArgRecycles(t *testing.T) {
	if _, code := compileAndRunArm64FreeOn(t, closureArgChurnSrc); code != 0 {
		t.Errorf("closure-call argument churn: got exit %d, want 0", code)
	}
}

func TestWASMClosureCallArgRecycles(t *testing.T) {
	prev := ast.RcFreeEnabled
	ast.RcFreeEnabled = true
	defer func() { ast.RcFreeEnabled = prev }()
	if got := runWasm(t, closureArgChurnSrc); got != 0 {
		t.Errorf("closure-call argument churn: got exit %d, want 0", got)
	}
}
