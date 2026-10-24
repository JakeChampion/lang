package e2e

import (
	"testing"

	"github.com/jakechampion/lang/internal/check/checker"
	"github.com/jakechampion/lang/internal/pkg/modload"
)

func TestStdArrayIndexOfContainsGeneric(t *testing.T) {
	t.Run("check i32[] receiver with std/i32 imported", func(t *testing.T) {
		src := `import "std/i32";
function main(): i32 {
    let xs: i32[] = [1, 2, 3];
    let i: Option[i32] = xs.index_of(2);
    if (xs.contains(3)) { match (i) { Some(v) => { return v; }, None => { return 0 - 1; } } }
    return 0 - 1;
}`
		prog, _, err := modload.LoadSource(src)
		if err != nil {
			t.Fatalf("load: %v", err)
		}
		if _, err := checker.Check(prog); err != nil {
			t.Fatalf("i32[] .index_of/.contains must type-check, got: %v", err)
		}
	})

	t.Run("check every element receiver", func(t *testing.T) {
		for _, recv := range []struct{ name, ty, lit, target string }{
			{"i32", "i32[]", "[1, 2, 3]", "2"},
			{"i64", "i64[]", "[10, 20, 30]", "20"},
			{"u8", "u8[]", "[7, 8, 9]", "8"},
			{"u64", "u64[]", "[1, 2, 3]", "2"},
			{"f64", "f64[]", "[1.5, 2.5]", "2.5"},
			{"string", "string[]", `["a", "bb"]`, `"bb"`},
			{"boolean", "boolean[]", "[true, false]", "false"},
		} {
			recv := recv
			t.Run(recv.name, func(t *testing.T) {
				src := `import "std/i32";
function main(): i32 {
    let xs: ` + recv.ty + ` = ` + recv.lit + `;
    if (xs.contains(` + recv.target + `)) {
        match (xs.index_of(` + recv.target + `)) { Some(v) => { return v; }, None => { return 0 - 1; } }
    }
    return 0 - 1;
}`
				prog, _, err := modload.LoadSource(src)
				if err != nil {
					t.Fatalf("load: %v", err)
				}
				if _, err := checker.Check(prog); err != nil {
					t.Fatalf("%s receiver must type-check, got: %v", recv.ty, err)
				}
			})
		}
	})

	cases := []struct {
		name string
		src  string
		want int
	}{
		{
			name: "i32[] hit / miss / absent",
			src: `import "std/i32";
function main(): i32 {
    let xs: i32[] = [1, 2, 3];
    match (xs.index_of(2)) { Some(v) => { if (v != 1) { return 1; } }, None => { return 1; } }
    match (xs.index_of(1)) { Some(v) => { if (v != 0) { return 2; } }, None => { return 2; } }
    match (xs.index_of(9)) { Some(_) => { return 3; }, None => {} }
    if (!xs.contains(3)) { return 4; }
    if (xs.contains(9)) { return 5; }
    let empty: i32[] = [];
    match (empty.index_of(1)) { Some(_) => { return 6; }, None => {} }
    if (empty.contains(1)) { return 7; }
    return 42;
}`,
			want: 42,
		},
		{
			name: "i64[] receiver",
			src: `import "std/array";
function main(): i32 {
    let xs: i64[] = [10, 20, 30];
    match (xs.index_of(30)) { Some(v) => { if (v != 2) { return 1; } }, None => { return 1; } }
    match (xs.index_of(11)) { Some(_) => { return 2; }, None => {} }
    if (!xs.contains(20)) { return 3; }
    if (xs.contains(21)) { return 4; }
    return 42;
}`,
			want: 42,
		},
		{
			name: "u8[] receiver",
			src: `import "std/array";
function main(): i32 {
    let xs: u8[] = [7, 8, 9];
    match (xs.index_of(9)) { Some(v) => { if (v != 2) { return 1; } }, None => { return 1; } }
    match (xs.index_of(6)) { Some(_) => { return 2; }, None => {} }
    if (!xs.contains(8)) { return 3; }
    if (xs.contains(6)) { return 4; }
    return 42;
}`,
			want: 42,
		},
		{
			name: "string[] receiver keeps content equality",
			src: `import "std/array";
function main(): i32 {
    let xs: string[] = ["a", "bb", "ccc"];
    match (xs.index_of("bb")) { Some(v) => { if (v != 1) { return 1; } }, None => { return 1; } }
    match (xs.index_of("zz")) { Some(_) => { return 2; }, None => {} }
    if (!xs.contains("ccc")) { return 3; }
    if (xs.contains("dddd")) { return 4; }
    if (!xs.contains("c" + "cc")) { return 5; }
    match (xs.index_of("b" + "b")) { Some(v) => { if (v != 1) { return 6; } }, None => { return 6; } }
    return 42;
}`,
			want: 42,
		},
		{
			name: "@derive(Eq) struct element",
			src: `import "std/array";
import "core/cmp" as cmp;
@derive(cmp.Eq)
struct Point { x: i32, y: i32 }
function main(): i32 {
    let ps: Point[] = [Point { x: 1, y: 2 }, Point { x: 3, y: 4 }];
    match (ps.index_of(Point { x: 3, y: 4 })) { Some(v) => { if (v != 1) { return 1; } }, None => { return 1; } }
    match (ps.index_of(Point { x: 1, y: 2 })) { Some(v) => { if (v != 0) { return 2; } }, None => { return 2; } }
    match (ps.index_of(Point { x: 3, y: 9 })) { Some(_) => { return 3; }, None => {} }
    if (!ps.contains(Point { x: 1, y: 2 })) { return 4; }
    if (ps.contains(Point { x: 9, y: 9 })) { return 5; }
    return 42;
}`,
			want: 42,
		},
		{
			name: "mixed element types in one program",
			src: `import "std/array";
function main(): i32 {
    let xs: i32[] = [1, 2, 3];
    let ss: string[] = ["a", "b"];
    match (xs.index_of(3)) { Some(v) => { if (v != 2) { return 1; } }, None => { return 1; } }
    match (ss.index_of("b")) { Some(v) => { if (v != 1) { return 2; } }, None => { return 2; } }
    if (!xs.contains(1)) { return 3; }
    if (!ss.contains("a")) { return 4; }
    if (xs.contains(0)) { return 5; }
    if (ss.contains("z")) { return 6; }
    return 42;
}`,
			want: 42,
		},
	}

	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			if got := runInterpByte(t, c.src); got != c.want {
				t.Errorf("interp: got exit %d, want %d", got, c.want)
			}
			t.Run("wasm", func(t *testing.T) {
				if got := compileAndRunWasmbinMain(t, c.src); got != c.want {
					t.Errorf("wasm: got exit %d, want %d", got, c.want)
				}
			})
			t.Run("x86-64 native", func(t *testing.T) {
				if _, got := compileAndRunX86_64(t, c.src); got != c.want {
					t.Errorf("x86-64 native: got exit %d, want %d", got, c.want)
				}
			})
		})
	}
}
