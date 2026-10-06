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

// On x86-64 an ALU op or a compare reads a spilled operand from its frame
// slot rather than loading it into a scratch register first. spread keeps
// eight values live across the calls in its loop, more than the five
// callee-saved registers hold, so some of them live in slots.
const spilledOperandProg = `@noinline function bump(x: i64): i64 {
    return x + 1i64;
}

@noinline function spread(n: i64): i64 {
    let a: i64 = bump(n);
    let b: i64 = bump(a);
    let c: i64 = bump(b);
    let d: i64 = bump(c);
    let e: i64 = bump(d);
    let f: i64 = bump(e);
    let g: i64 = bump(f);
    let h: i64 = bump(g);
    let s: i64 = 0i64;
    let i: i64 = 0i64;
    while (i < 6i64) {
        s = bump(s) + a;
        s = s - b;
        s = s ^ c;
        s = s | d;
        s = (s & e) | f;
        s = s * g;
        if (h > s) {
            s = s + 3i64;
        }
        if (e == 6i64) {
            s = s + 1i64;
        }
        if (b == 3i64) {
            s = s + 7i64;
        }
        if (c > b) {
            s = s + 2i64;
        }
        if (a == 2i64) {
            s = s + 1i64;
        }
        if (d == 5i64) {
            s = s + 1i64;
        }
        if (g == 8i64) {
            s = s + 1i64;
        }
        let lt: boolean = s < h;
        if (lt) {
            s = s + 5i64;
        }
        i = i + 1i64;
    }
    return s + a + b + c + d + e + f + g + h;
}

function main(): i32 {
    if (spread(1i64) != %dI64) {
        return 1;
    }
    return 42;
}
`

var (
	aluFromSlot  = regexp.MustCompile(`(?:addq|subq|xorq|orq|andq|imulq) -\d+\(%rbp\), %r\w+\n`)
	cmpImmToSlot = regexp.MustCompile(`cmpq \$\d+, -\d+\(%rbp\)\n`)
	cmpRegSlot   = regexp.MustCompile(`cmpq (?:-\d+\(%rbp\), %r\w+|%r\w+, -\d+\(%rbp\))\n`)
	slotToRcx    = regexp.MustCompile(`movq -\d+\(%rbp\), %rcx\n`)
)

func spilledOperandExpected() int64 {
	a := int64(2)
	b, c, d, e, f, g, h := a+1, a+2, a+3, a+4, a+5, a+6, a+7
	s := int64(0)
	for i := 0; i < 6; i++ {
		s = s + 1 + a
		s = s - b
		s = s ^ c
		s = s | d
		s = s&e | f
		s = s * g
		if h > s {
			s += 3
		}
		if e == 6 {
			s++
		}
		if b == 3 {
			s += 7
		}
		if c > b {
			s += 2
		}
		if a == 2 {
			s++
		}
		if d == 5 {
			s++
		}
		if g == 8 {
			s++
		}
		if s < h {
			s += 5
		}
	}
	return s + a + b + c + d + e + f + g + h
}

func TestSelfHostSpilledOperandInPlace(t *testing.T) {
	h := selfHostCLIForHost(t)
	dir := t.TempDir()
	prog := strings.ReplaceAll(fmt.Sprintf(spilledOperandProg, spilledOperandExpected()), "I64", "i64")
	src := filepath.Join(dir, "spill.fern")
	if err := os.WriteFile(src, []byte(prog), 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "spill.s")
	cmd := exec.Command(h.cli, "-target", "x86-64-linux", "-emit", "asm", "-o", out, src, h.stdlib)
	if combined, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("emitting: %v\n%s", err, combined)
	}
	raw, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	body := selfHostFnBody(t, raw, "spread")
	if !aluFromSlot.MatchString(body) {
		t.Errorf("no ALU op in spread reads a spilled operand from its slot:\n%s", body)
	}
	if !cmpImmToSlot.MatchString(body) {
		t.Errorf("no compare in spread tests a spilled value against an immediate in its slot:\n%s", body)
	}
	if !cmpRegSlot.MatchString(body) {
		t.Errorf("no compare in spread reads a spilled value from its slot against a register:\n%s", body)
	}
	if m := slotToRcx.FindString(body); m != "" {
		t.Errorf("spread still loads a spilled operand into the scratch: %q", m)
	}

	for _, tg := range h.targets {
		bin := filepath.Join(dir, tg.target+".bin")
		h.compileWith(t, tg, src, bin)
		if _, got := h.runProduced(t, tg, bin); got != 42 {
			t.Errorf("%s: exit %d, want 42", tg.target, got)
		}
	}
}
