package e2eselfhost

import (
	"os/exec"
	"testing"
)

// A `T[][]` local that shares its buffer with another — an alias, a struct
// field bind, or a local reassigned from it — takes the rows-walking release,
// so the last owner frees the rows (#10416). Before, only
// a fresh unaliased local did, and an alias left every slot on the shallow
// buffer dec, which freed the outer buffer and stranded the rows. Reads that
// follow the first release run after fresh allocations, so a row freed early
// shows as a wrong answer as well as under the sanitizer. Constant rows and
// strings go through id or ids so they are built on the heap rather than placed
// as constants.
var arrArrAliasCases = []struct {
	name string
	src  string
	want int
	// refused: the group escapes, so only the absence of an over-release is
	// checked, not the census.
	refused bool
}{
	{"alias", `function id(xs: i32[]): i32[] { return xs; }
function main(): i32 {
    let g: i32[][] = [id([3, 1]), id([2, 3])];
    let h: i32[][] = g;
    return g.len() + h.len();
}
`, 4, false},
	{"alias_chain", `function id(xs: i32[]): i32[] { return xs; }
function main(): i32 {
    let g: i32[][] = [id([3, 1]), id([2, 3])];
    let h: i32[][] = g;
    let k: i32[][] = h;
    return g.len() + h.len() + k[1][1];
}
`, 7, false},
	{"field_bind", `function id(xs: i32[]): i32[] { return xs; }
struct Bag { n: i32, grid: i32[][] }
function main(): i32 {
    let r: Bag = Bag { n: 3, grid: [id([3, 1]), id([2, 3])] };
    let p = r.grid;
    r = Bag { n: 1, grid: [id([7])] };
    let junk: i32[][] = [id([8, 8]), id([8, 8])];
    return r.n + p.len() + p[1][0] + junk.len();
}
`, 7, false},
	{"field_bind_loop", `function id(xs: i32[]): i32[] { return xs; }
struct Bag { n: i32, grid: i32[][] }
function main(): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 4) {
        let r: Bag = Bag { n: i, grid: [[i, 1], [2, 3]] };
        let p: i32[][] = r.grid;
        r = Bag { n: 9, grid: [[5]] };
        let junk: i32[][] = [id([8, 8]), id([8, 8])];
        acc = acc + p[0][0] + p[1][1] + r.grid[0][0] + junk.len();
        i = i + 1;
    }
    return acc;
}
`, 46, false},
	// A string[][] field bind whose holder is rebound first (#10548).
	{"field_bind_strings", `function ids(s: string): string { return s; }
struct Names { n: i32, names: string[][] }
function main(): i32 {
    let i: i32 = 0;
    let n: i32 = 0;
    while (i < 4) {
        let r: Names = Names { n: i, names: [[ids("a") + "b", "c"], [ids("d") + ""]] };
        let p = r.names;
        r = Names { n: 0, names: [[ids("z") + ""]] };
        let junk: string[][] = [[ids("x") + "y"]];
        n = n + p.len() + p[0][0].len() + r.names.len() + junk.len();
        i = i + 1;
    }
    return n;
}
`, 24, false},
	// A literal rebound inside an `if` (#10497's remainder on the AST lowering).
	{"literal_rebound_in_if", `function ids(s: string): string { return s; }
function main(): i32 {
    let q: string[][] = [[ids("a") + "b", "c"]];
    if (q.len() == 1) {
        q = [[ids("x") + "y"]];
    }
    let junk: string[][] = [[ids("j") + "k"]];
    return q[0][0].len() + q.len() + junk.len();
}
`, 4, false},
	{"literal_rebound_in_if_ints", `function id(xs: i32[]): i32[] { return xs; }
function main(): i32 {
    let q: i32[][] = [id([1, 2]), id([3])];
    if (q.len() == 2) {
        q = [id([7])];
    }
    let junk: i32[][] = [id([8, 8]), id([8, 8])];
    return q[0][0] + q.len() + junk.len();
}
`, 10, false},
	// A field bind whose holder is typed from a call result (#10548's remainder).
	{"field_bind_call_holder", `function ids(s: string): string { return s; }
struct Names { n: i32, names: string[][] }
function mk(i: i32): Names {
    return Names { n: i, names: [[ids("a") + "b"], [ids("c") + ""]] };
}
function main(): i32 {
    let total: i32 = 0;
    let i: i32 = 0;
    while (i < 4) {
        let r = mk(i);
        let p = r.names;
        total = total + p.len() + p[1].len() + p[0][0].len();
        i = i + 1;
    }
    return total;
}
`, 20, false},
	{"field_bind_call_holder_ints", `struct Bag { n: i32, grid: i32[][] }
function mk(i: i32): Bag {
    return Bag { n: i, grid: [[i, 1], [2]] };
}
function main(): i32 {
    let total: i32 = 0;
    let i: i32 = 0;
    while (i < 4) {
        let r = mk(i);
        let p = r.grid;
        total = total + p.len() + p[1].len() + p[0][0];
        i = i + 1;
    }
    return total;
}
`, 18, false},
	// A break out of a for over a row of an aliased array of arrays (#10219).
	{"break_out_of_row_for", `@noinline
function through(deps: i32[][], takes: i32[]): i32[][] {
    let out: i32[][] = deps;
    let w: i32 = 0;
    while (w < deps.len()) {
        let chain: i32[] = [];
        for d in deps[w] {
            chain = chain.append(d);
            if (takes[d] >= 0) {
                for e in deps[d] { chain = chain.append(e); }
                break;
            }
        }
        out = out.with(w, chain);
        w = w + 1;
    }
    return out;
}
function main(): i32 {
    let deps: i32[][] = [];
    let takes: i32[] = [];
    let i: i32 = 0;
    while (i < 12) {
        let row: i32[] = [];
        let j: i32 = 0;
        while (j < i) { row = row.append(j); j = j + 1; }
        deps = deps.append(row);
        if (i % 3 == 0) { takes = takes.append(1); } else { takes = takes.append(0 - 1); }
        i = i + 1;
    }
    let r: i32[][] = through(deps, takes);
    let n: i32 = 0;
    for x in r { n = n + x.len(); }
    return n;
}
`, 11, false},
	{"alias_outlives_source", `function id(xs: i32[]): i32[] { return xs; }
function main(): i32 {
    let g: i32[][] = [id([3, 1]), id([2, 3])];
    let h: i32[][] = g;
    let n: i32 = g.len();
    let junk: i32[][] = [id([8, 8]), id([8, 8])];
    let more: i32[] = id([9, 9, 9]);
    return n + h[1][0] + h[0][1] + junk.len() + more.len();
}
`, 10, false},
	{"alias_takes_last_use", `function id(xs: i32[]): i32[] { return xs; }
function main(): i32 {
    let g: i32[][] = [id([3, 1]), id([2, 3])];
    let n: i32 = g.len();
    let h: i32[][] = g;
    let junk: i32[][] = [id([8, 8]), id([8, 8])];
    return n + h[1][0] + h[0][1] + junk.len();
}
`, 7, false},
	{"alias_returned", `function id(xs: i32[]): i32[] { return xs; }
function keep(k: i32): i32[][] {
    let g: i32[][] = [[k, 1], [2, 3]];
    let h: i32[][] = g;
    return h;
}
function main(): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 3) {
        let q: i32[][] = keep(i);
        let junk: i32[][] = [id([8, 8]), id([8, 8])];
        acc = acc + q[0][0] + q[1][1] + junk.len();
        i = i + 1;
    }
    return acc;
}
`, 18, false},
	{"alias_loop_local", `function id(xs: i32[]): i32[] { return xs; }
function main(): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 5) {
        let g: i32[][] = [[i, 1], [2, 3]];
        let h: i32[][] = g;
        let junk: i32[][] = [id([8, 8]), id([8, 8])];
        acc = acc + h[0][0] + g[1][1] + junk.len() + h.len();
        i = i + 1;
    }
    return acc;
}
`, 45, false},
	{"alias_rebound_in_loop", `function id(xs: i32[]): i32[] { return xs; }
function main(): i32 {
    let acc: i32 = 0;
    let h: i32[][] = [[0]];
    let i: i32 = 0;
    while (i < 5) {
        let g: i32[][] = [[i, 1], [2, 3]];
        h = g;
        let junk: i32[][] = [id([8, 8]), id([8, 8])];
        acc = acc + h[0][0] + g[1][1] + junk.len();
        i = i + 1;
    }
    return acc + h.len();
}
`, 37, false},
	{"alias_swap", `function id(xs: i32[]): i32[] { return xs; }
function main(): i32 {
    let acc: i32 = 0;
    let a: i32[][] = [id([1, 2]), id([3, 4])];
    let b: i32[][] = [id([5, 6])];
    let i: i32 = 0;
    while (i < 4) {
        let t: i32[][] = a;
        a = b;
        b = t;
        let junk: i32[][] = [id([8, 8]), id([8, 8])];
        acc = acc + a[0][0] + b[0][1] + junk.len();
        i = i + 1;
    }
    return acc + a.len() + b.len();
}
`, 39, false},
	{"alias_self_append", `function id(xs: i32[]): i32[] { return xs; }
function grow(k: i32): i32 {
    let g: i32[][] = [[k, 1], [2, 3]];
    let h: i32[][] = g;
    h = h.append([k + 7]);
    g = g.append([k + 9]);
    let junk: i32[][] = [id([8, 8]), id([8, 8])];
    return g.len() + h.len() + junk.len() + h[2][0] + g[2][0] + h[0][0];
}
function main(): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 3) {
        acc = acc + grow(i);
        i = i + 1;
    }
    return acc;
}
`, 81, false},
	{"alias_escapes", `function id(xs: i32[]): i32[] { return xs; }
function pick(k: i32): i32[][] {
    let g: i32[][] = [[k, 1], [2, 3]];
    let h: i32[][] = g;
    let n: i32 = g.len();
    return h.append([n]);
}
function main(): i32 {
    let r: i32[][] = pick(4);
    let junk: i32[][] = [id([8, 8]), id([8, 8])];
    return r.len() + r[2][0] + r[0][0] + junk.len();
}
`, 11, true},
}

