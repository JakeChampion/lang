package e2eselfhost

import (
	"os"
	"path/filepath"
	"testing"
)

// Primitive values of every width as `dyn` method arguments and as boxed `dyn`
// receivers (#11122). The dispatch chain used to take every method of the
// called name, so a `dyn Adder` call to `add` also got arms for the operator
// `Add` impls of i64 / f64 / BigInt, and wasm rejected the i32 argument handed
// to `i64.add`. A boxed u8 named a type-id global wasm never defined.

const dynPrimArgSrc = `import "std/i32";
trait Adder {
    function add(self: Self, n: i32): i32;
}
impl Adder for i32 {
    function add(self: Self, n: i32): i32 { return self + n; }
}
function run(a: dyn Adder, n: i32): i32 { return a.add(n); }
function main(): i32 {
    let x: i32 = 10;
    print("r=" + run(x, 32).to_string());
    return 0;
}
`

const dynByteDisplaySrc = `import "core/cmp";
import "std/i32";
function main(): i32 {
    let s: string = "A";
    let b: u8 = s[0];
    let xs: dyn cmp.Display[] = [b, 7];
    print(xs[0].to_string() + "/" + xs[1].to_string());
    return 0;
}
`

// Every width as an argument, on a primitive and a struct receiver. `add`
// collides with the operator impls, as in dynPrimArgSrc.
const dynWidthArgsSrc = `import "std/i32";
import "std/i64";
import "std/u32";
import "std/u64";
import "std/float";
trait Take {
    function add(self: Self, n: i32): i32;
    function t_i64(self: Self, n: i64): i64;
    function t_u32(self: Self, n: u32): u32;
    function t_u64(self: Self, n: u64): u64;
    function t_u8(self: Self, n: u8): u8;
    function t_f32(self: Self, n: f32): f32;
    function t_f64(self: Self, n: f64): f64;
    function t_bool(self: Self, n: boolean): boolean;
    function t_char(self: Self, n: char): char;
    function t_str(self: Self, n: string): string;
}
struct P { k: i32 }
impl Take for i32 {
    function add(self: Self, n: i32): i32 { return self + n; }
    function t_i64(self: Self, n: i64): i64 { return n * 2; }
    function t_u32(self: Self, n: u32): u32 { return n + 1; }
    function t_u64(self: Self, n: u64): u64 { return n + 2; }
    function t_u8(self: Self, n: u8): u8 { return n + 1; }
    function t_f32(self: Self, n: f32): f32 { return n * 2.0; }
    function t_f64(self: Self, n: f64): f64 { return n + 0.25; }
    function t_bool(self: Self, n: boolean): boolean { return !n; }
    function t_char(self: Self, n: char): char { return n; }
    function t_str(self: Self, n: string): string { return n + "!"; }
}
impl Take for P {
    function add(self: Self, n: i32): i32 { return self.k * n; }
    function t_i64(self: Self, n: i64): i64 { return n * 3; }
    function t_u32(self: Self, n: u32): u32 { return n + 10; }
    function t_u64(self: Self, n: u64): u64 { return n + 20; }
    function t_u8(self: Self, n: u8): u8 { return n + 2; }
    function t_f32(self: Self, n: f32): f32 { return n * 4.0; }
    function t_f64(self: Self, n: f64): f64 { return n + 0.5; }
    function t_bool(self: Self, n: boolean): boolean { return n; }
    function t_char(self: Self, n: char): char { return n; }
    function t_str(self: Self, n: string): string { return n + "?"; }
}
function line(d: dyn Take): string {
    let big: i64 = 3000000000;
    let ub: u64 = 5000000000;
    let s: string = "A";
    let b: u8 = s[0];
    let c: char = 'z';
    let out: string = d.add(5).to_string();
    out = out + " " + d.t_i64(big).to_string();
    out = out + " " + d.t_u32(4000000000).to_string();
    out = out + " " + d.t_u64(ub).to_string();
    out = out + " " + (d.t_u8(b) as i32).to_string();
    let x: f32 = 1.5;
    out = out + " " + d.t_f32(x).to_string();
    out = out + " " + d.t_f64(2.0).to_string();
    if (d.t_bool(true)) { out = out + " T"; } else { out = out + " F"; }
    out = out + " " + (d.t_char(c) as i32).to_string();
    out = out + " " + d.t_str("s");
    return out;
}
function main(): i32 {
    let xs: dyn Take[] = [7, P { k: 3 }];
    for d in xs { print(line(d)); }
    return 0;
}
`

