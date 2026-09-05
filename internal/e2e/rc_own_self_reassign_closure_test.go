package e2e

import (
	"testing"

	"github.com/jakechampion/lang/internal/ast"
)

// The `own` self-reassign move — `a = grow(a, n)` where `grow` consumes `a` —
// written inside a CLOSURE. The shape itself is pinned at top level elsewhere;
// what is new here is that the checker admits it one nesting level in (#7452),
// so the rc side of it runs for the first time from a hoisted lambda body.
//
// The old `a` is moved into the callee, which deep-drops it at its own exit, so
// the overwrite must not drop it a second time. closureconv hoists the lambda
// to a top-level function before IR sees it, which is exactly why the same
// `callConsumesIdent` suppression applies unchanged — this pins that it does.
const ownSelfReassignClosureSrc = `struct B { items: i32[], tag: i32[] }
function grow(own b: B, x: i32): B { return B { items: b.items.append(x), tag: b.tag }; }
function main(): i32 {
    var k: i32 = 0;
    while (k < 100) {
        var build = (n: i32): i32 => {
            var a: B = B { items: [], tag: [42] };
            var i: i32 = 0;
            while (i < n) { a = grow(a, i); i = i + 1; }
            if (a.tag[0] != 42) { return 90; }   // carried field survives the threading
            return a.items.len();
        };
        if (build(6) != 6) { return 91; }
        k = k + 1;
    }
    return __rc_underflow_count();   // 0 == no double-free of the moved-out struct
}`

func TestX86_64OwnSelfReassignInClosure(t *testing.T) {
	if _, code := compileAndRunX86_64FreeOn(t, ownSelfReassignClosureSrc); code != 0 {
		t.Errorf("own self-reassign in a closure: got %d, want 0", code)
	}
}

func TestArm64OwnSelfReassignInClosure(t *testing.T) {
	if _, code := compileAndRunArm64FreeOn(t, ownSelfReassignClosureSrc); code != 0 {
		t.Errorf("own self-reassign in a closure: got %d, want 0", code)
	}
}

func TestWASMOwnSelfReassignInClosure(t *testing.T) {
	prev := ast.RcFreeEnabled
	ast.RcFreeEnabled = true
	defer func() { ast.RcFreeEnabled = prev }()
	if got := runWasm(t, ownSelfReassignClosureSrc); got != 0 {
		t.Errorf("own self-reassign in a closure: got %d, want 0", got)
	}
}
