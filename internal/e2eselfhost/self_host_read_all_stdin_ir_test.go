package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// `read_all_stdin()` on the self-host IR path (#5623).
//
// irlower intercepts the neighbouring stdin builtins — print / read_int /
// stdin — but had no case for read_all_stdin, so emit_module_ir_gated saw a
// call_direct to a symbol that is not a __fern_* helper, not a C call and not a
// module function, and bailed the WHOLE module to the AST emitter. That matters
// beyond optimisation quality: per #5622 the AST emitter silently drops i32
// wrapping, so every stdin-reading program was compiled by an emitter with a
// known correctness defect.
//
// The op lowers to each register backend's __fern_read_all_stdin
// (__fern_read_all_stdin_rc on arm64). Both build the result with
// __fern_str_box rather than the AST path's bare __fern_alloc(16): on the IR
// path this is a reclaimable string the program holds directly, and
// __fern_str_free reads the rc word at box-8, which a headerless box does not
// have. wasm defers it (see wasm_ir_deferrals_ok) — it has stdin, so that is a
// not-yet-written runtime rather than an impossibility.
//
// TestSelfHostReadAllStdinX86_64 already pins the byte counts through the same
// driver; these cases add the routing assertion and the shapes that exercise
// the string beyond `.len()`.
const readAllStdinIRProg = `function consume(s: string): i32 { return s.len(); }
function main(): i32 {
    var s: string = read_all_stdin();
    if (consume(s) != s.len()) { return 1; }
    var t: string = s + "!";
    if (t.len() != s.len() + 1) { return 2; }
    var ls: string[] = s.lines();
    if (ls.len() < 1) { return 3; }
    if (s.len() == 0) { return 40; }
    return 42;
}
`

// A 3 MiB body crosses the runtime's 1 MiB per-read chunk, so this pins the
// append loop rather than just the single-read case.
const readAllStdinIRBigProg = `function main(): i32 {
    var s: string = read_all_stdin();
    if (s.len() == 3145728) { return 7; }
    return 5;
}
`

func bigStdinInput() []byte { return bytes.Repeat([]byte("x"), 3145728) }

// TestSelfHostReadAllStdinIRRoutingX86_64 pins that a read_all_stdin program
// now takes the IR path rather than falling back to the AST emitter. The
// path-probe driver resolves no imports, which is fine here — the program has
// none.
func TestSelfHostReadAllStdinIRRoutingX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	for _, name := range []string{"asm_pathprobe_run.fern"} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	probeBin := buildSelfHostBin(t, gcc, dir, "asm_pathprobe_run.fern", "pathprobe")

	src := []byte("function main(): i32 { var s: string = read_all_stdin(); return s.len(); }\n")
	if path := strings.TrimSpace(string(runCapture(t, gcc, runner, probeBin, src))); path != "ir" {
		t.Errorf("read_all_stdin routed through %q path, want \"ir\"", path)
	}
}

// runReadAllStdinIR compiles `src` with the self-host modload driver for the
// given target and runs it with `in` on stdin, returning the exit code.
func runReadAllStdinIR(t *testing.T, target, src string, in []byte) int {
	t.Helper()
	var runner, runPrefix, extra []string
	var driverBin, linkGcc string
	// The runtime symbol that only the IR path emits. On arm64 the IR body is a
	// distinct symbol (the AST path keeps its headerless-box __fern_read_all_stdin),
	// so its presence alone proves the routing; on x86-64 both paths share the
	// name, which is why TestSelfHostReadAllStdinIRRoutingX86_64 asserts the
	// routing separately.
	wantSym := "__fern_read_all_stdin"
	if target == "arm64" {
		var qemu string
		_, runner, driverBin = buildModloadArm64DriverX86(t)
		linkGcc, qemu = arm64Tooling(t)
		if qemu != "" {
			runPrefix = []string{qemu}
		}
		extra = []string{"-target", "arm64"}
		wantSym = "__fern_read_all_stdin_rc:"
	} else {
		linkGcc, runner, driverBin = buildModloadDriverX86(t)
		runPrefix = runner
	}

	progAsm, progDir := compileSourceModload(t, runner, driverBin, src, extra...)
	if len(progAsm) == 0 {
		t.Fatal("self-host emitter produced 0 bytes")
	}
	if !strings.Contains(progAsm, wantSym) {
		t.Fatalf("emitted asm has no %s", wantSym)
	}
	progBin := buildBin(t, linkGcc, progDir, "ras_ir", progAsm)

	var cmd *exec.Cmd
	if len(runPrefix) == 0 {
		cmd = exec.Command(progBin)
	} else {
		cmd = exec.Command(runPrefix[0], append(append([]string{}, runPrefix[1:]...), progBin)...)
	}
	cmd.Stdin = bytes.NewReader(in)
	_, _ = cmd.CombinedOutput()
	if cmd.ProcessState == nil || !cmd.ProcessState.Exited() {
		t.Fatal("program did not exit normally")
	}
	return cmd.ProcessState.ExitCode()
}

func TestSelfHostReadAllStdinIRX86_64(t *testing.T) {
	if got := runReadAllStdinIR(t, "x86-64", readAllStdinIRProg, []byte("alpha\nbeta\n")); got != 42 {
		t.Errorf("read_all_stdin IR x86-64 = %d, want 42", got)
	}
	if got := runReadAllStdinIR(t, "x86-64", readAllStdinIRBigProg, bigStdinInput()); got != 7 {
		t.Errorf("read_all_stdin IR x86-64 3 MiB = %d, want 7", got)
	}
}

func TestSelfHostReadAllStdinIRArm64(t *testing.T) {
	if got := runReadAllStdinIR(t, "arm64", readAllStdinIRProg, []byte("alpha\nbeta\n")); got != 42 {
		t.Errorf("read_all_stdin IR arm64 = %d, want 42", got)
	}
	if got := runReadAllStdinIR(t, "arm64", readAllStdinIRBigProg, bigStdinInput()); got != 7 {
		t.Errorf("read_all_stdin IR arm64 3 MiB = %d, want 7", got)
	}
}
