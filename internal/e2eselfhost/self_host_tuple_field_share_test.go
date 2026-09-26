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
	name     string
	src      string
	want     int
	balanced bool
	// allocs, frees per lowering for a row that still leaks there, compared
	// exactly so a fix or a regression has to move the number.
	pinned map[string][2]int64
}{
	{"returned_strarr", `function pair(r: Rec): (i32, string[]) { return (r.n, r.xs); }
function main(): i32 {
    var r: Rec = Rec { n: 2, xs: ["ab", "cd"] };
    var p: (i32, string[]) = pair(r);
    return p.0 + p.1.len();
}
`, 4, true, nil},
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
`, 9, true, nil},
	{"local_ints_outlives_record", `function main(): i32 {
    var r: Ints = Ints { n: 2, ys: [5, 6, 7] };
    var p: (i32, i32[]) = (r.n, r.ys);
    r = Ints { n: 1, ys: [1, 1] };
    var acc: i32 = 0;
    var k: i32 = 0;
    while (k < 8) { var junk: i32[] = [9, 9, 9]; acc = acc + junk[k % 3]; k = k + 1; }
    return p.1[0] + p.1.len() + r.n + acc - 72;
}
`, 9, true, nil},
	{"local_strarr_outlives_record", `function main(): i32 {
    var r: Rec = Rec { n: 2, xs: ["e", "f", "g"] };
    var p: (i32, string[]) = (r.n, r.xs);
    r = Rec { n: 1, xs: ["a", "a"] };
    var acc: i32 = 0;
    var k: i32 = 0;
    while (k < 8) { var junk: string[] = ["z", "z", "z"]; acc = acc + junk[k % 3].len(); k = k + 1; }
    return p.1[0].len() * 5 + p.1.len() + r.n + acc - 8;
}
`, 9, true, nil},
	{"discarded", `function pair(r: Rec): (i32, string[]) { return (r.n, r.xs); }
function main(): i32 {
    var r: Rec = Rec { n: 2, xs: ["ab", "cd"] };
    pair(r);
    pair(r);
    return r.xs.len();
}
`, 2, true, nil},
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
`, 9, true, nil},
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
`, 7, true, nil},
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
`, 14, true, nil},
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
`, 9, true, nil},
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
`, 8, true, nil},
	// The same shape with an array-of-structs and an array-of-enums field: the
	// record is swept, and the tuple's release is the one buffer dec its 'a'
	// kind emits, so the elements the record's gated walk declined still leak
	// on the AST leg (less than main, which kept the whole record).
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
`, 11, false, map[string][2]int64{"ast": {30, 22}, "ast_callees": {30, 22}, "ast_main": {30, 20}}},
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
`, 10, false, map[string][2]int64{"ast": {28, 22}, "ast_callees": {28, 22}, "ast_main": {28, 20}}},
	// Extracting the element to a new owner refuses the tuple's element release,
	// so the AST leg keeps the tuple's reference: a leak, never a second free.
	{"refused_elem_extracted", `function main(): i32 {
    var r: Rec = Rec { n: 2, xs: ["ab", "cd"] };
    var p: (i32, string[]) = (r.n, r.xs);
    var u: string[] = p.1;
    r = Rec { n: 1, xs: ["e"] };
    var junk: string[] = ["zzz", "zzz"];
    return u[0].len() + u.len() + junk.len();
}
`, 6, false, nil},
}

var tupleFieldShareLowerings = []struct{ name, env string }{
	{"semantic", "FERN_SEM_IR=1"},
	{"ast", "FERN_SEM_IR="},
	{"ast_main", "FERN_SEM_IR_SKIP=main"},
	{"ast_callees", "FERN_SEM_IR_SKIP=pair,pick,first,mk"},
}

