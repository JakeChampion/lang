package parser

import (
	"reflect"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/syntax/ast"
)

func TestReceiverBoundParses(t *testing.T) {
	cases := []struct {
		name, src  string
		recv       string
		typeParams []string
		bounds     map[string][]string
	}{
		{"type argument", `struct M[K, V] { k: K, v: V }
function (m: M[K: cmp.Ord, V]) get(k: K): V { return m.v; }`,
			"M[K, V]", []string{"K"}, map[string][]string{"K": {"cmp.Ord"}}},
		{"two bounds on one parameter", `struct B[T] { v: T }
function (b: B[T: cmp.Ord + Show]) get(): T { return b.v; }`,
			"B[T]", []string{"T"}, map[string][]string{"T": {"cmp.Ord", "Show"}}},
		{"array element in a group", `function (xs: (T: num.Num)[]) total(): T { return xs[0]; }`,
			"T[]", []string{"T"}, map[string][]string{"T": {"num.Num"}}},
		{"slice element", `function (xs: [T: Eq]) first(): T { return xs[0]; }`,
			"[T]", []string{"T"}, map[string][]string{"T": {"Eq"}}},
		{"receiver bound before the method's own parameter", `struct B[T] { v: T }
function (b: B[T: Eq]) pick[U: Show](u: U): T { return b.v; }`,
			"B[T]", []string{"T", "U"}, map[string][]string{"T": {"Eq"}, "U": {"Show"}}},
		{"no bound leaves the parameter implicit", `struct B[T] { v: T }
function (b: B[T]) get(): T { return b.v; }`,
			"B[T]", nil, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			prog, err := Parse(c.src)
			if err != nil {
				t.Fatal(err)
			}
			fn := prog.Funcs[len(prog.Funcs)-1]
			if got := fn.Receiver.Type.String(); got != c.recv {
				t.Errorf("receiver type = %s, want %s", got, c.recv)
			}
			if !reflect.DeepEqual(fn.TypeParams, c.typeParams) {
				t.Errorf("type params = %v, want %v", fn.TypeParams, c.typeParams)
			}
			if len(fn.Bounds) != len(c.bounds) || (len(c.bounds) > 0 && !reflect.DeepEqual(fn.Bounds, c.bounds)) {
				t.Errorf("bounds = %v, want %v", fn.Bounds, c.bounds)
			}
		})
	}
}

func TestReceiverBoundArgsParse(t *testing.T) {
	prog, err := Parse(`struct B[T] { v: T }
function (b: B[T: Into[string]]) get(): T { return b.v; }`)
	if err != nil {
		t.Fatal(err)
	}
	fn := prog.Funcs[0]
	args := fn.BoundArgs["T"]
	if len(args) != 1 || len(args[0]) != 1 {
		t.Fatalf("bound args = %v, want one argument on one bound", fn.BoundArgs)
	}
	if _, ok := args[0][0].(ast.StringType); !ok {
		t.Errorf("bound argument = %v, want string", args[0][0])
	}
}

func TestReceiverBoundRefusals(t *testing.T) {
	cases := []struct{ name, src, want string }{
		{"bound at two places", `struct P[A, B] { a: A, b: B }
function (p: P[T: Eq, T: Show]) f(): i32 { return 0; }`, "type parameter T has a bound at two places in the receiver"},
		{"missing bound", `struct B[T] { v: T }
function (b: B[T: ]) f(): i32 { return 0; }`, "expected"},
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

func TestBoundOutsideReceiverTypeIsRefused(t *testing.T) {
	for _, src := range []string{
		`struct B[T] { v: T }
function f(b: B[T: Eq]): i32 { return 0; }`,
		`struct B[T] { v: T }
function (b: B[T]) f(x: B[U: Eq]): i32 { return 0; }`,
		`struct B[T] { v: T }
function (b: B[T]) f(): B[U: Eq] { return b; }`,
	} {
		if _, err := Parse(src); err == nil {
			t.Errorf("accepted a bound outside a receiver type:\n%s", src)
		}
	}
}

func TestReceiverTypeParamRedeclaredIsP007(t *testing.T) {
	for _, src := range []string{
		`struct B[T] { v: T }
function (b: B[T]) f[T](): T { return b.v; }`,
		`struct B[T] { v: T }
function (b: B[T]) f[T: Eq](): T { return b.v; }`,
		`struct B[T] { v: T }
function (b: B[T: Eq]) f[T: Eq](): T { return b.v; }`,
		`struct M[K, V] { k: K, v: V }
function (m: M[K, V]) f[A, V](a: A): V { return m.v; }`,
		`function (xs: T[]) f[T, K: Ord](k: K): T { return xs[0]; }`,
		`struct B[T] { v: T }
function [T] (b: B[T]) f(): T { return b.v; }`,
	} {
		_, err := Parse(src)
		if err == nil {
			t.Errorf("accepted a receiver type parameter declared again:\n%s", src)
			continue
		}
		if code := firstCode(err); code != "P007" {
			t.Errorf("want P007, got %q: %v\n%s", code, err, src)
		}
		if !strings.Contains(err.Error(), "the receiver introduces type parameter") {
			t.Errorf("want the P007 message, got %v", err)
		}
	}
	for _, src := range []string{
		`struct M[K, V] { k: K, v: V }
function (m: M[K, V]) fold[A](a: A): A { return a; }`,
		`function (xs: T[]) max_by[K: Ord](k: (T) => K): T { return xs[0]; }`,
		`struct B[T] { v: T }
function [U] (b: B[T]) f(u: U): U { return u; }`,
	} {
		if _, err := Parse(src); err != nil {
			t.Errorf("refused a method that declares only its own type parameters: %v\n%s", err, src)
		}
	}
}
