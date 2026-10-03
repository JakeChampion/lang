package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// A value a function returns asks the allocator for the result register, and
// a phi passes the ask to the values it merges, so neither is moved into
// %rax on the way out. pick returns a merge of two arms; bucket returns the
// sign extension of a division's remainder.
const returnInResultProg = `@noinline function pick(a: i64, b: i64): i64 {
    let r: i64 = 0i64;
    if (a > b) {
        r = a - b;
    } else {
        r = b * 3i64;
    }
    return r;
}

@noinline function bucket(s: string, n: i32): i32 {
    let h: i32 = 7;
    let i: i32 = 0;
    while (i < s.len()) {
        h = h * 31 + s[i] as i32;
        i = i + 1;
    }
    return (h & 1073741823) % n;
}

function main(): i32 {
    if (pick(10i64, 3i64) != 7i64 || pick(2i64, 5i64) != 15i64) {
        return 1;
    }
    if (bucket("abc", 7) != bucket("abc", 7) || bucket("abc", 1) != 0) {
        return 2;
    }
    return 42;
}
`

var movIntoRax = regexp.MustCompile(`^movq %r\w+, %rax$`)

// lastBeforeRet is the instruction a function body runs last before each
// ret, past the labels, unwind notes and register restores between them.
func lastBeforeRet(body string) []string {
	var lines []string
	for _, l := range strings.Split(body, "\n") {
		l = strings.TrimSpace(l)
		if l == "" || strings.HasPrefix(l, ".") || strings.HasSuffix(l, ":") || strings.HasPrefix(l, "popq") {
			continue
		}
		lines = append(lines, l)
	}
	var out []string
	for i, l := range lines {
		if l == "ret" && i > 0 {
			out = append(out, lines[i-1])
		}
	}
	return out
}

func TestSelfHostReturnValueInResultRegister(t *testing.T) {
	h := selfHostCLIForHost(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "ret.fern")
	if err := os.WriteFile(src, []byte(returnInResultProg), 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "ret.s")
	cmd := exec.Command(h.cli, "-target", "x86-64-linux", "-emit", "asm", "-o", out, src, h.stdlib)
	if combined, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("emitting: %v\n%s", err, combined)
	}
	raw, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	for _, fn := range []string{"pick", "bucket"} {
		body := selfHostFnBody(t, raw, fn)
		last := lastBeforeRet(body)
		if len(last) == 0 {
			t.Fatalf("%s has no ret:\n%s", fn, body)
		}
		for _, l := range last {
			if movIntoRax.MatchString(l) {
				t.Errorf("%s moves its result into %%rax before returning (%q):\n%s", fn, l, body)
			}
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
