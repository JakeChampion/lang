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

// A struct declares a const parameter as a function does, and a type
// application writes an integer for it.
func TestConstParamOnStruct(t *testing.T) {
	prog, err := Parse("struct Ring[const N: i32, T] { items: T[] }\nfunction f(r: Ring[4, string]): i32 {\n  return 0;\n}")
	if err != nil {
		t.Fatal(err)
	}
	sd := prog.Structs[0]
	if !slices.Equal(sd.TypeParams, []string{"N", "T"}) || !slices.Equal(sd.ConstParams, []string{"N"}) {
		t.Errorf("TypeParams = %v, ConstParams = %v, want [N T] and [N]", sd.TypeParams, sd.ConstParams)
	}
	pt, ok := prog.Funcs[0].Params[0].Type.(ast.EnumType)
	if !ok || len(pt.Args) != 2 || !ast.Equal(pt.Args[0], ast.ConstArg{Value: 4}) {
		t.Errorf("parameter type = %#v, want Ring[4, string]", prog.Funcs[0].Params[0].Type)
	}
}

// An enum and an impl declare a const parameter as a struct does. An impl
// method takes the impl's const parameters as its own.
func TestConstParamOnEnumAndImpl(t *testing.T) {
	prog, err := Parse("struct Ring[const N: i32, T] { items: T[] }\nenum Slot[const N: i32, T] { Full(Ring[N, T]), Empty }\ntrait Cap {\n  function cap(self: Self): i32;\n}\nimpl[const N: i32, T] Cap for Ring[N, T] {\n  function cap(self: Ring[N, T]): i32 {\n    return N;\n  }\n}")
	if err != nil {
		t.Fatal(err)
	}
	ed := prog.Enums[0]
	if !slices.Equal(ed.TypeParams, []string{"N", "T"}) || !slices.Equal(ed.ConstParams, []string{"N"}) {
		t.Errorf("enum TypeParams = %v, ConstParams = %v, want [N T] and [N]", ed.TypeParams, ed.ConstParams)
	}
	id := prog.Impls[0]
	if !slices.Equal(id.TypeParams, []string{"N", "T"}) || !slices.Equal(id.ConstParams, []string{"N"}) {
		t.Errorf("impl TypeParams = %v, ConstParams = %v, want [N T] and [N]", id.TypeParams, id.ConstParams)
	}
	for _, fn := range prog.Funcs {
		if fn.Name == "cap" && !slices.Equal(fn.ConstParams, []string{"N"}) {
			t.Errorf("impl method ConstParams = %v, want [N]", fn.ConstParams)
		}
	}
}

// A type alias and a trait declare a const parameter. A bound and an impl
// write an integer for it.
func TestConstParamOnAliasAndTrait(t *testing.T) {
	prog, err := Parse("struct Ring[const N: i32, T] { items: T[] }\nstruct Gone { n: i32 }\ntype Slot[const N: i32] = Ring[N, i32] | Gone;\ntrait Fixed[const N: i32] {\n  function room(self: Self): i32;\n}\nstruct B { n: i32 }\nimpl Fixed[8] for B {\n  function room(self: B): i32 {\n    return 0;\n  }\n}\nfunction f[F: Fixed[4]](x: F): i32 {\n  return 0;\n}")
	if err != nil {
		t.Fatal(err)
	}
	ud := prog.Unions[0]
	if !slices.Equal(ud.TypeParams, []string{"N"}) || !slices.Equal(ud.ConstParams, []string{"N"}) {
		t.Errorf("alias TypeParams = %v, ConstParams = %v, want [N] and [N]", ud.TypeParams, ud.ConstParams)
	}
	td := prog.Traits[0]
	if !slices.Equal(td.TypeParams, []string{"N"}) || !slices.Equal(td.ConstParams, []string{"N"}) {
		t.Errorf("trait TypeParams = %v, ConstParams = %v, want [N] and [N]", td.TypeParams, td.ConstParams)
	}
	if args := prog.Impls[0].TraitArgs; len(args) != 1 || !ast.Equal(args[0], ast.ConstArg{Value: 8}) {
		t.Errorf("impl TraitArgs = %v, want [8]", args)
	}
	for _, fn := range prog.Funcs {
		if fn.Name != "f" {
			continue
		}
		if ba := fn.BoundArgs["F"]; len(ba) != 1 || len(ba[0]) != 1 || !ast.Equal(ba[0][0], ast.ConstArg{Value: 4}) {
			t.Errorf("bound args = %v, want [[4]]", ba)
		}
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
		{"method", "struct B { x: i32 }\nfunction (b: B) get[const N: i32](): i32 {\n  return 0;\n}", "const parameter N: only a function, a struct, an enum, a type alias, an impl or a trait takes a const parameter"},
		{"impl method", "trait Show {\n  function show(self: Self): i32;\n}\nstruct B { x: i32 }\nimpl Show for B {\n  function show[const N: i32](self: B): i32 {\n    return 0;\n  }\n}", "const parameter N: only a function, a struct, an enum, a type alias, an impl or a trait takes a const parameter"},
		{"trait method", "trait T {\n  function get[const N: i32](self: Self): i32;\n}", "const parameter N: only a function, a struct, an enum, a type alias, an impl or a trait takes a const parameter"},
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
