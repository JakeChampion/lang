package arm64ssa

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// emitToLines runs a helper emitter and returns its instruction lines, stripped
// of labels, blank lines and the leading tab.
func emitToLines(emit func(func(string, ...any))) []string {
	var out []string
	emit(func(format string, args ...any) {
		l := strings.TrimSpace(fmt.Sprintf(format, args...))
		if l == "" || strings.HasSuffix(l, ":") {
			return
		}
		out = append(out, l)
	})
	return out
}

// destRe matches an instruction's first (destination) operand.
var destRe = regexp.MustCompile(`^([a-z][a-z0-9.]*)\s+([wx][0-9]+|sp)`)

// __ssa_bcopy's callers keep live values in x3..x15 and stack only x30, so the
// routine may only write x0, x1, x2 and x16 / x17 (IP0 / IP1, which a veneer
// already clobbers across any call). A store writes memory, not its first
// operand, so those are exempt; ldp / stp name a second destination too.
func TestBcopyClobbersOnlyItsArgumentsAndTheScratchPair(t *testing.T) {
	allowed := map[string]bool{"x0": true, "x1": true, "x2": true, "x16": true, "x17": true, "w16": true, "w17": true}
	for _, l := range emitToLines(emitBcopy) {
		m := destRe.FindStringSubmatch(l)
		if m == nil {
			continue
		}
		mnem, dst := m[1], m[2]
		if strings.HasPrefix(mnem, "st") || strings.HasPrefix(mnem, "b") || mnem == "cmp" || mnem == "cbz" || mnem == "ret" {
			continue
		}
		regs := []string{dst}
		if mnem == "ldp" {
			regs = append(regs, strings.TrimSuffix(strings.Fields(l)[2], ","))
		}
		for _, r := range regs {
			if !allowed[r] {
				t.Errorf("%q writes %s, outside __ssa_bcopy's clobber set", l, r)
			}
		}
	}
}

// A post-increment store into a byte register inside a loop is the shape that
// cost five instructions per byte. Every bulk copy routes through __ssa_bcopy
// now, so only the routine's own residue tail may carry one.
func TestBulkCopyHelpersHaveNoPerByteLoop(t *testing.T) {
	for name := range bcopyUsingHelpers {
		emit, ok := runtimeHelperEmitters[name]
		if !ok {
			t.Fatalf("%s is listed as calling __ssa_bcopy but has no emitter", name)
		}
		lines := emitToLines(emit)
		body := strings.Join(lines, "\n")
		if !strings.Contains(body, "bl "+bcopySym) {
			t.Errorf("%s is listed as calling %s but does not", name, bcopySym)
		}
		for _, l := range lines {
			if strings.HasPrefix(l, "strb") {
				t.Errorf("%s still copies a byte at a time: %q", name, l)
			}
		}
	}
}

// The routine is emitted exactly when a helper that calls it is referenced —
// otherwise the call sites branch to a symbol that was never written out.
func TestBcopyGateTracksItsCallers(t *testing.T) {
	if usesBcopy([]string{"__fern_rc_dec"}) {
		t.Error("usesBcopy is true for a helper that does not copy")
	}
	if !usesBcopy([]string{"__fern_rc_dec", "__str_concat"}) {
		t.Error("usesBcopy is false with __str_concat referenced")
	}
}

// The argument moves have to survive an argument that already sits in one of
// the registers a later move overwrites.
func TestBcopyCallMovesArgumentsWithoutLosingOne(t *testing.T) {
	// dst=x9, src=x0, n=x2: x2 is already in place, and x1 must take x0's
	// value before x0 is overwritten with x9.
	want := []string{"mov x1, x0", "mov x0, x9", "stp x29, x30, [sp, #-16]!", "bl " + bcopySym, "ldp x29, x30, [sp], #16"}
	got := bcopyCallLines("x9", "x0", "x2")
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("got:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

// stepRe matches a post-increment access or the counter decrement, capturing
// the amount each advances by.
var stepRe = regexp.MustCompile(`^(ldp|stp|ldr|str|ldrb|strb)\s.*\],\s*#(\d+)$|^(subs?) x2, x2, #(\d+)$`)

// Each of __ssa_bcopy's three steps advances the source, the destination and
// the remaining count by the same amount. A step whose counter falls behind its
// pointers over-copies past the end of the destination — into the next
// allocation's header on a bump heap, where nothing reading the copy back can
// see it, so no test over the copied bytes catches it.
func TestBcopyStepsAdvanceSourceDestinationAndCountTogether(t *testing.T) {
	var group []int
	flush := func() {
		if len(group) == 0 {
			return
		}
		for _, n := range group {
			if n != group[0] {
				t.Errorf("__ssa_bcopy step advances by %v, want all three equal", group)
				break
			}
		}
		if len(group) != 3 {
			t.Errorf("__ssa_bcopy step has %d advancing instructions %v, want 3 (load, store, count)", len(group), group)
		}
		group = nil
	}
	for _, l := range emitToLines(emitBcopy) {
		m := stepRe.FindStringSubmatch(l)
		if m == nil {
			continue
		}
		amt := m[2] + m[4] // exactly one alternative captured
		n, err := strconv.Atoi(amt)
		if err != nil {
			t.Fatalf("step amount %q in %q: %v", amt, l, err)
		}
		group = append(group, n)
		if m[3] != "" { // the counter decrement closes the step
			flush()
		}
	}
	flush()
}
