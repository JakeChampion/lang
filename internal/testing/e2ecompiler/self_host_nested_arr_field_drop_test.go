package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// A `T[][]` held by a struct field or a tuple element is released, inner
// arrays included, when its holder dies (#10397). Each row is checked on every
// backend, and the reads that follow a rebind run after fresh
// allocations, so a row released early shows as a wrong answer. A box whose
// release a row tests goes through id or ids, so it is built on the heap rather
// than placed as a constant; a rebind target the test only reads past can stay
// a constant.
var nestedArrFieldDropCases = []struct {
	name string
	src  string
	want int
}{
	{"literal", `@noinline function id(xs: i32[]): i32[] { return xs; }
struct Bag { n: i32, grid: i32[][] }
function main(): i32 {
    let r: Bag = Bag { n: 3, grid: [id([3, 1]), id([2, 3])] };
    return r.n + r.grid.len();
}
`, 5},
	{"strarr_literal", `@noinline function ids(s: string): string { return s; }
struct Grid { n: i32, rows: string[][] }
function main(): i32 {
    let r: Grid = Grid { n: 3, rows: [[ids("ab"), "c"], [ids("def")]] };
    return r.n + r.rows.len() + r.rows[1][0].len();
}
`, 8},
	{"loop_built_producer", `@noinline function id(xs: i32[]): i32[] { return xs; }
struct Bag { n: i32, grid: i32[][] }
function build(k: i32): Bag {
    let g: i32[][] = [];
    let i: i32 = 0;
    while (i < k) {
        g = g.append([i, i + 1]);
        i = i + 1;
    }
    return Bag { n: k, grid: g };
}
function main(): i32 {
    let acc: i32 = 0;
    let r: Bag = build(3);
    let j: i32 = 0;
    while (j < 5) {
        r = build(j + 2);
        let junk: i32[][] = [id([9, 9]), id([9, 9]), id([9, 9])];
        acc = acc + r.grid[1][1] + r.grid.len() + junk.len();
        j = j + 1;
    }
    return acc + r.n;
}
`, 51},
	{"rebind_read_after_churn", `@noinline function id(xs: i32[]): i32[] { return xs; }
struct Bag { n: i32, grid: i32[][] }
function main(): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 4) {
        let r: Bag = Bag { n: i, grid: [[i, 1], [2, 3]] };
        acc = acc + r.grid[0][0];
        r = Bag { n: 7, grid: [[5, i]] };
        let junk: i32[][] = [id([8, 8]), id([8, 8])];
        acc = acc + r.grid[0][1] + r.grid[0][0] + junk.len() + r.n;
        i = i + 1;
    }
    return acc;
}
`, 68},
	{"strarr_producer_rebind", `@noinline function ids(s: string): string { return s; }
struct Grid { n: i32, rows: string[][] }
function mk(k: i32): Grid {
    return Grid { n: k, rows: [[ids("ab"), "c"], [ids("def"), "g"]] };
}
function main(): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 4) {
        let g: Grid = mk(i);
        acc = acc + g.rows[0][0].len();
        g = mk(i + 10);
        let junk: string[][] = [[ids("zzzz")], [ids("zzzz")]];
        acc = acc + g.rows[1][0].len() + g.rows.len() + junk.len() + g.n;
        i = i + 1;
    }
    return acc;
}
`, 82},
	{"shared_into_struct_array", `@noinline function id(xs: i32[]): i32[] { return xs; }
struct Bag { n: i32, grid: i32[][] }
function main(): i32 {
    let g: i32[][] = [id([1, 2]), id([3])];
    let a: Bag = Bag { n: 1, grid: g };
    let b: Bag = Bag { n: 2, grid: a.grid };
    let bs: Bag[] = [a, b, Bag { n: 3, grid: [id([4, 5, 6])] }];
    let t: i32 = 0;
    for x in bs { t = t + x.grid.len() + x.grid[0][0]; }
    a = Bag { n: 9, grid: [[0]] };
    let junk: i32[] = id([7, 7]);
    return t + g[1][0] + b.grid[0][1] + a.n + junk.len();
}
`, 27},
	{"nested_struct", `@noinline function id(xs: i32[]): i32[] { return xs; }
struct Bag { n: i32, grid: i32[][] }
struct Box2 { b: Bag, m: i32 }
function main(): i32 {
    let o: Box2 = Box2 { b: Bag { n: 1, grid: [id([1, 2]), id([3, 4])] }, m: 2 };
    return o.b.grid[1][1] + o.m;
}
`, 6},
	{"tuple_holds_field", `@noinline function id(xs: i32[]): i32[] { return xs; }
struct Bag { n: i32, grid: i32[][] }
function main(): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 3) {
        let r: Bag = Bag { n: i, grid: [[i, 1], [2, 3]] };
        let p: (i32, i32[][]) = (r.n, r.grid);
        r = Bag { n: 7, grid: [[5]] };
        let junk: i32[][] = [id([8, 8]), id([8, 8])];
        acc = acc + p.1[0][0] + p.1[1][1] + p.0 + r.grid[0][0] + junk.len();
        i = i + 1;
    }
    return acc;
}
`, 36},
	{"tuple_holds_strarr_field", `@noinline function ids(s: string): string { return s; }
struct Grid { n: i32, rows: string[][] }
function main(): i32 {
    let r: Grid = Grid { n: 2, rows: [[ids("ab"), "c"], [ids("def")]] };
    let p: (i32, string[][]) = (r.n, r.rows);
    r = Grid { n: 1, rows: [[ids("x")]] };
    return p.1[1][0].len() + p.0 + r.rows.len();
}
`, 6},
	{"tuple_holds_local", `@noinline function id(xs: i32[]): i32[] { return xs; }
function main(): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 3) {
        let g: i32[][] = [[i, 1], [2, 3]];
        let t: (i32, i32[][]) = (3, g);
        let junk: i32[][] = [id([8, 8]), id([8, 8])];
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
