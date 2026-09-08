package ir

import "testing"

// A named growth in one field cannot mutate unrelated nested fields. Keep
// their root field identity when enumerating nested array paths, without
// weakening the conservative bracket for an unresolved nested growth.
func TestGrowBracketExcludesUnrelatedNestedFields(t *testing.T) {
	const src = `struct Tables { keys: string[], values: i32[] }
struct Scope { names: string[], tables: Tables }
function grow_names(s: Scope): Scope {
    return Scope { ...s, names: s.names.append("new") };
}
function grow_tables(s: Scope): Scope {
    return Scope { ...s, tables: Tables { ...s.tables, values: s.tables.values.append(9) } };
}
function grow_table_arg(t: Tables): Tables {
    return Tables { ...t, values: t.values.append(9) };
}
function grow_tables_forward(s: Scope): Scope {
    return Scope { ...s, tables: grow_table_arg(s.tables) };
}
function observe(s: Scope): i32 {
    return s.names.len() + s.tables.keys.len() + s.tables.values.len();
}
function direct(s: Scope): i32 {
    var r: Scope = grow_names(s);
    return observe(r) + observe(s);
}
function nested(s: Scope): i32 {
    var r: Scope = grow_tables(s);
    return observe(r) + observe(s);
}
function forwarded(s: Scope): i32 {
    var r: Scope = grow_tables_forward(s);
    return observe(r) + observe(s);
}
function main(): i32 { return 0; }`
	for _, ptrW := range []int{4, 8} {
		p := lowerSourceWith(t, src, ptrW)
		for _, tc := range []struct {
			name string
			want int
		}{
			{"direct", 1},
			// A nested mutation is still summarised conservatively as *.
			{"nested", 3},
			// Forwarding a struct field names that subtree, not its siblings.
			{"forwarded", 2},
		} {
			if got := countRcDecs(p, tc.name); got != tc.want {
				t.Errorf("ptrW=%d %s: %d bracket releases, want %d", ptrW, tc.name, got, tc.want)
			}
		}
	}
}
