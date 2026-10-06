package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The runtime's rc helpers refuse a pointer below 0x10000 before reading its
// header, and null is one, so none tests for null first. The program releases
// arrays, strings, a map, an array of arrays and an array of records, which
// pulls in arr_dec, rc_inc, rc_is_unique, str_free, the deep array free, the
// map free and the record array's element walk.
const lowGuardProg = `import "std/i32";
import "core/map";

struct Q { s: string }

function main(): i32 {
    let total: i32 = 0;
    let round: i32 = 0;
    while (round < 30) {
        let xs: i32[] = [round, round + 1];
        let ys: i32[] = xs;
        let s: string = "r" + round.to_string();
        let grid: i32[][] = [xs, [round]];
        let m: Map[string, i32] = map_new(4);
        m = m.insert(s, round);
        let qs: Q[] = [Q { s: s }, Q { s: "q" }];
        total = total + ys[1] + grid[1][0] + s.len() + m.len() + qs[0].s.len();
        round = round + 1;
    }
    if (total <= 0) { return 1; }
    return 42;
}
`

// A null test of a register followed by a low-address guard of the same
// register to the same label, whichever register holds the pointer. Each
// match is register, label, register, label.
var (
	x86NullThenLow   = regexp.MustCompile(`testq (%r\w+), %r\w+\n\s+jz (\S+)\n(?:\s+testb \$1, %\w+\n\s+jnz \S+\n)?\s+cmpq \$0x10000, (%r\w+)\n\s+jb (\S+)\n`)
	arm64NullThenLow = regexp.MustCompile(`cbz (x\d+), (\S+)\n(?:\s+tbnz x\d+, #0, \S+\n)?\s+(?:mov x9, #0x10000\n\s+cmp (x\d+), x9|cmp (x\d+), #16, lsl #12)\n\s+b\.?lo (\S+)\n`)
)

func TestSelfHostRuntimeLowGuardCoversNull(t *testing.T) {
	h := selfHostCLIForHost(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "guard.fern")
	if err := os.WriteFile(src, []byte(lowGuardProg), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		target, entry, low string
		pair               *regexp.Regexp
	}{
		{"x86-64-linux", "__fn___fern_arr_dec.r:\n", "cmpq $0x10000, %rax", x86NullThenLow},
		{"arm64-linux", "__fn___fern_arr_dec.r:\n", "cmp x0, #16, lsl #12", arm64NullThenLow},
	} {
		out := filepath.Join(dir, c.target+".s")
		cmd := exec.Command(h.cli, "-target", c.target, "-emit", "asm", "-o", out, src, h.stdlib)
		if combined, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%s: emitting: %v\n%s", c.target, err, combined)
		}
		raw, err := os.ReadFile(out)
		if err != nil {
			t.Fatal(err)
		}
		asm := string(raw)
		for _, m := range c.pair.FindAllStringSubmatch(asm, -1) {
			// The arm64 pattern names the guarded register in one of two
			// groups, by which compare form the guard takes.
			reg, label := m[3], m[len(m)-1]
			if len(m) == 6 && reg == "" {
				reg = m[4]
			}
			if m[1] == reg && m[2] == label {
				t.Errorf("%s: a null test precedes a low-address guard to the same label:\n%s", c.target, m[0])
			}
		}
		at := strings.Index(asm, c.entry)
		if at < 0 {
			t.Fatalf("%s: no %q in the listing", c.target, strings.TrimSpace(c.entry))
		}
		head := asm[at+len(c.entry):]
		if first := strings.TrimSpace(head[:strings.Index(head, "\n")]); first != c.low {
			t.Errorf("%s: arr_dec opens with %q, want the low-address guard %q", c.target, first, c.low)
		}
	}

	for _, tg := range h.targets {
		bin := filepath.Join(dir, tg.target+".bin")
		h.compileWith(t, tg, src, bin)
		if _, got := h.runProduced(t, tg, bin); got != 42 {
			t.Errorf("%s: exit %d, want 42", tg.target, got)
		}
	}
}
