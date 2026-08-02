package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// tupleReturnMoveProg is the #4598 reproducer: a `(St[], Par)` tuple return
// under cursor threading. Before the fix, the exit dec-sweep freed `body` /
// `r_body` right after the tuple box captured the buffer pointer
// (returned_moved_arr_slots had no ExprTuple arm, so a tuple-returned array
// local was not kept from the sweep the way an enum-payload move (#3720) is) —
// a use-after-free that stayed latent until a later same-size allocation
// recycled the freed block. The junk-allocation loop forces that recycling
// deterministically: pre-fix this program segfaulted; the parser.fern
// *Result→tuple migration (#4406) hit the same corruption via Par
// re-allocation in parse_block.
const tupleReturnMoveProg = `struct Par { toks: i32[], pos: i32 }
struct SA { v: i32 }
struct SB { name: string }
type St = SA | SB;

function (p: Par) advance(): Par {
    return Par { toks: p.toks, pos: p.pos + 1 };
}

function parse_sts(p0: Par): (St[], Par) {
    var body: St[] = [];
    var p: Par = p0;
    while (p.pos < 3) {
        var st: St = SA { v: p.pos };
        body = body.append(st);
        p = p.advance();
    }
    return (body, p);
}

function parse_block(p0: Par): (St[], Par) {
    var p: Par = p0;
    var (r_body, r_p) = parse_sts(p);
    p = r_p;
    if (p.pos < 100) { p = p.advance(); }
    var k: i32 = 0;
    var junk_total: i32 = 0;
    while (k < 8) {
        var junk: i32[] = [7777777, 7777777, 7777777, 7777777];
        junk_total = junk_total + junk[0];
        k = k + 1;
    }
    if (junk_total < 0) { return (r_body, p); }
    return (r_body, p);
}

function main(): i32 {
    var toks: i32[] = [1, 2, 3, 4];
    var p: Par = Par { toks: toks, pos: 0 };
    var (b, p2) = parse_block(p);
    var sum: i32 = 0;
    var i: i32 = 0;
    while (i < b.len()) {
        match (b[i]) {
            SA(sa) => { sum = sum + sa.v; },
            SB(_) => {}
        }
        i = i + 1;
    }
    return b.len() * 10 + sum + p2.pos;
}
`

// want: len(b)*10 + sum(0+1+2) + pos(4) = 30 + 3 + 4.
const tupleReturnMoveWant = 37

// TestSelfHostTupleReturnArrayElemMove pins the x86-64 IR-path move-on-return
// of an ARRAY tuple element (#4598): `return (body, p)` must keep `body` from
// the exit dec-sweep — the buffer moves into the (leak-mode) tuple box exactly
// like an enum payload (#3720). This is the gate for the #4406 parser.fern
// *Result→tuple migration, whose parse_block corruption reduced to this shape.
func TestSelfHostTupleReturnArrayElemMove(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	for _, name := range []string{"util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern", "ir.fern", "irlower.fern", "asm_ir.fern", "asm_arm64_ir.fern", "asm_ir_run.fern"} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	var cmd *exec.Cmd
	if len(runner) == 0 {
		cmd = exec.Command(driverBin, "-ir")
	} else {
		cmd = exec.Command(runner[0], append(append(append([]string{}, runner[1:]...), driverBin), "-ir")...)
	}
	cmd.Stdin = bytes.NewReader([]byte(tupleReturnMoveProg))
	asm, err := cmd.Output()
	if err != nil || len(asm) == 0 {
		t.Fatalf("driver failed: %v", err)
	}
	// The guard is only meaningful on the IR path — assert the tuple-returning
	// functions actually lowered there (an AST bail would pass vacuously).
	for _, fn := range []string{".Lir_parse_sts", ".Lir_parse_block"} {
		if !strings.Contains(string(asm), fn) {
			t.Fatalf("%s not on the IR path (no %s label) — the move-on-return guard is not being exercised", strings.TrimPrefix(fn, ".Lir_"), fn)
		}
	}
	progBin := buildBin(t, gcc, dir, "tuple_move_prog", string(asm))
	var run *exec.Cmd
	if len(runner) == 0 {
		run = exec.Command(progBin)
	} else {
		run = exec.Command(runner[0], append(runner[1:], progBin)...)
	}
	_ = run.Run()
	if code := run.ProcessState.ExitCode(); code != tupleReturnMoveWant {
		t.Errorf("tuple-return move program exited %d, want %d (pre-#4598-fix this segfaulted: the swept r_body buffer was recycled by the junk allocations)", code, tupleReturnMoveWant)
	}
}

// TestSelfHostTupleReturnArrayElemMoveWasm is the wasm-IR mirror — the lowering
// (irlower.fern) is shared, so the same move-on-return keep must hold in the
// WAT emit.
func TestSelfHostTupleReturnArrayElemMoveWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host tuple-return wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	for _, name := range []string{
		"util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern",
		"ir.fern", "irlower.fern", "asm_ir.fern", "wasm_ir.fern", "wasm_ir_run.fern",
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

	var cmd *exec.Cmd
	if len(runner) == 0 {
		cmd = exec.Command(driverBin, "-ir")
	} else {
		cmd = exec.Command(runner[0], append(append(append([]string{}, runner[1:]...), driverBin), "-ir")...)
	}
	cmd.Stdin = bytes.NewReader([]byte(tupleReturnMoveProg))
	wat, err := cmd.Output()
	if err != nil || len(wat) == 0 {
		t.Fatalf("driver failed: %v", err)
	}
	watFile := filepath.Join(dir, "tuple_move_prog.wat")
	if err := os.WriteFile(watFile, wat, 0o644); err != nil {
		t.Fatalf("write wat: %v", err)
	}
	run := exec.Command("wasmtime", "run", watFile)
	run.Dir = dir
	_ = run.Run()
	if run.ProcessState == nil || !run.ProcessState.Exited() {
		t.Fatalf("wasmtime did not exit normally")
	}
	if code := run.ProcessState.ExitCode(); code != tupleReturnMoveWant {
		t.Errorf("tuple-return move wasm program exited %d, want %d", code, tupleReturnMoveWant)
	}
}
