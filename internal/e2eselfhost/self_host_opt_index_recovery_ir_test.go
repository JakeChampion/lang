package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// optIndexRecoveryCases pin the Option/Result element recovery for an indexed
// read, across every shape the index BASE can take (#5646 option 3).
//
// Both scrutinee resolvers — the value-position one and the StmtMatch one —
// enumerated only `ExprIdent` and a named-field `ExprFieldAccess` as the base of
// `<base>[i]`. A nested index (`aoa[i][j]`) and a tuple element (`t.N[i]`) fell
// to the `_` arm, resolved to "", and bailed the enclosing function. Both
// now route through the shared `arr_tag_of`.
//
// The bail was invisible before `FERN_STRICT_IR`: the AST emitter happened to
// compile all four shapes correctly, so the exit codes agreed and only the
// routing differed. That is the whole point of the flag — these are the gaps a
// differential exit-code test cannot see, because there is nothing wrong with
// the answer, only with which backend produced it.
//
// The `-local` and `-struct-field` cases are the two bases that already worked;
// they are here so a regression in the shared helper is attributed to the
// refactor rather than to the two new shapes.
//
// Every `want` stays in [0, 126) — the wasm leg exits through WASI, which
// rejects anything above that.
var optIndexRecoveryCases = []struct {
	name string
	src  string
	want int
}{
	// New: the base is itself an index.
	{"nested-index", `
function main(): i32 {
    var aoa: Option[i32][][] = [[Some(4), None], [Some(9)]];
    match (aoa[0][0]) { Some(v) => { return v; }, None => { return 1; } }
}
`, 4},
	// New: the base is a tuple element.
	{"tuple-elem-index", `
function main(): i32 {
    var t: (Option[i32][], i32) = ([Some(7), None], 3);
    match (t.0[0]) { Some(v) => { return v; }, None => { return 1; } }
}
`, 7},
	// New, via `?` rather than `match` — the value-position resolver.
	{"nested-index-try", `
function first(aoa: Option[i32][][]): Option[i32] {
    var v: i32 = aoa[0][0]?;
    return Some(v + 1);
}
function main(): i32 {
    match (first([[Some(4), None]])) { Some(v) => { return v; }, None => { return 1; } }
}
`, 5},
	// Pre-existing: the base is a local.
	{"local-index", `
function main(): i32 {
    var a: Option[i32][] = [Some(4), None];
    match (a[0]) { Some(v) => { return v; }, None => { return 1; } }
}
`, 4},
	// Pre-existing: the base is a struct field.
	{"struct-field-index", `
struct B { o: Option[i32][] }
function main(): i32 {
    var b: B = B { o: [Some(5), None] };
    match (b.o[0]) { Some(v) => { return v; }, None => { return 1; } }
}
`, 5},
	// The None arm still has to be reachable through the recovered type.
	{"nested-index-none", `
function main(): i32 {
    var aoa: Option[i32][][] = [[None, Some(4)]];
    match (aoa[0][0]) { Some(v) => { return v; }, None => { return 6; } }
}
`, 6},
}

// TestSelfHostOptIndexRecoveryIRX86_64 asserts each shape lowers on the IR path
// — proven by running the driver under FERN_STRICT_IR, where a bail is exit 3 —
// and still produces the interpreter's answer.
func TestSelfHostOptIndexRecoveryIRX86_64(t *testing.T) {
	gcc, runner, driverBin := strictIRDriver(t)
	dir := filepath.Dir(driverBin)

	for _, tc := range optIndexRecoveryCases {
		t.Run(tc.name, func(t *testing.T) {
			asm, stderr, code := runDriver(t, runner, driverBin, []byte(tc.src), true)
			if code != 0 || len(asm) == 0 {
				t.Fatalf("%s did not lower on the IR path (exit %d):\n%s", tc.name, code, stderr)
			}
			progBin := buildBin(t, gcc, dir, "optidx_"+tc.name, string(asm))
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(progBin)
			} else {
				cmd = exec.Command(runner[0], append(append([]string{}, runner[1:]...), progBin)...)
			}
			_ = cmd.Run()
			if got := cmd.ProcessState.ExitCode(); got != tc.want {
				t.Errorf("%s exited %d, want %d", tc.name, got, tc.want)
			}
		})
	}
}

// TestSelfHostOptIndexRecoveryIRWasm runs the same cases through the wasm IR
// backend, which shares the resolver.
func TestSelfHostOptIndexRecoveryIRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host opt-index-recovery wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	for _, name := range []string{
		"util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern",
		"ir.fern", "irlower.fern", "irverify.fern", "irverifystack.fern", "irverifygate.fern", "asm_ir.fern", "wasm_ir.fern", "wasm_ir_run.fern",
	} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")

	for _, tc := range optIndexRecoveryCases {
		t.Run(tc.name, func(t *testing.T) {
			wat, stderr, code := runDriver(t, runner, driverBin, []byte(tc.src), true, "-ir")
			if code != 0 || len(wat) == 0 {
				t.Fatalf("%s did not lower on the wasm IR path (exit %d):\n%s", tc.name, code, stderr)
			}
			watFile := filepath.Join(dir, "opt_index_recovery_prog.wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			run := exec.Command("wasmtime", "run", watFile)
			_ = run.Run()
			if run.ProcessState == nil || !run.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q", tc.name)
			}
			if got := run.ProcessState.ExitCode(); got != tc.want {
				t.Errorf("opt-index-recovery wasm IR %q = %d, want %d", tc.name, got, tc.want)
			}
		})
	}
}
