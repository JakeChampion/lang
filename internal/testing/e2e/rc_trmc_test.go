package e2e

import (
	"fmt"
	"os/exec"
	"runtime"
	"syscall"
	"testing"
)

const trmcMapSrc = `enum List { Cons(i32, List), Nil }
function inc_all(xs: List): List {
    match (xs) {
        Cons(h, t) => { return Cons(h + 1, inc_all(t)); },
        Nil => { return Nil; },
    }
}
function build(n: i32): List {
    let acc: List = Nil;
    let i: i32 = 0;
    while (i < n) { acc = Cons(i, acc); i = i + 1; }   // [n-1, .., 1, 0]
    return acc;
}
function sum(l: List): i32 {
    let acc: i32 = 0;
    let cur: List = l;
    let go: boolean = true;
    while (go) { match (cur) { Cons(h, t) => { acc = acc + h; cur = t; }, Nil => { go = false; } } }
    return acc;
}
function main(): i32 {
    let ys: List = inc_all(build(50));   // sum(0..49) = 1225, +50 (one per elem) = 1275
    if (sum(ys) != 1275) { return 1; }
    return __rc_underflow_count();
}`

func TestX86_64Trmc(t *testing.T) {
	if _, code := compileAndRunX86_64FreeOn(t, trmcMapSrc); code != 0 {
		t.Errorf("trmc map: got %d, want 0", code)
	}
}

func TestArm64Trmc(t *testing.T) {
	if _, code := compileAndRunArm64FreeOn(t, trmcMapSrc); code != 0 {
		t.Errorf("trmc map: got %d, want 0", code)
	}
}

func TestWASMTrmc(t *testing.T) {
	if got := runWasm(t, trmcMapSrc); got != 0 {
		t.Errorf("trmc map: got %d, want 0", got)
	}
}

func TestInterpTrmc(t *testing.T) {
	if got := runInterpExit(t, trmcMapSrc); got != 0 {
		t.Errorf("trmc map under the interpreter: got %d, want 0", got)
	}
}

const trmcDeepSrc = `enum List { Cons(i32, List), Nil }
function inc_all(xs: List): List {
    match (xs) { Cons(h, t) => { return Cons(h + 1, inc_all(t)); }, Nil => { return Nil; } }
}
function build(n: i32): List { let acc: List = Nil; let i: i32 = 0; while (i < n) { acc = Cons(1, acc); i = i + 1; } return acc; }
function sum(l: List): i32 { let acc: i32 = 0; let cur: List = l; let go: boolean = true; while (go) { match (cur) { Cons(h, t) => { acc = acc + h; cur = t; }, Nil => { go = false; } } } return acc; }
function main(): i32 {
    if (sum(inc_all(build(300000))) != 600000) { return 1; }   // 300k elems, 1 -> 2 each
    return 0;
}`

func runWithStackLimit(t *testing.T, kib int, bin string, expectOverflow bool) int {
	t.Helper()
	script := fmt.Sprintf("ulimit -S -s %d && exec \"$1\"", kib)
	if expectOverflow && runtime.GOOS == "linux" {
		script = "printf '0\\n' > /proc/self/coredump_filter && " + script
	}
	cmd := exec.Command("bash", "-c", script, "--", bin)
	out, err := cmd.CombinedOutput()
	if err != nil && cmd.ProcessState == nil {
		t.Fatalf("run %s: %v\n%s", bin, err, out)
	}
	if expectOverflow {
		status, ok := cmd.ProcessState.Sys().(syscall.WaitStatus)
		if !ok || !status.Signaled() || status.Signal() != syscall.SIGSEGV {
			t.Fatalf("run %s: want stack-overflow SIGSEGV, got %v\n%s", bin, cmd.ProcessState, out)
		}
	}
	return cmd.ProcessState.ExitCode()
}

func TestX86_64TrmcDeepStack(t *testing.T) {
	bin, _ := compileX86_64FreeOn(t, trmcDeepSrc)
	if code := runWithStackLimit(t, 16*1024, bin, false); code != 0 {
		t.Errorf("deep map should succeed in a 16 MiB stack, got %d", code)
	}
}
