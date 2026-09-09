package e2eselfhost

import (
	"fmt"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/checker"
	"github.com/jakechampion/lang/internal/diag"
	"github.com/jakechampion/lang/internal/parser"
)

func TestSelfHostFloatTypeContracts(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "float_types_run.fern")
	bin := buildSelfHostBin(t, gcc, dir, "float_types_run.fern", "float_types")
	want := strings.Join([]string{
		"f32:concrete", "f64:concrete", "f64:concrete",
		"array<array<f32:concrete>>", "tuple(f32:concrete,tuple(f64:concrete,f32:concrete))",
		"fn(f32:concrete)->array<f64:concrete>",
		"f64:literal", "f32:concrete", "f64:concrete", "f32:concrete",
		"f32:concrete", "f32:concrete", "f64:literal", "tuple(f32:concrete,f64:literal)",
		"f32:concrete", "f32:concrete",
	}, "\n") + "\n"
	got, err := runX86_64Bin(runner, bin).CombinedOutput()
	if err != nil || string(got) != want {
		t.Fatalf("float type contract: %v\ngot:\n%swant:\n%s", err, got, want)
	}
}

// Build the actual checker directly with Go while the typed-match parser and
// lowering are still being integrated. This runs its complete diagnostic walk,
// not just check_expr or a model of its result rules.
func TestSelfHostFloatWidthCheckerX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "checker_run.fern")
	bin := buildSelfHostBin(t, gcc, dir, "checker_run.fern", "checker_run")
	cases := []struct{ name, src, code string }{
		{"option payload width", `function f(o: Option[f32]): f64 { match (o) { Some(x) => { return x; }, None => { return 0.5; } } }`, "E002"},
		{"result error width", `function f(r: Result[f32, f64]): f32 { match (r) { Ok(x) => { return x; }, Err(e) => { return e; } } }`, "E002"},
		{"payload guard type", `function f(o: Option[f32]): i32 { match (o) { Some(x) when x => { return 1; }, _ => { return 0; } } }`, "E027"},
		{"branch local width", `function f(c: boolean, a: f32): f32 { return if (c) { var x = a; x } else { var x = a; x }; }`, ""},
		{"branch local conflict", `function f(c: boolean, a: f32, b: f64): i32 { var v = if (c) { var x = a; x } else { var x = b; x }; return 0; }`, "E031"},
		{"match local conflict", `function f(c: i32, a: f32, b: f64): i32 { var v = match(c) { 0 => { var x = a; x }, _ => { var x = b; x } }; return 0; }`, "E031"},
		{"arithmetic", `function f(a: f32): f32 { return -(a + 0.5); }`, ""},
		{"assignment", `function f(a: f32): f32 { a = 0.5; return a; }`, ""},
		{"comparison", `function f(a: f32): boolean { return a < 0.5; }`, ""},
		{"equality", `function f(a: f32): boolean { return a == 0.5; }`, ""},
		{"declared local", `function f(): f32 { var a: f32 = 0.5; return a; }`, ""},
		{"return width", `function f(a: f32): f64 { return a; }`, "E002"},
		{"local width", `function f(a: f32): i32 { var b: f64 = a; return 0; }`, "E003"},
		{"arithmetic width", `function f(a: f32, b: f64): i32 { var c = a + b; return 0; }`, "E009"},
		{"comparison width", `function f(a: f32, b: f64): boolean { return a < b; }`, "E009"},
		{"equality width", `function f(a: f32, b: f64): boolean { return a == b; }`, "E041"},
		{"call width", `function g(a: f64): i32 { return 0; } function f(a: f32): i32 { return g(a); }`, "E038"},
		{"f32 method", `function (a: f32) value(): f32 { return a; } function f(a: f32): f32 { return a.value(); }`, ""},
		{"reverse literal", `function f(a: f32): f32 { return 0.5 - a; }`, ""},
		{"explicit widening", `function f(a: f32): f64 { return a as f64; }`, ""},
		{"explicit narrowing", `function f(a: f64): f32 { return a as f32; }`, ""},
		{"nested callable width", `function f(t: ((f32) => i32, i32), a: f64): i32 { return t.0(a); }`, "E038"},
		{"if literal join", `function f(c: boolean, a: f32): f32 { return if (c) { 0.5 } else { a }; }`, ""},
		{"if concrete join", `function f(c: boolean, a: f32, b: f64): i32 { var v = if (c) { a } else { b }; return 0; }`, "E031"},
		{"match concrete join", `function f(c: i32, a: f32, b: f64): i32 { var v = match(c) { 0 => a, _ => b }; return 0; }`, "E031"},
		{"match literal first", `function f(c: i32, a: f32, b: f64): i32 { var v = match(c) { 0 => 0.5, 1 => a, _ => b }; return 0; }`, "E031"},
		{"tuple literal join", `function f(c: boolean, a: f32): (f32, i32) { return if (c) { (0.5, 1) } else { (a, 2) }; }`, ""},
	}
	for i, arms := range [][3]string{
		{"0.5", "a", "b"}, {"0.5", "b", "a"}, {"a", "0.5", "b"},
		{"a", "b", "0.5"}, {"b", "0.5", "a"}, {"b", "a", "0.5"},
	} {
		for _, nested := range []bool{false, true} {
			values := arms
			if nested {
				for j, value := range values {
					values[j] = "(0, (" + value + ", 1))"
				}
			}
			cases = append(cases, struct{ name, src, code string }{
				fmt.Sprintf("commitment-order-%d-nested-%t", i, nested),
				fmt.Sprintf(`function f(c: i32, a: f32, b: f64): i32 { var v = match(c) { 0 => %s, 1 => %s, _ => %s }; return 0; }`, values[0], values[1], values[2]),
				"E031",
			})
		}
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			prog, err := parser.Parse(tc.src)
			if err != nil {
				t.Fatal(err)
			}
			_, err = checker.Check(prog)
			if tc.code == "" && err != nil || tc.code != "" && (err == nil || !strings.Contains(diag.Format("width.fern", tc.src, err), tc.code)) {
				t.Fatalf("native: want code %q, got %v", tc.code, err)
			}
			code, stderr := runSelfHostChecker(t, bin, runner, tc.src)
			if tc.code == "" {
				if code != 0 || strings.TrimSpace(stderr) != "" {
					t.Fatalf("self-host rejected: exit %d\n%s", code, stderr)
				}
			} else if code == 0 || !strings.Contains(stderr, tc.code) {
				t.Fatalf("self-host: want %s, got exit %d\n%s", tc.code, code, stderr)
			}
		})
	}
}
