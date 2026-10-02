package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// A tuple literal holding a record's array field (`(r.n, r.xs)`) stored the
// field read uncounted. For a string[] field the record lost its deep drop and
// the buffer leaked (#10203); for an i32[] field the record's release freed a
// buffer the tuple still read (#10314). The literal now retains the field, the
// tuple local's kinds and a returned tuple's ARRF: flags release it, and the
// field keeps its element walk.
const tupleFieldShareDecls = `import "std/i32";
struct Rec { n: i32, xs: string[] }
struct Ints { n: i32, ys: i32[] }
`

var tupleFieldShareCases = []struct {
	name string
	src  string
	want int
}{
	{"returned_strarr", `function pair(r: Rec): (i32, string[]) { return (r.n, r.xs); }
function main(): i32 {
    var r: Rec = Rec { n: 2, xs: ["ab", "cd"] };
    var p: (i32, string[]) = pair(r);
    return p.0 + p.1.len();
}
`, 4},
	{"returned_ints_outlives_record", `function pair(r: Ints): (i32, i32[]) { return (r.n, r.ys); }
function main(): i32 {
    var r: Ints = Ints { n: 2, ys: [5, 6, 7] };
    var p: (i32, i32[]) = pair(r);
    r = Ints { n: 1, ys: [1, 1] };
    var acc: i32 = 0;
    var k: i32 = 0;
    while (k < 8) { var junk: i32[] = [9, 9, 9]; acc = acc + junk[k % 3]; k = k + 1; }
    return p.1[0] + p.1.len() + r.n + acc - 72;
}
`, 9},
	{"local_ints_outlives_record", `function main(): i32 {
    var r: Ints = Ints { n: 2, ys: [5, 6, 7] };
    var p: (i32, i32[]) = (r.n, r.ys);
    r = Ints { n: 1, ys: [1, 1] };
    var acc: i32 = 0;
    var k: i32 = 0;
    while (k < 8) { var junk: i32[] = [9, 9, 9]; acc = acc + junk[k % 3]; k = k + 1; }
    return p.1[0] + p.1.len() + r.n + acc - 72;
}
`, 9},
	{"local_strarr_outlives_record", `function main(): i32 {
    var r: Rec = Rec { n: 2, xs: ["e", "f", "g"] };
    var p: (i32, string[]) = (r.n, r.xs);
    r = Rec { n: 1, xs: ["a", "a"] };
    var acc: i32 = 0;
    var k: i32 = 0;
    while (k < 8) { var junk: string[] = ["z", "z", "z"]; acc = acc + junk[k % 3].len(); k = k + 1; }
    return p.1[0].len() * 5 + p.1.len() + r.n + acc - 8;
}
`, 9},
	{"discarded", `function pair(r: Rec): (i32, string[]) { return (r.n, r.xs); }
function main(): i32 {
    var r: Rec = Rec { n: 2, xs: ["ab", "cd"] };
    pair(r);
    pair(r);
    return r.xs.len();
}
`, 2},
	{"literal_or_field", `function pick(r: Ints, k: i32): (i32, i32[]) {
    if (k > 1) { return (k, [k, k, k]); }
    return (r.n, r.ys);
}
function main(): i32 {
    var r: Ints = Ints { n: 2, ys: [5, 6, 7] };
    var a: (i32, i32[]) = pick(r, 1);
    var b: (i32, i32[]) = pick(r, 2);
    pick(r, 1);
    r = Ints { n: 1, ys: [1] };
    var junk: i32[] = [9, 9, 9];
    return a.1[0] + b.1.len() + r.n + junk[0] - 9;
}
`, 9},
	{"forwarded", `function pair(r: Rec): (i32, string[]) { return (r.n, r.xs); }
function first(r: Rec): string[] {
    var p: (i32, string[]) = pair(r);
    return p.1;
}
function main(): i32 {
    var r: Rec = Rec { n: 2, xs: ["ab", "cd"] };
    var ys: string[] = first(r);
    r = Rec { n: 1, xs: ["e"] };
    var junk: string[] = ["zzz", "zzz"];
    return ys[0].len() + ys.len() + r.n + junk.len();
}
`, 7},
	{"rebind_ident_or_field", `function main(): i32 {
    var r: Rec = Rec { n: 2, xs: ["ab", "cd"] };
    var ys: string[] = ["p", "q", "r"];
    var p: (i32, string[]) = (0, ys);
    var i: i32 = 0;
    while (i < 3) {
        if (i == 1) { p = (i, ys); } else { p = (r.n + i, r.xs); }
        i = i + 1;
    }
    r = Rec { n: 1, xs: ["e"] };
    var junk: string[] = ["zzz", "zzz"];
    return p.0 + p.1.len() + r.n + ys.len() + junk.len() + p.1[0].len();
}
`, 14},
	// A callee-local record whose array field the returned tuple holds is swept
	// at the return: the tuple's retain keeps the buffer for the caller (#10315).
	{"callee_local_ints", `function mk(k: i32): (i32, i32[]) {
    var r: Ints = Ints { n: k, ys: [k, 1, 2] };
    return (r.n, r.ys);
}
function main(): i32 {
    var t: (i32, i32[]) = mk(3);
    mk(4);
    var acc: i32 = 0;
    var k: i32 = 0;
    while (k < 8) { var junk: i32[] = [9, 9, 9]; acc = acc + junk[k % 3]; k = k + 1; }
    return t.0 + t.1[0] + t.1.len() + acc - 72;
}
`, 9},
	{"callee_local_strarr", `function mk(k: i32): (i32, string[]) {
    var r: Rec = Rec { n: k, xs: ["ab", "cd" + k.to_string()] };
    return (r.n, r.xs);
}
function main(): i32 {
    var t: (i32, string[]) = mk(3);
    mk(4);
    var acc: i32 = 0;
    var k: i32 = 0;
    while (k < 8) { var junk: string[] = ["zzz", "zzz" + k.to_string()]; acc = acc + junk[1].len(); k = k + 1; }
    return t.0 + t.1[1].len() + t.1.len() + acc - 32;
}
`, 8},
	// The same shape with an array-of-structs and an array-of-enums field: the
	// record's gated walk declines while the tuple holds the buffer, so the
	// tuple's 'b' release walks the elements (#10326).
	{"callee_local_structarr", `struct Pt { x: i32, tag: i32[] }
struct Bag { n: i32, pts: Pt[] }
function mk(k: i32): (i32, Pt[]) {
    var r: Bag = Bag { n: k, pts: [Pt { x: k, tag: [k, k] }, Pt { x: 2, tag: [2] }] };
    return (r.n, r.pts);
}
function main(): i32 {
    var t: (i32, Pt[]) = mk(3);
    mk(4);
    var acc: i32 = 0;
    var k: i32 = 0;
    while (k < 8) { var junk: Pt = Pt { x: 9, tag: [9, 9] }; acc = acc + junk.tag[0]; k = k + 1; }
    return t.0 + t.1[0].x + t.1[0].tag[1] + t.1.len() + acc - 72;
}
`, 11},
	{"callee_local_enumarr", `enum Flag { On(i32[]), Off }
struct Flags { n: i32, fs: Flag[] }
function mk(k: i32): (i32, Flag[]) {
    var r: Flags = Flags { n: k, fs: [Flag.On([k, 5]), Flag.Off] };
    return (r.n, r.fs);
}
function main(): i32 {
    var t: (i32, Flag[]) = mk(3);
    mk(4);
    var acc: i32 = 0;
    var k: i32 = 0;
    while (k < 8) { var junk: Flag = Flag.On([9, 9]); match (junk) { Flag.On(z) => { acc = acc + z[0]; }, Flag.Off => {} } k = k + 1; }
    var got: i32 = 0;
    match (t.1[0]) { Flag.On(q) => { got = q[1]; }, Flag.Off => { got = 0; } }
    return t.0 + got + t.1.len() + acc - 72;
}
`, 10},
	{"local_structarr_outlives_record", `struct Pt { x: i32, tag: i32[] }
struct Bag { n: i32, pts: Pt[] }
function main(): i32 {
    var r: Bag = Bag { n: 2, pts: [Pt { x: 4, tag: [3, 3] }, Pt { x: 2, tag: [2] }] };
    var p: (i32, Pt[]) = (r.n, r.pts);
    r = Bag { n: 1, pts: [Pt { x: 1, tag: [1] }] };
    var acc: i32 = 0;
    var k: i32 = 0;
    while (k < 8) { var junk: Pt = Pt { x: 9, tag: [9, 9] }; acc = acc + junk.tag[0]; k = k + 1; }
    return p.0 + p.1[0].x + p.1[0].tag[1] + p.1.len() + r.n + acc - 72;
}
`, 12},
	{"local_enumarr_outlives_record", `enum Flag { On(i32[]), Off }
struct Flags { n: i32, fs: Flag[] }
function main(): i32 {
    var r: Flags = Flags { n: 2, fs: [Flag.On([4, 5]), Flag.Off] };
    var p: (i32, Flag[]) = (r.n, r.fs);
    r = Flags { n: 1, fs: [Flag.Off] };
    var acc: i32 = 0;
    var k: i32 = 0;
    while (k < 8) { var junk: Flag = Flag.On([9, 9]); match (junk) { Flag.On(z) => { acc = acc + z[0]; }, Flag.Off => {} } k = k + 1; }
    var got: i32 = 0;
    match (p.1[0]) { Flag.On(q) => { got = q[1]; }, Flag.Off => { got = 0; } }
    return p.0 + got + p.1.len() + r.n + acc - 72;
}
`, 10},
	// An element the caller still holds: the record's array literal counts the
	// borrowed parameter, so the tuple's element walk only decs it and the
	// caller reads it back intact.
	{"callee_local_structarr_caller_elem", `struct Pt { x: i32, tag: i32[] }
struct Bag { n: i32, pts: Pt[] }
function mk(p: Pt, k: i32): (i32, Pt[]) {
    var r: Bag = Bag { n: k, pts: [p, Pt { x: 2, tag: [2] }] };
    return (r.n, r.pts);
}
function main(): i32 {
    var p0: Pt = Pt { x: 1, tag: [7, 8] };
    mk(p0, 1);
    var t: (i32, Pt[]) = mk(p0, 2);
    var acc: i32 = 0;
    var k: i32 = 0;
    while (k < 8) { var junk: Pt = Pt { x: 9, tag: [9, 9] }; acc = acc + junk.tag[0]; k = k + 1; }
    return p0.tag[1] + t.0 + t.1[0].tag[0] + acc - 72;
}
`, 17},
	// An array field's length as a returned element, and a nested-array field
	// as one (#10318).
	{"callee_local_arrlen", `struct Fs { n: i32, ws: f64[] }
function mk(k: i32): (i32, i32, i32) {
    var r: Ints = Ints { n: k, ys: [k, 1] };
    var f: Fs = Fs { n: k, ws: [1.5, 2.5, 3.5] };
    return (r.n, r.ys.len(), f.ws.len());
}
function main(): i32 {
    var t: (i32, i32, i32) = mk(3);
    mk(4);
    return t.0 + t.1 + t.2;
}
`, 8},
	{"callee_local_nested", `struct Bag { n: i32, grid: i32[][] }
function mk(k: i32): (i32, i32[][]) {
    var r: Bag = Bag { n: k, grid: [[k, 1], [2, 3]] };
    return (r.n, r.grid);
}
function main(): i32 {
    var t: (i32, i32[][]) = mk(3);
    mk(4);
    return t.0 + t.1[0][0] + t.1[1][1] + t.1.len();
}
`, 11},
	{"callee_local_nested_strarr", `struct Grid { n: i32, rows: string[][] }
function mk(k: i32): (i32, string[][]) {
    var r: Grid = Grid { n: k, rows: [["ab", "c"], ["def"]] };
    return (r.n, r.rows);
}
function main(): i32 {
    var (n, rows) = mk(3);
    var last: string[] = rows[1];
    return n + rows[0][0].len() + last[0].len() + rows.len();
}
`, 10},
	// Extracting the element to a new owner: never a second free.
	{"refused_elem_extracted", `function main(): i32 {
    var r: Rec = Rec { n: 2, xs: ["ab", "cd"] };
    var p: (i32, string[]) = (r.n, r.xs);
    var u: string[] = p.1;
    r = Rec { n: 1, xs: ["e"] };
    var junk: string[] = ["zzz", "zzz"];
    return u[0].len() + u.len() + junk.len();
}
`, 6},
}

