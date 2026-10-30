package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// title and Sink.name are no larger than a call, so each is spliced into all
// three of its callers. title is a free function, so it then loses its body.
// Each returns a string from a cell, which the caller then owns, so neither
// is a leaf.
const tinySpliceProgram = `struct Sink { n: i32, lv: Cell[i32], label: Cell[string] }
function title(s: Sink): string {
    return s.label.get();
}
function (s: Sink) name(): string {
    return s.label.get();
}
@noinline function overflow(s: Sink): i32 {
    s.lv.set(s.n);
    s.label.set("over " + s.n.to_string());
    return s.n;
}
@noinline function kept(s: Sink): i32 {
    if (s.n >= 100) {
        return overflow(s) * 1000;
    }
    if (title(s).len() > 0) {
        return s.lv.get() * 1000 + s.name().len();
    }
    return s.n;
}
@noinline function code(s: Sink): i32 {
    return title(s).len() * 100 + s.name().len();
}
@noinline function failed(s: Sink): boolean {
    return title(s) == s.name();
}
function print_int(n: i32): i32 {
    if (n > 9) { print_int(n / 10); }
    putchar(48 + n % 10);
    return 0;
}
function main(): i32 {
    let total: i32 = 0;
    let s: Sink = Sink { n: 0, lv: cell_new(0), label: cell_new("") };
    let i: i32 = 0;
    while (i < 120) {
        s = Sink { ...s, n: i };
        total = total + kept(s);
        i = i + 1;
    }
    print_int(total); print("");
    print_int(code(s)); print("");
    if (failed(s)) { print("failed"); }
    return 0;
}
`

func TestSelfHostTinySplice(t *testing.T) {
	callers := []string{"kept", "code", "failed"}
	runSemanticProgramSplicing(t, "tiny-splice", tinySpliceProgram, append([]string{"overflow"}, callers...),
		[]string{"title"}, semInlineWants("2194950\n808\nfailed\n"), callers...)

	h := selfHostCLIForHost(t)
	src := filepath.Join(t.TempDir(), "tiny_splice.fern")
	if err := os.WriteFile(src, []byte(tinySpliceProgram), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{"x86-64-linux", "arm64-linux"} {
		path := filepath.Join(filepath.Dir(src), target+".s")
		out, err := exec.Command(h.cli, "-target", target, "-emit", "asm", "-o", path, src, h.stdlib).CombinedOutput()
		if err != nil {
			t.Fatalf("%s: %v\n%s", target, err, out)
		}
		asm, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		call := "call __fn_"
		if target == "arm64-linux" {
			call = "bl __fn_"
		}
		for _, caller := range callers {
			body := condBranchBody(t, string(asm), caller)
			for _, callee := range []string{"title", "Sink__name"} {
				if strings.Contains(body, call+callee+"\n") || strings.Contains(body, call+callee+".r\n") {
					t.Errorf("%s: %s still calls %s", target, caller, callee)
				}
			}
		}
	}
}
