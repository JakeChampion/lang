package e2ecompiler

import "testing"

// nativeAcceptedCases are programs native compiles and runs that the self-host
// checker or the typed lowering used to refuse (#10767, #10763). Each answers the interpreter's exit code and
// output on every target through the typed lowering.
var nativeAcceptedCases = []struct {
	name   string
	src    string
	want   int
	stdout string
}{
	// A struct's bitand / bitor / bitxor / shl / shr / rem methods, reached only through their operators.
	{"bitwise-and-shift-overloads", `struct F { b: i32 }
function (self: F) rem(o: F): F { return F { b: self.b % o.b }; }
function (self: F) bitand(o: F): F { return F { b: self.b & o.b }; }
function (self: F) bitor(o: F): F { return F { b: self.b | o.b }; }
function (self: F) bitxor(o: F): F { return F { b: self.b ^ o.b }; }
function (self: F) shl(o: F): F { return F { b: self.b << o.b }; }
function (self: F) shr(o: F): F { return F { b: self.b >> o.b }; }
function main(): i32 {
    let a: F = F { b: 12 };
    let b: F = F { b: 10 };
    let c: F = a & b;
    c = a | b;
    c = a ^ b;
    c = F { b: 3 } << F { b: 1 };
    c = F { b: 12 } >> F { b: 1 };
    c = F { b: 17 } % F { b: 5 };
    return c.b;
}
`, 2, ""},
	// A record-form variant matched by field name and rendered by a derived Display.
	{"derived-display-record-variant", `import "std/i32";
import "core/cmp";

@derive(cmp.Display)
enum Shape {
    Circle { r: i32 },
    Rect { w: i32, h: i32 },
    Unit
}

function area(s: Shape): i32 {
    match (s) {
        Circle { r } => { return 3 * r * r; },
        Rect { h, w } => { return w * h; },
        Unit => { return 0; },
    }
    return 0 - 1;
}

function main(): i32 {
    let rr: Shape = Rect(3, 4);
    let c: Shape = Circle(2);
    print(rr.to_string());
    print("a=" + area(rr).to_string());
    print("a=" + area(c).to_string());
    return 0;
}
`, 0, "Rect { w: 3, h: 4 }\na=12\na=12\n"},
	// Every payload of a positional variant and every field of a record one, as native renders them.
	{"derived-display-every-payload", `import "std/i32";
import "core/cmp";
@derive(cmp.Display)
enum Shape { Circle(i32), Rect(i32, i32), Named { w: i32, h: i32 }, Unit }
function main(): i32 {
    let s: string = Rect(2, 3).to_string() + "|" + Named(4, 5).to_string() + "|" + Circle(1).to_string() + "|" + Unit.to_string();
    print(s);
    if (s != "Rect(2, 3)|Named { w: 4, h: 5 }|Circle(1)|Unit") { return 1; }
    return 42;
}
`, 42, ""},
	// Owned arrays lent to view parameters, a generic one among them.
	{"owned-array-lent-to-views", `struct Sink { base: i32 }
function sum_u8(bs: [u8]): i32 {
    let t: i32 = 0;
    let i: i32 = 0;
    while (i < bs.len()) { t = t + (bs[i] as i32); i = i + 1; }
    return t;
}
function sum_i32(xs: [i32]): i32 {
    let t: i32 = 0;
    let i: i32 = 0;
    while (i < xs.len()) { t = t + xs[i]; i = i + 1; }
    return t;
}
function count[T](xs: [T]): i32 {
    return xs.len();
}
function relend(bs: [u8]): i32 {
    return sum_u8(bs);
}
function tail_sum(bs: [u8]): i32 {
    return sum_u8(bs[1:]);
}
function (s: Sink) take(xs: [i32]): i32 {
    return s.base + sum_i32(xs);
}
function main(): i32 {
    let bytes: u8[] = [1, 2, 3];
    let ints: i32[] = [10, 20, 30];
    if (sum_u8(bytes) != 6) { return 1; }
    if (sum_i32(ints) != 60) { return 2; }
    if (count(bytes) != 3) { return 3; }
    if (count(ints) != 3) { return 4; }
    if (sum_u8([4, 5]) != 9) { return 5; }
    if (sum_u8(bytes[:]) != 6) { return 6; }
    if (sum_u8(bytes[1:]) != 5) { return 7; }
    if (sum_u8(bytes[:2]) != 3) { return 8; }
    if (relend(bytes) != 6) { return 9; }
    if (tail_sum(bytes) != 5) { return 10; }
    let s = Sink { base: 5 };
    if (s.take(ints) != 65) { return 11; }
    if (sum_u8(bytes) + bytes.len() != 9) { return 12; }
    return 0;
}
`, 0, ""},
	// A function named None is called, not read as the builtin variant.
	{"function-shadows-builtin-none", `function None(v: i32): Option[i32] { return Some(v); }
function mk_nullary(v: i32): Option[i32] { return None(v); }
function main(): i32 {
    match (mk_nullary(5)) { Some(v) => { return v; }, None => { return 99; } }
}
`, 5, ""},
	// An associated function of `impl[T] Box[T]`, its T bound from the argument.
	{"generic-inherent-impl", `struct Box[T] { v: T }
impl[T] Box[T] {
	function of(v: T): Box[T] { return Box { v: v }; }
	function get(self: Self): T { return self.v; }
}
function main(): i32 { let b: Box[i32] = Box.of(42); return b.get(); }
`, 42, ""},
	// A `?` inside a match value and an f-string returns from the function around it.
	{"try-inside-match-values", `import "std/i32";
function pick(p: Result[i32, i32]): Option[i32] {
    return (match (p) { Ok(v) => Some(v + 1), Err(e) => Some(((Some(e))?) + ((Some(40))?)) });
}
function tagged(n: i32): Option[i32] {
    let s: string = f"n{((Some(n))?)}";
    return Some(s.len());
}
function stop(): Option[i32] {
    let o: Option[i32] = None;
    return (match (o) { Some(v) => Some(v), None => Some(((o)?) + 1) });
}
function main(): i32 {
    let a: i32 = 0;
    match (pick(Err(2))) { Some(v) => { a = v; }, None => { return 1; } }
    match (tagged(7)) { Some(v) => { a = a + v; }, None => { return 2; } }
    match (stop()) { Some(v) => { return 3; }, None => {} }
    return a;
}
`, 44, ""},
	// `?` on a success that carries `()` resumes with the unit value.
	{"try-unit-success", `function ok(): Result[(), IoError] { return Ok(()); }
function bad(): Result[(), IoError] { return Err(Interrupted); }
function chain(fail: boolean): Result[(), IoError] {
    ok()?;
    if (fail) { bad()?; }
    return Ok(());
}
function main(): i32 {
    let n: i32 = 0;
    match (chain(false)) { Ok(_) => { n = n + 1; }, Err(_) => { n = n + 10; } }
    match (chain(true)) { Ok(_) => { n = n + 100; }, Err(_) => { n = n + 2; } }
    return n;
}
`, 3, ""},
	// An empty literal ahead of the argument that binds its element type.
	{"empty-literal-before-binder", `import "std/i32";
function head2[T](xs: [T], ys: T[]): i32 { return xs.len() + ys.len(); }
function join2[T](a: T[], b: T[]): T[] {
    let out: T[] = a;
    for x in b { out = out.append(x); }
    return out;
}
function main(): i32 {
    if (head2([], [7, 8]) != 2) { return 1; }
    let n: i32 = 2;
    let names: string[] = join2([], ["x", "y" + n.to_string()]);
    if (names.len() != 2 || names[1] != "y2") { return 2; }
    return 42;
}
`, 42, ""},
	// A `use` into a generic callee binds at the type its arguments give T.
	{"use-generic-callee", `function each[T](items: T[], cb: (T) => i32): i32 { return cb(items[0]); }
function first_len(): i32 {
    let words: string[] = ["abc", "de"];
    use w <- each(words);
    return w.len();
}
function main(): i32 {
    let nums: i32[] = [10, 20, 30];
    use n <- each(nums);
    return n + first_len();
}
`, 13, ""},
	// A value block every path of which returns.
	{"value-block-without-tail", `function f(n: i32): i32 { let x: i32 = { if (n < 0) { return 1; } return 2; }; return x; }
function g(n: i32): i32 { let x: i32 = if (n < 0) { return 10; } else { return 20; }; return x; }
function h(n: i32): i32 { let x: i32 = match (n) { 0 => { return 100; }, _ => { n * 2 } }; return x; }
function main(): i32 {
    if (f(0 - 1) + f(5) + g(0 - 1) + g(5) + h(0) + h(3) != 139) { return 1; }
    return 42;
}
`, 42, ""},
}

