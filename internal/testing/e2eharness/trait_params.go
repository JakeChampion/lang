package e2eharness

import (
	"os"
	"path/filepath"
	"testing"
)

// A parameter typed by a trait is an anonymous type parameter bounded by it.
// The project names a trait three ways: the entry's own (`total`), an
// imported module's own from inside that module (`shapes.twice`), and an
// imported one by its qualifier (`thrice`). main answers 0 when every call
// dispatched to the right impl, and the sum it got otherwise.
const traitParamsShapes = `pub trait Sided { function sides(self: Self): i32; }
pub struct Hex { r: i32 }
impl Sided for Hex { function sides(self: Hex): i32 { return 6; } }
pub function twice(s: Sided): i32 { return s.sides() * 2; }
`

const traitParamsMain = `import "./shapes";

trait Shape { function area(self: Self): i32; }
struct Sq { s: i32 }
impl Shape for Sq { function area(self: Sq): i32 { return self.s * self.s; } }
struct Rect { w: i32, h: i32 }
impl Shape for Rect { function area(self: Rect): i32 { return self.w * self.h; } }

function total(a: Shape, b: Shape): i32 { return a.area() + b.area(); }

struct Tri { b: i32 }
impl shapes.Sided for Tri { function sides(self: Tri): i32 { return 3; } }

function thrice(x: shapes.Sided): i32 { return x.sides() * 3; }

function main(): i32 {
    // 9 + 10, then 6 + 12, then 9 + 18.
    let n: i32 = total(Sq { s: 3 }, Rect { w: 2, h: 5 })
        + shapes.twice(Tri { b: 1 }) + shapes.twice(shapes.Hex { r: 0 })
        + thrice(Tri { b: 2 }) + thrice(shapes.Hex { r: 1 });
    if (n == 64) {
        return 0;
    }
    return n;
}
`

// WriteTraitParamsProject writes the two-module project into a fresh
// directory and returns the entry file's path.
func WriteTraitParamsProject(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for name, src := range map[string]string{"shapes.fern": traitParamsShapes, "main.fern": traitParamsMain} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return filepath.Join(dir, "main.fern")
}
