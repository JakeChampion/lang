package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// matchExprBinderBodyCases exercise a value-position `match` whose arm body is a
// pattern BINDER (#8657).
//
// The tuple-, struct- and nested-pattern desugars route their arm values through
// a synthesised local (parser.IfChain.value_local) that has to be declared
// before the parser can know what the arms produce, so it was declared from the
// first arm's syntax alone (parser.if_expr_rt), where a bare ident falls to the
// "i32" default. A `string` / `i64` / `f64` binder was then stored into an i32
// local: E003 on legal code, and the wrong slot width underneath it.
// checker.retype_value_blocks re-declares the local from the type the arms
// assign, which is why these run through fern.fern — a driver that skips
// checker.check_module / annotate_module never sees the rewrite.
//
// The controls are the two spellings that always compiled: a literal arm (the
// parser's guess was already right) and an i32 binder (the guess and the answer
// coincide). Both must keep their answers, since the rewrite is meant to be
// invisible wherever the declaration already agreed.
var matchExprBinderBodyCases = []struct {
	name string
	src  string
}{
	// The issue's first reproducer: tuple scrutinee, string result.
	{"tuple_string_binder", `function main(): i32 {
    var t: (string, i32) = ("elem", 4);
    var got: string = match (t) { (q, n) => q };
    if (got != "elem") { return 13; }
    return got.len() as i32;
}`}, // 4
	// The issue's second: the same shape at i64, so the gap is not string-only.
	{"tuple_i64_binder", `function main(): i32 {
    var t: (i64, i32) = (5000000000i64, 4);
    var got: i64 = match (t) { (q, n) => q };
    if (got != 5000000000i64) { return 13; }
    return (got % 97i64) as i32;
}`},
	// The issue's third: a payload SUB-pattern, which takes the flag-chain
	// desugar and so the same value local.
	{"payload_sub_binder", `enum Res { Ok1(string), Err1(string) }
enum Wrap { Box(Res) }
function pick(w: Wrap): string {
    return match (w) {
        Box(Ok1(q)) => q,
        Box(Err1(r)) => r
    };
}
function main(): i32 {
    if (pick(Box(Ok1("elem"))) != "elem") { return 13; }
    if (pick(Box(Err1("bad"))) != "bad") { return 14; }
    return (pick(Box(Ok1("elem"))).len() + pick(Box(Err1("bad"))).len()) as i32;
}`}, // 7
	// A tuple SUB-pattern inside a variant arm — build_merged_arm's chain, the
	// third desugar that needs a value local.
	{"tuple_subpattern_binder", `enum Pair { Both((string, i32)) }
function main(): i32 {
    var v: Pair = Both(("hi", 2));
    var got: string = match (v) { Both((a, b)) => a };
    if (got != "hi") { return 13; }
    return got.len() as i32;
}`}, // 2
	// A struct-pattern scrutinee: build_struct_match's flag chain.
	{"struct_pattern_binder", `struct Pt { x: i32, label: string }
function main(): i32 {
    var p: Pt = Pt { x: 3, label: "three" };
    var got: string = match (p) { Pt { x, label } => label };
    if (got != "three") { return 13; }
    return got.len() as i32;
}`}, // 5
	// No annotation on the binding: the arms are the only source of the type,
	// so a fix that read the declaration instead would still reject this.
	{"tuple_binder_unannotated", `function main(): i32 {
    var t: (string, i32) = ("elem", 4);
    var got = match (t) { (q, n) => q };
    if (got != "elem") { return 13; }
    return got.len() as i32;
}`}, // 4
	// A GUARDED binder arm: the store sits inside the guard's own `if`, one
	// level below the chain's top, and it is the first arm — so the parser's
	// guess comes from the binder rather than from the literal below it.
	{"tuple_binder_guarded", `function main(): i32 {
    var t: (string, i32) = ("elem", 4);
    var got: string = match (t) {
        (q, n) when n > 10 => q,
        (q, n) => "small"
    };
    if (got != "small") { return 13; }
    return got.len() as i32;
}`}, // 5
	// An unannotated binding of a value-LOCAL block, with a literal arm the
	// parser typed correctly: the DECLARATION was already right here, and the
	// binding still took an i32 slot for a string, because nothing typed the
	// block itself. A wrong answer rather than a diagnostic, so only an
	// oracle-compared run catches it.
	{"unannotated_literal_arm", `function main(): i32 {
    var t: (string, i32) = ("elem", 4);
    var got = match (t) { (q, n) => "lit" };
    return got.len() as i32;
}`}, // 3
	// The remaining widths the declaration has to spell, each one a distinct
	// literal zero: an unsuffixed `0` would leave every one of them an i32.
	{"tuple_f64_binder", `function main(): i32 {
    var t: (f64, i32) = (2.5, 4);
    var got: f64 = match (t) { (q, n) => q };
    return (got * 4.0) as i32;
}`}, // 10
	{"tuple_bool_binder", `function main(): i32 {
    var t: (boolean, i32) = (true, 4);
    var got: boolean = match (t) { (q, n) => q };
    if (!got) { return 13; }
    return 7;
}`},
	{"tuple_u32_binder", `function main(): i32 {
    var t: (u32, i32) = (4000000000u32, 4);
    var got: u32 = match (t) { (q, n) => q };
    return ((got >> 1u32) % 97u32) as i32;
}`},
	{"tuple_u64_binder", `function main(): i32 {
    var t: (u64, i32) = (18446744073709551615u64, 4);
    var got: u64 = match (t) { (q, n) => q };
    return ((got / 2u64) % 64u64) as i32;
}`},
	{"tuple_u8_binder", `function main(): i32 {
    var t: (u8, i32) = (200u8, 4);
    var got: u8 = match (t) { (q, n) => q };
    return (got % 97u8) as i32;
}`},
	{"tuple_char_binder", `function main(): i32 {
    var t: (char, i32) = ('z', 4);
    var got: char = match (t) { (q, n) => q };
    if (got != 'z') { return 13; }
    return 9;
}`},
	// Control: a literal arm, where the parser's guess was already the answer.
	{"control_literal_arm", `function main(): i32 {
    var t: (string, i32) = ("elem", 4);
    var got: string = match (t) { (q, n) => "lit" };
    return got.len() as i32;
}`}, // 3
	// Control: an i32 binder, where the guess and the answer coincide.
	{"control_i32_binder", `function main(): i32 {
    var t: (string, i32) = ("elem", 4);
    var got: i32 = match (t) { (q, n) => n };
    return got;
}`}, // 4
}

