package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Self-host IR coverage for the std/option + std/result combinators added
// alongside this test (map_or / is_some_and / or / and on Option; map_or /
// is_ok_and / is_err_and / or on Result). They're ordinary generic methods
// (#2692) — a small `match` — so they lower through the self-hosted IR path
// the same way the existing map / filter / and_then combinators do.
//
// The self-host stdin drivers don't resolve stdlib imports, so each case
// INLINES the combinator definition(s) under test (std/option imports core/cmp
// only vestigially — the combinators reference nothing from it). Every case is
// i32-only with scalar / non-callback combinators, routing-pinned to "ir", and
// oracle-checked against the interpreter (result kept <= 120, cf. #2908).
type optResultIRCase struct {
	name string
	src  string
}

var optResultIRCases = []optResultIRCase{
	{"opt-map_or", `
pub function (o: Option[T]) map_or[U](fallback: U, f: (T) => U): U {
    match (o) { Some(x) => { return f(x); }, None => { return fallback; } }
}
function inc(x: i32): i32 { return x + 1; }
function main(): i32 {
    var a: Option[i32] = Some(10);
    var n: Option[i32] = None;
    return a.map_or(0, inc) + n.map_or(7, inc);   // 11 + 7 = 18
}`},
	{"opt-or-and", `
pub function (o: Option[T]) or(other: Option[T]): Option[T] {
    match (o) { Some(x) => { return Some(x); }, None => { return other; } }
}
pub function (o: Option[T]) and[U](other: Option[U]): Option[U] {
    match (o) { Some(x) => { return other; }, None => { return None; } }
}
pub function (o: Option[T]) unwrap_or(fallback: T): T {
    match (o) { Some(x) => { return x; }, None => { return fallback; } }
}
function main(): i32 {
    var a: Option[i32] = Some(5);
    var n: Option[i32] = None;
    return n.or(Some(40)).unwrap_or(0) + a.and(Some(2)).unwrap_or(0);   // 40 + 2 = 42
}`},
	{"opt-is_some_and", `
pub function (o: Option[T]) is_some_and(pred: (T) => boolean): boolean {
    match (o) { Some(x) => { return pred(x); }, None => { return false; } }
}
function pos(x: i32): boolean { return x > 0; }
function main(): i32 {
    var a: Option[i32] = Some(7);
    var n: Option[i32] = None;
    var r: i32 = 0;
    if (a.is_some_and(pos)) { r = r + 100; }
    if (n.is_some_and(pos)) { r = r + 1; }    // None short-circuits to false
    return r;                                  // 100
}`},
	{"res-map_or-or", `
pub function (r: Result[T, E]) map_or[U](fallback: U, f: (T) => U): U {
    match (r) { Ok(x) => { return f(x); }, Err(e) => { return fallback; } }
}
pub function (r: Result[T, E]) or(other: Result[T, E]): Result[T, E] {
    match (r) { Ok(x) => { return Ok(x); }, Err(e) => { return other; } }
}
pub function (r: Result[T, E]) r_unwrap_or(fallback: T): T {
    match (r) { Ok(x) => { return x; }, Err(e) => { return fallback; } }
}
function inc(x: i32): i32 { return x + 1; }
function main(): i32 {
    var ok: Result[i32, i32] = Ok(20);
    var er: Result[i32, i32] = Err(3);
    var f: Result[i32, i32] = Ok(5);
    return ok.map_or(0, inc) + er.or(f).r_unwrap_or(0);   // 21 + 5 = 26
}`},
	{"res-is_ok_and-is_err_and", `
pub function (r: Result[T, E]) is_ok_and(pred: (T) => boolean): boolean {
    match (r) { Ok(x) => { return pred(x); }, Err(e) => { return false; } }
}
pub function (r: Result[T, E]) is_err_and(pred: (E) => boolean): boolean {
    match (r) { Ok(x) => { return false; }, Err(e) => { return pred(e); } }
}
function pos(x: i32): boolean { return x > 0; }
function main(): i32 {
    var ok: Result[i32, i32] = Ok(8);
    var er: Result[i32, i32] = Err(9);
    var r: i32 = 0;
    if (ok.is_ok_and(pos)) { r = r + 10; }
    if (er.is_err_and(pos)) { r = r + 5; }
    return r;                                  // 15
}`},
	// Option.transpose: Option[Result[T,E]] -> Result[Option[T],E].
	// Nested-match, callback-free — routes IR like flatten. Some(Ok(5))
	// -> Ok(Some(5)) (+5); Some(Err(9)) -> Err(9) (+9); None -> Ok(None)
	// (+100). 5 + 9 + 100 = 114.
	{"opt-transpose", `
pub function (o: Option[Result[T, E]]) transpose[T, E](): Result[Option[T], E] {
    match (o) {
        Some(r) => { match (r) { Ok(x) => { return Ok(Some(x)); }, Err(e) => { return Err(e); } } },
        None => { return Ok(None); }
    }
}
function main(): i32 {
    var a: Option[Result[i32, i32]] = Some(Ok(5));
    var b: Option[Result[i32, i32]] = Some(Err(9));
    var c: Option[Result[i32, i32]] = None;
    var r: i32 = 0;
    match (a.transpose()) { Ok(inner) => { match (inner) { Some(x) => { r = r + x; }, None => { r = r + 50; } } }, Err(e) => { r = r + 60; } }
    match (b.transpose()) { Ok(inner) => { r = r + 70; }, Err(e) => { r = r + e; } }
    match (c.transpose()) { Ok(inner) => { match (inner) { Some(x) => { r = r + 80; }, None => { r = r + 100; } } }, Err(e) => { r = r + 90; } }
    return r;                                  // 114
}`},
	// Result.transpose: Result[Option[T],E] -> Option[Result[T,E]], the
	// inverse. Ok(Some(3)) -> Some(Ok(3)) (+3); Err(7) -> Some(Err(7))
	// (+7); Ok(None) -> None (+100). 3 + 7 + 100 = 110.
	{"res-transpose", `
pub function (r: Result[Option[T], E]) transpose[T, E](): Option[Result[T, E]] {
    match (r) {
        Ok(inner) => { match (inner) { Some(x) => { return Some(Ok(x)); }, None => { return None; } } },
        Err(e) => { return Some(Err(e)); }
    }
}
function main(): i32 {
    var a: Result[Option[i32], i32] = Ok(Some(3));
    var b: Result[Option[i32], i32] = Err(7);
    var c: Result[Option[i32], i32] = Ok(None);
    var r: i32 = 0;
    match (a.transpose()) { Some(inner) => { match (inner) { Ok(x) => { r = r + x; }, Err(e) => { r = r + 40; } } }, None => { r = r + 50; } }
    match (b.transpose()) { Some(inner) => { match (inner) { Ok(x) => { r = r + 60; }, Err(e) => { r = r + e; } } }, None => { r = r + 70; } }
    match (c.transpose()) { Some(inner) => { r = r + 80; }, None => { r = r + 100; } }
    return r;                                  // 110
}`},
}

func TestSelfHostOptResultCombinatorsIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := writeSelfHostAsmProject(t)
	for _, name := range []string{"asm_run.fern", "asm_pathprobe_run.fern"} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "orc_driver")
	probeBin := buildSelfHostBin(t, gcc, dir, "asm_pathprobe_run.fern", "orc_probe")

	for _, tc := range optResultIRCases {
		t.Run(tc.name, func(t *testing.T) {
			src := []byte(tc.src)
			want := interpExit(t, interpBin, tc.src)
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
			if code := cmd.ProcessState.ExitCode(); code != want {
				t.Errorf("%s exited %d, want %d (interp oracle)", tc.name, code, want)
			}
		})
	}
}

func TestSelfHostOptResultCombinatorsIRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host opt/result wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := t.TempDir()
	for _, name := range []string{
		"util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern",
		"ir.fern", "irlower.fern", "asm_ir.fern", "wasm.fern", "wasm_ir.fern", "wasm_ir_run.fern",
	} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "orc_wasm_driver")

	for _, tc := range optResultIRCases {
		t.Run(tc.name, func(t *testing.T) {
			src := []byte(tc.src)
			want := interpExit(t, interpBin, tc.src)
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
			watFile := filepath.Join(dir, "orc_prog.wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			run := exec.Command("wasmtime", "run", watFile)
			_ = run.Run()
			if run.ProcessState == nil || !run.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.name, wat)
			}
			if code := run.ProcessState.ExitCode(); code != want {
				t.Errorf("opt/result wasm IR %q = %d, want %d (interp oracle)", tc.name, code, want)
			}
		})
	}
}
