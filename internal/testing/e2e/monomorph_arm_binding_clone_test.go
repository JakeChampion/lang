package e2e

import (
	"os"
	"path/filepath"
	"testing"
)

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
    let e: E = Lit { has_base: true };
    let n: i32 = fold(e, 0i32, bump);
    let seed: string[] = [];
    let ss: string[] = fold(e, seed, tag);
    return n + ss.len();
}
`

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
    let lo: E = Lo { v: 1i32 };
    let hi: E = Hi { v: 200i32 };
    let n: i32 = fold(lo, 0i32, bump) + fold(hi, 0i32, bump);
    let seed: string[] = [];
    let ss: string[] = fold(hi, seed, tag);
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
