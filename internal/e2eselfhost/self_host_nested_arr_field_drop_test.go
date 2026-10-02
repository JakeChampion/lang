package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// A `T[][]` held by a struct field or a tuple element was never released on
// the AST lowering: __struct_drop_<T> had no arm for the field, and a tuple
// position holding it took a shallow buffer dec (#10397). Each row is checked
// on every lowering, and the reads that follow a rebind run after fresh
// allocations, so a row released early shows as a wrong answer.
var nestedArrFieldDropCases = []struct {
	name string
	src  string
	want int
}{
	{"literal", `struct Bag { n: i32, grid: i32[][] }
function main(): i32 {
    var r: Bag = Bag { n: 3, grid: [[3, 1], [2, 3]] };
    return r.n + r.grid.len();
}
`, 5},
	{"strarr_literal", `struct Grid { n: i32, rows: string[][] }
function main(): i32 {
    var r: Grid = Grid { n: 3, rows: [["ab", "c"], ["def"]] };
    return r.n + r.rows.len() + r.rows[1][0].len();
}
`, 8},
	{"loop_built_producer", `struct Bag { n: i32, grid: i32[][] }
function build(k: i32): Bag {
    var g: i32[][] = [];
    var i: i32 = 0;
    while (i < k) {
        g = g.append([i, i + 1]);
        i = i + 1;
    }
    return Bag { n: k, grid: g };
}
function main(): i32 {
    var acc: i32 = 0;
    var r: Bag = build(3);
    var j: i32 = 0;
    while (j < 5) {
        r = build(j + 2);
        var junk: i32[][] = [[9, 9], [9, 9], [9, 9]];
        acc = acc + r.grid[1][1] + r.grid.len() + junk.len();
        j = j + 1;
    }
    return acc + r.n;
}
`, 51},
	{"rebind_read_after_churn", `struct Bag { n: i32, grid: i32[][] }
function main(): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 4) {
        var r: Bag = Bag { n: i, grid: [[i, 1], [2, 3]] };
        acc = acc + r.grid[0][0];
        r = Bag { n: 7, grid: [[5, i]] };
        var junk: i32[][] = [[8, 8], [8, 8]];
        acc = acc + r.grid[0][1] + r.grid[0][0] + junk.len() + r.n;
        i = i + 1;
    }
    return acc;
}
`, 68},
	{"strarr_producer_rebind", `struct Grid { n: i32, rows: string[][] }
function mk(k: i32): Grid {
    return Grid { n: k, rows: [["ab", "c"], ["def", "g"]] };
}
function main(): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 4) {
        var g: Grid = mk(i);
        acc = acc + g.rows[0][0].len();
        g = mk(i + 10);
        var junk: string[][] = [["zzzz"], ["zzzz"]];
        acc = acc + g.rows[1][0].len() + g.rows.len() + junk.len() + g.n;
        i = i + 1;
    }
    return acc;
}
`, 82},
	{"shared_into_struct_array", `struct Bag { n: i32, grid: i32[][] }
function main(): i32 {
    var g: i32[][] = [[1, 2], [3]];
    var a: Bag = Bag { n: 1, grid: g };
    var b: Bag = Bag { n: 2, grid: a.grid };
    var bs: Bag[] = [a, b, Bag { n: 3, grid: [[4, 5, 6]] }];
    var t: i32 = 0;
    for x in bs { t = t + x.grid.len() + x.grid[0][0]; }
    a = Bag { n: 9, grid: [[0]] };
    var junk: i32[] = [7, 7];
    return t + g[1][0] + b.grid[0][1] + a.n + junk.len();
}
`, 27},
	{"nested_struct", `struct Bag { n: i32, grid: i32[][] }
struct Box2 { b: Bag, m: i32 }
function main(): i32 {
    var o: Box2 = Box2 { b: Bag { n: 1, grid: [[1, 2], [3, 4]] }, m: 2 };
    return o.b.grid[1][1] + o.m;
}
`, 6},
	{"tuple_holds_field", `struct Bag { n: i32, grid: i32[][] }
function main(): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 3) {
        var r: Bag = Bag { n: i, grid: [[i, 1], [2, 3]] };
        var p: (i32, i32[][]) = (r.n, r.grid);
        r = Bag { n: 7, grid: [[5]] };
        var junk: i32[][] = [[8, 8], [8, 8]];
        acc = acc + p.1[0][0] + p.1[1][1] + p.0 + r.grid[0][0] + junk.len();
        i = i + 1;
    }
    return acc;
}
`, 36},
	{"tuple_holds_strarr_field", `struct Grid { n: i32, rows: string[][] }
function main(): i32 {
    var r: Grid = Grid { n: 2, rows: [["ab", "c"], ["def"]] };
    var p: (i32, string[][]) = (r.n, r.rows);
    r = Grid { n: 1, rows: [["x"]] };
    return p.1[1][0].len() + p.0 + r.rows.len();
}
`, 6},
	{"tuple_holds_local", `function main(): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 3) {
        var g: i32[][] = [[i, 1], [2, 3]];
        var t: (i32, i32[][]) = (3, g);
        var junk: i32[][] = [[8, 8], [8, 8]];
        acc = acc + t.0 + t.1.len() + t.1[0][0] + g[1][1] + junk.len();
        i = i + 1;
    }
    return acc;
}
`, 33},
}

