package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

var trmcConsumeCases = []struct {
	name string
	src  string
	want int
}{
	{"trmc-consume-halves-peak", `enum List { Cons(i32, List), Nil }
function inc_all(xs: List): List {
    match (xs) {
        Cons(h, t) => { return Cons(h + 1, inc_all(t)); },
        Nil => { return Nil; },
    }
}
function build(n: i32): List { let acc: List = Nil; let i: i32 = 0; while (i < n) { acc = Cons(1, acc); i = i + 1; } return acc; }
function sum(l: List): i32 { let acc: i32 = 0; let cur: List = l; let go: boolean = true; while (go) { match (cur) { Cons(h, t) => { acc = acc + h; cur = t; }, Nil => { go = false; } } } return acc; }
function main(): i32 {
    let warm: List = inc_all(build(10));
    if (sum(warm) != 20) { return 97; }
    let b1: i32 = (__heap_bump_bytes() as i32);
    let ys: List = inc_all(build(2000));
    let b2: i32 = (__heap_bump_bytes() as i32);
    if (sum(ys) != 4000) { return 96; }
    if (__rc_underflow_count() != 0) { return 99; }
    if ((b2 - b1) / 1024 >= 140) { return 98; }
    return 0;
}`, 0},
	{"trmc-consume-value-exact", `enum List { Cons(i32, List), Nil }
function inc_all(xs: List): List {
    match (xs) {
        Cons(h, t) => { return Cons(h + 1, inc_all(t)); },
        Nil => { return Nil; },
    }
}
function build(n: i32): List { let acc: List = Nil; let i: i32 = 0; while (i < n) { acc = Cons(i, acc); i = i + 1; } return acc; }
function sum(l: List): i32 { let acc: i32 = 0; let cur: List = l; let go: boolean = true; while (go) { match (cur) { Cons(h, t) => { acc = acc + h; cur = t; }, Nil => { go = false; } } } return acc; }
function main(): i32 {
    let ys: List = inc_all(build(50));
    if (sum(ys) != 1275) { return 96; }
    if (__rc_underflow_count() != 0) { return 99; }
    return 0;
}`, 0},
	{"trmc-consume-shared-input-safe", `enum List { Cons(i32, List), Nil }
function inc_all(xs: List): List {
    match (xs) {
        Cons(h, t) => { return Cons(h + 1, inc_all(t)); },
        Nil => { return Nil; },
    }
}
function build(n: i32): List { let acc: List = Nil; let i: i32 = 0; while (i < n) { acc = Cons(1, acc); i = i + 1; } return acc; }
function sum(l: List): i32 { let acc: i32 = 0; let cur: List = l; let go: boolean = true; while (go) { match (cur) { Cons(h, t) => { acc = acc + h; cur = t; }, Nil => { go = false; } } } return acc; }
function main(): i32 {
    let keep: List = build(30);
    let ys: List = inc_all(keep);
    if (sum(ys) != 60) { return 96; }
    if (sum(keep) != 30) { return 95; }
    if (sum(ys) != 60) { return 94; }
    if (__rc_underflow_count() != 0) { return 99; }
    return 0;
}`, 0},
	{"trmc-consume-shared-twice-safe", `enum List { Cons(i32, List), Nil }
function inc_all(xs: List): List {
    match (xs) {
        Cons(h, t) => { return Cons(h + 1, inc_all(t)); },
        Nil => { return Nil; },
    }
}
function build(n: i32): List { let acc: List = Nil; let i: i32 = 0; while (i < n) { acc = Cons(1, acc); i = i + 1; } return acc; }
function sum(l: List): i32 { let acc: i32 = 0; let cur: List = l; let go: boolean = true; while (go) { match (cur) { Cons(h, t) => { acc = acc + h; cur = t; }, Nil => { go = false; } } } return acc; }
function main(): i32 {
    let keep: List = build(20);
    let a: List = inc_all(keep);
    let b: List = inc_all(keep);
    if (sum(a) != 40) { return 96; }
    if (sum(b) != 40) { return 95; }
    if (sum(keep) != 20) { return 94; }
    if (__rc_underflow_count() != 0) { return 99; }
    return 0;
}`, 0},
	{"trmc-consume-string-head-refused", `enum SList { SCons(string, SList), SNil }
function tag_all(xs: SList): SList {
    match (xs) {
        SCons(h, t) => { return SCons(h + "!", tag_all(t)); },
        SNil => { return SNil; },
    }
}
function len_all(l: SList): i32 { let acc: i32 = 0; let cur: SList = l; let go: boolean = true; while (go) { match (cur) { SCons(h, t) => { acc = acc + h.len(); cur = t; }, SNil => { go = false; } } } return acc; }
function main(): i32 {
    let xs: SList = SCons("ab", SCons("cde", SNil));
    let ys: SList = tag_all(xs);
    if (len_all(ys) != 7) { return 96; }
    if (len_all(xs) != 5) { return 95; }
    if (__rc_underflow_count() != 0) { return 99; }
    return 0;
}`, 0},
	{"trmc-consume-fn-value-refused", `enum List { Cons(i32, List), Nil }
function inc_all(xs: List): List {
    match (xs) {
        Cons(h, t) => { return Cons(h + 1, inc_all(t)); },
        Nil => { return Nil; },
    }
}
function apply(f: (List) => List, l: List): List { return f(l); }
function build(n: i32): List { let acc: List = Nil; let i: i32 = 0; while (i < n) { acc = Cons(1, acc); i = i + 1; } return acc; }
function sum(l: List): i32 { let acc: i32 = 0; let cur: List = l; let go: boolean = true; while (go) { match (cur) { Cons(h, t) => { acc = acc + h; cur = t; }, Nil => { go = false; } } } return acc; }
function main(): i32 {
    let keep: List = build(20);
    let ys: List = apply(inc_all, keep);
    if (sum(ys) != 40) { return 96; }
    if (sum(keep) != 20) { return 95; }
    if (__rc_underflow_count() != 0) { return 99; }
    return 0;
}`, 0},
	{"trmc-consume-deep-stack", `enum List { Cons(i32, List), Nil }
function inc_all(xs: List): List {
    match (xs) {
        Cons(h, t) => { return Cons(h + 1, inc_all(t)); },
        Nil => { return Nil; },
    }
}
function build(n: i32): List { let acc: List = Nil; let i: i32 = 0; while (i < n) { acc = Cons(1, acc); i = i + 1; } return acc; }
function sum(l: List): i32 { let acc: i32 = 0; let cur: List = l; let go: boolean = true; while (go) { match (cur) { Cons(h, t) => { acc = acc + h; cur = t; }, Nil => { go = false; } } } return acc; }
function main(): i32 {
    let ys: List = inc_all(build(300000));
    if (sum(ys) != 600000) { return 96; }
    if (__rc_underflow_count() != 0) { return 99; }
    return 0;
}`, 0},
}

func TestSelfHostTrmcConsumeIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	src, err := os.ReadFile("../../../compiler/drivers/asm_run.fern")
	if err != nil {
		t.Fatalf("read asm_run.fern: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "drivers/asm_run.fern"), src, 0o644); err != nil {
		t.Fatalf("write asm_run.fern: %v", err)
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_run.fern", "driver")

	for _, tc := range trmcConsumeCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, gcc, runner, driverBin, []byte(tc.src+"\n"))
			if len(asm) == 0 {
				t.Fatal("self-host compiler emitted 0 bytes")
			}
			bin := buildBin(t, gcc, dir, tc.name, string(asm))
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(bin)
			} else {
				cmd = exec.Command(runner[0], append(runner[1:], bin)...)
			}
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("%s = %d, want %d (98 = input not recycled; 99 = over-release; 94-97 = value corrupted, 95 on the shared case = the call-site retain is missing)", tc.name, code, tc.want)
			}
		})
	}
}

func TestSelfHostTrmcConsumeIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "drivers/asm_ir_run.fern", "driver")

	for _, tc := range trmcConsumeCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, x86gcc, x86runner, driverBin, []byte(tc.src+"\n"), "-target", "arm64-linux")
			if len(asm) == 0 {
				t.Fatal("self-host arm64 compiler emitted 0 bytes")
			}
			bin := buildBinArm64(t, arm64gcc, dir, tc.name, string(asm))
			cmd := runArm64Bin(qemu, bin)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("%s = %d, want %d (98 = input not recycled; 99 = over-release; 94-97 = value corrupted, 95 on the shared case = the call-site retain is missing)", tc.name, code, tc.want)
			}
		})
	}
}

func TestSelfHostArm64DarwinTrmcConsume(t *testing.T) {
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		t.Skip("requires Apple Silicon")
	}
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "fern.fern")
	cli := buildSelfHostBinArm64Darwin(t, dir, "fern.fern", "fern")
	for _, tc := range trmcConsumeCases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			src := filepath.Join(dir, "main.fern")
			if err := os.WriteFile(src, []byte(tc.src), 0o644); err != nil {
				t.Fatal(err)
			}
			bin := filepath.Join(dir, "main")
			cmd := exec.Command(cli, "-target", "arm64-darwin", "-o", bin, src, e2eharness.SelfHostStdlibRoot(t))
			for _, kv := range os.Environ() {
				if !strings.HasPrefix(kv, "FERN_SANITIZE=") && !strings.HasPrefix(kv, "FERN_RC_FREE_DEBUG=") {
					cmd.Env = append(cmd.Env, kv)
				}
			}
			cmd.Env = append(cmd.Env, "FERN_STRICT_IR=1", "FERN_LEAKCHECK=1")
			if tc.name != "trmc-consume-halves-peak" {
				cmd.Env = append(cmd.Env, "FERN_SANITIZE=1")
			}
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("compile: %v\n%s", err, out)
			}
			out, err := exec.Command(bin).CombinedOutput()
			if err != nil {
				t.Fatalf("run: %v, want exit %d\n%s", err, tc.want, out)
			}
			assertBalancedCensus(t, string(out))
		})
	}
}
