package checker

import "testing"

const ownedIfPrelude = `struct Box { values: u8[] }
function take(own box: Box): Box { return box; }
function (own box: Box) step(): Box { return box; }
function both(own a: Box, own b: Box): Box { return a; }
`

func TestOwnedIfExpressionLastUse(t *testing.T) {
	for _, body := range []string{
		`let next = if (flag) { take(box) } else { take(box) }; return next;`,
		`let next = if (flag) { box.step() } else { box.step() }; return next;`,
		`let next = if (flag) { if (other) { take(box) } else { take(box) } } else { take(box) }; return next;`,
		`box = if (flag) { take(box) } else { take(box) }; return box;`,
		`let next = if (box.values[0] == 1u8) { take(box) } else { take(box) }; return next;`,
		`let next = if (flag) { let x = take(box); x } else { take(box) }; return next;`,
	} {
		wantOK(t, body, ownedIfPrelude+`function work(flag: boolean, other: boolean): Box { let box = Box { values: [1u8] }; `+body+` } function main(): i32 { return work(true, false).values[0] as i32; }`)
	}
}

func TestOwnedIfExpressionPreservesLaterUses(t *testing.T) {
	for _, body := range []string{
		`let next = if (flag) { take(box) } else { take(box) }; return box;`,
		`let next = if (flag) { both(box, box) } else { take(box) }; return next;`,
		`let next = if (flag) { let next = take(box); let n = box.values[0]; next } else { take(box) }; return next;`,
		`let next = both(if (flag) { take(box) } else { take(box) }, box); return next;`,
		`for i in 0..2 { let next = if (flag) { take(box) } else { take(box) }; } return Box { values: [] };`,
		`let view = () => box.values[0]; let next = if (flag) { take(box) } else { take(box) }; return next;`,
	} {
		wantE051(t, body, ownedIfPrelude+`function work(flag: boolean): Box { let box = Box { values: [1u8] }; `+body+` } function main(): i32 { return 0; }`)
	}
	wantE051(t, "borrowed parameter", ownedIfPrelude+`function work(box: Box, flag: boolean): Box { return if (flag) { take(box) } else { take(box) }; } function main(): i32 { return 0; }`)
}
