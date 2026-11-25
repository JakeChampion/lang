package e2ecompiler

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

// le16 reads two bytes of win, and the compiler joins them into one load with
// one check. TestSelfHostGuardBounds* check the values and the traps.
const groupCheckSrc = `@noinline
function le16(win: u8[], cur: i32, best: i32): i32 {
  let at = cur + best - 1;
  return win[at] as i32 | (win[at + 1] as i32) << 8;
}

function main(): i32 {
  let xs: u8[] = [1, 2, 3, 4, 5, 6];
  return le16(xs, 3, 2) - 5 - 6 * 256;
}
`

// The check of a joined read compares the zero-extended first index plus the
// span with the length. It has no shift of the sign and no OR, and the index
// has no sign extension.
func TestSelfHostGroupCheckIsOneUnsignedCompare(t *testing.T) {
	src := filepath.Join(t.TempDir(), "main.fern")
	if err := os.WriteFile(src, []byte(groupCheckSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		target, compare string
		absent          []string
		most            int
	}{
		{e2eharness.TargetX86_64Linux, "cmpq", []string{"sarq", "orq", "movslq"}, 6},
		{e2eharness.TargetArm64Linux, "cmp ", []string{"asr", "orr", "sxtw"}, 12},
	}
	for _, c := range cases {
		t.Run(c.target, func(t *testing.T) {
			asm := e2eharness.EmitAsmWithSelfHost(t, e2eharness.SelfHostCLI(t), c.target, src)
			body := fmaSection(t, asm, ".Lssa_le16_0:", ".cfi_endproc")
			for _, op := range c.absent {
				if strings.Contains(body, op) {
					t.Fatalf("le16 has %q:\n%s", op, body)
				}
			}
			if n := strings.Count(body, c.compare); n != 1 {
				t.Fatalf("le16 has %d compares, want 1:\n%s", n, body)
			}
			insts := 0
			for _, ln := range strings.Split(body, "\n") {
				ln = strings.TrimSpace(ln)
				if ln != "" && !strings.HasPrefix(ln, ".") && !strings.HasSuffix(ln, ":") {
					insts++
				}
			}
			if insts > c.most {
				t.Fatalf("le16 has %d instructions, want at most %d:\n%s", insts, c.most, body)
			}
		})
	}
}
