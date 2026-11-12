package e2ecompiler

import (
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"testing"

	"github.com/jakechampion/lang/internal/check/checker"
	"github.com/jakechampion/lang/internal/pkg/modload"
	"github.com/jakechampion/lang/internal/syntax/diag"
)

func TestSelfHostIndirectOwnContracts(t *testing.T) {
	cases := []struct {
		name, source string
		codes        []string
	}{
		{"fresh_method_same_name", `struct A { text: string }
struct B { text: string }
function fresh(): A { return A { text: "a" }; }
function (own value: A) update(text: string): A { return value; }
function (value: B) update(text: string): string { return text; }
function (own value: A) done(): i32 { return value.text.len(); }
function probe(text: string): i32 { return fresh().update(text).done(); }
function main(): i32 { return probe("b") - 1; }`, nil},
		{"copied_method_result", `struct B { text: string }
function (value: B) update(text: string): string { return text; }
function sink(own text: string): i32 { return text.len(); }
function probe(value: B, text: string): i32 {
  let n = sink(value.update(text));
  return n + text.len();
}
function main(): i32 { return 0; }`, nil},
		{"borrowed_method_result", `struct B { text: string }
struct Res { xs: i32[] }
function (own r: Res) close(): void {}
function (value: B) update(r: Res): Res { return r; }
function sink(own r: Res): i32 { return r.xs[0]; }
function probe(value: B, r: Res): i32 {
  let n = sink(value.update(r));
  return n + r.xs[0];
}
function main(): i32 { return 0; }`, []string{"E051"}},
		{"fresh_scalar_method_same_name", `struct B { text: string }
function (value: i32) update(): string { return "a" + "b"; }
function (value: B) update(): string { return value.text; }
function sink(own text: string): i32 { return text.len(); }
function main(): i32 { return sink((1).update()) - 2; }`, nil},
		{"string_literal", `function apply(f: (own string) => i32): i32 { return f("ab"); }
function main(): i32 { return 0; }`, nil},
		{"parameter_with_caller", `function length(own s: string): i32 { return s.len(); }
function apply(f: (own string) => i32, s: string): i32 { return f(s); }
function main(): i32 { return apply(length, "ab"); }`, []string{"E051"}},
		{"local_closure", `function run(s: string): i32 { let f: (own string) => i32 = (own t: string): i32 => t.len(); return f(s); }
function main(): i32 { return 0; }`, []string{"E051"}},
		{"function_field", `struct F { f: (own string) => i32 }
function apply(h: F, s: string): i32 { return h.f(s); }
function main(): i32 { return 0; }`, []string{"E051"}},
		{"parameter_literal_with_caller", `function length(own s: string): i32 { return s.len(); }
function apply(f: (own string) => i32): i32 { return f("ab"); }
function main(): i32 { return apply(length); }`, nil},
		{"local_closure_literal", `function main(): i32 { let f: (own string) => i32 = (own s: string): i32 => s.len(); return f("ab"); }`, nil},
		{"function_field_literal", `struct F { f: (own string) => i32 }
function apply(h: F): i32 { return h.f("ab"); }
function main(): i32 { return 0; }`, nil},
		{"borrowed_argument", `function apply(f: (own string) => i32, s: string): i32 { return f(s); }
function main(): i32 { return 0; }`, []string{"E051"}},
		{"borrowed_resource_callback", `struct R { n: i32 }
function (own r: R) close(): i32 { return r.n; }
function apply(f: (own R) => i32, r: R): i32 { return f(r); }
function main(): i32 { return 0; }`, []string{"E051"}},
		{"borrowed_resource_function_field", `struct R { n: i32 }
function (own r: R) close(): i32 { return r.n; }
struct F { f: (own R) => i32 }
function apply(h: F, r: R): i32 { return h.f(r); }
function main(): i32 { return 0; }`, []string{"E051"}},
		{"fresh_resource_callback", `struct R { n: i32 }
function (own r: R) close(): i32 { return r.n; }
function apply(f: (own R) => i32): i32 { return f(R { n: 2 }); }
function main(): i32 { return 0; }`, nil},
		{"use_after_move", `function apply(f: (own string) => i32, own s: string): i32 { let n = f(s); return n + s.len(); }
function main(): i32 { return 0; }`, []string{"E050"}},
		{"local_last_use", `function apply(f: (own string) => i32): i32 { let s = "ab"; return f(s); }
function main(): i32 { return 0; }`, nil},
		{"field_local_last_use", `struct F { f: (own string) => i32 }
function apply(h: F): i32 { let s = "ab"; return h.f(s); }
function main(): i32 { return 0; }`, nil},
		{"fresh_construction", `function apply(f: (own string) => i32): i32 { return f("a" + "b"); }
function main(): i32 { return 0; }`, nil},
		{"scalar_own_slot", `function apply(f: (own i32) => i32): i32 { return f(3); }
function main(): i32 { return 0; }`, nil},
		{"borrowed_callback", `function apply(f: (string) => i32, own s: string): i32 { let n = f(s); return n + f(s); }
function main(): i32 { return 0; }`, nil},
		{"field_value_result_local", `struct P[S] { update: (own S, i32) => S, effects: (own S) => S }
function run[S](p: P[S], own s: S): S { let next = p.update(s, 3); return p.effects(next); }
function main(): i32 { return 0; }`, []string{"E051"}},
		{"field_value_own_result_local", `struct P[S] { update: (own S, i32) => own S, effects: (own S) => own S }
function run[S](p: P[S], own s: S): S { let next = p.update(s, 3); return p.effects(next); }
function main(): i32 { return 0; }`, nil},
		{"borrowed_field_callback", `struct F { f: (string) => i32 }
function apply(h: F, own s: string): i32 { let n = h.f(s); return n + h.f(s); }
function main(): i32 { return 0; }`, nil},
	}
	cli := buildSelfHostCLI(t)
	findingRE := regexp.MustCompile(`(?m)^.*?:([0-9]+):([0-9]+): error\[([EP][0-9]{3})\]: ([^\n]*)`)
	findings := func(text string) []string {
		var out []string
		for _, m := range findingRE.FindAllStringSubmatch(text, -1) {
			out = append(out, m[1]+":"+m[2]+" "+m[3]+" "+m[4])
		}
		sort.Strings(out)
		return out
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			src := filepath.Join(t.TempDir(), "main.fern")
			if err := os.WriteFile(src, []byte(tc.source), 0o644); err != nil {
				t.Fatal(err)
			}
			prog, _, err := modload.Load(src)
			if err != nil {
				t.Fatal(err)
			}
			_, err = checker.Check(prog)
			if got := goCheckerCodes(t, t.TempDir(), tc.source); !reflect.DeepEqual(got, tc.codes) {
				t.Fatalf("Go codes = %v, want %v: %v", got, tc.codes, err)
			}
			want := findings(diag.Format(src, tc.source, err))
			cmd := cli.compileCmd("x86-64-linux", filepath.Join(t.TempDir(), "out"), src, "-check")
			out, runErr := cmd.CombinedOutput()
			if (runErr != nil) != (err != nil) {
				t.Fatalf("Go error = %v, self-host error = %v\n%s", err, runErr, out)
			}
			if got := findings(string(out)); !reflect.DeepEqual(got, want) {
				t.Errorf("findings = %v, want %v\n%s", got, want, out)
			}
		})
	}
}

func TestSelfHostIndirectOwnedLiteralRun(t *testing.T) {
	ownedResultRuntimeCase(t, "own_parameter_takes_a_string_literal")
}
