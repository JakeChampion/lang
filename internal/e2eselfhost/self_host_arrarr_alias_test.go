package e2eselfhost

import (
	"os/exec"
	"testing"
)

// A `T[][]` local that shares its buffer with another — an alias, a struct
// field bind, or a local reassigned from it — takes the rows-walking release
// on the AST lowering, so the last owner frees the rows (#10416). Before, only
// a fresh unaliased local did, and an alias left every slot on the shallow
// buffer dec, which freed the outer buffer and stranded the rows. Reads that
// follow the first release run after fresh allocations, so a row freed early
// shows as a wrong answer as well as under the sanitizer.
var arrArrAliasCases = []struct {
	name string
	src  string
	want int
	// refused: the group escapes, so only the absence of an over-release is
	// checked, not the census.
	refused bool
}{
	{"alias", `function main(): i32 {
    var g: i32[][] = [[3, 1], [2, 3]];
    var h: i32[][] = g;
    return g.len() + h.len();
}
`, 4, false},
	{"alias_chain", `function main(): i32 {
    var g: i32[][] = [[3, 1], [2, 3]];
    var h: i32[][] = g;
    var k: i32[][] = h;
    return g.len() + h.len() + k[1][1];
}
`, 7, false},
	{"field_bind", `struct Bag { n: i32, grid: i32[][] }
function main(): i32 {
    var r: Bag = Bag { n: 3, grid: [[3, 1], [2, 3]] };
    var p = r.grid;
    r = Bag { n: 1, grid: [[7]] };
    var junk: i32[][] = [[8, 8], [8, 8]];
    return r.n + p.len() + p[1][0] + junk.len();
}
`, 7, false},
	{"field_bind_loop", `struct Bag { n: i32, grid: i32[][] }
function main(): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 4) {
        var r: Bag = Bag { n: i, grid: [[i, 1], [2, 3]] };
        var p: i32[][] = r.grid;
        r = Bag { n: 9, grid: [[5]] };
        var junk: i32[][] = [[8, 8], [8, 8]];
        acc = acc + p[0][0] + p[1][1] + r.grid[0][0] + junk.len();
        i = i + 1;
    }
    return acc;
}
`, 46, false},
	{"alias_outlives_source", `function main(): i32 {
    var g: i32[][] = [[3, 1], [2, 3]];
    var h: i32[][] = g;
    var n: i32 = g.len();
    var junk: i32[][] = [[8, 8], [8, 8]];
    var more: i32[] = [9, 9, 9];
    return n + h[1][0] + h[0][1] + junk.len() + more.len();
}
`, 10, false},
	{"alias_takes_last_use", `function main(): i32 {
    var g: i32[][] = [[3, 1], [2, 3]];
    var n: i32 = g.len();
    var h: i32[][] = g;
    var junk: i32[][] = [[8, 8], [8, 8]];
    return n + h[1][0] + h[0][1] + junk.len();
}
`, 7, false},
	{"alias_returned", `function keep(k: i32): i32[][] {
    var g: i32[][] = [[k, 1], [2, 3]];
    var h: i32[][] = g;
    return h;
}
function main(): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 3) {
        var q: i32[][] = keep(i);
        var junk: i32[][] = [[8, 8], [8, 8]];
        acc = acc + q[0][0] + q[1][1] + junk.len();
        i = i + 1;
    }
    return acc;
}
`, 18, false},
	{"alias_loop_local", `function main(): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 5) {
        var g: i32[][] = [[i, 1], [2, 3]];
        var h: i32[][] = g;
        var junk: i32[][] = [[8, 8], [8, 8]];
        acc = acc + h[0][0] + g[1][1] + junk.len() + h.len();
        i = i + 1;
    }
    return acc;
}
`, 45, false},
	{"alias_rebound_in_loop", `function main(): i32 {
    var acc: i32 = 0;
    var h: i32[][] = [[0]];
    var i: i32 = 0;
    while (i < 5) {
        var g: i32[][] = [[i, 1], [2, 3]];
        h = g;
        var junk: i32[][] = [[8, 8], [8, 8]];
        acc = acc + h[0][0] + g[1][1] + junk.len();
        i = i + 1;
    }
    return acc + h.len();
}
`, 37, false},
	{"alias_swap", `function main(): i32 {
    var acc: i32 = 0;
    var a: i32[][] = [[1, 2], [3, 4]];
    var b: i32[][] = [[5, 6]];
    var i: i32 = 0;
    while (i < 4) {
        var t: i32[][] = a;
        a = b;
        b = t;
        var junk: i32[][] = [[8, 8], [8, 8]];
        acc = acc + a[0][0] + b[0][1] + junk.len();
        i = i + 1;
    }
    return acc + a.len() + b.len();
}
`, 39, false},
	{"alias_self_append", `function grow(k: i32): i32 {
    var g: i32[][] = [[k, 1], [2, 3]];
    var h: i32[][] = g;
    h = h.append([k + 7]);
    g = g.append([k + 9]);
    var junk: i32[][] = [[8, 8], [8, 8]];
    return g.len() + h.len() + junk.len() + h[2][0] + g[2][0] + h[0][0];
}
function main(): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 3) {
        acc = acc + grow(i);
        i = i + 1;
    }
    return acc;
}
`, 81, false},
	{"alias_escapes", `function pick(k: i32): i32[][] {
    var g: i32[][] = [[k, 1], [2, 3]];
    var h: i32[][] = g;
    var n: i32 = g.len();
    return h.append([n]);
}
function main(): i32 {
    var r: i32[][] = pick(4);
    var junk: i32[][] = [[8, 8], [8, 8]];
    return r.len() + r[2][0] + r[0][0] + junk.len();
}
`, 11, true},
}

