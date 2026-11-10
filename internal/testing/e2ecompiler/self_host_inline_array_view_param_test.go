package e2ecompiler

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// An array view parameter is spliced into its one caller, as a string view
// parameter is. Here the body makes a call, so it is not a leaf. The caller's
// reads then go through its own array, so the loop allocates nothing. A loop
// that rebinds a view keeps its call.
const inlineArrayViewParamProgram = `@noinline function fresh(n: i32): u8[] {
    let out: u8[] = [];
    let i: i32 = 0;
    while (i < n) {
        out = out.append((97 + i % 26) as u8);
        i = i + 1;
    }
    return out;
}
@noinline function letter(b: u8): boolean {
    return b >= 97 as u8 && b <= 122 as u8;
}
function lower(s: [u8], at: i32): boolean {
    return letter(s[at]);
}
function plain(buf: [u8], from: i32, end: i32): boolean {
    let i: i32 = from;
    while (i < end) {
        if (!lower(buf, i)) { return false; }
        i = i + 1;
    }
    return end - from == buf.len();
}
@noinline function checked(buf: u8[]): boolean {
    return plain(buf, 0, buf.len());
}
function halves(v: [u8]): i32 {
    let w: [u8] = v;
    let n: i32 = 0;
    while (w.len() > 1) {
        w = w[0:w.len() / 2];
        n = n + 1;
    }
    return n + w[0] as i32;
}
@noinline function exercise(): i32 {
    let base: u8[] = fresh(40);
    let before: i64 = __heap_alloc_count();
    let i: i32 = 0;
    while (i < 100) {
        if (!checked(base)) { return 1; }
        i = i + 1;
    }
    if (__heap_alloc_count() != before) { return 2; }
    let bad: u8[] = base.with(3, 48 as u8);
    if (checked(bad)) { return 3; }
    if (halves(base[1:9]) != 101) { return 4; }
    if (__rc_underflow_count() != 0) { return 5; }
    return 0;
}
function main(): i32 {
    let result: i32 = exercise();
    if (result != 0) { return result; }
    print("ok");
    return 0;
}
`

func TestSelfHostInlineArrayViewParams(t *testing.T) {
	runSemanticProgramSplicing(t, "array-view-params", inlineArrayViewParamProgram,
		[]string{"fresh", "letter", "checked", "halves", "exercise"}, []string{"plain"},
		semInlineWants("ok\n"), "fresh", "letter", "checked", "halves", "exercise")
}

// Once plain is spliced into checked, its reads and its length go to the
// array that checked lends. No lend of the array stays.
func TestSelfHostLentReadsReadTheArray(t *testing.T) {
	h := selfHostCLIForHost(t)
	src := filepath.Join(t.TempDir(), "array_view_params.fern")
	if err := os.WriteFile(src, []byte(inlineArrayViewParamProgram), 0o644); err != nil {
		t.Fatal(err)
	}
	if checked := semanticDumpOf(t, h, src, "checked"); strings.Contains(checked, "array_lend") {
		t.Errorf("checked still lends its array to the reads of plain:\n%s", checked)
	}
	if halves := semanticDumpOf(t, h, src, "halves"); !strings.Contains(halves, "arr_slice") {
		t.Errorf("halves lost the slice that its loop rebinds:\n%s", halves)
	}
}
