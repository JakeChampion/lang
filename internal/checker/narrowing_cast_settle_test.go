package checker

import (
	"strings"
	"testing"
)

// A narrowing cast used to push its target type into its own operand, so
// `(i % 256) as u8` range-checked the 256 against u8 and refused it — the
// canonical way to write a byte wrap, rejected.
//
// It only bit expressions with an UNSETTLED operand, which is what made it
// look arbitrary. `var x: i32 = …; (x % 256) as u8` was accepted all along
// because x had already committed to i32; the same expression over a loop
// variable was rejected because the variable had not. Whether an expression
// type-checked depended on whether its neighbour happened to be declared.
//
// A BARE literal still settles at the target, so an out-of-range constant
// stays the E047 it should be: `300 as u8` is a typo, not arithmetic.
func TestNarrowingCastDoesNotSettleItsOperand(t *testing.T) {
	const prelude = `import "std/i32";
import "std/u64";
function main(): i32 {
    var x: i32 = 300;
    var out: u8[] = [];
    var acc: u64 = 0;
`
	cases := []struct {
		name      string
		body      string
		accepted  bool
		wantInMsg string
	}{
		// The bug: a loop variable is unsettled, so the target reached the
		// literal. These are the shapes that were rejected.
		{"loop var, modulo", "for i in 0..4 { out = out.append((i % 256) as u8); }", true, ""},
		{"loop var, multiply", "for i in 0..4 { out = out.append((i * 300) as u8); }", true, ""},
		{"loop var, nested", "for i in 0..4 { out = out.append(((i * 100) % 256) as u8); }", true, ""},

		// The same expressions over a DECLARED local were always accepted.
		// They are here so the two spellings cannot drift apart again.
		{"declared local, modulo", "out = out.append((x % 256) as u8);", true, ""},
		{"declared local, multiply", "out = out.append((x * 300) as u8);", true, ""},
		{"declared local, bare", "out = out.append(x as u8);", true, ""},

		// A bare out-of-range literal is still an error. This is the case the
		// settling exists for, and loosening it would turn a typo into a
		// silent wrap.
		{"bare literal out of range", "out = out.append(300 as u8);", false, "300"},
		{"bare literal in range", "out = out.append(200 as u8);", true, ""},

		// Widening is unaffected: a literal too wide for i32 must still settle
		// at the target, or it truncates to zero on the way to the IR.
		{"wide literal to u64", "acc = 4611686018427387904 as u64;", true, ""},
		{"wide shift to u64", "acc = (1 << 40) as u64;", true, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := checkSource(t, prelude+c.body+"\n    return 0;\n}\n")
			if c.accepted {
				if err != nil {
					t.Errorf("rejected, want accepted: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatal("accepted, want rejected")
			}
			if !strings.Contains(err.Error(), c.wantInMsg) {
				t.Errorf("message %q does not mention %q", err.Error(), c.wantInMsg)
			}
		})
	}
}
