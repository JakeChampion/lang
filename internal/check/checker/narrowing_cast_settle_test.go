package checker

import (
	"strings"
	"testing"
)

func TestNarrowingCastDoesNotSettleItsOperand(t *testing.T) {
	const prelude = `import "std/i32";
import "std/u64";
function main(): i32 {
    let x: i32 = 300;
    let out: u8[] = [];
    let acc: u64 = 0;
`
	cases := []struct {
		name      string
		body      string
		accepted  bool
		wantInMsg string
	}{
		{"loop var, modulo", "for i in 0..4 { out = out.append((i % 256) as u8); }", true, ""},
		{"loop var, multiply", "for i in 0..4 { out = out.append((i * 300) as u8); }", true, ""},
		{"loop var, nested", "for i in 0..4 { out = out.append(((i * 100) % 256) as u8); }", true, ""},

		{"declared local, modulo", "out = out.append((x % 256) as u8);", true, ""},
		{"declared local, multiply", "out = out.append((x * 300) as u8);", true, ""},
		{"declared local, bare", "out = out.append(x as u8);", true, ""},

		{"bare literal out of range", "out = out.append(300 as u8);", false, "300"},
		{"bare literal in range", "out = out.append(200 as u8);", true, ""},

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
