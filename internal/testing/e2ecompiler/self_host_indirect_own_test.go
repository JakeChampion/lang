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

// Consuming positions belong to the callable type. A caller's choice of
// function and unrelated owned declarations must not change the contract.
func TestSelfHostIndirectOwnContracts(t *testing.T) {
	cases := []struct {
		name, source string
		codes        []string
	}{
		{"parameter_without_caller", `function apply(f: (own string) => i32): i32 { return f("ab"); }
function main(): i32 { return 0; }`, []string{"E051"}},
		{"parameter_with_caller", `function length(own s: string): i32 { return s.len(); }
function apply(f: (own string) => i32): i32 { return f("ab"); }
function main(): i32 { return apply(length); }`, []string{"E051"}},
		{"local_closure", `function main(): i32 { let f: (own string) => i32 = (own s: string): i32 => s.len(); return f("ab"); }`, []string{"E051"}},
		{"function_field", `struct F { f: (own string) => i32 }
function apply(h: F): i32 { return h.f("ab"); }
function main(): i32 { return 0; }`, []string{"E051"}},
		{"borrowed_argument", `function apply(f: (own string) => i32, s: string): i32 { return f(s); }
function main(): i32 { return 0; }`, []string{"E051"}},
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
