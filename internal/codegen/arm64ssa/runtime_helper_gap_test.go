package arm64ssa_test

import (
	"testing"

	"github.com/jakechampion/lang/internal/ssa"
)

// __fern_heap_bump_bytes reports the arena's high-water mark, (cursor - base).
// It reads the reservation base _start now records, which the cursor alone
// cannot supply: mmap picks the base, so it is not a compile-time constant.
//
// The check is a delta rather than an absolute: alloc 4 KiB between two reads
// and require the second to exceed the first by at least that much (header and
// alignment can only add). An absolute assertion would pin the emitter's own
// prologue allocations, which is not the contract.
func TestArmRunHeapBumpBytes(t *testing.T) {
	const size = 4096
	f := ssa.NewFunc("main")
	e := f.NewBlock()
	before := wideCallOp(f, e, "__fern_heap_bump_bytes")
	f.AddOp(e, ssa.OpAlloc, constOp(f, e, size))
	after := wideCallOp(f, e, "__fern_heap_bump_bytes")
	grew := f.AddOp(e, ssa.OpSub, after, before)
	// ret (grew >= size)
	f.SetRet(e, f.AddOp(e, ssa.OpGe, grew, constOp(f, e, size)))
	if got := assembleRunArmModule(t, map[string]*ssa.Func{"main": f}, "main", 12); got != 1 {
		t.Errorf("heap_bump_bytes grew by at least %d = %d, want 1", size, got)
	}
}

// A program that only READS the bump cursor still needs the arena reserved:
// __fern_heap_bump_bytes is in heapUsingHelpers, so _start emits the mmap even
// with no allocating op in the body. Without that the helper would read an
// unwritten .bss cursor — or the section would not exist at all.
func TestArmRunHeapBumpBytesWithoutAllocating(t *testing.T) {
	f := ssa.NewFunc("main")
	e := f.NewBlock()
	// A reserved-but-unused arena has cursor == base, so the mark is 0.
	f.SetRet(e, wideCallOp(f, e, "__fern_heap_bump_bytes"))
	if got := assembleRunArmModule(t, map[string]*ssa.Func{"main": f}, "main", 12); got != 0 {
		t.Errorf("heap_bump_bytes with no allocation = %d, want 0", got)
	}
}

// monotonic_ns and now_unix_ms: the two clock_gettime readers. Both are checked
// against bounds rather than a value — a clock has no fixed answer — but the
// bounds are tight enough to catch the ways the helper can be wrong: a bad
// clock id or a failed syscall leaves the timespec zero, and a mis-materialised
// 1e9 / 1e6 multiplier lands orders of magnitude off.
func TestArmRunClockHelpers(t *testing.T) {
	// monotonic_ns is nonzero and non-decreasing across two reads.
	f := ssa.NewFunc("main")
	e := f.NewBlock()
	t0 := wideCallOp(f, e, "monotonic_ns")
	t1 := wideCallOp(f, e, "monotonic_ns")
	nonzero := f.AddOp(e, ssa.OpGt, t0, constOp(f, e, 0))
	ordered := f.AddOp(e, ssa.OpGe, t1, t0)
	f.SetRet(e, f.AddOp(e, ssa.OpAnd, nonzero, ordered))
	if got := assembleRunArmModule(t, map[string]*ssa.Func{"main": f}, "main", 12); got != 1 {
		t.Errorf("monotonic_ns nonzero and non-decreasing = %d, want 1", got)
	}

	// now_unix_ms lands in milliseconds since the epoch: after 2023-11-14 and
	// before 2096. Seconds would read ~1.7e9 (below the low bound) and
	// nanoseconds ~1.7e18 (above the high one), so the window pins the scale.
	g := ssa.NewFunc("main")
	b := g.NewBlock()
	ms := wideCallOp(g, b, "now_unix_ms")
	lo := g.AddOp(b, ssa.OpGt, ms, constOp(g, b, 1_700_000_000_000))
	hi := g.AddOp(b, ssa.OpLt, ms, constOp(g, b, 4_000_000_000_000))
	g.SetRet(b, g.AddOp(b, ssa.OpAnd, lo, hi))
	if got := assembleRunArmModule(t, map[string]*ssa.Func{"main": g}, "main", 12); got != 1 {
		t.Errorf("now_unix_ms in the millisecond window = %d, want 1", got)
	}
}

// sleep_ms actually sleeps, and a non-positive argument returns without a
// syscall. Both are read off monotonic_ns rather than the wall clock. The
// bounds are one-sided on purpose: nanosleep may overshoot by any amount the
// scheduler likes, so only the floor is a property of the helper.
func TestArmRunSleepMs(t *testing.T) {
	// elapsedBound builds main() = (monotonic_ns after sleep_ms(ms) - before)
	// compared against limit, and returns the native exit code.
	elapsedBound := func(ms int64, cmp ssa.OpKind, limit int64) int {
		f := ssa.NewFunc("main")
		e := f.NewBlock()
		before := wideCallOp(f, e, "monotonic_ns")
		callOp(f, e, "sleep_ms", constOp(f, e, ms)) // void; the result is unused
		elapsed := f.AddOp(e, ssa.OpSub, wideCallOp(f, e, "monotonic_ns"), before)
		f.SetRet(e, f.AddOp(e, cmp, elapsed, constOp(f, e, limit)))
		return assembleRunArmModule(t, map[string]*ssa.Func{"main": f}, "main", 12)
	}

	if got := elapsedBound(60, ssa.OpGe, 50_000_000); got != 1 {
		t.Errorf("sleep_ms(60): elapsed >= 50ms = %d, want 1", got)
	}
	// A non-positive argument must not reach nanosleep at all.
	for _, ms := range []int64{0, -5} {
		if got := elapsedBound(ms, ssa.OpLt, 1_000_000_000); got != 1 {
			t.Errorf("sleep_ms(%d): returned promptly = %d, want 1", ms, got)
		}
	}
}
