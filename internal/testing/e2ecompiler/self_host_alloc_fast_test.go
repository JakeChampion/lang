package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

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
	shim = shim[:strings.Index(shim, "__fern_str_box:")]
	fast := shim[:strings.Index(shim, "ret\n")]
	if strings.Contains(fast, "pushq %rbp") || strings.Contains(fast, "call") || !strings.Contains(fast, "pushq (%rax)") {
		t.Errorf("__fern_arr_box's fast path builds a frame or calls out instead of popping its class:\n%s", fast)
	}
	if !strings.Contains(shim, "call __fern_alloc") {
		t.Errorf("__fern_arr_box has no allocator call for a miss:\n%s", shim)
	}

	for _, tg := range h.targets {
		bin := filepath.Join(dir, tg.target+".bin")
		h.compileWith(t, tg, src, bin)
		if _, got := h.runProduced(t, tg, bin); got != 42 {
			t.Errorf("%s: exit %d, want 42", tg.target, got)
		}
	}
}
