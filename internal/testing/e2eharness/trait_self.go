package e2eharness

import (
	"os"
	"path/filepath"
	"testing"
)

const TraitSelfProgram = `trait A { function scale(self: Self): i32; }
trait B {
    function scale(self: Self): i32;
    function twice(self: Self): i32 { return self.scale() * 2; }
    function thrice(other: Self): i32 { return other.scale() * 3; }
}
struct P { v: i32 }
impl A for P { function scale(self: Self): i32 { return self.v; } }
impl B for P { function scale(self: Self): i32 { return self.v + 1; } }

trait Mk {
    function zero(): Self;
    function one(self: Self): i32;
    function base(self: Self): i32 { let z: Self = Self.zero(); return z.v; }
}
struct Q { v: i32 }
impl Mk for Q {
    function zero(): Self { return Q { v: 5 }; }
    function one(self: Self): i32 { return Self.zero().v + 1; }
}

function main(): i32 {
    let p: P = P { v: 3 };
    let q: Q = Q { v: 9 };
    // 8 + 12 + 6 + 5
    let n: i32 = p.twice() + P.thrice(p) + q.one() + q.base();
    if (n == 31) {
        return 0;
    }
    return n;
}
`

func WriteTraitSelfProgram(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "main.fern")
	if err := os.WriteFile(path, []byte(TraitSelfProgram), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}