// Every primitive receiver boxed into a dyn of the program's own trait and of
// core/cmp's Display. Each box must dispatch to its own type's impl and read
// its value back at its own width.
const dynWidthBoxSrc = `import "core/cmp";
import "std/i32";
import "std/i64";
import "std/u32";
import "std/u64";
import "std/float";
trait Show { function show(self: Self, n: i32): string; }
impl Show for i32 { function show(self: Self, n: i32): string { return "i32:" + (self + n).to_string(); } }
impl Show for i64 { function show(self: Self, n: i32): string { return "i64:" + (self + (n as i64)).to_string(); } }
impl Show for u32 { function show(self: Self, n: i32): string { return "u32:" + (self + (n as u32)).to_string(); } }
impl Show for u64 { function show(self: Self, n: i32): string { return "u64:" + (self + (n as u64)).to_string(); } }
impl Show for u8 { function show(self: Self, n: i32): string { return "u8:" + ((self as i32) + n).to_string(); } }
impl Show for f32 { function show(self: Self, n: i32): string { return "f32:" + self.to_string(); } }
impl Show for f64 { function show(self: Self, n: i32): string { return "f64:" + self.to_string(); } }
impl Show for boolean { function show(self: Self, n: i32): string { if (self) { return "bool:T"; } return "bool:F"; } }
impl Show for char { function show(self: Self, n: i32): string { return "char:" + ((self as i32) + n).to_string(); } }
impl Show for string { function show(self: Self, n: i32): string { return "str:" + self; } }
function main(): i32 {
    let a: i32 = 0 - 4;
    let b: i64 = 6000000000;
    let c: u32 = 4000000000;
    let d: u64 = 9000000000;
    let s: string = "A";
    let e: u8 = s[0];
    let f: f32 = 0.1;
    let g: f64 = 2.5;
    let h: boolean = true;
    let k: char = 'a';
    let xs: dyn Show[] = [a, b, c, d, e, f, g, h, k, "hi"];
    let out: string = "";
    for x in xs { out = out + x.show(1) + ";"; }
    print(out);
    let ds: dyn cmp.Display[] = [a, b, c, d, e, f, g, h, "hi"];
    let o2: string = "";
    for x in ds { o2 = o2 + x.to_string() + ";"; }
    print(o2);
    return 0;
}
`

// The interpreter dispatches every boxed integer to the i32 impl (#10194), so
// dynWidthBoxSrc's expected output is written out rather than taken from it.
const dynWidthBoxWant = "i32:-3;i64:6000000001;u32:4000000001;u64:9000000001;u8:66;f32:0.1;f64:2.5;bool:T;char:98;str:hi;\n" +
	"-4;6000000000;4000000000;9000000000;65;0.1;2.5;true;hi;\n"

type dynWidthCase struct {
	name string
	src  string
	want func(t *testing.T) string
}

func dynWidthInterpWant(src string) func(t *testing.T) string {
	return func(t *testing.T) string {
		t.Helper()
		want, code := runInterp(t, src)
		if code != 0 || want == "" {
			t.Fatalf("interpreter: exit %d, stdout %q", code, want)
		}
		return want
	}
}

func dynWidthCases() []dynWidthCase {
	return []dynWidthCase{
		{"prim-arg", dynPrimArgSrc, dynWidthInterpWant(dynPrimArgSrc)},
		{"byte-display", dynByteDisplaySrc, dynWidthInterpWant(dynByteDisplaySrc)},
		{"width-args", dynWidthArgsSrc, dynWidthInterpWant(dynWidthArgsSrc)},
		{"width-box", dynWidthBoxSrc, func(*testing.T) string { return dynWidthBoxWant }},
	}
}

func TestSelfHostDynPrimWidthIRX86_64(t *testing.T) {
	cli := newStrictCLI(t)
	for _, c := range dynWidthCases() {
		t.Run(c.name, func(t *testing.T) {
			want := c.want(t)
			if code, out := cli.runX86(t, cli.emit(t, "x86-64-linux", c.src)); code != 0 || out != want {
				t.Fatalf("exit %d, stdout %q; want 0, %q", code, out, want)
			}
			bin := buildBin(t, cli.gcc, t.TempDir(), "census", cli.emit(t, "x86-64-linux", c.src, "FERN_LEAKCHECK=1"))
			stderr, exit := runWithStdin(t, cli.runner, bin, nil)
			if exit != 0 {
				t.Fatalf("census run: exit %d, want 0\n%s", exit, stderr)
			}
			assertBalancedCensus(t, stderr)
		})
	}
}

func TestSelfHostDynPrimWidthIRArm64(t *testing.T) {
	gcc, qemu := arm64Tooling(t)
	cli := newStrictCLI(t)
	for _, c := range dynWidthCases() {
		t.Run(c.name, func(t *testing.T) {
			want := c.want(t)
			if code, out := runArm64(t, gcc, qemu, cli.emit(t, "arm64-linux", c.src)); code != 0 || out != want {
				t.Fatalf("arm64: exit %d, stdout %q; want 0, %q", code, out, want)
			}
		})
	}
}

func TestSelfHostDynPrimWidthWasmIR(t *testing.T) {
	cli := newStrictCLI(t)
	for _, c := range dynWidthCases() {
		t.Run(c.name, func(t *testing.T) {
			want := c.want(t)
			if code, out := runWasm(t, cli.emit(t, "wasm32-wasi", c.src)); code != 0 || out != want {
				t.Fatalf("wasm: exit %d, stdout %q; want 0, %q", code, out, want)
			}
			census := filepath.Join(t.TempDir(), "census.wat")
			if err := os.WriteFile(census, []byte(cli.emit(t, "wasm32-wasi", c.src, "FERN_LEAKCHECK=1")), 0o644); err != nil {
				t.Fatal(err)
			}
			stderr, exit := runWasmCensus(t, census)
			if exit != 0 {
				t.Fatalf("census run: exit %d, want 0\n%s", exit, stderr)
			}
			assertBalancedCensus(t, stderr)
		})
	}
}
