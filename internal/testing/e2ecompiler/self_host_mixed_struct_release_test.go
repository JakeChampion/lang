package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// A caller releases a struct a producer returns when every return hands back
// a box the producer built (#10415). The reads after a rebind follow fresh
// allocations, so a box released early shows as a wrong answer.
var mixedStructReleaseCases = []struct {
	name string
	src  string
	want int
	// balanced: the census closes. A row that is not balanced still has to
	// run clean under the sanitizer apart from leaks.
	balanced bool
}{
	{"loop_built_field", `struct Ints { n: i32, ys: i32[] }
function build(k: i32): Ints {
    let g: i32[] = [];
    let i: i32 = 0;
    while (i < k) { g = g.append(i); i = i + 1; }
    return Ints { n: k, ys: g };
}
function main(): i32 { let r: Ints = build(3); return r.ys[1] + r.n; }
`, 4, true},
	{"rebind_in_loop", `struct Ints { n: i32, ys: i32[] }
function build(k: i32): Ints {
    let g: i32[] = [];
    let i: i32 = 0;
    while (i < k) { g = g.append(i); i = i + 1; }
    return Ints { n: k, ys: g };
}
function main(): i32 {
    let acc: i32 = 0;
    let r: Ints = build(3);
    let j: i32 = 0;
    while (j < 5) {
        let t: Ints = build(j + 1);
        r = build(j + 2);
        let junk: i32[] = [9, 9, 9];
        acc = acc + r.ys[1] + r.n + t.ys[0] + t.n + junk.len();
        j = j + 1;
    }
    return acc + r.n;
}
`, 61, true},
	{"merged_returns", `struct Ints { n: i32, ys: i32[] }
function build(k: i32): Ints {
    let s: Ints = Ints { n: k, ys: [k, 1] };
    if (k > 2) {
        let g: i32[] = [];
        let i: i32 = 0;
        while (i < k) { g = g.append(i * 2); i = i + 1; }
        s = Ints { n: k + s.n, ys: g };
    }
    return s;
}
function main(): i32 {
    let acc: i32 = 0;
    let j: i32 = 0;
    while (j < 5) {
        let r: Ints = build(j);
        let junk: i32[] = [9, 9, 9];
        acc = acc + r.ys[1] + r.n + junk.len();
        j = j + 1;
    }
    return acc;
}
`, 39, true},
	// Results that are not a box the producer built alone: a parameter handed
	// back, a choice between two, and a record also stored elsewhere. The
	// caller must not free a field the other owner still reads.
	{"shared_results", `struct Ints { n: i32, ys: i32[] }
function pass(p: Ints): Ints { return p; }
function pick(a: Ints, b: Ints, c: boolean): Ints { if (c) { return a; } return b; }
function stash(k: i32, keep: Ints[]): Ints {
    let s: Ints = Ints { n: k, ys: [k, k] };
    let more: Ints[] = keep.append(s);
    return s;
}
function main(): i32 {
    let base: Ints = Ints { n: 4, ys: [0, 1, 2, 3] };
    let acc: i32 = 0;
    let j: i32 = 0;
    while (j < 3) {
        let q: Ints = pass(base);
        let w: Ints = pick(base, q, j == 1);
        let z: Ints = stash(j, [base]);
        let junk: i32[] = [9, 9, 9];
        acc = acc + q.ys[2] + w.n + z.ys[1] + junk.len();
        j = j + 1;
    }
    return acc + base.ys[3];
}
`, 33, false},
	// A method's verdict must not reach the free function of the same name:
	// here the method builds its result and the free function hands back its
	// argument.
	{"method_name_collision", `struct Ints { n: i32, ys: i32[] }
function mk(p: Ints): Ints { return p; }
function (s: Ints) mk(k: i32): Ints { return Ints { n: k, ys: [k, s.n] }; }
function main(): i32 {
    let base: Ints = Ints { n: 2, ys: [5, 6, 7] };
    let acc: i32 = 0;
    let j: i32 = 0;
    while (j < 3) {
        let r: Ints = mk(base);
        let m: Ints = base.mk(j);
        let junk: i32[] = [9, 9, 9, 9];
        acc = acc + r.ys[2] + m.ys[0] + junk.len();
        j = j + 1;
    }
    return acc + base.ys[1];
}
`, 42, false},
	// A method's results (#10515): the array result's release, the record
	// results' counted class, and a discarded call's field walk.
	{"method_array_result", `struct Ints { n: i32, ys: i32[] }
function (s: Ints) arr(k: i32): i32[] {
    let g: i32[] = s.ys;
    if (k > 1) { g = [k, k, k]; }
    return g;
}
function main(): i32 {
    let base: Ints = Ints { n: 1, ys: [4, 5] };
    let acc: i32 = 0;
    let j: i32 = 0;
    while (j < 4) {
        acc = acc + base.arr(j)[1];
        base.arr(j + 1);
        let junk: i32[] = [9, 9, 9];
        acc = acc + junk.len();
        j = j + 1;
    }
    return acc + base.ys[1];
}
`, 32, true},
	{"method_record_results", `struct Ints { n: i32, ys: i32[] }
function (s: Ints) spread(k: i32): Ints {
    let t: Ints = Ints { n: k, ys: [k, s.n] };
    t = Ints { ...t, n: k + 1 };
    return t;
}
function (s: Ints) looped(k: i32): Ints {
    let t: Ints = Ints { n: 0, ys: [s.n] };
    let i: i32 = 0;
    while (i < k) { t = Ints { n: t.n + i, ys: [i, t.n] }; i = i + 1; }
    return t;
}
function main(): i32 {
    let base: Ints = Ints { n: 1, ys: [4, 5] };
    let acc: i32 = 0;
    let j: i32 = 0;
    while (j < 4) {
        let sp: Ints = base.spread(j);
        let lp: Ints = base.looped(j + 1);
        acc = acc + base.spread(j).n + base.looped(j).ys[0];
        base.spread(j);
        let junk: i32[] = [9, 9, 9];
        acc = acc + sp.ys[1] + lp.ys[1] + junk.len();
        j = j + 1;
    }
    return acc + base.ys[1];
}
`, 39, true},
	// A free producer's built record is in the counted class too, which is
	// what releases a read-through array field.
	{"free_record_read_through", `struct Ints { n: i32, ys: i32[] }
function looped(s: Ints, k: i32): Ints {
    let t: Ints = Ints { n: 0, ys: [s.n] };
    let i: i32 = 0;
    while (i < k) { t = Ints { n: t.n + i, ys: [i, t.n] }; i = i + 1; }
    return t;
}
function main(): i32 {
    let base: Ints = Ints { n: 1, ys: [4, 5] };
    let acc: i32 = 0;
    let j: i32 = 0;
    while (j < 4) {
        acc = acc + looped(base, j).ys[0];
        let junk: i32[] = [9, 9, 9];
        acc = acc + junk.len();
        j = j + 1;
    }
    return acc + base.ys[1];
}
`, 21, true},
}

