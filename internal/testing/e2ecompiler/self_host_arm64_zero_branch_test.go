package e2ecompiler

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

const arm64ZeroBranchProgram = `@noinline function first_zero(xs: i32[]): i32 {
    let i: i32 = 0;
    while (i < xs.len()) {
        if (xs[i] == 0) { return i; }
        i = i + 1;
    }
    return -1;
}

@noinline function count_set(xs: i64[]): i32 {
    let n: i32 = 0;
    for x in xs {
        if (x != 0i64) { n = bump(n); }
    }
    return n;
}

@noinline function bump(n: i32): i32 {
    return n + 1;
}

// The select needs the flags of the compare, so the compare stays.
@noinline function pick(x: i32): i32 {
    return if (x == 0) { 7 } else { 9 };
}

function main(): i32 {
    return first_zero([4, 3, 0, 1]) * 100 + first_zero([1, 2]) + count_set([0i64, 5i64, 0i64, -1i64]) * 10 + pick(0) + pick(2);
}
`

// A branch on an integer compare with zero for equality tests the register
// with cbz or cbnz on arm64, and sets no flags.
func TestSelfHostArm64ZeroCompareBranch(t *testing.T) {
	h := selfHostCLIForHost(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "zero_branch.fern")
	if err := os.WriteFile(src, []byte(arm64ZeroBranchProgram), 0o644); err != nil {
		t.Fatal(err)
	}
	zeroCompare := regexp.MustCompile(`cmp [xw]\d+, #0\n\s+b\.(eq|ne) `)
	for _, tg := range h.targets {
		if strings.HasPrefix(tg.target, "arm64") {
			asmPath := filepath.Join(dir, "zero_branch-"+tg.target+".s")
			h.compileWith(t, tg, src, asmPath, "-emit", "asm")
			asm, err := os.ReadFile(asmPath)
			if err != nil {
				t.Fatal(err)
			}
			for _, name := range []string{"__fn_first_zero", "__fn_count_set"} {
				fn := functionListing(string(asm), name)
				if !regexp.MustCompile(`\scbn?z [xw]\d+, `).MatchString(fn) {
					t.Errorf("%s: %s has no cbz or cbnz:\n%s", tg.target, name, fn)
				}
				if zeroCompare.MatchString(fn) {
					t.Errorf("%s: %s compares with zero and then branches:\n%s", tg.target, name, fn)
				}
			}
			if pick := functionListing(string(asm), "__fn_pick"); !strings.Contains(pick, "csel") || !strings.Contains(pick, "cmp ") {
				t.Errorf("%s: pick lost the compare of its select:\n%s", tg.target, pick)
			}
		}
		bin := filepath.Join(dir, "zero_branch-"+tg.target)
		h.compileWith(t, tg, src, bin)
		if _, code := h.runProduced(t, tg, bin); code != 235 {
			t.Errorf("%s: exit = %d, want 235 (200 - 1 + 20 + 7 + 9)", tg.target, code)
		}
	}
}
