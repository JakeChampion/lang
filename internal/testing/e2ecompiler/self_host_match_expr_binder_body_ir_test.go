package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

var computedScrutineeCases = []struct {
	name string
	src  string
}{
	{"computed_scrutinee_if_let", `function input(): Option[string] { return Some("payload"); }
function main(): i32 {
    let result = if let Some(text) = input() { text } else { "empty" };
    return result.len();
}`},
	{"computed_scrutinee_unannotated", `function value(): i32 { return 17; }
function pick(key: string): i32 {
    let keys: string[] = [key];
    let result = match (keys[0]) { "a" => value(), _ => { return 2; } };
    return result;
}
function main(): i32 { return pick("a") + pick("b"); }`},
	{"computed_scrutinee_wide_payload", `enum Item { Number(i64), Empty }
function main(): i32 {
    let items: Item[] = [Number(5000000000i64)];
    let result = match (items[0]) { Number(n) => n, Empty => 0i64 };
    if (result != 5000000000i64) { return 29; }
    return (result % 97i64) as i32;
}`},
	{"computed_scrutinee_single_evaluation", `function next(calls: Cell[i32]): string {
    calls.set(calls.get() + 1);
    return "payload";
}
function main(): i32 {
    let calls = cell_new(0);
    let result = match (next(calls)) { "other" => "wrong", _ => "correct" };
    if (calls.get() != 1 || result != "correct") { return 30; }
    return result.len();
}`},
}

