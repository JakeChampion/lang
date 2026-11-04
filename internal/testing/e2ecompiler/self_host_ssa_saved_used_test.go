package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// A compare that a branch fuses is never written, so it takes no register. It
// once took one past the caller-saved set, and `__fern_str_cmp` saved and
// restored %r12 for it while its body never used %r12.
const savedUsedProg = `@noinline function less(a: string, b: string): boolean { return a < b; }
function main(): i32 {
    let names: string[] = ["delta", "alpha", "charlie", "bravo"];
    let n: i32 = 0;
    let i: i32 = 0;
    while (i + 1 < names.len()) {
        if (less(names[i], names[i + 1])) {
            n = n + 1;
        }
        i = i + 1;
    }
    return n;
}
`

var savedRegPush = regexp.MustCompile(`^\s+pushq %(rbx|r12|r13|r14|r15)$`)
var savedRegPop = regexp.MustCompile(`^\s+popq %(rbx|r12|r13|r14|r15)$`)
var asmLabel = regexp.MustCompile(`^([A-Za-z_][\w.$]*):\s*$`)

// unusedSaves names each compiled function that saves a callee-saved register
// its body never reads or writes.
func unusedSaves(asm string) []string {
	var bad []string
	cur := ""
	saves := map[string]bool{}
	var body strings.Builder
	flush := func() {
		if cur == "" {
			return
		}
		text := body.String()
		for r := range saves {
			if !regexp.MustCompile(`%` + r + `[dwb]?\b`).MatchString(text) {
				bad = append(bad, cur+" "+r)
			}
		}
	}
	for _, line := range strings.Split(asm, "\n") {
		if m := asmLabel.FindStringSubmatch(line); m != nil {
			name := m[1]
			if strings.HasSuffix(name, ".r") || strings.HasSuffix(name, ".u") {
				continue
			}
			flush()
			cur, saves = "", map[string]bool{}
			body.Reset()
			if strings.HasPrefix(name, "__fn_") {
				cur = name
			}
			continue
		}
		if m := savedRegPush.FindStringSubmatch(line); m != nil {
			saves[m[1]] = true
			continue
		}
		if savedRegPop.MatchString(line) || strings.Contains(line, ".cfi") {
			continue
		}
		body.WriteString(line + "\n")
	}
	flush()
	return bad
}

func TestSelfHostSSASavedRegistersUsed(t *testing.T) {
	h := selfHostCLIForHost(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "saved.fern")
	if err := os.WriteFile(src, []byte(savedUsedProg), 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "saved.s")
	if combined, err := exec.Command(h.cli, "-target", "x86-64-linux", "-emit", "asm", "-o", out, src, h.stdlib).CombinedOutput(); err != nil {
		t.Fatalf("emitting: %v\n%s", err, combined)
	}
	asm, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(asm), "__fn___fern_str_cmp:") {
		t.Fatal("the program does not reach __fern_str_cmp, so it checks nothing")
	}
	if bad := unusedSaves(string(asm)); len(bad) > 0 {
		t.Errorf("functions save registers they never use: %v", bad)
	}
}

// A runtime function compiled from Fern has a `.r` entry that takes its
// arguments in registers, so a call to it pushes nothing.
func TestSelfHostSSARuntimeRegisterCalls(t *testing.T) {
	h := selfHostCLIForHost(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "regcall.fern")
	if err := os.WriteFile(src, []byte(savedUsedProg), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ target, call string }{
		{"x86-64-linux", "call __fn___fern_str_cmp"},
		{"arm64-linux", "bl __fn___fern_str_cmp"},
	} {
		out := filepath.Join(dir, tc.target+".s")
		if combined, err := exec.Command(h.cli, "-target", tc.target, "-emit", "asm", "-o", out, src, h.stdlib).CombinedOutput(); err != nil {
			t.Fatalf("%s: emitting: %v\n%s", tc.target, err, combined)
		}
		asm, err := os.ReadFile(out)
		if err != nil {
			t.Fatal(err)
		}
		text := string(asm)
		if !strings.Contains(text, tc.call+".r\n") {
			t.Errorf("%s: no call reaches __fern_str_cmp's register entry", tc.target)
		}
		if strings.Contains(text, tc.call+"\n") {
			t.Errorf("%s: a call still reaches __fern_str_cmp's stack entry", tc.target)
		}
	}
}