func writeNestedArrFieldDropSrc(t *testing.T, name, src string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name+".fern")
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestSelfHostNestedArrFieldDropX86_64(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, tc := range nestedArrFieldDropCases {
		src := writeNestedArrFieldDropSrc(t, tc.name, tc.src)
		t.Run(tc.name, func(t *testing.T) {
			stderr, exit := runWithStdin(t, cli.runner, cli.x86Binary(t, src, "FERN_LEAKCHECK=1"), nil)
			if exit != tc.want {
				t.Fatalf("leakcheck: exit = %d, want %d\n%s", exit, tc.want, stderr)
			}
			assertBalancedCensus(t, stderr)
			stderr, exit = runWithStdin(t, cli.runner, cli.x86Binary(t, src, "FERN_SANITIZE=1"), nil)
			if exit != tc.want || forArrStructSanitizerFault(stderr, true) {
				t.Fatalf("sanitize: exit = %d, want %d, and no sanitizer report\n%s", exit, tc.want, stderr)
			}
		})
	}
}

func TestSelfHostNestedArrFieldDropArm64(t *testing.T) {
	armgcc, qemu := arm64Tooling(t)
	cli := buildSelfHostCLI(t)
	for _, tc := range nestedArrFieldDropCases {
		src := writeNestedArrFieldDropSrc(t, tc.name, tc.src)
		t.Run(tc.name, func(t *testing.T) {
			asm, err := os.ReadFile(cli.emit(t, src, "arm64-linux", "FERN_LEAKCHECK=1"))
			if err != nil {
				t.Fatal(err)
			}
			cmd := runArm64Bin(qemu, buildBinArm64(t, armgcc, t.TempDir(), tc.name, string(asm)))
			var eb strings.Builder
			cmd.Stderr = &eb
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.want {
				t.Fatalf("exit = %d, want %d\n%s", code, tc.want, eb.String())
			}
			assertBalancedCensus(t, eb.String())
		})
	}
}

func TestSelfHostNestedArrFieldDropWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH")
	}
	cli := buildSelfHostCLI(t)
	for _, tc := range nestedArrFieldDropCases {
		src := writeNestedArrFieldDropSrc(t, tc.name, tc.src)
		t.Run(tc.name, func(t *testing.T) {
			stderr, exit := runWasmCensus(t, cli.emit(t, src, "wasm32-wasi", "FERN_LEAKCHECK=1"))
			if exit != tc.want {
				t.Fatalf("exit = %d, want %d\n%s", exit, tc.want, stderr)
			}
			assertBalancedCensus(t, stderr)
		})
	}
}
