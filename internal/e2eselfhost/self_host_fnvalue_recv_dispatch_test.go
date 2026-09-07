package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// A method called on the RESULT of a call through a fn value must dispatch on
// that value's declared return type.
//
// `keyed_sum[T, K: Key](xs, key: (T) => K)` instantiated at `K = string` calls
// `key(xs[i]).k_id()`. The receiver is a call through a PARAMETER holding a
// closure, whose type is coarsened to the flat "fn" tag with the result in a
// sidecar — so every scalar predicate in irlower answered false for it and
// `expr_scalar_type` handed back its "i32" last resort. The call then keyed
// `i32.k_id`, and because the program's OTHER `impl Key for i32` supplies a
// symbol of exactly that name, strict-IR was satisfied and the module lowered:
// `impl Key for i32 { k_id(self) { return self; } }` ran with a string BOX as
// its integer receiver and the program returned a heap address (#7187).
//
// That is the silent branch of the same failure
// `TestSelfHostArrayRecvMisdispatchRefuses` pins one receiver kind further out:
// no diagnostic, no refusal, only an oracle disagreement. The loud branch (no
// i32 impl of that verb) merely bailed the module.
//
// The first four cases are the issue's own table — the i32 forward, the string
// forward, a two-hop forward, and a lambda passed directly with no forwarding.
// The i32 rows were right all along; pinning them keeps a regression of the
// OTHER half visible, since a generic forwarding its closure parameter to
// another bounded generic used to fail to instantiate at all. The direct-lambda
// row is the issue's one mis-measurement: it records that shape as correct,
// but a string key through it is wrong too — the defect never needed the
// forwarding.
//
// The remaining cases sweep the rest of the scalar family through the same
// shape, since the "i32" fallback captured every one of them. Measured before
// the fix: `string` / `f64` / `boolean` / `u32` / `f32` all silently ran the
// i32 impl; `u64` bailed loudly on the absent `i64.k_id` (its 64-bit WIDTH was
// already tracked, only its unsigned-ness was not).
const fnValueRecvKeyTrait = `trait Key { function k_id(self: Self): i32; }
impl Key for i32 { function k_id(self: Self): i32 { return self; } }
impl Key for string { function k_id(self: Self): i32 { return self.len(); } }

function keyed_sum[T, K: Key](xs: T[], key: (T) => K): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < xs.len()) { acc = acc + key(xs[i]).k_id(); i = i + 1; }
    return acc;
}

function fwd_i32[T](xs: T[], key: (T) => i32): i32 { return keyed_sum(xs, key); }
function fwd_str[T](xs: T[], key: (T) => string): i32 { return keyed_sum(xs, key); }
function hop[T](xs: T[], key: (T) => i32): i32 { return fwd_i32(xs, key); }

struct Row { n: i32, name: string }

`

// The scalar sweep uses a distinct constant per impl, so the exit code names
// which impl ran rather than being a value the arithmetic could reach by
// accident.
const fnValueRecvScalarTrait = `import "std/i32";
import "std/float";

trait Tag { function tag(self: Self): i32; }
impl Tag for i32 { function tag(self: Self): i32 { return 1; } }
impl Tag for string { function tag(self: Self): i32 { return 2; } }
impl Tag for f64 { function tag(self: Self): i32 { return 3; } }
impl Tag for f32 { function tag(self: Self): i32 { return 4; } }
impl Tag for boolean { function tag(self: Self): i32 { return 5; } }
impl Tag for u32 { function tag(self: Self): i32 { return 6; } }
impl Tag for u64 { function tag(self: Self): i32 { return 7; } }

function pick[T, K: Tag](x: T, key: (T) => K): i32 { return key(x).tag(); }

`

