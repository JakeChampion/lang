package e2e

import "testing"

// Exercise both precise named growth and the conservative nested-growth
// summary. The old and new values must remain independent through every call.
const growBracketNestedFieldsProgram = `
struct Tables { keys: string[], values: i32[] }
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
function valid(s: Scope, names: i32, values: i32): boolean {
    return s.names.len() == names && s.names[0] == "seed"
        && s.tables.keys.len() == 1 && s.tables.keys[0] == "key"
        && s.tables.values.len() == values && s.tables.values[0] == 7;
}
function main(): i32 {
    var s = Scope { names: ["seed"], tables: Tables { keys: ["key"], values: [7] } };
    var a = grow_names(s);
    if (!valid(s, 1, 1) || !valid(a, 2, 1) || a.names[1] != "new") { return 1; }
    var b = grow_tables(s);
    if (!valid(s, 1, 1) || !valid(b, 1, 2) || b.tables.values[1] != 9) { return 2; }
    var c = grow_tables_forward(s);
    if (!valid(s, 1, 1) || !valid(c, 1, 2) || c.tables.values[1] != 9) { return 3; }
    var d = grow_names(c);
    if (!valid(c, 1, 2) || !valid(d, 2, 2) || d.names[1] != "new") { return 4; }
    return 0;
}
`

func TestGrowBracketNestedFieldsInterp(t *testing.T) {
	if got := runInterpExit(t, growBracketNestedFieldsProgram); got != 0 {
		t.Fatalf("interp result = %d, want 0", got)
	}
}

func TestGrowBracketNestedFieldsX86_64(t *testing.T) {
	if _, got := compileAndRunX86_64(t, growBracketNestedFieldsProgram); got != 0 {
		t.Fatalf("x86-64 result = %d, want 0", got)
	}
}

func TestGrowBracketNestedFieldsArm64(t *testing.T) {
	if _, got := compileAndRunArm64(t, growBracketNestedFieldsProgram); got != 0 {
		t.Fatalf("ARM64 result = %d, want 0", got)
	}
}

func TestGrowBracketNestedFieldsWasm(t *testing.T) {
	if got := compileAndRunWasmbinMain(t, growBracketNestedFieldsProgram); got != 0 {
		t.Fatalf("Wasm result = %d, want 0", got)
	}
}