var matchExprBinderBodyCases = append(computedScrutineeCases, []struct{ name, src string }{
	{"tuple_string_binder", `function main(): i32 {
    let t: (string, i32) = ("elem", 4);
    let got: string = match (t) { (q, n) => q };
    if (got != "elem") { return 13; }
    return got.len() as i32;
}`},
	{"tuple_i64_binder", `function main(): i32 {
    let t: (i64, i32) = (5000000000i64, 4);
    let got: i64 = match (t) { (q, n) => q };
    if (got != 5000000000i64) { return 13; }
    return (got % 97i64) as i32;
}`},
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
}`},
	{"tuple_subpattern_binder", `enum Pair { Both((string, i32)) }
function main(): i32 {
    let v: Pair = Both(("hi", 2));
    let got: string = match (v) { Both((a, b)) => a };
    if (got != "hi") { return 13; }
    return got.len() as i32;
}`},
	{"struct_pattern_binder", `struct Pt { x: i32, label: string }
function main(): i32 {
    let p: Pt = Pt { x: 3, label: "three" };
    let got: string = match (p) { Pt { x, label } => label };
    if (got != "three") { return 13; }
    return got.len() as i32;
}`},
	{"tuple_binder_unannotated", `function main(): i32 {
    let t: (string, i32) = ("elem", 4);
    let got = match (t) { (q, n) => q };
    if (got != "elem") { return 13; }
    return got.len() as i32;
}`},
	{"tuple_binder_guarded", `function main(): i32 {
    let t: (string, i32) = ("elem", 4);
    let got: string = match (t) {
        (q, n) when n > 10 => q,
        (q, n) => "small"
    };
    if (got != "small") { return 13; }
    return got.len() as i32;
}`},
	{"unannotated_literal_arm", `function main(): i32 {
    let t: (string, i32) = ("elem", 4);
    let got = match (t) { (q, n) => "lit" };
    return got.len() as i32;
}`},
	{"tuple_f64_binder", `function main(): i32 {
    let t: (f64, i32) = (2.5, 4);
    let got: f64 = match (t) { (q, n) => q };
    return (got * 4.0) as i32;
}`},
	{"tuple_bool_binder", `function main(): i32 {
    let t: (boolean, i32) = (true, 4);
    let got: boolean = match (t) { (q, n) => q };
    if (!got) { return 13; }
    return 7;
}`},
	{"tuple_u32_binder", `function main(): i32 {
    let t: (u32, i32) = (4000000000u32, 4);
    let got: u32 = match (t) { (q, n) => q };
    return ((got >> 1u32) % 97u32) as i32;
}`},
	{"tuple_u64_binder", `function main(): i32 {
    let t: (u64, i32) = (18446744073709551615u64, 4);
    let got: u64 = match (t) { (q, n) => q };
    return ((got / 2u64) % 64u64) as i32;
}`},
	{"tuple_u8_binder", `function main(): i32 {
    let t: (u8, i32) = (200u8, 4);
    let got: u8 = match (t) { (q, n) => q };
    return (got % 97u8) as i32;
}`},
	{"tuple_char_binder", `function main(): i32 {
    let t: (char, i32) = ('z', 4);
    let got: char = match (t) { (q, n) => q };
    if (got != 'z') { return 13; }
    return 9;
}`},
	{"control_literal_arm", `function main(): i32 {
    let t: (string, i32) = ("elem", 4);
    let got: string = match (t) { (q, n) => "lit" };
    return got.len() as i32;
}`},
	{"control_i32_binder", `function main(): i32 {
    let t: (string, i32) = ("elem", 4);
    let got: i32 = match (t) { (q, n) => n };
    return got;
}`},
	{"nested_match_string_payload", `enum Res { Ok1(string), Err1(string) }
enum Wrap { Box(Res) }
function main(): i32 {
    let w: Wrap = Box(Err1("bad"));
    let inner: string = match (w) { Box(x) => match (x) { Ok1(q) => q, Err1(r) => r } };
    if (inner != "bad") { return 14; }
    return inner.len() as i32;
}`},
	{"if_expr_over_wide_payload", `enum W { V(i64), U(f64) }
function pick(w: W): string {
    return match (w) { V(n) => if (n > 5000000000i64) { "big" } else { "small" }, U(f) => if (f > 1.5) { "hi" } else { "lo" } };
}
function main(): i32 {
    if (pick(V(6000000000i64)) != "big") { return 21; }
    if (pick(U(0.5)) != "lo") { return 22; }
    return (pick(V(1i64)).len() + pick(U(2.5)).len()) as i32;
}`},
	{"option_nested_match_i64_result", `enum Res { Ok1(string), Err1(string) }
function main(): i32 {
    let o: Option[Res] = Some(Err1("bad"));
    let u: i64 = match (o) { Some(x) => match (x) { Ok1(q) => 1i64, Err1(r) => 7000000000i64 }, None => 0i64 };
    if (u != 7000000000i64) { return 23; }
    return (u % 1000i64) as i32;
}`},
	{"dead_string_payload_literal_arm", `enum Res { Ok1(string), Err1(string) }
function main(): i32 {
    let r: Res = Err1("bad");
    let s: string = match (r) { Ok1(q) => "ok", Err1(e) => "err" };
    if (s != "err") { return 24; }
    return s.len() as i32;
}`},
}...)

func TestSelfHostComputedScrutineeCheck(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, tc := range computedScrutineeCases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "main.fern")
			if err := os.WriteFile(path, []byte(tc.src), 0o644); err != nil {
				t.Fatal(err)
			}
			cmd := runX86_64Bin(cli.runner, cli.bin, "-check", path, cli.stdlib)
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("-check: %v\n%s", err, out)
			}
		})
	}
	for _, tc := range []struct{ name, src, code string }{
		{"inferred_string_rejects_integer_destination", `function main(): i32 {
    let keys: string[] = ["a"];
    let result = match (keys[0]) { "a" => "text", _ => "other" };
    let bad: i32 = result;
    return bad;
}`, "E003"},
		{"incompatible_arms_remain_invalid", `function main(): i32 {
    let keys: string[] = ["a"];
    let result = match (keys[0]) { "a" => "text", _ => 7 };
    return 0;
}`, "E031"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "main.fern")
			if err := os.WriteFile(path, []byte(tc.src), 0o644); err != nil {
				t.Fatal(err)
			}
			out, err := runX86_64Bin(cli.runner, cli.bin, "-check", path, cli.stdlib).CombinedOutput()
			if err == nil || !strings.Contains(string(out), "error["+tc.code+"]") || strings.Contains(string(out), "error: internal:") {
				t.Fatalf("want %s, got %v\n%s", tc.code, err, out)
			}
		})
	}
}

func TestSelfHostMatchExprBinderBodyX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "fern.fern")
	fernBin := buildSelfHostBin(t, gcc, dir, "fern.fern", "fern")
	stdlibRoot, err := filepath.Abs("../../stdlib")
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
			if out, cerr := runX86_64Bin(runner, fernBin, "-target", "x86-64-linux", "-emit", "asm", "-o", asmPath, mainPath, stdlibRoot).CombinedOutput(); cerr != nil {
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

func TestSelfHostMatchExprBinderBodyWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping match-expression binder-body wasm cases")
	}
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "fern.fern")
	fernBin := buildSelfHostBin(t, gcc, dir, "fern.fern", "fern")
	stdlibRoot, err := filepath.Abs("../../stdlib")
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
			cmd := runX86_64Bin(runner, fernBin, "-target", "wasm32-wasi", "-emit", "asm", "-o", outWat, mainPath, stdlibRoot)
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
