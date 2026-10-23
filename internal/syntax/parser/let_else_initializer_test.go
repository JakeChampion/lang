package parser

import "testing"

func TestLetElseFullInitializer(t *testing.T) {
	for _, source := range []string{
		`function main(): i32 { let Some(n) = { Some(3) } else { return 1; }; return n; }`,
		`function main(): i32 { let 3 = { 3 } else { return 1; }; return 0; }`,
		`struct Box { n: i32 } function (b: Box) get(): Option[i32] { return Some(b.n); }
function main(): i32 { let Some(n) = Box { n: 3 }.get() else { return 1; }; return n; }`,
	} {
		if _, err := Parse(source); err != nil {
			t.Fatalf("initializer: %v\n%s", err, source)
		}
	}
}
