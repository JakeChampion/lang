package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// __fern_arr_push copies a full or shared array into its new box eight
// elements to the round through ymm moves, then four, then one at a time. The
// program grows arrays to every length from 1 to 70, so each doubling copies
// 4, 8, 16, 32 and 64 elements, and pushes onto a shared copy at every length,
// which copies every remainder of eight; every element is read back.
const arrPushCopyProg = `function check(xs: i32[], n: i32): boolean {
    if (xs.len() != n) { return false; }
    let i: i32 = 0;
    while (i < n) {
        if (xs[i] != i * 7 + 1) { return false; }
        i = i + 1;
    }
    return true;
}

function main(): i32 {
    let n: i32 = 1;
    while (n <= 70) {
        let xs: i32[] = [];
        let i: i32 = 0;
        while (i < n) { xs = xs.append(i * 7 + 1); i = i + 1; }
        if (!check(xs, n)) { return 1; }
        let shared: i32[] = xs;
        let grown: i32[] = xs.append(n * 7 + 1);
        if (!check(grown, n + 1)) { return 2; }
        if (!check(shared, n)) { return 3; }
        n = n + 1;
    }
    return 42;
}
`

func TestSelfHostArrPushCopy(t *testing.T) {
	h := selfHostCLIForHost(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "push.fern")
	if err := os.WriteFile(src, []byte(arrPushCopyProg), 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "push.s")
	cmd := exec.Command(h.cli, "-target", "x86-64-linux", "-emit", "asm", "-o", out, src, h.stdlib)
	if combined, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("emitting: %v\n%s", err, combined)
	}
	asm, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	start := strings.Index(string(asm), "__fern_arr_push:\n")
	if start < 0 {
		t.Fatal("no __fern_arr_push in the listing")
	}
	body := string(asm)[start:]
	if end := strings.Index(body, "leave\n    ret\n"); end >= 0 {
		body = body[:end]
	}
	for _, want := range []string{"vmovdqu %ymm1, 32(%rdi)", "vzeroupper"} {
		if !strings.Contains(body, want) {
			t.Errorf("__fern_arr_push's grow copy lacks %q:\n%s", want, body)
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
