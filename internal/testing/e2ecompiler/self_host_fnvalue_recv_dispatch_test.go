package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const fnValueRecvKeyTrait = `trait Key { function k_id(self: Self): i32; }
impl Key for i32 { function k_id(self: Self): i32 { return self; } }
impl Key for string { function k_id(self: Self): i32 { return self.len(); } }

function keyed_sum[T, K: Key](xs: T[], key: (T) => K): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < xs.len()) { acc = acc + key(xs[i]).k_id(); i = i + 1; }
    return acc;
}

function fwd_i32[T](xs: T[], key: (T) => i32): i32 { return keyed_sum(xs, key); }
function fwd_str[T](xs: T[], key: (T) => string): i32 { return keyed_sum(xs, key); }
function hop[T](xs: T[], key: (T) => i32): i32 { return fwd_i32(xs, key); }

struct Row { n: i32, name: string }

`

const fnValueRecvScalarTrait = `import "std/i32";
import "std/f64";

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
	{"fwd_i32", 16, fnValueRecvKeyTrait + `function main(): i32 {
    let rows: Row[] = [Row { n: 7, name: "abcd" }, Row { n: 9, name: "ef" }];
    return fwd_i32(rows, (r: Row): i32 => r.n);
}`},
	{"fwd_str", 6, fnValueRecvKeyTrait + `function main(): i32 {
    let rows: Row[] = [Row { n: 7, name: "abcd" }, Row { n: 9, name: "ef" }];
    return fwd_str(rows, (r: Row): string => r.name);
}`},
	{"two_hop", 16, fnValueRecvKeyTrait + `function main(): i32 {
    let rows: Row[] = [Row { n: 7, name: "abcd" }, Row { n: 9, name: "ef" }];
    return hop(rows, (r: Row): i32 => r.n);
}`},
	{"direct_lambda", 22, fnValueRecvKeyTrait + `function main(): i32 {
    let rows: Row[] = [Row { n: 7, name: "abcd" }, Row { n: 9, name: "ef" }];
    return keyed_sum(rows, (r: Row): i32 => r.n)
         + keyed_sum(rows, (r: Row): string => r.name);
}`},
	{"both_forwards", 22, fnValueRecvKeyTrait + `function main(): i32 {
    let rows: Row[] = [Row { n: 7, name: "abcd" }, Row { n: 9, name: "ef" }];
    return fwd_i32(rows, (r: Row): i32 => r.n)
         + fwd_str(rows, (r: Row): string => r.name);
}`},

	{"key_i32", 1, fnValueRecvScalarTrait + `function main(): i32 { return pick(0, (q: i32): i32 => 9); }`},
	{"key_string", 2, fnValueRecvScalarTrait + `function main(): i32 { return pick(0, (q: i32): string => "abc"); }`},
	{"key_f64", 3, fnValueRecvScalarTrait + `function main(): i32 { return pick(0, (q: i32): f64 => 1.5); }`},
	{"key_f32", 4, fnValueRecvScalarTrait + `function main(): i32 { return pick(0, (q: i32): f32 => 1.5 as f32); }`},
	{"key_boolean", 5, fnValueRecvScalarTrait + `function main(): i32 { return pick(0, (q: i32): boolean => true); }`},
	{"key_u32", 6, fnValueRecvScalarTrait + `function main(): i32 { return pick(0, (q: i32): u32 => 1u32); }`},
	{"key_u64", 7, fnValueRecvScalarTrait + `function main(): i32 { return pick(0, (q: i32): u64 => 1u64); }`},
}

func runFnValueRecvDispatch(t *testing.T, dir, driver, root string, runner, target []string, link func(t *testing.T, name, asm string) string, run func(bin string) *exec.Cmd) {
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
			out, err := runX86_64Bin(runner, driver, append(args, "-decide")...).CombinedOutput()
			if err != nil {
				t.Fatalf("self-host decide failed: %v\n%s", err, out)
			}
			if got := strings.TrimSpace(string(out)); got != "ir" {
				t.Fatalf("decide = %q, want \"ir\"", got)
			}
			asm, err := runX86_64Bin(runner, driver, args...).Output()
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
	driver := buildSelfHostBin(t, gcc, dir, "drivers/asm_load_run.fern", "fvrd")
	root, err := filepath.Abs("../../stdlib")
	if err != nil {
		t.Fatalf("abs stdlib root: %v", err)
	}
	runFnValueRecvDispatch(t, dir, driver, root, runner, nil,
		func(t *testing.T, name, asm string) string { return buildBin(t, gcc, dir, name, asm) },
		func(bin string) *exec.Cmd { return runX86_64Bin(runner, bin) })
}

func TestSelfHostFnValueRecvDispatchArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := copySelfHostTree(t)
	driver := buildSelfHostBin(t, x86gcc, dir, "drivers/asm_load_run.fern", "fvrd_arm64")
	root, err := filepath.Abs("../../stdlib")
	if err != nil {
		t.Fatalf("abs stdlib root: %v", err)
	}
	runFnValueRecvDispatch(t, dir, driver, root, x86runner, []string{"-target", "arm64-linux"},
		func(t *testing.T, name, asm string) string { return buildBinArm64(t, arm64gcc, dir, name, asm) },
		func(bin string) *exec.Cmd { return runArm64Bin(qemu, bin) })
}
