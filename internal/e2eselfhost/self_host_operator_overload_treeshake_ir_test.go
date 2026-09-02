package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// operatorOverloadRootCases are operator overloads whose method RETURNS A
// TYPE OTHER THAN ITS RECEIVER — the shape #8032 pruned.
//
// treeshake resolves the six arithmetic methods by receiver type, so a bare
// `add` root does not keep `add` for every type in the program. The root an
// operator node contributes used to be qualified by ExprBinary.ty /
// ExprUnary.ty, which annotate_module stamps with the type of the WHOLE
// expression. That is the receiver for the usual `add(self, other): Self`,
// and nothing else. `-w` on a `W.neg(): string` rooted `string|neg` while
// the lookup asked for `W|neg`, so W.neg was pruned and lowering emitted a
// call to a symbol nothing defined:
//
//	FERN_STRICT_IR: main (call to unknown symbol W.neg)
//
// ts_recv_tag reads the OPERAND's stamped type instead, which is the one
// ts_arith_kept is asking about.
//
// Each case is an exit code, so it fails on a wrong answer as well as on a
// bail, and it runs on x86-64 where the shape was first found on wasm.
var operatorOverloadRootCases = []struct {
	name string
	src  string
}{
	{"binary_mul_returns_scalar", `struct V { v: i32 }

function (a: V) mul(b: V): i32 {
    return a.v * b.v;
}

function main(): i32 {
    var x: V = V { v: 6 };
    var y: V = V { v: 7 };
    return x * y;
}`},
	{"binary_add_returns_string", `struct S { s: string }

function (a: S) add(b: S): string {
    return a.s;
}

function main(): i32 {
    var p: S = S { s: "hello" };
    var q: S = S { s: "world" };
    if ((p + q).len() == 5) { return 42; }
    return 1;
}`},
	{"unary_neg_returns_scalar", `struct N { n: i32 }

function (a: N) neg(): i32 {
    return a.n;
}

function main(): i32 {
    var k: N = N { n: 42 };
    return -k;
}`},
	// The self-returning shape the receiver lookup already handled. It must
	// keep working: the return-type lookup is an addition, not a swap.
	{"binary_add_returns_self", `struct C { n: i32 }

function (a: C) add(b: C): C {
    return C { n: a.n + b.n };
}

function main(): i32 {
    var u: C = C { n: 40 };
    var v: C = C { n: 2 };
    return (u + v).n;
}`},
}

// TestSelfHostOperatorOverloadRootsIRX86_64 compiles each shape with the
// full driver and oracle-checks the exit against the interpreter.
//
// The driver matters, and a first version of this test got it wrong: under
// asm_load_run nothing runs annotate_module, every `ty` is "", treeshake
// falls back to the BARE method name, and ts_kept_name's exact-name match
// keeps the method whatever its receiver — so the bug cannot appear and the
// test passed against the unfixed compiler. fern.fern annotates, which is
// what puts a qualified root in the set.
//
// FERN_STRICT_IR makes a prune fail the compile and name the site rather
// than routing the module to an AST emitter that no longer exists.
func TestSelfHostOperatorOverloadRootsIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "fern.fern")
	fernBin := buildSelfHostBin(t, gcc, dir, "fern.fern", "fern")
	stdlibRoot, err := filepath.Abs("../../internal/stdlib")
	if err != nil {
		t.Fatalf("abs stdlib root: %v", err)
	}

	for _, tc := range operatorOverloadRootCases {
		t.Run(tc.name, func(t *testing.T) {
			want := interpExit(t, interpBin, tc.src)
			proj := t.TempDir()
			mainPath := filepath.Join(proj, "main.fern")
			if err := os.WriteFile(mainPath, []byte(tc.src), 0o644); err != nil {
				t.Fatalf("write main.fern: %v", err)
			}
			asmPath := filepath.Join(proj, "out.s")
			ccmd := runX86_64Bin(runner, fernBin, "-target", "x86-64-linux", "-emit", "asm", mainPath, stdlibRoot, "-o", asmPath)
			ccmd.Env = append(os.Environ(), "FERN_STRICT_IR=1")
			if out, cerr := ccmd.CombinedOutput(); cerr != nil {
				t.Fatalf("compile: %v (%s)", cerr, out)
			}
			binPath := filepath.Join(proj, "out.bin")
			if out, lerr := exec.Command(gcc, "-nostdlib", "-static", "-o", binPath, asmPath).CombinedOutput(); lerr != nil {
				t.Fatalf("link: %v (%s)", lerr, out)
			}
			rcmd := runX86_64Bin(runner, binPath)
			_ = rcmd.Run()
			if got := rcmd.ProcessState.ExitCode(); got != want {
				t.Errorf("%s = %d, want %d (interp oracle)", tc.name, got, want)
			}
		})
	}
}
