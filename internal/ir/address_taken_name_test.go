package ir

import (
	"testing"

	"github.com/jakechampion/lang/internal/checker"
	"github.com/jakechampion/lang/internal/parser"
	"github.com/jakechampion/lang/internal/shadowrename"
)

// A parameter or local that only shares a function's name is not a use of the
// function's value, so the function stays on the pair-form return ABI. Runs
// the rename the pipeline runs first, which is what makes the name exact.
func TestAddressTakenIgnoresLocalsSharingAFunctionName(t *testing.T) {
	src := `function value(n: i32): Option[i32] {
    if (n < 0) { return None; }
    return Some(n);
}
function shift(value: i32): i32 {
    let find: i32 = value + 1;
    return find;
}
function find(n: i32): Option[i32] {
    if (n == 0) { return None; }
    return Some(n);
}
function main(): i32 {
    let f: (i32) => Option[i32] = find;
    match (value(shift(1))) {
        Some(v) => { return v; },
        None => { return 0; }
    }
    return 0;
}`
	prog, err := parser.Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	info, err := checker.Check(prog)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	shadowrename.Rename(prog, info)
	taken := addressTakenFuncs(prog, info)
	if taken["value"] {
		t.Errorf("value is marked address-taken by a parameter that shares its name")
	}
	if !taken["find"] {
		t.Errorf("find is read as a function value in main and is not marked address-taken")
	}
	pairForm := findPairFormFuncs(prog, info, 8, taken)
	if !pairForm["value"] {
		t.Errorf("value is not pair-form")
	}
}
