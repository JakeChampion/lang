package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

const loopLengthProg = `@noinline function pick(a: i32[], s: string, ix: i32[]): i32 {
    let t: i32 = 0;
    for k in 0..ix.len() {
        t = t + a[ix[k]] + s[ix[k]] as i32;
    }
    return t;
}
@noinline function bytes(b: u8[], ix: i32[]): i32 {
    let t: i32 = 0;
    for k in 0..ix.len() {
        t = t + b[ix[k]] as i32;
    }
    return t;
}
@noinline function grow(a0: i32[], ix: i32[]): i32 {
    let a = a0;
    let t: i32 = 0;
    for k in 0..ix.len() {
        t = t + a[ix[k]];
        a = a.append(t);
    }
    return t + a.len();
}
function main(): i32 {
    let n: i32 = args().len();
    let a = [10, 20, 30, 40];
    let none: i32[] = [];
    if (n == 2) {
        return pick(a, "abcd", [1, 4]);
    }
    if (n == 3) {
        return pick(a, "ab", [1, 3]);
    }
    if (n == 4) {
        return grow(a, [0, 9]);
    }
    let raw: u8[] = [7, 200, 13];
    if (n == 5) {
        return bytes(raw, [2, 3]);
    }
    let empty: i32[] = [];
    return pick(a, "abcd", [3, 0, 2]) + pick(empty, "", none) + grow(a, [0, 4, 5]) + bytes(raw, [1, 2, 1]) - 813;
}
`

// A checked read of an array or a string in a loop that cannot change a
// length compares its index with one load of the length before the loop,
// on arm64, and a byte read loads from the address of the elements. A loop
// that grows the array still loads the length on each trip. An index out of range still aborts with the message of an array or
// of a string.
func TestSelfHostLoopLengthLoad(t *testing.T) {
	h := selfHostCLIForHost(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "loop.fern")
	if err := os.WriteFile(src, []byte(loopLengthProg), 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "arm64.s")
	if combined, err := exec.Command(h.cli, "-O", "-target", "arm64-linux", "-emit", "asm", "-o", out, src, h.stdlib).CombinedOutput(); err != nil {
		t.Fatalf("emitting: %v\n%s", err, combined)
	}
	asm, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	// A check that loads the length itself compares the index with x6.
	ownLoad := regexp.MustCompile(`cmp x\d+, x6\n`)
	if body := condBranchBody(t, string(asm), "pick"); ownLoad.MatchString(body) || !strings.Contains(body, "b __fern_oob_abort") || !strings.Contains(body, "b __fern_str_oob_abort") {
		t.Errorf("pick loads a length in a check, or has lost a check:\n%s", body)
	}
	// A byte read loads from two registers: the address of the elements,
	// formed before the loop, and the index.
	if body := condBranchBody(t, string(asm), "bytes"); ownLoad.MatchString(body) || !regexp.MustCompile(`ldrb w\d+, \[x\d+, x\d+\]\n`).MatchString(body) {
		t.Errorf("bytes loads a length in a check, or reads a byte through an add:\n%s", body)
	}
	if body := condBranchBody(t, string(asm), "grow"); !ownLoad.MatchString(body) {
		t.Errorf("grow compares with a length from before the loop that grows the array:\n%s", body)
	}
	for _, tg := range h.targets {
		bin := filepath.Join(dir, tg.target+".bin")
		if combined, err := exec.Command(h.cli, "-O", "-target", tg.target, "-o", bin, src, h.stdlib).CombinedOutput(); err != nil {
			t.Fatalf("%s: building: %v\n%s", tg.target, err, combined)
		}
		for _, run := range []struct {
			args []string
			code int
			text string
		}{
			{nil, 23, ""},
			{[]string{"x"}, 134, "array index out of range"},
			{[]string{"x", "y"}, 134, "string index out of range"},
			{[]string{"x", "y", "z"}, 134, "array index out of range"},
			{[]string{"x", "y", "z", "w"}, 134, "array index out of range"},
		} {
			cmd := exec.Command(bin, run.args...)
			if len(tg.runner) > 0 {
				cmd = exec.Command(tg.runner[0], append(append(tg.runner[1:], bin), run.args...)...)
			}
			got, _ := cmd.CombinedOutput()
			if code := cmd.ProcessState.ExitCode(); code != run.code || !strings.Contains(string(got), run.text) {
				t.Errorf("%s %v: exit %d, output %q; want exit %d with %q", tg.target, run.args, code, got, run.code, run.text)
			}
		}
	}
}
