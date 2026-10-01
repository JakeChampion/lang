package e2eselfhost

import (
	"strings"
	"testing"
)

// A trait ASSOCIATED function reached through a `dyn` value. The call has no
// receiver to dispatch on, and a compiler that matched the dispatch arm on the
// method NAME called `make` with the dyn value prepended as an argument it does
// not take: `P { v: 0 }` was read as the `Box` the body projects, so the
// program answered 0 where 7 is the only number the source can mean (#7398).
// The CLI's checker rejects the call (E021) before any emitter sees it, and
// asm_ir_run, which runs no checker, has the typed lowering refuse it by name.
const dynAssocFnSrc = `struct Box { v: i32 }
trait Mk { function make(own b: Box): i32; }
struct P { v: i32 }
impl Mk for P { function make(own b: Box): i32 { return b.v; } }
function twice(own m: dyn Mk, b1: Box, b2: Box): i32 { return m.make(b1) + m.make(b2); }
function main(): i32 { return twice(P { v: 0 }, Box { v: 3 }, Box { v: 4 }); }`

// The `self`-taking twin: an ordinary method through the same dyn shape must
// still dispatch and answer, so the exclusion cannot widen into real methods.
const dynSelfMethodSrc = `struct Box { v: i32 }
trait Mk { function make(self: Self, b: Box): i32; }
struct P { v: i32 }
impl Mk for P { function make(self: Self, b: Box): i32 { return b.v + self.v; } }
function twice(m: dyn Mk, b1: Box, b2: Box): i32 { return m.make(b1) + m.make(b2); }
function main(): i32 { return twice(P { v: 0 }, Box { v: 3 }, Box { v: 4 }); }`

var dynAssocFnCases = []struct {
	name string
	src  string
	want int
}{
	{"self-method-dispatches", dynSelfMethodSrc, 7},
}

// checkDynAssocFnRejected asserts the CLI refuses dynAssocFnSrc for target
// with E021 rather than compiling it.
func checkDynAssocFnRejected(t *testing.T, cli *strictCLI, target string) {
	t.Helper()
	t.Run("assoc-fn-rejected", func(t *testing.T) {
		if _, diags, err := cli.tryEmit(t, target, dynAssocFnSrc); err == nil || !strings.Contains(diags, "E021") {
			t.Errorf("%s compiled an associated function called through dyn (err %v), want E021:\n%s", target, err, diags)
		}
	})
}

// TestSelfHostDynAssocFnDispatchX86_64 — the x86-64 leg.
func TestSelfHostDynAssocFnDispatchX86_64(t *testing.T) {
	cli := newStrictCLI(t)
	checkDynAssocFnRejected(t, cli, "x86-64-linux")
	t.Run("assoc-fn-traps-unchecked", func(t *testing.T) {
		gcc, runner := x86_64Tooling(t)
		dir := t.TempDir()
		copySelfHostDriver(t, dir, "asm_ir_run.fern")
		driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")
		cmd := runX86_64Bin(runner, driverBin)
		cmd.Stdin = strings.NewReader(dynAssocFnSrc)
		cmd.Env = childEnv()
		var stderr strings.Builder
		cmd.Stderr = &stderr
		_ = cmd.Run()
		if code := cmd.ProcessState.ExitCode(); code != 3 || !strings.Contains(stderr.String(), "dyn method has no implementation: make") {
			t.Errorf("an unchecked associated-function call through dyn: driver exited %d, want 3 naming the method:\n%s", code, stderr.String())
		}
	})
	for _, tc := range dynAssocFnCases {
		t.Run(tc.name, func(t *testing.T) {
			if code, _ := cli.runX86(t, cli.emit(t, "x86-64-linux", tc.src)); code != tc.want {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}

// TestSelfHostDynAssocFnDispatchArm64 — the same cases through the arm64 emit.
func TestSelfHostDynAssocFnDispatchArm64(t *testing.T) {
	gcc, qemu := arm64Tooling(t)
	cli := newStrictCLI(t)
	checkDynAssocFnRejected(t, cli, "arm64-linux")
	for _, tc := range dynAssocFnCases {
		t.Run(tc.name, func(t *testing.T) {
			if code, _ := runArm64(t, gcc, qemu, cli.emit(t, "arm64-linux", tc.src)); code != tc.want {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}

// TestSelfHostDynAssocFnDispatchWasmIR — the wasm leg.
func TestSelfHostDynAssocFnDispatchWasmIR(t *testing.T) {
	cli := newStrictCLI(t)
	checkDynAssocFnRejected(t, cli, "wasm32-wasi")
	for _, tc := range dynAssocFnCases {
		t.Run(tc.name, func(t *testing.T) {
			if code, _ := runWasm(t, cli.emit(t, "wasm32-wasi", tc.src)); code != tc.want {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}
