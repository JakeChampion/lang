package modload_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/check/checker"
	"github.com/jakechampion/lang/internal/pkg/modload"
)

const structWrapLib = `pub struct Circle { r: i32 }
pub struct Square { side: i32 }
struct Hidden { n: i32 }
pub type Shape = Circle | Square;
pub type Sealed = Hidden | Circle;

pub function size(s: Shape): i32 {
    match (s) {
        Circle(c) => { return c.r; },
        Square(q) => { return q.side; }
    }
}

pub function circle_size(c: Circle): i32 { return size(Circle(c)); }
`

func TestStructNameCallWrapsUnionMemberAcrossModules(t *testing.T) {
	for name, body := range map[string]string{
		"qualified":          `return shapes.size(shapes.Square(shapes.Square { side: 3 }));`,
		"in imported module": `return shapes.circle_size(shapes.Circle { r: 3 });`,
	} {
		t.Run(name, func(t *testing.T) {
			dir := writeFiles(t, map[string]string{
				"shapes.fern": structWrapLib,
				"main.fern":   `import "./shapes" as shapes; function main(): i32 { ` + body + ` }`,
			})
			prog, _, err := modload.Load(filepath.Join(dir, "main.fern"))
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			if _, err := checker.Check(prog); err != nil {
				t.Fatalf("Check: %v", err)
			}
		})
	}
}

func TestStructNameCallKeepsStructVisibility(t *testing.T) {
	dir := writeFiles(t, map[string]string{
		"shapes.fern": structWrapLib,
		"main.fern":   `import "./shapes" as shapes; function main(): i32 { let c = shapes.Circle { r: 1 }; let s = shapes.Hidden(c); return 0; }`,
	})
	_, _, err := modload.Load(filepath.Join(dir, "main.fern"))
	if err == nil || !strings.Contains(err.Error(), "shapes.Hidden is not exported") {
		t.Fatalf("private struct in a wrap call: got %v", err)
	}
}
