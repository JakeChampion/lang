package checker

import (
	"errors"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/pkg/modload"
	"github.com/jakechampion/lang/internal/syntax/diag"
	"github.com/jakechampion/lang/internal/syntax/parser"
)

func TestReservedTypeNamesPrecedeResolution(t *testing.T) {
	for _, tc := range []struct {
		name, declarations string
		count              int
	}{
		{"enum over struct", "enum Span { Empty, Wide(f64, string) }", 1},
		{"struct over enum", "struct Option { value: i32 }", 1},
		{"two reserved names", "enum Span { Empty, Wide(f64, string) }\nstruct Reader { value: i32 }", 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			prog, _, err := modload.LoadSource("import \"std/time\";\n" + tc.declarations + "\nfunction main(): i32 { return 0; }\n")
			if err != nil {
				t.Fatal(err)
			}
			info, err := Check(prog)
			if info == nil || info.FuncSigs == nil {
				t.Fatal("fatal type diagnostics must retain partial checker information")
			}
			var diagnostics diag.Errors
			if !errors.As(err, &diagnostics) || len(diagnostics) != tc.count {
				t.Fatalf("want %d reserved-name diagnostics, got %v", tc.count, err)
			}
			for _, diagnostic := range diagnostics {
				var check *Error
				if !errors.As(diagnostic, &check) || check.Code() != "E010" || check.Pos.Line < 2 {
					t.Fatalf("unexpected diagnostic: %v", diagnostic)
				}
			}
		})
	}
}

func TestReservedTypeNamesAfterPriorCheck(t *testing.T) {
	for _, declaration := range []string{"enum Span { Empty }", "struct Option { value: i32 }"} {
		t.Run(declaration, func(t *testing.T) {
			prog, err := parser.Parse("function main(): i32 { return 0; }")
			if err != nil {
				t.Fatal(err)
			}
			if _, err := Check(prog); err != nil {
				t.Fatal(err)
			}
			added, err := parser.Parse(declaration)
			if err != nil {
				t.Fatal(err)
			}
			prog.Enums = append(prog.Enums, added.Enums...)
			prog.Structs = append(prog.Structs, added.Structs...)
			_, err = Check(prog)
			var diagnostics diag.Errors
			if !errors.As(err, &diagnostics) || len(diagnostics) != 1 {
				t.Fatalf("want one reserved-name diagnostic after a prior check, got %v", err)
			}
			var check *Error
			if !errors.As(diagnostics[0], &check) || check.Code() != "E010" {
				t.Fatalf("want E010 after a prior check, got %v", err)
			}
		})
	}
}

func TestValidEnumKeepsBuiltinStructAndMethodChecks(t *testing.T) {
	const source = `import "std/time";
enum Sample { Empty, Wide(f64, string) }
function f(s: Sample): i32 {
  match (s) { Wide(d, t) => { return (d * 4.0) as i32 + t.len(); }, Empty => { return 0; } }
}
function main(): i32 { return f(Wide(1.0, "ab")); }
`
	prog, _, err := modload.LoadSource(source)
	if err != nil {
		t.Fatal(err)
	}
	info, err := Check(prog)
	if err != nil {
		t.Fatal(err)
	}
	if info.Structs["Span"] == nil || info.Enums["Sample"] == nil {
		t.Fatal("valid user enum lost the built-in struct")
	}
	prog, _, err = modload.LoadSource(strings.Replace(source, "t.len()", "t.no_such_method()", 1))
	if err != nil {
		t.Fatal(err)
	}
	_, err = Check(prog)
	var diagnostics diag.Errors
	if !errors.As(err, &diagnostics) || len(diagnostics) != 1 || !strings.Contains(err.Error(), "no_such_method") {
		t.Fatalf("valid names must retain method errors: %v", err)
	}
	var method *Error
	if !errors.As(diagnostics[0], &method) || method.Code() != "E043" {
		t.Fatalf("want the method error E043, got %v", err)
	}
}
