package e2ecompiler

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const arm64SharedStaticBoxProgram = `enum List { Nil, Cons(i32, List) }

@noinline function both(a: List, b: List): i32 {
    let n: i32 = 0;
    match (a) { Nil => { n = n + 1; }, Cons(h, t) => { n = n + h; } }
    match (b) { Nil => { n = n + 2; }, Cons(h, t) => { n = n + h; } }
    return n;
}

@noinline function noise(n: i32): i32 {
    return n * 3;
}

// The call between the two tests ends the reach of the first address.
@noinline function across(a: List, b: List): i32 {
    let n: i32 = 0;
    match (a) { Nil => { n = n + 1; }, Cons(h, t) => { n = n + h; } }
    n = noise(n);
    match (b) { Nil => { n = n + 2; }, Cons(h, t) => { n = n + h; } }
    return n;
}

function main(): i32 {
    let one: List = Cons(10, Nil);
    return both(Nil, Nil) + both(one, Nil) * 10 + across(Nil, one) * 100 + across(one, Nil);
}
`

// On arm64 two tests of a variant with no field form the address of its
// static box once when no call is between them, and once for each test when
// a call is.
func TestSelfHostArm64SharedStaticBox(t *testing.T) {
	h := selfHostCLIForHost(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "shared_box.fern")
	if err := os.WriteFile(src, []byte(arm64SharedStaticBoxProgram), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, tg := range h.targets {
		if strings.HasPrefix(tg.target, "arm64") {
			asmPath := filepath.Join(dir, "shared_box-"+tg.target+".s")
			h.compileWith(t, tg, src, asmPath, "-emit", "asm")
			asm, err := os.ReadFile(asmPath)
			if err != nil {
				t.Fatal(err)
			}
			for name, want := range map[string]int{"__fn_both": 1, "__fn_across": 2} {
				fn := functionListing(string(asm), name)
				got := 0
				for _, line := range strings.Split(fn, "\n") {
					if strings.Contains(line, "adrp") && strings.Contains(line, ".Kv") {
						got++
					}
				}
				if got != want {
					t.Errorf("%s: %s forms the address of the static box %d times, want %d:\n%s", tg.target, name, got, want, fn)
				}
			}
		}
		bin := filepath.Join(dir, "shared_box-"+tg.target)
		h.compileWith(t, tg, src, bin)
		if _, code := h.runProduced(t, tg, bin); code != 175 {
			t.Errorf("%s: exit = %d, want 175", tg.target, code)
		}
	}
}
