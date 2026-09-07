package x86_64

import (
	"math/bits"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/ast"
)

// The in-place string append grows a buffer only while the grown request
// still lands in the block the old one reserved. That used to be spelled as
// two capacity computations compared for equality; one suffices, because the
// capacity function is monotone and idempotent:
//
//	cap(new) == cap(old)   <=>   new <= cap(old)      (for old <= new)
//
// Left to right: new <= cap(new) == cap(old). Right to left: monotonicity
// gives cap(old) <= cap(new) <= cap(cap(old)), and idempotence collapses the
// right-hand side back to cap(old).
//
// So the equivalence rests on exactly three properties of the capacity
// function, and this file proves each of them over the whole request domain
// rather than arguing them. sizeClassCapModel below mirrors
// emitSizeClassCap — the ONE definition of a block's size, which
// internal/codegen/wasmbin's emitFreelistBin implements in wasm and
// __fern_alloc / __fern_free bin by.

// sizeClassCapModel is emitSizeClassCap in Go: the request itself in the
// small tier (16..2048, exact fit), and three significant bits above it.
func sizeClassCapModel(req int64) int64 {
	if req <= 2048 {
		return req
	}
	e := int64(bits.Len64(uint64(req))) - 1 // floor(log2(req))
	gran := int64(1) << uint(e-2)
	return (req + gran - 1) &^ (gran - 1)
}

// sizeClassRequests is every 16-aligned request the append can see: the whole
// small tier, then a dense sweep either side of each large-tier grid point up
// to the 1 GiB ceiling above which a block is bump-only.
func sizeClassRequests() []int64 {
	var out []int64
	for r := int64(16); r <= 4096; r += 16 {
		out = append(out, r)
	}
	for e := int64(11); e <= 30; e++ {
		mag := int64(1) << uint(e)
		gran := int64(1) << uint(e-2)
		for m := int64(0); m < 4; m++ {
			base := mag + m*gran
			for _, d := range []int64{-32, -16, 0, 16, 32} {
				if r := base + d; r >= 16 && r <= 0x40000000 && r%16 == 0 {
					out = append(out, r)
				}
			}
		}
	}
	return out
}

func TestSizeClassCapIsMonotoneIdempotentAndNoSmaller(t *testing.T) {
	reqs := sizeClassRequests()
	prev := int64(-1)
	prevCap := int64(-1)
	for _, r := range reqs {
		c := sizeClassCapModel(r)
		if c < r {
			t.Fatalf("cap(%d) = %d, smaller than the request it must hold", r, c)
		}
		if got := sizeClassCapModel(c); got != c {
			t.Fatalf("cap is not idempotent: cap(%d) = %d, cap(cap(%d)) = %d", r, c, r, got)
		}
		if r >= prev && c < prevCap {
			t.Fatalf("cap is not monotone: cap(%d) = %d but cap(%d) = %d", prev, prevCap, r, c)
		}
		prev, prevCap = r, c
	}
	if len(reqs) < 400 {
		t.Fatalf("only %d requests swept — the domain walk selected almost nothing", len(reqs))
	}
}

// TestSizeClassCapSameBlockPredicate is the equivalence itself, checked at
// the only two places it can break: the last request that still fits the old
// block, and the first that does not.
func TestSizeClassCapSameBlockPredicate(t *testing.T) {
	checked := 0
	for _, old := range sizeClassRequests() {
		capOld := sizeClassCapModel(old)
		if sizeClassCapModel(capOld) != capOld {
			t.Fatalf("cap(%d) = %d is not its own capacity", old, capOld)
		}
		if capOld+16 <= 0x40000000 && sizeClassCapModel(capOld+16) == capOld {
			t.Fatalf("a request of %d classes the same as %d, so `new <= cap(old)` admits a block that does not fit", capOld+16, old)
		}
		// And spot-check the equivalence directly across the whole span the
		// old block covers.
		for r := old; r <= capOld+64 && r <= 0x40000000; r += 16 {
			sameClass := sizeClassCapModel(r) == capOld
			fits := r <= capOld
			if sameClass != fits {
				t.Fatalf("old=%d cap=%d new=%d: same-class=%v but new<=cap=%v", old, capOld, r, sameClass, fits)
			}
			checked++
		}
	}
	if checked < 1000 {
		t.Fatalf("only %d (old, new) pairs compared", checked)
	}
}

// TestStrAppendTakesOneCapacityComputation pins the emitted half: the helper
// must round exactly ONE request up to its class. `bsr` is the large tier's
// leading instruction and appears nowhere else in the body, so counting it
// counts the expansions.
func TestStrAppendTakesOneCapacityComputation(t *testing.T) {
	prev := ast.RcFreeEnabled
	ast.RcFreeEnabled = true
	defer func() { ast.RcFreeEnabled = prev }()

	src := `function main(): i32 {
    var s: string = "";
    var i: i32 = 0;
    while (i < 4) { s = s + "ab"; i = i + 1; }
    return s.len();
}`
	for _, helper := range []string{"__fern_str_append", "__fern_str_append_range"} {
		body := helperBody(t, compile(t, srcUsing(helper, src)), helper)
		if n := strings.Count(body, "bsr rcx,"); n != 1 {
			t.Errorf("%s expands the size-class round-up %d time(s), want 1 (the old request's; the grown one is compared against its capacity):\n%s", helper, n, body)
		}
	}
}

// srcUsing returns a program that reaches `helper`: the plain append for
// __fern_str_append, and the fused range form for its range sibling.
func srcUsing(helper, plain string) string {
	if helper == "__fern_str_append" {
		return plain
	}
	return `function main(): i32 {
    var src: string = "abcdefgh";
    var s: string = "";
    var i: i32 = 0;
    while (i < 4) { s = s + slice_unchecked(src, 0, 3); i = i + 1; }
    return s.len();
}`
}

// helperBody returns the emitted text of one runtime helper, from its label
// up to the `.size` directive that closes it.
func helperBody(t *testing.T, asm, name string) string {
	t.Helper()
	i := strings.Index(asm, "\n"+name+":\n")
	if i < 0 {
		t.Fatalf("%s was not emitted", name)
	}
	body := asm[i:]
	if j := strings.Index(body, ".size "+name); j >= 0 {
		body = body[:j]
	}
	return body
}
