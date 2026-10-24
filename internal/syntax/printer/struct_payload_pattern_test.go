package printer

import (
	"strings"
	"testing"
)

func TestFormatKeepsRestInsideAPayloadSlot(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{`S(Pt { x, .. })`, "S(Pt { x, .. })"},
		{`S(Pt { x, y })`, "S(Pt { x, y })"},
		{`S(Pt { .. })`, "S(Pt { .. })"},
	} {
		src := `struct Pt { x: i32, y: i32, z: i32 }
enum H { S(Pt), N(i32) }
function f(h: H): i32 {
  match (h) {
    ` + tc.in + ` => { return 0; },
    N(n) => { return n; }
  }
}`
		got := formatSrc(t, src)
		if !strings.Contains(got, tc.want) {
			t.Errorf("formatting %q: want %q in\n%s", tc.in, tc.want, got)
		}
	}
}

func TestFormatKeepsSubPatternOnANamedField(t *testing.T) {
	src := `enum In { Ok2(i32), Er2(i32) }
struct Box { v: In, tag: i32 }
enum H { S(Box), N(i32) }
function f(h: H): i32 {
  match (h) {
    S(Box { v: Ok2(n), .. }) => { return n; },
    N(n) => { return n; },
    _ => { return 0; }
  }
}`
	got := formatSrc(t, src)
	if !strings.Contains(got, "S(Box { v: Ok2(n), .. })") {
		t.Errorf("named-field sub-pattern should round-trip; got:\n%s", got)
	}
}
