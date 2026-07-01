package x86_64ssa_test

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/jakechampion/lang/internal/ast"
	"github.com/jakechampion/lang/internal/checker"
	x86 "github.com/jakechampion/lang/internal/codegen/x86_64ssa"
	"github.com/jakechampion/lang/internal/interp"
	"github.com/jakechampion/lang/internal/monomorph"
	nativeelf "github.com/jakechampion/lang/internal/native/elf"
	nativex86 "github.com/jakechampion/lang/internal/native/x86_64"
	"github.com/jakechampion/lang/internal/parser"
)

// interpMain runs the program's main() through the tree-walking interpreter and
// returns its i32 result — an oracle independent of the SSA path.
func interpMain(t *testing.T, prog *ast.Program) int64 {
	t.Helper()
	ip := interp.New()
	for _, ed := range prog.Enums {
		ip.RegisterEnum(ed)
	}
	for _, fn := range prog.Funcs {
		ip.Register(fn)
	}
	v, err := ip.CallByName("main", nil)
	if err != nil {
		t.Fatalf("interp main: %v", err)
	}
	n, ok := v.(interp.Number)
	if !ok {
		t.Fatalf("interp main returned %T, want Number", v)
	}
	return int64(n)
}

// programMatchesInterp compiles `src` to a native binary through the SSA
// whole-program path (EmitProgram), runs it, and asserts its exit code equals
// the interpreter's main() result mod 256. This exercises the full chain —
// lower → lift → allocate → emit → assemble → run — on a real program, diffed
// against an independent oracle.
func programMatchesInterp(t *testing.T, src string, numAlloc int) {
	t.Helper()
	prog, err := parser.Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	info, err := checker.Check(prog)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if err := monomorph.Run(prog, info); err != nil {
		t.Fatalf("monomorph: %v", err)
	}

	want := interpMain(t, prog)

	asm, err := x86.EmitProgram(prog, info, numAlloc)
	if err != nil {
		t.Fatalf("EmitProgram: %v", err)
	}
	if runtime.GOARCH != "amd64" || runtime.GOOS != "linux" {
		t.Skipf("native x86-64 run needs amd64/linux, have %s/%s", runtime.GOOS, runtime.GOARCH)
	}
	text, rodata, err := nativex86.AssembleProgram(asm, nativeelf.TextVAddr)
	if err != nil {
		t.Fatalf("AssembleProgram: %v\n--- asm ---\n%s", err, asm)
	}
	bin := filepath.Join(t.TempDir(), "prog")
	if err := os.WriteFile(bin, nativeelf.StaticExecutableDataX86(text, rodata), 0o755); err != nil {
		t.Fatalf("write bin: %v", err)
	}
	got := 0
	if err := exec.Command(bin).Run(); err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			got = ee.ExitCode()
		} else {
			t.Fatalf("run: %v", err)
		}
	}
	if got != int(uint8(want)) {
		t.Errorf("SSA-emitted program exit=%d, want interp&0xFF=%d (interp=%d)\nsrc: %s", got, int(uint8(want)), want, src)
	}
}

// Whole-program integer subset: the SSA path compiles and runs real programs
// with the same result as the interpreter.
func TestProgramRunInteger(t *testing.T) {
	srcs := []string{
		`function main(): i32 { return 1 + 2 + 3; }`,
		`function main(): i32 { return (10 - 2) * 3; }`,
		`function add(a: i32, b: i32): i32 { return a + b; } function main(): i32 { return add(20, 22); }`,
		`function fact(n: i32): i32 { if (n <= 1) { return 1; } return n * fact(n - 1); } function main(): i32 { return fact(5); }`,
		`function main(): i32 { var t: i32 = 0; var i: i32 = 0; while (i < 10) { t = t + i; i = i + 1; } return t; }`,
		`function abs(n: i32): i32 { if (n < 0) { return 0 - n; } return n; } function main(): i32 { return abs(0 - 7); }`,
		`function main(): i32 { return (100 / 7) + (100 % 7); }`,
	}
	for _, n := range []int{1, 2, 8} {
		for _, src := range srcs {
			programMatchesInterp(t, src, n)
		}
	}
}

