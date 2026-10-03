package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// A request of at most 256 words is served from __fern_alloc's frameless
// head: a freed block of its class is popped, and a miss bumps the arena,
// both with %rax and %rdi alone. Only a larger request, or the first one
// before the arena is mapped, builds the frame. __fern_arr_box keeps its one
// live value across the call on the stack rather than in a frame. The program
// allocates and frees boxes of several sizes, so both head paths and the
// framed path all run.
const allocFastProg = `function build(n: i32): i32[] {
    let xs: i32[] = [];
    let i: i32 = 0;
    while (i < n) { xs = xs.append(i); i = i + 1; }
    return xs;
}

function main(): i32 {
    let total: i32 = 0;
    let round: i32 = 0;
    while (round < 20) {
        let small: i32[] = build(3);
        let mid: i32[] = build(40);
        let large: i32[] = build(400);
        total = total + small[2] + mid[39] + large[399];
        round = round + 1;
    }
    if (total != 20 * (2 + 39 + 399)) { return 1; }
    return 42;
}
`

func TestSelfHostAllocFastPath(t *testing.T) {
	h := selfHostCLIForHost(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "alloc.fern")
	if err := os.WriteFile(src, []byte(allocFastProg), 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "alloc.s")
	cmd := exec.Command(h.cli, "-target", "x86-64-linux", "-emit", "asm", "-o", out, src, h.stdlib)
	if combined, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("emitting: %v\n%s", err, combined)
	}
	asm, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	start := strings.Index(string(asm), "__fern_alloc:\n")
	if start < 0 {
		t.Fatal("no __fern_alloc in the listing")
	}
	body := string(asm)[start:]
	frame := strings.Index(body, "pushq %rbp")
	if frame < 0 {
		t.Fatalf("__fern_alloc builds no frame at all:\n%s", body)
	}
	head := body[:frame]
	for _, want := range []string{"pushq (%rax)", "movq %rdi, __fern_heap_ptr(%rip)"} {
		at := strings.Index(head, want)
		if at < 0 || !strings.Contains(head[at:], "ret") {
			t.Errorf("__fern_alloc's frameless head lacks %q followed by a return:\n%s", want, head)
		}
	}

	box := strings.Index(string(asm), "__fern_arr_box:\n")
	if box < 0 {
		t.Fatal("no __fern_arr_box in the listing")
	}
	shim := string(asm)[box:]
	shim = shim[:strings.Index(shim, "ret\n")]
	if strings.Contains(shim, "pushq %rbp") || !strings.Contains(shim, "call __fern_alloc") {
		t.Errorf("__fern_arr_box builds a frame around its allocation:\n%s", shim)
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
