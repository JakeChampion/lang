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

func TestSelfHostDiagnosticTextParity(t *testing.T) {
	cli := buildSelfHostCLI(t)
	cases := []struct{ name, source string }{
		{"integer_operand", `function main(): i32 { return 1 + true; }`},
		{"two_bad_operands", `function main(): i32 { return "a" + true; }`},
		{"float_operand", `function main(): i32 { return (1.0 + true) as i32; }`},
		{"mixed_float_widths", `function main(): i32 { let x: f32 = 1.0; let y: f64 = 2.0; return (x + y) as i32; }`},
		{"captured_array", `function main(): i32 { let xs: i32[] = [1]; let f = (): i32 => { xs = [2]; return 0; }; return f(); }`},
		{"tuple_bad_first", `function main(): i32 { let p = (1, 2); match (p) { (a, b, c) => { return a; }, (a, b) => { return b; } } return 0; }`},
		{"tuple_bad_second", `function main(): i32 { let p = (1, 2, 3); match (p) { (a, b) when a > 0 => { return a; }, (a, b, c) => { return c; } } return 0; }`},
		{"partial_type_args", `struct Pair[A, B] { first: A, second: B } function main(): i32 { let p: Pair[i32] = Pair { first: 1, second: 2 }; return p.first; }`},
		{"extra_type_args", `struct Pair[A, B] { first: A, second: B } function main(): i32 { let p: Pair[i32, i32, i32] = Pair { first: 1, second: 2 }; return p.first; }`},
		{"value_required", `function main(): i32 { let x: i32 = { let a: i32 = 1; a + 1; }; return x; }`},
		{"if_arms", `function main(): i32 { let x = if (true) { 1 } else { "a" }; return 0; }`},
		{"providers", `struct P { x: i32 }
trait Meters { function scale(self: Self): i32; }
trait Feet { function scale(self: Self): i32; }
impl Meters for P { function scale(self: Self): i32 { return self.x; } }
impl Feet for P { function scale(self: Self): i32 { return self.x * 3; } }
function main(): i32 { return P { x: 1 }.scale(); }`},
	}
	codeAndText := regexp.MustCompile(`error\[([EP][0-9]{3})\]: ([^\n]*)`)
	messages := func(text string) []string {
		var out []string
		for _, match := range codeAndText.FindAllStringSubmatch(text, -1) {
			out = append(out, match[1]+" "+match[2])
		}
		sort.Strings(out)
		return out
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			src := filepath.Join(t.TempDir(), tc.name+".fern")
			if err := os.WriteFile(src, []byte(tc.source), 0o644); err != nil {
				t.Fatal(err)
			}
			prog, _, err := modload.Load(src)
			if err != nil {
				t.Fatal(err)
			}
			_, err = checker.Check(prog)
			if err == nil {
				t.Fatal("oracle accepted the invalid program")
			}
			want := messages(diag.Format(src, tc.source, err))
			if len(want) == 0 {
				t.Fatal("oracle reported no coded diagnostic")
			}
			for _, mode := range []string{"check", "interp", "compile"} {
				extra := []string{}
				if mode != "compile" {
					extra = append(extra, "-"+mode)
				}
				cmd := cli.compileCmd("x86-64-linux", filepath.Join(t.TempDir(), "out"), src, extra...)
				out, err := cmd.CombinedOutput()
				if err == nil || cmd.ProcessState.ExitCode() != 1 {
					t.Fatalf("%s: expected exit 1, got %v\n%s", mode, err, out)
				}
				if got := messages(string(out)); !reflect.DeepEqual(got, want) {
					t.Errorf("%s: messages = %v, want %v\n%s", mode, got, want, out)
				}
			}
		})
	}
}
