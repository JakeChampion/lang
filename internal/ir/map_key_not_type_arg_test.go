package ir

import (
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/checker"
	"github.com/jakechampion/lang/internal/parser"
)

// A Map keyed by a tuple, array or slice is refused rather than compiled to
// the default (#10020) — but the refusal is about a KEY, and a generic call's
// TypeArgs are only a key when the callee is a Map or MapIter method. The
// first version asked the question for every generic call, so
//
//	function id[T](x: T): T { return x; }
//	function f(): i32 { var a = [1, 2, 3]; var b = id(a); return b[0]; }
//
// — a program with no map anywhere — was refused for "a Map keyed by i32[]".
// Both directions are pinned here because they are one rule: the array is a
// key in the second program and is not in the first.
func TestGenericTypeArgIsNotAMapKey(t *testing.T) {
	cases := []struct {
		name string
		src  string
	}{
		{
			name: "array",
			src: `function id[T](x: T): T { return x; }
function main(): i32 { var a = [1, 2, 3]; var b = id(a); return b[0]; }`,
		},
		{
			name: "tuple",
			src: `function id[T](x: T): T { return x; }
function main(): i32 { var a = (1, 2); var b = id(a); return b.0; }`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			prog, err := parser.Parse(tc.src)
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			info, err := checker.Check(prog)
			if err != nil {
				t.Fatalf("check: %v", err)
			}
			for _, ptrW := range []int{4, 8} {
				if _, err := LowerWith(prog, info, ptrW); err != nil {
					t.Fatalf("ptrW=%d: no map in this program, so nothing to refuse; got %v", ptrW, err)
				}
			}
		})
	}
}

// The other direction: the same array type, written as a key, is still
// refused and the message names it.
func TestMapKeyedByArrayIsRefused(t *testing.T) {
	src := `function main(): i32 {
	var m: Map[i32[], i32] = map_new(8);
	m = m.insert([1, 2], 5);
	return m.get_or([1, 2], 0);
}`
	prog, err := parser.Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	info, err := checker.Check(prog)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	_, err = LowerWith(prog, info, 8)
	if err == nil {
		t.Fatal("a Map keyed by i32[] lowered; the compiled build reads the default out of every lookup (#10020)")
	}
	if !strings.Contains(err.Error(), "i32[]") {
		t.Fatalf("the refusal should name the key type, got %v", err)
	}
}
