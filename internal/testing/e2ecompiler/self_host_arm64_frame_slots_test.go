package e2ecompiler

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

const arm64FrameSlotsProgram = `enum List { Nil, Cons(i64, List) }

@noinline function noise(n: i64): i64 {
    return n * 3i64;
}

// The loop tests Nil in each turn, and the call keeps registers live across it.
@noinline function total(xs: List): i64 {
    let sum: i64 = 0i64;
    let cur: List = xs;
    let go: boolean = true;
    while (go) {
        match (cur) {
            Cons(x, rest) => { sum = noise(sum) % 1000i64 + x; cur = rest; },
            Nil => { go = false; },
        }
    }
    return sum;
}

@noinline function count_up(from: i64, n: i64): List {
    if (n == 0i64) { return Nil; }
    return Cons(from, count_up(from + 1i64, n - 1i64));
}

// The ninth and tenth parameters arrive on the stack and survive a call.
@noinline function weighted(a: i32, b: i32, c: i32, d: i32, e: i32, f: i32, g: i32, h: i32, i: i32, j: i32): i32 {
    return noise((a + b + c + d + e + f + g + h) as i64) as i32 + 3 * i + 5 * j;
}

function main(): i32 {
    if (weighted(1, 2, 3, 4, 5, 6, 7, 8, 9, 10) != 185) { return 1; }
    return (total(count_up(1i64, 40i64)) % 200i64) as i32;
}
`

// On arm64 the frame record reserves the saved-register slots, and each
// register is saved and restored at a fixed positive offset from x29. A loop that
// tests a variant with no field forms its static box address before the loop.
func TestSelfHostArm64FrameSlotsAndHoistedBox(t *testing.T) {
	h := selfHostCLIForHost(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "frame_slots.fern")
	if err := os.WriteFile(src, []byte(arm64FrameSlotsProgram), 0o644); err != nil {
		t.Fatal(err)
	}
	moved := regexp.MustCompile(`(stp|ldp) x(19|2[0-8]), x\d+, \[sp`)
	for _, tg := range h.targets {
		if strings.HasPrefix(tg.target, "arm64") {
			asmPath := filepath.Join(dir, "frame_slots-"+tg.target+".s")
			h.compileWith(t, tg, src, asmPath, "-emit", "asm")
			asm, err := os.ReadFile(asmPath)
			if err != nil {
				t.Fatal(err)
			}
			weighted := functionListing(string(asm), "__fn_weighted")
			for _, want := range []string{"[sp, #-32]!", "[x29, #160]", "[x29, #176]", "[sp], #32"} {
				if !strings.Contains(weighted, want) {
					t.Errorf("%s: weighted has no %s:\n%s", tg.target, want, weighted)
				}
			}
			fn := functionListing(string(asm), "__fn_total")
			if moved.MatchString(fn) {
				t.Errorf("%s: total saves or restores a register through sp:\n%s", tg.target, fn)
			}
			for _, want := range []string{"stp x29, x30, [sp, #-64]!", ".cfi_def_cfa_offset 64", ".cfi_offset x29, -64", ".cfi_offset x30, -56", "stp x19, x20, [x29, #48]", "ldp x19, x20, [x29, #48]", "ldp x29, x30, [sp], #64"} {
				if !strings.Contains(fn, want) {
					t.Errorf("%s: total has no `%s`:\n%s", tg.target, want, fn)
				}
			}
			if strings.Contains(fn, "mov sp, x29") || strings.Contains(fn, "sub sp, sp, #48") {
				t.Errorf("%s: total adjusts sp separately from the frame record:\n%s", tg.target, fn)
			}
			boxes := 0
			for _, line := range strings.Split(fn, "\n") {
				if strings.Contains(line, "adrp") && strings.Contains(line, ".Kv") {
					boxes++
				}
			}
			loop := strings.Index(fn, "cmp x")
			if boxes != 1 || loop < 0 || strings.Index(fn, ".Kv") > loop {
				t.Errorf("%s: total forms the address of the static box %d times, want once and before the loop:\n%s", tg.target, boxes, fn)
			}
		}
		bin := filepath.Join(dir, "frame_slots-"+tg.target)
		h.compileWith(t, tg, src, bin)
		if _, code := h.runProduced(t, tg, bin); code != 180 {
			t.Errorf("%s: exit = %d, want 180", tg.target, code)
		}
	}
}