// tupleFieldShareBalanced: whether the census must balance for this row and
// lowering. The refused row leaks wherever main is AST-lowered; a pinned row
// wherever it has a pin.
func tupleFieldShareBalanced(balanced bool, pinned map[string][2]int64, lowering string) bool {
	if balanced || lowering == "semantic" {
		return true
	}
	if pinned != nil {
		_, ok := pinned[lowering]
		return !ok
	}
	return lowering == "ast_callees"
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
		for _, lw := range tupleFieldShareLowerings {
			t.Run(tc.name+"/"+lw.name, func(t *testing.T) {
				stderr, exit := runWithStdin(t, cli.runner, cli.x86Binary(t, src, "FERN_LEAKCHECK=1", lw.env), nil)
				if exit != tc.want {
					t.Fatalf("leakcheck: exit = %d, want %d\n%s", exit, tc.want, stderr)
				}
				if tupleFieldShareBalanced(tc.balanced, tc.pinned, lw.name) {
					assertBalancedCensus(t, stderr)
				} else if pin, ok := tc.pinned[lw.name]; ok {
					assertLeakPinned(t, stderr, pin)
				}
				stderr, exit = runWithStdin(t, cli.runner, cli.x86Binary(t, src, "FERN_SANITIZE=1", lw.env), nil)
				if exit != tc.want || forArrStructSanitizerFault(stderr, tupleFieldShareBalanced(tc.balanced, tc.pinned, lw.name)) {
					t.Fatalf("sanitize: exit = %d, want %d, and no sanitizer fault\n%s", exit, tc.want, stderr)
				}
			})
		}
	}
}

// TestSelfHostTupleFieldShareNative holds the native compiler to the same
// rows: every one clean, with the answer the self-host legs expect. The
// self-host legs are four paths through one compiler, so this is the check a
// miscompile they share cannot pass.
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
		for _, lw := range tupleFieldShareLowerings {
			t.Run(tc.name+"/"+lw.name, func(t *testing.T) {
				asm, err := os.ReadFile(cli.emit(t, src, "arm64-linux", "FERN_LEAKCHECK=1", lw.env))
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
				if tupleFieldShareBalanced(tc.balanced, tc.pinned, lw.name) {
					assertBalancedCensus(t, eb.String())
				} else if pin, ok := tc.pinned[lw.name]; ok {
					assertLeakPinned(t, eb.String(), pin)
				}
			})
		}
	}
}

func TestSelfHostTupleFieldShareWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH")
	}
	cli := buildSelfHostCLI(t)
	for _, tc := range tupleFieldShareCases {
		src := writeTupleFieldShareSrc(t, tc.name, tc.src)
		for _, lw := range tupleFieldShareLowerings {
			t.Run(tc.name+"/"+lw.name, func(t *testing.T) {
				stderr, exit := runWasmCensus(t, cli.emit(t, src, "wasm32-wasi", "FERN_LEAKCHECK=1", lw.env))
				if exit != tc.want {
					t.Fatalf("exit = %d, want %d\n%s", exit, tc.want, stderr)
				}
				if tupleFieldShareBalanced(tc.balanced, tc.pinned, lw.name) {
					assertBalancedCensus(t, stderr)
				} else if pin, ok := tc.pinned[lw.name]; ok {
					assertLeakPinned(t, stderr, pin)
				}
			})
		}
	}
}

// assertLeakPinned: a row that still leaks on this leg (#10326) left exactly
// its pinned allocs and frees. Fewer frees is a regression; more frees is a fix
// of the element leak, which moves the pin.
func assertLeakPinned(t *testing.T, stderr string, want [2]int64) {
	t.Helper()
	summary := leakSummaryLine(stderr)
	var allocs, frees, live int64
	if _, err := fmtSscan(summary, &allocs, &frees, &live); err != nil {
		t.Fatalf("parse %q: %v", summary, err)
	}
	if got := [2]int64{allocs, frees}; got != want {
		t.Errorf("%s, pinned allocs=%d frees=%d — fewer frees means the sweep lost a release; "+
			"more frees means the element leak (#10326) closed, so move the pin", summary, want[0], want[1])
	}
}
