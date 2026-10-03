package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// __fern_arr_inc_elems retains every element of a pointer-element array ahead
// of a copy of it, with rc_inc's guards inline rather than a call per element.
// The program pushes onto a string array another binding still holds, so the
// push copies it; its elements are heap strings and literals, whose static
// boxes keep a negative count. Both arrays are read back and dropped, and no
// count may underflow.
const arrIncElemsProg = `import "std/i32";

@noinline function mk(i: i32): string {
    return "s" + i.to_string();
}

function main(): i32 {
    let xs: string[] = [];
    let i: i32 = 0;
    while (i < 20) {
        if (i % 3 == 0) { xs = xs.append("lit"); } else { xs = xs.append(mk(i)); }
        i = i + 1;
    }
    let keep: string[] = xs;
    let ys: string[] = xs.append("tail");
    if (keep.len() != 20 || ys.len() != 21) { return 1; }
    if (keep[4] != "s4" || ys[4] != "s4" || ys[3] != "lit" || ys[20] != "tail") { return 2; }
    if (__rc_underflow_count() != 0) { return 3; }
    return 42;
}
`

func TestSelfHostArrIncElemsInline(t *testing.T) {
	h := selfHostCLIForHost(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "inc.fern")
	if err := os.WriteFile(src, []byte(arrIncElemsProg), 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "inc.s")
	cmd := exec.Command(h.cli, "-target", "x86-64-linux", "-emit", "asm", "-o", out, src, h.stdlib)
	if combined, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("emitting: %v\n%s", err, combined)
	}
	raw, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	asm := string(raw)
	if !strings.Contains(asm, "call __fn___fern_arr_inc_elems") {
		t.Fatal("the shared push does not retain its elements through __fern_arr_inc_elems")
	}
	at := strings.Index(asm, "__fn___fern_arr_inc_elems.r:\n")
	if at < 0 {
		t.Fatal("no register entry for __fern_arr_inc_elems")
	}
	body := asm[at:]
	body = body[:strings.Index(body, ".Laie_ret:")]
	if strings.Contains(body, "call") || strings.Contains(body, "push") {
		t.Errorf("__fern_arr_inc_elems calls or saves a register per element:\n%s", body)
	}

	for _, tg := range h.targets {
		bin := filepath.Join(dir, tg.target+".bin")
		h.compileWith(t, tg, src, bin)
		if _, got := h.runProduced(t, tg, bin); got != 42 {
			t.Errorf("%s: exit %d, want 42", tg.target, got)
		}
	}
}