// Whole-program closure dispatch: a lambda flows into a higher-order function
// and is called indirectly, exercising the full chain — OpConstFunc/OpMakeClosure
// lift to a {fn, env} cell, OpCallIndirect derefs it and dispatches through the
// real-asm function-address table with env appended last. Scope here is the
// non-escaping case (the closure is passed and called, never stored past its
// use), so no RC drop helpers are emitted; stored/capturing closures need the
// runtime-helper slice (__fern_closure_drop / __fern_rc_is_unique) and are a
// follow-up. Runs the same result as the interpreter.
func TestProgramRunClosure(t *testing.T) {
	srcs := []string{
		// Lambda passed to apply and called indirectly: apply(41, n => n+1) = 42.
		`function apply(x: i32, f: (i32) => i32): i32 { return f(x); }
		 function main(): i32 { return apply(41, (n: i32): i32 => n + 1); }`,
		// Two different lambdas dispatched through the same higher-order function.
		`function apply(x: i32, f: (i32) => i32): i32 { return f(x); }
		 function main(): i32 {
		   return apply(20, (n: i32): i32 => n + 1) + apply(10, (n: i32): i32 => n * 2);
		 }`,
		// A named top-level function used as a value (bare OpConstFunc, env=0).
		`function inc(n: i32): i32 { return n + 1; }
		 function apply(x: i32, f: (i32) => i32): i32 { return f(x); }
		 function main(): i32 { return apply(41, inc); }`,
	}
	for _, n := range []int{1, 2, 8} {
		for _, src := range srcs {
			programMatchesInterp(t, src, n)
		}
	}
}

// Whole-program escaping closures: a closure bound to a local (so the IR inserts
// a scope-exit __fern_closure_drop, which gates on __fern_rc_is_unique and
// releases through __fern_box_free / __fern_rc_dec). Exercises the RC runtime
// helpers (docs/SSA-RC-RUNTIME.md) end-to-end against the interpreter. Scope:
// zero or one scalar capture, used linearly (rc stays 1, so drop takes the free
// path). Multi-capture closures need the packed env-slot layout the SSA emit
// doesn't yet apply (it uses uniform 8-byte slots, correct only at offset 0) —
// a separate follow-up.
func TestProgramRunClosureEscaping(t *testing.T) {
	srcs := []string{
		// Stored non-capturing closure: var g = (n) => n*2; g(21) = 42.
		`function main(): i32 { var g: (i32) => i32 = (n: i32): i32 => n * 2; return g(21); }`,
		// Scalar-capturing closure: captures a: i32. g(5) = 5 + 10 = 15.
		`function main(): i32 {
		   var a: i32 = 10;
		   var g: (i32) => i32 = (n: i32): i32 => n + a;
		   return g(5);
		 }`,
	}
	for _, n := range []int{1, 2, 8} {
		for _, src := range srcs {
			programMatchesInterp(t, src, n)
		}
	}
}

// Whole-program Option + match: the pair-return (Some/None), the box the match
// reconstructs (i32 fields at 4-byte offsets — needs the 4-byte load/store), and
// the match-join phi all combine. Runs the same result as the interpreter.
func TestProgramRunOption(t *testing.T) {
	half := `function half(n: i32): Option[i32] {
		if (n % 2 == 0) { return Some(n / 2); }
		return None;
	}`
	srcs := []string{
		// Some path: half(84) = Some(42) -> 42.
		half + `function main(): i32 { return match (half(84)) { Some(v) => v, None => 0 }; }`,
		// None path: half(7) = None -> 99.
		half + `function main(): i32 { return match (half(7)) { Some(v) => v, None => 99 }; }`,
	}
	for _, n := range []int{1, 2, 8} {
		for _, src := range srcs {
			programMatchesInterp(t, src, n)
		}
	}
}
