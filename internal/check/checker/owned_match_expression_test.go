package checker

import "testing"

const ownedMatchPrelude = `struct Box { values: u8[] }
enum Slot { Full(Box), Empty }
function take(own box: Box): Box { return box; }
`

func TestOwnedMatchExpressionResult(t *testing.T) {
	for name, fn := range map[string]string{
		"literal arms":         `function pick(own box: Box, k: string): Box { return match (k) { "a" => take(box), _ => take(box), }; } function feed(own box: Box): Box { return take(pick(box, "a")); }`,
		"binder of owned slot": `function pick(own slot: Slot): Box { return match (slot) { Full(inner) => inner, Empty => Box { values: [] }, }; } function feed(own slot: Slot): Box { return take(pick(slot)); }`,
	} {
		wantOK(t, name, ownedMatchPrelude+fn+` function main(): i32 { return 0; }`)
	}
}

func TestOwnedMatchExpressionBorrowedResult(t *testing.T) {
	for name, fn := range map[string]string{
		"literal arm borrows":     `function pick(box: Box, k: string): Box { return match (k) { "a" => Box { values: [] }, _ => box, }; } function feed(box: Box): Box { return take(pick(box, "a")); }`,
		"binder of borrowed slot": `function pick(slot: Slot): Box { return match (slot) { Full(inner) => inner, Empty => Box { values: [] }, }; } function feed(slot: Slot): Box { return take(pick(slot)); }`,
	} {
		wantE051(t, name, ownedMatchPrelude+fn+` function main(): i32 { return 0; }`)
	}
}
