package e2eselfhost

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// viewPhiParamAnchorSource merges a COUNTED (`own`) view parameter into a
// loop-carried phi. `v` is reassigned inside the loop, so the header's phi
// takes the parameter itself on the entry edge, and with n == 0 the merge is
// still the parameter's box when the reads after the loop run.
//
// A view phi owns nothing (#9877), so nothing on that edge takes the
// parameter's unit. What holds it is the anchor: a parameter has no anchor row
// of its own, so the merge names the parameter, and a use of the merge is a
// use of it. Without that, `v` is dead at the edge and the frame releases it
// in the entry block, before the merge is read.
const viewPhiParamAnchorSource = `import "std/string";
function loopy(own v: str, t: string, n: i32): i32 {
    var i: i32 = 0;
    while (i < n) { v = slice_unchecked(t, 2, 4); i = i + 1; }
    var pad: string = "zzzz" + "yyyy";
    var k: i32 = 0;
    if (v.len() > 0) { k = v[0] as i32; }
    return v.len() + k + pad.len();
}
function main(): i32 {
    var acc: i32 = 0;
    var r: i32 = 0;
    while (r < 8) {
        var t: string = "abcde" + "fg";
        acc = acc + loopy("ab" + "cd", t, r % 2);
        r = r + 1;
    }
    return acc % 97;
}
`

// TestSelfHostViewPhiHoldsItsParameterSource reads the produced body rather
// than running it. The early release is not observable from the answer: the
// backend has the view's bytes in registers by then, and the box it frees is
// the header, so the program answers correctly and the sanitizer sees nothing
// touched. The placement is the whole of the defect, so the placement is what
// this pins.
func TestSelfHostViewPhiHoldsItsParameterSource(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "fern.fern")
	fernBin := buildSelfHostBin(t, gcc, dir, "fern.fern", "fern")
	stdlibRoot, err := filepath.Abs("../../internal/stdlib")
	if err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(t.TempDir(), "main.fern")
	if err := os.WriteFile(src, []byte(viewPhiParamAnchorSource), 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(filepath.Dir(src), "main.s")
	cmd := runX86_64Bin(runner, fernBin, "-target", "x86-64-linux", "-emit", "asm", src, stdlibRoot, "-o", out)
	cmd.Env = append(os.Environ(), "FERN_SEM_IR=1", "FERN_SEM_IR_ONLY=loopy")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("compile: %v\n%s", err, out)
	}
	asm, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	body := producedFunctionEntryBlock(t, string(asm), "__fn_loopy")
	if strings.Contains(body, "__fern_str_view_free") {
		t.Fatalf("the parameter's view box is released in loopy's entry block, before the merge that reads it:\n%s", body)
	}
}

// producedFunctionEntryBlock returns the text from a function's label up to
// the end of its first basic block, which is where a release of a parameter
// dead at the loop's entry edge lands.
func producedFunctionEntryBlock(t *testing.T, asm, label string) string {
	t.Helper()
	at := strings.Index(asm, "\n"+label+":")
	if at < 0 {
		t.Fatalf("%s is not in the emitted assembly; the body was not produced", label)
	}
	rest := asm[at+len(label)+2:]
	first := strings.Index(rest, ".Lssa_")
	if first < 0 {
		t.Fatalf("%s has no SSA block labels:\n%s", label, rest[:min(len(rest), 400)])
	}
	rest = rest[first:]
	if nl := strings.Index(rest, "\n"); nl >= 0 {
		rest = rest[nl+1:]
	}
	next := strings.Index(rest, ".Lssa_")
	if next < 0 {
		t.Fatalf("%s has only one SSA block; the loop header is missing:\n%s", label, rest[:min(len(rest), 400)])
	}
	return rest[:next]
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