func TestSelfHostNativeAcceptedShapesIRX86_64(t *testing.T) {
	cli := newStrictCLI(t)
	for _, tc := range nativeAcceptedCases {
		t.Run(tc.name, func(t *testing.T) {
			code, out := cli.runX86(t, cli.emit(t, "x86-64-linux", tc.src))
			if code != tc.want || (tc.stdout != "" && out != tc.stdout) {
				t.Errorf("exit %d, want %d; stdout %q, want %q", code, tc.want, out, tc.stdout)
			}
		})
	}
}

func TestSelfHostNativeAcceptedShapesIRArm64(t *testing.T) {
	gcc, qemu := arm64Tooling(t)
	cli := newStrictCLI(t)
	for _, tc := range nativeAcceptedCases {
		t.Run(tc.name, func(t *testing.T) {
			code, out := runArm64(t, gcc, qemu, cli.emit(t, "arm64-linux", tc.src))
			if code != tc.want || (tc.stdout != "" && out != tc.stdout) {
				t.Errorf("arm64: exit %d, want %d; stdout %q, want %q", code, tc.want, out, tc.stdout)
			}
		})
	}
}

func TestSelfHostNativeAcceptedShapesWasmIR(t *testing.T) {
	cli := newStrictCLI(t)
	for _, tc := range nativeAcceptedCases {
		t.Run(tc.name, func(t *testing.T) {
			code, out := runWasm(t, cli.emit(t, "wasm32-wasi", tc.src))
			if code != tc.want || (tc.stdout != "" && out != tc.stdout) {
				t.Errorf("wasm: exit %d, want %d; stdout %q, want %q", code, tc.want, out, tc.stdout)
			}
		})
	}
}
