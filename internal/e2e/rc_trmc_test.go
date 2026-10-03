package e2e

import (
	"fmt"
	"os/exec"
	"runtime"
	"syscall"
	"testing"
)

// Tail-recursion-modulo-cons (ast.TrmcEnabled). A `map`-shaped function —
// `match (xs) { Cons(h,t) => Cons(g(h), self(t)), Nil => Nil }` — is not
// tail-recursive (the constructor wraps the recursive call), so ordinary
// lowering grows the stack O(n). TRMC rewrites it into a hole-passing loop:
// O(1) stack, single pass. These pin (1) value-correctness + no rc
// over-release on all three backends, (2) agreement with the interpreter,
// which has no TRMC (the gate that makes the optimisation invisible), and
// (3) the actual O(1) stack win — a deep list succeeds under a 16 MiB stack.

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

// The interpreter does not rewrite the recursion, so agreeing with it is what
// makes the transform invisible.
func TestInterpTrmc(t *testing.T) {
	if got := runInterpExit(t, trmcMapSrc); got != 0 {
		t.Errorf("trmc map under the interpreter: got %d, want 0", got)
	}
}

// --- O(1) stack: a deep list succeeds under a bounded stack ---------------

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

// runWithStackLimit executes bin under an explicit RLIMIT_STACK soft limit
// (in KiB) and returns its exit code, so a deep-stack test measures against a
// fixed stack rather than whatever soft limit the host happens to use. With
// expectOverflow it requires the stack overflow's SIGSEGV.
func runWithStackLimit(t *testing.T, kib int, bin string, expectOverflow bool) int {
	t.Helper()
	script := fmt.Sprintf("ulimit -S -s %d && exec \"$1\"", kib)
	if expectOverflow && runtime.GOOS == "linux" {
		// Piped core handlers ignore RLIMIT_CORE. Omit memory mappings from
		// this expected crash's dump before exec; the filter survives exec.
		// Keep the parent's filter and unexpected-crash diagnostics intact.
		script = "printf '0\\n' > /proc/self/coredump_filter && " + script
	}
	cmd := exec.Command("bash", "-c", script, "--", bin)
	out, err := cmd.CombinedOutput()
	if err != nil && cmd.ProcessState == nil {
		t.Fatalf("run %s: %v\n%s", bin, err, out)
	}
	if expectOverflow {
		// A failed shell setup or ordinary nonzero exit is not a stack
		// overflow. Require the actual signal, including after filtering.
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
