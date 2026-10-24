package modload_test

import (
	"path/filepath"
	"testing"

	"github.com/jakechampion/lang/internal/check/checker"
	"github.com/jakechampion/lang/internal/pkg/modload"
)

func TestImportedTraitBoundsAreQualified(t *testing.T) {
	dir := writeFiles(t, map[string]string{
		"seq.fern": `pub trait It[T] {
    function next(self: Self): Option[(T, Self)];
    function zip[U, J: It[U]](self: Self, other: J): Pair[Self, J] {
        return Pair { a: self, b: other };
    }
    function count(self: Self): i32 {
        let n: i32 = 0;
        let cur: Self = self;
        let go: boolean = true;
        while (go) {
            match (cur.next()) {
                Some(t) => { n = n + 1; cur = t.1; },
                None => { go = false; }
            }
        }
        return n;
    }
}
pub struct Upto { at: i32, end: i32 }
impl It[i32] for Upto {
    function next(self: Upto): Option[(i32, Upto)] {
        if (self.at >= self.end) { return None; }
        return Some((self.at, Upto { at: self.at + 1, end: self.end }));
    }
}
pub struct Pair[I, J] { a: I, b: J }
impl[I: It[i32], J: It[i32]] It[i32] for Pair[I, J] {
    function next(self: Pair[I, J]): Option[(i32, Pair[I, J])] {
        match (self.a.next()) {
            Some(x) => {
                match (self.b.next()) {
                    Some(y) => { return Some((x.0 + y.0, Pair { a: x.1, b: y.1 })); },
                    None => { return None; }
                }
            },
            None => { return None; }
        }
    }
}`,
		"main.fern": `import "seq";
function main(): i32 {
    let a: seq.Upto = seq.Upto { at: 0, end: 5 };
    return a.zip(seq.Upto { at: 0, end: 3 }).count();
}`,
	})
	prog, _, err := modload.Load(filepath.Join(dir, "main.fern"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := checker.Check(prog); err != nil {
		t.Fatalf("an imported trait's bounds should resolve: %v", err)
	}
}
