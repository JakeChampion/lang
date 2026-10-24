package e2ecompiler

import "testing"

var dispatchedBracketRows = []leakRow{
	{"function_value_array", `function grow(xs: i32[]): i32[] {
    return xs.append(7);
}
function apply(f: (i32[]) => i32[], xs: i32[]): i32[] {
    return f(xs);
}
function main(): i32 {
    let a: i32[] = [];
    a = a.append(1);
    a = a.append(2);
    let old: i32[] = a;
    let f: (i32[]) => i32[] = grow;
    let b: i32[] = apply(grow, a);
    let c: i32[] = f(a);
    return old[0] * 10 + old[1] + a.len() * 30 + b.len() + c.len() * 2;
}
`, true},
	{"closure_array", `function main(): i32 {
    let a: i32[] = [];
    a = a.append(1);
    a = a.append(2);
    let k: i32 = 5;
    let push: (i32[]) => i32[] = (v: i32[]): i32[] => v.append(k);
    let b: i32[] = push(a);
    return a.len() * 10 + b.len() + b[2];
}
`, true},
	{"function_value_record_field", `struct Bag { items: i32[], n: i32 }
function take(b: Bag): i32[] {
    return b.items.append(3);
}
function apply(f: (Bag) => i32[], b: Bag): i32[] {
    return f(b);
}
function main(): i32 {
    let xs: i32[] = [];
    xs = xs.append(1);
    xs = xs.append(2);
    let bag: Bag = Bag { items: xs, n: 0 };
    xs = [];
    let r: i32[] = apply(take, bag);
    let f: (Bag) => i32[] = take;
    let r2: i32[] = f(bag);
    return bag.items.len() * 10 + r.len() + r2.len() * 2;
}
`, true},
	{"dyn_array_argument", `trait Grower {
    function grow(self: Self, xs: i32[]): i32[];
}
struct Adder { v: i32 }
impl Grower for Adder {
    function grow(self: Self, xs: i32[]): i32[] { return xs.append(self.v); }
}
function main(): i32 {
    let g: dyn Grower = Adder { v: 7 };
    let a: i32[] = [];
    a = a.append(1);
    a = a.append(2);
    let old: i32[] = a;
    let b: i32[] = g.grow(a);
    return old.len() * 10 + a.len() + b[2];
}
`, true},
	{"dyn_receiver_field", `trait Holder {
    function add(self: Self, v: i32): i32[];
    function count(self: Self): i32;
}
struct Bag { items: i32[] }
impl Holder for Bag {
    function add(self: Self, v: i32): i32[] { return self.items.append(v); }
    function count(self: Self): i32 { return self.items.len(); }
}
function main(): i32 {
    let xs: i32[] = [];
    xs = xs.append(1);
    xs = xs.append(2);
    let h: dyn Holder = Bag { items: xs };
    xs = [];
    let r: i32[] = h.add(3);
    return h.count() * 10 + r.len();
}
`, true},
}

func TestSelfHostDispatchedBracketX86_64(t *testing.T) {
	runLeakRowsX86_64(t, dispatchedBracketRows)
}

func TestSelfHostDispatchedBracketArm64(t *testing.T) {
	runLeakRowsArm64(t, dispatchedBracketRows)
}

func TestSelfHostDispatchedBracketWasm(t *testing.T) {
	runLeakRowsWasm(t, dispatchedBracketRows)
}

const dispatchedInPlaceSrc = `function push(acc: i32[], x: i32): i32[] {
    return acc.append(x);
}
function fold(xs: i32[], init: i32[], f: (i32[], i32) => i32[]): i32[] {
    let acc: i32[] = init;
    for x in xs { acc = f(acc, x); }
    return acc;
}
function main(): i32 {
    let xs: i32[] = [];
    let i: i32 = 0;
    while (i < 4096) { xs = xs.append(i); i = i + 1; }
    let empty: i32[] = [];
    let before: i64 = __heap_alloc_count();
    let out: i32[] = fold(xs, empty, push);
    if (__heap_alloc_count() - before > 64) { return 1; }
    if (out.len() != 4096 || out[4095] != 4095) { return 2; }
    return 42;
}
`

func TestSelfHostDispatchedLastReaderInPlace(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
		t.Run(target, func(t *testing.T) {
			if stderr, code := cli.exitOf(t, dispatchedInPlaceSrc, target); code != 42 {
				t.Errorf("exited %d, want 42 (1 is a copy per element)\n%s", code, stderr)
			}
		})
	}
}
