package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// A named type's release frees a box it solely owns without building a frame:
// it runs the type's drop and then frees the box, holding the box on the stack
// across both calls. The program builds and drops records that own a string
// and an array, so that path runs on every round.
const releaseTailProg = `struct P { name: string, xs: i32[] }

@noinline function make(i: i32): P {
    let xs: i32[] = [i, i + 1, i + 2];
    return P { name: "p" + "q", xs: xs };
}

function main(): i32 {
    let total: i32 = 0;
    let i: i32 = 0;
    while (i < 50) {
        let p: P = make(i);
        total = total + p.xs[2] + p.name.len();
        i = i + 1;
    }
    if (total != 50 * 2 + 49 * 50 / 2 + 50 * 2) { return 1; }
    return 42;
}
`

func TestSelfHostReleaseUniqueTail(t *testing.T) {
	h := selfHostCLIForHost(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "release.fern")
	if err := os.WriteFile(src, []byte(releaseTailProg), 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "release.s")
	cmd := exec.Command(h.cli, "-target", "x86-64-linux", "-emit", "asm", "-o", out, src, h.stdlib)
	if combined, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("emitting: %v\n%s", err, combined)
	}
	asm, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	loc := regexp.MustCompile(`(?m)^__fn___sem_release_\w*P\.r:\n`).FindIndex(asm)
	if loc == nil {
		t.Fatal("no release helper for P in the listing")
	}
	body := string(asm[loc[1]:])
	frame := strings.Index(body, "pushq %rbp")
	if frame < 0 {
		frame = len(body)
	}
	head := body[:frame]
	guard := strings.Index(head, "cmpl $1, -8(%rax)\n    jne ")
	drop := strings.Index(head, "call __fn___sem_drop_")
	free := strings.Index(head, "call __fn___fern_arr_dec.r")
	if drop < 0 || free < drop {
		t.Errorf("P's release does not drop and then free a unique box before building a frame:\n%s", head)
	}
	// Only a count of exactly one may take that path: a zero falls to the
	// body, whose helper reports the underflow.
	if guard < 0 || guard > drop {
		t.Errorf("P's release drops without first branching away every count but one:\n%s", head)
	}

	for _, tg := range h.targets {
		bin := filepath.Join(dir, tg.target+".bin")
		cmd := exec.Command(h.cli, "-target", tg.target, "-o", bin, src, h.stdlib)
		if combined, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%s: building: %v\n%s", tg.target, err, combined)
		}
		run := exec.Command(bin)
		if len(tg.runner) > 0 {
			run = exec.Command(tg.runner[0], append(tg.runner[1:], bin)...)
		}
		_ = run.Run()
		if got := run.ProcessState.ExitCode(); got != 42 {
			t.Errorf("%s: exit %d, want 42", tg.target, got)
		}
	}
}