func TestSelfHostArrArrAliasX86_64(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, tc := range arrArrAliasCases {
		src := writeNestedArrFieldDropSrc(t, tc.name, tc.src)
		t.Run(tc.name, func(t *testing.T) {
			stderr, exit := runWithStdin(t, cli.runner, cli.x86Binary(t, src, "FERN_LEAKCHECK=1"), nil)
			if exit != tc.want {
				t.Fatalf("leakcheck: exit = %d, want %d\n%s", exit, tc.want, stderr)
			}
			if !tc.refused {
				assertBalancedCensus(t, stderr)
			}
			stderr, exit = runWithStdin(t, cli.runner, cli.x86Binary(t, src, "FERN_SANITIZE=1"), nil)
			if exit != tc.want || forArrStructSanitizerFault(stderr, !tc.refused) {
				t.Fatalf("sanitize: exit = %d, want %d, and no sanitizer report\n%s", exit, tc.want, stderr)
			}
		})
	}
}

func TestSelfHostArrArrAliasWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH")
	}
	cli := buildSelfHostCLI(t)
	for _, tc := range arrArrAliasCases {
		src := writeNestedArrFieldDropSrc(t, tc.name, tc.src)
		t.Run(tc.name, func(t *testing.T) {
			stderr, exit := runWasmCensus(t, cli.emit(t, src, "wasm32-wasi", "FERN_LEAKCHECK=1"))
			if exit != tc.want {
				t.Fatalf("exit = %d, want %d\n%s", exit, tc.want, stderr)
			}
			if !tc.refused {
				assertBalancedCensus(t, stderr)
			}
		})
	}
}
