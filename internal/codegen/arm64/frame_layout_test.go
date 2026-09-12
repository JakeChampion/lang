package arm64

// Frame-slot layout tests. Only `ldur` / `stur` reach -256..255 from x29 in one
// instruction; every frame access past that costs a second instruction to
// materialise the address. Two things have to hold: the near window goes to the
// slots that use it most, and no emitted `ldur` / `stur` ever carries an offset
// the assembler cannot encode.

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

func TestFrameSlotOrder(t *testing.T) {
	cases := []struct {
		name     string
		accesses []int
		nParams  int
		want     []int
	}{
		{"empty", nil, 0, []int{}},
		{"all locals, descending", []int{1, 9, 3}, 0, []int{1, 2, 0}},
		{"ties keep slot order", []int{5, 5, 5}, 0, []int{0, 1, 2}},
		// Params sort among themselves and stay ahead of every body local,
		// however cold: slot 1 outranks slot 0, but neither yields to slot 2.
		{"params stay ahead", []int{1, 4, 99}, 2, []int{1, 0, 2}},
		{"locals sort behind params", []int{7, 1, 8}, 1, []int{0, 2, 1}},
		{"nParams past the end", []int{1, 2}, 9, []int{1, 0}},
		{"negative nParams", []int{1, 2}, -1, []int{1, 0}},
	}
	for _, c := range cases {
		got := frameSlotOrder(c.accesses, c.nParams)
		if len(got) != len(c.want) {
			t.Errorf("%s: frameSlotOrder = %v, want %v", c.name, got, c.want)
			continue
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Errorf("%s: frameSlotOrder = %v, want %v", c.name, got, c.want)
				break
			}
		}
	}
}

// frameSlotOrder is a permutation whatever the input: losing or duplicating a
// slot would silently alias two locals onto one frame offset.
func TestFrameSlotOrderIsAPermutation(t *testing.T) {
	accesses := []int{3, 0, 3, 12, 1, 12, 7, 0, 4, 4, 9, 2}
	for nParams := 0; nParams <= len(accesses); nParams++ {
		seen := make([]bool, len(accesses))
		for _, slot := range frameSlotOrder(accesses, nParams) {
			if slot < 0 || slot >= len(accesses) || seen[slot] {
				t.Fatalf("nParams=%d: frameSlotOrder is not a permutation (slot %d)", nParams, slot)
			}
			seen[slot] = true
		}
	}
}

// unscaledOff matches every emitted `ldur` / `stur`, whose signed 9-bit
// immediate reaches only -256..255.
var unscaledOff = regexp.MustCompile(`\t(?:ldur|stur) [wx][0-9a-z]+, \[x29, #(-?\d+)\]`)

// manyParams builds a function with n i64 parameters that reads each one, so
// the prologue has to spill parameters into slots past the unscaled range. The
// recursive call is what keeps it a function: inlined into main its parameters
// become ordinary locals and the prologue spill under test never runs.
func manyParams(n int) string {
	var params, args, body, tail strings.Builder
	for i := 0; i < n; i++ {
		if i > 0 {
			params.WriteString(", ")
			args.WriteString(", ")
			body.WriteString(" + ")
			tail.WriteString(", ")
			fmt.Fprintf(&tail, "p%d", i)
		}
		fmt.Fprintf(&params, "p%d: i64", i)
		fmt.Fprintf(&args, "%d", i)
		fmt.Fprintf(&body, "p%d", i)
	}
	return fmt.Sprintf(`
function wide(%s): i64 {
    if (p0 > 100) { return wide(p0 - 1%s); }
    return %s;
}
function main(): i32 { return wide(%s) as i32; }`,
		params.String(), tail.String(), body.String(), args.String())
}

// A parameter whose slot falls past the unscaled range must spill through the
// same address materialisation frameLoad and frameStore use. Emitting a bare
// `stur` there fails to assemble ("offset out of signed 9-bit range"), so this
// asserts the encodable range directly rather than the instruction chosen.
func TestEveryUnscaledFrameOffsetIsEncodable(t *testing.T) {
	for _, src := range []string{manyParams(40), manyParams(64)} {
		asm := compile(t, src, Options{})
		fn := functionBody(t, asm, "__fn_wide")
		matches := unscaledOff.FindAllStringSubmatch(fn, -1)
		if len(matches) == 0 {
			t.Fatalf("expected some unscaled frame accesses; asm:\n%s", fn)
		}
		for _, m := range matches {
			off, err := strconv.Atoi(m[1])
			if err != nil {
				t.Fatalf("unparsable offset %q", m[1])
			}
			if off < -256 || off > 255 {
				t.Errorf("ldur/stur offset %d is outside the signed 9-bit range: %s", off, m[0])
			}
		}
	}
}

// hotLateLocal declares `cold` locals that are written once, then loop
// variables that are read and written repeatedly. Laid out in index order the
// loop variables land past the unscaled range and every access to them costs an
// extra `sub`; laid out by use they take the near window instead.
func hotLateLocal(cold int) string {
	var decls, sum strings.Builder
	for i := 0; i < cold; i++ {
		fmt.Fprintf(&decls, "    var c%d: i64 = %d;\n", i, i)
		fmt.Fprintf(&sum, " + c%d", i)
	}
	return fmt.Sprintf(`
function hot(): i64 {
%s    var acc: i64 = 0;
    var i: i64 = 0;
    while (i < 1000) { acc = acc + i; i = i + 1; }
    return acc%s;
}
function main(): i32 { return hot() as i32; }`, decls.String(), sum.String())
}

// The loop variables are read and written far more than the write-once locals
// declared ahead of them, so they must take the near window. Index-order layout
// would put acc and i at slots 60 and 61 and materialise an address for every
// one of their loop accesses; use-ordered, none of them does.
func TestHotLocalTakesTheNearWindow(t *testing.T) {
	const cold = 60
	asm := compile(t, hotLateLocal(cold), Options{})
	fn := functionBody(t, asm, "__fn_hot")
	if !strings.Contains(fn, "ldur") {
		t.Fatalf("expected unscaled frame accesses in hot(); asm:\n%s", fn)
	}
	// The cold locals are each touched twice (one write, one read in the final
	// sum) and most of them must fall outside the window, so some
	// materialisation is expected; what may not survive is one per loop access.
	if n := strings.Count(fn, "sub x16, x29, #"); n > 2*cold {
		t.Errorf("hot(): %d frame-address materialisations, more than the %d cold-local accesses alone: the hot loop slots did not take the unscaled window; asm:\n%s", n, 2*cold, fn)
	}
}

// functionBody returns the emitted text of one function.
func functionBody(t *testing.T, asm, sym string) string {
	t.Helper()
	start := strings.Index(asm, "\n"+sym+":")
	if start < 0 {
		t.Fatalf("no %s in asm:\n%s", sym, asm)
	}
	rest := asm[start+1:]
	if end := strings.Index(rest, "\n.size"); end >= 0 {
		return rest[:end]
	}
	return rest
}
