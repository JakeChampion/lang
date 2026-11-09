package checker

import "testing"

const freshSpreadPrelude = `
struct Buffer { values: i32[], tag: i32 }
function fresh(): Buffer { return Buffer { values: [7, 8], tag: 0 }; }
function (own value: Buffer) consume(): i32 { return value.values[0] + value.tag; }
function take(own value: Buffer): i32 { return value.values[0] + value.tag; }
`

func TestOwnGuardAllowsFreshSpreadReceivers(t *testing.T) {
	for _, body := range []struct{ name, src string }{
		{"call", `let value = Buffer { ...fresh(), tag: 5 }; return value.consume() - 12;`},
		{"nested", `let value = Buffer { ...Buffer { ...fresh(), tag: 3 }, tag: 5 }; return value.consume() - 12;`},
		{"literal", `let value = Buffer { ...Buffer { values: [7, 8], tag: 0 }, tag: 5 }; return value.consume() - 12;`},
		{"function-control", `let value = Buffer { ...fresh(), tag: 5 }; return take(value) - 12;`},
		{"branch", `let flag = true; let value = Buffer { ...(if (flag) { fresh() } else { fresh() }), tag: 5 }; return value.consume() - 12;`},
	} {
		t.Run(body.name, func(t *testing.T) {
			wantOK(t, body.name, freshSpreadPrelude+"function main(): i32 { "+body.src+" }")
		})
	}
}

const freshSpreadHandlePrelude = `
struct Holder { sock: Option[Socket], tag: i32 }
function make(): Holder { return Holder { sock: None, tag: 0 }; }
function lend(value: Holder): Holder { return value; }
function fresh_from(value: Holder): Holder { return Holder { sock: None, tag: value.tag }; }
function (own value: Holder) consume(): i32 { return value.tag; }
`

func TestOwnGuardFreshSpreadRetainsBorrowChecks(t *testing.T) {
	wantOK(t, "independent-result", freshSpreadHandlePrelude+`
function f(value: Holder): i32 { let copy = Holder { ...fresh_from(value), tag: 5 }; return copy.consume(); }
function main(): i32 { return f(make()) - 5; }`)
	wantE051(t, "borrowed-call", freshSpreadHandlePrelude+`
function f(value: Holder): i32 { let copy = Holder { ...lend(value), tag: 5 }; return copy.consume(); }
function main(): i32 { return 0; }`)
	wantE051(t, "borrowed-nested-call", freshSpreadHandlePrelude+`
function f(value: Holder): i32 { let copy = Holder { ...Holder { ...lend(value), tag: 4 }, tag: 5 }; return copy.consume(); }
function main(): i32 { return 0; }`)
	wantE051(t, "named-base-live", freshSpreadHandlePrelude+`
function f(): i32 { let value = make(); let copy = Holder { ...value, tag: 5 }; let n = copy.consume(); return n + value.tag; }
function main(): i32 { return 0; }`)
	wantE051(t, "receiver-read-later", freshSpreadHandlePrelude+`
function f(): i32 { let value = Holder { ...make(), tag: 5 }; let n = value.consume(); return n + value.tag; }
function main(): i32 { return 0; }`)
}
