package checker

import (
	"os"
	"testing"

	"github.com/jakechampion/lang/internal/pkg/modload"
)

func TestOwnedExclusiveBranches(t *testing.T) {
	source, err := os.ReadFile("testdata/owned_branches.fern")
	if err != nil {
		t.Fatal(err)
	}
	program, _, err := modload.LoadSource(string(source))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Check(program); err != nil {
		t.Fatal(err)
	}
}

func TestOwnedBranchJoinsPreserveErrors(t *testing.T) {
	const prefix = `struct Box { data: i32[] }
struct Wrap { state: Box }
struct Pair { first: Box, second: Box }
function consume(own box: Box): i32 { return box.data[0]; }
function keep(own box: Box): Box { return box; }
function guard(own box: Box): boolean { return box.data[0] == 0; }
enum Choice { Wrapped(Box), Empty }
struct Callable { Wrapped: (Box) => Box }
`
	for _, tc := range []struct{ name, source string }{
		{"post expression", `function f(own box: Box, tag: i32): i32 { let n = match (tag) { 0 => consume(box), _ => 0 }; return n + consume(box); }`},
		{"expression order", `function f(own box: Box, tag: i32): i32 { return (match (tag) { 0 => consume(box), _ => 0 }) + box.data[0]; }`},
		{"post conditional", `function f(own box: Box, flag: boolean): i32 { let n = if (flag) { consume(box) } else { 0 }; return n + consume(box); }`},
		{"post nested value", `function f(own box: Box, flag: boolean): i32 { let n = if (flag) { if (flag) { consume(box) } else { 0 } } else { 0 }; return n + consume(box); }`},
		{"post value block", `function f(own box: Box): i32 { let n = { consume(box) }; return n + consume(box); }`},
		{"post statement", `function f(own box: Box, tag: i32): i32 { let n = 0; match (tag) { 0 => { n = consume(box); }, _ => {} } return n + consume(box); }`},
		{"same arm", `function f(own box: Box, tag: i32): i32 { return match (tag) { 0 => consume(box) + consume(box), _ => 0 }; }`},
		{"expression guard fallthrough", `function f(own box: Box, tag: i32): i32 { return match (tag) { 0 when guard(box) => 0, _ => consume(box) }; }`},
		{"statement guard fallthrough", `function f(own box: Box, tag: i32): i32 { match (tag) { 0 when guard(box) => { return 0; }, _ => { return consume(box); } } }`},
		{"statement guard and body", `function f(own box: Box, tag: i32): i32 { match (tag) { 0 when guard(box) => { return consume(box); }, _ => { return 0; } } }`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := checkSource(t, prefix+tc.source); !hasCode(err, "E050") {
				t.Fatalf("want E050, got %v", err)
			}
		})
	}
	for _, tc := range []struct{ name, source string }{
		{"borrowed return field", `function f(box: Box, flag: boolean): Wrap { if (flag) { return Wrap { state: keep(box) }; } return Wrap { state: box }; }`},
		{"duplicate return field", `function f(flag: boolean): Pair { let box = Box { data: [42] }; if (flag) { return Pair { first: keep(box), second: keep(box) }; } return Pair { first: box, second: box }; }`},
		{"borrowed statement guard", `function f(box: Box, tag: i32): i32 { match (tag) { 0 when guard(box) => { return 1; }, _ => { return 0; } } }`},
		{"borrowed payload", `function f(choice: Choice): i32 { return match (choice) { Choice.Wrapped(item) => consume(item), Choice.Empty => 0 }; }`},
		{"shadowed constructor", `function f(box: Box): i32 { let Choice = Callable { Wrapped: (value: Box): Box => value }; return consume(Choice.Wrapped(box)); }`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := checkSource(t, prefix+tc.source); !hasCode(err, "E051") {
				t.Fatalf("want E051, got %v", err)
			}
		})
	}
}
