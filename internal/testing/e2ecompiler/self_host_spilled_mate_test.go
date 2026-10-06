package e2ecompiler

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// A value read only by its phi mate, when that mate is already in a frame
// slot, takes the mate's slot. keep carries more values across the calls
// in its loop than the callee-saved registers hold; k changes on one
// iteration in five, and its merge feeds only the loop phi. Given a
// register, the merge loaded k's slot on the unchanged path and stored it
// back on the back edge.
const spilledMateProg = `@noinline function bump(x: i64): i64 {
    return x + 1i64;
}

@noinline function keep(n: i64): i64 {
    let s1: i64 = n;
    let s2: i64 = n + 1i64;
    let s3: i64 = n + 2i64;
    let s4: i64 = n + 3i64;
    let s5: i64 = n + 4i64;
    let s6: i64 = n + 5i64;
    let k: i64 = 0i64;
    let i: i64 = 0i64;
    while (i < 40i64) {
        s1 = bump(s1) + (s1 & 7i64);
        s2 = bump(s2) + (s2 & 7i64);
        s3 = bump(s3) + (s3 & 7i64);
        s4 = bump(s4) + (s4 & 7i64);
        s5 = bump(s5) + (s5 & 7i64);
        s6 = bump(s6) + (s6 & 7i64);
        if (i % 5i64 == 0i64) {
            k = k + 3i64;
        }
        i = i + 1i64;
    }
    return (s1 ^ s2 ^ s3 ^ s4 ^ s5 ^ s6) + k;
}

function main(): i32 {
    if (keep(1i64) != 0I64) {
        return 1;
    }
    return 42;
}
`

var (
	slotLoad   = regexp.MustCompile(`^movq (-\d+\(%rbp\)), (%r\w+)$`)
	jumpOrCall = regexp.MustCompile(`^(?:j\w+|call|ret)\b`)
)

// slotRoundTrip finds a load of a frame slot into a register that, falling
// through labels and with the register unwritten, is stored back into the
// same slot.
func slotRoundTrip(body string) string {
	var lines []string
	for _, l := range strings.Split(body, "\n") {
		if l = strings.TrimSpace(l); l != "" && !strings.HasPrefix(l, ".cfi") {
			lines = append(lines, l)
		}
	}
	for i, l := range lines {
		m := slotLoad.FindStringSubmatch(l)
		if m == nil {
			continue
		}
		slot, reg := m[1], m[2]
		for _, n := range lines[i+1:] {
			if n == "movq "+reg+", "+slot {
				return l + " ... " + n
			}
			if jumpOrCall.MatchString(n) || strings.HasSuffix(n, ", "+reg) || strings.HasSuffix(n, ", "+slot) {
				break
			}
		}
	}
	return ""
}

func spilledMateExpected() int64 {
	n := int64(1)
	s := []int64{n, n + 1, n + 2, n + 3, n + 4, n + 5}
	k := int64(0)
	for i := 0; i < 40; i++ {
		for j := range s {
			s[j] = s[j] + 1 + s[j]&7
		}
		if i%5 == 0 {
			k += 3
		}
	}
	return s[0] ^ s[1] ^ s[2] ^ s[3] ^ s[4] ^ s[5] + k
}

func TestSelfHostSpilledMateSlot(t *testing.T) {
	h := selfHostCLIForHost(t)
	dir := t.TempDir()
	prog := strings.Replace(spilledMateProg, "0I64", fmt.Sprintf("%di64", spilledMateExpected()), 1)
	src := filepath.Join(dir, "mate.fern")
	if err := os.WriteFile(src, []byte(prog), 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "mate.s")
	cmd := exec.Command(h.cli, "-target", "x86-64-linux", "-emit", "asm", "-o", out, src, h.stdlib)
	if combined, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("emitting: %v\n%s", err, combined)
	}
	raw, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	body := selfHostFnBody(t, raw, "keep")
	if !strings.Contains(body, "(%rbp)\n") {
		t.Fatalf("keep spills nothing, so the test no longer has a spilled phi:\n%s", body)
	}
	if rt := slotRoundTrip(body); rt != "" {
		t.Errorf("keep loads a slot and stores it back unchanged: %s\n%s", rt, body)
	}

	for _, tg := range h.targets {
		bin := filepath.Join(dir, tg.target+".bin")
		h.compileWith(t, tg, src, bin)
		if _, got := h.runProduced(t, tg, bin); got != 42 {
			t.Errorf("%s: exit %d, want 42", tg.target, got)
		}
	}
}
