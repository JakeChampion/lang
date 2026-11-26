package checker

import (
	"testing"
)

func TestCallCopiesDoNotStrengthenFunctionTypes(t *testing.T) {
	err := checkSource(t, `function loan(x: i32[]): i32[] { return x; } function main(): i32 { let f: (i32[]) => own i32[] = loan; return 0; }`)
	if err == nil || !hasCode(err, "E003") {
		t.Fatalf("expected E003, got %v", err)
	}
}
