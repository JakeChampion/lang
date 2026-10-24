package e2e

import (
	"testing"
)

const ownSelfReassignClosureSrc = `struct B { items: i32[], tag: i32[] }
function grow(own b: B, x: i32): B { return B { items: b.items.append(x), tag: b.tag }; }
function main(): i32 {
    let k: i32 = 0;
    while (k < 100) {
        let build = (n: i32): i32 => {
            let a: B = B { items: [], tag: [42] };
            let i: i32 = 0;
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
	if got := runWasm(t, ownSelfReassignClosureSrc); got != 0 {
		t.Errorf("own self-reassign in a closure: got %d, want 0", got)
	}
}
