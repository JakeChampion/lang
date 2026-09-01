package ir

import "testing"

// A Map reached as a CONTAINER CHILD — a struct field, a tuple element, a
// closure capture — owes exactly the column walks a Map bound to a local
// owes: the value column, the string-KEY column, then the buf and handle.
//
// The child sites hardcoded `__map_drop_values` and emitted no key walk at
// all, so `struct Tbl { m: Map[string, i32] }` stranded every heap key it
// held while the identical map bound to a local was reclaimed in full
// (#7914, fixed by the shared `appendMapDropChain`).
//
// These assert the emitted CALLS at each child site rather than the shared
// helper, so they hold whatever the dispatch is called and wherever it
// lives: the sibling in ir_test.go covers the bare local, and the corpus
// covers the runtime balance, but nothing else pins that a map reached
// through a struct field or a tuple element emits the key walk at all.
//
// Both ptrW legs run: the two-word ABI boxes each key in a cell and the
// single-word one stores the data pointer directly, but the walk is named
// the same way on both.

func TestStructFieldMapDropWalksTheKeyColumn(t *testing.T) {
	src := `struct Tbl { m: Map[string, i32], count: i32 }
function build(n: i32): Tbl {
    var mm: Map[string, i32] = map_new(8);
    mm = mm.insert("k" + "ey", n);
    return Tbl { m: mm, count: n };
}
function main(): i32 { var t: Tbl = build(3); return t.count; }`

	for _, ptrW := range []int{4, 8} {
		p := lowerSourceWith(t, src, ptrW)
		keys := countDirectCalls(p, "__drop_struct_Tbl", "__drop_map_str_keys")
		bufs := countDirectCalls(p, "__drop_struct_Tbl", "__fern_map_drop")
		if bufs == 0 {
			t.Fatalf("ptrW=%d: __drop_struct_Tbl frees no map at all — the probe stopped measuring the field drop:\n%s", ptrW, p)
		}
		if keys != bufs {
			t.Errorf("ptrW=%d: the Tbl field drop emits %d key-column walks against %d buf-and-handle frees; a string-keyed map owes both wherever it is reached from:\n%s", ptrW, keys, bufs, p)
		}
	}
}

// The value column follows the VALUE type the same way the local drop picks
// it: a string value routes to the string walk rather than the kind-guarded
// default, which only reclaims array values.
func TestStructFieldMapDropWalksStringValues(t *testing.T) {
	src := `struct Tbl { m: Map[string, string], count: i32 }
function build(n: i32): Tbl {
    var mm: Map[string, string] = map_new(8);
    mm = mm.insert("k" + "ey", "v" + "al");
    return Tbl { m: mm, count: n };
}
function main(): i32 { var t: Tbl = build(3); return t.count; }`

	for _, ptrW := range []int{4, 8} {
		p := lowerSourceWith(t, src, ptrW)
		if n := countDirectCalls(p, "__drop_struct_Tbl", "__drop_map_str_values"); n == 0 {
			t.Errorf("ptrW=%d: a Map[string, string] field drop never walks the string VALUE column, so every value buffer it holds is stranded:\n%s", ptrW, p)
		}
		if n := countDirectCalls(p, "__drop_struct_Tbl", "__drop_map_str_keys"); n == 0 {
			t.Errorf("ptrW=%d: a Map[string, string] field drop never walks the string KEY column:\n%s", ptrW, p)
		}
	}
}

// A scalar-keyed map owes no key walk — the credit is keyed on the static
// key type, not emitted for every map.
func TestScalarKeyedMapFieldDropSkipsTheKeyWalk(t *testing.T) {
	src := `struct Tbl { m: Map[i32, i32], count: i32 }
function build(n: i32): Tbl {
    var mm: Map[i32, i32] = map_new(8);
    mm = mm.insert(n, n);
    return Tbl { m: mm, count: n };
}
function main(): i32 { var t: Tbl = build(3); return t.count; }`

	for _, ptrW := range []int{4, 8} {
		p := lowerSourceWith(t, src, ptrW)
		if n := countDirectCalls(p, "__drop_struct_Tbl", "__drop_map_str_keys"); n != 0 {
			t.Errorf("ptrW=%d: a Map[i32, i32] field drop emits %d string-key walks over a column of scalars:\n%s", ptrW, n, p)
		}
	}
}

// The tuple element is the same child site through the same selector.
func TestTupleElementMapDropWalksTheKeyColumn(t *testing.T) {
	src := `function build(n: i32): (Map[string, i32], i32) {
    var mm: Map[string, i32] = map_new(8);
    mm = mm.insert("k" + "ey", n);
    return (mm, n);
}
function main(): i32 { var t: (Map[string, i32], i32) = build(3); return t.1; }`

	for _, ptrW := range []int{4, 8} {
		p := lowerSourceWith(t, src, ptrW)
		keys, bufs := 0, 0
		for _, fn := range p.Funcs {
			for _, op := range fn.Ops {
				if !isNamedCallKind(op.Kind) {
					continue
				}
				switch op.Str {
				case "__drop_map_str_keys":
					keys++
				case "__fern_map_drop":
					bufs++
				}
			}
		}
		if bufs == 0 {
			t.Fatalf("ptrW=%d: nothing frees the tuple's map — the probe stopped measuring:\n%s", ptrW, p)
		}
		if keys == 0 {
			t.Errorf("ptrW=%d: the tuple element's map is freed %d times and its key column is never walked:\n%s", ptrW, bufs, p)
		}
	}
}
