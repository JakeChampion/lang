package e2ecompiler

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// checkedArrayInferCases instantiate a generic from an argument only the
// checker can type: a local bound to a builtin's array result, an element of
// it, a string's byte, or an array built from those. The monomorphisers read the
// checker's stamp only at a scalar or a string, so each call stayed generic and
// the typed lowering refused it ("call target has no semantic contract",
// #11685). Every program runs with the arguments `ab cde`, and its exit code is
// the interpreter's.
var checkedArrayInferCases = []struct {
	name string
	src  string
	want int
}{
	{"issue-11685", `enum Box[T] { Packed(T) }
function wrap[T](x: T): Box[T] { return Box.Packed(x); }
function main(): i32 {
    let argv = args();
    match (wrap(argv[0])) { Packed(s) => { assert(s == argv[0]); } }
    return argv.len();
}`, 3},
	// A `T[]` parameter given the inferred array, directly and through a
	// generic forwarder whose own `T` stays unresolved until it is cloned.
	{"array-param", `function last[T](xs: T[]): T { return xs[xs.len() - 1]; }
function relay[T](xs: T[]): T { return last(xs); }
function main(): i32 {
    let argv = args();
    let tail = last(argv);
    if (tail != "cde") { return 1; }
    return tail.len() * 10 + relay(argv).len();
}`, 33},
	{"nested-array", `enum Box[T] { Packed(T) }
function corner[T](grid: T[][]): Box[T] { return Box.Packed(grid[grid.len() - 1][grid[0].len() - 1]); }
function wrap[T](x: T): Box[T] { return Box.Packed(x); }
function main(): i32 {
    let argv = args();
    let grid = [argv, argv, argv];
    match (corner(grid)) { Packed(c) => { if (c != "cde") { return 1; } } }
    let s = argv[argv.len() - 1];
    let bytes = [[s[0], s[1]], [s[1], s[2]]];
    match (corner(bytes)) { Packed(b) => { if (b != b'e') { return 2; } } }
    match (wrap(grid)) { Packed(g) => { return g.len() * 10 + g[2][1].len(); } }
    return 3;
}`, 32},
	// The clone must read a u8 element: a byte read from the builder, through
	// an immutable alias, and an array of string bytes.
	{"byte-width", `function last[T](xs: T[]): T { return xs[xs.len() - 1]; }
function main(): i32 {
    let argv = args();
    let h = buf_new(8);
    buf_push(h, argv[argv.len() - 1]);
    let bytes = buf_take_bytes(h);
    buf_free(h);
    let alias = bytes;
    let b = last(alias);
    if (b != b'e') { return 1; }
    let s = argv[argv.len() - 2];
    let picked = [s[1], s[0]];
    let a = last(picked);
    if (a != b'a') { return 2; }
    return (b - a) as i32 + 40;
}`, 44},
	// A string's byte as the argument, and its borrowed byte view.
	{"string-byte-index", `function both[T](a: T, b: T): T[] { return [a, b]; }
function head[T](xs: [T]): T { return xs[0]; }
function main(): i32 {
    let argv = args();
    let s = argv[argv.len() - 1];
    let pair = both(s[0], s[2]);
    if (pair[1] != b'e') { return 1; }
    let h = head(s.as_bytes());
    if (h != b'c') { return 2; }
    return (pair[1] - pair[0]) as i32 + 20;
}`, 22},
	// The struct and enum passes key a literal and a constructor the same way.
	{"struct-and-enum-args", `struct Holder[T] { v: T }
enum Opt[T] { Has(T), Empty }
function main(): i32 {
    let argv = args();
    let h = Holder { v: argv };
    let s = argv[argv.len() - 1];
    let hb = Holder { v: s[2] };
    let o = Has(argv);
    let ob = Has(s[1]);
    let n: i32 = 0;
    match (o) { Has(a) => { n = n + a.len(); }, Empty => { n = 50; } }
    match (ob) { Has(b) => { if (b == b'd') { n = n + 10; } }, Empty => { n = 60; } }
    if (hb.v != b'e') { return 1; }
    return n + h.v.len() * 20;
}`, 73},
}

func TestSelfHostGenericCheckedArrayInfer(t *testing.T) {
	cli := buildSelfHostCLI(t)
	interp := buildLangBinForInterp(t)
	progArgs := []string{"ab", "cde"}
	for _, tc := range checkedArrayInferCases {
		src := filepath.Join(t.TempDir(), "main.fern")
		if err := os.WriteFile(src, []byte(tc.src), 0o644); err != nil {
			t.Fatal(err)
		}
		cmd := exec.Command(interp, append([]string{"-interp", src}, progArgs...)...)
		var eb bytes.Buffer
		cmd.Stderr = &eb
		_ = cmd.Run()
		if cmd.ProcessState == nil || cmd.ProcessState.ExitCode() != tc.want {
			t.Fatalf("%s: interpreter exited %v, want %d\n%s", tc.name, cmd.ProcessState, tc.want, eb.String())
		}
		for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
			t.Run(target+"/"+tc.name, func(t *testing.T) {
				if stderr, code := cli.exitOfFileArgs(t, src, target, nil, progArgs, "FERN_STRICT_IR=1"); code != tc.want {
					t.Errorf("exited %d, want the interpreter's %d\n%s", code, tc.want, stderr)
				}
			})
		}
	}
}
