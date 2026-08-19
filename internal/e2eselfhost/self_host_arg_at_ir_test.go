package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// `arg_at(i)` on the self-host IR path (#3457).
//
// irlower lowered `args()` and (since the args().len() desugar) `args_count()`,
// but had no case for `arg_at`, so a program reading one argument made the
// WHOLE module IR-ineligible.
//
// arg_at deliberately does NOT reuse the args().len() desugar that args_count
// got: args() materialises the whole argv as a fresh string[] per call, so
// `args()[i]` in a loop would allocate all of argv per iteration. It gets a
// real op (kind 210) instead, which is O(1).
//
// The register backends call __fern_arg_at_rc rather than the existing
// __fern_arg_at. The latter builds a headerless __fern_alloc(16) box, which is
// fine for the elements of the string[] __fern_args builds (the array owns them
// and they are never str-freed individually) but wrong for a bare `arg_at(i)`:
// on the IR path that is a reclaimable string the program holds directly, and
// __fern_str_free reads the rc word at box-8, which a headerless box does not
// have. Same split as __fern_read_all_stdin / __fern_read_all_stdin_rc. wasm
// gets $__fern_arg_at, sharing the wasi args_sizes_get / args_get imports with
// $__fern_args but copying only the one entry.

// argAtIRProg exercises the result as a real string, not just its length: a
// concat, a comparison, and a loop that calls arg_at repeatedly (the shape the
// O(1) op exists for). argv[0] is the program name on every backend, so the
// arguments under test start at index 1.
const argAtIRProg = `function main(): i32 {
    var n: i32 = args_count();
    if (n != 3) { return 1; }
    var a: string = arg_at(1);
    var b: string = arg_at(2);
    if (a != "alpha") { return 2; }
    if (b != "beta") { return 3; }
    var j: string = a + "-" + b;
    if (j.len() != 10) { return 4; }
    var total: i32 = 0;
    var i: i32 = 1;
    while (i < n) {
        var s: string = arg_at(i);
        total = total + s.len();
        i = i + 1;
    }
    if (total != 9) { return 5; }
    return 42;
}
`

// argAtChurnProg calls arg_at in a long loop. Under the headerless box the
// string reclaim path would read an rc word out of whatever precedes the
// allocation; this runs enough iterations that a corrupted freelist shows up as
// a crash or a wrong answer rather than passing by luck.
const argAtChurnProg = `function main(): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 20000) {
        var s: string = arg_at(1);
        acc = (acc + s.len()) % 251;
        i = i + 1;
    }
    if (acc != 20000 * 5 % 251) { return 1; }
    return 7;
}
`

// TestSelfHostArgAtIRRoutingX86_64 pins that an arg_at program now takes the IR
// path rather than bailing. The path-probe driver
// resolves no imports, which is fine here — the program has none.
func TestSelfHostArgAtIRRoutingX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	src, err := os.ReadFile("../../examples/self_host/asm_pathprobe_run.fern")
	if err != nil {
		t.Fatalf("read asm_pathprobe_run.fern: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "asm_pathprobe_run.fern"), src, 0o644); err != nil {
		t.Fatalf("write asm_pathprobe_run.fern: %v", err)
	}
	probeBin := buildSelfHostBin(t, gcc, dir, "asm_pathprobe_run.fern", "pathprobe")

	prog := []byte("function main(): i32 { var s: string = arg_at(0); return s.len(); }\n")
	if path := strings.TrimSpace(string(runCapture(t, gcc, runner, probeBin, prog))); path != "ir" {
		t.Errorf("arg_at routed through %q path, want \"ir\"", path)
	}
}