// TestSelfHostMatchExprBinderBodyX86_64 — the x86-64 leg. fern.fern is the
// driver because the rewrite lives in the checker: the value local is
// re-declared in checker.check_module (so the build gate stops rejecting the
// program) and again in checker.annotate_module (so the slot the lowering sizes
// matches). A driver that runs neither compiles the parser's declaration
// unchanged.
func TestSelfHostMatchExprBinderBodyX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "fern.fern")
	fernBin := buildSelfHostBin(t, gcc, dir, "fern.fern", "fern")
	stdlibRoot, err := filepath.Abs("../../internal/stdlib")
	if err != nil {
		t.Fatalf("abs stdlib root: %v", err)
	}

	for _, tc := range matchExprBinderBodyCases {
		t.Run(tc.name, func(t *testing.T) {
			want := interpExit(t, interpBin, tc.src)
			proj := t.TempDir()
			mainPath := filepath.Join(proj, "main.fern")
			if err := os.WriteFile(mainPath, []byte(tc.src), 0o644); err != nil {
				t.Fatalf("write main.fern: %v", err)
			}
			asmPath := filepath.Join(proj, "out.s")
			if out, cerr := runX86_64Bin(runner, fernBin, "-target", "x86-64-linux", "-emit", "asm", mainPath, stdlibRoot, "-o", asmPath).CombinedOutput(); cerr != nil {
				t.Fatalf("compile: %v (%s)", cerr, out)
			}
			binPath := filepath.Join(proj, "out.bin")
			if out, lerr := exec.Command(gcc, "-nostdlib", "-static", "-o", binPath, asmPath).CombinedOutput(); lerr != nil {
				t.Fatalf("link: %v (%s)", lerr, out)
			}
			rcmd := runX86_64Bin(runner, binPath)
			_ = rcmd.Run()
			if got := rcmd.ProcessState.ExitCode(); got != want {
				t.Errorf("%s = %d, want %d (interp oracle) — a match-expression arm body that is a pattern binder must type the value local from the binder", tc.name, got, want)
			}
		})
	}
}

// TestSelfHostMatchExprBinderBodyWasm is the wasm leg. The value local's
// declaration decides the slot there too, and wasm validates types, so a
// string stored into an i32 local is a module-level rejection rather than a
// wrong answer.
func TestSelfHostMatchExprBinderBodyWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping match-expression binder-body wasm cases")
	}
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "fern.fern")
	fernBin := buildSelfHostBin(t, gcc, dir, "fern.fern", "fern")
	stdlibRoot, err := filepath.Abs("../../internal/stdlib")
	if err != nil {
		t.Fatalf("abs stdlib root: %v", err)
	}

	for _, tc := range matchExprBinderBodyCases {
		t.Run(tc.name, func(t *testing.T) {
			want := interpExit(t, interpBin, tc.src)
			proj := t.TempDir()
			mainPath := filepath.Join(proj, "main.fern")
			if err := os.WriteFile(mainPath, []byte(tc.src), 0o644); err != nil {
				t.Fatalf("write main.fern: %v", err)
			}
			outWat := filepath.Join(proj, "out.wat")
			var stderr strings.Builder
			cmd := runX86_64Bin(runner, fernBin, "-target", "wasm32-wasi", "-emit", "asm", mainPath, stdlibRoot, "-o", outWat)
			cmd.Stderr = &stderr
			if cerr := cmd.Run(); cerr != nil {
				t.Fatalf("compile: %v (%s)", cerr, stderr.String())
			}
			rcmd := exec.Command("wasmtime", "run", outWat)
			_ = rcmd.Run()
			if rcmd.ProcessState == nil || !rcmd.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q", tc.name)
			}
			if got := rcmd.ProcessState.ExitCode(); got != want {
				t.Errorf("%s = %d, want %d (interp oracle) — a match-expression arm body that is a pattern binder must type the value local from the binder", tc.name, got, want)
			}
		})
	}
}
