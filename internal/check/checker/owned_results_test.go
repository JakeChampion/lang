package checker

import "testing"

const ownedResultPrelude = `struct Box { values: u8[] } function (own box: Box) close(): void {}`

func TestCheckedOwnedResultRefusesStrengthening(t *testing.T) {
	err := checkSource(t, ownedResultPrelude+`function pass(own x: Box): Box { return x; }
function main(): i32 { let f: (own Box) => own Box = pass; return 0; }`)
	if err == nil || !hasCode(err, "E003") {
		t.Fatalf("expected E003, got %v", err)
	}
}

func TestCheckedOwnedTraitResult(t *testing.T) {
	err := checkSource(t, ownedResultPrelude+`trait Factory { function make(): own Self; } impl Factory for Box { function make(): Self { return Box { values: [] }; } } function main(): i32 { return 0; }`)
	if err == nil || !hasCode(err, "E021") {
		t.Fatalf("expected E021, got %v", err)
	}
}
