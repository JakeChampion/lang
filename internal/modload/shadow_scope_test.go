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
