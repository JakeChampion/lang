package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// On x86-64 the register path calls __fern_alloc_reuse through its register
// entry, the token in %rax and the slot count in %rsi, rather than pushing
// both; the stack entry loads them there and falls into it. The program
// rebuilds a record from itself in a loop, so each round hands its donor to
// the reuse.
const allocReuseEntryProg = `struct Acc { names: string[], tag: string, n: i32 }

function add(own a: Acc, s: string): Acc {
    a = Acc { ...a, names: a.names.append(s), n: a.n + 1 };
    return a;
}

function main(): i32 {
    let a: Acc = Acc { names: [], tag: "t", n: 0 };
    let i: i32 = 0;
    while (i < 40) { a = add(a, "x"); i = i + 1; }
    if (__rc_underflow_count() != 0) { return 99; }
    if (a.n != 40 || a.names.len() != 40 || a.tag != "t") { return 1; }
    return 42;
}
`

var allocReuseStackEntry = regexp.MustCompile(`__fn___fern_alloc_reuse:\n\s+movq 16\(%rsp\), %rax\n\s+movq 8\(%rsp\), %rsi\n__fn___fern_alloc_reuse\.r:\n`)

func TestSelfHostAllocReuseRegisterEntry(t *testing.T) {
	boxedProbes(t)
	h := selfHostCLIForHost(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "reuse.fern")
	if err := os.WriteFile(src, []byte(allocReuseEntryProg), 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "reuse.s")
	cmd := exec.Command(h.cli, "-target", "x86-64-linux", "-emit", "asm", "-o", out, src, h.stdlib)
	if combined, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("emitting: %v\n%s", err, combined)
	}
	raw, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	asm := string(raw)
	if !allocReuseStackEntry.MatchString(asm) {
		t.Error("__fern_alloc_reuse's stack entry does not load the token and slots and fall into its register entry")
	}
	reg := strings.Count(asm, "call __fn___fern_alloc_reuse.r\n")
	stack := strings.Count(asm, "call __fn___fern_alloc_reuse\n")
	if reg == 0 || stack != 0 {
		t.Errorf("the reuse is called %d times through the register entry and %d through the stack entry, want only the register entry", reg, stack)
	}

	for _, tg := range h.targets {
		bin := filepath.Join(dir, tg.target+".bin")
		h.compileWith(t, tg, src, bin)
		if _, got := h.runProduced(t, tg, bin); got != 42 {
			t.Errorf("%s: exit %d, want 42", tg.target, got)
		}
	}
}
