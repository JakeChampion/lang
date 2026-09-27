package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// An array literal or a self-append that stores a value another owner
// releases — a borrowed struct or enum parameter, an indexed box — stores it
// counted, as the clone-append already did (stored_elem_is_borrow). Stored
// uncounted, the record's field drop freed the caller's box: the caller read it
// back wrong. The counted-parameter tiers credit the store, so a temporary
// argument is still released by the caller.
var arrlitBorrowedElemCases = []struct {
	name string
	src  string
	want int
	// allocs, frees for a lowering where main keeps a leak of its own (it
	// never releases an enum local or a struct array it passed on), compared
	// exactly; every other lowering balances.
	pinned map[string][2]int64
}{
	{"struct_param_in_field_array", `struct Pt { x: i32, tag: i32[] }
struct Bag { n: i32, pts: Pt[] }
function mk(p: Pt, k: i32): i32 {
    var r: Bag = Bag { n: k, pts: [p, Pt { x: 2, tag: [2] }] };
    return r.n;
}
function main(): i32 {
    var p0: Pt = Pt { x: 1, tag: [7, 8] };
    mk(p0, 1);
    var acc: i32 = 0;
    var k: i32 = 0;
    while (k < 8) { var junk: Pt = Pt { x: 9, tag: [9, 9] }; acc = acc + junk.tag[0]; k = k + 1; }
    return p0.tag[1] + acc - 72;
}
`, 8, nil},
	{"struct_param_self_append", `struct Pt { x: i32, tag: i32[] }
struct Bag { n: i32, pts: Pt[] }
function mk(p: Pt, k: i32): i32 {
    var xs: Pt[] = [Pt { x: 2, tag: [2] }];
    xs = xs.append(p);
    var r: Bag = Bag { n: k, pts: xs };
    return r.n;
}
function main(): i32 {
    var p0: Pt = Pt { x: 1, tag: [7, 8] };
    mk(p0, 1);
    var acc: i32 = 0;
    var k: i32 = 0;
    while (k < 8) { var junk: Pt = Pt { x: 9, tag: [9, 9] }; acc = acc + junk.tag[0]; k = k + 1; }
    return p0.tag[1] + acc - 72;
}
`, 8, nil},
	{"enum_param_in_field_array", `enum Flag { On(i32[]), Off }
struct Flags { n: i32, fs: Flag[] }
function mk(f: Flag, k: i32): i32 {
    var r: Flags = Flags { n: k, fs: [f, Flag.Off] };
    return r.n;
}
function main(): i32 {
    var f0: Flag = Flag.On([4, 6]);
    mk(f0, 1);
    var acc: i32 = 0;
    var k: i32 = 0;
    while (k < 8) { var junk: Flag = Flag.On([9, 9]); match (junk) { Flag.On(z) => { acc = acc + z[0]; }, Flag.Off => {} } k = k + 1; }
    var g: i32 = 0;
    match (f0) { Flag.On(q) => { g = q[1]; }, Flag.Off => {} }
    return g + acc - 72;
}
`, 6, map[string][2]int64{"ast": {21, 19}, "ast_main": {21, 19}, "ast_callees": {21, 19}}},
	{"enum_param_self_append", `enum Flag { On(i32[]), Off }
struct Flags { n: i32, fs: Flag[] }
function mk(f: Flag, k: i32): i32 {
    var xs: Flag[] = [Flag.Off];
    xs = xs.append(f);
    var r: Flags = Flags { n: k, fs: xs };
    return r.n;
}
function main(): i32 {
    var f0: Flag = Flag.On([4, 6]);
    mk(f0, 1);
    var acc: i32 = 0;
    var k: i32 = 0;
    while (k < 8) { var junk: Flag = Flag.On([9, 9]); match (junk) { Flag.On(z) => { acc = acc + z[0]; }, Flag.Off => {} } k = k + 1; }
    var g: i32 = 0;
    match (f0) { Flag.On(q) => { g = q[1]; }, Flag.Off => {} }
    return g + acc - 72;
}
`, 6, map[string][2]int64{"ast": {22, 19}, "ast_main": {22, 20}, "ast_callees": {22, 19}}},
	{"indexed_elem_in_field_array", `struct Pt { x: i32, tag: i32[] }
struct Bag { n: i32, pts: Pt[] }
function mk(ps: Pt[], k: i32): i32 {
    var r: Bag = Bag { n: k, pts: [ps[0], Pt { x: 2, tag: [2] }] };
    return r.n;
}
function main(): i32 {
    var ps: Pt[] = [Pt { x: 1, tag: [7, 8] }];
    mk(ps, 1);
    var acc: i32 = 0;
    var k: i32 = 0;
    while (k < 8) { var junk: Pt = Pt { x: 9, tag: [9, 9] }; acc = acc + junk.tag[0]; k = k + 1; }
    return ps[0].tag[1] + acc - 72;
}
`, 8, map[string][2]int64{"ast": {23, 21}, "ast_callees": {23, 21}}},
	// A temporary handed to a storing parameter: the counted store leaves it
	// rc 2, and the caller's post-call release nets it to the record's one.
	{"temp_args", `struct Pt { x: i32, tag: i32[] }
struct Bag { n: i32, pts: Pt[] }
enum Flag { On(i32[]), Off }
struct Flags { n: i32, fs: Flag[] }
function mk(p: Pt, k: i32): i32 {
    var r: Bag = Bag { n: k, pts: [p, Pt { x: 2, tag: [2] }] };
    return r.n + r.pts.len();
}
function mka(f: Flag, k: i32): i32 {
    var r: Flags = Flags { n: k, fs: [f, Flag.Off] };
    return r.n + r.fs.len();
}
function main(): i32 {
    var s: i32 = 0;
    var i: i32 = 0;
    while (i < 3) {
        s = s + mk(Pt { x: i, tag: [i, i] }, i);
        s = s + mka(Flag.On([i]), i);
        i = i + 1;
    }
    return s;
}
`, 18, nil},
}

