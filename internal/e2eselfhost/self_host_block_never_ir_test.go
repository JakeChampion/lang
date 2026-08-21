package e2eselfhost

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// blockNeverCases pin a value-position `{ … }` block whose statements always
// exit early (#6858). Such a block never reaches a trailing value, so its type
// is the bottom type `never`, not void — the rule native has carried since
// #4522. The self-host front end classified every tail-less block as an error
// and rejected these with P001, which is why #6852's own repro spelling
// (`(n: i32): i32 => { return n; }`) did not compile here.
//
// Each case is oracle-checked against the interpreter: the block's statements
// run INLINE in the enclosing function, so an accepted-but-mislowered version
// would take the block's dead tail as the function's answer instead of the
// `return` that fired.
var blockNeverCases = []struct {
	name string
	src  string
}{
	// The issue's spelling: an arrow lambda whose block body leaves only
	// through `return`.
	{"arrow_lambda_block_return", `function main(): i32 {
    var f = (n: i32): i32 => { return n; };
    return f(4);
}`},
	// A diverging block in a `var` initialiser: the trailing `return 2` is the
	// FUNCTION's return, not the block's value, so `+ 100` is unreachable.
	{"var_init_all_paths_return", `function f(c: boolean): i32 {
    var x: i32 = { if (c) { return 1; } return 2; };
    return x + 100;
}
function main(): i32 {
    return f(true) * 10 + f(false);
}`},
	// A diverging `if`-EXPRESSION arm: the then arm never yields a value, so the
	// arm type comes from the else arm alone.
	{"if_expression_arm_returns", `function pick(c: boolean): i32 {
    var x: i32 = if (c) { return 1; } else { 2 };
    return x + 100;
}
function main(): i32 {
    return pick(true) * 10 + pick(false);
}`},
	// A string-typed diverging block — the dead tail must not retype the block.
	{"string_block_all_paths_return", `function label(c: boolean): string {
    var s: string = { if (c) { return "yes"; } return "no"; };
    return s + "!";
}
function main(): i32 {
    return label(true).len() * 10 + label(false).len();
}`},
}

// TestSelfHostBlockNeverIR_X86_64 drives blockNeverCases through the self-host
// x86-64 IR path under FERN_STRICT_IR.
func TestSelfHostBlockNeverIR_X86_64(t *testing.T) {
	dir, mmc, stdlibRoot, gcc, runner, interpBin := annotateF64ProjDir(t)

	for _, tc := range blockNeverCases {
		t.Run(tc.name, func(t *testing.T) {
			want := interpExit(t, interpBin, tc.src)
			proj := t.TempDir()
			mainPath := filepath.Join(proj, "main.fern")
			if err := os.WriteFile(mainPath, []byte(tc.src), 0o644); err != nil {
				t.Fatalf("write main.fern: %v", err)
			}

			cmd := runX86_64Bin(runner, mmc, mainPath, stdlibRoot)
			cmd.Env = append(os.Environ(), "FERN_STRICT_IR=1")
			asm, cerr := cmd.Output()
			if cerr != nil {
				t.Fatalf("strict-IR compile: %v: %s", cerr, exitStderr(cerr))
			}
			if len(asm) == 0 {
				t.Fatal("loader emitted 0 bytes")
			}
			progBin := buildBin(t, gcc, dir, "blocknever_"+tc.name, string(asm))
			run := runX86_64Bin(runner, progBin)
			_ = run.Run()
			if code := run.ProcessState.ExitCode(); code != want {
				t.Errorf("%s exited %d, want %d (interp oracle)", tc.name, code, want)
			}
		})
	}
}

// TestSelfHostBlockValuelessStillRejected is the other direction: a tail-less
// block that can FALL THROUGH still has no value and is still refused. Without
// it, admitting the `never` case could widen into accepting every void block.
//
// The refusal comes from the build gate and names the same code native does,
// E061. It used to reach IR lowering and be refused there instead, so this
// asserted on that bail's wording; gating every coded diagnostic (#6961) means
// the checker now speaks first, which is the convergence worth pinning.
func TestSelfHostBlockValuelessStillRejected(t *testing.T) {
	_, mmc, stdlibRoot, _, runner, _ := annotateF64ProjDir(t)
	const src = `function side(): i32 { return 1; }
function main(): i32 {
    var x: i32 = { side(); };
    return x;
}`
	proj := t.TempDir()
	mainPath := filepath.Join(proj, "main.fern")
	if err := os.WriteFile(mainPath, []byte(src), 0o644); err != nil {
		t.Fatalf("write main.fern: %v", err)
	}
	cmd := runX86_64Bin(runner, mmc, mainPath, stdlibRoot)
	out, _ := cmd.CombinedOutput()
	if cmd.ProcessState.ExitCode() == 0 {
		t.Fatalf("a fall-through value-less block was accepted; driver emitted %d bytes of asm", len(out))
	}
	if !strings.Contains(string(out), "E061") {
		t.Errorf("refused, but not with native's E061; driver said: %s", out)
	}
}