func writeTupleFieldShareSrc(t *testing.T, name, src string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name+".fern")
	if err := os.WriteFile(path, []byte(tupleFieldShareDecls+src), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestSelfHostTupleFieldShareX86_64(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, tc := range tupleFieldShareCases {
		src := writeTupleFieldShareSrc(t, tc.name, tc.src)
		t.Run(tc.name, func(t *testing.T) {
			stderr, exit := runWithStdin(t, cli.runner, cli.x86Binary(t, src, "FERN_LEAKCHECK=1"), nil)
			if exit != tc.want {
				t.Fatalf("leakcheck: exit = %d, want %d\n%s", exit, tc.want, stderr)
			}
			assertBalancedCensus(t, stderr)
			stderr, exit = runWithStdin(t, cli.runner, cli.x86Binary(t, src, "FERN_SANITIZE=1"), nil)
			if exit != tc.want || forArrStructSanitizerFault(stderr, true) {
				t.Fatalf("sanitize: exit = %d, want %d, and no sanitizer fault\n%s", exit, tc.want, stderr)
			}
		})
	}
}

// TestSelfHostTupleFieldShareNative holds the native compiler to the same
// rows: every one clean, with the answer the self-host legs expect, a check a
// miscompile in the self-host compiler cannot pass.
func TestSelfHostTupleFieldShareNative(t *testing.T) {
	_, runner := x86_64Tooling(t)
	cli := buildLangBinForInterp(t)
	for _, tc := range tupleFieldShareCases {
		t.Run(tc.name, func(t *testing.T) {
			src := writeTupleFieldShareSrc(t, tc.name, tc.src)
			bin := filepath.Join(t.TempDir(), tc.name+".nat")
			compile := exec.Command(cli, "-target", "x86-64-linux", "-o", bin, src)
			compile.Env = childEnv("FERN_LEAKCHECK=1")
			if out, err := compile.CombinedOutput(); err != nil {
				t.Fatalf("native compile: %v\n%s", err, out)
			}
			stderr, exit := runWithStdin(t, runner, bin, nil)
			if exit != tc.want {
				t.Fatalf("native: exit = %d, want %d\n%s", exit, tc.want, stderr)
			}
			assertBalancedCensus(t, stderr)
		})
	}
}

func TestSelfHostTupleFieldShareArm64(t *testing.T) {
	armgcc, qemu := arm64Tooling(t)
	cli := buildSelfHostCLI(t)
	for _, tc := range tupleFieldShareCases {
		src := writeTupleFieldShareSrc(t, tc.name, tc.src)
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

func TestSelfHostTupleFieldShareWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH")
	}
	cli := buildSelfHostCLI(t)
	for _, tc := range tupleFieldShareCases {
		src := writeTupleFieldShareSrc(t, tc.name, tc.src)
		t.Run(tc.name, func(t *testing.T) {
			stderr, exit := runWasmCensus(t, cli.emit(t, src, "wasm32-wasi", "FERN_LEAKCHECK=1"))
			if exit != tc.want {
				t.Fatalf("exit = %d, want %d\n%s", exit, tc.want, stderr)
			}
			assertBalancedCensus(t, stderr)
		})
	}
}
