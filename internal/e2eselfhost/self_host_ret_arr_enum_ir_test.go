package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// retArrEnumIRCases pin the move-on-return of an ARRAY-PAYLOAD ENUM built from a
// LOCAL array on the self-host IR path (#3720). A function `return Many(a)` over a
// local `var a: Tok[] = [...]` moves `a`'s buffer into the (leaking) enum box, but
// the lowerer's exit dec-sweep freed `a` anyway — a use-after-free that surfaced
// as a SIGSEGV only once the freed buffer was recycled by a later allocation (e.g.
// a second `append` that grew the holding array). The fix excludes every array
// local the returned value moves out — including one nested under a returned enum
// or struct-enum field (returned_moved_arr_slots) — from the sweep, so the buffer
// leaks WITH the box per the IR leak-mode invariant. Each case is routing-pinned to
// "ir" and value-pinned against the native interpreter oracle (interp == native).
var retArrEnumIRCases = []struct {
	name string
	src  string
	want int
}{
	// The minimal trigger: mk() returns Many(a) over a local array; the result is
	// appended into another array, then a SECOND append grows that array (recycling
	// the prematurely-freed buffer). count() walks the nested tree → a, b, c = 3.
	{"mk_append_grow", `enum Tok { One(i32), Many(Tok[]) }
function mk(): Tok { var a: Tok[] = [One(97), One(98)]; return Many(a); }
function count(t: Tok): i32 { match (t) { One(_) => { return 1; }, Many(xs) => { var c: i32 = 0; var k: i32 = 0; while (k < xs.len()) { c = c + count(xs[k]); k = k + 1; } return c; } } }
function main(): i32 { var items: Tok[] = []; items = items.append(mk()); items = items.append(One(99)); return count(items[0]) + count(items[1]); }`, 3},
	// Wrap the holding array in another Many before counting — the same shape the
	// std/regex grouping parser hits.
	{"mk_append_wrap", `enum Tok { One(i32), Many(Tok[]) }
function mk(): Tok { var a: Tok[] = [One(97), One(98)]; return Many(a); }
function count(t: Tok): i32 { match (t) { One(_) => { return 1; }, Many(xs) => { var c: i32 = 0; var k: i32 = 0; while (k < xs.len()) { c = c + count(xs[k]); k = k + 1; } return c; } } }
function main(): i32 { var items: Tok[] = []; items = items.append(mk()); items = items.append(One(99)); return count(Many(items)); }`, 3},
	// The original issue repro: mutually-recursive parse_one / parse_many returning
	// a struct whose field is an array-payload enum, group-first ("(ab)c" → a,b,c).
	{"recursive_parser", `enum Tok { One(i32), Many(Tok[]) }
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
function main(): i32 { var r: PS = parse_many("(ab)c", 0); return count(r.node); }`, 3},
	// A struct literal whose enum field is built from a local array, returned and
	// then consumed across an allocation — exercises the struct-lit field descent.
	{"struct_field_enum", `enum Tok { One(i32), Many(Tok[]) }
struct W { node: Tok }
function mk(): W { var a: Tok[] = [One(1), One(2), One(3)]; return W { node: Many(a) }; }
function count(t: Tok): i32 { match (t) { One(_) => { return 1; }, Many(xs) => { var c: i32 = 0; var k: i32 = 0; while (k < xs.len()) { c = c + count(xs[k]); k = k + 1; } return c; } } }
function main(): i32 { var w: W = mk(); var pad: Tok[] = []; pad = pad.append(One(9)); pad = pad.append(One(9)); return count(w.node) + pad.len(); }`, 5},
}

// TestSelfHostRetArrEnumIRX86_64 routes each case through the self-host x86-64 IR
// driver (pinned to "ir") and asserts the native-oracle exit code.
func TestSelfHostRetArrEnumIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "asm_run.fern", "asm_pathprobe_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")
	probeBin := buildSelfHostBin(t, gcc, dir, "asm_pathprobe_run.fern", "pathprobe")

	for _, tc := range retArrEnumIRCases {
		t.Run(tc.name, func(t *testing.T) {
			src := []byte(tc.src + "\n")
			path := strings.TrimSpace(string(runCapture(t, gcc, runner, probeBin, src)))
			if path != "ir" {
				t.Fatalf("%s routed through %q path, want \"ir\"", tc.name, path)
			}
			asm := runCapture(t, gcc, runner, driverBin, src)
			if len(asm) == 0 {
				t.Fatal("self-host compiler emitted 0 bytes")
			}
			progBin := buildBin(t, gcc, dir, "ret_arr_enum_"+tc.name, string(asm))
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(progBin)
			} else {
				cmd = exec.Command(runner[0], append(runner[1:], progBin)...)
			}
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("ret-arr-enum %q exited %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}

// TestSelfHostRetArrEnumWasmIR runs the same cases through the wasm IR backend.
func TestSelfHostRetArrEnumWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host ret-arr-enum wasm IR e2e")
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

	for _, tc := range retArrEnumIRCases {
		t.Run(tc.name, func(t *testing.T) {
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(driverBin, "-ir")
			} else {
				cmd = exec.Command(runner[0], append(append(append([]string{}, runner[1:]...), driverBin), "-ir")...)
			}
			cmd.Stdin = bytes.NewReader([]byte(tc.src + "\n"))
			wat, err := cmd.Output()
			if err != nil || len(wat) == 0 {
				t.Fatalf("driver failed for %q: %v", tc.name, err)
			}
			watFile := filepath.Join(dir, "ret_arr_enum_prog.wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			run := exec.Command("wasmtime", "run", watFile)
			_ = run.Run()
			if run.ProcessState == nil || !run.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.name, wat)
			}
			if code := run.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("ret-arr-enum wasm IR %q = %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}
