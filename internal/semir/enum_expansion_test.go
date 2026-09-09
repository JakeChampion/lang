package semir

import (
	"fmt"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/ast"
	"github.com/jakechampion/lang/internal/checker"
)

func TestFiniteEnumExpansion(t *testing.T) {
	tParam, uParam := ast.ParamType{Name: "T"}, ast.ParamType{Name: "U"}
	array := func(t ast.Type) ast.Type { return ast.ArrayType{Elem: t} }
	enum := func(name string, args ...ast.Type) ast.EnumType { return ast.EnumType{Name: name, Args: args} }
	for _, tc := range []struct {
		name    string
		e, f    ast.Type
		wantBad bool
	}{
		{"identity", enum("E", tParam, uParam), nil, false},
		{"permutation", enum("E", uParam, tParam), nil, false},
		{"constant reset", enum("E", array(uParam), ast.NumberType{}), nil, false},
		{"array growth", enum("E", array(tParam), uParam), nil, true},
		{"permuted growth", enum("E", array(uParam), tParam), nil, true},
		{"tuple growth", enum("E", ast.TupleType{Elems: []ast.Type{tParam, uParam}}, uParam), nil, true},
		{"nested enum growth", enum("E", enum("E", tParam, uParam), uParam), nil, true},
		{"nested constant reset", enum("E", enum("E", ast.NumberType{}, ast.StringType{}), ast.StringType{}), nil, false},
		{"mutual growth", enum("F", array(tParam), uParam), enum("E", tParam, uParam), true},
		{"mutual reset", enum("F", array(tParam), uParam), enum("E", ast.NumberType{}, uParam), false},
		{"unused growing declaration", tParam, enum("F", array(tParam), uParam), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			info := &checker.Info{Enums: map[string]*ast.EnumDecl{}}
			for name, payload := range map[string]ast.Type{"E": tc.e, "F": tc.f} {
				info.Enums[name] = &ast.EnumDecl{Name: name, TypeParams: []string{"T", "U"}, Variants: []ast.EnumVariant{{Name: "Next", Payloads: []ast.Type{payload}}}}
			}
			err := verifyFiniteEnumExpansion(enum("E", ast.NumberType{}, ast.StringType{}), info)
			if (err != nil) != tc.wantBad || err != nil && !strings.Contains(err.Error(), "unbounded enum") {
				t.Fatalf("got %v, want unbounded=%t", err, tc.wantBad)
			}
		})
	}
}

func TestFiniteEnumImportDoesNotUseDepthCap(t *testing.T) {
	for _, count := range []int{1, 128} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			info := &checker.Info{Enums: map[string]*ast.EnumDecl{}}
			for i := range count {
				name := fmt.Sprintf("E%d", i)
				next := fmt.Sprintf("E%d", (i+1)%count)
				info.Enums[name] = &ast.EnumDecl{Name: name, TypeParams: []string{"T"}, Variants: []ast.EnumVariant{
					{Name: "End"},
					{Name: "Next", Payloads: []ast.Type{ast.EnumType{Name: next, Args: []ast.Type{ast.ParamType{Name: "T"}}}}},
				}}
			}
			p := &Program{}
			root := ast.EnumType{Name: "E0", Args: []ast.Type{ast.NumberType{}}}
			if err := p.importNominalTypes(root, info); err != nil {
				t.Fatal(err)
			}
			if len(p.enums) != count {
				t.Fatalf("imported %d interfaces, want %d", len(p.enums), count)
			}
			v := typeVerifier{program: p}
			if err := v.check(root, false); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestEnumImportChecksPhantomArguments(t *testing.T) {
	info := &checker.Info{Enums: map[string]*ast.EnumDecl{
		"E": {Name: "E", TypeParams: []string{"T"}, Variants: []ast.EnumVariant{{Name: "Only"}}},
	}}
	for _, typ := range []ast.Type{ast.ParamType{Name: "T"}, ast.VoidType{}, ast.StructType{Name: "Missing"}} {
		t.Run(typ.String(), func(t *testing.T) {
			p := &Program{}
			root := ast.EnumType{Name: "E", Args: []ast.Type{typ}}
			err := p.importNominalTypes(root, info)
			if err == nil {
				v := typeVerifier{program: p}
				err = v.check(root, false)
			}
			if err == nil {
				t.Fatal("unsupported phantom type argument accepted")
			}
		})
	}
}

// For this five-expression grammar, a finite two-parameter substitution reaches
// a repeated instance within four steps: dependencies either permute two values
// or end at the single constant. Sixteen concrete steps distinguish every case
// independently of SCC structure. This bound belongs only to the test oracle.
func TestFiniteEnumExpansionConcreteOracle(t *testing.T) {
	tParam, uParam := ast.ParamType{Name: "T"}, ast.ParamType{Name: "U"}
	choices := []ast.Type{tParam, uParam, ast.NumberType{}, ast.ArrayType{Elem: tParam}, ast.ArrayType{Elem: uParam}}
	for i, left := range choices {
		for j, right := range choices {
			t.Run(fmt.Sprintf("%d-%d", i, j), func(t *testing.T) {
				info := &checker.Info{Enums: map[string]*ast.EnumDecl{
					"E": {Name: "E", TypeParams: []string{"T", "U"}, Variants: []ast.EnumVariant{{Name: "Next", Payloads: []ast.Type{ast.EnumType{Name: "E", Args: []ast.Type{left, right}}}}}},
				}}
				state := []ast.Type{ast.NumberType{}, ast.StringType{}}
				seen, repeats := make(map[string]bool), false
				for range 16 {
					key := ast.EnumType{Name: "E", Args: state}.String()
					if seen[key] {
						repeats = true
						break
					}
					seen[key] = true
					var expand func(ast.Type) ast.Type
					expand = func(typ ast.Type) ast.Type {
						switch x := typ.(type) {
						case ast.ParamType:
							if x.Name == "T" {
								return state[0]
							}
							return state[1]
						case ast.ArrayType:
							return ast.ArrayType{Elem: expand(x.Elem)}
						}
						return typ
					}
					state = []ast.Type{expand(left), expand(right)}
				}
				err := verifyFiniteEnumExpansion(ast.EnumType{Name: "E", Args: []ast.Type{ast.NumberType{}, ast.StringType{}}}, info)
				if (err == nil) != repeats {
					t.Fatalf("concrete repeats=%t, static check=%v", repeats, err)
				}
			})
		}
	}
}
