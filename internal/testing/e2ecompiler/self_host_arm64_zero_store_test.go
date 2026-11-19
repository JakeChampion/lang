package e2ecompiler

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

const arm64ZeroStoreProgram = `enum List { Nil, Cons(i64, List) }

@noinline function zero_head(t: List): List {
    return Cons(0i64, t);
}

@noinline function seven_head(t: List): List {
    return Cons(7i64, t);
}

@noinline function sum(xs: List, n: i64): i64 {
    return match (xs) { Cons(h, t) => sum(t, n + h + 1i64), Nil => n };
}

function main(): i32 {
    return sum(zero_head(seven_head(zero_head(Nil))), 0i64) as i32;
}
`

// On arm64 a store of the constant zero stores the zero register, and makes
// no zero in a register first. Another constant stays in a register.
func TestSelfHostArm64ZeroStore(t *testing.T) {
	h := selfHostCLIForHost(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "zero_store.fern")
	if err := os.WriteFile(src, []byte(arm64ZeroStoreProgram), 0o644); err != nil {
		t.Fatal(err)
	}
	zeroMove := regexp.MustCompile(`mov x\d+, #0\n`)
	for _, tg := range h.targets {
		if strings.HasPrefix(tg.target, "arm64") {
			asmPath := filepath.Join(dir, "zero_store-"+tg.target+".s")
			h.compileWith(t, tg, src, asmPath, "-emit", "asm")
			asm, err := os.ReadFile(asmPath)
			if err != nil {
				t.Fatal(err)
			}
			zero := functionListing(string(asm), "__fn_zero_head")
			if !strings.Contains(zero, "str xzr, [") || zeroMove.MatchString(zero) {
				t.Errorf("%s: zero_head does not store the zero register:\n%s", tg.target, zero)
			}
			seven := functionListing(string(asm), "__fn_seven_head")
			if strings.Contains(seven, "xzr") || !strings.Contains(seven, "#7") {
				t.Errorf("%s: seven_head does not store its constant from a register:\n%s", tg.target, seven)
			}
		}
		bin := filepath.Join(dir, "zero_store-"+tg.target)
		h.compileWith(t, tg, src, bin)
		if _, code := h.runProduced(t, tg, bin); code != 10 {
			t.Errorf("%s: exit = %d, want 10 (0 + 7 + 0, and 1 for each of 3 cells)", tg.target, code)
		}
	}
}