var arrArrAliasLowerings = []struct{ name, env string }{
	{"semantic", "FERN_SEM_IR=1"},
	{"ast", "FERN_SEM_IR="},
}

func TestSelfHostArrArrAliasX86_64(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, tc := range arrArrAliasCases {
		src := writeNestedArrFieldDropSrc(t, tc.name, tc.src)
		for _, lw := range arrArrAliasLowerings {
			t.Run(tc.name+"/"+lw.name, func(t *testing.T) {
				stderr, exit := runWithStdin(t, cli.runner, cli.x86Binary(t, src, "FERN_LEAKCHECK=1", lw.env), nil)
				if exit != tc.want {
					t.Fatalf("leakcheck: exit = %d, want %d\n%s", exit, tc.want, stderr)
				}
				if !tc.refused {
					assertBalancedCensus(t, stderr)
				}
				stderr, exit = runWithStdin(t, cli.runner, cli.x86Binary(t, src, "FERN_SANITIZE=1", lw.env), nil)
				if exit != tc.want || forArrStructSanitizerFault(stderr, !tc.refused) {
					t.Fatalf("sanitize: exit = %d, want %d, and no sanitizer report\n%s", exit, tc.want, stderr)
				}
			})
		}
	}
}

func TestSelfHostArrArrAliasWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH")
	}
	cli := buildSelfHostCLI(t)
	for _, tc := range arrArrAliasCases {
		src := writeNestedArrFieldDropSrc(t, tc.name, tc.src)
		for _, lw := range arrArrAliasLowerings {
			t.Run(tc.name+"/"+lw.name, func(t *testing.T) {
				stderr, exit := runWasmCensus(t, cli.emit(t, src, "wasm32-wasi", "FERN_LEAKCHECK=1", lw.env))
				if exit != tc.want {
					t.Fatalf("exit = %d, want %d\n%s", exit, tc.want, stderr)
				}
				if !tc.refused {
					assertBalancedCensus(t, stderr)
				}
			})
		}
	}
}
