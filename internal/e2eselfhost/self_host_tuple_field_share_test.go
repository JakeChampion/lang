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
const tupleFieldShareDecls = `struct Rec { n: i32, xs: string[] }
struct Ints { n: i32, ys: i32[] }
`

var tupleFieldShareCases = []struct {
	name     string
	src      string
	want     int
	balanced bool
}{
	{"returned_strarr", `function pair(r: Rec): (i32, string[]) { return (r.n, r.xs); }
function main(): i32 {
    var r: Rec = Rec { n: 2, xs: ["ab", "cd"] };
    var p: (i32, string[]) = pair(r);
    return p.0 + p.1.len();
}
`, 4, true},
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
`, 9, true},
	{"local_ints_outlives_record", `function main(): i32 {
    var r: Ints = Ints { n: 2, ys: [5, 6, 7] };
    var p: (i32, i32[]) = (r.n, r.ys);
    r = Ints { n: 1, ys: [1, 1] };
    var acc: i32 = 0;
    var k: i32 = 0;
    while (k < 8) { var junk: i32[] = [9, 9, 9]; acc = acc + junk[k % 3]; k = k + 1; }
    return p.1[0] + p.1.len() + r.n + acc - 72;
}
`, 9, true},
	{"local_strarr_outlives_record", `function main(): i32 {
    var r: Rec = Rec { n: 2, xs: ["e", "f", "g"] };
    var p: (i32, string[]) = (r.n, r.xs);
    r = Rec { n: 1, xs: ["a", "a"] };
    var acc: i32 = 0;
    var k: i32 = 0;
    while (k < 8) { var junk: string[] = ["z", "z", "z"]; acc = acc + junk[k % 3].len(); k = k + 1; }
    return p.1[0].len() * 5 + p.1.len() + r.n + acc - 8;
}
`, 9, true},
	{"discarded", `function pair(r: Rec): (i32, string[]) { return (r.n, r.xs); }
function main(): i32 {
    var r: Rec = Rec { n: 2, xs: ["ab", "cd"] };
    pair(r);
    pair(r);
    return r.xs.len();
}
`, 2, true},
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
`, 9, true},
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
`, 7, true},
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
`, 14, true},
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
`, 6, false},
}

var tupleFieldShareLowerings = []struct{ name, env string }{
	{"semantic", "FERN_SEM_IR=1"},
	{"ast", "FERN_SEM_IR="},
	{"ast_main", "FERN_SEM_IR_SKIP=main"},
	{"ast_callees", "FERN_SEM_IR_SKIP=pair,pick,first"},
}

// tupleFieldShareBalanced: whether the census must balance for this row and
// lowering. The refused row leaks wherever main is AST-lowered.
func tupleFieldShareBalanced(balanced bool, lowering string) bool {
	return balanced || lowering == "semantic" || lowering == "ast_callees"
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
				if tupleFieldShareBalanced(tc.balanced, lw.name) {
					assertBalancedCensus(t, stderr)
				}
				stderr, exit = runWithStdin(t, cli.runner, cli.x86Binary(t, src, "FERN_SANITIZE=1", lw.env), nil)
				if exit != tc.want || forArrStructSanitizerFault(stderr, tupleFieldShareBalanced(tc.balanced, lw.name)) {
					t.Fatalf("sanitize: exit = %d, want %d, and no sanitizer fault\n%s", exit, tc.want, stderr)
				}
			})
		}
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
				if tupleFieldShareBalanced(tc.balanced, lw.name) {
					assertBalancedCensus(t, eb.String())
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
				if tupleFieldShareBalanced(tc.balanced, lw.name) {
					assertBalancedCensus(t, stderr)
				}
			})
		}
	}
}