// runArgAtIR compiles `src` with the self-host modload driver for the given
// target and runs it with `argv` appended, returning the exit code.
func runArgAtIR(t *testing.T, target, src string, argv ...string) int {
	t.Helper()
	var runner, runPrefix, extra []string
	var driverBin, linkGcc string
	// The runtime symbol only the IR path emits: the AST path keeps the
	// headerless __fern_arg_at, so the _rc sibling's presence proves the routing
	// on both register backends.
	const wantSym = "__fern_arg_at_rc"
	if target == "arm64-linux" {
		var qemu string
		_, runner, driverBin = buildModloadArm64DriverX86(t)
		linkGcc, qemu = arm64Tooling(t)
		if qemu != "" {
			runPrefix = []string{qemu}
		}
		extra = []string{"-target", "arm64-linux"}
	} else {
		linkGcc, runner, driverBin = buildModloadDriverX86(t)
		runPrefix = runner
	}

	progAsm, progDir := compileSourceModload(t, runner, driverBin, src, extra...)
	if len(progAsm) == 0 {
		t.Fatal("self-host emitter produced 0 bytes")
	}
	if !strings.Contains(progAsm, wantSym) {
		t.Fatalf("emitted asm has no %s — arg_at did not take the IR path", wantSym)
	}
	progBin := buildBin(t, linkGcc, progDir, "arg_at_ir", progAsm)

	args := append(append([]string{}, runPrefix...), progBin)
	args = append(args, argv...)
	cmd := exec.Command(args[0], args[1:]...)
	_, _ = cmd.CombinedOutput()
	if cmd.ProcessState == nil || !cmd.ProcessState.Exited() {
		t.Fatal("program did not exit normally")
	}
	return cmd.ProcessState.ExitCode()
}

func TestSelfHostArgAtIRX86_64(t *testing.T) {
	if got := runArgAtIR(t, "x86-64-linux", argAtIRProg, "alpha", "beta"); got != 42 {
		t.Errorf("arg_at IR x86-64 = %d, want 42", got)
	}
	if got := runArgAtIR(t, "x86-64-linux", argAtChurnProg, "alpha"); got != 7 {
		t.Errorf("arg_at IR x86-64 churn = %d, want 7", got)
	}
}

func TestSelfHostArgAtIRArm64(t *testing.T) {
	if got := runArgAtIR(t, "arm64-linux", argAtIRProg, "alpha", "beta"); got != 42 {
		t.Errorf("arg_at IR arm64 = %d, want 42", got)
	}
	if got := runArgAtIR(t, "arm64-linux", argAtChurnProg, "alpha"); got != 7 {
		t.Errorf("arg_at IR arm64 churn = %d, want 7", got)
	}
}

// TestSelfHostArgAtIRWasm runs the same shapes through the self-hosted wasm IR
// driver. A wasm string is a [len:i32][bytes] block rather than the register
// backends' {data,len} box, but it is rc-headered just the same — $__fern_str_box
// prepends rc+bsz and returns base+8 — so $__fern_arg_at must BOX its result
// rather than hand back a raw __fern_alloc block, which is what it used to do.
// Arguments reach the module through
// wasmtime's `--` passthrough, which is what wasi args_get reads.
func TestSelfHostArgAtIRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host arg_at wasm IR e2e")
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

	for _, tc := range []struct {
		name string
		src  string
		argv []string
		want int
	}{
		{"shapes", argAtIRProg, []string{"alpha", "beta"}, 42},
		{"churn", argAtChurnProg, []string{"alpha"}, 7},
		// Out of range yields the empty string rather than reading past argv.
		{"oob", "function main(): i32 { var s: string = arg_at(99); return s.len(); }\n", nil, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(driverBin, "-ir")
			} else {
				cmd = exec.Command(runner[0], append(append(append([]string{}, runner[1:]...), driverBin), "-ir")...)
			}
			cmd.Stdin = bytes.NewReader([]byte(tc.src))
			wat, err := cmd.Output()
			if err != nil || len(wat) == 0 {
				t.Fatalf("wasm IR driver failed: %v", err)
			}
			if !bytes.Contains(wat, []byte("$__fern_arg_at")) {
				t.Fatal("emitted wat has no $__fern_arg_at helper")
			}
			watFile := filepath.Join(dir, "arg_at_"+tc.name+".wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			runArgs := append([]string{"run", watFile}, tc.argv...)
			run := exec.Command("wasmtime", runArgs...)
			_ = run.Run()
			if run.ProcessState == nil || !run.ProcessState.Exited() {
				t.Fatal("wasmtime did not exit normally")
			}
			if code := run.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("arg_at wasm IR %q = %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}