func writeMixedStructReleaseSrc(t *testing.T, name, src string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name+".fern")
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestSelfHostMixedStructReleaseX86_64(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, tc := range mixedStructReleaseCases {
		src := writeMixedStructReleaseSrc(t, tc.name, tc.src)
		balanced := tc.balanced
		t.Run(tc.name, func(t *testing.T) {
			stderr, exit := runWithStdin(t, cli.runner, cli.x86Binary(t, src, "FERN_LEAKCHECK=1"), nil)
			if exit != tc.want {
				t.Fatalf("leakcheck: exit = %d, want %d\n%s", exit, tc.want, stderr)
			}
			if balanced {
				assertBalancedCensus(t, stderr)
			}
			stderr, exit = runWithStdin(t, cli.runner, cli.x86Binary(t, src, "FERN_SANITIZE=1"), nil)
			if exit != tc.want || forArrStructSanitizerFault(stderr, balanced) {
				t.Fatalf("sanitize: exit = %d, want %d, and no sanitizer report\n%s", exit, tc.want, stderr)
			}
		})
	}
}

func TestSelfHostMixedStructReleaseArm64(t *testing.T) {
	armgcc, qemu := arm64Tooling(t)
	cli := buildSelfHostCLI(t)
	for _, tc := range mixedStructReleaseCases {
		src := writeMixedStructReleaseSrc(t, tc.name, tc.src)
		balanced := tc.balanced
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
			if balanced {
				assertBalancedCensus(t, eb.String())
			}
		})
	}
}

func TestSelfHostMixedStructReleaseWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH")
	}
	cli := buildSelfHostCLI(t)
	for _, tc := range mixedStructReleaseCases {
		src := writeMixedStructReleaseSrc(t, tc.name, tc.src)
		balanced := tc.balanced
		t.Run(tc.name, func(t *testing.T) {
			stderr, exit := runWasmCensus(t, cli.emit(t, src, "wasm32-wasi", "FERN_LEAKCHECK=1"))
			if exit != tc.want {
				t.Fatalf("exit = %d, want %d\n%s", exit, tc.want, stderr)
			}
			if balanced {
				assertBalancedCensus(t, stderr)
			}
		})
	}
}
