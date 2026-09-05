package modload_test

// A local binding shadows a module decl of the same name, so the import
// rewriter must leave every reference to it alone. It collects those names
// with collectLocals, which walked statements only — and two binding
// positions are not reachable that way (#6993):
//
//   - a `defer { … }` action is a BlockExpr, i.e. an expression, so nothing
//     inside it was ever visited;
//   - a match arm's `@` whole-value binding rides the arm rather than its
//     Bindings list.
//
// Either one left the local out of the set, so the reference was mangled to
// `<mod>__<name>` and resolved to the module decl: `error[E001]: undefined
// identifier "shadowlib__K"` here, and a silent read of the wrong value
// wherever the two happen to share a type. collectLocals now runs over
// ast.Walk, which reaches both.

import (
	"path/filepath"
	"testing"

	"github.com/jakechampion/lang/internal/checker"
	"github.com/jakechampion/lang/internal/constfold"
	"github.com/jakechampion/lang/internal/modload"
)

const shadowScopeLib = `pub const K: string = "hundred";

enum Box { Full(i32), Empty }

function total(b: Box): i32 { match (b) { Full(v) => { return v; }, Empty => { return 0; } } return 0; }

// A defer action's block declares a local shadowing the module const.
pub function defer_local(): i32 {
    var out: i32 = 3;
    defer { var K: i32 = 5; out = K + 1; }
    return out;
}

// The @ whole-value binding shadows the module const.
pub function at_binding(): i32 {
    var b: Box = Full(4);
    match (b) {
        K @ Full(v) => { return total(K) + v; },
        Empty => { return 0; },
    }
    return 0;
}

pub function name_len(): i32 { return K.len(); }
`

const shadowScopeMain = `import "./shadowlib";
function main(): i32 {
    if (shadowlib.defer_local() != 3) { return 1; }
    if (shadowlib.at_binding() != 8) { return 2; }
    if (shadowlib.name_len() != 7) { return 3; }
    return 0;
}
`

// TestShadowedLocalsInDeferAndAtBinding pins that both binding positions
// suppress the import rewrite. The const is a `string` and both locals are
// `i32`, so a mangled reference cannot merely read the wrong value — it fails
// the checker, which is what makes this a gate rather than a coincidence.
func TestShadowedLocalsInDeferAndAtBinding(t *testing.T) {
	dir := writeFiles(t, map[string]string{
		"shadowlib.fern": shadowScopeLib,
		"main.fern":      shadowScopeMain,
	})
	entry := filepath.Join(dir, "main.fern")
	prog, _, err := modload.Load(entry)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if err := constfold.Fold(prog, nil); err != nil {
		t.Fatalf("constfold: %v", err)
	}
	if _, err := checker.Check(prog); err != nil {
		t.Fatalf("checker rejected a local shadowing a module const: %v", err)
	}
	// The module's own functions still mangle — only the shadowing locals
	// are exempt.
	if findFunc(prog, "shadowlib__at_binding") == nil {
		t.Errorf("expected mangled shadowlib__at_binding in merged program; got %v", funcNames(prog))
	}
}

// The same rule, for the pattern positions collectLocals could not see at
// all: a tuple pattern's elements, a payload SUB-PATTERN's binders, and the
// expression form of `match`, which the walk did not visit in any shape
// (#8607). Each binder shadows the module function `helper`, so a mangled
// reference resolves to `shadowlib__helper` and the checker reports
// `operator "+" requires an integer type, got () => i32`.
const shadowArmLib = `function helper(): i32 { return 100; }

enum Inner { Some1(i32), None1 }
enum Outer2 { Holds(Inner), Empty }

pub function tuple_binder(): i32 {
    var t: (i32, i32) = (3, 4);
    match (t) {
        (helper, b) => { return helper + b; }
    }
}

pub function nested_tuple_binder(): i32 {
    var t: (i32, (i32, i32)) = (1, (2, 4));
    match (t) {
        (a, (helper, c)) => { return a + helper + c; }
    }
}

pub function tuple_variant_elem_binder(): i32 {
    var t: (Inner, i32) = (Some1(3), 4);
    match (t) {
        (Some1(helper), b) => { return helper + b; },
        _ => { return 0; }
    }
}

pub function payload_sub_binder(): i32 {
    var o: Outer2 = Holds(Some1(3));
    match (o) {
        Holds(Some1(helper)) => { return helper + 4; },
        Holds(None1()) => { return 0; },
        Empty => { return 0; }
    }
}

pub function match_expr_tuple_binder(): i32 {
    var t: (i32, i32) = (3, 4);
    return match (t) {
        (helper, b) => helper + b
    };
}

pub function match_expr_variant_binder(): i32 {
    var i: Inner = Some1(3);
    return match (i) {
        Some1(helper) => helper + 4,
        None1 => 0
    };
}
`

const shadowArmMain = `import "./shadowlib";
function main(): i32 {
    if (shadowlib.tuple_binder() != 7) { return 1; }
    if (shadowlib.nested_tuple_binder() != 7) { return 2; }
    if (shadowlib.tuple_variant_elem_binder() != 7) { return 3; }
    if (shadowlib.payload_sub_binder() != 7) { return 4; }
    if (shadowlib.match_expr_tuple_binder() != 7) { return 5; }
    if (shadowlib.match_expr_variant_binder() != 7) { return 6; }
    return 0;
}
`

// TestShadowedLocalsInTupleAndSubPatterns pins that every binder position of
// both arm forms suppresses the import rewrite. The shadowed decl is a
// FUNCTION and every binder an `i32`, so a mangled reference is a type error
// rather than a value that merely happens to be wrong.
func TestShadowedLocalsInTupleAndSubPatterns(t *testing.T) {
	dir := writeFiles(t, map[string]string{
		"shadowlib.fern": shadowArmLib,
		"main.fern":      shadowArmMain,
	})
	entry := filepath.Join(dir, "main.fern")
	prog, _, err := modload.Load(entry)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if err := constfold.Fold(prog, nil); err != nil {
		t.Fatalf("constfold: %v", err)
	}
	if _, err := checker.Check(prog); err != nil {
		t.Fatalf("checker rejected a match binder shadowing a module function: %v", err)
	}
	// The module's own decls still mangle — only the shadowing binders are
	// exempt.
	if findFunc(prog, "shadowlib__helper") == nil {
		t.Errorf("expected mangled shadowlib__helper in merged program; got %v", funcNames(prog))
	}
}
