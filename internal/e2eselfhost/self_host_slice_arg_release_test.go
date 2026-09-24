package e2eselfhost

import (
	"os"
	"path/filepath"
	"testing"
)

// An array slice passed straight to a borrowing callee is released by an
// AST-lowered caller (#9843). `arr_slice` copies the window into a fresh
// buffer, and before this nothing freed it: one leaked array per call, while
// the same slice bound to a local first was released. i32, i64 and f64
// elements cover both slice widths.
const sliceArgReleaseSrc = `function sum(xs: [i32]): i32 { var t: i32 = 0; var i: i32 = 0; while (i < xs.len()) { t = t + xs[i]; i = i + 1; } return t; }
function sum64(xs: [i64]): i64 { var t: i64 = 0 as i64; for x in xs { t = t + x; } return t; }
function sumf(xs: [f64]): f64 { var t: f64 = 0.0; for x in xs { t = t + x; } return t; }
function main(): i32 {
    var words: i32[] = [10, 20, 30, 40, 50, 60];
    var wide: i64[] = [1 as i64, 2 as i64, 3 as i64, 4 as i64];
    var reals: f64[] = [0.5, 1.5, 2.5, 3.5];
    var t: i32 = 0;
    var i: i32 = 0;
    while (i < 3) {
        t = t + sum(words[i:i + 2]) + (sum64(wide[i:i + 1]) as i32) + (sumf(reals[i:i + 2]) as i32);
        i = i + 1;
    }
    return t;
}
`

// 150 from the i32 slices, 6 from the i64 slices, 2 + 4 + 6 from the f64 ones.
const sliceArgReleaseWant = 168

func TestSelfHostSliceArgReleaseAST(t *testing.T) {
	cli := buildSelfHostCLI(t)
	src := filepath.Join(t.TempDir(), "slice_arg_release.fern")
	if err := os.WriteFile(src, []byte(sliceArgReleaseSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	bin := cli.x86Binary(t, src, "FERN_SEM_IR=", "FERN_SANITIZE=1", "FERN_LEAKCHECK=1")
	stderr, exit := runWithStdin(t, cli.runner, bin, nil)
	if exit != sliceArgReleaseWant {
		t.Fatalf("exit=%d, want %d (stderr %q)", exit, sliceArgReleaseWant, stderr)
	}
	allocs, frees, _ := leakSummaryOf(t, "slice_arg_release", stderr)
	if allocs != frees {
		t.Fatalf("allocs=%d frees=%d, want every slice released (stderr %q)", allocs, frees, stderr)
	}
}
