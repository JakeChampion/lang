package e2e

import (
	"os"
	"path/filepath"
	"testing"
)

// A generic function whose `match` binds the SAME NAME in two arms, instantiated
// more than once.
//
// `ast.CloneBlock` shallow-copied a MatchArm, so every monomorphised clone
// shared one `Bindings` backing array while each got its own deep-copied body.
// shadowrename then renamed the second arm's binding in the first instantiation
// — writing through the shared array, and rewriting only that instantiation's
// body — and every later clone's pattern said `p$1` while its body still said
// `p`. The stale `p` resolved to the OTHER arm's binding of that name, so the
// arm read a slot its own arm never stored.
//
// Both observable halves are covered, because they fail differently:
//
//   - `arm_binding_clone_shapes`: the two arms bind different struct types, so
//     the wrong resolution is a type error and lowering aborts with
//     `ir: struct Slice has no field "has_base"` — a build failure.
//   - `arm_binding_clone_same_shape`: the two arms bind types of the same shape,
//     so nothing objects and the arm dereferences an unwritten slot — a
//     segfault, and on a different layout a silently wrong answer.
//
// The self-host compiler's `astwalk.fold_expr` is exactly this shape (its
// `ExprSlice` and `ExprStructLit` arms both bind `sl`) and it is instantiated
// three times, which is how this surfaced (#6993).
const armBindingCloneShapes = `struct Slice { arr: i32 }
struct Lit { has_base: boolean }
type E = Slice | Lit;

function fold[T](e: E, acc: T, visit: (E, T) => T): T {
    acc = visit(e, acc);
    match (e) {
        Slice(sl) => { return acc; },
        Lit(sl) => {
            if (sl.has_base) { return acc; }
            return acc;
        }
    }
    return acc;
}

function bump(e: E, acc: i32): i32 { return acc + 1i32; }
function tag(e: E, acc: string[]): string[] { return acc.append("t"); }

function main(): i32 {
    var e: E = Lit { has_base: true };
    var n: i32 = fold(e, 0i32, bump);
    var seed: string[] = [];
    var ss: string[] = fold(e, seed, tag);
    return n + ss.len();
}
`

// The same-shape half: both payloads are one-i32 structs, so the stale
// reference type-checks and the arm reads a slot only the other arm writes.
const armBindingCloneSameShape = `struct Lo { v: i32 }
struct Hi { v: i32 }
type E = Lo | Hi;

function fold[T](e: E, acc: T, visit: (E, T) => T): T {
    match (e) {
        Lo(p) => { if (p.v > 100i32) { return acc; } return visit(e, acc); },
        Hi(p) => { if (p.v > 100i32) { return visit(e, acc); } return acc; }
    }
    return acc;
}

function bump(e: E, acc: i32): i32 { return acc + 1i32; }
function tag(e: E, acc: string[]): string[] { return acc.append("t"); }

function main(): i32 {
    var lo: E = Lo { v: 1i32 };
    var hi: E = Hi { v: 200i32 };
    var n: i32 = fold(lo, 0i32, bump) + fold(hi, 0i32, bump);
    var seed: string[] = [];
    var ss: string[] = fold(hi, seed, tag);
    return n * 10i32 + ss.len();
}
`

var armBindingCloneCases = []struct {
	name string
	src  string
	want int
}{
	{"arm_binding_clone_shapes", armBindingCloneShapes, 2},
	{"arm_binding_clone_same_shape", armBindingCloneSameShape, 21},
}

// TestNativeMonomorphArmBindingClone pins the clone on the native backends.
// The interp leg is the oracle: it runs off the same monomorphised AST, so a
// leg disagreeing with it is a lowering answer rather than a language question.
func TestNativeMonomorphArmBindingClone(t *testing.T) {
	for _, tc := range armBindingCloneCases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			p := filepath.Join(dir, "main.fern")
			if err := os.WriteFile(p, []byte(tc.src), 0o644); err != nil {
				t.Fatalf("write: %v", err)
			}
			if _, code := runFixtureInterp(t, p, ""); code != tc.want {
				t.Errorf("interp = %d, want %d", code, tc.want)
			}
			if _, code := runFixtureX86_64(t, p, ""); code != tc.want {
				t.Errorf("x86-64 = %d, want %d", code, tc.want)
			}
			if code := runWasm(t, tc.src); code != tc.want {
				t.Errorf("wasm = %d, want %d", code, tc.want)
			}
		})
	}
}
