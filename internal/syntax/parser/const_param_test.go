package parser

import (
	"slices"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/syntax/ast"
)

func TestConstParamOnFunction(t *testing.T) {
	prog, err := Parse("function count[const N: i32, T](x: T): i32 {\n  return N;\n}")
	if err != nil {
		t.Fatal(err)
	}
	fn := prog.Funcs[0]
	if !slices.Equal(fn.TypeParams, []string{"N", "T"}) {
		t.Errorf("TypeParams = %v, want [N T]", fn.TypeParams)
	}
	if !slices.Equal(fn.ConstParams, []string{"N"}) {
		t.Errorf("ConstParams = %v, want [N]", fn.ConstParams)
	}
}

func TestConstArgInTypeArgList(t *testing.T) {
	prog, err := Parse("function main(): i32 {\n  return count[3, string](\"a\");\n}")
	if err != nil {
		t.Fatal(err)
	}
	ret := prog.Funcs[0].Body.Stmts[0].(*ast.Return)
	call := ret.Value.(*ast.Call)
	if len(call.TypeArgs) != 2 || !ast.Equal(call.TypeArgs[0], ast.ConstArg{Value: 3}) {
		t.Errorf("TypeArgs = %v, want [3 string]", call.TypeArgs)
	}
}

func TestConstArgSpellings(t *testing.T) {
	for src, want := range map[string]ast.ConstArg{
		"0x10":  {Value: 16, Raw: "0x10"},
		"0o17":  {Value: 15, Raw: "0o17"},
		"0b101": {Value: 5, Raw: "0b101"},
		"1_000": {Value: 1000, Raw: "1_000"},
		"007":   {Value: 7},
	} {
		prog, err := Parse("function main(): i32 {\n  return count[" + src + ", string](\"a\");\n}")
		if err != nil {
			t.Fatalf("%s: %v", src, err)
		}
		call := prog.Funcs[0].Body.Stmts[0].(*ast.Return).Value.(*ast.Call)
		if got, ok := call.TypeArgs[0].(ast.ConstArg); !ok || got != want {
			t.Errorf("%s: TypeArgs[0] = %#v, want %#v", src, call.TypeArgs[0], want)
		}
	}
}

func TestConstParamRefusals(t *testing.T) {
	cases := []struct{ name, src, want string }{
		{"no type", "function f[const N](): i32 {\n  return 0;\n}", "const parameter N needs a type: write const N: i32"},
		{"not i32", "function f[const N: i64](): i32 {\n  return 0;\n}", "const parameter N has type i32, got \"i64\""},
		{"impl", "trait Show {\n  function show(self: Self): i32;\n}\nstruct B { x: i32 }\nimpl[const N: i32] Show for B {\n  function show(self: B): i32 {\n    return 0;\n  }\n}", "const parameter N: only a function takes a const parameter"},
		{"trait", "trait T[const N: i32] {\n  function get(self: Self): i32;\n}", "const parameter N: only a function takes a const parameter"},
		{"method", "struct B { x: i32 }\nfunction (b: B) get[const N: i32](): i32 {\n  return 0;\n}", "const parameter N: only a function takes a const parameter"},
		{"impl method", "trait Show {\n  function show(self: Self): i32;\n}\nstruct B { x: i32 }\nimpl Show for B {\n  function show[const N: i32](self: B): i32 {\n    return 0;\n  }\n}", "const parameter N: only a function takes a const parameter"},
		{"trait method", "trait T {\n  function get[const N: i32](self: Self): i32;\n}", "const parameter N: only a function takes a const parameter"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := Parse(c.src)
			if err == nil {
				t.Fatalf("accepted:\n%s", c.src)
			}
			if code := firstCode(err); code != "P001" {
				t.Errorf("want P001, got %q: %v", code, err)
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Errorf("want %q, got %v", c.want, err)
			}
		})
	}
}
