package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// paramAppendReclaimCases pin the borrow boundary of the self-append reclaim
// (#5717 / #5713). `a = a.append(v)` on a sole-owner target grows the buffer
// and, on a grow-realloc, reclaims the pre-grow block (`arr_push_owned`)
// instead of leaking it. The sole-owner test, `is_aliased_name`, only sees
// aliases created INSIDE the function, so it also fired when the target was a
// borrowed PARAM — whose buffer the CALLER owns and still references. The fix
// adds the explicit `slot < n_params` borrow boundary.
//
// #5717 landed the fix WITHOUT a regression test, on the finding that the
// symptom could not be reproduced synthetically ("several shapes modelling the
// aliasing exactly still exit 0 unfixed — the corruption needs the real
// allocation order"), leaving the whole-checker corpus
// (`TestSelfHostCheckerCodes*` / `…Differential*`) as the only pin. These cases
// show it IS synthetically reproducible, and name the missing ingredient.
//
// Modelling the aliasing is not enough, because freeing the caller's buffer is
// harmless on its own — no rc underflow, the caller's count genuinely does
// reach zero, and nothing has yet been handed the recycled block. The symptom
// needs the callee to ALLOCATE AGAIN after the append and for that allocation
// to be the value it RETURNS: then the returned object sits in the block the
// param append released, and the caller's exit-sweep dec of its own stale
// pointer frees the value it was just handed. `caller-reads-seed-after-callee-append`
// below is the aliasing-only shape and passes even unfixed — it is kept
// precisely to document that distinction; the other two carry the teeth.
//
// That is the live shape: `slc_walk` / `e065_stmts` self-append their
// `localarr` / `sbacked` param while building the `Diag[]` they return, so
// `slice_escape_diags` / `e065_diags` freed their own return value on the way
// out and the diagnostic codes read back as recycled memory (`E063` as ` E06`,
// `E065` as `)E06`).
//
// Verified to have teeth: reverting the `|| slot < s.n_params` gate turns
// `param-append-recycled-by-return` and `param-append-i32-recycled` red (both
// exit 3, want 4). Worth keeping alongside the corpus pin — it fails in
// seconds on a standalone program instead of via a multi-minute checker build
// reporting garbled diagnostic codes, and it covers wasm, which the
// x86-only checker corpus does not.
var paramAppendReclaimCases = []struct {
	name string
	main string
	want int
}{
	// The live shape: the callee self-appends a `string[]` param AND builds a
	// struct array it returns, so the returned buffer lands in the released
	// block. Reading the returned array's string fields must survive the
	// caller's exit sweep. 4 diagnostics, all readable.
	{"param-append-recycled-by-return", `
struct D { code: string, n: i32 }
function walk(acc: string[], n: i32): D[] {
    var out: D[] = [];
    var i: i32 = 0;
    while (i < n) {
        acc = acc.append("xx");
        out = out.append(D { code: "E065", n: i });
        i = i + 1;
    }
    return out;
}
function entry(n: i32): D[] {
    var seed: string[] = [];
    return walk(seed, n);
}
function main(): i32 {
    var t: i32 = 0;
    var k: i32 = 0;
    while (k < 30) {
        var d: D[] = entry(4);
        t = (t + d.len()) % 100;
        var junk: string[] = ["aaaa", "bbbb"];
        t = (t + junk.len()) % 100;
        k = k + 1;
    }
    var ds: D[] = entry(4);
    var hit: i32 = 0;
    var j: i32 = 0;
    while (j < ds.len()) { if (ds[j].code == "E065") { hit = hit + 1; } j = j + 1; }
    return hit;
}
`, 4},
	// Element-kind agnostic: the reclaim is on the buffer, so an `i32[]` param
	// append corrupts the same way.
	{"param-append-i32-recycled", `
struct D { code: string, n: i32 }
function walk(acc: i32[], n: i32): D[] {
    var out: D[] = [];
    var i: i32 = 0;
    while (i < n) {
        acc = acc.append(i);
        out = out.append(D { code: "E065", n: i });
        i = i + 1;
    }
    return out;
}
function entry(n: i32): D[] {
    var seed: i32[] = [];
    return walk(seed, n);
}
function main(): i32 {
    var t: i32 = 0;
    var k: i32 = 0;
    while (k < 30) {
        var d: D[] = entry(4);
        t = (t + d.len()) % 100;
        var junk: i32[] = [1, 2];
        t = (t + junk.len()) % 100;
        k = k + 1;
    }
    var ds: D[] = entry(4);
    var hit: i32 = 0;
    var j: i32 = 0;
    while (j < ds.len()) { if (ds[j].code == "E065") { hit = hit + 1; } j = j + 1; }
    return hit;
}
`, 4},
	// The direct half: the CALLER reads its own seed array after the callee
	// self-appended it. The caller's buffer must still hold its own contents.
	// walk returns 7, the seed still reads "keep" -> 8.
	{"caller-reads-seed-after-callee-append", `
function walk(acc: string[], n: i32): i32 {
    var i: i32 = 0;
    while (i < n) { acc = acc.append("xx"); i = i + 1; }
    return acc.len();
}
function entry(): i32 {
    var seed: string[] = [];
    seed = seed.append("keep");
    var r: i32 = walk(seed, 6);
    var hit: i32 = 0;
    if (seed[0] == "keep") { hit = 1; }
    return r + hit;
}
function main(): i32 {
    var t: i32 = 0;
    var k: i32 = 0;
    while (k < 30) {
        t = (t + entry()) % 100;
        var junk: string[] = ["aaaa", "bbbb"];
        t = (t + junk.len()) % 100;
        k = k + 1;
    }
    return entry();
}
`, 8},
}

// TestSelfHostParamAppendReclaimIRX86_64 routes each case through the
// self-hosted x86-64 IR driver, with the routing pinned to the "ir" path.
func TestSelfHostParamAppendReclaimIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "asm_run.fern", "asm_pathprobe_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")
	probeBin := buildSelfHostBin(t, gcc, dir, "asm_pathprobe_run.fern", "pathprobe")

	for _, tc := range paramAppendReclaimCases {
		t.Run(tc.name, func(t *testing.T) {
			src := []byte(tc.main)
			path := strings.TrimSpace(string(runCapture(t, gcc, runner, probeBin, src)))
			if path != "ir" {
				t.Fatalf("%s routed through %q path, want \"ir\"", tc.name, path)
			}
			asm := runCapture(t, gcc, runner, driverBin, src)
			if len(asm) == 0 {
				t.Fatal("self-host compiler emitted 0 bytes")
			}
			progBin := buildBin(t, gcc, dir, tc.name, string(asm))
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(progBin)
			} else {
				cmd = exec.Command(runner[0], append(runner[1:], progBin)...)
			}
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}

// TestSelfHostParamAppendReclaimIRWasm runs the same cases through the wasm IR
// backend.
func TestSelfHostParamAppendReclaimIRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host param-append-reclaim wasm IR e2e")
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

	for _, tc := range paramAppendReclaimCases {
		t.Run(tc.name, func(t *testing.T) {
			src := []byte(tc.main)
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
			watFile := filepath.Join(dir, "param_append_reclaim_prog.wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			run := exec.Command("wasmtime", "run", watFile)
			_ = run.Run()
			if run.ProcessState == nil || !run.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.name, wat)
			}
			if code := run.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("param-append-reclaim wasm IR %q = %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}
