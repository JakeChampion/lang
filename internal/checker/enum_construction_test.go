package checker

import (
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/ast"
	"github.com/jakechampion/lang/internal/parser"
)

func TestEnumConstructionContracts(t *testing.T) {
	cases := []struct {
		name, source string
		want         []string
	}{
		{"bare and qualified", `function f(): Option[i64] { let a: Option[string] = Option.None; return None; }`,
			[]string{"Option[i64]/None()", "Option[string]/None()"}},
		{"partial and phantom arguments", `enum Mark[T, P] { Marked(T), Blank }
function f(): Result[i64, string] { let m: Mark[i32, boolean] = Marked(1); let b: Mark[string, i64] = Blank; return Ok(2); }`,
			[]string{"Mark[i32, boolean]/Marked(i32)", "Mark[string, i64]/Blank()", "Result[i64, string]/Ok(i64)"}},
		{"qualified constructor", `enum Wide[T] { Value(T) } function f(): Wide[f32] { return Wide.Value(3.14); }`,
			[]string{"Wide[f32]/Value(f32)"}},
		{"qualified builtin behind function", `function Ok(n: i32): Result[u8, string] { return Result.Ok(1u8); }
function f(): Result[u8, string] { return Ok(300); }`,
			[]string{"Result[u8, string]/Ok(u8)"}},
		{"qualified variant behind function", `enum Box[T] { Packed(T) }
function Packed(n: i32): i32 { return n; }
function f(): Box[string] { let n = Packed(3); return Box.Packed("value"); }`,
			[]string{"Box[string]/Packed(string)"}},
		{"qualified variant behind local", `enum Box[T] { Packed(T) }
function f(): Box[string] { let Packed = 3; return Box.Packed("value"); }`,
			[]string{"Box[string]/Packed(string)"}},
		{"qualified variant with ambiguous bare function", `enum A { Value(i32) } enum B { Value(string) }
function Value(n: i32): B { return B.Value("value"); }
function f(): B { let direct = Value(3); let call = Value; return call(3); }`,
			[]string{"B/Value(string)"}},
		{"nested enum payloads", `function f(): Option[Option[i64]] { return Some(None); }`,
			[]string{"Option[Option[i64]]/Some(Option[i64])", "Option[i64]/None()"}},
		{"declared payload context without destination", `enum Outer { Wrap((Option[i64], Option[string][])) }
function f(): i32 { let value = Wrap((None, [None])); return 0; }`,
			[]string{"Outer/Wrap((Option[i64], Option[string][]))", "Option[i64]/None()", "Option[string]/None()"}},
		{"nested array and tuple", `function f(): (Option[i64][], Result[string, boolean]) { return ([None, Some(2)], Err(true)); }`,
			[]string{"Option[i64]/None()", "Option[i64]/Some(i64)", "Result[string, boolean]/Err(boolean)"}},
		{"inferred array join", `function f(): i32 { let values = [None, Some(2i64)]; return 0; }`,
			[]string{"Option[i64]/None()", "Option[i64]/Some(i64)"}},
		{"inferred if join", `function f(b: boolean): i32 { let value = if (b) { None } else { Some(2i64) }; return 0; }`,
			[]string{"Option[i64]/None()", "Option[i64]/Some(i64)"}},
		{"match destination", `function f(b: boolean): Result[i64, string] { return match (b) { true => Ok(2), _ => Err("no") }; }`,
			[]string{"Result[i64, string]/Ok(i64)", "Result[i64, string]/Err(string)"}},
		{"arguments assignment and fields", `struct Holder { value: Option[i64] }
function take(x: Option[i64]): i32 { return 0; }
function f(): i32 { let x: Option[i64] = None; x = Some(2); let h = Holder { value: None }; return take(Some(3)); }`,
			[]string{"Option[i64]/None()", "Option[i64]/None()", "Option[i64]/Some(i64)", "Option[i64]/Some(i64)"}},
		{"ordinary enum values are not constructors", `function Some(x: i32): Option[i64] { return None; }
function f(): Option[i64] { let None: Option[i64] = Option.None; let value = Some(1); return None; }`,
			[]string{"Option[i64]/None()", "Option[i64]/None()"}},
		{"generic declaration remains truthful", `function wrap[T](x: T): Option[T] { return Some(x); }`,
			[]string{"Option[T]/Some(T)"}},
		{"checked arithmetic rewrite", `function f(a: i64, b: i64): Option[i64] { return a +? b; }`,
			[]string{"Option[i64]/Some(i64)", "Option[i64]/None()"}},
		{"uninferred constructor remains incomplete", `function f(): i32 { let x = Ok(1); let n = None; return 0; }`,
			[]string{"Result[i32, _]/Ok(i32)", "Option/None()"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			prog, err := parser.Parse(tc.source)
			if err != nil {
				t.Fatal(err)
			}
			info, err := Check(prog)
			if err != nil {
				t.Fatal(err)
			}
			reachable := make(map[ast.Expr]bool)
			ast.WalkProgram(prog, func(n ast.Node) bool {
				if e, ok := n.(ast.Expr); ok {
					reachable[e] = true
				}
				return true
			})
			var got []string
			for expr, construction := range info.EnumConstructions {
				if !reachable[expr] {
					t.Errorf("contract for unreachable %T", expr)
				}
				variant := info.Enums[construction.Type.Name].Variants[construction.VariantIndex]
				payloads := make([]string, len(construction.Payloads))
				for i, payload := range construction.Payloads {
					payloads[i] = payload.String()
				}
				got = append(got, fmt.Sprintf("%s/%s(%s)", construction.Type, variant.Name, strings.Join(payloads, ", ")))
			}
			slices.Sort(got)
			slices.Sort(tc.want)
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("contracts:\n got %v\nwant %v", got, tc.want)
			}
		})
	}
}

func TestEnumConstructionRequiresResolution(t *testing.T) {
	info := checkInfo(t, `function f(None: i32): i32 { return None; }`)
	if info.EnumConstructions != nil {
		t.Fatalf("ordinary expressions allocated constructor metadata: %v", info.EnumConstructions)
	}
}

func TestEnumConstructionContextDoesNotSettleOrdinaryCallArguments(t *testing.T) {
	prog, err := parser.Parse(`function f(Some: (i32) => Option[i64]): i32 {
    let values = [Some(1), None];
    let value: Option[i64] = Some(2);
    return 0;
}`)
	if err != nil {
		t.Fatal(err)
	}
	info, err := Check(prog)
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	ast.WalkProgram(prog, func(n ast.Node) bool {
		if call, ok := n.(*ast.Call); ok {
			calls++
			if _, constructed := info.EnumConstructions[call]; constructed {
				t.Errorf("ordinary call classified as constructor")
			}
			if arg := call.Args[0].(*ast.NumberLit); arg.Width != 32 {
				t.Errorf("enum result context changed i32 argument to width %d", arg.Width)
			}
		}
		return true
	})
	if calls != 2 {
		t.Fatalf("checked %d calls, want 2", calls)
	}
}
