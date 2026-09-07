package arm64

import (
	"fmt"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/ast"
	"github.com/jakechampion/lang/internal/fernstring"
)

// strAppendSrc reaches every guard of both append helpers: a self-append that
// grows a local accumulator well past the inline cap, and the fused
// `acc + slice_unchecked(...)` form beside it.
const strAppendSrc = `function main(): i32 {
    var out: string = "";
    var src: string = "abcdefghijklmnopqrstuvwxyz";
    var i: i32 = 0;
    while (i < 40) {
        out = out + "abcdefgh";
        out = out + slice_unchecked(src, 0, 6);
        i = i + 1;
    }
    return out.len();
}`

// TestStrAppendDeclinesBelowTheInlineCap pins the append's fast path against a
// result that still fits the INLINE (data, len) form.
//
// The in-place grow keeps the accumulator's heap buffer and hands it back. For
// a total at or under the inline cap that is the WRONG answer twice over.
// __fern_strcat packs such a result into the pair with no allocation at all
// and the append's fallback then releases the accumulator's buffer, so the
// copy path is strictly cheaper — no allocation either way, one heap block
// fewer live afterwards. And keeping the buffer makes the append change the
// RESULT'S REPRESENTATION, which is what separates an optimisation of `a + b`
// from a second meaning for it: `slice_unchecked(s, 0, 3) + ""` came back as
// an inline pair from the plain concat and as a live heap buffer from the
// append, so a container that then held it stranded a block the inline value
// never had. arm64's 15-byte inline cap makes that a wide class of strings —
// every short number, key and tag a parser builds.
func TestStrAppendDeclinesBelowTheInlineCap(t *testing.T) {
	prev := ast.RcFreeEnabled
	ast.RcFreeEnabled = true
	defer func() { ast.RcFreeEnabled = prev }()

	asm := compile(t, strAppendSrc, Options{})
	want := fmt.Sprintf("cmp x9, #%d", fernstring.InlineCap(8))
	for _, sym := range []string{"__fern_str_append", "__fern_str_append_range"} {
		body := helperBody(asm, sym)
		if body == "" {
			t.Fatalf("%s was not emitted; cannot verify its inline-cap decline", sym)
		}
		if !strings.Contains(body, want) {
			t.Errorf("%s grows in place for a total that still fits the inline form: "+
				"want the fast path to decline at %q and fall through to __fern_strcat, "+
				"which packs the pair without allocating and releases the old buffer\n--- body ---\n%s",
				sym, want, body)
		}
	}
}

// TestStrAppendGuardsItsOwnBuffer pins the three tests that decide whether the
// accumulator may be grown at all. Each one alone is a use-after-free or a
// write into read-only memory:
//
//   - the inline-form tag on the len word — there is no buffer to grow;
//   - the below-heap floor, which is what refuses a .rodata literal whatever
//     the word at data-8 happens to read;
//   - rc == 1, without which a second reference observes the mutation.
func TestStrAppendGuardsItsOwnBuffer(t *testing.T) {
	prev := ast.RcFreeEnabled
	ast.RcFreeEnabled = true
	defer func() { ast.RcFreeEnabled = prev }()

	asm := compile(t, strAppendSrc, Options{})
	for _, sym := range []string{"__fern_str_append", "__fern_str_append_range"} {
		body := helperBody(asm, sym)
		if body == "" {
			t.Fatalf("%s was not emitted", sym)
		}
		for _, guard := range []struct{ insn, why string }{
			{"tbnz x20, #63", "an inline/SSO accumulator has no heap buffer to grow"},
			{"lsr x9, x19, #28", "a pointer below the heap floor is a .rodata literal"},
			{"cmp w10, #1", "a shared buffer must be copied, not mutated"},
		} {
			if !strings.Contains(body, guard.insn) {
				t.Errorf("%s is missing the %q guard: %s\n--- body ---\n%s", sym, guard.insn, guard.why, body)
			}
		}
		if !strings.Contains(body, "bl __fern_str_dec") {
			t.Errorf("%s must release the accumulator it consumed on the copy path\n--- body ---\n%s", sym, body)
		}
	}
}
