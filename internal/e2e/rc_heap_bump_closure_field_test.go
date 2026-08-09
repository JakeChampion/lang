package e2e

import (
	"testing"

	"github.com/jakechampion/lang/internal/ast"
)

// A closure stored in a struct FIELD is reclaimed with the struct (#6443).
//
// `appendChildDrop` — the shared child-release used by `__drop_struct_<N>`,
// `__drop_enum_<N>` and `__drop_tuple_<m>` — had no `*ast.FuncType` arm, so a
// closure field fell to the bare `__fern_rc_dec` fall-through. That zeroes the
// pair's count and stops: the pair block, the env block and every rc-tracked
// capture were stranded, three blocks per instance.
//
// A closure LOCAL never had this problem — `b.closureTarget` names the single
// closure it can hold, so its drop calls that closure's `__closure_drop_<name>`
// thunk directly. A closure reached through a container cannot name which
// closure it holds (the field type is just `(T) => R`), which is why the
// release has to dispatch through the drop-fn pointer the pair carries. The
// array-of-closure path already did exactly that per element; this routes the
// other three container kinds through the same body.
//
// Run on all three compiled backends: the release is generated IR, not a
// per-backend runtime, so a backend that mis-emits `OpCallIndirect` through the
// pair's drop-fn slot fails here rather than silently leaking.
//
// The probe is a provider table — the shape the bug was found in. Each round
// builds eight records holding a capturing closure, calls each one, and
// discards the lot, so nothing survives a round and the bump cursor must not
// move between 100 rounds and 200. Leaked it was ~512 B/round.
const closureFieldChurnSrc = `import "std/i32";
import "std/i64";

struct P { name: string, f: (i32) => i32 }

function mkP(n: i32): P {
    return P { name: "provider" + n.to_string(), f: (x: i32) => x + n };
}

function round(): i32 {
    var t: i32 = 0;
    var ps: P[] = [];
    var i: i32 = 0;
    while (i < 8) { ps = ps.append(mkP(i)); i = i + 1; }
    var j: i32 = 0;
    while (j < ps.len()) { t = t + (ps[j].f)(1); j = j + 1; }
    return t;
}

function churn(n: i32): i32 {
    var t: i32 = 0;
    var r: i32 = 0;
    while (r < n) { t = t + round(); r = r + 1; }
    if (t != n * 36) { return 99; }
    if ((__heap_bump_bytes() as i32) < 65536) { return 0; }
    return 1;
}

function main(): i32 { return churn(2000); }`

func TestX86_64ClosureInStructFieldRecycles(t *testing.T) {
	if _, code := compileAndRunX86_64FreeOn(t, closureFieldChurnSrc); code != 0 {
		t.Errorf("closure-in-struct-field churn: got exit %d, want 0 (heap bump < 64 KiB — the pair, its env and its captures must recycle with the struct)", code)
	}
}

func TestArm64ClosureInStructFieldRecycles(t *testing.T) {
	if _, code := compileAndRunArm64FreeOn(t, closureFieldChurnSrc); code != 0 {
		t.Errorf("closure-in-struct-field churn: got exit %d, want 0", code)
	}
}

func TestWASMClosureInStructFieldRecycles(t *testing.T) {
	prev := ast.RcFreeEnabled
	ast.RcFreeEnabled = true
	defer func() { ast.RcFreeEnabled = prev }()
	if got := runWasm(t, closureFieldChurnSrc); got != 0 {
		t.Errorf("closure-in-struct-field churn: got exit %d, want 0", got)
	}
}
