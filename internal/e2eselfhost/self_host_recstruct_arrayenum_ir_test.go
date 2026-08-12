package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// recStructArrayEnumPrelude is the #3720 repro frame: a pair of mutually-
// recursive functions that build and return a struct (`PS`) whose field is an
// enum (`Tok`) with an array payload (`Many(Tok[])`). Returning the nested
// array-payload enum through the struct from the re-entrant recursive call
// segfaulted on the self-host x86-64 / wasm IR path — the enum constructor
// stored the `items` array buffer into the box without a Perceus retain, so the
// exit dec-sweep freed it out from under the returned box (a UAF that `count`
// then walked into unbounded recursion). `fern -interp` and the native compiler
// were correct. Each case below varies only main()'s input; `count` returns the
// number of leaf `One` tokens, oracle-checked against the interpreter.
const recStructArrayEnumPrelude = `enum Tok { One(i32), Many(Tok[]) }
struct PS { node: Tok, pos: i32 }

function parse_one(s: string, i: i32): PS {
    if (s[i] == 40) {
        var inner: PS = parse_many(s, i + 1);
        var pos: i32 = inner.pos;
        if (pos < s.len() && s[pos] == 41) { pos = pos + 1; }
        return PS { node: inner.node, pos: pos };
    }
    return PS { node: One(s[i] as i32), pos: i + 1 };
}
function parse_many(s: string, i: i32): PS {
    var items: Tok[] = [];
    var pos: i32 = i;
    while (pos < s.len() && s[pos] != 41) {
        var p: PS = parse_one(s, pos);
        items = items.append(p.node);
        pos = p.pos;
    }
    if (items.len() == 1) { return PS { node: items[0], pos: pos }; }
    return PS { node: Many(items), pos: pos };
}
function count(t: Tok): i32 {
    match (t) {
        One(_) => { return 1; },
        Many(xs) => { var c: i32 = 0; var k: i32 = 0; while (k < xs.len()) { c = c + count(xs[k]); k = k + 1; } return c; }
    }
}
`

var recStructArrayEnumCases = []struct {
	name string
	main string
}{
	// Group first, then a trailing atom: leaves a,b,c -> 3 (the canonical crash).
	{"group-first", `function main(): i32 { var r: PS = parse_many("(ab)c", 0); return count(r.node); }`},
	// Group only, inner is a 2-element Many: a,b -> 2.
	{"group-only", `function main(): i32 { var r: PS = parse_many("(ab)", 0); return count(r.node); }`},
	// Single-element group (inner is a bare One, no Many wrapper): a -> 1.
	{"single-group", `function main(): i32 { var r: PS = parse_many("(a)", 0); return count(r.node); }`},
	// Group not first: z,a,b,c -> 4 (was fine before the fix; regression guard).
	{"group-not-first", `function main(): i32 { var r: PS = parse_many("z(ab)c", 0); return count(r.node); }`},
	// No group at all: a,b,c -> 3.
	{"no-group", `function main(): i32 { var r: PS = parse_many("abc", 0); return count(r.node); }`},
	// Nested groups: a,b,c -> 3 (deeper recursive struct returns).
	{"nested-group", `function main(): i32 { var r: PS = parse_many("((ab)c)", 0); return count(r.node); }`},
}

// TestSelfHostRecStructArrayEnumIRX86_64 pins the #3720 recursive
// struct-with-array-payload-enum return shapes to the self-host x86-64 IR path,
// oracle-checked against the interpreter, with routing pinned to "ir".
func TestSelfHostRecStructArrayEnumIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "asm_run.fern", "asm_pathprobe_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")
	probeBin := buildSelfHostBin(t, gcc, dir, "asm_pathprobe_run.fern", "pathprobe")

	for _, tc := range recStructArrayEnumCases {
		t.Run(tc.name, func(t *testing.T) {
			src := []byte(recStructArrayEnumPrelude + tc.main + "\n")
			want := interpExit(t, interpBin, string(src))
			path := strings.TrimSpace(string(runCapture(t, gcc, runner, probeBin, src)))
			if path != "ir" {
				t.Fatalf("%s routed through %q path, want \"ir\"", tc.name, path)
			}
			asm := runCapture(t, gcc, runner, driverBin, src)
			if len(asm) == 0 {
				t.Fatal("self-host compiler emitted 0 bytes")
			}
			progBin := buildBin(t, gcc, dir, "recstruct_"+tc.name, string(asm))
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(progBin)
			} else {
				cmd = exec.Command(runner[0], append(runner[1:], progBin)...)
			}
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != want {
				t.Errorf("%s exited %d, want %d (interp oracle)", tc.name, code, want)
			}
		})
	}
}

// TestSelfHostRecStructArrayEnumIRWasm runs the same #3720 cases through the
// wasm IR backend. The x86-64 RC fix (the enum-ctor Perceus alias-inc in
// irlower.fern) is shared by every IR backend, but the wasm path had a SEPARATE
// defect: a self-reassign append (`a = a.append(x)`) lowers to op_arr_push_owned,
// which on wasm maps to `call $__fern_arr_push` — yet wasm_ir_run only pulled the
// $__fern_arr_push helper in for op_arr_push, so a program whose appends are all
// self-reassigns (every #3720 parse loop) called an undefined function → an
// invalid module (wasmtime "unknown func", exit 1). Fixed by gating the helper on
// arr_push OR arr_push_owned.
func TestSelfHostRecStructArrayEnumIRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host recstruct-arrayenum wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
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

	for _, tc := range recStructArrayEnumCases {
		t.Run(tc.name, func(t *testing.T) {
			src := []byte(recStructArrayEnumPrelude + tc.main + "\n")
			want := interpExit(t, interpBin, string(src))
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(driverBin, "-ir")
			} else {
				cmd = exec.Command(runner[0], append(append(append([]string{}, runner[1:]...), driverBin), "-ir")...)
			}
			cmd.Stdin = bytes.NewReader(src)
			wat, err := cmd.Output()
			if err != nil || len(wat) == 0 {
				t.Fatalf("driver failed for %q: %v", tc.name, err)
			}
			watFile := filepath.Join(dir, "recstruct_arrayenum_"+tc.name+".wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			run := exec.Command("wasmtime", "run", watFile)
			_ = run.Run()
			if run.ProcessState == nil || !run.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.name, wat)
			}
			if code := run.ProcessState.ExitCode(); code != want {
				t.Errorf("recstruct-arrayenum wasm IR %q = %d, want %d (interp oracle)", tc.name, code, want)
			}
		})
	}
}