var fnValueRecvDispatchCases = []struct {
	name string
	want int
	src  string
}{
	// The issue's table, row by row. rows = [{7,"abcd"}, {9,"ef"}].
	{"fwd_i32", 16, fnValueRecvKeyTrait + `function main(): i32 {
    var rows: Row[] = [Row { n: 7, name: "abcd" }, Row { n: 9, name: "ef" }];
    return fwd_i32(rows, (r: Row): i32 => r.n);
}`},
	{"fwd_str", 6, fnValueRecvKeyTrait + `function main(): i32 {
    var rows: Row[] = [Row { n: 7, name: "abcd" }, Row { n: 9, name: "ef" }];
    return fwd_str(rows, (r: Row): string => r.name);
}`},
	{"two_hop", 16, fnValueRecvKeyTrait + `function main(): i32 {
    var rows: Row[] = [Row { n: 7, name: "abcd" }, Row { n: 9, name: "ef" }];
    return hop(rows, (r: Row): i32 => r.n);
}`},
	{"direct_lambda", 22, fnValueRecvKeyTrait + `function main(): i32 {
    var rows: Row[] = [Row { n: 7, name: "abcd" }, Row { n: 9, name: "ef" }];
    return keyed_sum(rows, (r: Row): i32 => r.n)
         + keyed_sum(rows, (r: Row): string => r.name);
}`},
	// The issue's repro as written: both forwards in one program. It is its
	// own row because the wrong answer was CONTEXT-dependent — what `K` bound
	// to moved with what else the unit monomorphised — so a per-term table
	// measured one at a time would not have caught every shape.
	{"both_forwards", 22, fnValueRecvKeyTrait + `function main(): i32 {
    var rows: Row[] = [Row { n: 7, name: "abcd" }, Row { n: 9, name: "ef" }];
    return fwd_i32(rows, (r: Row): i32 => r.n)
         + fwd_str(rows, (r: Row): string => r.name);
}`},

	// The scalar sweep: one row per key type, each answering its own impl's
	// constant. A wrong dispatch answers 1 (i32's) or refuses the module.
	{"key_i32", 1, fnValueRecvScalarTrait + `function main(): i32 { return pick(0, (q: i32): i32 => 9); }`},
	{"key_string", 2, fnValueRecvScalarTrait + `function main(): i32 { return pick(0, (q: i32): string => "abc"); }`},
	{"key_f64", 3, fnValueRecvScalarTrait + `function main(): i32 { return pick(0, (q: i32): f64 => 1.5); }`},
	{"key_f32", 4, fnValueRecvScalarTrait + `function main(): i32 { return pick(0, (q: i32): f32 => 1.5 as f32); }`},
	{"key_boolean", 5, fnValueRecvScalarTrait + `function main(): i32 { return pick(0, (q: i32): boolean => true); }`},
	{"key_u32", 6, fnValueRecvScalarTrait + `function main(): i32 { return pick(0, (q: i32): u32 => 1u32); }`},
	{"key_u64", 7, fnValueRecvScalarTrait + `function main(): i32 { return pick(0, (q: i32): u64 => 1u64); }`},
}

// runFnValueRecvDispatch drives one case through a self-host driver: it must
// route "ir" (a refusal is a regression too — the point is that the call
// dispatches, correctly), then the linked binary's exit code must equal both
// the interpreter oracle and the hand-computed expectation.
func runFnValueRecvDispatch(t *testing.T, dir, driver, root string, target []string, link func(t *testing.T, name, asm string) string, run func(bin string) *exec.Cmd) {
	t.Helper()
	for _, tc := range fnValueRecvDispatchCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			entry := filepath.Join(dir, "fvrd_"+tc.name+".fern")
			if err := os.WriteFile(entry, []byte(tc.src+"\n"), 0o644); err != nil {
				t.Fatalf("write entry: %v", err)
			}
			_, want := runFixtureInterp(t, entry, "")
			if want != tc.want {
				t.Fatalf("oracle = %d, want %d (the case stopped exercising what it describes)", want, tc.want)
			}
			args := append([]string{entry, root}, target...)
			out, _ := exec.Command(driver, append(args, "-decide")...).Output()
			if got := strings.TrimSpace(string(out)); got != "ir" {
				t.Fatalf("decide = %q, want \"ir\"", got)
			}
			asm, err := exec.Command(driver, args...).Output()
			if err != nil {
				t.Fatalf("self-host compile failed: %v", err)
			}
			if len(asm) == 0 {
				t.Fatalf("driver emitted 0 bytes")
			}
			cmd := run(link(t, "fvrd_"+tc.name+"_bin", string(asm)))
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != want {
				t.Errorf("self-host run = %d, want %d (native oracle)", code, want)
			}
		})
	}
}

func TestSelfHostFnValueRecvDispatch(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := copySelfHostTree(t)
	driver := buildSelfHostBin(t, gcc, dir, "asm_load_run.fern", "fvrd")
	root, err := filepath.Abs("../../internal/stdlib")
	if err != nil {
		t.Fatalf("abs stdlib root: %v", err)
	}
	runFnValueRecvDispatch(t, dir, driver, root, nil,
		func(t *testing.T, name, asm string) string { return buildBin(t, gcc, dir, name, asm) },
		func(bin string) *exec.Cmd { return runX86_64Bin(runner, bin) })
}

// arm64 parity: the dispatch decision is target-agnostic (it happens in irlower,
// before backend selection), but the aarch64 emitter is the only path where the
// self-host compiler produces the finished binary itself, so the gate runs there
// too. The driver is an x86 host binary emitting aarch64 asm; aarch64 gcc links
// it; qemu-aarch64 runs it.
func TestSelfHostFnValueRecvDispatchArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	if len(x86runner) != 0 {
		t.Skip("arm64 fn-value dispatch gate needs a native x86 host to run the driver")
	}
	dir := copySelfHostTree(t)
	driver := buildSelfHostBin(t, x86gcc, dir, "asm_load_run.fern", "fvrd_arm64")
	root, err := filepath.Abs("../../internal/stdlib")
	if err != nil {
		t.Fatalf("abs stdlib root: %v", err)
	}
	runFnValueRecvDispatch(t, dir, driver, root, []string{"-target", "arm64-linux"},
		func(t *testing.T, name, asm string) string { return buildBinArm64(t, arm64gcc, dir, name, asm) },
		func(bin string) *exec.Cmd { return runArm64Bin(qemu, bin) })
}