var arrlitBorrowedElemLowerings = []struct{ name, env string }{
	{"semantic", "FERN_SEM_IR=1"},
	{"ast", "FERN_SEM_IR="},
	{"ast_main", "FERN_SEM_IR_SKIP=main"},
	{"ast_callees", "FERN_SEM_IR_SKIP=mk,mka"},
}

func writeArrlitBorrowedElemSrc(t *testing.T, name, src string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name+".fern")
	if err := os.WriteFile(path, []byte(`import "std/i32";`+"\n"+src), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestSelfHostArrlitBorrowedElemX86_64(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, tc := range arrlitBorrowedElemCases {
		src := writeArrlitBorrowedElemSrc(t, tc.name, tc.src)
		for _, lw := range arrlitBorrowedElemLowerings {
			t.Run(tc.name+"/"+lw.name, func(t *testing.T) {
				pin, pinned := tc.pinned[lw.name]
				stderr, exit := runWithStdin(t, cli.runner, cli.x86Binary(t, src, "FERN_LEAKCHECK=1", lw.env), nil)
				if exit != tc.want {
					t.Fatalf("leakcheck: exit = %d, want %d\n%s", exit, tc.want, stderr)
				}
				if pinned {
					assertCensusPinned(t, stderr, pin)
				} else {
					assertBalancedCensus(t, stderr)
				}
				stderr, exit = runWithStdin(t, cli.runner, cli.x86Binary(t, src, "FERN_SANITIZE=1", lw.env), nil)
				if exit != tc.want || forArrStructSanitizerFault(stderr, !pinned) {
					t.Fatalf("sanitize: exit = %d, want %d, and no sanitizer fault\n%s", exit, tc.want, stderr)
				}
			})
		}
	}
}

// TestSelfHostArrlitBorrowedElemNative holds the native compiler to the same
// answers, every row clean.
func TestSelfHostArrlitBorrowedElemNative(t *testing.T) {
	_, runner := x86_64Tooling(t)
	cli := buildLangBinForInterp(t)
	for _, tc := range arrlitBorrowedElemCases {
		t.Run(tc.name, func(t *testing.T) {
			src := writeArrlitBorrowedElemSrc(t, tc.name, tc.src)
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

// assertCensusPinned: a lowering where main keeps its own leak left exactly
// its pinned allocs and frees. Fewer frees is a lost release; more frees moves
// the pin.
func assertCensusPinned(t *testing.T, stderr string, want [2]int64) {
	t.Helper()
	summary := leakSummaryLine(stderr)
	var allocs, frees, live int64
	if _, err := fmtSscan(summary, &allocs, &frees, &live); err != nil {
		t.Fatalf("parse %q: %v", summary, err)
	}
	if got := [2]int64{allocs, frees}; got != want {
		t.Errorf("%s, pinned allocs=%d frees=%d", summary, want[0], want[1])
	}
}
