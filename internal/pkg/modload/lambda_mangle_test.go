package modload_test

import (
	"path/filepath"
	"testing"

	"github.com/jakechampion/lang/internal/check/checker"
	"github.com/jakechampion/lang/internal/check/constfold"
	"github.com/jakechampion/lang/internal/pkg/modload"
)

const lambdaMangleLib = `pub function add_one(x: i32): i32 { return x + 1; }

pub function apply(f: (i32) => i32, x: i32): i32 { return f(x); }

// bare module-local fn reference in arg position
pub function via_bare(x: i32): i32 { return apply(add_one, x); }

// module-local CALL inside a lambda body
pub function via_lambda(x: i32): i32 {
    return apply((v: i32): i32 => { return add_one(v) + 10; }, x);
}

// bare module-local fn REFERENCE inside a lambda body
pub function via_lambda_ref(x: i32): i32 {
    return apply((v: i32): i32 => { return apply(add_one, v) + 100; }, x);
}

// lambda param shadowing a module-level function name: stays the param
pub function shadow_param(x: i32): i32 {
    return apply((add_one: i32): i32 => { return add_one * 2; }, x);
}

// lambda body local shadowing a module-level function name
pub function shadow_local(x: i32): i32 {
    return apply((v: i32): i32 => { let add_one: i32 = 7; return v + add_one; }, x);
}

// captured enclosing local shadowing a module-level function name,
// read inside the lambda body
pub function shadow_capture(x: i32): i32 {
    let add_one: i32 = 50;
    return apply((v: i32): i32 => { return v + add_one; }, x);
}
`

const lambdaMangleMain = `import "./lamlib";
function main(): i32 {
    if (lamlib.via_bare(1) != 2) { return 1; }
    if (lamlib.via_lambda(1) != 12) { return 2; }
    if (lamlib.via_lambda_ref(1) != 102) { return 3; }
    if (lamlib.shadow_param(3) != 6) { return 4; }
    if (lamlib.shadow_local(3) != 10) { return 5; }
    if (lamlib.shadow_capture(3) != 53) { return 6; }
    return 0;
}
`

func TestLambdaBodyManglingChecks(t *testing.T) {
	dir := writeFiles(t, map[string]string{
		"lamlib.fern": lambdaMangleLib,
		"main.fern":   lambdaMangleMain,
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
		t.Fatalf("checker rejected mangled lambda-body references: %v", err)
	}
	if findFunc(prog, "lamlib__add_one") == nil {
		t.Errorf("expected mangled lamlib__add_one in merged program; got %v", funcNames(prog))
	}
}
