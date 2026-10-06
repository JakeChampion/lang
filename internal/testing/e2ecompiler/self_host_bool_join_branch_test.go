package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// A predicate spliced into its caller brings its `&&` or `||` as a boolean
// phi, and a branch on that phi used to test again what the arms had decided:
// a setcc and a zero-extend on one arm, a constant on the other, then a test.
// ssa.thread_bool_joins sends each arm to the branch's targets instead, so
// `digits`, `skip_space` and `word_char` must materialise no flag and call no
// predicate. `flag` returns the same predicate as a value, so it still
// materialises one, which keeps the absence in the other three from passing
// vacuously.
const boolJoinBranchProg = `function is_digit(c: i32): boolean {
    return c >= 48 && c <= 57;
}

function is_space(c: i32): boolean {
    return c == 32 || c == 9 || c == 10;
}

function is_lower(c: i32): boolean {
    return c >= 97 && c <= 122;
}

@noinline function digits(s: string, from: i32): i32 {
    let a: i32 = from;
    while (a < s.len()) {
        if (!is_digit(s[a] as i32)) {
            break;
        }
        a = a + 1;
    }
    return a - from;
}

@noinline function skip_space(s: string, from: i32): i32 {
    let a: i32 = from;
    while (a < s.len() && is_space(s[a] as i32)) {
        a = a + 1;
    }
    return a;
}

@noinline function word_char(c: i32): i32 {
    if (is_lower(c) || c == 95) {
        return 1;
    }
    return 0;
}

@noinline function flag(c: i32): boolean {
    return is_digit(c);
}

function main(): i32 {
    if (digits("12345x", 0) != 5 || digits("x1", 0) != 0 || digits("x1", 1) != 1 || digits("", 0) != 0 || digits("/09:", 1) != 2) {
        return 1;
    }
    if (skip_space("  \t\nx", 0) != 4 || skip_space("x ", 0) != 0 || skip_space("   ", 0) != 3 || skip_space("a\rb", 1) != 1) {
        return 2;
    }
    if (word_char(96) != 0 || word_char(97) != 1 || word_char(122) != 1 || word_char(123) != 0 || word_char(95) != 1 || word_char(94) != 0) {
        return 3;
    }
    if (!flag(48) || !flag(57) || flag(47) || flag(58)) {
        return 4;
    }
    return 42;
}
`

func TestSelfHostBoolJoinBranch(t *testing.T) {
	h := selfHostCLIForHost(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "booljoin.fern")
	if err := os.WriteFile(src, []byte(boolJoinBranchProg), 0o644); err != nil {
		t.Fatal(err)
	}
	flagOp := map[string]*regexp.Regexp{
		"x86-64-linux": regexp.MustCompile(`\n\s+set[a-z]+ `),
		"arm64-linux":  regexp.MustCompile(`\n\s+cset `),
	}
	for _, target := range []string{"x86-64-linux", "arm64-linux"} {
		out := filepath.Join(dir, target+".s")
		if combined, err := exec.Command(h.cli, "-O", "-target", target, "-emit", "asm", "-o", out, src, h.stdlib).CombinedOutput(); err != nil {
			t.Fatalf("%s: emitting: %v\n%s", target, err, combined)
		}
		asm, err := os.ReadFile(out)
		if err != nil {
			t.Fatal(err)
		}
		for _, fn := range []string{"digits", "skip_space", "word_char"} {
			body := condBranchBody(t, string(asm), fn)
			if flagOp[target].MatchString(body) {
				t.Errorf("%s: %s materialises the predicate it branches on:\n%s", target, fn, body)
			}
			if strings.Contains(body, "__fn_is_") {
				t.Errorf("%s: %s calls its predicate rather than splicing it, so the shape above is not this pass's:\n%s", target, fn, body)
			}
		}
		if body := condBranchBody(t, string(asm), "flag"); !flagOp[target].MatchString(body) {
			t.Errorf("%s: flag materialises no flag either, so the absence above proves nothing:\n%s", target, body)
		}
	}
	for _, tg := range h.targets {
		bin := filepath.Join(dir, tg.target+".bin")
		if combined, err := exec.Command(h.cli, "-O", "-target", tg.target, "-o", bin, src, h.stdlib).CombinedOutput(); err != nil {
			t.Fatalf("%s: building: %v\n%s", tg.target, err, combined)
		}
		run := exec.Command(bin)
		if len(tg.runner) > 0 {
			run = exec.Command(tg.runner[0], append(tg.runner[1:], bin)...)
		}
		_ = run.Run()
		if got := run.ProcessState.ExitCode(); got != 42 {
			t.Errorf("%s: exit %d, want 42 (1: digits, 2: skip_space, 3: word_char, 4: flag)", tg.target, got)
		}
	}
}
