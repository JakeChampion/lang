package checker

import (
	"fmt"
	"reflect"
	"testing"

	"github.com/jakechampion/lang/internal/syntax/ast"
	"github.com/jakechampion/lang/internal/syntax/parser"
)

// A `?`'s Type is the payload the IR lays the SOURCE box out by, so settling
// it against its destination may only fix a literal payload's width (#10614):
// stamping the destination's i64 onto an `Option[i32]` source moved the
// payload load past the end of the box.
func TestTryOpTypeIsTheSourcePayload(t *testing.T) {
	cases := []struct{ name, src, want string }{
		{"i32 source in i64 cast", `function f(o: Option[i32]): Option[i64] { return Some((o)? as i64); }
function main(): i32 { return 0; }`, "i32"},
		{"i64 source in i32 cast", `function f(o: Option[i64]): Option[i32] { return Some((o)? as i32); }
function main(): i32 { return 0; }`, "i64"},
		{"checked i32 beside i64", `function f(s: i64, a: i32, b: i32): Option[i64] { return Some(s + ((a +? b)?)); }
function main(): i32 { return 0; }`, "i32"},
		{"literal settles to destination", `function f(): Option[f32] { let v: f32 = Some(3.5)?; return Some(v); }
function main(): i32 { return 0; }`, "f32"},
	}
	for _, c := range cases {
		prog, err := parser.Parse(c.src)
		if err != nil {
			t.Fatalf("%s: parse: %v", c.name, err)
		}
		if _, err := Check(prog); err != nil {
			t.Fatalf("%s: check: %v", c.name, err)
		}
		tries := findTryOps(reflect.ValueOf(prog), map[uintptr]bool{}, nil)
		if len(tries) != 1 {
			t.Fatalf("%s: found %d `?` nodes, want 1", c.name, len(tries))
		}
		if got := fmt.Sprint(tries[0].Type); got != c.want {
			t.Errorf("%s: `?` typed %s, want the source payload %s", c.name, got, c.want)
		}
	}
}

func findTryOps(v reflect.Value, seen map[uintptr]bool, out []*ast.TryOp) []*ast.TryOp {
	switch v.Kind() {
	case reflect.Interface:
		if v.IsNil() {
			return out
		}
		return findTryOps(v.Elem(), seen, out)
	case reflect.Pointer:
		if v.IsNil() || seen[v.Pointer()] {
			return out
		}
		seen[v.Pointer()] = true
		if to, ok := v.Interface().(*ast.TryOp); ok {
			out = append(out, to)
		}
		return findTryOps(v.Elem(), seen, out)
	case reflect.Struct:
		for i := 0; i < v.NumField(); i++ {
			if v.Type().Field(i).IsExported() {
				out = findTryOps(v.Field(i), seen, out)
			}
		}
	case reflect.Slice:
		for i := 0; i < v.Len(); i++ {
			out = findTryOps(v.Index(i), seen, out)
		}
	}
	return out
}
